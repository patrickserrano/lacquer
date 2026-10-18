package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

// #522 U4b: a Swift component lists local SwiftPM packages nested inside it
// whose tests CI must compile. The tests here EXECUTE the rendered "Build Swift
// packages" step against a swift stub that fails exactly when the package it is
// pointed at holds a planted compile error in its TEST target, so a step that
// built the wrong directory, or none, or swallowed the failure, is red here.

const (
	nestedMac = "let package = Package(name: \"X\", platforms: [.iOS(.v26), .macOS(.v26)])\n"
	nestedIOS = "let package = Package(name: \"X\", platforms: [.iOS(.v26)])\n"
	planted   = "BROKEN: this does not compile\n"
)

// nestedRepo is an app component `ios` holding two nested packages and a third
// that is iOS-only, committed, with the given files overriding or adding to it.
func nestedRepo(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"ios/App/App.swift":                           "// app\n",
		"ios/AppCore/Package.swift":                   nestedMac,
		"ios/AppCore/Sources/AppCore/A.swift":         "// ok\n",
		"ios/AppCore/Tests/AppCoreTests/ATests.swift": "// ok\n",
		"ios/Libs/Net/Package.swift":                  nestedMac,
		"ios/Libs/Net/Sources/Net/N.swift":            "// ok\n",
		"ios/Libs/Net/Tests/NetTests/NTests.swift":    "// ok\n",
		"ios/Device/Package.swift":                    nestedIOS,
		"ios/Device/Sources/Device/D.swift":           "// ok\n",
		"ios/Device/Tests/DeviceTests/DTests.swift":   "// ok\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Init(t, dir, "-q")
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "f"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func nestedConfig(dir string, packages ...string) *config.Config {
	cfg := soloConfig()
	cfg.Root = dir
	cfg.Components = []config.Component{{Path: "ios", Profiles: []string{"ios"}, Stack: "ios", Packages: packages}}
	return cfg
}

// swiftBuildingTests stands in for `swift build --build-tests --package-path
// <dir>`: it records its argv and fails, with a compiler-shaped message, when
// <dir>/Tests holds a file carrying the planted marker.
func swiftBuildingTests(t *testing.T) (bin, log string) {
	t.Helper()
	bin = t.TempDir()
	log = filepath.Join(bin, "runs")
	stubTool(t, bin, "swift", `echo "$*" >> "`+log+`"
pkg=""
while [ $# -gt 0 ]; do [ "$1" = "--package-path" ] && pkg="$2"; shift; done
if grep -rq BROKEN "$pkg/Tests" 2>/dev/null; then
  echo "$pkg/Tests/x.swift:1:1: error: expected declaration"; exit 1
fi
`)
	return bin, log
}

const buildFlags = " --only-use-versions-from-resolved-file -Xswiftc -warnings-as-errors"

// The fail path first: a compile error planted in a LISTED package's test
// target turns the step red, naming the package, and the other listed package
// is still attempted.
func TestBuildPackagesStepIsRedForACompileErrorInAListedPackagesTests(t *testing.T) {
	dir := nestedRepo(t, map[string]string{"ios/AppCore/Tests/AppCoreTests/ATests.swift": planted})
	body := stepRun(t, parseIOSCI(t, nestedConfig(dir, "AppCore", "Libs/Net")), "lint", "Build Swift packages")
	bin, log := swiftBuildingTests(t)
	out, code := runStepIn(t, dir, body, bin)
	if code == 0 {
		t.Fatalf("Build Swift packages passed with a compile error in ios/AppCore's tests\n%s", out)
	}
	if !strings.Contains(out, "Swift package ios/AppCore did not build") || strings.Contains(out, "ios/Libs/Net did not build") {
		t.Errorf("the failure does not name exactly the broken package\n%s", out)
	}
	if n := len(runs(t, log)); n != 2 {
		t.Errorf("%d swift runs, want both listed packages attempted", n)
	}

	// The same tree with the plant removed is green, so the stub is not simply
	// failing everything.
	dir = nestedRepo(t, nil)
	body = stepRun(t, parseIOSCI(t, nestedConfig(dir, "AppCore", "Libs/Net")), "lint", "Build Swift packages")
	bin, log = swiftBuildingTests(t)
	if out, code := runStepIn(t, dir, body, bin); code != 0 {
		t.Fatalf("Build Swift packages failed on a clean tree\n%s", out)
	}
	want := []string{
		"build --build-tests --package-path ios/AppCore" + buildFlags,
		"build --build-tests --package-path ios/Libs/Net" + buildFlags,
	}
	if got := runs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("swift runs = %q, want %q", got, want)
	}
}

// An unlisted nested package is NOT built, even with a compile error in it:
// the step is green and swift is never pointed at it.
func TestBuildPackagesStepDoesNotBuildAnUnlistedNestedPackage(t *testing.T) {
	dir := nestedRepo(t, map[string]string{"ios/Libs/Net/Tests/NetTests/NTests.swift": planted})
	body := stepRun(t, parseIOSCI(t, nestedConfig(dir, "AppCore")), "lint", "Build Swift packages")
	bin, log := swiftBuildingTests(t)
	out, code := runStepIn(t, dir, body, bin)
	if code != 0 {
		t.Fatalf("an unlisted package's compile error failed the step\n%s", out)
	}
	want := []string{"build --build-tests --package-path ios/AppCore" + buildFlags}
	if got := runs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("swift runs = %q, want only %q (ios/Libs/Net and ios/Device are nested but unlisted)", got, want)
	}
}

