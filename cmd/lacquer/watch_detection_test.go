package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/testtargets"
)

// A watchOS test bundle that nothing runs was invisible to the audit on every
// project that generates its Xcode project with XcodeGen and commits no
// pbxproj, which is every watch app in the fleet this was built for. These drive
// the real CLI against committed fixture shapes.

const watchFindingHeader = "watchOS test bundles nothing runs"

// fixturesDir is absolute, resolved before any test changes directory.
var fixturesDir, _ = filepath.Abs(filepath.Join("..", "..", "internal", "shipped", "testdata", "projects"))

// fixtureCopy copies a committed fixture project into a fresh git repository,
// committing everything, and returns its root.
func fixtureCopy(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join(fixturesDir, name)
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, dst)
	return dst
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		gittest.Init(t, dir, "-q")
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "fixture", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// auditAt runs `lacquer audit` in dir against a minimal lacquer root.
func auditAt(t *testing.T, dir string) (int, string) {
	t.Helper()
	hr, _ := auditFixture(t, pbxCompliant, "")
	chdir(t, dir)
	var out, errb bytes.Buffer
	code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	return code, out.String() + errb.String()
}

// gateFrom moves the watch gate's start date for one test.
func gateFrom(t *testing.T, d time.Time) {
	t.Helper()
	saved := testtargets.WatchGateFrom
	t.Cleanup(func() { testtargets.WatchGateFrom = saved })
	testtargets.WatchGateFrom = d
}

var (
	longAgo  = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	farAhead = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
)

func appendManifest(t *testing.T, dir, extra string) {
	t.Helper()
	p := filepath.Join(dir, ".lacquer.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The fail path: the XcodeGen-only fixture declares no watch_tests, and its watch
// bundle is found from project.yml alone, with the exact table to add. Once the
// gate date has passed, the audit exits 4.
func TestAuditReportsAnUndeclaredWatchTestBundleFromTheSpec(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "watchapp")
	code, out := auditAt(t, dir)
	if code != 4 {
		t.Errorf("audit exited %d, want 4 for a watch bundle nothing runs, past the gate date\n%s", code, out)
	}
	section := sectionAfter(out, watchFindingHeader)
	if section == "" {
		t.Fatalf("no watch finding:\n%s", out)
	}
	for _, want := range []string{
		"Watchapp Watch AppTests",
		"[project.watch_tests]",
		`scheme      = "Watchapp Watch App"`,
		`test_target = "Watchapp Watch AppTests"`,
		"BLOCKING",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the watch finding does not say %q:\n%s", want, section)
		}
	}
	// The iOS bundle is not a watch bundle.
	if strings.Contains(section, "  WatchappTests") {
		t.Errorf("the iOS test bundle is reported as a watch bundle:\n%s", section)
	}
}

// Before the gate date the same finding warns, prints the date it starts
// blocking, and does not change the exit code.
func TestAuditWarnsAboutAnUnrunWatchBundleBeforeTheGateDate(t *testing.T) {
	gateFrom(t, farAhead)
	code, out := auditAt(t, fixtureCopy(t, "watchapp"))
	if code != 0 {
		t.Errorf("audit exited %d, want 0 before the gate date\n%s", code, out)
	}
	if s := sectionAfter(out, watchFindingHeader); !strings.Contains(s, "2099-01-01") || !strings.Contains(s, "Watchapp Watch AppTests") {
		t.Errorf("the warning does not name the bundle and the date it starts blocking:\n%s", out)
	}
}

// Declaring the job removes the finding: the managed selector covers it.
func TestAuditIsSilentOnceTheWatchBundleIsDeclared(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "watchapp")
	appendManifest(t, dir, "\n[project.watch_tests]\nscheme = \"Watchapp Watch App\"\ntest_target = \"Watchapp Watch AppTests\"\n")
	code, out := auditAt(t, dir)
	if strings.Contains(out, watchFindingHeader) {
		t.Errorf("a declared watch bundle is still reported:\n%s", out)
	}
	if code != 0 {
		t.Errorf("audit exited %d, want 0\n%s", code, out)
	}
}

