package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

func renderRelease(t *testing.T, cfg *config.Config) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg.Project, "", cfg.Products()))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	return out
}

func steps(t *testing.T, rendered string) []map[string]any {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered release workflow is not valid YAML: %v", err)
	}
	return doc.Jobs["build-and-deploy"].Steps
}

func withSecrets() *config.Config {
	return &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{
			{Name: "Paid", Scheme: "Paid", BundleID: "com.x.paid", AscAppID: "111", TagPrefix: "paid"},
			{Name: "Free", Scheme: "Free", BundleID: "com.x.free", AscAppID: "222", TagPrefix: "free",
				SecretsFile: "Config/Monetization.xcconfig",
				Secrets:     map[string]string{"ADMOB_APPLICATION_ID": "ABV_ADMOB_APP_ID"}},
		},
	}
}

// The common case renders NOTHING. Twelve projects declare no product secrets,
// and a stray blank step would be a syntax error in every one of them.
func TestNoSecretsRendersNoStep(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.solo", AscAppID: "1", Xcodeproj: "Solo.xcodeproj",
	}}
	for _, st := range steps(t, renderRelease(t, cfg)) {
		if name, _ := st["name"].(string); strings.Contains(name, "Write release configuration") {
			t.Errorf("a project with no product secrets got a config step: %q", name)
		}
	}
}

// A product's keys must never reach another product's build: shipping the free
// app's ad unit IDs inside the paid app is not a build failure, it is a bad
// release.
func TestSecretsStepIsGatedToItsProduct(t *testing.T) {
	var found bool
	for _, st := range steps(t, renderRelease(t, withSecrets())) {
		name, _ := st["name"].(string)
		if !strings.Contains(name, "Write release configuration") {
			continue
		}
		found = true
		if name != "Write release configuration (Free)" {
			t.Errorf("unexpected config step %q — only Free declares secrets", name)
		}
		// Single quotes: a GitHub expression takes single-quoted literals only,
		// and `== "Free"` is a syntax error there, not a failed match.
		if got, _ := st["if"].(string); got != "matrix.product.name == 'Free'" {
			t.Errorf("gate = %q, want single-quoted product match", got)
		}
	}
	if !found {
		t.Error("no config step rendered for a product that declares secrets")
	}
}

// An unset secret expands to the empty string, and an empty xcconfig value is
// not an error to xcodebuild — it would build, sign, upload, and be wrong.
func TestSecretsStepFailsOnEmptySecret(t *testing.T) {
	var run string
	for _, st := range steps(t, renderRelease(t, withSecrets())) {
		if name, _ := st["name"].(string); strings.Contains(name, "Write release configuration") {
			run, _ = st["run"].(string)
		}
	}
	if !strings.Contains(run, `${ADMOB_APPLICATION_ID:?`) {
		t.Errorf("step does not fail on an empty secret:\n%s", run)
	}
	if !strings.Contains(run, "umask 077") {
		t.Errorf("credentials written without a umask on a runner whose disk outlives the job:\n%s", run)
	}
	// The real value must never appear in the tree; only the secret's name does.
	if !strings.Contains(run, `> "Config/Monetization.xcconfig"`) {
		t.Errorf("step does not write the product's declared secrets_file:\n%s", run)
	}
}
