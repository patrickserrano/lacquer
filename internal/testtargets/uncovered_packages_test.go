package testtargets

import (
	"path/filepath"
	"strings"
	"testing"
)

// flare's shape, and the gap lacquer#382 describes. Flare/Flare.xcodeproj
// references two local packages that sit BESIDE its directory (`../FlareCore`,
// `../FlareData`), each with one test suite. No selector names either suite, and
// since flare #240 retired the workflows that used to run them, nothing else
// does. The audit said nothing, because its "runs nowhere" direction only looked
// at native targets.
const flarePbx = `// !$*UTF8*$!
{
	objects = {
		A1 /* Flare */ = {
			isa = PBXNativeTarget;
			name = Flare;
			productType = "com.apple.product-type.application";
		};
		A2 /* FlareTests */ = {
			isa = PBXNativeTarget;
			name = FlareTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
/* Begin XCLocalSwiftPackageReference section */
		F3D1A0052F530004FLARECORE /* XCLocalSwiftPackageReference "FlareCore" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = ../FlareCore;
		};
		F3D1A0062F530005FLAREDATA /* XCLocalSwiftPackageReference "FlareData" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = ../FlareData;
		};
/* End XCLocalSwiftPackageReference section */
	};
}
`

func flarePackage(name string, tests ...string) string {
	var b strings.Builder
	b.WriteString("// swift-tools-version: 6.0\nimport PackageDescription\n\nlet package = Package(\n")
	b.WriteString("    name: \"" + name + "\",\n    targets: [\n        .target(name: \"" + name + "\"),\n")
	for _, t := range tests {
		b.WriteString("        .testTarget(name: \"" + t + "\", dependencies: [\"" + name + "\"]),\n")
	}
	b.WriteString("    ]\n)\n")
	return b.String()
}

// flareCI is the managed ios-ci.yml's test step as flare has it: the one
// selector the manifest derives, nothing for the packages.
const flareCI = `name: iOS CI
on:
  pull_request:
  push:
    branches: [main]
jobs:
  test:
    runs-on: [self-hosted, macOS]
    steps:
      - uses: actions/checkout@v4
      - name: Test
        run: |
          xcodebuild test \
            -project "Flare/Flare.xcodeproj" \
            -scheme "Flare" \
            "-only-testing:FlareTests" \
            CODE_SIGNING_REQUIRED=NO
`

var flareSelectors = []string{"FlareTests"}

