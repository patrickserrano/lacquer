package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// loadManifest writes manifest into dir as .lacquer.toml and loads it the way
// `lacquer audit` does, so these tests cover the TOML -> Products() path rather
// than a Config assembled by hand.
func loadManifest(t *testing.T, dir, manifest string) *config.Config {
	t.Helper()
	p := filepath.Join(dir, ".lacquer.toml")
	if err := os.WriteFile(p, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const singleAppSecrets = `
[project]
name = "Flare"
project_name = "Flare"
scheme = "Flare"
secrets = { REVENUECAT_API_KEY = "FLARE_REVENUECAT_API_KEY", SENTRY_DSN = "FLARE_SENTRY_DSN" }
`

// The #319 audit reads Products(), so a single-app project declaring
// [project].secrets must be seen by it with no change to the detector itself.
// If it were not, the one check that notices "declared, but nothing writes
// them" would be blind to exactly the projects this spelling exists for.
func TestProjectSecretsWithNoWriterAreReported(t *testing.T) {
	dir := inertRepo(t, "jobs:\n  release:\n    steps:\n      - run: xcodebuild archive\n")
	fs := InertSecretDeclarations(dir, loadManifest(t, dir, singleAppSecrets))
	if len(fs) != 1 {
		t.Fatalf("[project].secrets with nothing writing them was not reported: %+v", fs)
	}
	if fs[0].Product != "Flare" {
		t.Errorf("Product = %q, want the project name", fs[0].Product)
	}
	if got := strings.Join(fs[0].Keys, ","); got != "REVENUECAT_API_KEY,SENTRY_DSN" {
		t.Errorf("Keys = %q", got)
	}
	// The report must send the reader to the table they actually wrote. A
	// single-app manifest has no [[product]] block, so naming one would send
	// them looking for a line that does not exist.
	out := FormatInertSecrets(fs)
	if !strings.Contains(out, "[project] declares 2 secret(s)") {
		t.Errorf("report does not name [project] as the declaring table:\n%s", out)
	}
	if strings.Contains(out, "[[product]]") {
		t.Errorf("report names a [[product]] block the manifest does not have:\n%s", out)
	}
}

func TestProjectSecretsWithAWriterAreQuiet(t *testing.T) {
	dir := inertRepo(t, "jobs:\n  release:\n    steps:\n      - name: Write release configuration (Flare)\n        run: scripts/write-release-config.sh \"Secrets.xcconfig\"\n")
	if fs := InertSecretDeclarations(dir, loadManifest(t, dir, singleAppSecrets)); len(fs) != 0 {
		t.Fatalf("a single-app project whose release DOES write its secrets was flagged: %+v", fs)
	}
}

// A declared product keeps its own label: the wording change is for the
// synthesised product only.
func TestDeclaredProductSecretsKeepTheirLabel(t *testing.T) {
	out := FormatInertSecrets(InertSecretDeclarations(inertRepo(t, ""), cfgWithSecrets(false)))
	if !strings.Contains(out, "[[product]] Probe declares 2 secret(s)") {
		t.Errorf("a declared product is no longer named as one:\n%s", out)
	}
}