// A project-owned workflow that runs it, declared and verified, also removes it.
func TestAuditIsSilentWhenAVerifiedWorkflowRunsTheWatchBundle(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "watchapp")
	wf := filepath.Join(dir, ".github", "workflows", "watch-ci.yml")
	if err := os.MkdirAll(filepath.Dir(wf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wf, []byte(`on:
  pull_request:
jobs:
  watch-tests:
    runs-on: macos-latest
    steps:
      - run: xcodebuild test -scheme "Watchapp Watch App" -only-testing:"Watchapp Watch AppTests"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	appendManifest(t, dir, "\n[[project.covered_elsewhere]]\ntarget = \"Watchapp Watch AppTests\"\n"+
		"workflow = \".github/workflows/watch-ci.yml\"\njob = \"watch-tests\"\n"+
		"reason = \"watch-ci.yml job watch-tests runs it on a watch simulator\"\n")
	commitAll(t, dir)
	code, out := auditAt(t, dir)
	if strings.Contains(out, watchFindingHeader) {
		t.Errorf("a watch bundle a verified workflow runs is still reported:\n%s", out)
	}
	if code != 0 {
		t.Errorf("audit exited %d, want 0\n%s", code, out)
	}
}

// THE CONTROL: a phone app with a widget and no watch app has no watch finding
// of any kind, and no notice either.
func TestAuditIsSilentForAWidgetOnlySpec(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "watchapp")
	if err := os.WriteFile(filepath.Join(dir, "project.yml"), []byte(`name: Watchapp
targets:
  Watchapp:
    type: application
    platform: iOS
  WatchappTests:
    type: bundle.unit-test
    platform: iOS
  WatchappWidget:
    type: app-extension
    platform: iOS
schemes:
  Watchapp:
    test:
      targets: [WatchappTests]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)
	_, out := auditAt(t, dir)
	if strings.Contains(strings.ToLower(out), "watch app") || strings.Contains(out, watchFindingHeader) {
		t.Errorf("a widget-only project has a watch finding:\n%s", out)
	}
}

// Every other shipped fixture is a project with no watch app: none may say a
// word about one.
func TestAuditIsSilentForEveryShippedFixture(t *testing.T) {
	gateFrom(t, longAgo)
	for _, name := range []string{"rootapp", "multistack", "duoapp", "spmpackage"} {
		_, out := auditAt(t, fixtureCopy(t, name))
		if strings.Contains(out, watchFindingHeader) || strings.Contains(strings.ToLower(out), "watch app") {
			t.Errorf("%s: watch finding on a project with no watch app:\n%s", name, out)
		}
	}
}

// A watch app with no test bundle at all has nothing to declare. Said once, as
// a notice, and never blocking.
func TestAuditNoticesAWatchAppWithNoTestBundle(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "watchapp")
	spec := filepath.Join(dir, "project.yml")
	b, _ := os.ReadFile(spec)
	trimmed := strings.Replace(string(b), `  Watchapp Watch AppTests:
    type: bundle.unit-test
    platform: watchOS
    sources: ["Watchapp Watch AppTests"]
    dependencies:
      - target: Watchapp Watch App
`, "", 1)
	if trimmed == string(b) {
		t.Fatal("could not remove the watch test bundle from the fixture spec")
	}
	if err := os.WriteFile(spec, []byte(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)
	code, out := auditAt(t, dir)
	if code != 0 {
		t.Errorf("audit exited %d, want 0: a watch app with no tests is a notice\n%s", code, out)
	}
	if !strings.Contains(out, "watch app Watchapp Watch App has no test bundle") {
		t.Errorf("no notice for a watch app without a test bundle:\n%s", out)
	}
}

// When the pbxproj is TRACKED it is the project, and the spec beside it is not
// read: the two can disagree, and the committed project is what builds.
func TestAuditReadsTheTrackedPbxprojNotTheSpec(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "watchapp")
	// The spec has no watch targets; the tracked pbxproj does.
	if err := os.WriteFile(filepath.Join(dir, "project.yml"), []byte("name: Watchapp\ntargets:\n  Watchapp:\n    type: application\n    platform: iOS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pbx := filepath.Join(dir, "Watchapp.xcodeproj", "project.pbxproj")
	if err := os.MkdirAll(filepath.Dir(pbx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pbx, []byte(watchSDKPbxproj), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("DerivedData/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir)
	_, out := auditAt(t, dir)
	if !strings.Contains(sectionAfter(out, watchFindingHeader), "Watchapp Watch AppTests") {
		t.Errorf("the tracked pbxproj's watch bundle is not reported, so the spec was read instead:\n%s", out)
	}
}

// sectionAfter is out from the line containing header up to the next line that
// starts a new section (non-blank and not indented).
func sectionAfter(out, header string) string {
	i := strings.Index(out, header)
	if i < 0 {
		return ""
	}
	lines := strings.Split(out[i:], "\n")
	end := len(lines)
	for j := 1; j < len(lines); j++ {
		if l := lines[j]; l != "" && !strings.HasPrefix(l, " ") {
			end = j
			break
		}
	}
	return strings.Join(lines[:end], "\n")
}

// watchSDKPbxproj is a tracked project with a watch app and its unit-test bundle,
// platform given by SDKROOT.
const watchSDKPbxproj = `
/* Begin PBXNativeTarget section */
		A1 /* Watchapp */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L1;
			name = Watchapp;
			productType = "com.apple.product-type.application";
		};
		A2 /* Watchapp Watch App */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L2;
			name = "Watchapp Watch App";
			productType = "com.apple.product-type.application";
		};
		A3 /* Watchapp Watch AppTests */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L3;
			name = "Watchapp Watch AppTests";
			productType = "com.apple.product-type.bundle.unit-test";
		};
/* End PBXNativeTarget section */
/* Begin XCConfigurationList section */
		L1 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C1 /* Debug */,
			);
		};
		L2 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C2 /* Debug */,
			);
		};
		L3 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C3 /* Debug */,
			);
		};
/* End XCConfigurationList section */
/* Begin XCBuildConfiguration section */
		C1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
		C2 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SDKROOT = watchos;
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
		C3 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SDKROOT = watchos;
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`

// A covered_elsewhere entry whose workflow file is gone silences nothing and
// claims something false. The audit fails on it outright, whatever the target.
func TestAuditFailsWhenACoveredElsewhereWorkflowIsMissing(t *testing.T) {
	gateFrom(t, farAhead)
	dir := fixtureCopy(t, "watchapp")
	appendManifest(t, dir, "\n[[project.covered_elsewhere]]\ntarget = \"Watchapp Watch AppTests\"\n"+
		"workflow = \".github/workflows/watch-ci.yml\"\njob = \"watch-tests\"\n"+
		"reason = \"watch-ci.yml job watch-tests runs it on a watch simulator\"\n")
	commitAll(t, dir)
	code, out := auditAt(t, dir)
	if code != 4 {
		t.Errorf("audit exited %d, want 4 for a covered_elsewhere naming a missing workflow\n%s", code, out)
	}
	if !strings.Contains(out, ".github/workflows/watch-ci.yml does not exist") {
		t.Errorf("the report does not name the missing workflow:\n%s", out)
	}
}
