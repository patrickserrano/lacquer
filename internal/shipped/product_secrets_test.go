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
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
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

// The rendered step is a CALL, and these assert the two halves that make it
// one: the workflow hands the script the file and keys the manifest declared,
// and the script itself rejects what must not ship. Asserting only the first
// would pass over a script that writes whatever it is given.
func TestSecretsStepCallsTheShippedWriter(t *testing.T) {
	run := secretsRun(t, withSecrets())
	// The path is repo-root relative because the script is a `root` asset and
	// the workflow's working directory is the repository root — a component
	// prefix on it would name a file no project has.
	if !strings.Contains(run, `scripts/write-release-config.sh "Config/Monetization.xcconfig"`) {
		t.Errorf("step does not hand the product's declared secrets_file to the shipped writer:\n%s", run)
	}
	if !strings.Contains(run, `"ADMOB_APPLICATION_ID"`) {
		t.Errorf("step does not name the key it must write:\n%s", run)
	}
}

// releaseWriter copies the shipped script into a scratch project so a rendered
// `run:` body can be executed verbatim, from the working directory the workflow
// actually uses.
func releaseWriter(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(root(t), "profiles", "ios", "root", "scripts", "write-release-config.sh")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "write-release-config.sh"), data, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func secretsRun(t *testing.T, cfg *config.Config) string {
	t.Helper()
	for _, st := range steps(t, renderRelease(t, cfg)) {
		if name, _ := st["name"].(string); strings.Contains(name, "Write release configuration") {
			run, _ := st["run"].(string)
			return run
		}
	}
	t.Fatal("no config step rendered")
	return ""
}

// An unset secret expands to the empty string, and an empty xcconfig value is
// not an error to xcodebuild — it would build, sign, upload, and be wrong. This
// is the exact shape of the rail incident: a release that produced a perfectly
// valid IPA with its runtime keys unset, rejected under Guideline 2.1(a), with
// every check green.
func TestSecretsStepFailsOnAMissingSecret(t *testing.T) {
	run := secretsRun(t, withSecrets())
	for _, tc := range []struct {
		name string
		env  []string
	}{
		{"unset", nil},
		{"set but empty", []string{"ADMOB_APPLICATION_ID="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := releaseWriter(t)
			cmd := exec.Command("bash", "-c", run)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), tc.env...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("a release with no ADMOB_APPLICATION_ID was allowed to proceed:\n%s", out)
			}
			if !strings.Contains(string(out), "ADMOB_APPLICATION_ID") {
				t.Errorf("the failure does not name the missing key:\n%s", out)
			}
		})
	}
}

// xcconfig treats `//` as the start of a comment, so a URL value truncates at
// it — `https://host` becomes `https:`. Non-empty, so every accessor that only
// checks for blank passes it through, and the service is silently
// misconfigured. The profile's own Secrets.xcconfig.example has documented the
// `/$()/` escape for years; the code that wrote REAL values did not apply it.
func TestSecretsStepEscapesURLValues(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{{
			Name: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1",
			Secrets: map[string]string{"SENTRY_DSN": "SENTRY_DSN"},
		}},
	}
	run := secretsRun(t, cfg)
	dir := releaseWriter(t)
	cmd := exec.Command("bash", "-c", run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SENTRY_DSN=https://abc@o0.ingest.sentry.io/1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("writing a URL-valued secret failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Secrets.xcconfig"))
	if err != nil {
		t.Fatal(err)
	}
	want := "SENTRY_DSN = https:/$()/abc@o0.ingest.sentry.io/1\n"
	if string(got) != want {
		t.Errorf("wrote %q, want %q — an unescaped // truncates the value at the comment marker", got, want)
	}
}

// The xcconfig is the Xcode target's BASE CONFIGURATION file, so it has to
// carry every key the project references — not only the ones held in secrets.
// Writing just the declared keys leaves the rest undefined, which is the same
// silent-empty failure one layer down.
func TestSecretsStepSeedsFromTheCommittedExample(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{{
			Name: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1",
			Secrets: map[string]string{"REVENUECAT_API_KEY": "RC_KEY"},
		}},
	}
	run := secretsRun(t, cfg)
	dir := releaseWriter(t)
	example := "// header\nREVENUECAT_API_KEY = appl_xxxxxxxx\nAPTABASE_APP_KEY = A-DEV-0000000000\n"
	if err := os.WriteFile(filepath.Join(dir, "Secrets.xcconfig.example"), []byte(example), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "REVENUECAT_API_KEY=appl_real")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Secrets.xcconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "REVENUECAT_API_KEY = appl_real") {
		t.Errorf("the declared key was not substituted:\n%s", got)
	}
	if !strings.Contains(string(got), "APTABASE_APP_KEY = A-DEV-0000000000") {
		t.Errorf("a key the template carried but the manifest does not declare was dropped:\n%s", got)
	}
}

// A sibling that declares no secrets still reads the same base configuration
// file, and a clean checkout does not have it — `xcodebuild archive` fails
// before compiling. This is a-bible-verse-each-day's
// `cp Config/Monetization.xcconfig.example …` branch, which is part of why that
// project had to keep its own release workflow.
func TestSiblingProductWithoutSecretsStillGetsItsConfig(t *testing.T) {
	var seen string
	for _, st := range steps(t, renderRelease(t, withSecrets())) {
		name, _ := st["name"].(string)
		if !strings.Contains(name, "Seed release configuration") {
			continue
		}
		seen = name
		run, _ := st["run"].(string)
		if !strings.Contains(run, `scripts/write-release-config.sh "Config/Monetization.xcconfig"`) {
			t.Errorf("the seed step does not name the shared config file:\n%s", run)
		}
		if got, _ := st["if"].(string); got != "matrix.product.name == 'Paid'" {
			t.Errorf("seed gate = %q, want the sibling's leg", got)
		}
	}
	if seen != "Seed release configuration (Paid)" {
		t.Errorf("no seed step for the product that declares no secrets (got %q)", seen)
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
	run := secretsRun(t, cfg)

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
			dir := releaseWriter(t)
			cmd := exec.Command("bash", "-c", run)
			cmd.Dir = dir
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
