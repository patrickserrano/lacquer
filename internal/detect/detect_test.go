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
	mk(t, filepath.Join(root, "ios", "Rail.xcodeproj", "project.pbxproj"))
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
	if derived.ProjectName != "Rail" || derived.Scheme != "Rail" {
		t.Errorf("derived = %+v", derived)
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
	mk(t, filepath.Join(root, "ios", "Rail.xcodeproj", "project.pbxproj"))

	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Errorf("expected only the ios component, got %+v", comps)
	}
	if derived.ProjectName != "Rail" {
		t.Errorf("derived name corrupted by Pods/Carthage: %+v", derived)
	}
}

func TestComponentsConfigDirAndXcodeproj(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Queueify", "Queueify.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))

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
