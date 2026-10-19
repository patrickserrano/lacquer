package shipped

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

// The release checks that every declared build-time key reached the ARCHIVED
// app's Info.plist with a real value, after the archive and before the export.
// A key lost between the config the release writes and the plist the app reads
// archives, signs and uploads an app whose service is unconfigured, and every
// check before this one passes it.
//
// Every case below runs the RENDERED step, as Actions runs it, against a fake
// .xcarchive holding a real binary plist, so it proves the wiring (paths,
// quoting, the archive variables) and the script together.

// Two declared keys, one per mapping route. API_KEY reaches the plist directly
// (`ApiKey = $(API_KEY)` in the source plist), so the step finds its key there.
// AD_APP_ID takes the two-hop route: a build setting reads $(AD_APP_ID) and the
// plist reads that setting, so nothing in the source plist names it and the
// manifest must.
const (
	archivedAPIValue = "appl_REAL-VALUE-NEVER-PRINTED-7f3a"
	archivedAdValue  = "ca-app-pub-REAL~VALUE-NEVER-PRINTED-91c2"
	// The template's placeholder for API_KEY. AD_APP_ID has no template line,
	// as a key the writer appends.
	archivedTemplate = "// Service keys.\nAPI_KEY = appl_xxxxxxxxxxxxxxxx\nANALYTICS_KEY = A-DEV-0000000000\n"
)

func archivedSecretsConfig() *config.Config {
	cfg := soloConfig()
	cfg.Project.Secrets = map[string]string{"API_KEY": "DEMO_API_KEY", "AD_APP_ID": "DEMO_AD_APP_ID"}
	cfg.Project.SecretPlistKeys = map[string]string{"AD_APP_ID": "AdAppIdentifier"}
	return cfg
}

// archivedSourcePlist is the app's committed Info.plist template.
const archivedSourcePlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>$(PRODUCT_BUNDLE_IDENTIFIER)</string>
	<key>ApiKey</key>
	<string>$(API_KEY)</string>
	<key>AdAppIdentifier</key>
	<string>$(AD_APP_ID_SETTING)</string>
