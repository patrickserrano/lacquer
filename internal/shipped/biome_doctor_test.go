package shipped

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/doctor"
)

// biomeNodeModulesEnv names a node_modules directory holding an installed
// @biomejs/biome. The test below needs a REAL biome, and installing one is a
// network fetch, so the Go suite never does it: CI's test job installs a pinned
// version and points this at it, then requires the test to have run and passed
// (a skip there is a failure). Locally it skips unless you set it.
const biomeNodeModulesEnv = "LACQUER_TEST_BIOME_NODE_MODULES"

// biomeControls is, for each shipped biome probe that lints a fixture, the same
// fixture with its violation removed. The probe failing proves the check can
// reject; the control passing proves that rejection came from the violation and
// not from anything else about the fixture, the config or where it was written.
// A new biome fixture probe without an entry here fails the test.
var biomeControls = map[string]string{
	"biome ci rejects an error-severity lint violation": "export const value: number = 1\n",
	"biome ci rejects a WARNING-severity violation":     "const maybe: string | undefined = process.env.HOME\nexport const value = maybe ?? ''\n",
	"biome ci rejects unformatted code":                 "export const value = 'single quoted and well spaced'\n",
}

// TestDoctorWebBiomeProbesAgainstRealBiome runs the shipped web profile's biome
// probes through doctor, against a real biome, in a synced component.
//
// Biome 2.5.15 crashed every one of them ("path is expected to be under the
// root", exit 101) because the fixture sat outside vcs.root; 2.5.14 did not.
// TestDoctorWebProbesNeedAnInstall explains why the e2e suite cannot run the web
// probes in general. This test runs the biome ones by borrowing one install
// instead of a per-test `pnpm install`.
func TestDoctorWebBiomeProbesAgainstRealBiome(t *testing.T) {
	nm := os.Getenv(biomeNodeModulesEnv)
	if nm == "" {
		if os.Getenv("LACQUER_TEST_REQUIRE_BIOME") != "" {
			t.Fatalf("LACQUER_TEST_REQUIRE_BIOME is set but %s is not, so the biome probes would go unproved", biomeNodeModulesEnv)
		}
		t.Skipf("%s is not set; CI sets it and requires this test to pass", biomeNodeModulesEnv)
	}
	pkg, err := os.ReadFile(filepath.Join(nm, "@biomejs", "biome", "package.json"))
	if err != nil {
		t.Fatalf("%s=%s holds no @biomejs/biome: %v", biomeNodeModulesEnv, nm, err)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkg, &meta); err != nil || meta.Version == "" {
		t.Fatalf("could not read the installed biome's version: %v", err)
	}
	t.Logf("biome %s", meta.Version)

	probes, err := doctor.LoadProbes(root(t), "web")
	if err != nil {
		t.Fatal(err)
	}
	biome := map[string]bool{}
	var fixtures []doctor.Probe
	for _, p := range probes {
		if p.Check == "biome-schema" || strings.Contains(strings.Join(p.Requires, " "), "node_modules/.bin/biome") {
			biome[p.Name] = true
		}
		if p.File != "" && strings.Contains(strings.Join(p.Argv, " "), "node_modules/.bin/biome") {
			clean, ok := biomeControls[p.Name]
			if !ok {
				t.Fatalf("web probe %q lints a fixture with biome but has no entry in biomeControls", p.Name)
			}
			p.Content = clean
			p.Expect = "pass"
			// Exit 0 AND biome saying it read the one file. A run that matched
			// nothing also exits 0 under some flags, and would prove nothing.
			p.ExpectOutput = `(?s)Checked 1 file`
			fixtures = append(fixtures, p)
		}
	}
	if len(fixtures) != len(biomeControls) {
		t.Fatalf("found %d biome fixture probes but biomeControls has %d entries; one was renamed or removed", len(fixtures), len(biomeControls))
	}

	// The controls run from a lacquer root holding only them.
	controlRoot := t.TempDir()
	f, err := os.Create(filepath.Join(mkdir(t, filepath.Join(controlRoot, "profiles", "web")), "doctor.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := toml.NewEncoder(f).Encode(struct {
		Probe []doctor.Probe `toml:"probe"`
	}{fixtures}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	for _, component := range []string{".", "apps/admin"} {
		t.Run(component, func(t *testing.T) {
			project := biomeProject(t, component)
			if err := os.Symlink(nm, filepath.Join(project, filepath.FromSlash(component), "node_modules")); err != nil {
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
					t.Errorf("shipped probe %q failed on biome %s: %s", r.Name, meta.Version, r.Detail)
				}
			}
			if ran != len(biome) {
				t.Fatalf("ran %d of the %d biome probes:\n%s", ran, len(biome), out.String())
			}

			out.Reset()
			results, err = doctor.Run(controlRoot, project, cfg, []string{"web"}, &out)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != len(fixtures) {
				t.Fatalf("ran %d controls, want %d:\n%s", len(results), len(fixtures), out.String())
			}
			for _, r := range results {
				if !r.OK {
					t.Errorf("control for %q (violation removed) did not pass on biome %s, so the probe's failure is not down to its violation: %s", r.Name, meta.Version, r.Detail)
				}
			}

			// Nothing left behind: the fixtures were written inside the component.
			entries, err := os.ReadDir(filepath.Join(project, filepath.FromSlash(component)))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".lacquer-doctor-") {
					t.Errorf("doctor left %s in the component", e.Name())
				}
			}
		})
	}
}

func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
