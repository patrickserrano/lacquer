package baseline

import (
	"os"
	"path/filepath"
	"testing"
)

// projectDirs builds a throwaway lacquer root + project root pair: an ios profile
// that ships the standard, and optionally a pbxproj at ios/App.xcodeproj.
//
// Run takes its targets as an argument rather than loading .lacquer.toml, because
// config imports this package to reach the Relax type — so importing config back
// would be a cycle. The caller assembles targets from the manifest; the
// manifest-to-target plumbing is covered where it lives, in the CLI wiring.
func projectDirs(t *testing.T, pbxproj string) (lacquerRoot, projectRoot string) {
	t.Helper()
	lacquerRoot, projectRoot = t.TempDir(), t.TempDir()

	dir := filepath.Join(lacquerRoot, "profiles", "ios")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := "[baseline]\nswift_version = \"6\"\nwarnings_as_errors = true\nstrict_concurrency = \"complete\"\n"
	if err := os.WriteFile(filepath.Join(dir, "baseline.toml"), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	if pbxproj != "" {
		xc := filepath.Join(projectRoot, "ios", "App.xcodeproj")
		if err := os.MkdirAll(xc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(xc, "project.pbxproj"), []byte(pbxproj), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return lacquerRoot, projectRoot
}

// writeSwift drops a Swift file at a project-relative path, creating parents.
// Baseline checks never read these — their only job is to make a fixture look
// like a project that has been written, as opposed to one that has not.
func writeSwift(t *testing.T, projectRoot, rel string) {
	t.Helper()
	path := filepath.Join(projectRoot, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("import Foundation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const compliantPbx = `
/* Begin XCBuildConfiguration section */
		APP1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`

const partialPbx = `
/* Begin XCBuildConfiguration section */
		APP1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
		EXT1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`

func iosTarget() []Target {
	return []Target{{Profile: "ios", Component: "ios", Xcodeproj: "ios/App.xcodeproj"}}
}

func TestRunCompliantProject(t *testing.T) {
	lr, pr := projectDirs(t, compliantPbx)
	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 1 {
		t.Fatalf("got %d reports, want 1", len(reps))
	}
	if reps[0].Profile != "ios" || reps[0].Component != "ios" {
		t.Errorf("report = %+v", reps[0])
	}
	if v := Violations(reps[0].Findings); len(v) != 0 {
		t.Errorf("violations = %+v, want none", v)
	}
}

func TestRunPartialCoverageViolates(t *testing.T) {
	lr, pr := projectDirs(t, partialPbx)
	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	v := Violations(reps[0].Findings)
	if len(v) != 1 || v[0].Key != "warnings_as_errors" {
		t.Fatalf("violations = %+v, want one for warnings_as_errors", v)
	}
	if v[0].Have != 1 || v[0].Total != 2 {
		t.Errorf("coverage = %d/%d, want 1/2", v[0].Have, v[0].Total)
	}
}

// A relaxation is honored, which is what makes the escape hatch real rather than
// theoretical.
func TestRunHonorsRelaxation(t *testing.T) {
	lr, pr := projectDirs(t, partialPbx)
	relax := map[string]Relax{
		"warnings_as_errors": {Until: "2099-01-01", Reason: "tracked in #142"},
	}
	reps, err := Run(lr, pr, iosTarget(), relax, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v := Violations(reps[0].Findings); len(v) != 0 {
		t.Errorf("violations = %+v, want none under an unexpired relaxation", v)
	}
}

// A profile that ships no baseline.toml produces no report at all.
func TestRunSkipsProfileWithoutSpec(t *testing.T) {
	lr, pr := projectDirs(t, "")
	targets := []Target{{Profile: "web", Component: "dashboard"}}
	reps, err := Run(lr, pr, targets, nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 0 {
		t.Errorf("reports = %+v, want none for a profile with no baseline", reps)
	}
}

// A component with a baseline but no xcodeproj cannot be checked. That is
// reported as such, not silently passed — "we could not look" must never render
// as "it is fine".
func TestRunReportsUncheckableComponent(t *testing.T) {
	lr, pr := projectDirs(t, "")
	targets := []Target{{Profile: "ios", Component: "ios"}} // no Xcodeproj
	reps, err := Run(lr, pr, targets, nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 1 {
		t.Fatalf("got %d reports, want 1", len(reps))
	}
	if reps[0].Unchecked == "" {
		t.Error("want an Unchecked reason when no xcodeproj is configured")
	}
	if len(reps[0].Findings) != 0 {
		t.Error("an unchecked component must not fabricate findings")
	}
}

// A configured xcodeproj that does not exist on disk is an error, not a pass —
// for a project that has Swift in it. That is the renamed/mistyped-path case,
// and it must never render as a pass.
func TestRunMissingXcodeprojIsAnError(t *testing.T) {
	lr, pr := projectDirs(t, "")
	writeSwift(t, pr, filepath.Join("ios", "App", "App.swift"))
	if _, err := Run(lr, pr, iosTarget(), nil, now); err == nil {
		t.Fatal("want an error for a configured xcodeproj that is absent, got nil")
	}
}

// The pre-code case: a project onboarded from an archetype declares the
// xcodeproj its CI will build before any Swift is written, because `sync`
// refuses to render the iOS assets with a blank {{XCODEPROJ}}. That is the
// documented workflow (archetypes/README.md), so it must not audit red. It is
// Unchecked — visible — rather than a pass.
func TestRunPreCodeXcodeprojIsUncheckedNotAnError(t *testing.T) {
	lr, pr := projectDirs(t, "")
	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 1 {
		t.Fatalf("got %d reports, want 1", len(reps))
	}
	if reps[0].Unchecked == "" {
		t.Error("want an Unchecked reason for an xcodeproj declared before the code exists")
	}
	if len(reps[0].Findings) != 0 {
		t.Error("an unchecked component must not fabricate findings")
	}
}

// The excuse is Swift the project wrote, not Swift that landed in it. Build
// output under DerivedData must not flip a not-yet-written project into the
// hard-error case, or the fix regresses the moment anyone builds once.
func TestRunPreCodeIgnoresSwiftInBuildOutput(t *testing.T) {
	lr, pr := projectDirs(t, "")
	writeSwift(t, pr, filepath.Join("ios", "DerivedData", "Build", "Generated.swift"))
	writeSwift(t, pr, filepath.Join("ios", ".build", "checkouts", "Dep", "Dep.swift"))
	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 1 || reps[0].Unchecked == "" {
		t.Errorf("reports = %+v, want one Unchecked report — build output is not the project's own Swift", reps)
	}
}

// A gitignored, XcodeGen-generated .xcodeproj (project.yml present, no
// xcodeproj committed) must not be the same hard error as a renamed or
// mistyped path — it needs xcodegen to make it appear at all, and this
// environment might not have it (e.g. a GitHub-hosted Linux runner running
// the web/supabase profiles' "No lacquer drift" job). Discovered live on
// sleevetap, which deliberately doesn't commit its generated pbxproj.
func TestRunProjectYMLWithoutXcodegenIsUncheckedNotAnError(t *testing.T) {
	lr, pr := projectDirs(t, "")
	writeSwift(t, pr, filepath.Join("ios", "App", "App.swift"))
	if err := os.WriteFile(filepath.Join(pr, "ios", "project.yml"), []byte("name: App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // no xcodegen reachable

	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 1 || reps[0].Unchecked == "" {
		t.Fatalf("reports = %+v, want one Unchecked report — no xcodegen to regenerate the project", reps)
	}
	if len(reps[0].Findings) != 0 {
		t.Error("an unchecked component must not fabricate findings")
	}
}

// The other half: when xcodegen IS available, the project.yml sibling gets
// regenerated and the baseline check proceeds normally against the result —
// this is the fix actually working, not just failing safe.
func TestRunProjectYMLRegeneratesAndChecksWhenXcodegenAvailable(t *testing.T) {
	lr, pr := projectDirs(t, "")
	writeSwift(t, pr, filepath.Join("ios", "App", "App.swift"))
	if err := os.WriteFile(filepath.Join(pr, "ios", "project.yml"), []byte("name: App\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A fake `xcodegen` that just writes the pbxproj the rest of Run expects,
	// standing in for the real generator so this test is deterministic
	// regardless of whether xcodegen is actually installed on the machine
	// running it.
	binDir := t.TempDir()
	// Run in the directory Run() passes as cmd.Dir (the project.yml's own
	// directory, same as where the xcodeproj is expected) -- so paths here
	// are relative to THAT, not the project root.
	script := "#!/bin/sh\nmkdir -p App.xcodeproj\ncat > App.xcodeproj/project.pbxproj <<'EOF'\n" + compliantPbx + "EOF\n"
	if err := os.WriteFile(filepath.Join(binDir, "xcodegen"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Prepend, not replace: the script itself shells out to mkdir/cat, which
	// need the real PATH to resolve.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(reps) != 1 {
		t.Fatalf("got %d reports, want 1", len(reps))
	}
	if reps[0].Unchecked != "" {
		t.Errorf("Unchecked = %q, want the regenerated project to be checked", reps[0].Unchecked)
	}
	if v := Violations(reps[0].Findings); len(v) != 0 {
		t.Errorf("violations = %+v, want none against the regenerated compliant project", v)
	}
}

// Blocking aggregates across every report so one caller can decide the exit code.
func TestBlockingAcrossReports(t *testing.T) {
	lr, pr := projectDirs(t, partialPbx)
	reps, err := Run(lr, pr, iosTarget(), nil, now)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := Blocking(reps); n != 1 {
		t.Errorf("Blocking = %d, want 1", n)
	}
}