// flare lays the project out with the given extra files (workflows, schemes)
// and returns the project root and what Parse read from it.
func flare(t *testing.T, extra map[string]string) (string, []Target) {
	t.Helper()
	files := map[string]string{
		"Flare/Flare.xcodeproj/project.pbxproj": flarePbx,
		"FlareCore/Package.swift":               flarePackage("FlareCore", "FlareCoreTests"),
		"FlareData/Package.swift":               flarePackage("FlareData", "FlareDataTests"),
		".github/workflows/ios-ci.yml":          flareCI,
	}
	for k, v := range extra {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	pbx := writeProject(t, "Flare/Flare.xcodeproj", files)
	root := filepath.Dir(filepath.Dir(filepath.Dir(pbx)))
	return root, parsePath(t, pbx)
}

// audit runs what `lacquer audit` runs: compare, verify, apply.
func audit(root string, targets []Target, selectors []string, decls []Declaration) Report {
	return Apply(Compare(targets, selectors), Verify(root, decls, targets, nil))
}

func uncoveredNames(r Report) string {
	var names []string
	for _, u := range r.Uncovered {
		names = append(names, u.Name)
	}
	return strings.Join(names, " ")
}

// lacquer#382, measured on flare: package suites nothing runs are reported, each
// with the package it belongs to and the three ways out.
func TestPackageSuitesNothingRunsAreUncovered(t *testing.T) {
	root, targets := flare(t, nil)
	r := audit(root, targets, flareSelectors, nil)
	if got := uncoveredNames(r); got != "FlareCoreTests FlareDataTests" {
		t.Fatalf("uncovered = %q, want %q", got, "FlareCoreTests FlareDataTests")
	}
	for _, u := range r.Uncovered {
		want := strings.TrimSuffix(u.Name, "Tests")
		if u.PackageDir != want {
			t.Errorf("%s: PackageDir = %q, want the repo-relative %q (not the pbxproj's %q)", u.Name, u.PackageDir, want, u.Package)
		}
	}
	out := Format(r)
	for _, want := range []string{
		"FlareCoreTests  (unit tests, local package FlareCore)",
		"FlareDataTests  (unit tests, local package FlareData)",
		"extra_test_targets",
		"the scheme's TestAction must list it",
		"[[project.covered_elsewhere]]",
		"swift test --package-path <package>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

// rail's shape: the suites are named by extra_test_targets, so a managed
// selector runs them. Nothing to report.
func TestPackageSuitesNamedByASelectorAreCovered(t *testing.T) {
	pbx := railProject(t)
	root := filepath.Dir(filepath.Dir(pbx))
	targets := parsePath(t, pbx)
	if r := audit(root, targets, railSelectors, nil); len(r.Uncovered) != 0 {
		t.Fatalf("suites named by extra_test_targets were reported uncovered: %+v", r.Uncovered)
	}
	// The same project without extra_test_targets is the finding.
	if got := uncoveredNames(audit(root, targets, []string{"RailTests"}, nil)); got != "RailCoreTests RailDataTests" {
		t.Fatalf("uncovered = %q, want %q", got, "RailCoreTests RailDataTests")
	}
}

// workflow wraps one step's run block (and optional step/job/workflow keys) in
// a workflow triggered by pull requests.
func workflow(workflowKeys, jobKeys, stepKeys, run string) string {
	var b strings.Builder
	b.WriteString("name: Packages\non:\n  pull_request:\n")
	b.WriteString(workflowKeys)
	b.WriteString("jobs:\n  packages:\n    runs-on: macos-latest\n")
	b.WriteString(jobKeys)
	b.WriteString("    steps:\n      - uses: actions/checkout@v4\n      - name: Test\n")
	b.WriteString(stepKeys)
	b.WriteString("        run: |\n")
	for _, line := range strings.Split(strings.TrimRight(run, "\n"), "\n") {
		b.WriteString("          " + line + "\n")
	}
	return b.String()
}

// Each of these runs FlareCore's tests on a pull request, and nothing else's.
// FlareDataTests staying reported is half of every case: a rule that covered
// every package once it saw any `swift test` would pass the first half.
func TestAWorkflowThatRunsThePackageCoversIt(t *testing.T) {
	cases := map[string]string{
		"--package-path":            workflow("", "", "", "swift test --package-path FlareCore"),
		"--package-path=":           workflow("", "", "", "swift test --package-path=FlareCore --parallel"),
		"quoted, ./ and trailing /": workflow("", "", "", `swift test --package-path "./FlareCore/"`),
		"step working-directory":    workflow("", "", "        working-directory: FlareCore\n", "swift test"),
		// Windsock's kit-test.yml, the one real case in the fleet.
		"job defaults": workflow("", "    defaults:\n      run:\n        working-directory: FlareCore\n", "",
			"swift test --enable-code-coverage"),
		"workflow defaults": workflow("defaults:\n  run:\n    working-directory: FlareCore\n", "", "", "swift test"),
		"step overrides job defaults": workflow("", "    defaults:\n      run:\n        working-directory: FlareData\n",
			"        working-directory: FlareCore\n", "swift test"),
		"cd && swift test":    workflow("", "", "", "cd FlareCore && swift test"),
		"cd; then swift test": workflow("", "", "", "set -euo pipefail\ncd FlareCore\nswift test 2>&1 | xcpretty"),
		"package path relative to working-directory": workflow("", "", "        working-directory: Flare\n",
			"swift test --package-path ../FlareCore"),
		"$GITHUB_WORKSPACE":                workflow("", "", "", `swift test --package-path "$GITHUB_WORKSPACE/FlareCore"`),
		"xcrun, continuation":              workflow("", "", "", "xcrun swift test \\\n  --package-path FlareCore \\\n  --parallel"),
		"xcodebuild -only-testing":         workflow("", "", "", "xcodebuild test \\\n  -scheme Flare \\\n  -only-testing:FlareCoreTests"),
		"xcodebuild -only-testing a class": workflow("", "", "", `xcodebuild test -scheme Flare "-only-testing:FlareCoreTests/ParserTests"`),
		"workflow_call":                    strings.Replace(workflow("", "", "", "swift test --package-path FlareCore"), "pull_request:", "workflow_call:", 1),
	}
	for name, wf := range cases {
		t.Run(name, func(t *testing.T) {
			root, targets := flare(t, map[string]string{".github/workflows/packages.yml": wf})
			r := audit(root, targets, flareSelectors, nil)
			if got := uncoveredNames(r); got != "FlareDataTests" {
				t.Fatalf("uncovered = %q, want just FlareDataTests\n%s", got, wf)
			}
			if len(r.Ran) != 1 || r.Ran[0].Target != "FlareCoreTests" || r.Ran[0].Workflow != ".github/workflows/packages.yml" {
				t.Fatalf("ran = %+v, want FlareCoreTests <- .github/workflows/packages.yml", r.Ran)
			}
			if len(r.Unchecked) != 0 {
				t.Errorf("unchecked = %+v, want none", r.Unchecked)
			}
			out := Format(r)
			for _, want := range []string{"FlareCoreTests  <- .github/workflows/packages.yml", "NOT checked"} {
				if !strings.Contains(out, want) {
					t.Errorf("report does not contain %q:\n%s", want, out)
				}
			}
		})
	}
}

// Each of these mentions FlareCore, or tests, or both, and runs FlareCore's
// suite on no pull request. Every one must leave it reported.
func TestThingsThatDoNotRunThePackageDoNotCoverIt(t *testing.T) {
	cases := map[string]string{
		// momfriend's shape: compiled, deliberately never run.
		"swift build --build-tests":    workflow("", "", "", "swift build --package-path FlareCore --build-tests"),
		"another package":              workflow("", "", "", "swift test --package-path FlareData"),
		"repo root, no package path":   workflow("", "", "", "swift test"),
		"a parent of the package":      workflow("", "", "        working-directory: Flare\n", "swift test"),
		"commented out":                workflow("", "", "", "# swift test --package-path FlareCore\necho skipped"),
		"trailing comment":             workflow("", "", "", "echo later # swift test --package-path FlareCore"),
		"echoed, not run":              workflow("", "", "", `echo "swift test --package-path FlareCore"`),
		"cd, then back":                workflow("", "", "", "cd FlareCore\ncd ..\nswift test"),
		"xcodebuild build-for-testing": workflow("", "", "", "xcodebuild build-for-testing -scheme Flare -only-testing:FlareCoreTests"),
		"xcodebuild a prefix":          workflow("", "", "", "xcodebuild test -scheme Flare -only-testing:FlareCoreTestsExtra"),
		"xcodebuild -skip-testing":     workflow("", "", "", "xcodebuild test -scheme Flare -only-testing:FlareCoreTests -skip-testing:FlareCoreTests"),
		"manual trigger only": strings.Replace(workflow("", "", "", "swift test --package-path FlareCore"),
			"  pull_request:\n", "  workflow_dispatch:\n", 1),
		"schedule only": strings.Replace(workflow("", "", "", "swift test --package-path FlareCore"),
			"  pull_request:\n", "  schedule:\n    - cron: '0 4 * * *'\n", 1),
	}
	for name, wf := range cases {
		t.Run(name, func(t *testing.T) {
			root, targets := flare(t, map[string]string{".github/workflows/packages.yml": wf})
			r := audit(root, targets, flareSelectors, nil)
			if got := uncoveredNames(r); got != "FlareCoreTests FlareDataTests" && !(name == "another package" && got == "FlareCoreTests") {
				t.Fatalf("uncovered = %q; FlareCoreTests should still be reported\n%s", got, wf)
			}
			if len(r.Unchecked) != 0 {
				t.Errorf("unchecked = %+v, want none: this workflow was read and does not run it", r.Unchecked)
			}
		})
	}
}

// A package with two suites, and a `swift test` that runs one of them.
func TestSwiftTestFilterAndSkipNarrowWhatRuns(t *testing.T) {
	two := flarePackage("FlareCore", "FlareCoreTests", "FlareCoreSnapshotTests")
	cases := map[string]struct{ run, want string }{
		"no filter runs both":    {"swift test --package-path FlareCore", "FlareDataTests"},
		"--filter one suite":     {"swift test --package-path FlareCore --filter FlareCoreTests", "FlareCoreSnapshotTests FlareDataTests"},
		"--filter=suite.class":   {"swift test --package-path FlareCore --filter=FlareCoreTests.ParserTests", "FlareCoreSnapshotTests FlareDataTests"},
		"--filter is not prefix": {"swift test --package-path FlareCore --filter FlareCoreTestsX", "FlareCoreSnapshotTests FlareCoreTests FlareDataTests"},
		"--skip one suite":       {"swift test --package-path FlareCore --skip FlareCoreSnapshotTests", "FlareCoreSnapshotTests FlareDataTests"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root, targets := flare(t, map[string]string{
				"FlareCore/Package.swift":        two,
				".github/workflows/packages.yml": workflow("", "", "", c.run),
			})
			if got := uncoveredNames(audit(root, targets, flareSelectors, nil)); got != c.want {
				t.Fatalf("uncovered = %q, want %q", got, c.want)
			}
		})
	}
}

// fragments of a shared .xcscheme's TestAction.
func scheme(testables string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<Scheme LastUpgradeVersion = "1600" version = "1.7">
   <TestAction buildConfiguration = "Debug">
      <Testables>
` + testables + `      </Testables>
   </TestAction>
</Scheme>
`
}

func testable(name, skipped string) string {
	return `         <TestableReference skipped = "` + skipped + `">
            <BuildableReference BuildableIdentifier = "primary" BlueprintName = "` + name + `" ReferencedContainer = "container:../FlareCore">
            </BuildableReference>
         </TestableReference>
`
}

// `xcodebuild test -scheme X` with no -only-testing runs whatever the scheme's
// TestAction lists, so it reaches a package suite exactly when the committed
// scheme lists it and does not skip it.
func TestXcodebuildSchemeReachesWhatTheSchemeLists(t *testing.T) {
	const run = `xcodebuild test -project Flare/Flare.xcodeproj -scheme "Flare" -destination "platform=iOS Simulator,name=iPhone 16"`
	const schemePath = "Flare/Flare.xcodeproj/xcshareddata/xcschemes/Flare.xcscheme"
	cases := map[string]struct {
		scheme, wantUncovered string
		wantUnchecked         bool
	}{
		"listed":               {scheme(testable("FlareTests", "NO") + testable("FlareCoreTests", "NO")), "FlareDataTests", false},
		"listed, skipped":      {scheme(testable("FlareCoreTests", "YES")), "FlareCoreTests FlareDataTests", false},
		"not listed":           {scheme(testable("FlareTests", "NO")), "FlareCoreTests FlareDataTests", false},
		"scheme not committed": {"", "", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			extra := map[string]string{".github/workflows/packages.yml": workflow("", "", "", run)}
			if c.scheme != "" {
				extra[schemePath] = c.scheme
			}
			root, targets := flare(t, extra)
			r := audit(root, targets, flareSelectors, nil)
			if got := uncoveredNames(r); got != c.wantUncovered {
				t.Fatalf("uncovered = %q, want %q", got, c.wantUncovered)
			}
			if c.wantUnchecked != (len(r.Unchecked) == 2) {
				t.Fatalf("unchecked = %+v, want both suites unchecked: %v", r.Unchecked, c.wantUnchecked)
			}
			if c.wantUnchecked && !strings.Contains(Format(r), "Flare.xcscheme") {
				t.Errorf("the unchecked reason does not name the scheme file it could not read:\n%s", Format(r))
			}
		})
	}
}

// (b): a VERIFIED [[project.covered_elsewhere]] covers a package suite, for a
// workflow whose shape the audit does not recognise on its own (fastlane). An
// unverified one does not.
func TestCoveredElsewhereCoversAPackageSuiteOnlyWhenVerified(t *testing.T) {
	const fastlane = "bundle exec fastlane scan --scheme FlareCore --only_testing FlareCoreTests"
	d := []Declaration{{Target: "FlareCoreTests", Workflow: ".github/workflows/packages.yml", Reason: "fastlane runs the package scheme"}}

	root, targets := flare(t, map[string]string{".github/workflows/packages.yml": workflow("", "", "", fastlane)})
	r := audit(root, targets, flareSelectors, d)
	if got := uncoveredNames(r); got != "FlareDataTests" {
		t.Fatalf("uncovered = %q, want just FlareDataTests", got)
	}
	if len(r.Elsewhere) != 1 || r.Elsewhere[0].Target != "FlareCoreTests" {
		t.Fatalf("elsewhere = %+v, want the FlareCoreTests declaration", r.Elsewhere)
	}

	// The same declaration against a workflow that never names the suite.
	root, targets = flare(t, map[string]string{".github/workflows/packages.yml": workflow("", "", "", "bundle exec fastlane scan")})
	r = audit(root, targets, flareSelectors, d)
	if got := uncoveredNames(r); got != "FlareCoreTests FlareDataTests" {
		t.Fatalf("an unverified declaration removed a finding: uncovered = %q", got)
	}
	if out := Format(r); !strings.Contains(out, "NOT CONFIRMED") {
		t.Errorf("the failed declaration is not printed on the suite's line:\n%s", out)
	}
}

// A declaration for a package suite is checked like any other: it is not stale
// just because the suite is not a native target (it was, before this change).
func TestCoveredElsewhereSeesPackageSuites(t *testing.T) {
	root, targets := flare(t, nil)
	claims := Verify(root, []Declaration{{Target: "FlareCoreTests", Workflow: ".github/workflows/ios-ci.yml"}}, targets, nil)
	var declared []Claim
	for _, c := range claims {
		if !c.Detected {
			declared = append(declared, c)
		}
	}
	if len(declared) != 1 || declared[0].Stale != "" {
		t.Fatalf("declaration for a package suite = %+v, want it checked rather than stale", declared)
	}
}

// "Could not look" is not "it is not there", in this direction too. A package
// whose Package.swift is absent has suites the audit cannot name, so it cannot
// call them uncovered: it says it did not check, and why. Its readable
// neighbour is still judged.
func TestUnreadablePackageIsNotCheckedRatherThanUncovered(t *testing.T) {
	root, targets := flare(t, map[string]string{"FlareData/Package.swift": ""})
	r := audit(root, targets, flareSelectors, nil)
	if got := uncoveredNames(r); got != "FlareCoreTests" {
		t.Fatalf("uncovered = %q, want just FlareCoreTests", got)
	}
	if len(r.Unchecked) != 1 || r.Unchecked[0].Suite != "" || !strings.Contains(r.Unchecked[0].Reason, "FlareData/Package.swift does not exist") {
		t.Fatalf("unchecked = %+v, want FlareData's package, with its reason", r.Unchecked)
	}
	out := Format(r)
	for _, want := range []string{"could not check whether", "FlareData/Package.swift does not exist"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

// A `swift test` whose directory is an expression the audit cannot evaluate may
// be running any package — so the suites it might be running are not checked,
// rather than reported as running nowhere or as covered.
func TestSwiftTestInADirectoryTheAuditCannotResolveIsNotChecked(t *testing.T) {
	cases := map[string]string{
		"matrix package path": workflow("", "", "", `swift test --package-path "${{ matrix.package }}"`),
		"shell variable":      workflow("", "", "", `for p in FlareCore FlareData; do swift test --package-path "$p"; done`),
		"matrix working-dir":  workflow("", "", "        working-directory: ${{ matrix.package }}\n", "swift test"),
	}
	for name, wf := range cases {
		t.Run(name, func(t *testing.T) {
			root, targets := flare(t, map[string]string{".github/workflows/packages.yml": wf})
			r := audit(root, targets, flareSelectors, nil)
			if len(r.Uncovered) != 0 {
				t.Fatalf("uncovered = %q, want none: this workflow may run them", uncoveredNames(r))
			}
			if len(r.Unchecked) != 2 || !strings.Contains(r.Unchecked[0].Reason, ".github/workflows/packages.yml") {
				t.Fatalf("unchecked = %+v, want both suites, naming the workflow", r.Unchecked)
			}
		})
	}
}

// A workflow that cannot be parsed is a place the suite may be run, unexamined.
func TestUnparseableWorkflowIsNotChecked(t *testing.T) {
	root, targets := flare(t, map[string]string{".github/workflows/packages.yml": "on: [pull_request]\njobs: [unterminated\n"})
	r := audit(root, targets, flareSelectors, nil)
	if len(r.Uncovered) != 0 || len(r.Unchecked) != 2 {
		t.Fatalf("uncovered = %q, unchecked = %+v; want both suites unchecked", uncoveredNames(r), r.Unchecked)
	}
}

// A package outside the repository is not this repository's to run.
func TestPackageOutsideTheRepositoryIsNotChecked(t *testing.T) {
	root, targets := flare(t, nil)
	// The same targets, audited as though the project root were Flare/.
	r := audit(filepath.Join(root, "Flare"), targets, flareSelectors, nil)
	if len(r.Uncovered) != 0 || len(r.Unchecked) != 2 || !strings.Contains(r.Unchecked[0].Reason, "outside") {
		t.Fatalf("uncovered = %q, unchecked = %+v; want both suites unchecked as outside the repository", uncoveredNames(r), r.Unchecked)
	}
}

// Compare alone (no Verify) still reports package suites, with the package as
// the pbxproj spells it, so nothing depends on Apply having run to be seen.
func TestCompareAloneReportsPackageSuites(t *testing.T) {
	_, targets := flare(t, nil)
	r := Compare(targets, flareSelectors)
	if got := uncoveredNames(r); got != "FlareCoreTests FlareDataTests" {
		t.Fatalf("uncovered = %q", got)
	}
	if !strings.Contains(Format(r), "FlareCoreTests  (unit tests, local package ../FlareCore)") {
		t.Errorf("report:\n%s", Format(r))
	}
}

// sleevetap's shape, from the fleet dry-run: the step runs the package's own
// script, which moves to the package root, writes the workspace wrapper through
// a heredoc, and runs `flowdeck test` on the package's GENERATED scheme — named
// after the package, and running all its suites (91 tests on sleevetap's pull
// requests). Reported as running nowhere by the first version of this check.
const verifyScript = `#!/usr/bin/env bash
# Builds and tests the package, Debug and Release.
set -euo pipefail

cd "$(dirname "$0")/.."

workspace=".swiftpm/xcode/package.xcworkspace"
mkdir -p "$workspace"
cat > "$workspace/contents.xcworkspacedata" <<'XCWORKSPACE'
<?xml version="1.0" encoding="UTF-8"?>
swift test --package-path ../FlareData
XCWORKSPACE

for configuration in Debug Release; do
    flowdeck build -w "$workspace" -s SCHEME -C "$configuration"
    flowdeck test \
        -w "$workspace" \
        -s SCHEME \
        -C "$configuration" \
        --headless
done
`

func TestAScriptTheStepRunsIsReadInto(t *testing.T) {
	step := workflow("", "", "        working-directory: FlareCore\n", "./Scripts/verify.sh")
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"generated scheme named after the package": {map[string]string{
			"FlareCore/Scripts/verify.sh": strings.ReplaceAll(verifyScript, "SCHEME", "FlareCore")}, "FlareDataTests"},
		"the <package>-Package scheme": {map[string]string{
			"FlareCore/Scripts/verify.sh": strings.ReplaceAll(verifyScript, "SCHEME", "FlareCore-Package")}, "FlareDataTests"},
		// A product scheme has no test action; xcodebuild refuses it.
		"some other generated scheme": {map[string]string{
			"FlareCore/Scripts/verify.sh": strings.ReplaceAll(verifyScript, "SCHEME", "FlareCoreKit")}, "FlareCoreTests FlareDataTests"},
		// The heredoc's `swift test` line is data written to a file.
		"heredoc is not run": {map[string]string{
			"FlareCore/Scripts/verify.sh": strings.ReplaceAll(verifyScript, "SCHEME", "FlareCoreKit")}, "FlareCoreTests FlareDataTests"},
		"a committed scheme wins over the generated one": {map[string]string{
			"FlareCore/Scripts/verify.sh":                                        strings.ReplaceAll(verifyScript, "SCHEME", "FlareCore"),
			"FlareCore/.swiftpm/xcode/xcshareddata/xcschemes/FlareCore.xcscheme": scheme(testable("FlareCoreTests", "YES")),
		}, "FlareCoreTests FlareDataTests"},
		"no such script": {map[string]string{}, "FlareCoreTests FlareDataTests"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			extra := map[string]string{".github/workflows/swift-ci.yml": step}
			for k, v := range c.files {
				extra[k] = v
			}
			root, targets := flare(t, extra)
			r := audit(root, targets, flareSelectors, nil)
			if got := uncoveredNames(r); got != c.want {
				t.Fatalf("uncovered = %q, want %q (unchecked %+v)", got, c.want, r.Unchecked)
			}
			if len(r.Unchecked) != 0 {
				t.Fatalf("unchecked = %+v, want none", r.Unchecked)
			}
			if c.want == "FlareDataTests" && !strings.Contains(Format(r), "(in FlareCore/Scripts/verify.sh)") {
				t.Errorf("the evidence does not name the script it was found in:\n%s", Format(r))
			}
		})
	}
}

func TestMoreWaysARunIsSpelled(t *testing.T) {
	cases := map[string]struct{ run, want string }{
		"bash script.sh": {"bash scripts/test.sh", "FlareDataTests"},
		"bash -c":        {"bash -c 'cd FlareCore && swift test'", "FlareDataTests"},
		"a variable":     {"PKG=FlareCore\nswift test --package-path \"$PKG\"", "FlareDataTests"},
		"${variable}":    {"PKG=FlareCore\nswift test --package-path \"${PKG}\"", "FlareDataTests"},
		// `X=1 cmd` sets X for cmd only.
		"a prefix assignment does not persist":        {"PKG=FlareCore true\nswift test --package-path FlareData", "FlareCoreTests"},
		"bash -c does not move the caller":            {"bash -c 'cd FlareCore'\nswift test", "FlareCoreTests FlareDataTests"},
		"a script's cd does not move the caller":      {"./scripts/cd.sh\nswift test", "FlareCoreTests FlareDataTests"},
		"here-string is not a heredoc":                {"cat <<< \"x\"\nswift test --package-path FlareCore", "FlareDataTests"},
		"xcodebuild in the package, generated scheme": {"cd FlareCore\nxcodebuild test -scheme FlareCore -destination 'platform=macOS'", "FlareDataTests"},
		"xcodebuild on the package workspace":         {"xcodebuild test -workspace FlareCore/.swiftpm/xcode/package.xcworkspace -scheme FlareCore", "FlareDataTests"},
		"flowdeck --only":                             {"flowdeck test -w Flare/Flare.xcodeproj -s Flare --only FlareTests/A FlareCoreTests/B", "FlareDataTests"},
		"flowdeck --test-targets":                     {"flowdeck test -w Flare/Flare.xcodeproj -s Flare --test-targets FlareTests,FlareDataTests", "FlareCoreTests"},
		"flowdeck --skip the suite":                   {"flowdeck test -w FlareCore/.swiftpm/xcode/package.xcworkspace -s FlareCore --skip FlareCoreTests", "FlareCoreTests FlareDataTests"},
		"flowdeck discover runs nothing":              {"flowdeck test discover -w FlareCore/.swiftpm/xcode/package.xcworkspace -s FlareCore", "FlareCoreTests FlareDataTests"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root, targets := flare(t, map[string]string{
				".github/workflows/packages.yml": workflow("", "", "", c.run),
				"scripts/test.sh":                "#!/bin/sh\nset -e\nswift test --package-path FlareCore\n",
				"scripts/cd.sh":                  "#!/bin/sh\ncd FlareCore\n",
			})
			r := audit(root, targets, flareSelectors, nil)
			if got := uncoveredNames(r); got != c.want {
				t.Fatalf("uncovered = %q, want %q (unchecked %+v)", got, c.want, r.Unchecked)
			}
		})
	}
}

// flowdeck with no -w/-s runs whatever `flowdeck config set` saved, which is not
// in the repository in a form this reads.
func TestFlowdeckOnItsSavedConfigIsNotChecked(t *testing.T) {
	root, targets := flare(t, map[string]string{".github/workflows/packages.yml": workflow("", "", "", "flowdeck test --headless")})
	r := audit(root, targets, flareSelectors, nil)
	if len(r.Uncovered) != 0 || len(r.Unchecked) != 2 || !strings.Contains(r.Unchecked[0].Reason, "flowdeck config") {
		t.Fatalf("uncovered = %q, unchecked = %+v; want both unchecked", uncoveredNames(r), r.Unchecked)
	}
}

// A declaration for a suite the workflows are already seen running is a second
// answer, and it reads as the reason the suite is covered. Stale, with why.
func TestDeclarationForASuiteAWorkflowRunsIsStale(t *testing.T) {
	root, targets := flare(t, map[string]string{
		".github/workflows/packages.yml": workflow("", "", "", "swift test --package-path FlareCore # FlareCoreTests"),
	})
	d := []Declaration{{Target: "FlareCoreTests", Workflow: ".github/workflows/packages.yml", Reason: "r"}}
	r := audit(root, targets, flareSelectors, d)
	if len(r.Stale) != 1 || !strings.Contains(r.Stale[0].Stale, "already runs") || len(r.Elsewhere) != 0 {
		t.Fatalf("stale = %+v, elsewhere = %+v; want the declaration stale", r.Stale, r.Elsewhere)
	}
	if len(r.Ran) != 1 || uncoveredNames(r) != "FlareDataTests" {
		t.Fatalf("ran = %+v, uncovered = %q", r.Ran, uncoveredNames(r))
	}
}