// An iOS-only listed package is neither built nor passed over: the named
// notice appears, and the step is not the "nothing declared" no-op.
func TestBuildPackagesStepNamesAListedIOSOnlyPackageAndDoesNotBuildIt(t *testing.T) {
	dir := nestedRepo(t, map[string]string{"ios/Device/Tests/DeviceTests/DTests.swift": planted})
	body := stepRun(t, parseIOSCI(t, nestedConfig(dir, "AppCore", "Device")), "lint", "Build Swift packages")
	bin, log := swiftBuildingTests(t)
	out, code := runStepIn(t, dir, body, bin)
	if code != 0 {
		t.Fatalf("exit %d; an iOS-only package is skipped with a notice, not failed\n%s", code, out)
	}
	if !strings.Contains(out, "::notice title=Swift package not built::Not built: ios/Device is an iOS-only package") {
		t.Errorf("no notice naming ios/Device\n%s", out)
	}
	if got, want := runs(t, log), []string{"build --build-tests --package-path ios/AppCore" + buildFlags}; !reflect.DeepEqual(got, want) {
		t.Errorf("swift runs = %q, want %q (the iOS-only package must not be built)", got, want)
	}

	// Listed alone, it is still named, and the step says it built nothing
	// instead of claiming there was nothing to build.
	body = stepRun(t, parseIOSCI(t, nestedConfig(dir, "Device")), "lint", "Build Swift packages")
	bin, log = swiftBuildingTests(t)
	out, code = runStepIn(t, dir, body, bin)
	if code != 0 || !strings.Contains(out, "Not built: ios/Device") || strings.Contains(out, "No package components declared") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("swift ran for a project whose only listed package is iOS-only")
	}
}

// The app lints the listed package's files once and the render adds no lint
// directory for it: no double lint, and the push filter already covers it.
func TestListedPackageAddsNoLintDirectoryAndIsCoveredByAPushPath(t *testing.T) {
	dir := nestedRepo(t, nil)
	listed := nestedConfig(dir, "AppCore", "Libs/Net")
	plain := nestedConfig(dir)

	if a, b := tokens.CILintComponents(listed, "ios/"), tokens.CILintComponents(plain, "ios/"); a != b || a != "ios/." {
		t.Errorf("lint directories with the list = %q, without = %q, want both %q (a listed package is not a component)", a, b, "ios/.")
	}
	paths := pushPaths(t, listed)
	for _, pkg := range []string{"ios/AppCore/Package.swift", "ios/Libs/Net/Tests/NetTests/NTests.swift"} {
		if !pushMatches(paths, pkg) {
			t.Errorf("a push touching only %s would not run CI: push paths %q", pkg, paths)
		}
	}
}

// pushMatches reports whether any `dir/**` (or bare `**`) filter covers file.
func pushMatches(paths []string, file string) bool {
	for _, p := range paths {
		if p == "**" || (strings.HasSuffix(p, "/**") && strings.HasPrefix(file, strings.TrimSuffix(p, "**"))) {
			return true
		}
	}
	return false
}

// A project that lists nothing renders exactly as it did: of every value a
// file can be rendered with, listing packages changes the two package arrays
// and nothing else, so no other shipped file can differ.
func TestListingPackagesChangesOnlyThePackageArrays(t *testing.T) {
	dir := nestedRepo(t, nil)
	with := tokens.Values(nestedConfig(dir, "AppCore", "Device"), "ios/")
	without := tokens.Values(nestedConfig(dir), "ios/")

	var diff []string
	for k, v := range with {
		if without[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range without {
		if _, ok := with[k]; !ok {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	want := []string{tokens.IOSCIPackageDirs, tokens.IOSCIPackageSkips}
	if !reflect.DeepEqual(diff, want) {
		t.Fatalf("listing packages changed %v, want only %v", diff, want)
	}
	if with[tokens.IOSCIPackageDirs] != "'ios/AppCore'" || with[tokens.IOSCIPackageSkips] != "'ios/Device'" {
		t.Errorf("dirs = %q, skips = %q", with[tokens.IOSCIPackageDirs], with[tokens.IOSCIPackageSkips])
	}
	if without[tokens.IOSCIPackageDirs] != "" || without[tokens.IOSCIPackageSkips] != "" {
		t.Errorf("a project listing nothing renders dirs=%q skips=%q, want both empty", without[tokens.IOSCIPackageDirs], without[tokens.IOSCIPackageSkips])
	}
}

// Two manifests with the same root and components but different lists must not
// share a cached package set: the render is keyed on the list too.
func TestPackageRenderIsNotCachedAcrossDifferentLists(t *testing.T) {
	dir := nestedRepo(t, nil)
	a := tokens.CIPackageDirs(nestedConfig(dir, "AppCore"), false)
	b := tokens.CIPackageDirs(nestedConfig(dir, "Libs/Net"), false)
	if a != "'ios/AppCore'" || b != "'ios/Libs/Net'" {
		t.Fatalf("lists render %q then %q; a stale cache returns the first for both", a, b)
	}
}
