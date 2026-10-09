package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runWriter runs the shipped writer directly against a scratch project whose
// Secrets.xcconfig.example is example, with env added to the environment.
func runWriter(t *testing.T, example string, env []string, keys ...string) (string, string, error) {
	t.Helper()
	dir := releaseWriter(t)
	if example != "" {
		if err := os.WriteFile(filepath.Join(dir, "Secrets.xcconfig.example"), []byte(example), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := append([]string{"scripts/write-release-config.sh", "Secrets.xcconfig"}, keys...)
	cmd := exec.Command("bash", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	written, _ := os.ReadFile(filepath.Join(dir, "Secrets.xcconfig"))
	return string(out), string(written), err
}

// placeholderError returns the writer's `::error::` line about placeholders, or
// "" when it printed none. Assertions read this line, not the whole log, so a
// key named in a hint cannot satisfy them.
func placeholderError(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "::error::") && strings.Contains(line, "placeholder") {
			return line
		}
	}
	return ""
}

// A value still equal to the template's placeholder is non-empty and, when the
// template was written to look like the real thing, it matches the declared
// shape too. It builds, signs and uploads, and the service it configures does
// nothing. Every presence and shape check before this one passes it.
func TestReleaseConfigRejectsADeclaredKeyLeftAtItsPlaceholder(t *testing.T) {
	const real = "APTABASE_APP_KEY = A-DEV-0000000000\n"
	for _, tc := range []struct {
		name, line, value string
	}{
		// The secret was set by copying the template's line.
		{"equal to the template", "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx", "appl_xxxxxxxxxxxxxxxx"},
		// The template is spaced differently from the line the writer emits.
		{"equal to an unspaced template", "REVENUECAT_API_KEY=appl_xxxxxxxxxxxxxxxx", "appl_xxxxxxxxxxxxxxxx"},
		{"equal to a template with trailing blanks", "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx \t\r", "appl_xxxxxxxxxxxxxxxx"},
		// The template differs, but the secret is itself an obvious placeholder.
		{"an obvious placeholder", "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx", "appl_your_key_here"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			example := tc.line + "\n" + real
			out, _, err := runWriter(t, example, []string{"REVENUECAT_API_KEY=" + tc.value}, "REVENUECAT_API_KEY=appl_*")
			if err == nil {
				t.Fatalf("a release whose REVENUECAT_API_KEY is still a placeholder was allowed to proceed:\n%s", out)
			}
			line := placeholderError(out)
			if !strings.Contains(line, "REVENUECAT_API_KEY") {
				t.Errorf("the failure does not name the placeholder key:\n%s", out)
			}
			if strings.Contains(line, "APTABASE_APP_KEY") {
				t.Errorf("a key holding a real non-secret default was reported as a placeholder:\n%s", out)
			}
			if strings.Contains(out, tc.value) {
				t.Errorf("the failure printed the value it rejected:\n%s", out)
			}
		})
	}
}

// A key added to the template after the manifest was written is never declared,
// so it is seeded and shipped as the template's text. Nothing before this check
// looks at an undeclared key's value at all.
func TestReleaseConfigRejectsAnUndeclaredKeyLeftAsSeeded(t *testing.T) {
	for _, tc := range []struct {
		name, line string
	}{
		{"your-", "SETLIST_API_KEY = your-setlist-api-key"},
		{"YOUR_", "SETLIST_API_KEY = YOUR_SETLIST_API_KEY"},
		{"CHANGE-ME in an escaped URL", "SETLIST_API_KEY = https:/$()/CHANGE-ME.example.com"},
		{"changeme", "SETLIST_API_KEY = changeme"},
		{"angle brackets", "SETLIST_API_KEY = <paste the key>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			example := "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\n" + tc.line + "\n"
			out, _, err := runWriter(t, example, []string{"REVENUECAT_API_KEY=appl_real"}, "REVENUECAT_API_KEY")
			if err == nil {
				t.Fatalf("an undeclared key left as its template placeholder was allowed to ship:\n%s", out)
			}
			line := placeholderError(out)
			if !strings.Contains(line, "SETLIST_API_KEY") {
				t.Errorf("the failure does not name the undeclared key:\n%s", out)
			}
			if strings.Contains(line, "REVENUECAT_API_KEY") {
				t.Errorf("the declared, real key was reported too:\n%s", out)
			}
		})
	}
}

