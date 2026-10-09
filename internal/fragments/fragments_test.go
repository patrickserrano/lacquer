package fragments

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

func shipped(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func probedIOS(t *testing.T) []string {
	t.Helper()
	rules, err := SwiftLintProbedRules(shipped(t, "profiles/ios/doctor.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func probedWeb(t *testing.T) []string {
	t.Helper()
	rules, err := BiomeProbedRules(shipped(t, "profiles/web/doctor.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

// The probed-rule guard is only as good as the list it reads. These are the
// rules the shipped probes assert today; a regex that stopped matching the
// escaped form in doctor.toml would empty the list and the guard with it.
func TestProbedRulesReadTheShippedProbes(t *testing.T) {
	ios := probedIOS(t)
	for _, want := range []string{"line_length", "force_unwrapping", "missing_docs", "identifier_name"} {
		if !slices.Contains(ios, want) {
			t.Errorf("ios probed rules %v lack %s", ios, want)
		}
	}
	web := probedWeb(t)
	for _, want := range []string{"suspicious/noExplicitAny", "style/noNonNullAssertion"} {
		if !slices.Contains(web, want) {
			t.Errorf("web probed rules %v lack %s", web, want)
		}
	}
}

func TestProbedRulesFailWhenTheProbesNameNone(t *testing.T) {
	none := []byte("[[probe]]\nname='x'\nexpect_output='nothing named here'\n")
	if _, err := SwiftLintProbedRules(none); err == nil {
		t.Error("SwiftLint: a doctor.toml naming no rule was read as an empty, satisfied list")
	}
	if _, err := BiomeProbedRules(none); err == nil {
		t.Error("Biome: a doctor.toml naming no rule was read as an empty, satisfied list")
	}
}

// ---- SwiftLint ----

func rule(regex string) SwiftLintRule { return SwiftLintRule{Regex: regex, Severity: "error"} }

// Planted fragments, each trying to switch off or hollow out a profile rule or
// a doctor-probed one. Every one must be refused.
func TestSwiftLintFragmentCannotReplaceAProfileOrProbedRule(t *testing.T) {
	profile := shipped(t, "profiles/ios/config/.swiftlint.yml")
	for id, why := range map[string]string{
		"no_print_statements": "a profile custom rule: a never-matching regex under its id silences it (measured)",
		"one_suite_per_file":  "a profile custom rule",
		"identifier_name":     "configured by the profile and asserted by the sentence-name probe",
		"line_length":         "configured by the profile and asserted by the --strict probe",
		"force_unwrapping":    "in the profile's opt_in_rules and asserted by the invocation probe",
		"trailing_comma":      "in the profile's disabled_rules",
		"missing_docs":        "asserted by a doctor probe, though set in .swiftlint-docs.yml rather than here",
	} {
		err := CheckSwiftLintRules(profile, probedIOS(t), map[string]SwiftLintRule{id: rule("NEVER_MATCHES")})
		if err == nil {
			t.Errorf("accepted a project rule named %s (%s)", id, why)
			continue
		}
		if !strings.Contains(err.Error(), "may only ADD rules") {
			t.Errorf("%s: refused, but not with the add-only explanation: %v", id, err)
		}
	}
}

func TestSwiftLintFragmentShapeIsClosed(t *testing.T) {
	for name, rules := range map[string]map[string]SwiftLintRule{
		"no regex":          {"a_rule": {Severity: "error"}},
		"blank regex":       {"a_rule": {Regex: "  "}},
		"bad severity":      {"a_rule": {Regex: "x", Severity: "fatal"}},
		"bad id":            {"ARule": rule("x")},
		"both match kinds":  {"a_rule": {Regex: "x", MatchKinds: []string{"identifier"}, ExcludedMatchKinds: []string{"comment"}}},
		"empty list member": {"a_rule": {Regex: "x", Excluded: []string{""}}},
	} {
		if err := ValidateSwiftLintRules(rules); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSwiftLintRenderIsByteIdenticalWithoutRules(t *testing.T) {
	profile := shipped(t, "profiles/ios/config/.swiftlint.yml")
	for _, rules := range []map[string]SwiftLintRule{nil, {}} {
		got, err := RenderSwiftLint(profile, probedIOS(t), rules)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(profile) {
			t.Fatal("rendering with no project rules changed .swiftlint.yml")
		}
	}
}

// The project rules land in custom_rules beside the profile's, and nothing the
// profile set moves: every other key parses to the same value, every profile
// custom rule is still there unchanged, and every shipped line survives in
// order (so the comments explaining the settings reach the project too).
func TestSwiftLintRenderAddsRulesAndKeepsTheProfile(t *testing.T) {
	profile := shipped(t, "profiles/ios/config/.swiftlint.yml")
	rules := map[string]SwiftLintRule{
		"sentry_import_confined": {
			Name:     "import Sentry confined",
			Regex:    `^\s*import\s+Sentry\s*$`,
			Message:  `Only "DiagnosticReporter" may import Sentry`,
			Severity: "error",
			Excluded: []string{`.*Core/Diagnostics/DiagnosticReporter\.swift$`, `.*Tests/.*\.swift$`},
		},
		"no_repository_in_viewmodel": {
			Regex:    `(any|some)\s+\w+Repository`,
			Included: []string{`.*ViewModel\.swift`},
			Severity: "warning",
		},
	}
	got, err := RenderSwiftLint(profile, probedIOS(t), rules)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := yaml.Unmarshal(profile, &before); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(got, &after); err != nil {
		t.Fatalf("rendered .swiftlint.yml does not parse: %v\n%s", err, got)
	}
	for k, v := range before {
		if k == "custom_rules" {
			continue
		}
		if !reflect.DeepEqual(after[k], v) {
			t.Errorf("rendering moved profile key %s", k)
		}
	}
	if len(after) != len(before) {
		t.Errorf("rendering added top-level keys: %d -> %d", len(before), len(after))
	}
	bc, ac := before["custom_rules"].(map[string]any), after["custom_rules"].(map[string]any)
	for id, v := range bc {
		if !reflect.DeepEqual(ac[id], v) {
			t.Errorf("profile custom rule %s changed", id)
		}
	}
	if len(ac) != len(bc)+len(rules) {
		t.Errorf("custom_rules has %d rules, want %d", len(ac), len(bc)+len(rules))
	}
	want := map[string]any{
		"name":     "import Sentry confined",
		"regex":    `^\s*import\s+Sentry\s*$`,
		"message":  `Only "DiagnosticReporter" may import Sentry`,
		"severity": "error",
		"excluded": []any{`.*Core/Diagnostics/DiagnosticReporter\.swift$`, `.*Tests/.*\.swift$`},
	}
	if !reflect.DeepEqual(ac["sentry_import_confined"], want) {
		t.Errorf("sentry_import_confined rendered as %#v, want %#v", ac["sentry_import_confined"], want)
	}
	// Every shipped line, in order.
	rest := string(got)
	for _, line := range strings.SplitAfter(string(profile), "\n") {
		i := strings.Index(rest, line)
		if i < 0 {
			t.Fatalf("shipped line lost or reordered: %q", line)
		}
		rest = rest[i+len(line):]
	}
}

// ---- Biome ----

func override(t *testing.T, tomlText string) []BiomeOverride {
	t.Helper()
	var doc struct {
		Web struct {
			BiomeOverrides []BiomeOverride `toml:"biome_overrides"`
		} `toml:"web"`
	}
	if _, err := toml.Decode(tomlText, &doc); err != nil {
		t.Fatalf("%v\n%s", err, tomlText)
	}
	return doc.Web.BiomeOverrides
}

// A real per-path rule from the fleet, anonymised: forbid node: imports under
// one directory. It adds enforcement and touches nothing the profile sets.
const restrictImports = `
[[web.biome_overrides]]
includes = ["core/**"]
[web.biome_overrides.linter.rules.style.noRestrictedImports]
level = "error"
options = { patterns = [{ group = ["node:*", "node:*/*"], message = "core runs on a runtime with no node: builtins." }] }
`

// Planted overrides, each trying to switch off or hollow out a profile rule, a
// doctor-probed rule, or the formatter. Every one must be refused.
func TestBiomeOverrideCannotDisableAProfileOrProbedRule(t *testing.T) {
	profile := shipped(t, "profiles/web/config/biome.json")
	cases := map[string]string{
		"profile rule off":            "includes=['**']\nlinter.rules.suspicious.noExplicitAny='off'",
		"profile rule restated":       "includes=['src/**']\nlinter.rules.suspicious.noExplicitAny='error'",
		"probed preset rule off":      "includes=['**']\nlinter.rules.style.noNonNullAssertion='off'",
		"probed preset rule restated": "includes=['**']\nlinter.rules.style.noNonNullAssertion='error'",
		"profile override's rule":     "includes=['**']\nlinter.rules.style.useNamingConvention={level='error'}",
		"warn lowers a preset error":  "includes=['**']\nlinter.rules.correctness.noUnusedLabels='warn'",
		"info":                        "includes=['**']\nlinter.rules.correctness.noUnusedLabels='info'",
		"object level off":            "includes=['**']\nlinter.rules.correctness.noUnusedLabels={level='off'}",
		"group recommended off":       "includes=['**']\nlinter.rules.style.recommended=false",
		"preset none":                 "includes=['**']\nlinter.rules.preset='none'",
		"widening options":            "includes=['**']\nlinter.rules.a11y.noBlankTarget={level='error',options={allowDomains=['x.com']}}",
		"formatter off":               "includes=['**']\nformatter.enabled=false\nlinter.rules.correctness.noUnusedLabels='error'",
		"linter off":                  "includes=['**']\nlinter.enabled=false\nlinter.rules.correctness.noUnusedLabels='error'",
		"files key":                   "includes=['**']\nfiles.maxSize=1\nlinter.rules.correctness.noUnusedLabels='error'",
		"no includes":                 "linter.rules.correctness.noUnusedLabels='error'",
		"no rules":                    "includes=['**']",
		"unknown group":               "includes=['**']\nlinter.rules.made_up.rule='error'",
	}
	for name, body := range cases {
		o := override(t, "[[web.biome_overrides]]\n"+body+"\n")
		if err := CheckBiomeOverrides(profile, probedWeb(t), o); err == nil {
			t.Errorf("%s: accepted %s", name, body)
		}
	}
}

func TestBiomeOverrideAcceptsATightening(t *testing.T) {
	profile := shipped(t, "profiles/web/config/biome.json")
	for _, body := range []string{
		restrictImports,
		"[[web.biome_overrides]]\nincludes=['**']\nlinter.rules.correctness.noUnusedFunctionParameters='error'\n",
		"[[web.biome_overrides]]\nincludes=['src/**']\nlinter.rules.correctness.noUnusedFunctionParameters={level='error'}\n",
	} {
		if err := CheckBiomeOverrides(profile, probedWeb(t), override(t, body)); err != nil {
			t.Errorf("refused a pure tightening: %v\n%s", err, body)
		}
	}
}

func TestBiomeRenderIsByteIdenticalWithoutOverrides(t *testing.T) {
	profile := shipped(t, "profiles/web/config/biome.json")
	got, err := RenderBiomeOverrides(profile, probedWeb(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(profile) {
		t.Fatal("rendering with no overrides changed biome.json")
	}
}

func TestBiomeRenderAppendsOverridesAndKeepsTheProfile(t *testing.T) {
	profile := shipped(t, "profiles/web/config/biome.json")
	overrides := override(t, restrictImports+"\n[[web.biome_overrides]]\nincludes=['**']\nlinter.rules.correctness.noUnusedFunctionParameters='error'\n")
	got, err := RenderBiomeOverrides(profile, probedWeb(t), overrides)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal(profile, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &after); err != nil {
		t.Fatalf("rendered biome.json is not JSON: %v\n%s", err, got)
	}
	for k, v := range before {
		if k != "overrides" && !reflect.DeepEqual(after[k], v) {
			t.Errorf("rendering moved profile key %s", k)
		}
	}
	bo, ao := before["overrides"].([]any), after["overrides"].([]any)
	if len(ao) != len(bo)+2 || !reflect.DeepEqual(ao[:len(bo)], bo) {
		t.Fatalf("overrides went %d -> %d, or the profile's changed", len(bo), len(ao))
	}
	want := map[string]any{
		"includes": []any{"core/**"},
		"linter": map[string]any{"rules": map[string]any{"style": map[string]any{"noRestrictedImports": map[string]any{
			"level": "error",
			"options": map[string]any{"patterns": []any{map[string]any{
				"group":   []any{"node:*", "node:*/*"},
				"message": "core runs on a runtime with no node: builtins.",
			}}},
		}}}},
	}
	if !reflect.DeepEqual(ao[len(bo)], want) {
		t.Errorf("first project override rendered as %#v", ao[len(bo)])
	}
}

// ---- TypeDoc ----

func TestTypeDocEntryPointsRefuseValuesThatDoNotMeanWhatTheySay(t *testing.T) {
	for _, entries := range [][]string{
		{},
		{""},
		{"/abs/index.ts"},
		{"../other/src/index.ts"},
		{"src/../../x.ts"},
		{"--plugin=evil"},
		{"src\\index.ts"},
		{"a.ts", "a.ts"},
		{"a\nb.ts"},
	} {
		if err := ValidateTypeDocEntryPoints(entries); err == nil {
			t.Errorf("accepted %q", entries)
		}
	}
	if err := ValidateTypeDocEntryPoints(nil); err != nil {
		t.Errorf("an absent key was refused: %v", err)
	}
}

func TestTypeDocRender(t *testing.T) {
	profile := shipped(t, "profiles/web/config/typedoc.json")
	got, err := RenderTypeDoc(profile, nil)
	if err != nil || string(got) != string(profile) {
		t.Fatalf("rendering with no entry points changed typedoc.json (err %v)", err)
	}

	got, err = RenderTypeDoc(profile, []string{"src/cli.ts", "src/index.ts"})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(profile), typeDocDefault, `  "entryPoints": ["src/cli.ts", "src/index.ts"],`, 1)
	if string(got) != want {
		t.Errorf("short list rendered as:\n%s", got)
	}

	long := []string{"core/src/index.ts", "api/src/index.ts", "redirect/src/index.ts", "proxy/src/index.ts", "extra/src/index.ts"}
	got, err = RenderTypeDoc(profile, long)
	if err != nil {
		t.Fatal(err)
	}
	wantLong := "  \"entryPoints\": [\n" +
		"    \"core/src/index.ts\",\n    \"api/src/index.ts\",\n    \"redirect/src/index.ts\",\n" +
		"    \"proxy/src/index.ts\",\n    \"extra/src/index.ts\"\n  ],"
	if !strings.Contains(string(got), wantLong) {
		t.Errorf("a list over the line width was not expanded one per line:\n%s", got)
	}

	// The profile line moved: refuse rather than leave the value unused.
	edited := []byte(strings.Replace(string(profile), typeDocDefault, `  "entryPoints": ["src/main.ts"],`, 1))
	if _, err := RenderTypeDoc(edited, []string{"src/cli.ts"}); err == nil {
		t.Error("rendered entry points into a typedoc.json that no longer carries the default line")
	}
}
