package audit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

// A generic project whose lockfile has moved past an exactVersion pin: the
// lockfile-only bump the strict resolve exists to refuse.
const (
	gatePbx     = "App.xcodeproj/project.pbxproj"
	gatePbxproj = `// !$*UTF8*$!
{
	objects = {

/* Begin XCRemoteSwiftPackageReference section */
		AA0000000000000000000001 /* XCRemoteSwiftPackageReference "example-kit" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://example.com/acme/example-kit";
			requirement = {
				kind = exactVersion;
				version = 1.2.0;
			};
		};
/* End XCRemoteSwiftPackageReference section */
	};
}
`
	gateResolved = `{
  "pins" : [
    {
      "identity" : "example-kit",
      "kind" : "remoteSourceControl",
      "location" : "https://example.com/acme/example-kit",
      "state" : {
        "revision" : "0000000000000000000000000000000000000001",
        "version" : "1.3.0"
      }
    }
  ],
  "version" : 3
}
`
)

// renderedIOSWorkflows renders the lacquer's own iOS CI and release templates
// for a single app with a watch suite, so the detector is tested against the
// generator's output rather than a hand-written imitation of it.
func renderedIOSWorkflows(t *testing.T) map[string]string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	repo := filepath.Join(filepath.Dir(here), "..", "..")
	cfg := &config.Config{Project: config.Project{
		Name: "app", ProjectName: "App", Scheme: "App", BundleID: "com.example.app",
		AscAppID: "1", Xcodeproj: "App.xcodeproj", SwiftVersion: "6", GithubOrg: "example",
		WatchTests: &config.WatchTests{Scheme: "App Watch", TestTarget: "App WatchTests"},
	}}
	out := map[string]string{}
	for dest, src := range map[string]string{
		".github/workflows/ios-ci.yml":      "profiles/ios/workflows/ci.yml",
		".github/workflows/ios-release.yml": "profiles/ios/workflows/release.yml",
	} {
		raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(src)))
		if err != nil {
			t.Fatal(err)
		}
		rendered, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
		if len(missing) > 0 {
			t.Fatalf("%s: unsubstituted tokens %v", src, missing)
		}
		out[dest] = rendered
	}
	return out
}

func gateProject(t *testing.T, workflows map[string]string) []PinFinding {
	t.Helper()
	files := map[string]string{
		gatePbx:                           gatePbxproj,
		"App.xcodeproj/" + bundleResolved: gateResolved,
	}
	for p, body := range workflows {
		files[p] = body
	}
	return PackagePinFindings(pinRepo(t, files, nil))
}

func gateSummary(t *testing.T, fs []PinFinding) PinFinding {
	t.Helper()
	s := only(t, fs, PinChecked)
	if len(s) != 1 {
		t.Fatalf("summaries = %+v; want one", s)
	}
	if v := only(t, fs, PinViolates); len(v) != 1 {
		t.Fatalf("violations = %+v; the fixture must violate exactly once", v)
	}
	return s[0]
}

// What the lacquer ships is recognised as the gate, and the report stops
// saying such a pin merges green.
func TestPinGateRecognisesTheShippedWorkflows(t *testing.T) {
	fs := gateProject(t, renderedIOSWorkflows(t))
	s := gateSummary(t, fs)
	if s.Gate != GateStrict || len(s.GateLoose) != 0 {
		t.Fatalf("gate = %q, loose = %v; want strict with nothing loose", s.Gate, s.GateLoose)
	}
	// build, test and resolve in two CI jobs, watch test, release resolve and
	// archive: a gate read from fewer calls has stopped seeing some of them.
	if len(s.GateStrict) < 7 {
		t.Errorf("strict calls = %v; want at least 7", s.GateStrict)
	}
	out := FormatPackagePins(fs)
	for _, want := range []string{"CI resolves strictly", "FAIL its build"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stay green") {
		t.Errorf("report still says the pin stays green under a strict CI:\n%s", out)
	}
}

