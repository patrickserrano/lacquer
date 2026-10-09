package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/initcmd"
	"github.com/patrickserrano/lacquer/internal/sync"
)

// identifierProbe is the ios probe whose fixture a greedy project rule can
// reach; the test below uses it to show a fragment can turn a probe red but
// never green.
const identifierProbe = "the linter allows a Swift Testing sentence name but still catches a bad one"

// TestDoctorIOSProbesBiteWithProjectRules runs every shipped ios probe against
// a project that declares SwiftLint custom rules. Each SwiftLint probe plants a
// violation of a profile rule (line_length, force_unwrapping, identifier_name,
// missing_docs, ...) and asserts it is rejected BY NAME; with the project's rules
// rendered into the same .swiftlint.yml, every one must still be.
//
// Then the adversarial half: a project rule whose regex matches a probe's own
// fixture adds a violation to that probe's run. That probe must go RED (it
// counts violations), never green: a fragment can make a probe fail, and the
// grammar leaves it no way to make one pass.
func TestDoctorIOSProbesBiteWithProjectRules(t *testing.T) {
	for _, tool := range []string{"swiftlint", "swiftformat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed; the ios probes cannot run here", tool)
		}
	}
	lacquerRoot := root(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Acme.xcodeproj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Acme.xcodeproj", "project.pbxproj"), []byte(newXcodeproj), 0o644); err != nil {
		t.Fatal(err)
	}
	initRepo(t, dir)
	if _, err := initcmd.Run(lacquerRoot, dir, "ios"); err != nil {
		t.Fatal(err)
	}
	p := &project{root: dir, lacquerRoot: lacquerRoot, t: t}
	p.commit("init")
	appendManifest(t, p, `
[ios.swiftlint_custom_rules.sentry_import_confined]
name = "import Sentry confined"
regex = '^\s*import\s+Sentry\s*$'
message = "report through the reporter"
severity = "error"
excluded = ['.*Reporter\.swift$']

[ios.swiftlint_custom_rules.no_repository_in_viewmodel]
regex = '(any|some)\s+\w+Repository'
included = ['.*ViewModel\.swift']
severity = "warning"
`)
	p.commit("declare project rules")
	p.sync()
	p.commit("sync")
	cfg := p.config()
	comp := cfg.Components[0].Path

	results, err := doctor.Run(lacquerRoot, dir, cfg, []string{"ios"}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	ran := map[string]bool{}
	for _, r := range results {
		ran[r.Name] = true
		if !r.OK {
			t.Errorf("with project rules present, probe %q failed: %s", r.Name, r.Detail)
		}
	}
	for _, want := range []string{identifierProbe, "SwiftLint rejects a warning-severity violation under --strict",
		"SwiftLint is invoked in a form it still accepts", "manifest-declared SwiftLint custom rules reach the synced config, and cannot replace a profile rule"} {
		if !ran[want] {
			t.Errorf("probe %q did not run", want)
		}
	}

	// The project rule is live in the file the probes read.
	fixture := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixture, "Probe.swift"), []byte("import Sentry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("swiftlint", "lint", "--strict", "--quiet", "--config",
		filepath.Join(dir, comp, ".swiftlint.yml"), fixture).CombinedOutput()
	if !strings.Contains(string(out), "(sentry_import_confined)") {
		t.Fatalf("the declared project rule did not fire:\n%s", out)
	}

	appendManifest(t, p, `
[ios.swiftlint_custom_rules.greedy_probe_match]
regex = 'func DoTheThing'
severity = "error"
`)
	p.commit("a project rule that matches a probe fixture")
	p.sync()
	results, err = doctor.Run(lacquerRoot, dir, p.config(), []string{"ios"}, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Name == identifierProbe && r.OK {
			t.Errorf("a project rule matching the probe's fixture left %q green; it must count the extra violation and fail", r.Name)
		}
	}
}

