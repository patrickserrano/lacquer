package shipped

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// These pin scripts/verify-archive-info-plist.sh and the release step that runs
// it: after the archive, read the BUILT app's Info.plist and refuse the release
// when a build-time key reached it empty, unexpanded, or as a placeholder.
//
// The defect they exist for: write-release-config.sh checked a secret's VALUE
// and nothing checked that the value reached the product. Flare archived with
// REVENUECAT_PUBLIC_SDK_KEY = REPLACE_ME_APPL_KEY, and kit and port-of-entry —
// which declare no secrets at all — would archive with their keys empty, every
// gate green.

const archiveCheckStep = "Verify build-time keys reached the archive"

func archiveSteps(t *testing.T, cfg *config.Config) (names []string, runs map[string]string, gates map[string]string) {
	t.Helper()
	runs, gates = map[string]string{}, map[string]string{}
	for _, st := range steps(t, renderRelease(t, cfg)) {
		name, _ := st["name"].(string)
		names = append(names, name)
		if strings.HasPrefix(name, archiveCheckStep) {
			runs[name], _ = st["run"].(string)
			gates[name], _ = st["if"].(string)
		}
	}
	return names, runs, gates
}

// The repositories actually at risk declare NOTHING, so the check has to render
// where no secrets are declared — the opposite of the writer step. A project
// with no [[product]] gets exactly one, gated on its synthesised product.
func TestArchiveCheckRendersWithoutDeclaredSecrets(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.solo", AscAppID: "1", Xcodeproj: "Solo.xcodeproj",
	}}
	names, runs, gates := archiveSteps(t, cfg)
	want := archiveCheckStep + " (Solo)"
	run, ok := runs[want]
	if !ok || len(runs) != 1 {
		t.Fatalf("want exactly one %q step, got %v", want, names)
	}
	if gates[want] != "matrix.product.name == 'Solo'" {
		t.Errorf("gate = %q", gates[want])
	}
	for _, s := range []string{
		"scripts/verify-archive-info-plist.sh",
		`--archive "$ARCHIVE_DIR/$PRODUCT_NAME.xcarchive"`,
		`--project "Solo.xcodeproj"`,
		`--scheme "$PRODUCT_SCHEME"`,
		`--example "Secrets.xcconfig.example"`,
	} {
		if !strings.Contains(run, s) {
			t.Errorf("run is missing %s:\n%s", s, run)
		}
	}
}

// Placement is the guard's whole value: after the archive exists, and before
// the IPA is exported, let alone uploaded. A check that ran after upload would
// report a bad build to nobody who could still stop it.
func TestArchiveCheckRunsBetweenArchiveAndExport(t *testing.T) {
	names, _, _ := archiveSteps(t, withSecrets())
	idx := func(name string) int {
		for i, n := range names {
			if n == name {
				return i
			}
		}
		t.Fatalf("no step %q in %v", name, names)
		return -1
	}
	archive, export := idx("Build and archive"), idx("Export IPA")
	for _, p := range []string{"Paid", "Free"} {
		if i := idx(archiveCheckStep + " (" + p + ")"); i < archive || i > export {
			t.Errorf("%s check at step %d, want between archive (%d) and export (%d)", p, i, archive, export)
		}
	}
}

