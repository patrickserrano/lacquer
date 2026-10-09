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

// The release's secrets template used to be found in exactly one place, beside
// the destination. A consumer whose template sits elsewhere — at the component
// root, while the release writes into the app's own folder — got it silently
// skipped when keys WERE declared: the file was written with the declared keys
// only, the template's non-secret lines were never seeded, and the
// placeholder-equality check had nothing to compare against. Every one of those
// is a release that builds, signs and uploads.

// nestedTemplate is that consumer: an ios/ component, the base configuration in
// the app's folder, and the one template at the component root.
func nestedTemplate() *config.Config {
	cfg := soloConfig()
	cfg.Project.Secrets = map[string]string{"REVENUECAT_API_KEY": "RC_KEY"}
	cfg.Project.SecretsFile = "App/Secrets.xcconfig"
	cfg.Project.SecretsExample = "Secrets.xcconfig.example"
	return cfg
}

func renderReleaseAt(t *testing.T, cfg *config.Config, prefix string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, prefix))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	return out
}

func releaseStepRun(t *testing.T, rendered, name string) string {
	t.Helper()
	for _, st := range steps(t, rendered) {
		if n, _ := st["name"].(string); strings.Contains(n, name) {
			run, _ := st["run"].(string)
			return run
		}
	}
	t.Fatalf("no %q step rendered", name)
	return ""
}

// runIn executes a rendered run: body from dir, the workflow's working
// directory, with env added.
func runIn(t *testing.T, dir, run string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-c", run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Keys declared and no template anywhere: the writer used to write a keys-only
// file and exit 0. It must stop, name where it looked, and write nothing.
func TestReleaseConfigFailsClosedWithoutATemplate(t *testing.T) {
	out, written, err := runWriter(t, "", []string{"REVENUECAT_API_KEY=appl_real"}, "REVENUECAT_API_KEY")
	if err == nil {
		t.Fatalf("keys were declared, no template exists, and the release proceeded:\n%s", out)
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "::error::") {
			line = l
		}
	}
	if !strings.Contains(line, "Secrets.xcconfig.example") {
		t.Errorf("the failure does not name the path it looked in:\n%s", out)
	}
	if !strings.Contains(out, "secrets_example") {
		t.Errorf("the failure does not name the manifest key that points at a template elsewhere:\n%s", out)
	}
	if written != "" {
		t.Errorf("the writer refused but still left a file behind:\n%s", written)
	}
}

