package gitignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func load(t *testing.T, manifest string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".lacquer.toml")
	if err := os.WriteFile(p, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A single-app project may redirect its release-time keys with
// [project].secrets_file, the single-product spelling of
// [[product]].secrets_file. The release writes that file on a runner, and
// anyone reproducing a release locally writes it into their working tree, which
// is where real keys get committed from. So it must be ignored exactly as a
// declared product's is. This read cfg.Product, the declared list only, and
// rendered no rule at all for the synthesised product.
func TestProjectSecretsFileIsIgnored(t *testing.T) {
	cfg := load(t, `
[project]
name = "Flare"
project_name = "Flare"
scheme = "Flare"
secrets_file = "Config/Keys.xcconfig"
secrets = { REVENUECAT_API_KEY = "FLARE_REVENUECAT_API_KEY" }
`)
	body, err := Body(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "\n**/Config/Keys.xcconfig") {
		t.Errorf("[project].secrets_file is not gitignored, so a locally reproduced release can commit real keys:\n%s", body)
	}
}

// The declared-product route is unchanged by reading Products(): it returns the
// declared list verbatim.
func TestProductSecretsFileIsIgnored(t *testing.T) {
	cfg := load(t, `
[project]
name = "X"
project_name = "X"
scheme = "X"

[[product]]
name = "Free"
scheme = "Free"
bundle_id = "com.x.free"
asc_app_id = "1"
secrets_file = "Config/Monetization.xcconfig"
secrets = { ADMOB_APPLICATION_ID = "ABV_ADMOB_APP_ID" }
`)
	body, err := Body(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "\n**/Config/Monetization.xcconfig") {
		t.Errorf("[[product]].secrets_file is not gitignored:\n%s", body)
	}
}

// The default file is covered by the unanchored Secrets.xcconfig rule already,
// so neither spelling adds a redundant anchored one.
func TestDefaultSecretsFileAddsNoRule(t *testing.T) {
	cfg := load(t, `
[project]
name = "Kit"
project_name = "Kit"
scheme = "Kit"
secrets = { APTABASE_APP_KEY = "KIT_APTABASE_APP_KEY" }
`)
	body, err := Body(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "**/Secrets.xcconfig") {
		t.Errorf("the default Secrets.xcconfig got a redundant anchored rule:\n%s", body)
	}
}