// Every product leg gets exactly one check, gated to it, carrying only ITS
// declared keys and shapes. A leg with no check would be a release the guard
// never looked at, and a leg checking a sibling's keys would fail on keys it
// never had.
func TestArchiveCheckCarriesEachProductsDeclaredKeys(t *testing.T) {
	cfg := withSecrets()
	cfg.Product[1].SecretFormats = map[string]string{"ADMOB_APPLICATION_ID": "ca-app-pub-*~*"}
	cfg.Product[1].Secrets["REVENUECAT_API_KEY"] = "ABV_RC"
	_, runs, gates := archiveSteps(t, cfg)
	if len(runs) != 2 {
		t.Fatalf("want one check per product, got %v", runs)
	}
	free := runs[archiveCheckStep+" (Free)"]
	paid := runs[archiveCheckStep+" (Paid)"]
	if gates[archiveCheckStep+" (Free)"] != "matrix.product.name == 'Free'" ||
		gates[archiveCheckStep+" (Paid)"] != "matrix.product.name == 'Paid'" {
		t.Errorf("gates = %v", gates)
	}
	// Sorted, shaped where a format is declared, bare where it is not.
	if !strings.Contains(free, `"ADMOB_APPLICATION_ID=ca-app-pub-*~*" \`+"\n"+`  "REVENUECAT_API_KEY"`) {
		t.Errorf("Free's check does not carry its keys and shapes in order:\n%s", free)
	}
	if strings.Contains(paid, "ADMOB_APPLICATION_ID") || strings.Contains(paid, "REVENUECAT_API_KEY") {
		t.Errorf("Paid's check names a key only Free declares:\n%s", paid)
	}
	// Both legs compare against every example the project has: Paid archives
	// against the Monetization base configuration too.
	for name, run := range map[string]string{"Free": free, "Paid": paid} {
		for _, e := range []string{`--example "Config/Monetization.xcconfig.example"`, `--example "Secrets.xcconfig.example"`} {
			if !strings.Contains(run, e) {
				t.Errorf("%s: missing %s:\n%s", name, e, run)
			}
		}
	}
}

// ---- the script, run for real against fixture archives ----

// shippedScripts copies the release scripts into a scratch repository root, as
// a sync would lay them out.
func shippedScripts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"verify-archive-info-plist.sh", "secret-placeholders.sh", "write-release-config.sh"} {
		data, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "root", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scripts", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// plistXML renders a flat string dictionary as an XML property list.
func plistXML(entries map[string]string) string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "\t<key>%s</key>\n\t<string>%s</string>\n", xmlEscape(k), xmlEscape(entries[k]))
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixtureArchive writes an .xcarchive whose app carries the given Info.plist
// entries, shaped the way `xcodebuild archive` lays one out, and returns its
// path. The app name has a space because real ones do.
func fixtureArchive(t *testing.T, dir string, built map[string]string) string {
	t.Helper()
	archive := filepath.Join(dir, "build", "Demo.xcarchive")
	writeFile(t, filepath.Join(archive, "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>ApplicationProperties</key>
	<dict>
		<key>ApplicationPath</key>
		<string>Applications/Demo App.app</string>
	</dict>
</dict>
</plist>
`)
	writeFile(t, filepath.Join(archive, "Products", "Applications", "Demo App.app", "Info.plist"), plistXML(built))
	return archive
}

