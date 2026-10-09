package shipped

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every bundle an app embeds (a watch app, a widget, any app extension) must
// carry the app's build number, or App Store validation rejects the upload after
// the archive has been built and signed. The release used to check only the
// app's own bundle id, read from a scheme-level -showBuildSettings, which lists
// the scheme's buildable and nothing it embeds: an embedded bundle at another
// number could not fail it.
//
// These tests RUN the rendered steps. The settings tests run every step from
// "Increment build number" up to the archive that reads build settings, against
// a fake xcodebuild that answers the way the real one was measured to: a
// -scheme read lists only the scheme's own targets and needs -derivedDataPath,
// an -alltargets read lists every target and refuses -derivedDataPath. The
// archive tests run every step between the archive and the export against an
// archive laid out on disk.

type embeddedStep struct {
	Name string            `yaml:"name"`
	ID   string            `yaml:"id"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

func embeddedSteps(t *testing.T) []embeddedStep {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []embeddedStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(renderRelease(t, soloProject())), &doc); err != nil {
		t.Fatalf("rendered release workflow is not valid YAML: %v", err)
	}
	st := doc.Jobs["build-and-deploy"].Steps
	if len(st) == 0 {
		t.Fatal("no build-and-deploy steps rendered")
	}
	return st
}

func stepIndex(t *testing.T, steps []embeddedStep, name string) int {
	t.Helper()
	for i, s := range steps {
		if s.Name == name {
			return i
		}
	}
	t.Fatalf("no %q step; the release changed shape or was renamed", name)
	return -1
}

// runStep runs one step's script the way Actions does (bash -e), with its env.
// ${{ steps.build_number.outputs.build_number }} resolves to next.
func runStep(t *testing.T, s embeddedStep, dir string, env []string, next string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-e", "-c", s.Run)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	for k, v := range s.Env {
		if v == "${{ steps.build_number.outputs.build_number }}" {
			v = next
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// fakeTarget is one entry of a -showBuildSettings -json listing.
type fakeTarget struct {
	target, id, wrapper, version string
	inScheme                     bool // listed by a -scheme read
	schemeOnly                   bool // and by no -alltargets read
}

func settingsJSON(t *testing.T, targets []fakeTarget, schemeOnly bool) []byte {
	t.Helper()
	var out []map[string]any
	for _, tg := range targets {
		if schemeOnly && !tg.inScheme || !schemeOnly && tg.schemeOnly {
			continue
		}
		out = append(out, map[string]any{"target": tg.target, "buildSettings": map[string]string{
			"PRODUCT_BUNDLE_IDENTIFIER": tg.id, "WRAPPER_EXTENSION": tg.wrapper, "CURRENT_PROJECT_VERSION": tg.version}})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// runBuildNumberSettings runs, in order, every step from "Increment build
// number" up to the archive that reads build settings, as one job would.
func runBuildNumberSettings(t *testing.T, targets []fakeTarget, allTargetsFails bool) (string, error) {
	t.Helper()
	steps := embeddedSteps(t)
	from, to := stepIndex(t, steps, "Increment build number"), stepIndex(t, steps, "Build and archive")
	dir := t.TempDir()
	bin := t.TempDir()
	scheme, all := filepath.Join(dir, "scheme.json"), filepath.Join(dir, "all.json")
	if err := os.WriteFile(scheme, settingsJSON(t, targets, true), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(all, settingsJSON(t, targets, false), 0o644); err != nil {
		t.Fatal(err)
	}
	fail := "0"
	if allTargetsFails {
		fail = "1"
	}
	for name, body := range map[string]string{
		"app-store-connect": "echo 41",
		"agvtool":           "echo 'Setting version of project Demo to: 42'",
		"xcodebuild": fmt.Sprintf(`args=" $* "
case "$args" in *" -showBuildSettings "*) ;; *) echo "fake xcodebuild: unexpected call: $*" >&2; exit 98;; esac
case "$args" in
  *" -alltargets "*)
    case "$args" in *" -derivedDataPath "*) echo "xcodebuild: error: The flag -scheme, -testProductsPath, or -xctestrun is required when specifying -derivedDataPath." >&2; exit 64;; esac
    case "$args" in *" -scheme "*) echo "xcodebuild: error: You cannot specify both a scheme and -alltargets." >&2; exit 64;; esac
    [ %q = 1 ] && { echo "xcodebuild: error: could not read the project" >&2; exit 74; }
    cat %q ;;
  *" -scheme "*)
    case "$args" in *" -derivedDataPath "*) ;; *) exit 99;; esac
    cat %q ;;
  *) echo "fake xcodebuild: neither -scheme nor -alltargets: $*" >&2; exit 97 ;;
