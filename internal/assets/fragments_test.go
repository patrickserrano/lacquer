package assets

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

const fragmentKeys = `
[ios.swiftlint_custom_rules.sentry_import_confined]
regex = '^\s*import\s+Sentry\s*$'
severity = 'error'

[web]
typedoc_entry_points = ['src/cli.ts', 'src/index.ts']

[[web.biome_overrides]]
includes = ['core/**']
[web.biome_overrides.linter.rules.style.noRestrictedImports]
level = 'error'
options = { patterns = [{ group = ['node:*'], message = 'no node builtins in core' }] }
`

// renderEvery renders every asset of every shipped profile, each in its own
// component, keyed by destination.
func renderEvery(t *testing.T, extra string) map[string]string {
	t.Helper()
	profiles, err := os.ReadDir("../../profiles")
	if err != nil {
		t.Fatal(err)
	}
	manifest := "[project]\nname='demo'\nproject_name='Demo'\nscheme='Demo'\nbundle_id='com.x.demo'\nasc_app_id='1'\nxcodeproj='Demo.xcodeproj'\nswift_version='6'\ngithub_org='acme'\n" + extra
	var names []string
	for _, p := range profiles {
		if p.IsDir() {
			names = append(names, p.Name())
			manifest += "\n[[component]]\npath='" + p.Name() + "'\nprofiles=['" + p.Name() + "']\n"
		}
	}
	if len(names) < 3 {
		t.Fatalf("found only %v under profiles/", names)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".lacquer.toml")
	write(t, path, manifest)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range plan {
		body, _, err := Render(a, cfg)
		if err != nil {
			t.Fatalf("render %s: %v", a.Dest, err)
		}
		out[a.Dest] = string(body)
		if len(a.Merged) == 0 && extra == "" {
			// Without fragment keys every single-source asset is its source with
			// tokens substituted, and nothing else: no fragment path touched it.
			src, err := os.ReadFile(a.Src)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := tokens.Substitute(string(src), tokens.Values(cfg, a.Prefix))
			if string(body) != want {
				t.Errorf("%s renders differently from its shipped source with no fragment declared", a.Dest)
			}
		}
	}
	return out
}

// A project without the new keys renders byte-identical to before, in every
// profile; with them, exactly the three fragment destinations change.
func TestFragmentsRenderOnlyWhereDeclared(t *testing.T) {
	without := renderEvery(t, "")
	with := renderEvery(t, fragmentKeys)
	var changed []string
	for dest, body := range with {
		if without[dest] != body {
			changed = append(changed, dest)
		}
	}
	sort.Strings(changed)
	want := []string{"ios/.swiftlint.yml", "web/biome.json", "web/typedoc.json"}
	if !slices.Equal(changed, want) {
		t.Errorf("declaring fragments changed %v, want exactly %v", changed, want)
	}
	if len(with) != len(without) {
		t.Errorf("declaring fragments changed the plan: %d -> %d assets", len(without), len(with))
	}
}

// The strategy key alone changes typedoc.json and nothing else, and what it
// changes is the strategy plus the test exclude, with the default entry point
// kept. Without it the exclude is absent: the shipped file has none, and the
// render must not grow one for every web project on sync.
func TestTypeDocStrategyRendersOnlyTypeDoc(t *testing.T) {
	without := renderEvery(t, "")
	with := renderEvery(t, "[web]\ntypedoc_entry_point_strategy = 'expand'\n")
	var changed []string
	for dest, body := range with {
		if without[dest] != body {
			changed = append(changed, dest)
		}
	}
	if !slices.Equal(changed, []string{"web/typedoc.json"}) {
		t.Fatalf("declaring the strategy changed %v, want exactly web/typedoc.json", changed)
	}
	got := with["web/typedoc.json"]
	for _, want := range []string{`"entryPoints": ["src/index.ts"],`, `"entryPointStrategy": "expand",`, `"**/*.test.ts"`, `"**/__tests__/**"`} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered typedoc.json lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(without["web/typedoc.json"], `"exclude"`) || strings.Contains(without["web/typedoc.json"], "entryPointStrategy") {
		t.Errorf("an undeclared project's typedoc.json carries a strategy or exclude:\n%s", without["web/typedoc.json"])
	}
}
