package shipped

import (
	"os"
	"os/exec"
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

// A repo tagging `steps-v1.2.3` has no tag starting with `v`. Under a fixed
// 'v*' filter, adopting this workflow would mean no tag ever starts a release —
// nothing errors and nothing runs, which is the worst way for a release
// pipeline to break.
func TestReleaseTagsFollowProductPrefixes(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{
			{Name: "Steps", Scheme: "Steps", BundleID: "com.x.s", AscAppID: "1", TagPrefix: "steps-v"},
			{Name: "Lite", Scheme: "StepsFree", BundleID: "com.x.f", AscAppID: "2", TagPrefix: "stepsfree-v"},
		},
	}
	var doc struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(renderRelease(t, cfg)), &doc); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(doc.On.Push.Tags, ",")
	if got != "steps-v*,stepsfree-v*" {
		t.Errorf("tag filter = %q, want the products' prefixes", got)
	}
}

// The twelve projects that declare no products must keep the historical filter.
func TestReleaseTagsDefaultToV(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.solo", AscAppID: "1", Xcodeproj: "Solo.xcodeproj",
	}}
	if !strings.Contains(renderRelease(t, cfg), "- 'v*'") {
		t.Error("a project with no products lost the historical 'v*' tag filter")
	}
}

// Non-empty is not the same as correct. The two ways these keys actually go
// wrong — pasting the paid app's key into the free app, and leaving Google's
// public test AdMob ID in place — both produce a perfectly non-empty value that
// builds, signs, uploads and passes review.
func TestSecretFormatsRejectWrongShapedValues(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{
			{Name: "Paid", Scheme: "Paid", BundleID: "com.x.paid", AscAppID: "111", TagPrefix: "paid-v"},
			{Name: "Lite", Scheme: "Lite", BundleID: "com.x.lite", AscAppID: "222", TagPrefix: "lite-v",
				Secrets:       map[string]string{"REVENUECAT_API_KEY": "LITE_RC_KEY", "GAD_APP_ID": "LITE_GAD_ID"},
				SecretFormats: map[string]string{"REVENUECAT_API_KEY": "appl_*", "GAD_APP_ID": "ca-app-pub-*~*"}},
		},
	}
	var run string
	for _, st := range steps(t, renderRelease(t, cfg)) {
		if name, _ := st["name"].(string); strings.Contains(name, "Write release configuration") {
			run, _ = st["run"].(string)
		}
	}
	if run == "" {
		t.Fatal("no config step rendered")
	}

	for _, tc := range []struct {
		name       string
		rc, gad    string
		wantReject bool
	}{
		{"both valid", "appl_realkey", "ca-app-pub-123~456", false},
		// The paid app's RevenueCat key is a `goog_`/`appl_` sibling from another
		// app; a raw uuid is the common paste error.
		{"revenuecat key wrong shape", "8f3a-not-a-key", "ca-app-pub-123~456", true},
		// Google's public test ID has no `~unit` suffix in the form projects
		// paste, and shipping it means the app serves test ads to real users.
		{"admob id wrong shape", "appl_realkey", "ca-app-pub-3940256099942544", true},
		{"empty value", "", "ca-app-pub-123~456", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("bash", "-c", "cd "+dir+"\n"+run)
			cmd.Env = append(os.Environ(), "REVENUECAT_API_KEY="+tc.rc, "GAD_APP_ID="+tc.gad)
			out, err := cmd.CombinedOutput()
			if rejected := err != nil; rejected != tc.wantReject {
				t.Fatalf("rejected=%v want %v; output: %s", rejected, tc.wantReject, out)
			}
			// A rejection must never echo the value it rejected — CI logs are
			// broader-read than the secret itself.
			if tc.wantReject && tc.rc != "" && strings.Contains(string(out), tc.rc) {
				t.Errorf("the error printed the secret's value: %s", out)
			}
		})
	}
}
