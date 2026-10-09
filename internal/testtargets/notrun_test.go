package testtargets

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bravoapp's shape. ios/BravoApp.xcodeproj references the local package
// ios/BravoAppCore, whose one suite needs on-device models and is written to
// fail, not skip, without them. ios-ci.yml compiles it (`swift build
// --build-tests`) and deliberately never runs it, so no selector names it and no
// workflow runs it — the report is right that it runs nowhere, and none of the
// fixes it offers fits a suite run by a person on a device on purpose.
const bravoappPbx = `// !$*UTF8*$!
{
	objects = {
		A1 /* BravoApp */ = {
			isa = PBXNativeTarget;
			name = BravoApp;
			productType = "com.apple.product-type.application";
		};
		A2 /* BravoAppTests */ = {
			isa = PBXNativeTarget;
			name = BravoAppTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
		A3 /* BravoAppUITests */ = {
			isa = PBXNativeTarget;
			name = BravoAppUITests;
			productType = "com.apple.product-type.bundle.ui-testing";
		};
		B1 /* XCLocalSwiftPackageReference "BravoAppCore" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = BravoAppCore;
		};
	};
}
`

const bravoappCI = `name: iOS CI
on:
  pull_request:
jobs:
  test:
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build Swift packages
        run: |
          swift build --package-path ios/BravoAppCore --build-tests \
            -Xswiftc -warnings-as-errors
      - name: Test
        run: |
          xcodebuild test \
            -project ios/BravoApp.xcodeproj \
            -scheme BravoApp \
            "-only-testing:BravoAppTests"
`

var bravoappSelectors = []string{"BravoAppTests"}

const bravoappReason = "needs on-device models; built in CI, run on device before release"

// inTerm and expired sit either side of the declaration's until date.
var (
	inTerm  = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	expired = time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
)

func bravoappDecl() []NotRun {
	return []NotRun{{Target: "BravoAppCoreTests", Reason: bravoappReason, Until: "2026-12-31"}}
}

// bravoapp lays the project out with the given extra files; "" deletes one.
func bravoapp(t *testing.T, extra map[string]string) (string, []Target) {
	t.Helper()
	files := map[string]string{
		"ios/BravoApp.xcodeproj/project.pbxproj": bravoappPbx,
		"ios/BravoAppCore/Package.swift":         flarePackage("BravoAppCore", "BravoAppCoreTests"),
		".github/workflows/ios-ci.yml":           bravoappCI,
	}
	for k, v := range extra {
		if v == "" {
			delete(files, k)
			continue
		}
		files[k] = v
	}
	pbx := writeProject(t, "ios/BravoApp.xcodeproj", files)
	root := filepath.Dir(filepath.Dir(filepath.Dir(pbx)))
	return root, parsePath(t, pbx)
}

// deliberate runs the whole audit path: compare, verify, apply, deliberate.
func deliberate(root string, targets []Target, selectors []string, decls []Declaration, notRun []NotRun, now time.Time) Report {
	return Deliberate(audit(root, targets, selectors, decls), targets, notRun, now)
}

// The control: without a declaration, bravoapp's suite is uncovered. If this
// ever fails, every test below is testing a fixture, not the feature.
func TestBravoappSuiteIsUncoveredWithoutADeclaration(t *testing.T) {
	root, targets := bravoapp(t, nil)
	r := deliberate(root, targets, bravoappSelectors, nil, nil, inTerm)
	if got := uncoveredNames(r); got != "BravoAppCoreTests BravoAppUITests" {
		t.Fatalf("uncovered = %q, want BravoAppCoreTests BravoAppUITests", got)
	}
	if len(r.NotRun) != 0 || Blocking(r) != 0 {
		t.Fatalf("notRun = %+v, blocking = %d; want none", r.NotRun, Blocking(r))
	}
	if strings.Contains(Format(r), "deliberately not run in CI") {
		t.Errorf("an undeclared suite was described as deliberate:\n%s", Format(r))
	}
}

