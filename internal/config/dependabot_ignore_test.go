package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// load writes a manifest and loads it, returning the config or the error.
func load(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".lacquer.toml")
	head := `
[project]
name = "P"
project_name = "P"
scheme = "P"
bundle_id = "com.x.p"
asc_app_id = "1"
xcodeproj = "P.xcodeproj"
swift_version = "6"
`
	if err := os.WriteFile(path, []byte(head+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// The shape a project actually writes, parsed into all four fields.
//
// Every one of them is load-bearing downstream: dependency and versions become
// the rendered Dependabot rule, reason becomes a comment in the generated file,
// and until is what `lacquer audit` expires against. A field silently dropped
// here is a field that reads as configured and does nothing.
func TestDependabotIgnoreParses(t *testing.T) {
	cfg, err := load(t, `
[[component]]
path = "admin"
stack = "web"
dependabot_ignore = [
  { dependency = "typedoc", versions = ["0.29.x", "^1.0.0"], reason = "crashes on the new compiler major", until = "2026-11-30" },
]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Components[0].DependabotIgnore
	if len(got) != 1 {
		t.Fatalf("got %d ignores, want 1", len(got))
	}
	d := got[0]
	if d.Dependency != "typedoc" {
		t.Errorf("dependency = %q, want typedoc", d.Dependency)
	}
	if len(d.Versions) != 2 || d.Versions[0] != "0.29.x" || d.Versions[1] != "^1.0.0" {
		t.Errorf("versions = %v, want [0.29.x ^1.0.0]", d.Versions)
	}
	if d.Reason != "crashes on the new compiler major" {
		t.Errorf("reason = %q", d.Reason)
	}
	if d.Until != "2026-11-30" {
		t.Errorf("until = %q, want 2026-11-30", d.Until)
	}
	if _, err := d.UntilDate(); err != nil {
		t.Errorf("UntilDate: %v", err)
	}
}

// A component that declares none gets none, and nothing invents an empty entry.
func TestDependabotIgnoreAbsentByDefault(t *testing.T) {
	cfg, err := load(t, `
[[component]]
path = "."
profiles = ["ios"]
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if n := len(cfg.Components[0].DependabotIgnore); n != 0 {
		t.Errorf("got %d ignores for a component that declared none", n)
	}
}

// Every partial or malformed entry must fail the LOAD, loudly.
//
// This is the whole safety argument for the mechanism. A missing `until` is a
// permanent ignore nobody will revisit; a missing `reason` is an ignore nobody
// can review; a typo'd key is a field silently dropped, turning a time-boxed
// ignore back into a permanent one — the exact decay this type exists to
// prevent. And unlike a broken exclusion, which merely fails to exclude, a
// broken ignore renders a rule that matches nothing while the file looks
// configured.
func TestDependabotIgnoreRejectsPartialEntries(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			"no until",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], reason = "r" }]`,
			"needs an until date",
		},
		{
			"no reason",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], until = "2026-11-30" }]`,
			"needs a reason",
		},
		{
			"blank reason",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], reason = "   ", until = "2026-11-30" }]`,
			"needs a reason",
		},
		{
			"no dependency",
			`dependabot_ignore = [{ versions = ["1.x"], reason = "r", until = "2026-11-30" }]`,
			"needs a dependency",
		},
		{
			"no versions",
			`dependabot_ignore = [{ dependency = "d", reason = "r", until = "2026-11-30" }]`,
			"needs at least one versions entry",
		},
		{
			"empty versions list",
			`dependabot_ignore = [{ dependency = "d", versions = [], reason = "r", until = "2026-11-30" }]`,
			"needs at least one versions entry",
		},
		{
			"unparseable until",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], reason = "r", until = "November 2026" }]`,
			"invalid until",
		},
		{
			// A date shape that is nearly right is the dangerous one: it looks
			// reviewed and would never expire.
			"until in the wrong order",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], reason = "r", until = "30-11-2026" }]`,
			"invalid until",
		},
		{
			"typo'd key",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], reasn = "r", until = "2026-11-30" }]`,
			"unknown [[component]].dependabot_ignore key",
		},
		{
			// Real Dependabot syntax, deliberately not accepted here: it is the
			// key that turns an ignore into a volume control.
			"update_types",
			`dependabot_ignore = [{ dependency = "d", versions = ["1.x"], reason = "r", until = "2026-11-30", update_types = ["version-update:semver-minor"] }]`,
			"does not support",
		},
		{
			"wildcard dependency",
			`dependabot_ignore = [{ dependency = "*", versions = ["1.x"], reason = "r", until = "2026-11-30" }]`,
			"must name ONE dependency",
		},
		{
			"partial wildcard dependency",
			`dependabot_ignore = [{ dependency = "@scope/*", versions = ["1.x"], reason = "r", until = "2026-11-30" }]`,
			"must name ONE dependency",
		},
		{
			"blanket versions",
			`dependabot_ignore = [{ dependency = "d", versions = ["*"], reason = "r", until = "2026-11-30" }]`,
			"no version in it",
		},
		{
			"bare string form",
			`dependabot_ignore = ["typedoc"]`,
			"must be a table",
		},
		{
			"versions not a list",
			`dependabot_ignore = [{ dependency = "d", versions = "1.x", reason = "r", until = "2026-11-30" }]`,
			"must be an array of strings",
		},
		{
			"versions holding a non-string",
			`dependabot_ignore = [{ dependency = "d", versions = [7], reason = "r", until = "2026-11-30" }]`,
			"must be an array of strings",
		},
		{
			// Dependabot takes the union of two rules for one name, so the
			// narrower entry's until date silently stops meaning anything.
			"same dependency twice",
			`dependabot_ignore = [
  { dependency = "d", versions = ["1.x"], reason = "a", until = "2026-11-30" },
  { dependency = "d", versions = ["2.x"], reason = "b", until = "2027-01-01" },
]`,
			"twice",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, "\n[[component]]\npath = \"admin\"\nstack = \"web\"\n"+tc.body+"\n")
			if err == nil {
				t.Fatalf("loaded without error; %s must fail loudly", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The names real ecosystems use must all load. A validator that rejected scoped
// npm packages or Go module paths would push people back to hand-editing the
// generated file, which is the outcome the whole tool exists to prevent.
func TestDependabotIgnoreAcceptsRealDependencyNames(t *testing.T) {
	for _, name := range []string{
		"typescript",
		"@scope/some-pkg",
		"github.com/owner/mod/v2",
		"serde_json",
		"actions/checkout",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, `
[[component]]
path = "admin"
stack = "web"
dependabot_ignore = [{ dependency = "`+name+`", versions = ["1.x"], reason = "r", until = "2026-11-30" }]
`); err != nil {
				t.Errorf("valid dependency name rejected: %v", err)
			}
		})
	}
}

// And the range syntaxes, which differ per ecosystem. Dependabot forwards these
// to the package manager verbatim; a charset check that rejected Maven's
// brackets or Bundler's `~>` would make the field unusable for those.
func TestDependabotIgnoreAcceptsRealVersionRanges(t *testing.T) {
	for _, v := range []string{"7.x", "^7.0.0", "7.*", "~> 2.0", "[1.4,)", ">=2.0, <3.0", "1.2.3"} {
		t.Run(v, func(t *testing.T) {
			if _, err := load(t, `
[[component]]
path = "admin"
stack = "web"
dependabot_ignore = [{ dependency = "d", versions = ["`+v+`"], reason = "r", until = "2026-11-30" }]
`); err != nil {
				t.Errorf("valid range %q rejected: %v", v, err)
			}
		})
	}
}