esac`, fail, all, scheme),
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(dir, "github-output")
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"PRODUCT_ASC_APP_ID=1", "PRODUCT_SCHEME=Demo", "PRODUCT_BUNDLE_ID=com.x.demo",
		"PRODUCT_EXTRA_BUNDLE_IDS=[]", "GITHUB_OUTPUT=" + output}
	var log strings.Builder
	ran := 0
	for _, s := range steps[from:to] {
		if s.Name != "Increment build number" && !strings.Contains(s.Run, "-showBuildSettings") {
			continue
		}
		ran++
		next := ""
		if b, err := os.ReadFile(output); err == nil {
			next = strings.TrimPrefix(strings.TrimSpace(string(b)), "build_number=")
		}
		out, err := runStep(t, s, dir, env, next)
		log.WriteString("--- " + s.Name + "\n" + out)
		if err != nil {
			return log.String(), err
		}
	}
	if ran == 0 {
		t.Fatal("ran no step")
	}
	return log.String(), nil
}

// The project every settings case starts from: the app, a widget and a watch
// app it embeds, a test bundle, and a separate app outside its id space.
func demoTargets(watch, widget string) []fakeTarget {
	return []fakeTarget{
		{"Demo", "com.x.demo", "app", "42", true, false},
		{"DemoWidget", "com.x.demo.widget", "appex", widget, false, false},
		{"DemoWatch", "com.x.demo.watchkitapp", "app", watch, false, false},
		// Test bundles carry whatever number; they never ship.
		{"DemoTests", "com.x.demo.tests", "xctest", "5", false, false},
		// Not embedded: another app whose id merely starts with the same text.
		{"DemoOther", "com.x.demoother", "app", "5", false, false},
		{"Unrelated", "com.y.unrelated", "app", "5", false, false},
	}
}

func TestReleaseSettingsCheckCoversEveryEmbeddedBundle(t *testing.T) {
	cases := []struct {
		name     string
		targets  []fakeTarget
		failRead bool
		wantFail bool
		want     []string
	}{
		{name: "all embedded bundles agree", targets: demoTargets("42", "42"),
			want: []string{"com.x.demo.watchkitapp", "com.x.demo.widget"}},
		{name: "watch app at another number", targets: demoTargets("7", "42"), wantFail: true,
			want: []string{"::error::", "com.x.demo.watchkitapp", "CURRENT_PROJECT_VERSION=7"}},
		{name: "widget at another number", targets: demoTargets("42", "7"), wantFail: true,
			want: []string{"::error::", "com.x.demo.widget", "CURRENT_PROJECT_VERSION=7"}},
		{name: "watch app with no number", targets: demoTargets("", "42"), wantFail: true,
			want: []string{"::error::", "com.x.demo.watchkitapp"}},
		{name: "no embedded bundles", targets: demoTargets("42", "42")[:1]},
		// Selection anchored on the app: a listing with no app in it cannot
		// pass by selecting nothing.
		{name: "every-target read lists no app", wantFail: true,
			targets: append([]fakeTarget{{"Demo", "com.x.demo", "app", "42", true, true}}, demoTargets("7", "7")[1:]...),
			want:    []string{"::error::No application target", "com.x.demo"}},
		{name: "every-target read fails", targets: demoTargets("42", "42"), failRead: true, wantFail: true,
			want: []string{"::error::"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runBuildNumberSettings(t, c.targets, c.failRead)
			if failed := err != nil; failed != c.wantFail {
				t.Fatalf("failed = %v (%v), want %v\n%s", failed, err, c.wantFail, out)
			}
			mustContain(t, "the output", out, c.want...)
			for _, never := range []string{"com.x.demo.tests", "com.x.demoother", "com.y.unrelated"} {
				if strings.Contains(out, never) {
					t.Errorf("selected %s, which the app does not embed:\n%s", never, out)
				}
			}
		})
	}
}

// plutilPath is a PATH with a plutil on it: the real one on a Mac, and on Linux
// CI a stand-in implementing only `plutil -extract KEY raw -o - FILE` with
// Python's plistlib. TestFakePlutilAgreesWithPlutil holds the two together.
func plutilPath(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("plutil"); err == nil {
		return os.Getenv("PATH")
	}
	return fakePlutil(t) + string(os.PathListSeparator) + os.Getenv("PATH")
}

func fakePlutil(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("neither plutil nor python3 is available, so the archive check cannot run")
	}
	bin := t.TempDir()
	script := `#!` + py + `
