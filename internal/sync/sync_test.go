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
	lacquer := t.TempDir()
	project := t.TempDir()

	// Lacquer fixtures.
	writeFile(t, filepath.Join(lacquer, "VERSION"), "2\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE RULES")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS RULES")

	// Project fixtures.
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"acme\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "# acme\n\nlocal note\n")

	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}

	root, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if !strings.Contains(string(root), "local note") {
		t.Error("root CLAUDE.md lost project-owned text")
	}
	if !strings.Contains(string(root), "<!-- lacquer:core:start v0.2.0 -->") ||
		!strings.Contains(string(root), "CORE RULES") {
		t.Errorf("root CLAUDE.md missing core region:\n%s", root)
	}

	comp, err := os.ReadFile(filepath.Join(project, "ios", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("component CLAUDE.md not written: %v", err)
	}
	if !strings.Contains(string(comp), "<!-- lacquer:ios:start v0.2.0 -->") ||
		!strings.Contains(string(comp), "IOS RULES") {
		t.Errorf("component CLAUDE.md missing ios region:\n%s", comp)
	}
}

func TestSyncMirrorsAgentsMd(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()

	writeFile(t, filepath.Join(lacquer, "VERSION"), "3\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE RULES")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS RULES")
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"acme\"\ntools=[\"claude\",\"codex\",\"antigravity\"]\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	// Pre-existing project-owned text in AGENTS.md must be preserved (managed
	// region merge, not whole-file overwrite).
	writeFile(t, filepath.Join(project, "AGENTS.md"), "# acme agents\n\nkeep me\n")

	if _, err := Run(lacquer, project, false); err != nil {
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
	if !strings.Contains(s, "<!-- lacquer:core:start v0.3.0 -->") || !strings.Contains(s, "CORE RULES") {
		t.Errorf("root AGENTS.md missing core region:\n%s", s)
	}

	compAgents, err := os.ReadFile(filepath.Join(project, "ios", "AGENTS.md"))
	if err != nil {
		t.Fatalf("component AGENTS.md not written: %v", err)
	}
	if !strings.Contains(string(compAgents), "<!-- lacquer:ios:start v0.3.0 -->") ||
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
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	// No tools field -> defaults to claude-only -> no AGENTS.md.
	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"x\"\n")

	if _, err := Run(lacquer, project, false); err != nil {
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
	lacquer := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()

	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n")

	// Point the project's root CLAUDE.md at a file outside the project.
	secret := filepath.Join(outside, "secret.md")
	writeFile(t, secret, "ORIGINAL SECRET\n")
	if err := os.Symlink(secret, filepath.Join(project, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(lacquer, project, false); err == nil {
		t.Fatal("expected error syncing through a symlink, got nil")
	}
	// The symlink target must be untouched.
	got, _ := os.ReadFile(secret)
	if string(got) != "ORIGINAL SECRET\n" {
		t.Errorf("symlink target was modified: %q", got)
	}
}

func TestSyncRefusesSymlinkedComponentDir(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()

	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS")

	// Component dir is a symlink pointing outside the project root.
	if err := os.Symlink(outside, filepath.Join(project, "vendor")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"vendor\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(lacquer, project, false); err == nil {
		t.Fatal("expected error: component dir is a symlink escaping the project root")
	}
	// Nothing should have been written into the escape target.
	if _, err := os.Stat(filepath.Join(outside, "CLAUDE.md")); err == nil {
		t.Error("file was written outside the project root via symlinked component dir")
	}
}

func TestSyncCopiesAssets(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()

	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "core", "skills", "git.md"), "GIT SKILL")

	// init project as a git repo (gitguard needs one)
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"x\"\n")

	if _, err := Run(lacquer, project, false); err != nil {
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
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "core", "skills", "git.md"), "S")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"x\"\n")

	res, err := Run(lacquer, project, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Regions < 1 || res.Assets != 1 {
		t.Errorf("Result = %+v, want Regions>=1 Assets=1", res)
	}
}

func TestRunRefusesAssetsInNonGitProject(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir() // NOT git init'd
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "core", "skills", "git.md"), "S")
	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"x\"\n")
	if _, err := Run(lacquer, project, false); err == nil {
		t.Fatal("expected Run to refuse asset sync in a non-git project, got nil")
	}
}

func TestSyncSubstitutesTokens(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "root", ".x.yml"), "scheme: {{SCHEME}}\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\nscheme=\"Acme\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, ".x.yml"))
	if string(got) != "scheme: Acme\n" {
		t.Errorf("token not substituted: %q", got)
	}
}

