package fragments

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// A Biome override is written in the manifest in Biome's own shape, so a
// project's existing override moves across unchanged:
//
//	[[web.biome_overrides]]
//	includes = ["core/**"]
//	[web.biome_overrides.linter.rules.style.noRestrictedImports]
//	level = "error"
//	options = { patterns = [{ group = ["node:*"], message = "..." }] }
//
// What it may say is much narrower than what Biome accepts:
//
//   - only `includes` and `linter.rules`. No formatter, no `linter.enabled`, no
//     `files`, no assist: each of those can switch a profile check off for a
//     path, and the formatter is what one of the doctor probes asserts.
//   - every rule's level is "error". An override exists to ENFORCE something on
//     a path; "off", "info" and "warn" all lower a rule somewhere, and a group's
//     `recommended = false` or the rules' `preset` lowers dozens at once.
//   - `options` only on the noRestricted* rules, whose options can only forbid
//     more. Options on any other rule can widen it (an allow-list, a threshold).
//   - no rule the profile's biome.json names, and no rule a doctor probe
//     asserts. The profile owns those settings in both directions.
//
// Because nothing an override can say lowers a rule, includes may be as wide as
// the project likes: `**` with an extra error rule is a fleet-legal tightening.
type BiomeOverride = map[string]any

var biomeGroups = []string{"a11y", "complexity", "correctness", "nursery", "performance", "security", "style", "suspicious"}

// Rules whose options list what is FORBIDDEN, so options can only tighten them.
var biomeRestrictRules = []string{"noRestrictedGlobals", "noRestrictedImports", "noRestrictedTypes"}

// ValidateBiomeOverrides checks the overrides' shape, with no profile in hand.
// config.Load calls it, so a malformed override fails every command.
func ValidateBiomeOverrides(overrides []BiomeOverride) error {
	for i, o := range overrides {
		where := fmt.Sprintf("[[web.biome_overrides]] #%d", i+1)
		for _, k := range sortedKeys(o) {
			if k != "includes" && k != "linter" {
				return fmt.Errorf("%s sets %q; an override may only set includes and linter.rules, because every other key "+
					"(formatter, files, assist, javascript, ...) can switch a profile check off for a path", where, k)
			}
		}
		includes, ok := o["includes"].([]any)
		if !ok || len(includes) == 0 {
			return fmt.Errorf("%s needs includes, a nonempty list of paths the override applies to", where)
		}
		for _, p := range includes {
			s, ok := p.(string)
			if !ok || strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n") {
				return fmt.Errorf("%s has an include that is not a nonempty one-line path pattern: %v", where, p)
			}
		}
		linter, ok := o["linter"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s needs linter.rules; an override that sets no rule does nothing", where)
		}
		for _, k := range sortedKeys(linter) {
			if k != "rules" {
				return fmt.Errorf("%s sets linter.%s; an override may only set linter.rules (linter.enabled = false would stop every rule on the path)", where, k)
			}
		}
		rules, ok := linter["rules"].(map[string]any)
		if !ok || len(rules) == 0 {
			return fmt.Errorf("%s needs linter.rules, with at least one rule", where)
		}
		for _, group := range sortedKeys(rules) {
			if !slices.Contains(biomeGroups, group) {
				return fmt.Errorf("%s sets linter.rules.%s, which is not a rule group (one of %s); preset and recommended would change "+
					"every rule at once", where, group, strings.Join(biomeGroups, ", "))
			}
			byRule, ok := rules[group].(map[string]any)
			if !ok || len(byRule) == 0 {
				return fmt.Errorf("%s: linter.rules.%s must be a table of rules", where, group)
			}
			for _, rule := range sortedKeys(byRule) {
				if err := validateBiomeRule(byRule[rule], rule); err != nil {
					return fmt.Errorf("%s: linter.rules.%s.%s %v", where, group, rule, err)
				}
			}
		}
	}
	return nil
}

func validateBiomeRule(v any, rule string) error {
	if rule == "recommended" || rule == "all" {
		return fmt.Errorf("would change every rule in the group; name the rule you mean to enforce")
	}
	if s, ok := v.(string); ok {
		if s != "error" {
			return fmt.Errorf("is %q; an override may only raise a rule to \"error\", never lower one", s)
		}
		return nil
	}
	t, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("must be \"error\" or a table with level = \"error\"")
	}
	for _, k := range sortedKeys(t) {
		if k != "level" && k != "options" {
			return fmt.Errorf("sets %q; a rule takes only level and options here", k)
		}
	}
	if t["level"] != "error" {
		return fmt.Errorf("has level %v; an override may only raise a rule to \"error\", never lower one", t["level"])
	}
	if _, has := t["options"]; has && !slices.Contains(biomeRestrictRules, rule) {
		return fmt.Errorf("sets options; options are accepted only on %s, whose options can only forbid more. "+
			"Options on any other rule can widen it", strings.Join(biomeRestrictRules, ", "))
	}
	return nil
}

// CheckBiomeOverrides refuses any override that names a rule the profile's
// biome.json sets (top-level or in its own overrides) or that a doctor probe
// asserts.
func CheckBiomeOverrides(profile []byte, probed []string, overrides []BiomeOverride) error {
	if err := ValidateBiomeOverrides(overrides); err != nil {
		return err
	}
	if len(overrides) == 0 {
		return nil
	}
	owned, err := biomeProfileRules(profile)
	if err != nil {
		return err
	}
	for _, p := range probed {
		owned[p] = "is asserted by a doctor probe"
	}
	for i, o := range overrides {
		rules := o["linter"].(map[string]any)["rules"].(map[string]any)
		for _, group := range sortedKeys(rules) {
			for _, rule := range sortedKeys(rules[group].(map[string]any)) {
				if why, ok := owned[group+"/"+rule]; ok {
					return fmt.Errorf("[[web.biome_overrides]] #%d: %s/%s %s; a project override may not restate or change it. "+
						"The profile owns that rule's setting in both directions", i+1, group, rule, why)
				}
			}
		}
	}
	return nil
}

