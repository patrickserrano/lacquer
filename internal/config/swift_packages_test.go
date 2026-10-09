package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// #522 U4b: a Swift component may list local SwiftPM packages nested inside it
// whose tests CI must compile. The list is explicit, like the component list
// itself, and every entry is held to rules that name the entry and, where one
// exists, the corrected form. Each rule has its own test, and each test makes
// the refused case the ONLY thing wrong with the manifest, so a rule cannot be
// satisfied by a different rule firing.

const manifestHead = `
[project]
name = "P"
project_name = "P"
scheme = "P"
bundle_id = "com.x.p"
asc_app_id = "1"
xcodeproj = "ios/P.xcodeproj"
swift_version = "6"
`

// pkgTree writes files (relative to a fresh project root) and returns the root.
func pkgTree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// loadAt loads a manifest written into dir, so the checks that read the
// filesystem have a tree to read.
func loadAt(t *testing.T, dir, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(dir, ".lacquer.toml")
	if err := os.WriteFile(p, []byte(manifestHead+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

const appWith = `
[[component]]
path = "ios"
profiles = ["ios"]
stack = "ios"
packages = %s
`

func appPackages(list string) string { return strings.Replace(appWith, "%s", list, 1) }

func TestPackagesOnAComponentLoadAndAreKept(t *testing.T) {
	dir := pkgTree(t, "ios/AppCore/Package.swift", "ios/Libs/Net/Package.swift")
	cfg, err := loadAt(t, dir, appPackages(`["AppCore", "Libs/Net"]`))
	if err != nil {
		t.Fatalf("a valid packages list was refused: %v", err)
	}
	if got, want := cfg.Components[0].Packages, []string{"AppCore", "Libs/Net"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Packages = %q, want %q", got, want)
	}
	// No packages key at all is the existing behaviour: nothing is listed.
	dir = pkgTree(t, "ios/AppCore/Package.swift")
	cfg, err = loadAt(t, dir, "\n[[component]]\npath = \"ios\"\nprofiles = [\"ios\"]\n")
	if err != nil || len(cfg.Components[0].Packages) != 0 {
		t.Fatalf("a component without packages = %v, err %v", cfg.Components[0].Packages, err)
	}
}

// refusal runs one manifest and requires an error that contains every want.
func refusal(t *testing.T, dir, body string, want ...string) {
	t.Helper()
	_, err := loadAt(t, dir, body)
	if err == nil {
		t.Fatal("the manifest loaded; the packages entry should have been refused")
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error does not contain %q:\n%v", w, err)
		}
	}
}

func TestPackagesEntryEscapingTheComponentIsRefused(t *testing.T) {
	// A real package, outside the component, so only the escape is wrong.
	dir := pkgTree(t, "ios/Core/Package.swift", "Elsewhere/Package.swift")
	for _, entry := range []string{`"../Elsewhere"`, `"Core/../../Elsewhere"`, `".."`} {
		refusal(t, dir, appPackages(`[`+entry+`]`), "escapes the component", `component "ios"`)
	}
}

func TestPackagesEntryThatIsNotAPackageIsRefused(t *testing.T) {
	// A directory with Swift in it and no Package.swift.
	dir := pkgTree(t, "ios/Plain/Sources/P.swift")
	refusal(t, dir, appPackages(`["Plain"]`), `"Plain"`, "no Package.swift")
	// A path that does not exist at all is refused the same way, not skipped.
	refusal(t, dir, appPackages(`["Missing"]`), `"Missing"`, "does not exist")
	// A FILE named Package.swift's parent that is itself a file.
	dir = pkgTree(t, "ios/NotADir")
	refusal(t, dir, appPackages(`["NotADir"]`), `"NotADir"`, "is not a directory; list the directory that holds a Package.swift")
}

func TestPackagesEntryThatIsAlsoAComponentIsRefused(t *testing.T) {
	dir := pkgTree(t, "ios/Lib/Package.swift")
	body := appPackages(`["Lib"]`) + `
[[component]]
path = "ios/Lib"
stack = "web"
`
	refusal(t, dir, body, `"Lib"`, "ios/Lib", "declared component")
	// Spelled through a package component, which also owns the directory.
	dir = pkgTree(t, "tools/Lib/Package.swift")
	body = `
[[component]]
path = "tools"
stack = "ios"
packages = ["Lib"]

[[component]]
path = "tools/Lib"
stack = "web"
`
	refusal(t, dir, body, `"Lib"`, "tools/Lib", "declared component")
}

func TestPackagesEntryListedTwiceIsRefused(t *testing.T) {
	dir := pkgTree(t, "ios/Core/Package.swift")
	refusal(t, dir, appPackages(`["Core", "Core"]`), `"Core"`, "twice")
}

func TestPackagesEntryNotInCanonicalFormIsRefusedWithTheCorrectedForm(t *testing.T) {
	dir := pkgTree(t, "ios/Core/Package.swift", "ios/Libs/Net/Package.swift")
	refusal(t, dir, appPackages(`["Core/"]`), `"Core/"`, `write "Core"`)
	refusal(t, dir, appPackages(`["./Core"]`), `"./Core"`, `write "Core"`)
	refusal(t, dir, appPackages(`["Libs//Net"]`), `write "Libs/Net"`)
	// It spells a duplicate of an entry already listed, which is how two
	// spellings of one directory would otherwise both load.
	refusal(t, dir, appPackages(`["Core", "./Core"]`), `write "Core"`)
}

func TestPackagesEntryNamingTheComponentItselfIsRefused(t *testing.T) {
	dir := pkgTree(t, "ios/Package.swift")
	refusal(t, dir, appPackages(`["."]`), `"."`, "the component itself")
}

func TestPackagesEntryAbsoluteOrEmptyIsRefused(t *testing.T) {
	dir := pkgTree(t, "ios/Core/Package.swift")
	refusal(t, dir, appPackages(`["`+filepath.Join(dir, "ios", "Core")+`"]`), "relative to the component")
	refusal(t, dir, appPackages(`[""]`), "empty")
}

func TestPackagesEntryWithShellMetacharactersIsRefused(t *testing.T) {
	// The directory EXISTS and holds a Package.swift, so only the characters are
	// wrong. Entries reach rendered shell as a quoted array element, and a
	// quote in the name would end it.
	for _, name := range []string{"it's", "a b", "a;b", "$(x)", "a`x`", "a|b", "a&b", "-rf"} {
		dir := pkgTree(t, "ios/"+name+"/Package.swift")
		refusal(t, dir, appPackages(`["`+name+`"]`), "unsafe characters")
	}
}

func TestPackagesEntryThroughASymlinkOutOfTheComponentIsRefused(t *testing.T) {
	dir := pkgTree(t, "ios/Real/Package.swift", "Elsewhere/Package.swift")
	if err := os.Symlink(filepath.Join(dir, "Elsewhere"), filepath.Join(dir, "ios", "Link")); err != nil {
		t.Fatal(err)
	}
	refusal(t, dir, appPackages(`["Link"]`), `"Link"`, "symlink", "outside the component")
	// A directory link out whose Package.swift is itself a link back IN: the
	// manifest resolves inside the component, so only the directory is wrong.
	dir2 := pkgTree(t, "ios/Real/Package.swift", "Elsewhere/.keep")
	if err := os.Symlink(filepath.Join(dir2, "ios", "Real", "Package.swift"), filepath.Join(dir2, "Elsewhere", "Package.swift")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir2, "Elsewhere"), filepath.Join(dir2, "ios", "Link")); err != nil {
		t.Fatal(err)
	}
	refusal(t, dir2, appPackages(`["Link"]`), `"Link"`, "symlink that leads outside the component")
	// A symlink that stays inside the component is resolved and accepted.
	if err := os.Symlink(filepath.Join(dir, "ios", "Real"), filepath.Join(dir, "ios", "Alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAt(t, dir, appPackages(`["Alias"]`)); err != nil {
		t.Fatalf("a symlink that resolves inside the component was refused: %v", err)
	}
	// So is a Package.swift that is itself a link out.
	dir = pkgTree(t, "Outside/Package.swift", "ios/Core/.keep")
	if err := os.Symlink(filepath.Join(dir, "Outside", "Package.swift"), filepath.Join(dir, "ios", "Core", "Package.swift")); err != nil {
		t.Fatal(err)
	}
	refusal(t, dir, appPackages(`["Core"]`), `"Core"`, "symlink")
}

func TestPackagesOnAComponentThatIsNotSwiftIsRefused(t *testing.T) {
	dir := pkgTree(t, "admin/Lib/Package.swift")
	body := `
[[component]]
path = "admin"
stack = "web"
packages = ["Lib"]
`
	refusal(t, dir, body, `"admin"`, "Swift component")
}

// A listed package under a package component the Lint job already discovers
// (depth 0 or 1) is not an error: it loads, and is built once (see
// swiftcomponents). Only the manifest is under test here.
func TestPackagesUnderAPackageComponentLoad(t *testing.T) {
	dir := pkgTree(t, "tools/alpha/Package.swift", "tools/deep/er/Package.swift")
	body := `
[[component]]
path = "tools"
stack = "ios"
packages = ["alpha", "deep/er"]
`
	if _, err := loadAt(t, dir, body); err != nil {
		t.Fatalf("packages on a package component were refused: %v", err)
	}
}
