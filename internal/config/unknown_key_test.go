package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The measured case, and the reason this check exists: `profiles` misspelled as
// `profile` leaves the component gating nothing — no CI, no hooks, no CLAUDE
// region — while `lacquer audit` exits 6 blaming an undeclared stack rather than
// the typo three characters away.
func TestLoadRejectsMisspelledComponentKey(t *testing.T) {
	_, err := loadString(t, "[project]\nname=\"x\"\n\n[[component]]\npath=\".\"\nprofile=[\"ios\"]\n")
	if err == nil {
		t.Fatal("a [[component]] with `profile` instead of `profiles` loaded clean; it manages nothing")
	}
	msg := err.Error()
	// The unknown key must be named...
	if !strings.Contains(msg, "component.profile") {
		t.Errorf("error does not name the unknown key:\n%s", msg)
	}
	// ...and so must the set it should have been drawn from, or the reader is
	// left guessing at the spelling that would have worked.
	for _, want := range []string{"[[component]]", "profiles", "path", "stack", "dependabot_ignore"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not offer the known key %q:\n%s", want, msg)
		}
	}
}

// A `retired` key written before retirement existed read as a working
// retirement and did nothing. Every table in the manifest gets the same
// treatment, so this walks them rather than pinning one.
func TestLoadRejectsUnknownKeyInEveryTable(t *testing.T) {
	cases := []struct {
		name, body, key, table string
	}{
		{
			"top level", "typo = 1\n[project]\nname=\"x\"\n",
			"typo", "the top level",
		},
		{
			"project", "[project]\nname=\"x\"\nretred = true\n",
			"project.retred", "[project]",
		},
		{
			"component", "[project]\nname=\"x\"\n[[component]]\npath=\".\"\nstakc=\"web\"\n",
			"component.stakc", "[[component]]",
		},
		{
			"product",
			"[project]\nname=\"x\"\n[[product]]\nname=\"A\"\nscheme=\"A\"\nbundle_id=\"a.b\"\nasc_app_id=\"1\"\ntagprefix=\"a\"\n",
			"product.tagprefix", "[[product]]",
		},
		{
			"baseline", "[project]\nname=\"x\"\n[baseline]\nrelaxx = {}\n",
			"baseline.relaxx", "[baseline]",
		},
		{
			"baseline relax entry",
			"[project]\nname=\"x\"\n[baseline.relax]\nswift_version = { reason=\"r\", until=\"2099-01-01\", note=\"n\" }\n",
			"baseline.relax.swift_version.note", "[baseline.relax].<name>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadString(t, c.body)
			if err == nil {
				t.Fatalf("unknown key %q loaded clean", c.key)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("error does not name %q:\n%s", c.key, err)
			}
			if !strings.Contains(err.Error(), c.table) {
				t.Errorf("error does not name the table %q:\n%s", c.table, err)
			}
		})
	}
}

// An unknown TABLE reports itself and every key under it. Saying all of them
// would bury the one line that matters under its own consequences.
func TestLoadNamesOnlyTheOutermostUnknownTable(t *testing.T) {
	_, err := loadString(t, "[project]\nname=\"x\"\n\n[project.limits]\nmax = 3\nmin = 1\n")
	if err == nil {
		t.Fatal("an unknown table loaded clean")
	}
	msg := err.Error()
	if !strings.Contains(msg, "project.limits") {
		t.Errorf("error does not name the unknown table:\n%s", msg)
	}
	for _, buried := range []string{"project.limits.max", "project.limits.min"} {
		if strings.Contains(msg, buried) {
			t.Errorf("error repeats %q, a consequence of the unknown table rather than a finding:\n%s", buried, msg)
		}
	}
}

// Config.Root is `toml:"-"`: it is the directory the manifest was read from, and
// a file must not be able to name it. Silently ignoring it was the old
// behaviour; now it is refused out loud.
func TestLoadRejectsRootKey(t *testing.T) {
	if _, err := loadString(t, "root = \"/etc\"\n[project]\nname=\"x\"\n"); err == nil {
		t.Error("a top-level `root` key loaded clean; it names a field no manifest may set")
	}
}

// Undecoded() is BLIND inside a type with a custom UnmarshalTOML — the decoder
// hands it the whole subtree and records nothing below. The strict check is only
// honest because all three such types close their own key sets, so this asserts
// the interior of each is still rejected. If one ever loses its closed set, the
// top-level check will go on looking like it covers the whole manifest.
func TestUnknownKeysAreRejectedInsideCustomUnmarshalers(t *testing.T) {
	cases := map[string]string{
		"[project].exclude": "[project]\nname=\"x\"\nexclude=[{path=\"a.yml\", reason=\"r\", reson=\"typo\"}]\n",
		"[project].retired": "[project]\nname=\"x\"\nretired={since=\"2026-01-01\", reason=\"r\", until=\"2030-01-01\"}\n",
		"dependabot_ignore": "[project]\nname=\"x\"\n[[component]]\npath=\".\"\n" +
			"dependabot_ignore=[{dependency=\"ts\", versions=[\"5.x\"], reason=\"r\", until=\"2099-01-01\", update_types=[\"a\"]}]\n",
	}
	for where, body := range cases {
		t.Run(where, func(t *testing.T) {
			if _, err := loadString(t, body); err == nil {
				t.Errorf("an unknown key inside %s loaded clean; the strict check cannot see in here, so this type's own closed key set is the only guard", where)
			}
		})
	}
}

// Everything a real manifest actually writes must keep loading. A strictness
// check that rejects the fleet is worse than no check: it locks projects out of
// `sync` and `fix`, which are what repair them.
func TestLoadAcceptsEveryDeclaredKey(t *testing.T) {
	body := `
[project]
name = "Example"
project_name = "Example"
scheme = "Example"
bundle_id = "com.example.app"
asc_app_id = "1000000001"
xcodeproj = "Example.xcodeproj"
swift_version = "6"
github_org = "example-org"
stack = "ios"
tools = ["claude", "codex", "antigravity"]
skills = ["example-org/skills@healthkit"]
optional_workflows = []
build_env = ["NEXT_PUBLIC_API_URL"]
exclude = ["typedoc.json", { path = "a.yml", reason = "r", until = "2099-01-01" }]
retired = { since = "2026-08-18", reason = "not viable" }

[[product]]
name = "Example"
scheme = "Example"
bundle_id = "com.example.app"
asc_app_id = "1000000001"
tag_prefix = "example"
test_target = "ExampleTests"
ui_test_target = "ExampleUITests"
extra_test_targets = ["CoreKitTests"]
app_target = "Example.app"
secrets_file = "Secrets.xcconfig"
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY" }
secret_formats = { REVENUECAT_API_KEY = "appl_*" }

[[component]]
path = "."
profiles = ["ios"]
stack = "ios"
dependabot_ignore = [{ dependency = "typescript", versions = ["5.x"], reason = "r", until = "2099-01-01" }]

[baseline.relax]
swift_version = { reason = "r", until = "2099-01-01" }
`
	if _, err := loadString(t, body); err != nil {
		t.Fatalf("a manifest using every declared key must load: %v", err)
	}
}

// The fixtures under internal/shipped are the committed record of what a real
// project's manifest looks like. Loading them here means the strict check is
// measured against them on every run, not just when the e2e suite happens to.
func TestShippedFixtureManifestsStillLoad(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "shipped", "testdata", "projects", "*", ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Skip("not running from a lacquer checkout: no project fixtures found")
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err != nil {
			t.Errorf("%s no longer loads: %v", p, err)
		}
	}
}
