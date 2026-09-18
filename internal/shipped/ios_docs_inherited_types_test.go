package shipped

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// .swiftlint-docs.yml once set `missing_docs.excludes_inherited_types: true`,
// under a comment saying a conformance inherits the protocol's documentation.
// That describes MEMBERS implementing a requirement. What SwiftLint does with
// the option (MissingDocsRule.swift, 0.65.1) is return .skipChildren from any
// struct, class, enum, actor, protocol or extension with an inheritance clause:
// the type AND every member inside it go unchecked. In a SwiftUI app that is
// nearly every View and every Codable/Identifiable model, so the documentation
// standard silently stopped applying to most of the code it exists for.
// Measured across the fourteen iOS repositories: 2,219 violations with it on,
// 10,218 with it off. One repository reported zero today and owed 584.
//
// SwiftLint's default for the option is TRUE, so deleting the line restores the
// exemption just as surely as setting it. Both tests below fail either way.

// iosDocsConfig is the shipped documentation config.
func iosDocsConfig(t *testing.T) string {
	t.Helper()
	return filepath.Join(root(t), "profiles", "ios", "config", ".swiftlint-docs.yml")
}

// TestIOSDocsLintDoesNotExemptConformingTypes pins the setting itself. It runs
// everywhere, including CI, which has no swiftlint to run the test below.
func TestIOSDocsLintDoesNotExemptConformingTypes(t *testing.T) {
	data, err := os.ReadFile(iosDocsConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MissingDocs map[string]any `yaml:"missing_docs"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf(".swiftlint-docs.yml is not valid YAML: %v", err)
	}
	v, ok := cfg.MissingDocs["excludes_inherited_types"]
	if !ok {
		t.Fatal("missing_docs.excludes_inherited_types is absent. SwiftLint defaults it to TRUE, " +
			"which skips every type declaring a conformance and every member inside it: " +
			"nearly every SwiftUI View and Codable model. Set it to false explicitly.")
	}
	if b, isBool := v.(bool); !isBool || b {
		t.Fatalf("missing_docs.excludes_inherited_types = %v, want false. true skips every type "+
			"declaring a conformance AND every member inside it, not just protocol-requirement members.", v)
	}
}

// swiftlintViolation is the part of SwiftLint's JSON reporter output read here.
type swiftlintViolation struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	RuleID string `json:"rule_id"`
}

// TestIOSDocsLintFlagsConformingTypesAndTheirMembers runs the shipped config
// against a View and a Codable model, and requires violations on the type and
// on a member of a documented conforming type. The undocumented plain struct is
// the positive control: without it, "no violations anywhere" would read the
// same as "the lint never ran".
func TestIOSDocsLintFlagsConformingTypesAndTheirMembers(t *testing.T) {
	if _, err := exec.LookPath("swiftlint"); err != nil {
		t.Skip("swiftlint is not installed; TestIOSDocsLintDoesNotExemptConformingTypes still pins the setting")
	}
	dir := t.TempDir()
	cfg, err := os.ReadFile(iosDocsConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	// Copied beside the fixture: SwiftLint resolves `excluded` globs relative to
	// the config file's directory (see the doctor probe for test targets).
	if err := os.WriteFile(filepath.Join(dir, ".swiftlint-docs.yml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}
	// Line numbers matter: 3 is the control, 5 the conforming type, 13 a member
	// of a documented conforming type.
	const fixture = `import SwiftUI

struct DocsProbePlain {}

struct DocsProbeView: View {
    var body: some View { Text("x") }
}

/// Documented, so a violation below is the member's own.
struct DocsProbeModel: Codable, Identifiable {
    /// Documented.
    let id: UUID
    var nickname: String
}
`
	if err := os.WriteFile(filepath.Join(dir, "Probe.swift"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("swiftlint", "lint", "--strict", "--quiet", "--reporter", "json",
		"--config", ".swiftlint-docs.yml", ".")
	cmd.Dir = dir
	out, err := cmd.Output()
	// Violations under --strict exit non-zero; anything else must still parse.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("swiftlint did not run: %v", err)
	}
	var got []swiftlintViolation
	if jerr := json.Unmarshal(out, &got); jerr != nil {
		t.Fatalf("swiftlint output is not its JSON report (%v): %s", jerr, out)
	}
	flagged := map[int]bool{}
	for _, v := range got {
		if v.RuleID == "missing_docs" && filepath.Base(v.File) == "Probe.swift" {
			flagged[v.Line] = true
		}
	}
	if !flagged[3] {
		t.Fatalf("the control (undocumented plain struct, line 3) was not flagged, so the lint did not run "+
			"as intended and nothing below can be trusted. Violations: %+v", got)
	}
	if !flagged[5] {
		t.Errorf("undocumented `struct DocsProbeView: View` (line 5) was not flagged: the docs lint " +
			"exempts types that declare a conformance (excludes_inherited_types).")
	}
	if !flagged[13] {
		t.Errorf("undocumented `var nickname` inside `struct DocsProbeModel: Codable, Identifiable` (line 13) " +
			"was not flagged: the docs lint skips every member of a conforming type.")
	}
}