</dict>
</plist>
`

// archivedPlist is the archived app's Info.plist as XML: the bundle id plus
// entries, each a key and the raw XML of its value.
func archivedPlist(entries ...[2]string) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\">\n<dict>\n\t<key>CFBundleIdentifier</key>\n\t<string>com.x.demo</string>\n")
	for _, e := range entries {
		b.WriteString("\t<key>" + e[0] + "</key>\n\t" + e[1] + "\n")
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func str(v string) string { return "<string>" + v + "</string>" }

// validArchivedEntries is a release whose keys all arrived.
func validArchivedEntries() [][2]string {
	return [][2]string{{"ApiKey", str(archivedAPIValue)}, {"AdAppIdentifier", str(archivedAdValue)}}
}

// archivedSecretsStep renders release.yml for cfg and returns the one step that
// runs the check, failing when there is not exactly one.
func archivedSecretsStep(t *testing.T, cfg *config.Config) map[string]string {
	t.Helper()
	var found []map[string]string
	for _, st := range steps(t, renderRelease(t, cfg)) {
		run, _ := st["run"].(string)
		if strings.Contains(run, "verify-archived-secrets.sh") {
			step := map[string]string{}
			for k, v := range st {
				if s, ok := v.(string); ok {
					step[k] = s
				}
			}
			found = append(found, step)
		}
	}
	if len(found) != 1 {
		t.Fatalf("release renders %d archived-secrets steps, want 1", len(found))
	}
	return found[0]
}

// archiveFixture lays out a scratch checkout: the shipped scripts, the template,
// the source plist, and an archive whose app is appName with the given Info.plist
// ("" for none, "-" for no app at all). It returns the checkout and the env the
// release step runs with.
func archiveFixture(t *testing.T, appName, plist string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"verify-archived-secrets.sh", "secret-placeholders.awk"} {
		data, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "root", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		writeAt(t, filepath.Join(dir, "scripts", name), string(data))
		if err := os.Chmod(filepath.Join(dir, "scripts", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeAt(t, filepath.Join(dir, "Secrets.xcconfig.example"), archivedTemplate)
	writeAt(t, filepath.Join(dir, "Demo", "Info.plist"), archivedSourcePlist)
	apps := filepath.Join(dir, "archives", "Demo.xcarchive", "Products", "Applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	if appName != "-" {
		if err := os.MkdirAll(filepath.Join(apps, appName), 0o755); err != nil {
			t.Fatal(err)
		}
		if plist != "" {
			writeAt(t, filepath.Join(apps, appName, "Info.plist"), plist)
		}
	}
	return dir, []string{"ARCHIVE_DIR=" + filepath.Join(dir, "archives"), "PRODUCT_NAME=Demo"}
}

// runArchivedStep runs the rendered step against a fixture archive and returns
// its output and exit code, failing the test if any value reached the output.
func runArchivedStep(t *testing.T, cfg *config.Config, appName, plist string) (string, int) {
	t.Helper()
	step := archivedSecretsStep(t, cfg)
	dir, env := archiveFixture(t, appName, plist)
	out, err := runIn(t, dir, step["run"], env...)
	for _, v := range []string{archivedAPIValue, archivedAdValue, "REAL-VALUE", "appl_xxxxxxxxxxxxxxxx", "your-key-here"} {
		if strings.Contains(out, v) {
			t.Fatalf("the step printed a value (%q):\n%s", v, out)
		}
	}
	return out, exitCode(err)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(interface{ ExitCode() int }); ok {
		return ee.ExitCode()
	}
	return -1
}

// errorLines is every ::error:: line, so an assertion cannot be satisfied by a
// name in a hint.
func errorLines(out string) string {
	var b strings.Builder
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "::error::") {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

func TestArchivedSecretsPassesARealRelease(t *testing.T) {
	out, code := runArchivedStep(t, archivedSecretsConfig(), "Demo.app", binaryPlist(t, archivedPlist(validArchivedEntries()...)))
	if code != 0 {
		t.Fatalf("a release whose keys all reached the archive failed (exit %d):\n%s", code, out)
	}
	// The log says which keys were checked, by name and plist key.
	mustContain(t, "the output", out, "AD_APP_ID (AdAppIdentifier)", "API_KEY (ApiKey)", "checked 2 of 2 declared key(s)")
	if strings.Contains(out, "::error::") || strings.Contains(out, "::warning::") {
		t.Errorf("a clean release printed an annotation:\n%s", out)
	}
}

// Each way a resolved key can fail to arrive fails the release now, names the
// key and its plist key, and prints no value. The other key stays valid, so the
// failure is pinned to the one that broke.
func TestArchivedSecretsFailsAKeyThatDidNotArrive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries [][2]string
		want    string
	}{
		{"missing", [][2]string{{"AdAppIdentifier", str(archivedAdValue)}},
			"API_KEY: Info.plist key ApiKey is not in the archived Info.plist"},
		{"missing under its declared plist key", [][2]string{{"ApiKey", str(archivedAPIValue)}},
			"AD_APP_ID: Info.plist key AdAppIdentifier is not in the archived Info.plist"},
		{"empty", [][2]string{{"ApiKey", str("")}, {"AdAppIdentifier", str(archivedAdValue)}},
			"API_KEY: Info.plist key ApiKey is empty"},
		{"blank", [][2]string{{"ApiKey", str("  \t ")}, {"AdAppIdentifier", str(archivedAdValue)}},
			"API_KEY: Info.plist key ApiKey is empty"},
		{"not a string", [][2]string{{"ApiKey", "<dict><key>v</key>" + str(archivedAPIValue) + "</dict>"}, {"AdAppIdentifier", str(archivedAdValue)}},
			"API_KEY: Info.plist key ApiKey is not a string"},
		{"equal to its template placeholder", [][2]string{{"ApiKey", str("appl_xxxxxxxxxxxxxxxx")}, {"AdAppIdentifier", str(archivedAdValue)}},
			"API_KEY: Info.plist key ApiKey in the archived Demo.app still holds a placeholder"},
		{"an obvious placeholder", [][2]string{{"ApiKey", str(archivedAPIValue)}, {"AdAppIdentifier", str("your-key-here")}},
			"AD_APP_ID: Info.plist key AdAppIdentifier in the archived Demo.app still holds a placeholder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runArchivedStep(t, archivedSecretsConfig(), "Demo.app", binaryPlist(t, archivedPlist(tc.entries...)))
			if code != 1 {
				t.Fatalf("exit %d, want 1 (a key failed):\n%s", code, out)
			}
			mustContain(t, "the ::error:: lines", errorLines(out), tc.want)
			other := "AD_APP_ID:"
			if strings.HasPrefix(tc.want, "AD_APP_ID") {
				other = "API_KEY:"
			}
			if strings.Contains(errorLines(out), other) {
				t.Errorf("the key that arrived intact was reported too:\n%s", out)
			}
		})
	}
}

// An archive this step cannot read fails closed, with exit 2, never as "nothing
// to check".
func TestArchivedSecretsFailsClosedOnAnUnreadableArchive(t *testing.T) {
	for _, tc := range []struct {
		name, app, plist, want string
	}{
		{"plist does not parse", "Demo.app", "not a property list\x00\x01", "the archived Info.plist does not parse"},
		{"plist is not a dictionary", "Demo.app", binaryPlist(t, `<?xml version="1.0"?><plist version="1.0"><array><string>x</string></array></plist>`), "is not a dictionary"},
		{"plist has no bundle id", "Demo.app", binaryPlist(t, `<?xml version="1.0"?><plist version="1.0"><dict><key>ApiKey</key>`+str(archivedAPIValue)+`</dict></plist>`), "is not a dictionary with a CFBundleIdentifier"},
		{"app has no Info.plist", "Demo.app", "", "Demo.app in the archive has no Info.plist"},
		{"app missing from the archive", "-", "", "the archive holds no Demo.app"},
		{"only another app in the archive", "Other.app", binaryPlist(t, archivedPlist(validArchivedEntries()...)), "the archive holds no Demo.app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runArchivedStep(t, archivedSecretsConfig(), tc.app, tc.plist)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (a control failed):\n%s", code, out)
			}
			mustContain(t, "the ::error:: lines", errorLines(out), tc.want)
		})
	}
}

// A declared key no source plist references and the manifest does not map
// cannot be checked. Before the gate date that is a named warning and the
// release goes on; from it, the release fails. Both sides, from the rendered
// step, so the date the workflow carries is the one under test.
func TestArchivedSecretsUnresolvedKeyWarnsThenFails(t *testing.T) {
	cfg := archivedSecretsConfig()
	cfg.Project.SecretPlistKeys = nil // AD_APP_ID now resolves nowhere
	plist := binaryPlist(t, archivedPlist(validArchivedEntries()...))
	saved := tokens.ArchivedSecretsGateFrom
	t.Cleanup(func() { tokens.ArchivedSecretsGateFrom = saved })
	today := time.Now().UTC().Truncate(24 * time.Hour)

	tokens.ArchivedSecretsGateFrom = today.AddDate(0, 0, 1)
	out, code := runArchivedStep(t, cfg, "Demo.app", plist)
	if code != 0 {
		t.Fatalf("before the gate date an unresolved key failed the release (exit %d):\n%s", code, out)
	}
	mustContain(t, "the output", out,
		"::warning::verify-archived-secrets: AD_APP_ID: no Info.plist key is known to carry it",
		`Declare secret_plist_keys.AD_APP_ID = "<Info.plist key>" in .lacquer.toml`,
		"this fails releases from "+tokens.ArchivedSecretsGateFrom.Format("2006-01-02"),
		"checked 1 of 2 declared key(s)")
	if errorLines(out) != "" {
		t.Errorf("before the gate date the step printed an error:\n%s", out)
	}

	for _, from := range []time.Time{today, today.AddDate(0, 0, -1)} {
		tokens.ArchivedSecretsGateFrom = from
		out, code = runArchivedStep(t, cfg, "Demo.app", plist)
		if code != 1 {
			t.Fatalf("on/after the gate date (%s) an unresolved key passed (exit %d):\n%s", from.Format("2006-01-02"), code, out)
		}
		mustContain(t, "the ::error:: lines", errorLines(out), "AD_APP_ID: no Info.plist key is known to carry it", "Declare secret_plist_keys.AD_APP_ID")
	}
}

// The grace is for a key nobody can locate, not for a key that broke: a
// resolved key that did not arrive fails even before the gate date.
func TestArchivedSecretsGraceDoesNotCoverAResolvedKey(t *testing.T) {
	saved := tokens.ArchivedSecretsGateFrom
	t.Cleanup(func() { tokens.ArchivedSecretsGateFrom = saved })
	tokens.ArchivedSecretsGateFrom = time.Now().UTC().AddDate(1, 0, 0)
	out, code := runArchivedStep(t, archivedSecretsConfig(), "Demo.app", binaryPlist(t, archivedPlist([2]string{"AdAppIdentifier", str(archivedAdValue)})))
	if code != 1 {
		t.Fatalf("a missing resolved key passed inside the grace period (exit %d):\n%s", code, out)
	}
	mustContain(t, "the ::error:: lines", errorLines(out), "API_KEY: Info.plist key ApiKey is not in the archived Info.plist")
}

// The gate date ships as the one constant, and the rendered step carries it.
func TestArchivedSecretsGateDateIsRendered(t *testing.T) {
	if got := tokens.ArchivedSecretsGateFrom.Format("2006-01-02"); got != "2026-11-15" {
		t.Errorf("ArchivedSecretsGateFrom = %s, want 2026-11-15", got)
	}
	mustContain(t, "the rendered step", archivedSecretsStep(t, archivedSecretsConfig())["run"], "--unresolved-fails-from 2026-11-15 ")
}

// A key the source plist carries only INSIDE a longer value is not resolved:
// the archived value would not be the secret's, so neither "empty" nor the
// placeholder test could be judged on it.
func TestArchivedSecretsDoesNotResolveAPartialReference(t *testing.T) {
	cfg := archivedSecretsConfig()
	step := archivedSecretsStep(t, cfg)
	dir, env := archiveFixture(t, "Demo.app", binaryPlist(t, archivedPlist(append(validArchivedEntries(), [2]string{"Endpoint", str("https://real.example/x")})...)))
	writeAt(t, filepath.Join(dir, "Demo", "Info.plist"), strings.Replace(archivedSourcePlist,
		"<string>$(API_KEY)</string>", "<string>https://$(API_KEY)/x</string>", 1))
	out, err := runIn(t, dir, step["run"], env...)
	if err == nil && !strings.Contains(out, "API_KEY: no Info.plist key is known to carry it") {
		t.Fatalf("a partial $(API_KEY) reference was taken as its plist key:\n%s", out)
	}
	if strings.Contains(out, "API_KEY (ApiKey)") || strings.Contains(out, "API_KEY (Endpoint)") {
		t.Errorf("a key the source plist does not carry exactly was reported checked:\n%s", out)
	}
}

// The step renders after the archive's build numbers are verified and before
// the export, once per product declaring secrets, gated on its own leg and
// reading its own app; a product declaring none gets no step.
func TestArchivedSecretsStepPlacementAndProducts(t *testing.T) {
	cfg := twoIOSProducts()
	cfg.Product[1].Secrets = map[string]string{"AD_APP_ID": "FREE_AD_APP_ID", "API_KEY": "FREE_API_KEY"}
	cfg.Product[1].SecretPlistKeys = map[string]string{"AD_APP_ID": "AdAppIdentifier"}
	cfg.Product[1].SecretsFile = "Config/Monetization.xcconfig"
	step := archivedSecretsStep(t, cfg)
	if step["if"] != "matrix.product.name == 'Free'" {
		t.Errorf("if = %q, want the Free leg only", step["if"])
	}
	want := `scripts/verify-archived-secrets.sh --example "Config/Monetization.xcconfig.example" --sources "." --unresolved-fails-from ` +
		tokens.ArchivedSecretsGateFrom.Format("2006-01-02") +
		` "$ARCHIVE_DIR/$PRODUCT_NAME.xcarchive" "Sample Reader Daily Free.app" "AD_APP_ID=AdAppIdentifier" "API_KEY"`
	if step["run"] != want {
		t.Errorf("runs\n  %s\nwant\n  %s", step["run"], want)
	}

	cfg.Product[0].Secrets = map[string]string{"API_KEY": "PAID_API_KEY"}
	body := renderRelease(t, cfg)
	if n := strings.Count(body, "scripts/verify-archived-secrets.sh "); n != 2 {
		t.Errorf("two products declaring secrets render %d steps, want 2", n)
	}
	mustContain(t, "the release", body, "if: matrix.product.name == 'Paid'\n        run: scripts/verify-archived-secrets.sh", `"Sample Reader Daily.app" "API_KEY"`)

	order := []string{"- name: Build and archive\n", "- name: Verify the archived build number of every bundle\n",
		"- name: Verify build-time keys reached the archived Info.plist (Paid)\n",
		"- name: Verify build-time keys reached the archived Info.plist (Free)\n", "- name: Export IPA\n"}
	last := -1
	for _, o := range order {
		i := strings.Index(body, o)
		if i < 0 || i < last {
			t.Fatalf("%q is missing or out of order (at %d, previous at %d)", strings.TrimSpace(o), i, last)
		}
		last = i
	}
}

// A nested component reads its template and source tree under its prefix.
func TestArchivedSecretsStepUnderAComponentPrefix(t *testing.T) {
	run := releaseStepRun(t, renderReleaseAt(t, archivedSecretsConfig(), "ios/"), "Verify build-time keys reached the archived Info.plist")
	mustContain(t, "the step", run, `--example "ios/Secrets.xcconfig.example" --sources "ios" `)
}

// A project declaring no build-time keys renders a release.yml byte-identical
// to the one it had before this step existed: the template with the token
// removed. The token is the only thing this step added to the template.
func TestNoSecretsRendersAnUnchangedRelease(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "{{IOS_RELEASE_ARCHIVED_SECRETS}}"); n != 1 {
		t.Fatalf("release.yml carries the token %d times, want 1; this test pins nothing", n)
	}
	before := strings.ReplaceAll(string(raw), "{{IOS_RELEASE_ARCHIVED_SECRETS}}", "")
	for name, cfg := range map[string]*config.Config{"solo": soloConfig(), "two products": twoIOSProducts()} {
		got := renderIOSWorkflow(t, "release.yml", cfg, "")
		if strings.Contains(got, "verify-archived-secrets") {
			t.Errorf("%s: renders the archived-secrets step for a project declaring no secrets", name)
		}
		if want := renderIOSWorkflowFrom(t, before, cfg); got != want {
			t.Errorf("%s: release.yml changed for a project declaring no secrets.\n%s", name, firstDiff(want, got))
		}
	}
}

func TestArchivedSecretsScriptIsExecutable(t *testing.T) {
	path := filepath.Join(root(t), "profiles", "ios", "root", "scripts", "verify-archived-secrets.sh")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable (%v)", path, fi.Mode())
	}
	if b, _ := os.ReadFile(path); !regexp.MustCompile(`\A#!/usr/bin/env bash\n`).Match(b) {
		t.Fatal("the script does not start with a bash shebang")
	}
}

// The writer and the archive check judge placeholders with one program. If the
// shared file goes missing the writer must stop before the release archives,
// not skip the check.
func TestReleaseWriterFailsWithoutThePlaceholderRules(t *testing.T) {
	dir := releaseWriter(t)
	if err := os.Remove(filepath.Join(dir, "scripts", "secret-placeholders.awk")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(dir, "Secrets.xcconfig.example"), archivedTemplate)
	out, err := runIn(t, dir, "scripts/write-release-config.sh Secrets.xcconfig API_KEY", "API_KEY="+archivedAPIValue)
	if err == nil {
		t.Fatalf("the writer ran without its placeholder rules:\n%s", out)
	}
	mustContain(t, "the output", out, "secret-placeholders.awk is missing")
	if strings.Contains(out, archivedAPIValue) {
		t.Fatalf("printed a value:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "Secrets.xcconfig")); err == nil {
		t.Error("the writer wrote the credentials file before refusing")
	}
}