import plistlib, sys
a = sys.argv[1:]
if len(a) != 6 or a[0] != "-extract" or a[2] != "raw" or a[3:5] != ["-o", "-"]:
    sys.exit("fake plutil: unsupported arguments: %r" % a)
try:
    with open(a[5], "rb") as f:
        v = plistlib.load(f)[a[1]]
except Exception as e:
    sys.exit("%s: %s" % (a[5], e))
print(v)
`
	if err := os.WriteFile(filepath.Join(bin, "plutil"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// On a Mac, the stand-in Linux CI uses must answer as plutil does: the value
// for a readable plist, a failure for an unreadable one or a missing key.
func TestFakePlutilAgreesWithPlutil(t *testing.T) {
	real, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("no plutil to compare against; this runs on a Mac")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 for the stand-in")
	}
	fake := filepath.Join(fakePlutil(t), "plutil")
	dir := t.TempDir()
	files := map[string]string{"good": infoPlist("com.x.demo", "42"), "garbage": "not a property list\x00\x01"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ file, key string }{
		{"good", "CFBundleVersion"}, {"good", "CFBundleIdentifier"}, {"good", "NoSuchKey"},
		{"garbage", "CFBundleVersion"}, {"absent", "CFBundleVersion"},
	} {
		run := func(bin string) (string, bool) {
			out, err := exec.Command(bin, "-extract", c.key, "raw", "-o", "-", filepath.Join(dir, c.file)).Output()
			return strings.TrimSpace(string(out)), err == nil
		}
		rv, rok := run(real)
		fv, fok := run(fake)
		if rok != fok || (rok && rv != fv) {
			t.Errorf("%s %s: plutil = (%q, ok=%v), stand-in = (%q, ok=%v)", c.file, c.key, rv, rok, fv, fok)
		}
	}
}

func infoPlist(id, version string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + id + `</string>
	<key>CFBundleVersion</key>
	<string>` + version + `</string>