// The check must not touch what is legitimately there: a declared key with a
// real value, an undeclared key whose template value is a real non-secret
// default, and an undeclared key the template leaves empty on purpose.
func TestReleaseConfigPassesRealValues(t *testing.T) {
	const example = "// your-key goes below; CHANGE-ME is fine in a comment\n" +
		"REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\n" +
		"SENTRY_DSN = https:/$()/0000000000000000000000000000000@o0.ingest.sentry.io/0\n" +
		"APTABASE_APP_KEY = A-DEV-0000000000\n" +
		"OPTIONAL_FEATURE_FLAG =\n" +
		// xcconfig ends a value at `//`, so a trailing comment is not part of it.
		"ANALYTICS_ENABLED = YES // your-key belongs in a secret, not here\n"
	out, written, err := runWriter(t, example,
		[]string{"REVENUECAT_API_KEY=appl_real", "SENTRY_DSN=https://abc@o1.ingest.sentry.io/2"},
		"REVENUECAT_API_KEY=appl_*", "SENTRY_DSN=https://*")
	if err != nil {
		t.Fatalf("a fully real release config was rejected: %v\n%s", err, out)
	}
	for _, want := range []string{
		"REVENUECAT_API_KEY = appl_real\n",
		"SENTRY_DSN = https:/$()/abc@o1.ingest.sentry.io/2\n",
		"APTABASE_APP_KEY = A-DEV-0000000000\n",
		"OPTIONAL_FEATURE_FLAG =\n",
		"ANALYTICS_ENABLED = YES // your-key belongs in a secret, not here\n",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("written config is missing %q:\n%s", want, written)
		}
	}
}

// The `//` escape rewrites a declared URL secret before it is written, so a
// comparison of the written text against the template has to see through it.
// A DSN copied from the template, unescaped, is the same placeholder.
//
// A template that wrote its URL placeholder UNESCAPED is a template bug, but for
// a declared key it is harmless (the line is replaced), and it is what a human
// copies. xcconfig would read it as `https:`, so comparing only the value
// xcconfig reads would miss the copy.
func TestReleaseConfigSeesThroughTheURLEscape(t *testing.T) {
	const escaped = "https:/$()/0000000000000000000000000000000@o0.ingest.sentry.io/0"
	const plain = "https://0000000000000000000000000000000@o0.ingest.sentry.io/0"
	for _, tc := range []struct{ name, template, value string }{
		{"escaped template, plain secret", escaped, plain},
		{"escaped template, escaped secret", escaped, escaped},
		{"unescaped template, plain secret", plain, plain},
	} {
		example := "SENTRY_DSN = " + tc.template + "\n"
		out, _, err := runWriter(t, example, []string{"SENTRY_DSN=" + tc.value}, "SENTRY_DSN")
		if err == nil {
			t.Fatalf("%s: a SENTRY_DSN equal to the template's placeholder was allowed to ship:\n%s", tc.name, out)
		}
		if !strings.Contains(placeholderError(out), "SENTRY_DSN") {
			t.Errorf("the failure does not name SENTRY_DSN as a placeholder:\n%s", out)
		}
		if strings.Contains(out, "0000000000000000000000000000000") {
			t.Errorf("the failure printed the value it rejected:\n%s", out)
		}
	}
}

// A sibling product that declares no secrets only seeds the shared file so the
// archive can find it. The keys in that file belong to the sibling that does
// declare them, so their placeholders are not this leg's to reject: checking
// them here would block every paid variant of a free app.
func TestReleaseConfigSeedOnlyLeavesTheSiblingsPlaceholders(t *testing.T) {
	const example = "ADMOB_APP_ID = your-admob-app-id\n"
	out, written, err := runWriter(t, example, nil)
	if err != nil {
		t.Fatalf("seeding a sibling's shared config with no declared keys failed: %v\n%s", err, out)
	}
	if written != example {
		t.Errorf("seed-only wrote %q, want the template verbatim", written)
	}
}

// A check that could not run must stop the release, not report "no
// placeholders". An awk shim fails only the placeholder pass (the one awk call
// that receives XCCONFIG_DECLARED) and passes every other call through, so the
// writer reaches the check with a correctly written file.
func TestReleaseConfigFailsClosedWhenThePlaceholderCheckCannotRun(t *testing.T) {
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	shim := "#!/bin/sh\n[ -n \"$XCCONFIG_DECLARED\" ] && exit 2\nexec " + awk + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "awk"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	const example = "REVENUECAT_API_KEY = appl_xxxxxxxxxxxxxxxx\n"
	out, written, err := runWriter(t, example,
		[]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "REVENUECAT_API_KEY=appl_real"},
		"REVENUECAT_API_KEY")
	if !strings.Contains(written, "REVENUECAT_API_KEY = appl_real") {
		t.Fatalf("the shim broke the write itself, so this test proves nothing:\n%s", out)
	}
	if err == nil {
		t.Fatalf("a placeholder check that could not run let the release proceed:\n%s", out)
	}
	if !strings.Contains(out, "could not read") {
		t.Errorf("the failure does not say the check itself failed:\n%s", out)
	}
}
