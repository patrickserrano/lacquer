package safepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAllowsPlainRelPath(t *testing.T) {
	root := t.TempDir()
	// "ios/CLAUDE.md" need not exist yet.
	got, err := Resolve(root, filepath.Join("ios", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	want := filepath.Join(realRoot, "ios", "CLAUDE.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveRejectsSymlinkedLeafDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// root/vendor -> outside
	if err := os.Symlink(outside, filepath.Join(root, "vendor")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, filepath.Join("vendor", "CLAUDE.md")); err == nil {
		t.Fatal("expected error for symlinked component dir escaping root, got nil")
	}
}

func TestResolveRejectsSymlinkedIntermediateDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// root/a -> outside ; path a/b/CLAUDE.md
	if err := os.Symlink(outside, filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, filepath.Join("a", "b", "CLAUDE.md")); err == nil {
		t.Fatal("expected error for symlinked intermediate dir escaping root, got nil")
	}
}

func TestResolveAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	// root/link -> root/real (stays inside root)
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, filepath.Join("link", "CLAUDE.md")); err != nil {
		t.Errorf("internal symlink should be allowed: %v", err)
	}
}