</dict>
</plist>
`
}

// runArchiveCheck lays out an archive (bundle path relative to the .app ->
// Info.plist contents, "" for a bundle with no Info.plist) and runs every step
// between the archive and the export.
func runArchiveCheck(t *testing.T, bundles map[string]string, noApp bool) (string, error) {
	t.Helper()
	steps := embeddedSteps(t)
	from, to := stepIndex(t, steps, "Build and archive"), stepIndex(t, steps, "Export IPA")
	dir := t.TempDir()
	app := filepath.Join(dir, "Demo.xcarchive", "Products", "Applications", "Demo.app")
	if noApp {
		app = filepath.Join(dir, "Demo.xcarchive", "Products", "Applications")
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, plist := range bundles {
		b := filepath.Join(app, rel)
		if err := os.MkdirAll(b, 0o755); err != nil {
			t.Fatal(err)
		}
		if plist == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(b, "Info.plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + plutilPath(t), "ARCHIVE_DIR=" + dir, "PRODUCT_NAME=Demo", "PRODUCT_BUNDLE_ID=com.x.demo"}
	var all strings.Builder
	for _, s := range steps[from+1 : to] {
		out, err := runStep(t, s, dir, env, "42")
		all.WriteString("--- " + s.Name + "\n" + out)
		if err != nil {
			return all.String(), err
		}
	}
	return all.String(), nil
}

func archivedApp(over map[string]string) map[string]string {
	b := map[string]string{
		".":                                      infoPlist("com.x.demo", "42"),
		"PlugIns/DemoWidget.appex":               infoPlist("com.x.demo.widget", "42"),
		"Extensions/DemoIntents.appex":           infoPlist("com.x.demo.intents", "42"),
		"Watch/DemoWatch.app":                    infoPlist("com.x.demo.watchkitapp", "42"),
		"Watch/DemoWatch.app/PlugIns/Comp.appex": infoPlist("com.x.demo.watchkitapp.complication", "42"),
		// A framework's version is its own; validation does not compare it.
		"Frameworks/Lib.framework": infoPlist("com.vendor.lib", "1"),
	}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func TestReleaseArchiveCheckReadsEveryEmbeddedBundle(t *testing.T) {
	cases := []struct {
		name     string
		bundles  map[string]string
		noApp    bool
		wantFail bool
		want     []string
	}{
		{name: "every bundle agrees", bundles: archivedApp(nil),
			want: []string{"com.x.demo.watchkitapp.complication", "All 5 archived bundles"}},
		{name: "watch app at another number", wantFail: true,
			bundles: archivedApp(map[string]string{"Watch/DemoWatch.app": infoPlist("com.x.demo.watchkitapp", "7")}),
			want:    []string{"::error::com.x.demo.watchkitapp (Demo.app/Watch/DemoWatch.app)", "CFBundleVersion 7"}},
		{name: "watch extension nested in the watch app", wantFail: true,
			bundles: archivedApp(map[string]string{"Watch/DemoWatch.app/PlugIns/Comp.appex": infoPlist("com.x.demo.watchkitapp.complication", "7")}),
			want:    []string{"::error::com.x.demo.watchkitapp.complication", "CFBundleVersion 7"}},
		{name: "extension in Extensions/", wantFail: true,
			bundles: archivedApp(map[string]string{"Extensions/DemoIntents.appex": infoPlist("com.x.demo.intents", "41")}),
			want:    []string{"::error::com.x.demo.intents", "CFBundleVersion 41"}},
		{name: "the app itself", wantFail: true,
			bundles: archivedApp(map[string]string{".": infoPlist("com.x.demo", "41")}),
			want:    []string{"::error::com.x.demo (Demo.app)", "CFBundleVersion 41"}},
		{name: "no embedded bundles", bundles: map[string]string{".": infoPlist("com.x.demo", "42")},
			want: []string{"All 1 archived bundles"}},
		{name: "unreadable plist", wantFail: true,
			bundles: archivedApp(map[string]string{"PlugIns/DemoWidget.appex": "not a property list\x00\x01"}),
			want:    []string{"::error::Cannot read CFBundleVersion from Demo.app/PlugIns/DemoWidget.appex/Info.plist"}},
		{name: "bundle with no Info.plist", wantFail: true,
			bundles: archivedApp(map[string]string{"Watch/DemoWatch.app": ""}),
			want:    []string{"::error::Cannot read CFBundleVersion from Demo.app/Watch/DemoWatch.app/Info.plist"}},
		{name: "no app in the archive", noApp: true, wantFail: true, want: []string{"::error::Expected one app"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runArchiveCheck(t, c.bundles, c.noApp)
			if failed := err != nil; failed != c.wantFail {
				t.Fatalf("failed = %v (%v), want %v\n%s", failed, err, c.wantFail, out)
			}
			mustContain(t, "the output", out, c.want...)
			if strings.Contains(out, "com.vendor.lib") {
				t.Errorf("compared a framework's version:\n%s", out)
			}
		})
	}
}

// Both checks read the number the bump step exported, and the new xcodebuild
// call resolves strictly (TestEveryResolvingXcodebuildCallIsStrict covers it
// too; this pins it to the call that lists every target).
func TestReleaseEmbeddedChecksAreWired(t *testing.T) {
	steps := embeddedSteps(t)
	if steps[stepIndex(t, steps, "Increment build number")].ID != "build_number" {
		t.Fatal("the bump step's id is not build_number, so the checks read no number")
	}
	for _, name := range []string{"Check embedded bundles' build number settings", "Verify the archived build number of every bundle"} {
		s := steps[stepIndex(t, steps, name)]
		if s.Env["NEXT"] != "${{ steps.build_number.outputs.build_number }}" {
			t.Errorf("%s: NEXT = %q, want the bump step's output", name, s.Env["NEXT"])
		}
	}
	s := steps[stepIndex(t, steps, "Check embedded bundles' build number settings")]
	for _, w := range []string{"-alltargets", strictResolveFlag, "-clonedSourcePackagesDirPath DerivedData/SourcePackages", `-IDECustomDerivedDataLocation="$PWD/DerivedData/`} {
		if !strings.Contains(s.Run, w) {
			t.Errorf("the every-target settings read lacks %s", w)
		}
	}
}
