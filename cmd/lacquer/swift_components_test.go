package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #522 U4: `lacquer swift-components --check` is the Lint job's "Every Swift
// file belongs to a declared component" step, and `lacquer audit` reports the
// same list. Both read the manifest's components; neither scans for configs.

func swiftComponents(t *testing.T, dir string, env map[string]string, args ...string) (int, string) {
	t.Helper()
	chdir(t, dir)
	var out, errb bytes.Buffer
	code := run(append([]string{"swift-components"}, args...), envMap(env), &out, &errb)
	return code, out.String() + errb.String()
}

// multiswiftWithUntracked is the multiswift fixture plus a stray that is on
// disk but never committed: the file someone is about to commit, which a
// pre-commit run of the check must see.
func multiswiftWithUntracked(t *testing.T) string {
	t.Helper()
	dir := fixtureCopy(t, "multiswift")
	p := filepath.Join(dir, "Scratch", "Untracked.swift")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("let x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The fail path, past the gate date: exit 1, every stray listed (committed and
// untracked), every discovered component named, and the remedy.
func TestSwiftComponentsCheckFailsOnAStrayFile(t *testing.T) {
	gateFrom(t, longAgo)
	code, out := swiftComponents(t, multiswiftWithUntracked(t), nil, "--check")
	if code != 1 {
		t.Fatalf("swift-components --check exited %d, want 1 for stray Swift past the gate date\n%s", code, out)
	}
	for _, want := range []string{
		"Stray.swift", "Scratch/Untracked.swift",
		"ios (app)", "tools",
		`path = "Scratch"`, `stack = "ios"`,
		"BLOCKING since 2020-01-01",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tools/beta") || strings.Contains(out, "ios/Packages") {
		t.Errorf("a file under a declared component was reported as stray:\n%s", out)
	}
}

// Before the gate date the same finding is a warning: exit 0, and the date it
// becomes a failure is printed. Under GitHub Actions it is a ::warning::
// annotation, and past the date an ::error:: one.
func TestSwiftComponentsCheckWarnsUntilTheGateDate(t *testing.T) {
	gateFrom(t, farAhead)
	dir := multiswiftWithUntracked(t)
	code, out := swiftComponents(t, dir, map[string]string{"GITHUB_ACTIONS": "true"}, "--check")
	if code != 0 {
		t.Fatalf("swift-components --check exited %d before the gate date, want 0 (a warning)\n%s", code, out)
	}
	if !strings.Contains(out, "::warning") || !strings.Contains(out, "This becomes a failure on 2099-01-01") {
		t.Errorf("no ::warning:: annotation carrying the gate date:\n%s", out)
	}
	if strings.Contains(out, "::error") {
		t.Errorf("an ::error:: annotation before the gate date:\n%s", out)
	}

	gateFrom(t, longAgo)
	code, out = swiftComponents(t, dir, map[string]string{"GITHUB_ACTIONS": "true"}, "--check")
	if code != 1 || !strings.Contains(out, "::error") {
		t.Errorf("past the gate date: exit %d, want 1 with an ::error:: annotation\n%s", code, out)
	}
	// Annotations are for Actions only; a local run prints plain text.
	_, out = swiftComponents(t, dir, nil, "--check")
	if strings.Contains(out, "::error") || strings.Contains(out, "::warning") {
		t.Errorf("annotations printed outside GitHub Actions:\n%s", out)
	}
}

// The silent path, on the shapes the fleet has: a root layout with a local
// package, two products, and three stacks with the app under ios/. Each exits 0
// and says how much it checked, so a run that looked at nothing is visible.
func TestSwiftComponentsCheckIsSilentWhenEverythingIsCovered(t *testing.T) {
	gateFrom(t, longAgo)
	for _, fixture := range []string{"rootapp", "duoapp", "multistack"} {
		t.Run(fixture, func(t *testing.T) {
			code, out := swiftComponents(t, fixtureCopy(t, fixture), map[string]string{"GITHUB_ACTIONS": "true"}, "--check")
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, "every Swift file belongs to a declared component") {
				t.Errorf("no positive line naming what was checked:\n%s", out)
			}
			if strings.Contains(out, "::warning") || strings.Contains(out, "::error") {
				t.Errorf("an annotation on a clean project:\n%s", out)
			}
		})
	}
}

// Outside a git work tree the files cannot be listed. That is an error, not a
// clean pass.
func TestSwiftComponentsCheckFailsClosedWithoutGit(t *testing.T) {
	dir := t.TempDir()
	manifest := "[project]\nname = \"x\"\n\n[[component]]\npath = \"ios\"\nprofiles = [\"ios\"]\n"
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := swiftComponents(t, dir, nil, "--check")
	if code == 0 {
		t.Fatalf("swift-components --check passed where it could not list any file:\n%s", out)
	}
}

// Without --check the command lists what it would lint and build: the
// components, and the packages the Lint job builds.
func TestSwiftComponentsListsComponentsAndPackages(t *testing.T) {
	code, out := swiftComponents(t, fixtureCopy(t, "multiswift"), nil)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"ios (app)", "tools", "tools/alpha", "tools/beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing does not include %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ios/Packages/MultiswiftCore") {
		t.Errorf("a package under the app component is listed as built by Lint:\n%s", out)
	}
	if strings.Contains(out, "NOT built") {
		t.Errorf("a macOS-buildable package is listed as not built:\n%s", out)
	}

	// An iOS-only package is listed, and marked as not built.
	dir := fixtureCopy(t, "multiswift")
	p := filepath.Join(dir, "tools", "beta", "Package.swift")
	if err := os.WriteFile(p, []byte("let package = Package(name: \"Beta\", platforms: [.iOS(.v18)])\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out = swiftComponents(t, dir, nil)
	if !strings.Contains(out, "tools/beta  NOT built by Lint: iOS-only") {
		t.Errorf("an iOS-only package is not marked as not built:\n%s", out)
	}
}

// `lacquer audit` prints the same list and, past the gate date, exits 6: Swift
// under no component is a stack the manifest does not declare. Before the
// date it is printed and does not change the exit code.
func TestAuditCountsStraySwiftAsUndeclared(t *testing.T) {
	dir := multiswiftWithUntracked(t)

	gateFrom(t, farAhead)
	code, out := auditAt(t, dir)
	if code != 0 {
		t.Errorf("audit exited %d before the gate date, want 0 (strays only warn)\n%s", code, out)
	}
	if !strings.Contains(out, "Stray.swift") || !strings.Contains(out, "Not blocking yet: from 2099-01-01") {
		t.Errorf("audit before the gate date does not report the strays with the date:\n%s", out)
	}

	gateFrom(t, longAgo)
	code, out = auditAt(t, dir)
	if code != 6 {
		t.Errorf("audit exited %d past the gate date, want 6 (undeclared stack)\n%s", code, out)
	}
	if !strings.Contains(out, "Scratch/Untracked.swift") {
		t.Errorf("audit does not list the untracked stray:\n%s", out)
	}
}

// A local audit outside a git work tree cannot list the repository's files. It
// says so, and neither errors nor reports the project clean of strays.
func TestAuditSaysStraySwiftWasNotCheckedOutsideGit(t *testing.T) {
	gateFrom(t, longAgo)
	dir := fixtureCopy(t, "multiswift")
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	code, out := auditAt(t, dir)
	if code == 1 {
		t.Fatalf("audit errored outside a git work tree:\n%s", out)
	}
	if !strings.Contains(out, "NOT checked for stray Swift files") {
		t.Errorf("audit does not say the stray check did not run:\n%s", out)
	}
}