// biomeProfileRules maps "<group>/<rule>" for every rule the shipped biome.json
// sets, top-level or in an override.
func biomeProfileRules(profile []byte) (map[string]string, error) {
	type rulesDoc struct {
		Linter struct {
			Rules map[string]json.RawMessage `json:"rules"`
		} `json:"linter"`
	}
	var doc struct {
		rulesDoc
		Overrides []rulesDoc `json:"overrides"`
	}
	if err := json.Unmarshal(profile, &doc); err != nil {
		return nil, fmt.Errorf("parse the shipped biome.json: %w", err)
	}
	owned := map[string]string{}
	add := func(rules map[string]json.RawMessage, how string) {
		for group, raw := range rules {
			var byRule map[string]json.RawMessage
			if json.Unmarshal(raw, &byRule) != nil {
				continue // "preset": "recommended" is a string, not a group
			}
			for rule := range byRule {
				owned[group+"/"+rule] = how
			}
		}
	}
	add(doc.Linter.Rules, "is set by the profile's biome.json")
	for _, o := range doc.Overrides {
		add(o.Linter.Rules, "is set by an override in the profile's biome.json")
	}
	if len(owned) == 0 {
		return nil, fmt.Errorf("the shipped biome.json sets no rule, so nothing would stop an override restating one")
	}
	return owned, nil
}

// RenderBiomeOverrides returns the shipped biome.json with the project's
// overrides appended to its overrides array. With none it returns body
// untouched, byte for byte. The appended JSON is laid out the way `biome
// format` lays it out, because biome.json is itself a file `biome ci` checks.
func RenderBiomeOverrides(body []byte, probed []string, overrides []BiomeOverride) ([]byte, error) {
	if len(overrides) == 0 {
		return body, nil
	}
	if err := CheckBiomeOverrides(body, probed, overrides); err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	raw, ok := doc["overrides"]
	if !ok {
		return nil, fmt.Errorf("the shipped biome.json has no overrides array to add project overrides to")
	}
	var existing []json.RawMessage
	if err := json.Unmarshal(raw, &existing); err != nil {
		return nil, fmt.Errorf("the shipped biome.json's overrides is not an array: %w", err)
	}
	var add bytes.Buffer
	for _, o := range overrides {
		if len(existing) > 0 || add.Len() > 0 {
			add.WriteString(",")
		}
		add.WriteString("\n    ")
		writeJSON(&add, o, 4, 4)
	}
	end := bytes.LastIndexByte(raw, ']')
	updated := append([]byte{}, bytes.TrimRight(raw[:end], " \r\n\t")...)
	updated = append(updated, add.Bytes()...)
	updated = append(updated, []byte("\n  ]")...)
	if bytes.Count(body, raw) != 1 {
		return nil, fmt.Errorf("the shipped biome.json's overrides array is not unique in the file")
	}
	return bytes.Replace(body, raw, updated, 1), nil
}

const biomeLineWidth = 100

// writeJSON writes v as Biome's formatter would: objects always expanded, an
// array of scalars on one line when it fits the line width, otherwise one
// element per line. col is the column v starts at; indent is the current
// nesting indent.
func writeJSON(b *bytes.Buffer, v any, indent, col int) {
	pad := strings.Repeat(" ", indent)
	switch t := v.(type) {
	case map[string]any:
		keys := sortedKeys(t)
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n" + pad + "  ")
			key := quote(k) + ": "
			b.WriteString(key)
			writeJSON(b, t[k], indent+2, indent+2+len(key))
		}
		b.WriteString("\n" + pad + "}")
	case []map[string]any:
		items := make([]any, len(t))
		for i := range t {
			items[i] = t[i]
		}
		writeJSON(b, items, indent, col)
	case []any:
		if inline, ok := inlineArray(t); ok && col+len(inline)+1 <= biomeLineWidth {
			b.WriteString(inline)
			return
		}
		b.WriteString("[")
		for i, e := range t {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n" + pad + "  ")
			writeJSON(b, e, indent+2, indent+2)
		}
		b.WriteString("\n" + pad + "]")
	case string:
		b.WriteString(quote(t))
	default:
		enc, _ := json.Marshal(t) // TOML scalars: int64, float64, bool
		b.Write(enc)
	}
}

func inlineArray(a []any) (string, bool) {
	parts := make([]string, len(a))
	for i, e := range a {
		switch t := e.(type) {
		case string:
			parts[i] = quote(t)
		case map[string]any, []any, []map[string]any:
			return "", false
		default:
			enc, _ := json.Marshal(t)
			parts[i] = string(enc)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]", true
}

// BiomeOverrideRules lists "<group>/<rule>" for every rule an override sets,
// sorted. The doctor uses it to find each declared rule in the synced file.
func BiomeOverrideRules(o BiomeOverride) []string {
	var out []string
	linter, _ := o["linter"].(map[string]any)
	rules, _ := linter["rules"].(map[string]any)
	for group, v := range rules {
		byRule, _ := v.(map[string]any)
		for rule := range byRule {
			out = append(out, group+"/"+rule)
		}
	}
	sort.Strings(out)
	return out
}
