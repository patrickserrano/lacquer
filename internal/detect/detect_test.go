package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComponents(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Acme.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "dashboard", "package.json"))
	mk(t, filepath.Join(root, "cli", "Cargo.toml"))

	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range comps {
		got[c.Path] = c.Profiles[0]
	}
	if got["ios"] != "ios" || got["dashboard"] != "web" || got["cli"] != "rust" {
		t.Errorf("components = %+v", comps)
	}
	if derived.ProjectName != "Acme" || derived.Scheme != "Acme" {
		t.Errorf("derived = %+v", derived)
	}
}

func TestComponentsDetectsSupabase(t *testing.T) {
	root := t.TempDir()
	// supabase/config.toml under server/ marks server/ as a supabase component.
	mk(t, filepath.Join(root, "server", "supabase", "config.toml"))
	mk(t, filepath.Join(root, "server", "supabase", "functions", "hello", "index.ts"))
	mk(t, filepath.Join(root, "ios", "App.xcodeproj", "project.pbxproj"))

	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range comps {
		got[c.Path] = c.Profiles[0]
	}
	if got["server"] != "supabase" {
		t.Errorf("expected server -> supabase, got %+v", comps)
	}
	if got["ios"] != "ios" {
		t.Errorf("ios component should still be detected alongside supabase: %+v", comps)
	}
}

func TestComponentsIgnoresVendorDirs(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "node_modules", "dep", "package.json"))
	mk(t, filepath.Join(root, ".git", "x", "Cargo.toml"))
	comps, _, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 0 {
		t.Errorf("expected no components from vendor dirs, got %+v", comps)
	}
}

func TestComponentsIgnoresCocoaPodsCarthage(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "Pods", "Pods.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "Carthage", "Checkouts", "Dep", "Dep.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", "Acme.xcodeproj", "project.pbxproj"))

	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Errorf("expected only the ios component, got %+v", comps)
	}
	if derived.ProjectName != "Acme" {
		t.Errorf("derived name corrupted by Pods/Carthage: %+v", derived)
	}
}

func TestComponentsConfigDirAndXcodeproj(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Queueify", "Queueify.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))
	// Unrelated Swift config in a lexically-earlier dir must not win (walk-order
	// fragility): the ios component must still resolve to "ios".
	mk(t, filepath.Join(root, "aaa", ".swiftformat"))

	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Fatalf("component should be the config dir 'ios', got %+v", comps)
	}
	if derived.Xcodeproj != "ios/Queueify/Queueify.xcodeproj" {
		t.Errorf("xcodeproj = %q, want ios/Queueify/Queueify.xcodeproj", derived.Xcodeproj)
	}
	if derived.ProjectName != "Queueify" {
		t.Errorf("project_name = %q", derived.ProjectName)
	}
}

func TestComponentsXcodeprojParentFallback(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "App.xcodeproj", "project.pbxproj"))
	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Errorf("component = %+v, want ios", comps)
	}
	if derived.Xcodeproj != "ios/App.xcodeproj" {
		t.Errorf("xcodeproj = %q", derived.Xcodeproj)
	}
}

func TestComponentsDerivesSwiftVersion(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))
	if err := os.WriteFile(filepath.Join(root, "project.yml"), []byte("settings:\n  base:\n    SWIFT_VERSION: \"6.2\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if derived.SwiftVersion != "6.2" {
		t.Errorf("SwiftVersion = %q, want 6.2", derived.SwiftVersion)
	}
}
