package skillsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// fakeAdd simulates the `skills` CLI's project-scoped `add` behavior closely
// enough to test Install's orchestration: each successful call appends the
// requested skill to a skills-lock.json in dir, matching the real tool's
// accumulate-don't-clobber lockfile behavior observed against the live CLI.
func fakeAdd(t *testing.T, fail map[string]bool) func(dir string, args ...string) ([]byte, error) {
	t.Helper()
	return func(dir string, args ...string) ([]byte, error) {
		// args: "add", source, "-s", name, "-p", "-y"
		if len(args) < 4 || args[0] != "add" || args[2] != "-s" {
			t.Fatalf("unexpected args: %v", args)
		}
		source, name := args[1], args[3]
		if fail[name] {
			return []byte("boom"), fmt.Errorf("exit status 1")
		}
		lockPath := filepath.Join(dir, "skills-lock.json")
		existing := `{"version":1,"skills":{}}`
		if data, err := os.ReadFile(lockPath); err == nil {
			existing = string(data)
		}
		// Minimal hand-rolled JSON splice: good enough for a test fixture.
		injected := fmt.Sprintf(`"%s":{"source":%q}`, name, source)
		updated := existing
		if existing == `{"version":1,"skills":{}}` {
			updated = fmt.Sprintf(`{"version":1,"skills":{%s}}`, injected)
		} else {
			// insert before the closing "}}" of the skills object
			idx := len(existing) - 2
			updated = existing[:idx] + "," + injected + existing[idx:]
		}
		if err := os.WriteFile(lockPath, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		// Match the real CLI: the canonical copy always lands at
		// .agents/skills/<name>, which bridgeToolDirs reads from.
		skillDir := filepath.Join(dir, ".agents", "skills", name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}
}

func TestInstallCallsAddForEachEntry(t *testing.T) {
	dir := t.TempDir()
	orig := Runner
	var calls [][]string
	Runner = func(d string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return fakeAdd(t, nil)(d, args...)
	}
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{
		{Source: "dpearson2699/swift-ios-skills", Name: "healthkit"},
		{Source: "patrickserrano/lacquer", Name: "security-review"},
	}
	res, err := Install(dir, entries, []string{"claude"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if len(res.Installed) != 2 {
		t.Errorf("Installed = %v", res.Installed)
	}
	if len(res.Failed) != 0 {
		t.Errorf("Failed = %v", res.Failed)
	}
	if len(res.Undeclared) != 0 {
		t.Errorf("Undeclared = %v, want none (both installed entries were declared)", res.Undeclared)
	}
}

func TestInstallContinuesAfterOneFailure(t *testing.T) {
	dir := t.TempDir()
	orig := Runner
	Runner = fakeAdd(t, map[string]bool{"bad-skill": true})
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{
		{Source: "owner/repo", Name: "bad-skill"},
		{Source: "owner/repo", Name: "good-skill"},
	}
	res, err := Install(dir, entries, []string{"claude"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 1 || res.Installed[0] != "good-skill" {
		t.Errorf("Installed = %v", res.Installed)
	}
	if _, ok := res.Failed["bad-skill"]; !ok {
		t.Errorf("Failed missing bad-skill: %v", res.Failed)
	}
}

func TestInstallFlagsUndeclaredSkills(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a lockfile with a skill the manifest no longer declares.
	if err := os.WriteFile(filepath.Join(dir, "skills-lock.json"),
		[]byte(`{"version":1,"skills":{"orphaned-skill":{"source":"owner/repo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{{Source: "owner/repo", Name: "wanted-skill"}}
	res, err := Install(dir, entries, []string{"claude"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Undeclared) != 1 || res.Undeclared[0] != "orphaned-skill" {
		t.Errorf("Undeclared = %v, want [orphaned-skill]", res.Undeclared)
	}
}

func TestInstallWithNoEntriesStillReportsUndeclared(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "skills-lock.json"),
		[]byte(`{"version":1,"skills":{"leftover":{"source":"owner/repo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	res, err := Install(dir, nil, []string{"claude"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 0 {
		t.Errorf("Installed = %v, want none", res.Installed)
	}
	if len(res.Undeclared) != 1 || res.Undeclared[0] != "leftover" {
		t.Errorf("Undeclared = %v", res.Undeclared)
	}
}

func TestInstallWithNoLockfileYet(t *testing.T) {
	dir := t.TempDir()
	// No skills-lock.json exists and Install is called with zero entries —
	// must not error just because nothing has ever been installed here.
	res, err := Install(dir, nil, []string{"claude"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 0 || len(res.Undeclared) != 0 {
		t.Errorf("res = %+v, want empty", res)
	}
}

func TestInstallBridgesCodexDir(t *testing.T) {
	dir := t.TempDir()
	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{{Source: "owner/repo", Name: "some-skill"}}
	res, err := Install(dir, entries, []string{"claude", "codex", "antigravity"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 1 {
		t.Fatalf("Installed = %v", res.Installed)
	}

	link := filepath.Join(dir, ".codex", "skills", "some-skill")
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("expected a bridged .codex/skills entry: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf(".codex/skills/some-skill is not a symlink: mode=%v", fi.Mode())
	}
	// The symlink must actually resolve to the canonical skill content.
	data, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "name: some-skill") {
		t.Errorf("symlink does not resolve to the canonical skill: data=%q err=%v", data, err)
	}
}

func TestInstallDoesNotBridgeClaudeOrAntigravity(t *testing.T) {
	dir := t.TempDir()
	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{{Source: "owner/repo", Name: "some-skill"}}
	if _, err := Install(dir, entries, []string{"claude", "antigravity"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// `skills add` itself is responsible for Claude Code (a symlink it
	// creates) and antigravity (the canonical location) -- bridgeToolDirs
	// must not also write a .claude/skills entry the fake Runner never made.
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "skills", "some-skill")); err == nil {
		t.Error("bridgeToolDirs should not create a .claude/skills entry; that's skills add's own job")
	}
}

func TestInstallBridgeNeverClobbersExisting(t *testing.T) {
	dir := t.TempDir()
	// A lacquer-synced skill of the same name already lives in .codex/skills
	// (a real directory, not a symlink) -- bridging a same-named third-party
	// skill must never overwrite it.
	existing := filepath.Join(dir, ".codex", "skills", "some-skill")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "SKILL.md"), []byte("pre-existing lacquer-synced content"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{{Source: "owner/repo", Name: "some-skill"}}
	if _, err := Install(dir, entries, []string{"claude", "codex"}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	fi, err := os.Lstat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("pre-existing real directory was replaced with a symlink")
	}
	data, err := os.ReadFile(filepath.Join(existing, "SKILL.md"))
	if err != nil || string(data) != "pre-existing lacquer-synced content" {
		t.Errorf("pre-existing content was clobbered: data=%q err=%v", data, err)
	}
}
