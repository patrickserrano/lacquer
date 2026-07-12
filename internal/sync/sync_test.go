package sync

import (
	"os"
	"os/exec"
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
		"[project]\nname=\"acme\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "# acme\n\nlocal note\n")

	if _, err := Run(harness, project, false); err != nil {
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

func TestSyncMirrorsAgentsMd(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()

	writeFile(t, filepath.Join(harness, "VERSION"), "3\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE RULES")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS RULES")
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"acme\"\ntools=[\"claude\",\"codex\",\"antigravity\"]\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	// Pre-existing project-owned text in AGENTS.md must be preserved (managed
	// region merge, not whole-file overwrite).
	writeFile(t, filepath.Join(project, "AGENTS.md"), "# acme agents\n\nkeep me\n")

	if _, err := Run(harness, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rootAgents, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatalf("root AGENTS.md not written: %v", err)
	}
	s := string(rootAgents)
	if !strings.Contains(s, "keep me") {
		t.Error("root AGENTS.md lost project-owned text")
	}
	if !strings.Contains(s, "<!-- harness:core:start v3 -->") || !strings.Contains(s, "CORE RULES") {
		t.Errorf("root AGENTS.md missing core region:\n%s", s)
	}

	compAgents, err := os.ReadFile(filepath.Join(project, "ios", "AGENTS.md"))
	if err != nil {
		t.Fatalf("component AGENTS.md not written: %v", err)
	}
	if !strings.Contains(string(compAgents), "<!-- harness:ios:start v3 -->") ||
		!strings.Contains(string(compAgents), "IOS RULES") {
		t.Errorf("component AGENTS.md missing ios region:\n%s", compAgents)
	}

	// AGENTS.md and CLAUDE.md must carry identical managed-region bodies.
	rootClaude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if !strings.Contains(string(rootClaude), "CORE RULES") {
		t.Error("root CLAUDE.md missing core region (mirror must not replace it)")
	}
}

func TestSyncClaudeOnlyWritesNoAgentsMd(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	// No tools field -> defaults to claude-only -> no AGENTS.md.
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")

	if _, err := Run(harness, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "AGENTS.md")); err == nil {
		t.Error("claude-only project must not get an AGENTS.md")
	}
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); err != nil {
		t.Errorf("claude-only project must still get CLAUDE.md: %v", err)
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

	if _, err := Run(harness, project, false); err == nil {
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

	if _, err := Run(harness, project, false); err == nil {
		t.Fatal("expected error: component dir is a symlink escaping the project root")
	}
	// Nothing should have been written into the escape target.
	if _, err := os.Stat(filepath.Join(outside, "CLAUDE.md")); err == nil {
		t.Error("file was written outside the project root via symlinked component dir")
	}
}

func TestSyncCopiesAssets(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()

	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "core", "skills", "git.md"), "GIT SKILL")

	// init project as a git repo (gitguard needs one)
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")

	if _, err := Run(harness, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "git.md"))
	if err != nil {
		t.Fatalf("skill asset not synced: %v", err)
	}
	if string(got) != "GIT SKILL" {
		t.Errorf("content = %q", got)
	}
}

func TestRunReportsCounts(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "core", "skills", "git.md"), "S")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")

	res, err := Run(harness, project, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Regions < 1 || res.Assets != 1 {
		t.Errorf("Result = %+v, want Regions>=1 Assets=1", res)
	}
}

func TestRunRefusesAssetsInNonGitProject(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir() // NOT git init'd
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "core", "skills", "git.md"), "S")
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")
	if _, err := Run(harness, project, false); err == nil {
		t.Fatal("expected Run to refuse asset sync in a non-git project, got nil")
	}
}

func TestSyncSubstitutesTokens(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "root", ".x.yml"), "scheme: {{SCHEME}}\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\nscheme=\"Acme\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(harness, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, ".x.yml"))
	if string(got) != "scheme: Acme\n" {
		t.Errorf("token not substituted: %q", got)
	}
}

func TestSyncFailsClosedOnMissingToken(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "root", ".x.yml"), "scheme: {{SCHEME}}\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")

	_, err := Run(harness, project, false)
	if err == nil {
		t.Fatal("expected fail-closed error for missing {{SCHEME}} value")
	}
	if !strings.Contains(err.Error(), "SCHEME") {
		t.Errorf("error should name the missing token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".x.yml")); err == nil {
		t.Error(".x.yml was written despite missing token (not fail-closed)")
	}
}

func TestSyncRootLayoutEmptyPrefix(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "workflows", "ci.yml"), "lint: {{COMPONENT_PREFIX}}.swiftlint.yml\nf: '{{COMPONENT_PREFIX}}**'\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\nproject_name=\"Acme\"\nscheme=\"Acme\"\nbundle_id=\"com.me.acme\"\nasc_app_id=\"9\"\n\n[[component]]\npath=\".\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(harness, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, ".github", "workflows", "ios-ci.yml"))
	if string(got) != "lint: .swiftlint.yml\nf: '**'\n" {
		t.Errorf("root-layout prefix not applied:\n%q", got)
	}
}