// The case this exists for. The declared suite leaves "no selector covers" and
// gets a line of its own, reason and date included; the other uncovered target
// is untouched, and nothing blocks.
func TestDeclaredSuiteMovesToItsOwnLine(t *testing.T) {
	root, targets := bravoapp(t, nil)
	r := deliberate(root, targets, bravoappSelectors, nil, bravoappDecl(), inTerm)
	if got := uncoveredNames(r); got != "BravoAppUITests" {
		t.Fatalf("uncovered = %q, want just BravoAppUITests", got)
	}
	if len(r.NotRun) != 1 {
		t.Fatalf("notRun = %+v, want one", r.NotRun)
	}
	if c := r.NotRun[0]; c.Expired || c.Stale != "" || !c.Applied {
		t.Fatalf("claim = %+v, want applied, in term, not stale", c)
	}
	if n := Blocking(r); n != 0 {
		t.Errorf("Blocking = %d, want 0 while in term", n)
	}
	out := Format(r)
	want := "deliberately not run in CI: BravoAppCoreTests — " + bravoappReason + " (until 2026-12-31)"
	if !strings.Contains(out, want) {
		t.Errorf("report does not contain\n  %s\n%s", want, out)
	}
	// Printed once, as deliberate: not also listed as running nowhere.
	uncoveredSection := out[strings.Index(out, "test targets no selector covers:"):]
	uncoveredSection, _, _ = strings.Cut(uncoveredSection, "deliberately not run in CI:")
	if strings.Contains(uncoveredSection, "BravoAppCoreTests  (") {
		t.Errorf("the declared suite is still listed as uncovered:\n%s", out)
	}
}