func TestSyncFailsClosedOnMissingToken(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "root", ".x.yml"), "scheme: {{SCHEME}}\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")

	_, err := Run(lacquer, project, false)
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
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "workflows", "ci.yml"), "lint: {{COMPONENT_PREFIX}}.swiftlint.yml\nf: '{{COMPONENT_PREFIX}}**'\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\nproject_name=\"Acme\"\nscheme=\"Acme\"\nbundle_id=\"com.me.acme\"\nasc_app_id=\"9\"\n\n[[component]]\npath=\".\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, ".github", "workflows", "ios-ci.yml"))
	if string(got) != "lint: .swiftlint.yml\nf: '**'\n" {
		t.Errorf("root-layout prefix not applied:\n%q", got)
	}
}

// sync writes managed regions and THEN copies assets, and the asset phase can
// refuse (an uncommitted managed file, a symlink, a confinement violation).
// That refusal used to happen after the regions were already on disk, leaving a
// half-synced project from a run that reported failure — Queueify landed in
// exactly that state, with rewritten CLAUDE.md and AGENTS.md from a sync that
// errored. Everything that can refuse must refuse before anything is written.
func TestSyncWritesNoRegionWhenTheAssetPhaseRefuses(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()

	writeFile(t, filepath.Join(lacquer, "VERSION"), "2\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE RULES")
	// One core asset, so the plan is non-empty and the asset phase runs.
	writeFile(t, filepath.Join(lacquer, "core", "root", "scripts", "thing.sh"), "#!/bin/sh\necho v1\n")

	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"acme\"\n")
	writeFile(t, filepath.Join(project, "CLAUDE.md"), "# acme\n\nlocal note\n")

	for _, args := range [][]string{
		{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if err := cmd.Run(); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}

	// Make the managed asset dirty so the asset preflight refuses.
	writeFile(t, filepath.Join(project, "scripts", "thing.sh"), "#!/bin/sh\necho local\n")
	cmd := exec.Command("git", "add", "scripts/thing.sh")
	cmd.Dir = project
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "asset")
	cmd.Dir = project
	_ = cmd.Run()
	writeFile(t, filepath.Join(project, "scripts", "thing.sh"), "#!/bin/sh\necho uncommitted\n")

	before, err := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Run(lacquer, project, false); err == nil {
		t.Fatal("sync must refuse when a managed asset has uncommitted changes")
	}

	after, err := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("CLAUDE.md was rewritten by a sync that failed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The needledrop failure, at the gate that should have caught it: a manifest
// that declares one stack, a repo that has grown a second, and a sync that used
// to print "sync complete" while writing nothing for the second.
func TestSyncRefusesAnUndeclaredStack(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "2\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "CLAUDE.web.md"), "WEB")
	writeFile(t, filepath.Join(lacquer, "profiles", "supabase", "CLAUDE.supabase.md"), "SB")

	writeFile(t, filepath.Join(project, "package.json"), "{}")
	writeFile(t, filepath.Join(project, "server", "supabase", "config.toml"), "x")
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\".\"\nprofiles=[\"web\"]\n")

	_, err := Run(lacquer, project, false)
	if err == nil {
		t.Fatal("sync must refuse rather than silently leave a stack ungated")
	}
	if !strings.Contains(err.Error(), "server -> supabase") ||
		!strings.Contains(err.Error(), "lacquer adopt") {
		t.Errorf("error must name the stack and the fix, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(project, "CLAUDE.md")); statErr == nil {
		t.Error("the refusal must happen before anything is written")
	}
}

// A stack no profile ships for cannot be adopted, so gating on it would punish
// the project for the lacquer's gap. It is reported by `audit`, not by refusing
// every sync forever.
func TestSyncProceedsPastAStackNoProfileCovers(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "2\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "CLAUDE.web.md"), "WEB")

	writeFile(t, filepath.Join(project, "package.json"), "{}")
	writeFile(t, filepath.Join(project, "ios", "Kit", "Package.swift"), "x")
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\".\"\nprofiles=[\"web\"]\n")

	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// [project].exclude is the escape hatch, and it lands in the manifest where the
// next reader sees it — rather than in a flag nobody records.
func TestSyncExcludeSilencesAnUndeclaredStack(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "2\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "CLAUDE.web.md"), "WEB")
	writeFile(t, filepath.Join(lacquer, "profiles", "supabase", "CLAUDE.supabase.md"), "SB")

	writeFile(t, filepath.Join(project, "package.json"), "{}")
	writeFile(t, filepath.Join(project, "fixtures", "supabase", "config.toml"), "x")
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\nexclude=[\"fixtures\"]\n\n[[component]]\npath=\".\"\nprofiles=[\"web\"]\n")

	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
