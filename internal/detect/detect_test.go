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
