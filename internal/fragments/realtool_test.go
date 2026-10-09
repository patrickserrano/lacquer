package fragments

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The appended JSON has to be what `biome format` would write, because a synced
// project's own `biome ci` checks biome.json and typedoc.json like any other
// file: a renderer that emitted valid but differently-laid-out JSON would fail
// the project's CI on the sync PR that adopts a fragment. Needs a real biome,
// so it skips without one; the layouts it pins are asserted textually above.
func TestRenderedJSONIsBiomeFormatted(t *testing.T) {
	bin, err := exec.LookPath("biome")
	if err != nil {
		t.Skip("biome not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	profile := strings.ReplaceAll(string(shipped(t, "profiles/web/config/biome.json")), "{{COMPONENT_TO_ROOT}}", ".")
	biome, err := RenderBiomeOverrides([]byte(profile), probedWeb(t), override(t, restrictImports+
		"\n[[web.biome_overrides]]\nincludes=['**']\nlinter.rules.correctness.noUnusedFunctionParameters='error'\n"))
	if err != nil {
		t.Fatal(err)
	}
	typedoc, err := RenderTypeDoc(shipped(t, "profiles/web/config/typedoc.json"),
		[]string{"core/src/index.ts", "api/src/index.ts", "redirect/src/index.ts", "proxy/src/index.ts", "extra/src/index.ts"})
	if err != nil {
		t.Fatal(err)
	}
	short, err := RenderTypeDoc(shipped(t, "profiles/web/config/typedoc.json"), []string{"src/cli.ts", "src/index.ts"})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"biome.json": biome, "typedoc.json": typedoc, "short/typedoc.json": short} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// --write, then compare: a check-mode exit code alone would also be
	// non-zero for an unparseable config, and zero for a file it skipped.
	cmd := exec.Command(bin, "format", "--write", "--colors=off", "biome.json", "typedoc.json", "short/typedoc.json")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Formatted 3 files") {
		t.Fatalf("biome format did not format all three files: %v\n%s", err, out)
	}
	for name, body := range map[string][]byte{"biome.json": biome, "typedoc.json": typedoc, "short/typedoc.json": short} {
		after, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(body) {
			t.Errorf("biome format rewrote the rendered %s:\n--- rendered\n%s\n--- formatted\n%s", name, body, after)
		}
	}
}

// SwiftLint must load the rendered file and enforce the project rule beside the
// profile's: a fixture carrying one violation of each is reported for both.
func TestRenderedSwiftLintConfigEnforcesBoth(t *testing.T) {
	bin, err := exec.LookPath("swiftlint")
	if err != nil {
		t.Skip("swiftlint not installed")
	}
	dir := t.TempDir()
	rendered, err := RenderSwiftLint(shipped(t, "profiles/ios/config/.swiftlint.yml"), probedIOS(t), map[string]SwiftLintRule{
		"sentry_import_confined": {
			Name:     "import Sentry confined",
			Regex:    `^\s*import\s+Sentry\s*$`,
			Message:  "report through the reporter",
			Severity: "error",
			Excluded: []string{`.*Reporter\.swift$`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".swiftlint.yml"), rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	src := "import Sentry\n\n/// A type.\nstruct Probe {\n    /// A value.\n    func value() { print(\"x\") }\n}\n"
	for _, f := range []string{"Probe.swift", "Reporter.swift"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "lint", "--strict", "--quiet", "--config", ".swiftlint.yml", ".")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	got := string(out)
	for _, want := range []string{
		"Probe.swift:1:1: error: import Sentry confined Violation: report through the reporter (sentry_import_confined)",
		"Probe.swift:6:20: error: No print statements Violation",
		"Reporter.swift:6:20: error: No print statements Violation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Reporter.swift:1:1") {
		t.Errorf("the project rule's own exclusion was not honoured:\n%s", got)
	}
}
