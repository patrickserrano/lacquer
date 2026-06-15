package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile is a test helper that creates parent dirs and writes content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncMergesCoreAndProfile(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()

	// Harness fixtures.
	writeFile(t, filepath.Join(harness, "VERSION"), "2\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE RULES")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS RULES")

	// Project fixtures.
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"rail\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "# rail\n\nlocal note\n")

	if err := Run(harness, project); err != nil {
		t.Fatalf("Run: %v", err)
	}

	root, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if !strings.Contains(string(root), "local note") {
		t.Error("root CLAUDE.md lost project-owned text")
	}
	if !strings.Contains(string(root), "<!-- harness:core:start v2 -->") ||
		!strings.Contains(string(root), "CORE RULES") {
		t.Errorf("root CLAUDE.md missing core region:\n%s", root)
	}

	comp, err := os.ReadFile(filepath.Join(project, "ios", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("component CLAUDE.md not written: %v", err)
	}
	if !strings.Contains(string(comp), "<!-- harness:ios:start v2 -->") ||
		!strings.Contains(string(comp), "IOS RULES") {
		t.Errorf("component CLAUDE.md missing ios region:\n%s", comp)
	}
}

func TestSyncRefusesToWriteThroughSymlink(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()

	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\n")

	// Point the project's root CLAUDE.md at a file outside the project.
	secret := filepath.Join(outside, "secret.md")
	writeFile(t, secret, "ORIGINAL SECRET\n")
	if err := os.Symlink(secret, filepath.Join(project, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	if err := Run(harness, project); err == nil {
		t.Fatal("expected error syncing through a symlink, got nil")
	}
	// The symlink target must be untouched.
	got, _ := os.ReadFile(secret)
	if string(got) != "ORIGINAL SECRET\n" {
		t.Errorf("symlink target was modified: %q", got)
	}
}

func TestSyncRefusesSymlinkedComponentDir(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()

	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS")

	// Component dir is a symlink pointing outside the project root.
	if err := os.Symlink(outside, filepath.Join(project, "vendor")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"vendor\"\nprofiles=[\"ios\"]\n")

	if err := Run(harness, project); err == nil {
		t.Fatal("expected error: component dir is a symlink escaping the project root")
	}
	// Nothing should have been written into the escape target.
	if _, err := os.Stat(filepath.Join(outside, "CLAUDE.md")); err == nil {
		t.Error("file was written outside the project root via symlinked component dir")
	}
}