// A declared template that does not exist is the same failure, and must not
// fall back to a different file: a typo in secrets_example would otherwise pick
// up whatever happens to sit beside the destination.
func TestReleaseConfigFailsClosedOnAMissingDeclaredTemplate(t *testing.T) {
	dir := releaseWriter(t)
	writeAt(t, filepath.Join(dir, "App", "Secrets.xcconfig.example"), "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\n")
	out, err := runIn(t, dir, `scripts/write-release-config.sh --example=Missing.example App/Secrets.xcconfig REVENUECAT_API_KEY`,
		"REVENUECAT_API_KEY=appl_real")
	if err == nil {
		t.Fatalf("a declared template that does not exist was not an error:\n%s", out)
	}
	if !strings.Contains(out, "Missing.example") {
		t.Errorf("the failure does not name the declared template:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "App", "Secrets.xcconfig")); statErr == nil {
		t.Errorf("the writer fell back to the template beside the destination:\n%s", out)
	}
}

// The rendered step hands the declared template to the writer, and the writer
// both seeds from it and checks against it.
func TestReleaseStepUsesTheDeclaredTemplate(t *testing.T) {
	run := releaseStepRun(t, renderReleaseAt(t, nestedTemplate(), "ios/"), "Write release configuration")
	if !strings.Contains(run, `--example="ios/Secrets.xcconfig.example"`) {
		t.Fatalf("the step does not pass the declared, component-prefixed template:\n%s", run)
	}
	const template = "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\nAPTABASE_APP_KEY = A-DEV-0000000000\n"

	t.Run("seeds the non-secret lines", func(t *testing.T) {
		dir := releaseWriter(t)
		writeAt(t, filepath.Join(dir, "ios", "Secrets.xcconfig.example"), template)
		if out, err := runIn(t, dir, run, "REVENUECAT_API_KEY=appl_real"); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got, err := os.ReadFile(filepath.Join(dir, "ios", "App", "Secrets.xcconfig"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "APTABASE_APP_KEY = A-DEV-0000000000") {
			t.Errorf("the template's non-secret line was not seeded:\n%s", got)
		}
		if !strings.Contains(string(got), "REVENUECAT_API_KEY = appl_real") {
			t.Errorf("the declared key was not written:\n%s", got)
		}
	})
	t.Run("compares against it", func(t *testing.T) {
		dir := releaseWriter(t)
		writeAt(t, filepath.Join(dir, "ios", "Secrets.xcconfig.example"), template)
		out, err := runIn(t, dir, run, "REVENUECAT_API_KEY=appl_xxxxxxxxxxxxxxxx")
		if err == nil {
			t.Fatalf("a secret copied from the declared template passed the placeholder check:\n%s", out)
		}
		if !strings.Contains(placeholderError(out), "REVENUECAT_API_KEY") {
			t.Errorf("the failure is not the placeholder check naming the key:\n%s", out)
		}
	})
}

// Without secrets_example the step renders exactly as before, byte for byte:
// every managed repository's release.yml must not move for a feature it does
// not use.
func TestReleaseStepWithoutADeclaredTemplateIsUnchanged(t *testing.T) {
	run := secretsRun(t, withSecrets())
	if strings.Contains(run, "--example") {
		t.Errorf("a product declaring no secrets_example renders a template flag:\n%s", run)
	}
}

// A sibling that declares no secrets seeds the shared file from the template
// its owning product declared, not from a path beside it that does not exist.
func TestSiblingSeedUsesTheOwnersDeclaredTemplate(t *testing.T) {
	cfg := withSecrets()
	cfg.Product[1].SecretsExample = "Config/Shared.example"
	run := releaseStepRun(t, renderRelease(t, cfg), "Seed release configuration (Paid)")
	if !strings.Contains(run, `scripts/write-release-config.sh --example="Config/Shared.example" "Config/Monetization.xcconfig"`) {
		t.Errorf("the sibling's seed step does not use the owner's declared template:\n%s", run)
	}
}

// The watch-test job seeded Secrets.xcconfig only beside each committed example.
// A consumer whose base configuration is in the app's folder, with the template
// at the component root, got a job that died opening the base configuration —
// unless they committed a second copy of the example. It now resolves the
// template exactly as the release does.
func TestWatchJobSeedsTheDeclaredSecretsFileFromItsTemplate(t *testing.T) {
	cfg := nestedTemplate()
	cfg.Project.WatchTests = &config.WatchTests{Scheme: "W", TestTarget: "WTests"}
	raw, err := os.ReadFile(iosCIPath(t))
	if err != nil {
		t.Fatal(err)
	}
	rendered, missing := tokens.Substitute(string(raw), tokens.Values(cfg, "ios/"))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	var doc watchDoc
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered ci.yml is not valid YAML: %v", err)
	}
	var run string
	for _, st := range doc.Jobs["watch-test"].Steps {
		if strings.HasPrefix(st.Name, "Create Secrets.xcconfig") {
			run = st.Run
		}
	}
	if run == "" {
		t.Fatal("the watch-test job has no Secrets.xcconfig step")
	}
	dir := releaseWriter(t)
	writeAt(t, filepath.Join(dir, "ios", "Secrets.xcconfig.example"), "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\n")
	if err := os.MkdirAll(filepath.Join(dir, "ios", "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := runIn(t, dir, run); err != nil {
		t.Fatalf("the watch job's seed step failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "ios", "App", "Secrets.xcconfig"))
	if err != nil {
		t.Fatalf("the declared secrets_file was not seeded, so the watch build cannot open its base configuration: %v", err)
	}
	if string(got) != "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\n" {
		t.Errorf("seeded %q, want the declared template", got)
	}
}

// config accepts `@` in a secret_formats glob, because a Sentry DSN cannot be
// shaped without it, and pins that in TestSecretFormatCharsetIsExactly. The
// writer re-checks the charset and left `@` out, so a manifest declaring the
// documented DSN shape loaded cleanly and then failed every release with
// "unsafe in a shell pattern". The two sides must agree; this runs the
// rendered step, so it fails if either drifts.
func TestReleaseAcceptsTheSentryDSNShapeConfigAccepts(t *testing.T) {
	cfg := soloConfig()
	cfg.Project.Secrets = map[string]string{"SENTRY_DSN": "SENTRY_DSN"}
	cfg.Project.SecretFormats = map[string]string{"SENTRY_DSN": "https://*@*/*"}
	run := releaseStepRun(t, renderRelease(t, cfg), "Write release configuration")
	for _, tc := range []struct {
		name, value string
		wantReject  bool
	}{
		{"a real DSN", "https://abc@o0.ingest.sentry.io/1", false},
		{"no key before the host", "https://o0.ingest.sentry.io/1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := releaseWriter(t)
			writeAt(t, filepath.Join(dir, "Secrets.xcconfig.example"), "SENTRY_DSN = https:/$()/example.invalid/0\n")
			out, err := runIn(t, dir, run, "SENTRY_DSN="+tc.value)
			if rejected := err != nil; rejected != tc.wantReject {
				t.Fatalf("rejected=%v want %v:\n%s", rejected, tc.wantReject, out)
			}
			if tc.wantReject && !strings.Contains(out, "does not match its declared shape") {
				t.Errorf("rejected for the wrong reason — the shape check must be what refuses it:\n%s", out)
			}
		})
	}
}

// Without a declared template the watch job's find already seeds the file beside
// each example, which is the release's resolution too. So a project declaring
// secrets but no secrets_example must get the watch job it had before.
func TestWatchJobWithoutADeclaredTemplateIsUnchanged(t *testing.T) {
	cfg := nestedTemplate()
	cfg.Project.SecretsExample = ""
	cfg.Project.WatchTests = &config.WatchTests{Scheme: "W", TestTarget: "WTests"}
	job := tokens.CIWatchTestJob(cfg, "ios/")
	if job == "" {
		t.Fatal("no watch job rendered, so this proves nothing")
	}
	if strings.Contains(job, "write-release-config.sh") {
		t.Errorf("a project declaring no secrets_example got a new seed line in its watch job:\n%s", job)
	}
}
