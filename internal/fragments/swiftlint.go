package fragments

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SwiftLintRule is one project-owned SwiftLint custom rule, declared in the
// manifest as [ios.swiftlint_custom_rules.<id>]. The fields are SwiftLint's own
// custom-rule keys, so a rule moves between the two files unchanged.
//
// The set is closed on purpose. A custom rule can only report violations of its
// own regex: nothing in it can disable, reconfigure or narrow another rule. That
// is the whole reason the fragment is shaped as custom rules and not as a free
// SwiftLint child config, where `disabled_rules`, `only_rules`, `excluded` or a
// rule's own key would each silently loosen the profile (each measured on
// SwiftLint 0.65.1).
type SwiftLintRule struct {
	Name               string   `toml:"name"`
	Regex              string   `toml:"regex"`
	Message            string   `toml:"message"`
	Severity           string   `toml:"severity"`
	Included           []string `toml:"included"`
	Excluded           []string `toml:"excluded"`
	MatchKinds         []string `toml:"match_kinds"`
	ExcludedMatchKinds []string `toml:"excluded_match_kinds"`
}

var swiftLintRuleID = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidateSwiftLintRules checks the rules' own shape, with no profile in hand.
// config.Load calls it, so a malformed rule fails every command, not only sync.
func ValidateSwiftLintRules(rules map[string]SwiftLintRule) error {
	for _, id := range sortedKeys(rules) {
		r := rules[id]
		where := fmt.Sprintf("[ios.swiftlint_custom_rules.%s]", id)
		if !swiftLintRuleID.MatchString(id) {
			return fmt.Errorf("%s: a custom rule id must be lowercase snake_case, like SwiftLint's own", where)
		}
		if strings.TrimSpace(r.Regex) == "" {
			return fmt.Errorf("%s needs a regex; a custom rule with none matches nothing and reads as enforced", where)
		}
		if r.Severity != "" && r.Severity != "warning" && r.Severity != "error" {
			return fmt.Errorf("%s has severity %q; SwiftLint accepts warning or error", where, r.Severity)
		}
		if len(r.MatchKinds) > 0 && len(r.ExcludedMatchKinds) > 0 {
			return fmt.Errorf("%s sets both match_kinds and excluded_match_kinds; SwiftLint refuses the pair", where)
		}
		for _, list := range [][]string{r.Included, r.Excluded, r.MatchKinds, r.ExcludedMatchKinds} {
			for _, v := range list {
				if strings.TrimSpace(v) == "" {
					return fmt.Errorf("%s has an empty entry in a list", where)
				}
			}
		}
	}
	return nil
}

// CheckSwiftLintRules refuses any project rule whose id the profile already
// uses, or that a doctor probe asserts. A custom rule with a profile custom
// rule's id REPLACES it: a project `no_print_statements` whose regex never
// matches silently ends the profile's rule (measured). An id the profile names
// anywhere else, or that a probe names, is refused for the same reason in
// advance: the profile owns that name.
func CheckSwiftLintRules(profile []byte, probed []string, rules map[string]SwiftLintRule) error {
	if err := ValidateSwiftLintRules(rules); err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	owned, err := swiftLintProfileRules(profile)
	if err != nil {
		return err
	}
	for _, p := range probed {
		owned[p] = "is asserted by a doctor probe"
	}
	for _, id := range sortedKeys(rules) {
		if why, ok := owned[id]; ok {
			return fmt.Errorf("[ios.swiftlint_custom_rules.%s]: %q %s, so a project rule of that name would replace or shadow it; "+
				"a project fragment may only ADD rules. Pick an id of your own", id, id, why)
		}
	}
	return nil
}

// swiftLintProfileRules maps every rule id the shipped .swiftlint.yml names to
// how it names it.
func swiftLintProfileRules(profile []byte) (map[string]string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(profile, &doc); err != nil {
		return nil, fmt.Errorf("parse the shipped .swiftlint.yml: %w", err)
	}
	owned := map[string]string{}
	for key := range doc {
		owned[key] = "is configured by the profile"
	}
	for _, list := range []string{"opt_in_rules", "disabled_rules", "only_rules", "analyzer_rules"} {
		entries, _ := doc[list].([]any)
		for _, e := range entries {
			if s, ok := e.(string); ok {
				owned[s] = "is in the profile's " + list
			}
		}
	}
	custom, ok := doc["custom_rules"].(map[string]any)
	if !ok || len(custom) == 0 {
		return nil, fmt.Errorf("the shipped .swiftlint.yml has no custom_rules block to add project rules to")
	}
	for id := range custom {
		owned[id] = "is a profile custom rule"
	}
	return owned, nil
}

// RenderSwiftLint returns the shipped .swiftlint.yml with the project's custom
// rules appended to its custom_rules block. With no rules it returns body
// untouched, byte for byte.
//
// It edits text rather than re-serialising the document so that every comment
// in the shipped file — most of them are the reason a rule is set the way it is
// — survives into the project.
func RenderSwiftLint(body []byte, probed []string, rules map[string]SwiftLintRule) ([]byte, error) {
	if len(rules) == 0 {
		return body, nil
	}
	if err := CheckSwiftLintRules(body, probed, rules); err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(body), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, "\r\n") == "custom_rules:" {
			if start >= 0 {
				return nil, fmt.Errorf("the shipped .swiftlint.yml has two custom_rules blocks")
			}
			start = i
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("the shipped .swiftlint.yml has no top-level custom_rules: line to add project rules under")
	}
	// The block runs until the next top-level key. Comments at column 0 after
	// it (the file ends with one) belong to no rule, so the insertion goes after
	// the last indented line, not before the next top-level line.
	last := start
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		if l == "" || strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' {
			if l[0] == '#' {
				continue
			}
			break
		}
		last = i
	}
	var b bytes.Buffer
	b.WriteString("\n  # Project rules, from [ios.swiftlint_custom_rules] in .lacquer.toml. lacquer\n")
	b.WriteString("  # renders them here and refuses any that would replace a profile rule.\n")
	for _, id := range sortedKeys(rules) {
		r := rules[id]
		fmt.Fprintf(&b, "  %s:\n", id)
		scalar := func(k, v string) {
			if v != "" {
				fmt.Fprintf(&b, "    %s: %s\n", k, quote(v))
			}
		}
		list := func(k string, vs []string) {
			if len(vs) == 0 {
				return
			}
			fmt.Fprintf(&b, "    %s:\n", k)
			for _, v := range vs {
				fmt.Fprintf(&b, "      - %s\n", quote(v))
			}
		}
		scalar("name", r.Name)
		scalar("regex", r.Regex)
		scalar("message", r.Message)
		scalar("severity", r.Severity)
		list("included", r.Included)
		list("excluded", r.Excluded)
		list("match_kinds", r.MatchKinds)
		list("excluded_match_kinds", r.ExcludedMatchKinds)
	}
	out := strings.Join(lines[:last+1], "")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += b.String() + strings.Join(lines[last+1:], "")
	return []byte(out), nil
}

// quote renders a YAML double-quoted scalar. A JSON string is one: YAML 1.2's
// double-quoted escapes are a superset of JSON's.
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return strings.TrimRight(b.String(), "\n")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
