package console

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "initial commit")
}

// The minimum viable handoff a relaunch can give: the original task, plus
// what's cheaply inspectable about the directory it was running in.
func TestBuildRelaunchTaskIncludesOriginalTaskAndGitContext(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	r := Record{Task: "original brief", Dir: dir}
	task := buildRelaunchTask(r)
	if !strings.Contains(task, "original brief") {
		t.Errorf("expected the original task to be preserved:\n%s", task)
	}
	if !strings.Contains(task, "died") {
		t.Errorf("expected an explicit note that the session died:\n%s", task)
	}
	if !strings.Contains(task, "initial commit") {
		t.Errorf("expected git log -1 context:\n%s", task)
	}
	if !strings.Contains(task, "clean") {
		t.Errorf("expected a clean-tree note for a repo with nothing staged:\n%s", task)
	}
}

func TestBuildRelaunchTaskReportsUncommittedChanges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := buildRelaunchTask(Record{Task: "t", Dir: dir})
	if !strings.Contains(task, "f.txt") {
		t.Errorf("expected git status --short to surface the modified file:\n%s", task)
	}
}

// A directory that is not a git repo at all (or doesn't exist) must not
// break the handoff -- it just has less context to offer.
func TestBuildRelaunchTaskToleratesNonGitDir(t *testing.T) {
	task := buildRelaunchTask(Record{Task: "original", Dir: t.TempDir()})
	if !strings.Contains(task, "original") {
		t.Errorf("expected the task to still include the original brief:\n%s", task)
	}
}

func TestRelaunchDispatchesAProjectRecordViaDispatch(t *testing.T) {
	roster := rosterOf("alpha")
	r := Record{Kind: ProjectKind, Name: "alpha", Mode: Tmux, Dir: "/w/alpha", Task: "original"}
	out, err := Relaunch(r, roster, RoleRoster{}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tmux new-session -A -s alpha") {
		t.Errorf("expected a project relaunch to go through Dispatch:\n%s", out)
	}
	if !strings.Contains(out, "died and is being relaunched") {
		t.Errorf("expected the relaunch task, not the bare original:\n%s", out)
	}
}

func TestRelaunchDispatchesARoleRecordViaDispatchRole(t *testing.T) {
	roles := roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "original", Dir: "/fleet-ops"})
	r := Record{Kind: RoleKind, Name: "lead", Mode: Tmux, Dir: "/fleet-ops", Task: "original"}
	out, err := Relaunch(r, fleet.Roster{}, roles, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tmux new-session -A -s lead") {
		t.Errorf("expected a role relaunch to go through DispatchRole:\n%s", out)
	}
}

func TestRelaunchRejectsUnknownKind(t *testing.T) {
	r := Record{Kind: "bogus", Name: "x", Task: "t"}
	if _, err := Relaunch(r, fleet.Roster{}, RoleRoster{}, nil, true); err == nil {
		t.Error("expected rejection of a record with an unknown kind")
	}
}

// Watch must never act on its own -- relaunch only happens when explicitly
// asked, and only for a record confirmed Failed.
func TestWatchOnlyRelaunchesFailedRecordsWhenAsked(t *testing.T) {
	dir := t.TempDir()
	sessionsPath := filepath.Join(dir, "sessions.jsonl")

	// A tmux-mode record with no such session -- Missing, not Failed.
	if err := AppendRecord(sessionsPath, Record{Kind: ProjectKind, Name: "no-such-tmux-session-xyz", Mode: Tmux, Dir: "/w/x", Task: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this test environment")
	}

	roster := rosterOf("no-such-tmux-session-xyz")
	results, err := Watch(sessionsPath, roster, RoleRoster{}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status != Missing {
		t.Fatalf("status = %q, want %q", results[0].Status, Missing)
	}
	if results[0].Relaunched {
		t.Error("a Missing record must not be relaunched -- there is no state to relaunch FROM")
	}
}

func TestWatchWithoutRelaunchNeverActs(t *testing.T) {
	dir := t.TempDir()
	sessionsPath := filepath.Join(dir, "sessions.jsonl")
	if err := AppendRecord(sessionsPath, Record{Kind: ProjectKind, Name: "alpha", Mode: Background, DaemonID: "does-not-exist", Dir: "/w/a", Task: "t"}); err != nil {
		t.Fatal(err)
	}
	results, err := Watch(sessionsPath, rosterOf("alpha"), RoleRoster{}, nil, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Relaunched {
			t.Error("relaunch must be opt-in via the relaunch flag, never automatic")
		}
	}
}

func TestWatchTextReportsNoSessions(t *testing.T) {
	var b strings.Builder
	WatchText(&b, nil)
	if !strings.Contains(b.String(), "no sessions") {
		t.Errorf("expected an explicit empty-state message, got:\n%s", b.String())
	}
}
