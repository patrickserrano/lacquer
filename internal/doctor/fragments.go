package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/fragments"
	"github.com/patrickserrano/lacquer/internal/safepath"
	"gopkg.in/yaml.v3"
)

// checkFragments proves two things about a project-owned fragment of a managed
// config (see internal/fragments): that the guard refusing a fragment which
// would switch off a profile rule still refuses one, planted against the synced
// file; and that every fragment the manifest declares reached that file.
//
// The planted control comes first and runs whether or not the project declares
// anything. A guard that had stopped refusing would otherwise be invisible on
// exactly the projects that rely on it, because their fragments are the ones it
// passes.
func checkFragments(check, root, component string) error {
	cfg, err := config.Load(filepath.Join(root, ".lacquer.toml"))
	if err != nil {
		return err
	}
	read := func(name string) ([]byte, error) {
		rel, err := filepath.Rel(root, filepath.Join(component, name))
		if err != nil {
			return nil, err
		}
		path, err := safepath.Resolve(root, rel)
		if err != nil {
			return nil, err
		}
		return os.ReadFile(path)
	}
	switch check {
	case "swiftlint-custom-rules":
		data, err := read(".swiftlint.yml")
		if err != nil {
			return err
		}
		return checkSwiftLintCustomRules(data, cfg.IOS.SwiftLintCustomRules)
	case "biome-overrides":
		data, err := read("biome.json")
		if err != nil {
			return err
		}
		return checkBiomeOverrides(data, cfg.Web.BiomeOverrides)
	default: // typedoc-entry-points
		data, err := read("typedoc.json")
		if err != nil {
			return err
		}
		return checkTypeDocEntryPoints(data, cfg.Web.TypeDocEntryPoints, cfg.Web.TypeDocEntryPointStrategy)
	}
}

func checkSwiftLintCustomRules(synced []byte, declared map[string]fragments.SwiftLintRule) error {
	var doc struct {
		CustomRules map[string]map[string]any `yaml:"custom_rules"`
	}
	if err := yaml.Unmarshal(synced, &doc); err != nil {
		return fmt.Errorf(".swiftlint.yml does not parse: %w", err)
	}
	// Control: a project rule reusing a PROFILE custom rule's id, with a regex
	// that never matches, is how a fragment would silently end that rule.
	var profileRule string
	for _, id := range sortedIDs(doc.CustomRules) {
		if _, own := declared[id]; !own {
			profileRule = id
			break
		}
	}
	if profileRule == "" {
		return fmt.Errorf(".swiftlint.yml has no profile custom rule to plant a control against")
	}
	planted := map[string]fragments.SwiftLintRule{profileRule: {Regex: "DOCTOR_NEVER_MATCHES"}}
	if fragments.CheckSwiftLintRules(synced, nil, planted) == nil {
		return fmt.Errorf("the fragment guard accepted a project rule replacing the profile's %s, so a fragment could switch it off", profileRule)
	}
	for _, id := range sortedIDs(declared) {
		got, ok := doc.CustomRules[id]
		if !ok || got["regex"] != declared[id].Regex {
			return fmt.Errorf(".swiftlint.yml is missing declared project rule %s (or carries a different regex); re-sync", id)
		}
	}
	return nil
}

func checkBiomeOverrides(synced []byte, declared []fragments.BiomeOverride) error {
	var doc struct {
		Linter struct {
			Rules map[string]json.RawMessage `json:"rules"`
		} `json:"linter"`
		Overrides []any `json:"overrides"`
	}
	if err := json.Unmarshal(synced, &doc); err != nil {
		return fmt.Errorf("biome.json does not parse: %w", err)
	}
	// Controls: switching a rule off for every path, and restating a rule the
	// profile sets at "error" (which would let options or a later edit change
	// it), must both be refused.
	var group, rule string
	for _, g := range sortedIDs(doc.Linter.Rules) {
		var byRule map[string]json.RawMessage
		if json.Unmarshal(doc.Linter.Rules[g], &byRule) == nil && len(byRule) > 0 {
			group, rule = g, sortedIDs(byRule)[0]
			break
		}
	}
	if rule == "" {
		return fmt.Errorf("biome.json sets no rule to plant a control against")
	}
	for _, level := range []string{"off", "error"} {
		planted := []fragments.BiomeOverride{{
			"includes": []any{"**"},
			"linter":   map[string]any{"rules": map[string]any{group: map[string]any{rule: level}}},
		}}
		if fragments.CheckBiomeOverrides(synced, nil, planted) == nil {
			return fmt.Errorf("the fragment guard accepted an override setting the profile's %s/%s to %q for every path", group, rule, level)
		}
	}
	for i, o := range declared {
		want, err := normalise(o)
		if err != nil {
			return err
		}
		found := false
		for _, got := range doc.Overrides {
			if reflect.DeepEqual(got, want) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("biome.json is missing declared override #%d (%v); re-sync", i+1, fragments.BiomeOverrideRules(o))
		}
	}
	return nil
}

func checkTypeDocEntryPoints(synced []byte, declared []string, strategy string) error {
	if fragments.ValidateTypeDocEntryPoints([]string{"../outside/index.ts"}) == nil {
		return fmt.Errorf("the entry-point guard accepted a path outside the component")
	}
	if fragments.ValidateTypeDoc([]string{"src"}, "packages") == nil {
		return fmt.Errorf("the strategy guard accepted \"packages\", which does not apply the docs validation per package")
	}
	want := typeDocKeys{EntryPoints: declared, EntryPointStrategy: strategy, Exclude: fragments.TypeDocExcludes(declared, strategy)}
	if len(want.EntryPoints) == 0 {
		want.EntryPoints = []string{"src/index.ts"}
	}
	got, err := readTypeDocKeys(synced)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(got.EntryPoints, want.EntryPoints) {
		return fmt.Errorf("typedoc.json's entryPoints are %q, want %q; re-sync", got.EntryPoints, want.EntryPoints)
	}
	if got.EntryPointStrategy != want.EntryPointStrategy {
		return fmt.Errorf("typedoc.json's entryPointStrategy is %q, want %q; re-sync", got.EntryPointStrategy, want.EntryPointStrategy)
	}
	if !reflect.DeepEqual(got.Exclude, want.Exclude) {
		return fmt.Errorf("typedoc.json's exclude is %q, want %q; re-sync", got.Exclude, want.Exclude)
	}
	return nil
}

// typeDocKeys are the typedoc.json keys the manifest decides. An absent key
// reads as its zero value, which is what an undeclared manifest wants.
type typeDocKeys struct {
	EntryPoints        []string `json:"entryPoints"`
	EntryPointStrategy string   `json:"entryPointStrategy"`
	Exclude            []string `json:"exclude"`
}

// readTypeDocKeys reads them from typedoc.json, which carries // comments: each
// full-line comment is dropped before parsing. The shipped file has no comment
// after a value, and a line that did would fail to parse here, loudly, rather
// than be misread.
func readTypeDocKeys(data []byte) (typeDocKeys, error) {
	var kept [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("//")) {
			kept = append(kept, line)
		}
	}
	var doc typeDocKeys
	if err := json.Unmarshal(bytes.Join(kept, []byte("\n")), &doc); err != nil {
		return doc, fmt.Errorf("typedoc.json does not parse: %w", err)
	}
	return doc, nil
}

// normalise round-trips a TOML-decoded override through JSON so it compares
// equal to the same override read back from biome.json.
func normalise(o fragments.BiomeOverride) (any, error) {
	data, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	var v any
	return v, json.Unmarshal(data, &v)
}

func sortedIDs[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
