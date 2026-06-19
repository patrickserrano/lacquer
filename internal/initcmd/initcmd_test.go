package initcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/harness/internal/config"
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

func TestInitWritesManifest(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Rail.xcodeproj", "project.pbxproj"))

	if _, err := Run(root); err != nil {
		t.Fatalf("Run: %v", err)
	}
	manifest := filepath.Join(root, ".harness.toml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		"[project]", `project_name = "Rail"`, `scheme = "Rail"`,
		"[[component]]", `path = "ios"`, `profiles = ["ios"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
	// The generated manifest must itself be loadable.
	if _, err := config.Load(manifest); err != nil {
		t.Errorf("generated manifest does not load: %v", err)
	}
}

func TestInitRefusesExistingManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".harness.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root); err == nil {
		t.Fatal("expected init to refuse clobbering an existing .harness.toml")
	}
}

func TestInitScaffoldsBriefStub(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "Skein.xcodeproj", "project.pbxproj"))
	if _, err := Run(root); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "brief.md"))
	if err != nil {
		t.Fatalf("brief stub not written: %v", err)
	}
	if !strings.Contains(string(data), "# Skein — Product Brief") {
		t.Errorf("brief stub missing project name heading:\n%s", data)
	}
}

func TestInitPreservesExistingBrief(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "Skein.xcodeproj", "project.pbxproj"))
	brief := filepath.Join(root, "docs", "brief.md")
	if err := os.MkdirAll(filepath.Dir(brief), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("MY REAL BRIEF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(brief)
	if string(got) != "MY REAL BRIEF" {
		t.Errorf("init overwrote an existing brief: %q", got)
	}
}

func TestInitWritesXcodeproj(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Queueify", "Queueify.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))
	if _, err := Run(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".harness.toml"))
	s := string(data)
	for _, want := range []string{`xcodeproj = "ios/Queueify/Queueify.xcodeproj"`, `path = "ios"`} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
	if _, err := config.Load(filepath.Join(root, ".harness.toml")); err != nil {
		t.Errorf("generated manifest does not load: %v", err)
	}
}
