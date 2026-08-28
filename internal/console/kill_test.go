package console

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoveRecordDropsOnlyTheMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	r1 := Record{Kind: ProjectKind, Name: "alpha", Mode: Background, Dir: "/w/alpha", Task: "t1", DaemonID: "aaa111", StartedAt: time.Now().UTC()}
	r2 := Record{Kind: ProjectKind, Name: "beta", Mode: Background, Dir: "/w/beta", Task: "t2", DaemonID: "bbb222", StartedAt: time.Now().UTC()}
	if err := AppendRecord(path, r1); err != nil {
		t.Fatal(err)
	}
	if err := AppendRecord(path, r2); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRecord(path, r1); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "beta" {
		t.Errorf("got %+v, want only beta left", got)
	}
}

func TestRemoveRecordNoMatchErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	r1 := Record{Kind: ProjectKind, Name: "alpha", Mode: Background, Dir: "/w/alpha", Task: "t1", DaemonID: "aaa111", StartedAt: time.Now().UTC()}
	if err := AppendRecord(path, r1); err != nil {
		t.Fatal(err)
	}
	other := r1
	other.DaemonID = "zzz999"
	if err := RemoveRecord(path, other); err == nil {
		t.Error("expected an error removing a record that is not actually in the file")
	}
}

// A Background record with no live job directory (the state this session hit
// live: 12 dispatches "blocked" with no process at all) must still be
// killable -- Kill's job-directory removal is best-effort, not a hard
// requirement, and the sessions-file cleanup is the part that matters.
func TestKillBackgroundWithNoJobDirectoryStillRemovesRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	r := Record{Kind: ProjectKind, Name: "alpha", Mode: Background, Dir: "/w/alpha", Task: "t1", DaemonID: "no-such-daemon", StartedAt: time.Now().UTC()}
	if err := AppendRecord(path, r); err != nil {
		t.Fatal(err)
	}
	// checkBackground on a nonexistent job dir returns Missing (not Alive),
	// so force is not needed here -- Missing already clears the Alive gate.
	if _, err := Kill(path, r, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	got, err := ReadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d records, want the sessions file empty after kill", len(got))
	}
}

// The actual incident this fixes: an earlier Kill removed only the job
// directory, leaving a git-worktree-locked directory behind. Every
// subsequent redispatch to that project hit the stale lock and got stuck.
// This test builds a REAL locked worktree (not a fixture) and asserts Kill
// actually removes it, using the job state file's own worktreePath -- the
// only authoritative source of that path.
func TestKillRemovesALockedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed in this test environment")
	}

	mainDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", mainDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(mainDir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "initial")

	worktreePath := filepath.Join(mainDir, ".claude", "worktrees", "lacquer-sync-test")
	run("worktree", "add", "-b", "worktree-lacquer-sync-test", worktreePath)
	run("worktree", "lock", worktreePath)

	// Confirm it's really there and really locked before asserting Kill fixed
	// anything -- otherwise a no-op Kill would pass this test by accident.
	list, err := exec.Command("git", "-C", mainDir, "worktree", "list").CombinedOutput()
	if err != nil || !strings.Contains(string(list), "locked") {
		t.Fatalf("setup did not produce a locked worktree; git worktree list:\n%s", list)
	}

	jobsDir := t.TempDir()
	t.Setenv("HOME", jobsDir)
	daemonDir := filepath.Join(jobsDir, ".claude", "jobs", "wt1")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateJSON := `{"state":"blocked","detail":"stuck","worktreePath":"` + worktreePath + `"}`
	if err := os.WriteFile(filepath.Join(daemonDir, "state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	sessionsPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	r := Record{Kind: ProjectKind, Name: "test-proj", Mode: Background, Dir: mainDir, Task: "t1", DaemonID: "wt1", StartedAt: time.Now().UTC()}
	if err := AppendRecord(sessionsPath, r); err != nil {
		t.Fatal(err)
	}

	if note, err := Kill(sessionsPath, r, false); err != nil {
		t.Fatalf("Kill: %v (note: %s)", err, note)
	}

	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree at %s still exists after Kill (err=%v) -- this is the exact bug that stuck 7 redispatched sessions", worktreePath, err)
	}
	list, _ = exec.Command("git", "-C", mainDir, "worktree", "list").CombinedOutput()
	if strings.Contains(string(list), "lacquer-sync-test") {
		t.Errorf("git worktree list still references the killed worktree:\n%s", list)
	}
}

func TestKillRefusesAliveWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	jobsDir := t.TempDir()
	t.Setenv("HOME", jobsDir) // claudeJobsDir reads $HOME
	daemonDir := filepath.Join(jobsDir, ".claude", "jobs", "alive1")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daemonDir, "state.json"), []byte(`{"state":"working","detail":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Record{Kind: ProjectKind, Name: "alpha", Mode: Background, Dir: "/w/alpha", Task: "t1", DaemonID: "alive1", StartedAt: time.Now().UTC()}
	if err := AppendRecord(path, r); err != nil {
		t.Fatal(err)
	}
	if _, err := Kill(path, r, false); err == nil {
		t.Error("expected Kill to refuse an alive session without force")
	}
	if _, err := Kill(path, r, true); err != nil {
		t.Fatalf("Kill with force: %v", err)
	}
	got, err := ReadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d records, want empty after forced kill", len(got))
	}
}
