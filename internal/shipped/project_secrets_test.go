package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// A flare-shaped manifest: one app, no [[product]] block, build-time keys read
// from a gitignored Secrets.xcconfig. Before [project].secrets existed this
// shape could not ask the release to write that file at all, so the archive
// shipped the placeholders: a dead paywall, no analytics, no crash reports.
const flareShaped = `
[project]
name = "Flare"
project_name = "Flare"
scheme = "Flare"
bundle_id = "com.example.flare"
asc_app_id = "1000000001"
xcodeproj = "Flare.xcodeproj"
secrets = { REVENUECAT_API_KEY = "FLARE_REVENUECAT_API_KEY", APTABASE_APP_KEY = "FLARE_APTABASE_APP_KEY" }
secret_formats = { REVENUECAT_API_KEY = "appl_*" }

[[component]]
path = "."
profiles = ["ios"]
`

// loadFlareShaped goes through config.Load, not a Config built by hand, so the
// render below is proven from the manifest text a project actually commits.
func loadFlareShaped(t *testing.T) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".lacquer.toml")
	if err := os.WriteFile(p, []byte(flareShaped), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// tokens.ProductSecrets reads Products(), so [project].secrets must render the
// same step a declared product's secrets do, with no change to the renderer.
func TestProjectSecretsRenderTheReleaseConfigStep(t *testing.T) {
	var step map[string]any
	for _, st := range steps(t, renderRelease(t, loadFlareShaped(t))) {
		if name, _ := st["name"].(string); strings.Contains(name, "Write release configuration") {
			if step != nil {
				t.Fatalf("more than one config step rendered for a single-app project")
			}
			step = st
		}
	}
	if step == nil {
		t.Fatal("[project].secrets rendered no release configuration step, so the release would ship placeholder keys")
	}
	if got, _ := step["name"].(string); got != "Write release configuration (Flare)" {
		t.Errorf("step name = %q", got)
	}
	// The synthesised product's name is the project name, which is also what
	// the release matrix carries, so the gate matches the only leg there is.
	if got, _ := step["if"].(string); got != "matrix.product.name == 'Flare'" {
		t.Errorf("gate = %q, want the synthesised product's leg", got)
	}
	env, _ := step["env"].(map[string]any)
	for k, secret := range map[string]string{
		"REVENUECAT_API_KEY": "${{ secrets.FLARE_REVENUECAT_API_KEY }}",
		"APTABASE_APP_KEY":   "${{ secrets.FLARE_APTABASE_APP_KEY }}",
	} {
		if got, _ := env[k].(string); got != secret {
			t.Errorf("env.%s = %q, want %q", k, got, secret)
		}
	}
	run, _ := step["run"].(string)
	for _, want := range []string{
		`scripts/write-release-config.sh "Secrets.xcconfig"`,
		`"APTABASE_APP_KEY"`,
		// KEY=GLOB: [project].secret_formats reached the shape check.
		`"REVENUECAT_API_KEY=appl_*"`,
	} {
		if !strings.Contains(run, want) {
			t.Errorf("run does not contain %s:\n%s", want, run)
		}
	}
}

// Rendering is not writing. Execute the rendered step against the shipped
// writer: a real key is written, and a wrong-shaped or missing one stops the
// release, which is the whole reason secret_formats has to reach it.
func TestProjectSecretsStepWritesAndGuards(t *testing.T) {
	run := secretsRun(t, loadFlareShaped(t))
	for _, tc := range []struct {
		name       string
		env        []string
		wantReject bool
	}{
		{"valid", []string{"REVENUECAT_API_KEY=appl_real", "APTABASE_APP_KEY=A-EU-123"}, false},
		{"wrong-shaped RevenueCat key", []string{"REVENUECAT_API_KEY=goog_real", "APTABASE_APP_KEY=A-EU-123"}, true},
		{"missing Aptabase key", []string{"REVENUECAT_API_KEY=appl_real"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := releaseWriter(t)
			cmd := exec.Command("bash", "-c", run)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), tc.env...)
			out, err := cmd.CombinedOutput()
			if rejected := err != nil; rejected != tc.wantReject {
				t.Fatalf("rejected=%v want %v; output: %s", rejected, tc.wantReject, out)
			}
			if tc.wantReject {
				return
			}
			got, err := os.ReadFile(filepath.Join(dir, "Secrets.xcconfig"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"REVENUECAT_API_KEY = appl_real", "APTABASE_APP_KEY = A-EU-123"} {
				if !strings.Contains(string(got), want) {
					t.Errorf("Secrets.xcconfig missing %q:\n%s", want, got)
				}
			}
		})
	}
}
