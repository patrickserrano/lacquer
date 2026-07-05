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

// harnessWith builds a temp harness checkout that ships exactly the given
// profiles (each as profiles/<p>/CLAUDE.<p>.md), so init's profile-ship gate has
// something to check against.
func harnessWith(t *testing.T, profiles ...string) string {
	t.Helper()
	hr := t.TempDir()
	for _, p := range profiles {
		mk(t, filepath.Join(hr, "profiles", p, "CLAUDE."+p+".md"))
	}
	return hr
}

func TestInitWritesManifest(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Rail.xcodeproj", "project.pbxproj"))

	if _, err := Run(harnessWith(t, "ios"), root); err != nil {
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
	if _, err := Run(harnessWith(t), root); err == nil {
		t.Fatal("expected init to refuse clobbering an existing .harness.toml")
	}
}

func TestInitScaffoldsBriefStub(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "Skein.xcodeproj", "project.pbxproj"))
	if _, err := Run(harnessWith(t, "ios"), root); err != nil {
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
	if _, err := Run(harnessWith(t, "ios"), root); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(brief)
	if string(got) != "MY REAL BRIEF" {
		t.Errorf("init overwrote an existing brief: %q", got)
	}
}

func TestInitRefusesDanglingManifestSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// A dangling symlink: os.Stat would report "not exist" and os.WriteFile
	// would follow it, CREATING the target outside the project root.
	target := filepath.Join(outside, "planted.toml")
	if err := os.Symlink(target, filepath.Join(root, ".harness.toml")); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root); err == nil {
		t.Fatal("expected init to refuse a symlinked .harness.toml")
	}
	if _, err := os.Lstat(target); err == nil {
		t.Error("init created a file outside the project root via dangling symlink")
	}
}

func TestInitRefusesManifestSymlinkToExistingFile(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	target := filepath.Join(outside, "existing.toml")
	if err := os.WriteFile(target, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".harness.toml")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root)
	if err == nil {
		t.Fatal("expected init to refuse a symlinked .harness.toml")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("want a symlink refusal, got: %v", err)
	}
	// The symlink target must be untouched.
	got, _ := os.ReadFile(target)
	if string(got) != "ORIGINAL" {
		t.Errorf("symlink target was modified: %q", got)
	}
}

func TestInitRefusesSymlinkedDocsDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mk(t, filepath.Join(root, "Skein.xcodeproj", "project.pbxproj"))

	// docs is a symlink pointing outside the project root — the brief stub
	// must not land in the escape target.
	if err := os.Symlink(outside, filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root); err == nil {
		t.Fatal("expected init to refuse a symlinked docs dir")
	}
	if _, err := os.Lstat(filepath.Join(outside, "brief.md")); err == nil {
		t.Error("brief.md was written outside the project root via symlinked docs dir")
	}
}

func TestInitWritesXcodeproj(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Queueify", "Queueify.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))
	if _, err := Run(harnessWith(t, "ios"), root); err != nil {
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

// A go.mod-only project detects a "go" component, but no go profile ships — the
// component must still be recorded (empty profiles) with a notice, and the
// manifest must stay loadable (so the next `harness sync` doesn't hard-fail on an
// opaque missing-profile-file error).
func TestInitDropsNonShippingProfile(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "tool", "go.mod"))

	summary, err := Run(harnessWith(t, "ios"), root) // ships ios, NOT go
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(summary, `component "tool" detected as "go"`) ||
		!strings.Contains(summary, "profiles/go/") {
		t.Errorf("summary missing the non-shipping-profile notice:\n%s", summary)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".harness.toml"))
	s := string(data)
	for _, want := range []string{`path = "tool"`, "profiles = []"} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
	// The whole point: the emitted manifest must load cleanly.
	if _, err := config.Load(filepath.Join(root, ".harness.toml")); err != nil {
		t.Errorf("generated manifest does not load: %v", err)
	}
}

// Regression guard: an .xcodeproj still yields the ios profile when it ships.
func TestInitKeepsShippingProfile(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "App.xcodeproj", "project.pbxproj"))
	if _, err := Run(harnessWith(t, "ios"), root); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".harness.toml"))
	if !strings.Contains(string(data), `profiles = ["ios"]`) {
		t.Errorf("ios profile was dropped despite shipping:\n%s", data)
	}
}
