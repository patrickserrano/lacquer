package swiftcomponents

import (
	"reflect"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// #522 U4b: a Swift component can list nested packages whose tests CI must
// compile. Listed packages join the ones the Lint job builds; nothing else does.

const (
	macPkg = "let package = Package(name: \"X\", platforms: [.iOS(.v26), .macOS(.v26)])\n"
	iosPkg = "let package = Package(name: \"X\", platforms: [.iOS(.v26)])\n"
)

func listing(c config.Component, pkgs ...string) config.Component {
	c.Packages = pkgs
	return c
}

func dirsOf(pkgs []Package) []string {
	var out []string
	for _, p := range pkgs {
		out = append(out, p.Dir)
	}
	return out
}

// An unlisted nested package is NOT built: the same tree, once with the list
// and once without it, differs by exactly the listed package.
func TestOnlyListedNestedPackagesAreBuilt(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/App/App.swift":                   "",
		"ios/AppCore/Package.swift":           macPkg,
		"ios/AppCore/Sources/AppCore/A.swift": "",
		"ios/Libs/Net/Package.swift":          macPkg, // nested, not listed
	})
	got, err := Packages(dir, manifest(listing(appIOS, "AppCore")))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ios/AppCore"}; !reflect.DeepEqual(dirsOf(got), want) {
		t.Fatalf("Packages = %v, want %v (ios/Libs/Net is nested but unlisted)", dirsOf(got), want)
	}
	got, _ = Packages(dir, manifest(appIOS))
	if len(got) != 0 {
		t.Fatalf("with no packages key the app component built %v, want nothing (unchanged behaviour)", dirsOf(got))
	}
}

// A listed package is read for its platforms exactly as a discovered one is:
// an iOS-only one is returned marked, so the step names it and does not build it.
func TestListedIOSOnlyPackageIsMarkedNotDropped(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/Mac/Package.swift":  macPkg,
		"ios/Only/Package.swift": iosPkg,
	})
	got, err := Packages(dir, manifest(listing(appIOS, "Mac", "Only")))
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{{Dir: "ios/Mac", Listed: true}, {Dir: "ios/Only", IOSOnly: true, Listed: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Packages = %+v, want %+v", got, want)
	}
}

// A package under a package component that the Lint job already discovers
// (depth 0 or 1), and is also listed, is built once.
func TestPackageBothDiscoveredAndListedIsBuiltOnce(t *testing.T) {
	dir := repo(t, map[string]string{
		"tools/alpha/Package.swift":   macPkg,
		"tools/deep/er/Package.swift": macPkg,
	})
	got, err := Packages(dir, manifest(appIOS, listing(toolsPkg, "alpha", "deep/er")))
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{{Dir: "tools/alpha", Listed: true}, {Dir: "tools/deep/er", Listed: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Packages = %+v, want %+v (alpha once; deep/er is depth 2, so only the list finds it)", got, want)
	}
	// Unlisted, the depth-2 package is not built; the depth-1 one still is.
	got, _ = Packages(dir, manifest(appIOS, toolsPkg))
	if want := []string{"tools/alpha"}; !reflect.DeepEqual(dirsOf(got), want) {
		t.Fatalf("unlisted Packages = %v, want %v", dirsOf(got), want)
	}
}

// A root-layout app lists packages relative to the repository root.
func TestListedPackageOnARootLayoutApp(t *testing.T) {
	dir := repo(t, map[string]string{"AppCore/Package.swift": macPkg})
	got, err := Packages(dir, manifest(listing(appRoot, "AppCore")))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"AppCore"}; !reflect.DeepEqual(dirsOf(got), want) {
		t.Fatalf("Packages = %v, want %v", dirsOf(got), want)
	}
}

// A listed package whose manifest cannot be read is an error, never an empty
// answer: "could not look" must not read as "nothing to build".
func TestListedPackageWithoutAManifestIsAnError(t *testing.T) {
	dir := repo(t, map[string]string{"ios/App/App.swift": ""})
	if _, err := Packages(dir, manifest(listing(appIOS, "Gone"))); err == nil {
		t.Fatal("a listed package with no Package.swift produced no error")
	}
}

// No double lint and no gap. The nested package's files belong to the declaring
// component and to nothing else, and no file under it is stray.
func TestListedPackageFilesAreLintedOnceByTheirComponent(t *testing.T) {
	files := []string{
		"ios/App/App.swift",
		"ios/AppCore/Sources/AppCore/A.swift",
		"ios/AppCore/Tests/AppCoreTests/ATests.swift",
	}
	comps := Components(manifest(listing(appIOS, "AppCore"), toolsPkg))
	groups, unmatched := Group(files, comps)
	if len(unmatched) != 0 {
		t.Fatalf("files %v under no component: a gap", unmatched)
	}
	if len(groups) != 1 || groups[0].Component.Path != "ios" || !reflect.DeepEqual(groups[0].Files, files) {
		t.Fatalf("groups = %+v, want every file in the one group for ios (a second group is a double lint)", groups)
	}
	// And the listed package adds no component of its own.
	if want := []Component{{Path: "ios", Profile: true}, {Path: "tools"}}; !reflect.DeepEqual(comps, want) {
		t.Fatalf("Components = %+v, want %+v", comps, want)
	}
	dir := repo(t, map[string]string{
		"ios/App/App.swift":                   "",
		"ios/AppCore/Package.swift":           macPkg,
		"ios/AppCore/Sources/AppCore/A.swift": "",
		"tools/Package.swift":                 macPkg,
	})
	stray, err := Stray(dir, manifest(listing(appIOS, "AppCore"), toolsPkg))
	if err != nil || len(stray) != 0 {
		t.Fatalf("Stray = %v, err %v; the listed package's files are the app's", stray, err)
	}
}