// verify runs the shipped script from the scratch root and returns its output.
func verify(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"scripts/verify-archive-info-plist.sh"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The Info.plist a typical fleet app ships: Apple's own settings, plus the
// project's keys referenced as build settings.
var sourcePlist = map[string]string{
	"CFBundleIdentifier": "$(PRODUCT_BUNDLE_IDENTIFIER)",
	"CFBundleVersion":    "$(CURRENT_PROJECT_VERSION)",
	"RevenueCatAPIKey":   "$(REVENUECAT_API_KEY)",
	"AptabaseAppKey":     "$(APTABASE_APP_KEY)",
}

func builtPlist(overrides map[string]string) map[string]string {
	out := map[string]string{
		"CFBundleIdentifier": "com.x.demo",
		"CFBundleVersion":    "42",
		"RevenueCatAPIKey":   "appl_R3alKeyValue9",
		"AptabaseAppKey":     "A-EU-4812093751",
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// A release whose keys all arrived passes, says what it checked — and prints
// no value, not even a correct one.
func TestArchiveCheckPassesRealKeys(t *testing.T) {
	dir := shippedScripts(t)
	archive := fixtureArchive(t, dir, builtPlist(nil))
	writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
	out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist", "REVENUECAT_API_KEY=appl_*")
	if err != nil {
		t.Fatalf("a correctly wired archive was refused: %v\n%s", err, out)
	}
	for _, s := range []string{
		"ok — REVENUECAT_API_KEY (declared) reached Demo App.app/Info.plist at RevenueCatAPIKey",
		"ok — APTABASE_APP_KEY (undeclared) reached",
		"checked 2 build-setting reference(s) in Demo App.app/Info.plist; 0 failed",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	// Apple's own settings are not a project's keys and are not reported.
	if strings.Contains(out, "PRODUCT_BUNDLE_IDENTIFIER") || strings.Contains(out, "CURRENT_PROJECT_VERSION") {
		t.Errorf("a standard Xcode setting was checked:\n%s", out)
	}
	for _, v := range []string{"appl_R3alKeyValue9", "A-EU-4812093751"} {
		if strings.Contains(out, v) {
			t.Errorf("the output printed a secret's value %q:\n%s", v, out)
		}
	}
}

// Each way a DECLARED key reaches the archive wrong, and the rule it breaks.
// Every one of these archives, signs and uploads; none is a build failure.
func TestArchiveCheckRejectsADeclaredKeyThatDidNotArrive(t *testing.T) {
	for _, tc := range []struct {
		name, value, rule string
	}{
		{"empty — the secrets file was never included", "", "is empty"},
		{"left unexpanded", "$(REVENUECAT_API_KEY)", "build-setting reference"},
		{"flare's committed placeholder", "REPLACE_ME_APPL_KEY", "REPLACE_ME placeholder"},
		{"the profile example's placeholder", "appl_xxxxxxxxxxxxxxxx", "appl_xxxx"},
		{"equal to the committed example", "appl_ExampleOnly42", "equals the committed .example"},
		{"the wrong app's key", "goog_OtherPlatform7", "does not match its declared shape appl_*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shippedScripts(t)
			archive := fixtureArchive(t, dir, builtPlist(map[string]string{"RevenueCatAPIKey": tc.value}))
			writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
			writeFile(t, filepath.Join(dir, "Secrets.xcconfig.example"), "// keys\nREVENUECAT_API_KEY = appl_ExampleOnly42 // RevenueCat\n")
			out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist",
				"--example", "Secrets.xcconfig.example", "REVENUECAT_API_KEY=appl_*")
			if err == nil {
				t.Fatalf("the release was allowed through:\n%s", out)
			}
			if !strings.Contains(out, "::error::verify-archive-info-plist: REVENUECAT_API_KEY ") || !strings.Contains(out, tc.rule) {
				t.Errorf("the failure does not name the key and the rule %q:\n%s", tc.rule, out)
			}
			if tc.value != "" && strings.Contains(out, tc.value) {
				t.Errorf("the output printed the value it rejected:\n%s", out)
			}
		})
	}
}

// A URL is the key most easily reduced to a husk: `https:/$()/$(HOST)` with
// HOST empty resolves to `https://` — non-empty, so an emptiness check passes
// it. The all-zero DSN is the profile example's own Sentry placeholder.
func TestArchiveCheckRejectsURLHusksAndTheZeroDSN(t *testing.T) {
	src := map[string]string{"SentryDSN": "$(SENTRY_DSN)", "APIBase": "https://$(API_HOST)/v1"}
	for _, tc := range []struct {
		name  string
		built map[string]string
		want  string
	}{
		{"scheme-only husk", map[string]string{"SentryDSN": "https://", "APIBase": "https://api.real.dev/v1"}, "SENTRY_DSN (undeclared build-time key) is a URL with no host"},
		{"one-slash husk", map[string]string{"SentryDSN": "https:/", "APIBase": "https://api.real.dev/v1"}, "SENTRY_DSN (undeclared build-time key) is a URL with no host"},
		{"zero DSN", map[string]string{"SentryDSN": "https://0000000000000000000000000000000@o0.ingest.sentry.io/0", "APIBase": "https://api.real.dev/v1"}, "SENTRY_DSN (undeclared build-time key) is the all-zero Sentry DSN"},
		// The composite form: the setting's own contribution is isolated, so an
		// empty host is caught even though the whole value is `https:///v1`.
		{"empty host inside a composite URL", map[string]string{"SentryDSN": "https://abc123@o1.ingest.sentry.io/5", "APIBase": "https:///v1"}, "API_HOST (undeclared build-time key) is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shippedScripts(t)
			archive := fixtureArchive(t, dir, tc.built)
			writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(src))
			out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist")
			if err == nil {
				t.Fatalf("the release was allowed through:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

// Undeclared keys are held to the same rules: kit and port-of-entry declare no
// secrets and are the repositories actually at risk.
func TestArchiveCheckRejectsAnUndeclaredKeyThatDidNotArrive(t *testing.T) {
	for _, tc := range []struct{ name, value, rule string }{
		{"empty", "", "is empty"},
		{"placeholder", "A-DEV-0000000000", "is the all-zero Aptabase placeholder"},
		{"your_ placeholder", "your_aptabase_key", "is a your_…/your-… placeholder"},
		// Placeholders are typed by hand, in whatever case the author chose.
		{"upper-case YOUR_ placeholder", "YOUR_APTABASE_APP_KEY", "is a your_…/your-… placeholder"},
		{"lower-case replace_me", "replace_me", "is a REPLACE_ME placeholder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shippedScripts(t)
			archive := fixtureArchive(t, dir, builtPlist(map[string]string{"AptabaseAppKey": tc.value}))
			writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
			out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist", "REVENUECAT_API_KEY=appl_*")
			if err == nil {
				t.Fatalf("an undeclared key that did not arrive was allowed through:\n%s", out)
			}
			want := "::error::verify-archive-info-plist: APTABASE_APP_KEY (undeclared build-time key) " + tc.rule
			if !strings.Contains(out, want) {
				t.Errorf("want %q in:\n%s", want, out)
			}
			for _, fix := range []string{"declare it in .lacquer.toml", "[project].secrets", "[[product]].secrets", "APTABASE_APP_KEY = \"<name of the GitHub secret"} {
				if !strings.Contains(out, fix) {
					t.Errorf("the failure does not say how to fix it (%q):\n%s", fix, out)
				}
			}
			if tc.value != "" && strings.Contains(out, tc.value) {
				t.Errorf("the output printed the value it rejected:\n%s", out)
			}
		})
	}
}

// A declared key no Info.plist entry references is not a failure — some keys
// reach the code another way — but the log must SAY the check did not cover
// it, rather than let a pass imply that it did.
func TestArchiveCheckPrintsADeclaredKeyItCannotSee(t *testing.T) {
	dir := shippedScripts(t)
	archive := fixtureArchive(t, dir, builtPlist(nil))
	writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
	out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist", "REVENUECAT_API_KEY", "GAD_APPLICATION_IDENTIFIER=ca-app-pub-*~*")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "NOT COVERED — declared key GAD_APPLICATION_IDENTIFIER is referenced by no Info.plist entry") {
		t.Errorf("an uncovered declared key was not reported:\n%s", out)
	}
	if strings.Contains(out, "NOT COVERED — declared key REVENUECAT_API_KEY") {
		t.Errorf("a covered key was reported uncovered:\n%s", out)
	}
}

// Fail closed: anything the check cannot read stops the release. A check that
// skipped when it could not look would be indistinguishable from one that
// looked and found nothing wrong.
func TestArchiveCheckFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir, archive string)
		want  string
	}{
		{"no archive", func(t *testing.T, dir, archive string) {
			if err := os.RemoveAll(archive); err != nil {
				t.Fatal(err)
			}
		}, "no archive at"},
		{"archive without its Info.plist", func(t *testing.T, dir, archive string) {
			if err := os.Remove(filepath.Join(archive, "Info.plist")); err != nil {
				t.Fatal(err)
			}
		}, "cannot read ApplicationProperties.ApplicationPath"},
		{"app without its Info.plist", func(t *testing.T, dir, archive string) {
			if err := os.Remove(filepath.Join(archive, "Products", "Applications", "Demo App.app", "Info.plist")); err != nil {
				t.Fatal(err)
			}
		}, "does not exist"},
		{"corrupt archived Info.plist", func(t *testing.T, dir, archive string) {
			writeFile(t, filepath.Join(archive, "Products", "Applications", "Demo App.app", "Info.plist"), "not a plist")
		}, "as a property list"},
		{"no source Info.plist", func(t *testing.T, dir, archive string) {
			if err := os.Remove(filepath.Join(dir, "App", "Info.plist")); err != nil {
				t.Fatal(err)
			}
		}, "source Info.plist App/Info.plist does not exist"},
		// The source says the key is there and the built app has no such entry:
		// the plist resolved is not the one that was built, so nothing it says
		// about the archive can be trusted.
		{"source entry missing from the archive", func(t *testing.T, dir, archive string) {
			built := builtPlist(nil)
			delete(built, "RevenueCatAPIKey")
			writeFile(t, filepath.Join(archive, "Products", "Applications", "Demo App.app", "Info.plist"), plistXML(built))
		}, "REVENUECAT_API_KEY is referenced by the source Info.plist at RevenueCatAPIKey but that entry is missing from the archived one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shippedScripts(t)
			archive := fixtureArchive(t, dir, builtPlist(nil))
			writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
			tc.setup(t, dir, archive)
			out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist", "REVENUECAT_API_KEY")
			if err == nil {
				t.Fatalf("the check passed with nothing to verify:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

// `xcodebuild archive` writes the app's Info.plist in BINARY form. A check
// that only parsed XML would pass every fixture above and fail — or worse,
// skip — on the real thing.
func TestArchiveCheckReadsABinaryArchivedPlist(t *testing.T) {
	dir := shippedScripts(t)
	archive := fixtureArchive(t, dir, builtPlist(map[string]string{"RevenueCatAPIKey": "REPLACE_ME_APPL_KEY"}))
	app := filepath.Join(archive, "Products", "Applications", "Demo App.app", "Info.plist")
	conv := exec.Command("python3", "-c", `import plistlib,sys
p=sys.argv[1]
d=plistlib.load(open(p,"rb"))
plistlib.dump(d,open(p,"wb"),fmt=plistlib.FMT_BINARY)`, app)
	if out, err := conv.CombinedOutput(); err != nil {
		t.Fatalf("could not write a binary plist fixture: %v\n%s", err, out)
	}
	if data, _ := os.ReadFile(app); !strings.HasPrefix(string(data), "bplist00") {
		t.Fatal("fixture is not a binary plist")
	}
	writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
	out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist", "REVENUECAT_API_KEY")
	if err == nil || !strings.Contains(out, "REVENUECAT_API_KEY is a REPLACE_ME placeholder") {
		t.Fatalf("a binary archived plist carrying a placeholder was not rejected (err=%v):\n%s", err, out)
	}
}

// A macOS app keeps its Info.plist under Contents/, where an iOS app's is at
// the bundle root. Reading only the iOS path would find no plist and — worse
// than failing — a check written to skip on that would pass every macOS release.
func TestArchiveCheckReadsAMacOSAppBundle(t *testing.T) {
	dir := shippedScripts(t)
	archive := fixtureArchive(t, dir, builtPlist(nil))
	app := filepath.Join(archive, "Products", "Applications", "Demo App.app")
	if err := os.Remove(filepath.Join(app, "Info.plist")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(app, "Contents", "Info.plist"), plistXML(builtPlist(map[string]string{"RevenueCatAPIKey": ""})))
	writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
	out, err := verify(t, dir, "--archive", archive, "--info-plist", "App/Info.plist", "REVENUECAT_API_KEY")
	if err == nil || !strings.Contains(out, "REVENUECAT_API_KEY is empty") {
		t.Fatalf("an empty key in a macOS bundle was not rejected (err=%v):\n%s", err, out)
	}
}

// In the workflow the source Info.plist is resolved the way the archive was
// built: Release build settings, the target whose product IS the archived app.
// The fake xcodebuild returns two targets — a widget listed first — so picking
// the first entry, or any entry by position, would read the wrong plist.
func TestArchiveCheckResolvesTheSourcePlistFromBuildSettings(t *testing.T) {
	settings := `[
  {"target": "DemoWidgets", "buildSettings": {"FULL_PRODUCT_NAME": "DemoWidgets.appex", "INFOPLIST_FILE": "Widgets/Info.plist", "PROJECT_DIR": "@DIR@"}},
  {"target": "Demo", "buildSettings": {"FULL_PRODUCT_NAME": "Demo App.app", "INFOPLIST_FILE": "App/Info.plist", "PROJECT_DIR": "@DIR@"}}
]`
	for _, tc := range []struct {
		name     string
		xcode    string
		wantErr  bool
		wantText string
	}{
		{"resolves the app target", "cat <<'JSON'\n" + settings + "\nJSON\n", true, "REVENUECAT_API_KEY is a REPLACE_ME placeholder"},
		{"xcodebuild fails", "echo 'error: scheme not found' >&2; exit 65\n", true, "xcodebuild -showBuildSettings failed"},
		{"no target builds the app", "echo '[{\"target\":\"X\",\"buildSettings\":{\"FULL_PRODUCT_NAME\":\"Other.app\"}}]'\n", true, "do not identify exactly one target producing Demo App.app"},
		// A notice ahead of the JSON must not turn into "unreadable settings".
		{"a notice before the JSON", "echo 'note: Using new build system'\ncat <<'JSON'\n" + settings + "\nJSON\n", true, "REVENUECAT_API_KEY is a REPLACE_ME placeholder"},
		// A generated Info.plist cannot reference a custom setting, so there is
		// nothing to check — which is also what a project looks like when the
		// xcconfig that set INFOPLIST_FILE never reached the build. It passes,
		// but LOUDLY, and the declared key it could not see is named.
		{"generated Info.plist", "echo '[{\"target\":\"Demo\",\"buildSettings\":{\"FULL_PRODUCT_NAME\":\"Demo App.app\",\"GENERATE_INFOPLIST_FILE\":\"YES\"}}]'\n", false, "::warning::verify-archive-info-plist: Demo App.app has no INFOPLIST_FILE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shippedScripts(t)
			archive := fixtureArchive(t, dir, builtPlist(map[string]string{"RevenueCatAPIKey": "REPLACE_ME_APPL_KEY"}))
			writeFile(t, filepath.Join(dir, "App", "Info.plist"), plistXML(sourcePlist))
			// The widget's plist references nothing; reading it would pass.
			writeFile(t, filepath.Join(dir, "Widgets", "Info.plist"), plistXML(map[string]string{"CFBundleIdentifier": "$(PRODUCT_BUNDLE_IDENTIFIER)"}))
			bin := filepath.Join(dir, "fakebin")
			writeFile(t, filepath.Join(bin, "xcodebuild"), "#!/bin/sh\n"+strings.ReplaceAll(tc.xcode, "@DIR@", dir))
			if err := os.Chmod(filepath.Join(bin, "xcodebuild"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "scripts/verify-archive-info-plist.sh", "--archive", archive,
				"--project", "Demo.xcodeproj", "--scheme", "Demo", "REVENUECAT_API_KEY")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			outb, err := cmd.CombinedOutput()
			out := string(outb)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, want error=%v:\n%s", err, tc.wantErr, out)
			}
			if !strings.Contains(out, tc.wantText) {
				t.Errorf("want %q in:\n%s", tc.wantText, out)
			}
		})
	}
}

// ---- the writer's fast pre-check, sharing the same placeholder rules ----

func writerRun(t *testing.T, secrets, formats map[string]string) string {
	t.Helper()
	return secretsRun(t, &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{{
			Name: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1",
			Secrets: secrets, SecretFormats: formats,
		}},
	})
}

// A placeholder is non-empty, so the presence check waves it through and the
// archive ships wired to a key that does not exist. The writer now refuses it
// before the archive starts — seconds rather than a whole build — using the
// same rules as the archive check, so the two cannot disagree.
func TestWriterRefusesPlaceholderSecrets(t *testing.T) {
	run := writerRun(t, map[string]string{"REVENUECAT_API_KEY": "RC", "SENTRY_DSN": "DSN"}, nil)
	for _, tc := range []struct{ name, rc, dsn, want string }{
		{"the example's own value", "appl_ExampleOnly42", "https://abc123@o1.ingest.sentry.io/5", "REVENUECAT_API_KEY equals the committed .example"},
		{"REPLACE_ME", "REPLACE_ME_APPL_KEY", "https://abc123@o1.ingest.sentry.io/5", "REVENUECAT_API_KEY is a REPLACE_ME placeholder"},
		{"URL husk", "appl_R3alKeyValue9", "https://", "SENTRY_DSN is a URL with no host"},
		{"zero DSN", "appl_R3alKeyValue9", "https://0000000000000000000000000000000@o0.ingest.sentry.io/0", "SENTRY_DSN is the all-zero Sentry DSN"},
		// xcconfig would SUBSTITUTE this, silently changing the key, and the
		// archive check could not tell it from an unexpanded reference.
		{"a $( in the value", "appl_R3al$(HOME)", "https://abc123@o1.ingest.sentry.io/5", "REVENUECAT_API_KEY contains a $(…) or ${…} build-setting reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := shippedScripts(t)
			writeFile(t, filepath.Join(dir, "Secrets.xcconfig.example"), "REVENUECAT_API_KEY = appl_ExampleOnly42\n")
			cmd := exec.Command("bash", "-c", run)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "REVENUECAT_API_KEY="+tc.rc, "SENTRY_DSN="+tc.dsn)
			outb, err := cmd.CombinedOutput()
			out := string(outb)
			if err == nil {
				t.Fatalf("the writer accepted a placeholder:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
			for _, v := range []string{tc.rc, tc.dsn} {
				if strings.Contains(out, v) {
					t.Errorf("the writer printed a value (%q):\n%s", v, out)
				}
			}
		})
	}
}

// xcconfig substitutes a bare `$NAME` that names a build setting — measured on
// Xcode: `a$ZB c` built as `abee c` with ZB = bee — and `$$` is its escape,
// reaching the built Info.plist as one `$`. A secret containing `$` has to be
// written escaped or it changes at build time. The `//` escape still applies,
// and its inserted `$()` must not itself be doubled.
func TestWriterEscapesDollarSigns(t *testing.T) {
	run := writerRun(t, map[string]string{"PROXY_SECRET": "PS"}, nil)
	dir := shippedScripts(t)
	cmd := exec.Command("bash", "-c", run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PROXY_SECRET=k$HOME$$x//y")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Secrets.xcconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "PROXY_SECRET = k$$HOME$$$$x/$()/y\n"; string(got) != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

// A DSN's shape needs `@`: `https://*@*/*`. The writer's glob charset rejected
// it, so the most URL-shaped secret could not be given a shape at all.
func TestWriterAcceptsAnAtSignInAShape(t *testing.T) {
	run := writerRun(t, map[string]string{"SENTRY_DSN": "DSN"}, map[string]string{"SENTRY_DSN": "https://*@*/*"})
	for _, tc := range []struct {
		value  string
		reject bool
	}{
		{"https://abc123@o1.ingest.sentry.io/5", false},
		{"https://o1.ingest.sentry.io/5", true},
	} {
		dir := shippedScripts(t)
		cmd := exec.Command("bash", "-c", run)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "SENTRY_DSN="+tc.value)
		out, err := cmd.CombinedOutput()
		if (err != nil) != tc.reject {
			t.Errorf("%s: rejected=%v want %v:\n%s", tc.value, err != nil, tc.reject, out)
		}
		if strings.Contains(string(out), "unsafe in a shell pattern") {
			t.Errorf("an @ in the shape was refused as unsafe:\n%s", out)
		}
	}
}
