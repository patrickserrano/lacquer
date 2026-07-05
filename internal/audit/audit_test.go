package audit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/harness/internal/audit"
	syncpkg "github.com/patrickserrano/harness/internal/sync"
)

func TestFormatReportsAndSummarizesClobbers(t *testing.T) {
	rows := []audit.Row{
		{Dest: "CLAUDE.md", Kind: "region", Detail: "core", Status: audit.OK},
		{Dest: ".claude/skills/git.md", Kind: "asset", Status: audit.Modified},
		{Dest: "ios/CLAUDE.md", Kind: "region", Detail: "ios", Status: audit.Behind},
	}
	out := audit.Format(rows, 7)

	if !strings.Contains(out, "harness audit — project vs harness v7") {
		t.Errorf("missing header:\n%s", out)
	}
	// The Modified unit is listed under its status section by bare dest...
	if !strings.Contains(out, "locally-modified:\n  .claude/skills/git.md") {
		t.Errorf("modified unit not listed under its section:\n%s", out)
	}
	// ...and a region under Behind is labelled dest#marker.
	if !strings.Contains(out, "ios/CLAUDE.md#ios") {
		t.Errorf("region label missing marker key:\n%s", out)
	}
	// The clobber summary counts Modified (and Conflict) units.
	if !strings.Contains(out, "1 unit(s) would be overwritten by sync") {
		t.Errorf("missing clobber summary:\n%s", out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// setup builds a minimal harness + a git project, syncs once, and commits the
// result so the working tree is clean (isolating the audit guard from gitguard).
func setup(t *testing.T) (harness, project string) {
	t.Helper()
	harness = t.TempDir()
	project = t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE RULES")
	writeFile(t, filepath.Join(harness, "core", "skills", "git.md"), "GIT SKILL")
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")
	git(t, project, "init", "-q")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "init")

	if _, err := syncpkg.Run(harness, project, false); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "sync")
	return harness, project
}

func statusOf(rows []audit.Row, dest string) audit.Status {
	for _, r := range rows {
		if r.Dest == dest {
			return r.Status
		}
	}
	return audit.Status("<absent>")
}

func TestAuditRoundTripAllOK(t *testing.T) {
	harness, project := setup(t)
	rows, _, err := audit.Classify(harness, project)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.Status != audit.OK {
			t.Errorf("%s = %s, want ok (lock must match what sync wrote)", r.Dest, r.Status)
		}
	}
}

func TestAuditDetectsLocallyModified(t *testing.T) {
	harness, project := setup(t)
	// Commit a local edit to a harness-managed asset (clean tree, so gitguard
	// would NOT catch it — only the lock-based audit can).
	skill := filepath.Join(project, ".claude", "skills", "git.md")
	writeFile(t, skill, "HACKED")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "local edit")

	rows, _, err := audit.Classify(harness, project)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := statusOf(rows, ".claude/skills/git.md"); got != audit.Modified {
		t.Errorf("status = %s, want locally-modified", got)
	}
	if clob := audit.Clobbered(rows); len(clob) != 1 || clob[0] != ".claude/skills/git.md" {
		t.Errorf("Clobbered = %v, want [.claude/skills/git.md]", clob)
	}
}

func TestSyncRefusesToClobberWithoutForce(t *testing.T) {
	harness, project := setup(t)
	skill := filepath.Join(project, ".claude", "skills", "git.md")
	writeFile(t, skill, "HACKED")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "local edit")

	// Without --force, sync must refuse and leave the local content intact.
	if _, err := syncpkg.Run(harness, project, false); err == nil {
		t.Fatal("expected sync to refuse clobbering a local change")
	}
	if got, _ := os.ReadFile(skill); string(got) != "HACKED" {
		t.Errorf("local change was overwritten despite no --force: %q", got)
	}

	// With --force, sync adopts the harness version.
	if _, err := syncpkg.Run(harness, project, true); err != nil {
		t.Fatalf("forced sync: %v", err)
	}
	if got, _ := os.ReadFile(skill); string(got) != "GIT SKILL" {
		t.Errorf("forced sync did not reset to harness content: %q", got)
	}
}

func TestAuditReportsBehindWhenHarnessAdvances(t *testing.T) {
	harness, project := setup(t)
	// Harness evolves the core rules; the project is untouched.
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE RULES V2")
	writeFile(t, filepath.Join(harness, "VERSION"), "2\n")

	rows, _, err := audit.Classify(harness, project)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := statusOf(rows, "CLAUDE.md"); got != audit.Behind {
		t.Errorf("status = %s, want behind (project clean, harness advanced)", got)
	}
	// Behind must NOT block a normal sync.
	if _, err := syncpkg.Run(harness, project, false); err != nil {
		t.Errorf("behind unit should sync without --force: %v", err)
	}
}

func TestAuditUntrackedWithoutLock(t *testing.T) {
	harness, project := setup(t)
	// Drop the lock (simulate a project synced before locking existed) and edit.
	if err := os.Remove(filepath.Join(project, ".harness.lock")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project, ".claude", "skills", "git.md"), "DIFFERENT")

	rows, _, err := audit.Classify(harness, project)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := statusOf(rows, ".claude/skills/git.md"); got != audit.Untracked {
		t.Errorf("status = %s, want untracked (no lock baseline)", got)
	}
	// Untracked must never block sync (lock bootstraps).
	if clob := audit.Clobbered(rows); len(clob) != 0 {
		t.Errorf("untracked must not be clobber-blocking, got %v", clob)
	}
}
