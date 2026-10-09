package doctor

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/fragments"
)

const fragmentManifest = `[project]
name = 'demo'

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

[[component]]
path = 'app'
profiles = ['ios']

[[component]]
path = 'admin'
profiles = ['web']
`

func shippedFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The three fragment checks pass on a project whose configs were rendered from
// its manifest, and each fails when its config is the stale, shipped one. Run
// from a lacquer root holding only these probes, through Run, so the probe
// names, the dispatch and the component paths are all exercised.
func TestFragmentChecksPassWhenRenderedAndFailWhenStale(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "profiles/ios/doctor.toml"), "[[probe]]\nname='swiftlint'\ncheck='swiftlint-custom-rules'\nexpect='pass'\n")
	write(t, filepath.Join(root, "profiles/web/doctor.toml"), "[[probe]]\nname='biome'\ncheck='biome-overrides'\nexpect='pass'\n[[probe]]\nname='typedoc'\ncheck='typedoc-entry-points'\nexpect='pass'\n")
	write(t, filepath.Join(project, ".lacquer.toml"), fragmentManifest)
	cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}

	swiftlint := shippedFile(t, "profiles/ios/config/.swiftlint.yml")
	biome := shippedFile(t, "profiles/web/config/biome.json")
	typedoc := shippedFile(t, "profiles/web/config/typedoc.json")
	iosProbed, err := fragments.SwiftLintProbedRules(shippedFile(t, "profiles/ios/doctor.toml"))
	if err != nil {
		t.Fatal(err)
	}
	webProbed, err := fragments.BiomeProbedRules(shippedFile(t, "profiles/web/doctor.toml"))
	if err != nil {
		t.Fatal(err)
	}
	renderedSwiftLint, err := fragments.RenderSwiftLint(swiftlint, iosProbed, cfg.IOS.SwiftLintCustomRules)
	if err != nil {
		t.Fatal(err)
	}
	renderedBiome, err := fragments.RenderBiomeOverrides(biome, webProbed, cfg.Web.BiomeOverrides)
	if err != nil {
		t.Fatal(err)
	}
	renderedTypeDoc, err := fragments.RenderTypeDoc(typedoc, cfg.Web.TypeDocEntryPoints, cfg.Web.TypeDocEntryPointStrategy)
	if err != nil {
		t.Fatal(err)
	}

	run := func() map[string]Result {
		t.Helper()
		res, err := Run(root, project, cfg, nil, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		byName := map[string]Result{}
		for _, r := range res {
			byName[r.Name] = r
		}
		if len(byName) != 3 {
			t.Fatalf("ran %d probes, want 3: %+v", len(byName), res)
		}
		return byName
	}

	write(t, filepath.Join(project, "app/.swiftlint.yml"), string(renderedSwiftLint))
	write(t, filepath.Join(project, "admin/biome.json"), string(renderedBiome))
	write(t, filepath.Join(project, "admin/typedoc.json"), string(renderedTypeDoc))
	for name, r := range run() {
		if !r.OK {
			t.Errorf("%s failed on a rendered project: %s", name, r.Detail)
		}
	}

	for name, stale := range map[string]struct {
		path string
		body []byte
		want string
	}{
		"swiftlint": {"app/.swiftlint.yml", swiftlint, "missing declared project rule sentry_import_confined"},
		"biome":     {"admin/biome.json", biome, "missing declared override #1"},
		"typedoc":   {"admin/typedoc.json", typedoc, `entryPoints are ["src/index.ts"]`},
	} {
		write(t, filepath.Join(project, stale.path), string(stale.body))
		r := run()[name]
		if r.OK || !strings.Contains(r.Detail, stale.want) {
			t.Errorf("%s on a stale config: OK=%v detail=%q, want a failure naming %q", name, r.OK, r.Detail, stale.want)
		}
	}
}

// A project that declares nothing still runs the planted controls, and passes
// on the shipped configs: the checks are not vacuous for it, and not red.
func TestFragmentChecksPassOnAnUndeclaredProject(t *testing.T) {
	if err := checkSwiftLintCustomRules(shippedFile(t, "profiles/ios/config/.swiftlint.yml"), nil); err != nil {
		t.Errorf("swiftlint: %v", err)
	}
	if err := checkBiomeOverrides(shippedFile(t, "profiles/web/config/biome.json"), nil); err != nil {
		t.Errorf("biome: %v", err)
	}
	if err := checkTypeDocEntryPoints(shippedFile(t, "profiles/web/config/typedoc.json"), nil, ""); err != nil {
		t.Errorf("typedoc: %v", err)
	}
}

func TestShippedProfilesRunTheFragmentChecks(t *testing.T) {
	for profile, want := range map[string][]string{
		"ios": {"swiftlint-custom-rules"},
		"web": {"biome-overrides", "typedoc-entry-points"},
	} {
		probes, err := LoadProbes(filepath.Join("..", ".."), profile)
		if err != nil {
			t.Fatal(err)
		}
		var checks []string
		for _, p := range probes {
			checks = append(checks, p.Check)
		}
		for _, w := range want {
			if !slices.Contains(checks, w) {
				t.Errorf("profiles/%s/doctor.toml does not run %s", profile, w)
			}
		}
	}
}

// Every TypeDoc probe passes its own --entryPoints, which is why no
// [web] typedoc_entry_points value can make one pass or fail. A probe that
// dropped it would read the project's entry points instead, and this guarantee
// would quietly stop holding.
func TestTypeDocProbesPinTheirOwnEntryPoint(t *testing.T) {
	probes, err := LoadProbes(filepath.Join("..", ".."), "web")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range probes {
		cmd := strings.Join(p.Argv, " ")
		if !strings.Contains(cmd, "typedoc") {
			continue
		}
		n++
		if !strings.Contains(cmd, "--entryPoints ./probe.ts") {
			t.Errorf("TypeDoc probe %q does not pin --entryPoints, so the manifest's entry points would decide what it checks", p.Name)
		}
	}
	if n == 0 {
		t.Fatal("found no TypeDoc probe; the guarantee above is about nothing")
	}
}

// The entry-point check also holds the strategy and the test exclude to the
// manifest, in both directions: a declared strategy missing from the synced
// file, a synced strategy the manifest no longer declares, and an exclude that
// was edited (a wider one would hide real exports from the gate).
func TestTypeDocCheckHoldsStrategyAndExcludeToTheManifest(t *testing.T) {
	shipped := shippedFile(t, "profiles/web/config/typedoc.json")
	entries := []string{"src/lib", "src/types"}
	expanded, err := fragments.RenderTypeDoc(shipped, entries, "expand")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTypeDocEntryPoints(expanded, entries, "expand"); err != nil {
		t.Fatalf("a freshly rendered expand config failed: %v", err)
	}
	widened := []byte(strings.Replace(string(expanded), `"**/__tests__/**"`, `"**/__tests__/**", "src/**"`, 1))
	for name, tc := range map[string]struct {
		synced   []byte
		entries  []string
		strategy string
		want     string
	}{
		"declared, not synced": {shipped, nil, "expand", `entryPointStrategy is "", want "expand"`},
		"synced, not declared": {expanded, entries, "", `entryPointStrategy is "expand", want ""`},
		"exclude widened":      {widened, entries, "expand", "exclude is"},
	} {
		err := checkTypeDocEntryPoints(tc.synced, tc.entries, tc.strategy)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