// TestDoctorWebBiomeProbesBiteWithOverrides runs the shipped web biome probes
// against a component whose biome.json carries project overrides, against a real
// biome. One override is a per-path restriction (the fleet's real use); the
// other raises a rule to error for EVERY path, the widest an override can reach.
// Every probe must still fail on its own violation, the fragment check must
// pass, and the per-path override must itself fire.
//
// Needs a real biome: CI installs a pinned one and requires this to pass (see
// TestDoctorWebBiomeProbesAgainstRealBiome for the mechanism).
func TestDoctorWebBiomeProbesBiteWithOverrides(t *testing.T) {
	nm := os.Getenv(biomeNodeModulesEnv)
	if nm == "" {
		if os.Getenv("LACQUER_TEST_REQUIRE_BIOME") != "" {
			t.Fatalf("LACQUER_TEST_REQUIRE_BIOME is set but %s is not", biomeNodeModulesEnv)
		}
		t.Skipf("%s is not set; CI sets it and requires this test to pass", biomeNodeModulesEnv)
	}
	probes, err := doctor.LoadProbes(root(t), "web")
	if err != nil {
		t.Fatal(err)
	}
	biome := map[string]bool{"manifest-declared Biome overrides reach the synced config, and cannot lower a rule": true}
	for _, p := range probes {
		if p.Check == "biome-schema" || strings.Contains(strings.Join(p.Requires, " "), "node_modules/.bin/biome") {
			biome[p.Name] = true
		}
	}

	for _, component := range []string{".", "apps/admin"} {
		t.Run(component, func(t *testing.T) {
			project := biomeProject(t, component)
			appendManifest(t, &fragmentsProject{root: project, t: t}, `
[[web.biome_overrides]]
includes = ["core/**"]
[web.biome_overrides.linter.rules.style.noRestrictedImports]
level = "error"
options = { patterns = [{ group = ["node:*", "node:*/*"], message = "core runs on a runtime with no node: builtins." }] }

[[web.biome_overrides]]
includes = ["**"]
linter.rules.correctness.noUnusedFunctionParameters = "error"
`)
			git(t, project, "add", "-A")
			git(t, project, "commit", "-q", "-m", "declare overrides")
			if _, err := sync.Run(root(t), project, false); err != nil {
				t.Fatal(err)
			}
			compDir := filepath.Join(project, filepath.FromSlash(component))
			if err := os.Symlink(nm, filepath.Join(compDir, "node_modules")); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			results, err := doctor.Run(root(t), project, cfg, []string{"web"}, &out)
			if err != nil {
				t.Fatal(err)
			}
			ran := 0
			for _, r := range results {
				if !biome[r.Name] {
					continue
				}
				ran++
				if !r.OK {
					t.Errorf("with overrides present, probe %q failed: %s", r.Name, r.Detail)
				}
			}
			if ran != len(biome) {
				t.Fatalf("ran %d of the %d biome probes:\n%s", ran, len(biome), out.String())
			}

			// The per-path override is live: a node: import under core/ fails
			// `biome ci`, and the same file outside core/ does not.
			for path, wantFail := range map[string]bool{"core/uses-node.ts": true, "other/uses-node.ts": false} {
				file := filepath.Join(compDir, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("import { readFileSync } from 'node:fs'\n\nexport const read = readFileSync\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(filepath.Join(nm, ".bin", "biome"), "ci", "--colors=off", "--error-on-warnings", path)
				cmd.Dir = compDir
				got, err := cmd.CombinedOutput()
				failed := err != nil && strings.Contains(string(got), "lint/style/noRestrictedImports")
				if failed != wantFail {
					t.Errorf("%s: noRestrictedImports reported=%v, want %v:\n%s", path, failed, wantFail, got)
				}
				if !wantFail && (err != nil || !strings.Contains(string(got), "Checked 1 file")) {
					t.Errorf("%s: the control did not pass cleanly: %v\n%s", path, err, got)
				}
			}
		})
	}
}

// fragmentsProject names the e2e project type where a local `project` path
// variable shadows it.
type fragmentsProject = project
