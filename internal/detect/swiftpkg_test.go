package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// needledrop/Sleevetap's shape: Swift that lives in a SwiftPM package with no
// Xcode project anywhere. Detection had no marker for it, so a repo whose
// manifest was written during a TypeScript-only spike kept declaring
// `profiles = ["web"]` after the Swift arrived — 191 tests run by nothing.
func TestComponentsDetectsBarePackageSwift(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "SleevetapNFC", "Package.swift"))
	mk(t, filepath.Join(root, "spike", "package.json"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, c := range comps {
		got[c.Path] = c.Profiles
	}
	if len(got["ios/SleevetapNFC"]) != 1 || got["ios/SleevetapNFC"][0] != SwiftProfile {
		t.Errorf("expected ios/SleevetapNFC -> %s, got %+v", SwiftProfile, comps)
	}
	if len(got["spike"]) != 1 || got["spike"][0] != "web" {
		t.Errorf("web component lost: %+v", comps)
	}
}

// The measured false-positive guard. 6 of the 7 Swift repos in this fleet carry
// a Package.swift *inside* an app repo — a local module of the app, not a
// component of its own. Treating those as components would have produced a
// second Swift component in every one of them.
func TestComponentsIgnoresPackageSwiftBesideAnXcodeproj(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "Flare.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "Packages", "FlareKit", "Package.swift"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "." {
		t.Fatalf("expected only the root ios component, got %+v", comps)
	}
	for _, p := range comps[0].Profiles {
		if p == SwiftProfile {
			t.Errorf("a local package inside an app repo must not become a %q component: %+v", SwiftProfile, comps)
		}
	}
}

// Several top-level packages collapse to their common ancestor: a manifest may
// declare a profile only once, so emitting one component each would write a
// manifest that fails to load.
func TestComponentsCollapsesSeveralSwiftPackages(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "packages", "Core", "Package.swift"))
	mk(t, filepath.Join(root, "packages", "UI", "Package.swift"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "packages" {
		t.Fatalf("expected one component at 'packages', got %+v", comps)
	}
}

// A package vendored inside another package is part of that package, not a
// component of its own.
func TestComponentsCollapsesNestedSwiftPackages(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "Kit", "Package.swift"))
	mk(t, filepath.Join(root, "Kit", "Vendor", "Dep", "Package.swift"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "Kit" {
		t.Fatalf("expected one component at 'Kit', got %+v", comps)
	}
}

// The Swift config dir wins over the package dir, mirroring the xcodeproj rule:
// configs at ios/ governing packages beneath it means the component is ios/.
func TestComponentsPrefersSwiftConfigDirOverPackageDir(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))
	mk(t, filepath.Join(root, "ios", "Feature", "Package.swift"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Fatalf("expected the config dir 'ios' to win, got %+v", comps)
	}
}

// SwiftPM's build directory holds checked-out dependency packages. skipdirs
// already excludes it; this pins that it stays excluded.
func TestComponentsIgnoresPackageSwiftInBuildDir(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, ".build", "checkouts", "swift-log", "Package.swift"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 0 {
		t.Errorf("expected no components from .build/, got %+v", comps)
	}
}

func TestCommonDir(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"ios/A", "ios/B", "ios"},
		{"ios/A", "ios/A", "ios/A"},
		{"ios", "web", "."},
		{".", "ios", "."},
		{"a/b/c", "a/b", "a/b"},
	}
	for _, c := range cases {
		if got := commonDir(c.a, c.b); got != c.want {
			t.Errorf("commonDir(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// mkFile writes content (mk writes a placeholder byte).
func mkFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