// The re-surfacing. Past `until` the suite is a finding again, the expiry is
// named, and the audit blocks.
func TestExpiredDeclarationComesBackAndBlocks(t *testing.T) {
	root, targets := bravoapp(t, nil)
	r := deliberate(root, targets, bravoappSelectors, nil, bravoappDecl(), expired)
	if got := uncoveredNames(r); got != "BravoAppCoreTests BravoAppUITests" {
		t.Fatalf("uncovered = %q; an expired declaration must stop suppressing", got)
	}
	if len(r.NotRun) != 1 || !r.NotRun[0].Expired || r.NotRun[0].Applied {
		t.Fatalf("notRun = %+v, want one expired, not applied", r.NotRun)
	}
	if n := Blocking(r); n != 1 {
		t.Fatalf("Blocking = %d, want 1", n)
	}
	out := Format(r)
	for _, want := range []string{"BravoAppCoreTests", "EXPIRED 2026-12-31", bravoappReason, "exit 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

// The boundary, pinned. The whole of the until DAY is in term — "until
// 2026-12-31" reads as "through the 31st" — and the first instant after it is
// not. Same construction as depignore and exclusion.
func TestUntilIsInclusiveOfTheWholeDay(t *testing.T) {
	root, targets := bravoapp(t, nil)
	for _, tc := range []struct {
		now     time.Time
		expired bool
	}{
		{time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), false},
		{time.Date(2026, 12, 31, 23, 59, 59, 999999999, time.UTC), false},
		{time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{time.Date(2027, 1, 1, 0, 0, 0, 1, time.UTC), true},
	} {
		r := deliberate(root, targets, bravoappSelectors, nil, bravoappDecl(), tc.now)
		if got := r.NotRun[0].Expired; got != tc.expired {
			t.Errorf("at %s: expired = %v, want %v", tc.now.Format(time.RFC3339Nano), got, tc.expired)
		}
		if got := Blocking(r) == 1; got != tc.expired {
			t.Errorf("at %s: blocking = %v, want %v", tc.now.Format(time.RFC3339Nano), got, tc.expired)
		}
	}
}

// An until that cannot be read is not in term. Load rejects one, so this is
// the fallback if that ever changes: fail closed, not open.
func TestUnreadableUntilIsExpired(t *testing.T) {
	root, targets := bravoapp(t, nil)
	decls := []NotRun{{Target: "BravoAppCoreTests", Reason: bravoappReason, Until: "someday"}}
	r := deliberate(root, targets, bravoappSelectors, nil, decls, inTerm)
	if !r.NotRun[0].Expired || Blocking(r) != 1 || !uncovered(r, "BravoAppCoreTests") {
		t.Fatalf("claim = %+v, blocking = %d; an unreadable date must not be in term", r.NotRun[0], Blocking(r))
	}
}

// Stale because it is covered now: whatever runs it, the declaration is
// claiming a gap that closed, and it has to say which thing closed it.
func TestDeclarationForACoveredSuiteIsStale(t *testing.T) {
	cases := []struct {
		name      string
		extra     map[string]string
		selectors []string
		decls     []Declaration
		want      string
	}{
		{
			name:      "a selector names it",
			selectors: []string{"BravoAppTests", "BravoAppCoreTests"},
			want:      "a managed test selector names it",
		},
		{
			name:  "a workflow runs it",
			extra: map[string]string{".github/workflows/core.yml": workflow("", "", "", "swift test --package-path ios/BravoAppCore")},
			want:  ".github/workflows/core.yml runs it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, targets := bravoapp(t, tc.extra)
			sel := tc.selectors
			if sel == nil {
				sel = bravoappSelectors
			}
			r := deliberate(root, targets, sel, tc.decls, bravoappDecl(), inTerm)
			if uncovered(r, "BravoAppCoreTests") {
				t.Fatalf("BravoAppCoreTests is uncovered; the fixture does not cover it")
			}
			if len(r.NotRun) != 1 || r.NotRun[0].Applied || !strings.Contains(r.NotRun[0].Stale, tc.want) {
				t.Fatalf("notRun = %+v, want stale naming %q", r.NotRun, tc.want)
			}
			if Blocking(r) != 0 {
				t.Errorf("a stale declaration in term blocked")
			}
			out := Format(r)
			for _, want := range []string{"not_run_in_ci declarations that are not doing anything", "BravoAppCoreTests — ", tc.want} {
				if !strings.Contains(out, want) {
					t.Errorf("report does not contain %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "deliberately not run in CI: BravoAppCoreTests") {
				t.Errorf("a covered suite is still described as not run:\n%s", out)
			}
		})
	}
}

// Native targets alike, including coverage by a verified covered_elsewhere.
// alphaapp's watch suite is the fixture: declared not-run it moves; declared
// both not-run and covered elsewhere (which config rejects, but this package
// does not rely on that) the verified claim wins and the declaration is stale.
func TestNativeTargetsAreDeclaredTheSameWay(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})
	targets := parseFixture(t)
	notRun := []NotRun{{Target: watchTarget, Reason: "watch suite run by hand on a paired device", Until: "2026-12-31"}}

	r := Deliberate(Apply(Compare(targets, nil), Verify(dir, nil, targets, nil)), targets, notRun, inTerm)
	if uncovered(r, watchTarget) {
		t.Fatalf("the declared native target is still uncovered")
	}
	if len(r.NotRun) != 1 || !r.NotRun[0].Applied {
		t.Fatalf("notRun = %+v, want the native target applied", r.NotRun)
	}

	r = Deliberate(Apply(Compare(targets, nil), Verify(dir, decl(), targets, nil)), targets, notRun, inTerm)
	if len(r.Elsewhere) != 1 {
		t.Fatalf("fixture: covered_elsewhere did not verify: %+v", r.Unconfirmed)
	}
	if len(r.NotRun) != 1 || !strings.Contains(r.NotRun[0].Stale, "[[project.covered_elsewhere]]") {
		t.Fatalf("notRun = %+v, want stale naming covered_elsewhere", r.NotRun)
	}
}

// Stale because it is missing: renamed or deleted, and the declaration names
// nothing. A declaration that names nothing and still printed as a live
// exception is how a project comes to believe a gap is being managed.
func TestDeclarationForAMissingTargetIsStale(t *testing.T) {
	root, targets := bravoapp(t, nil)
	decls := []NotRun{{Target: "BravoAppKitTests", Reason: "renamed since", Until: "2026-12-31"}}
	r := deliberate(root, targets, bravoappSelectors, nil, decls, inTerm)
	if len(r.NotRun) != 1 || !strings.Contains(r.NotRun[0].Stale, "no test target with that name") {
		t.Fatalf("notRun = %+v, want stale: no such target", r.NotRun)
	}
	if got := uncoveredNames(r); got != "BravoAppCoreTests BravoAppUITests" {
		t.Errorf("uncovered = %q; a stale declaration must not move anything", got)
	}
	if !strings.Contains(Format(r), "BravoAppKitTests — this project has no test target with that name") {
		t.Errorf("report does not name the stale declaration:\n%s", Format(r))
	}
}

// ...unless a package the target could live in could not be read. "Could not
// look" is not "it is not there", here as everywhere else in this package.
func TestMissingTargetBesideAnUnreadablePackageIsNotStale(t *testing.T) {
	root, targets := bravoapp(t, map[string]string{"ios/BravoAppCore/Package.swift": ""})
	r := deliberate(root, targets, bravoappSelectors, nil, bravoappDecl(), inTerm)
	if len(r.NotRun) != 1 || r.NotRun[0].Stale != "" {
		t.Fatalf("notRun = %+v; a target in an unreadable package must not be called missing", r.NotRun)
	}
}

// A suite the audit could not decide about (a workflow it could not read) is
// answered by the declaration: it moves out of "could not check" too.
func TestDeclaredUncheckedSuiteMoves(t *testing.T) {
	root, targets := bravoapp(t, map[string]string{
		".github/workflows/core.yml": workflow("", "", "", `swift test --package-path "${{ matrix.package }}"`),
	})
	r := deliberate(root, targets, bravoappSelectors, nil, nil, inTerm)
	if len(r.Unchecked) != 1 || r.Unchecked[0].Suite != "BravoAppCoreTests" {
		t.Fatalf("fixture: unchecked = %+v, want BravoAppCoreTests", r.Unchecked)
	}
	r = deliberate(root, targets, bravoappSelectors, nil, bravoappDecl(), inTerm)
	if len(r.Unchecked) != 0 || len(r.NotRun) != 1 || !r.NotRun[0].Applied {
		t.Fatalf("unchecked = %+v, notRun = %+v; want the suite moved to deliberate", r.Unchecked, r.NotRun)
	}
	// Expired, it goes back where it was.
	r = deliberate(root, targets, bravoappSelectors, nil, bravoappDecl(), expired)
	if len(r.Unchecked) != 1 || Blocking(r) != 1 {
		t.Fatalf("unchecked = %+v, blocking = %d; want the suite back and the audit blocked", r.Unchecked, Blocking(r))
	}
}

// Stale and expired together still blocks: the entry is past its term, and
// the fix — delete it — is the same either way.
func TestStaleAndExpiredStillBlocks(t *testing.T) {
	root, targets := bravoapp(t, nil)
	decls := []NotRun{{Target: "BravoAppKitTests", Reason: "renamed since", Until: "2026-12-31"}}
	r := deliberate(root, targets, bravoappSelectors, nil, decls, expired)
	if Blocking(r) != 1 {
		t.Fatalf("Blocking = %d, want 1", Blocking(r))
	}
	if out := Format(r); !strings.Contains(out, "EXPIRED 2026-12-31") {
		t.Errorf("report does not say the stale entry also expired:\n%s", out)
	}
}

// Nothing declared, nothing changed: the report must be byte-identical to what
// Apply alone produces, which is what the fleet dry-run relies on.
func TestNoNotRunDeclarationsChangesNothing(t *testing.T) {
	root, targets := bravoapp(t, nil)
	before := Format(audit(root, targets, bravoappSelectors, nil))
	after := Format(deliberate(root, targets, bravoappSelectors, nil, nil, inTerm))
	if before != after {
		t.Errorf("output changed with nothing declared:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// A report whose only content is the declaration still prints it. With every
// other target covered, the deliberate line is the whole report — and an empty
// report here would be the exception made invisible, the one thing it must not be.
func TestADeclarationAloneIsStillPrinted(t *testing.T) {
	root, targets := bravoapp(t, nil)
	sel := []string{"BravoAppTests", "BravoAppUITests"}
	if out := Format(deliberate(root, targets, sel, nil, nil, inTerm)); !strings.Contains(out, "BravoAppCoreTests") {
		t.Fatalf("fixture: without the declaration the suite should be the one finding:\n%s", out)
	}
	r := deliberate(root, targets, sel, nil, bravoappDecl(), inTerm)
	if len(r.Uncovered) != 0 {
		t.Fatalf("uncovered = %q, want none", uncoveredNames(r))
	}
	if out := Format(r); !strings.Contains(out, "deliberately not run in CI: BravoAppCoreTests") {
		t.Errorf("the declaration is the whole report and was not printed:\n%q", out)
	}
}