// The same workflows without the flag are what every project ran before: the
// report must keep saying the pin is unguarded.
func TestPinGateAbsentWithoutTheFlag(t *testing.T) {
	wf := renderedIOSWorkflows(t)
	for p, body := range wf {
		wf[p] = strings.ReplaceAll(body, StrictResolveFlag, "")
	}
	fs := gateProject(t, wf)
	s := gateSummary(t, fs)
	if s.Gate != GateNone || len(s.GateStrict) != 0 || len(s.GateLoose) == 0 {
		t.Fatalf("gate = %q, strict = %v, loose = %v; want none, with the loose calls listed", s.Gate, s.GateStrict, s.GateLoose)
	}
	out := FormatPackagePins(fs)
	for _, want := range []string{"does not resolve strictly", "stay green"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

// One loose call is enough to re-resolve: the archive that ships, here.
func TestPinGatePartialNamesTheLooseCall(t *testing.T) {
	wf := renderedIOSWorkflows(t)
	rel := wf[".github/workflows/ios-release.yml"]
	strictArchive := "-allowProvisioningUpdates \\\n            " + StrictResolveFlag + " \\\n"
	if strings.Count(rel, strictArchive) != 1 {
		t.Fatalf("the rendered release archive call changed shape; update this fixture")
	}
	rel = strings.Replace(rel, strictArchive, "-allowProvisioningUpdates \\\n", 1)
	wf[".github/workflows/ios-release.yml"] = rel
	line := 1 + strings.Count(rel[:strings.Index(rel, "xcodebuild archive")], "\n")

	fs := gateProject(t, wf)
	s := gateSummary(t, fs)
	want := ".github/workflows/ios-release.yml:" + itoa(line)
	if s.Gate != GatePartial || len(s.GateLoose) != 1 || !strings.HasSuffix(s.GateLoose[0], want) {
		t.Fatalf("gate = %q, loose = %v; want partial naming %s", s.Gate, s.GateLoose, want)
	}
	out := FormatPackagePins(fs)
	for _, w := range []string{"only in part", want, "stay green"} {
		if !strings.Contains(out, w) {
			t.Errorf("report lacks %q:\n%s", w, out)
		}
	}
}

// Mentioning xcodebuild is not running it: comments, echoes and a heartbeat
// message must not count as calls either way.
func TestPinGateIgnoresMentions(t *testing.T) {
	fs := gateProject(t, map[string]string{".github/workflows/ci.yml": `jobs:
  a:
    steps:
      - run: |
          # xcodebuild build -onlyUsePackageVersionsFromResolvedFile
          echo "xcodebuild test -onlyUsePackageVersionsFromResolvedFile"
          ( while true; do sleep 60; echo "xcodebuild still running"; done ) &
          xcodebuild -version
`})
	s := gateSummary(t, fs)
	if s.Gate != GateNone || len(s.GateStrict) != 0 || len(s.GateLoose) != 0 {
		t.Fatalf("gate = %q, strict = %v, loose = %v; want none, with nothing counted", s.Gate, s.GateStrict, s.GateLoose)
	}
}

// A Package.swift lockfile is resolved by swift build, which this does not
// assess, so it carries no gate and no claim either way, even beside strict
// xcodebuild workflows.
func TestPinGateNotClaimedForAPackageManifest(t *testing.T) {
	files := map[string]string{
		"Tools/Package.swift":    "// swift-tools-version: 6.0\nimport PackageDescription\nlet package = Package(name: \"Tools\", dependencies: [\n    .package(url: \"https://example.com/acme/example-kit\", exact: \"1.2.0\"),\n])\n",
		"Tools/Package.resolved": gateResolved,
	}
	for p, body := range renderedIOSWorkflows(t) {
		files[p] = body
	}
	fs := PackagePinFindings(pinRepo(t, files, nil))
	if v := only(t, fs, PinViolates); len(v) != 1 {
		t.Fatalf("violations = %+v; the fixture must violate exactly once", v)
	}
	for _, f := range only(t, fs, PinChecked) {
		if f.Gate != "" {
			t.Errorf("summary for %s carries gate %q; swift build lockfiles are not assessed", f.Resolved, f.Gate)
		}
	}
	if out := FormatPackagePins(fs); strings.Contains(out, "CI resolves strictly") || strings.Contains(out, "FAIL its build") {
		t.Errorf("report claims a strict CI for a swift build lockfile:\n%s", out)
	}
}

// Only COMMITTED workflows are the gate: one on disk but not in git is not what
// CI runs.
func TestPinGateIgnoresUncommittedWorkflows(t *testing.T) {
	root := pinRepo(t, map[string]string{
		gatePbx:                           gatePbxproj,
		"App.xcodeproj/" + bundleResolved: gateResolved,
	}, renderedIOSWorkflows(t))
	s := gateSummary(t, PackagePinFindings(root))
	if s.Gate != GateNone || len(s.GateStrict) != 0 {
		t.Fatalf("gate = %q, strict = %v; uncommitted workflows were counted", s.Gate, s.GateStrict)
	}
}
