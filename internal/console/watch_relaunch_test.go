package console

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// Before this, Relaunch dropped the relaunched session's record and Watch
// wrote nothing back: the dead record stayed Failed, every later
// `watch --relaunch` pass relaunched it again (for bg, another claude in the
// same worktree each time), and the sessions it did start were recorded
// nowhere.

// fakeJobs points $HOME at a temp dir and returns a function that writes a
// bg daemon's state file there, the way `claude --bg` would.
func fakeJobs(t *testing.T) func(id, state string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return func(id, state string) {
		t.Helper()
		dir := filepath.Join(home, ".claude", "jobs", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"state":"`+state+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSessions(t *testing.T, records ...Record) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.jsonl")
	for _, r := range records {
		if err := AppendRecord(path, r); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func readSessions(t *testing.T, path string) []Record {
	t.Helper()
	records, err := ReadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// (a) A dead bg session: two passes start exactly one new session, and the
// file ends with one record for it, pointing at the new daemon -- in the
// same place in the file, with its original task.
func TestWatchRelaunchReplacesADeadBackgroundRecord(t *testing.T) {
	roster, rec, calls := bgDispatchOnce(t)
	job := fakeJobs(t)
	job(rec.DaemonID, "failed")
	bystander := Record{Kind: RoleKind, Name: "someone-else", Mode: Tmux, Task: "t", StartedAt: time.Now().UTC()}
	path := writeSessions(t, rec, bystander)

	t.Setenv("FAKE_DAEMON_ID", "beef0002")
	job("beef0002", "working")
	for pass := 1; pass <= 2; pass++ {
		results, err := Watch(path, roster, RoleRoster{}, nil, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if relaunched := results[0].Relaunched; relaunched != (pass == 1) {
			t.Fatalf("pass %d: relaunched = %v (status %s)", pass, relaunched, results[0].Status)
		}
		if results[0].RecordErr != nil {
			t.Fatalf("pass %d: %v", pass, results[0].RecordErr)
		}
	}

	if n := len(claudeCalls(t, calls)); n != 2 {
		t.Errorf("claude was started %d times, want 2: the dispatch and exactly one relaunch", n)
	}
	got := readSessions(t, path)
	if len(got) != 2 {
		t.Fatalf("sessions file has %d records, want 2: %+v", len(got), got)
	}
	if got[0].DaemonID != "beef0002" || got[0].Worktree != rec.Worktree || got[0].Task != "the task" || got[0].LaunchError != "" {
		t.Errorf("replacement record = %+v; want the new daemon, the same worktree, the original task", got[0])
	}
	if got[1] != bystander {
		t.Errorf("an unrelated record changed or moved: %+v", got[1])
	}
}

// (b) A failed launch whose relaunch succeeds: replaced by the started
// session, with its failure count cleared.
func TestWatchRelaunchReplacesALaunchErrorRecord(t *testing.T) {
	calls := fakeClaude(t)
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "alpha", Path: repo}}}
	failed := Record{Kind: ProjectKind, Name: "alpha", Mode: Background, Dir: repo, Task: "the task",
		LaunchError: "no worktree", FailedLaunches: 1, StartedAt: time.Now().UTC()}
	path := writeSessions(t, failed)
	job := fakeJobs(t)
	t.Setenv("FAKE_DAEMON_ID", "beef0003")
	job("beef0003", "working")

	for pass := 1; pass <= 2; pass++ {
		results, err := Watch(path, roster, RoleRoster{}, nil, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if relaunched := results[0].Relaunched; relaunched != (pass == 1) {
			t.Fatalf("pass %d: relaunched = %v (status %s %s)", pass, relaunched, results[0].Status, results[0].Detail)
		}
	}
	if n := len(claudeCalls(t, calls)); n != 1 {
		t.Errorf("claude was started %d times, want exactly 1", n)
	}
	got := readSessions(t, path)
	if len(got) != 1 || got[0].DaemonID != "beef0003" || got[0].LaunchError != "" || got[0].FailedLaunches != 0 || got[0].Worktree == "" {
		t.Errorf("sessions file = %+v; want one record for the started session", got)
	}
}

// (b, continued) A launch that keeps failing is retried until it has failed
// MaxFailedLaunches times in a row, then held for the operator -- not
// retried on every pass forever.
func TestWatchStopsRelaunchingALaunchThatKeepsFailing(t *testing.T) {
	tmuxCalls := fakeTmux(t)
	roles := roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "lead", Dir: t.TempDir()})
	failed := Record{Kind: RoleKind, Name: "lead", Mode: Tmux, Dir: roles.Role[0].Dir, Task: "lead",
		LaunchError: "open terminal failed: not a terminal", FailedLaunches: 1, StartedAt: time.Now().UTC()}
	path := writeSessions(t, failed)

	for pass := 1; pass <= 4; pass++ {
		results, err := Watch(path, fleet.Roster{}, roles, nil, true, false)
		if err != nil {
			t.Fatal(err)
		}
		wantRelaunch := pass <= MaxFailedLaunches-1
		if results[0].Relaunched != wantRelaunch {
			t.Fatalf("pass %d: relaunched = %v, want %v (held: %q)", pass, results[0].Relaunched, wantRelaunch, results[0].Held)
		}
		if !wantRelaunch && results[0].Held == "" {
			t.Errorf("pass %d: a held record must say why it was not relaunched", pass)
		}
	}
	data, err := os.ReadFile(tmuxCalls)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "new-session"); n != MaxFailedLaunches-1 {
		t.Errorf("tmux new-session ran %d times over 4 passes, want %d", n, MaxFailedLaunches-1)
	}
	got := readSessions(t, path)
	if len(got) != 1 || got[0].FailedLaunches != MaxFailedLaunches || !strings.Contains(got[0].LaunchError, "not a terminal") || got[0].Task != "lead" {
		t.Errorf("sessions file = %+v; want one record carrying %d failed launches", got, MaxFailedLaunches)
	}
}

// (c) A dry-run relaunch starts nothing and writes nothing.
func TestWatchDryRunRelaunchWritesNothing(t *testing.T) {
	roster, rec, calls := bgDispatchOnce(t)
	job := fakeJobs(t)
	job(rec.DaemonID, "failed")
	path := writeSessions(t, rec)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	results, err := Watch(path, roster, RoleRoster{}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].Relaunched || results[0].Replacement != nil {
		t.Errorf("a dry run should show the relaunch and replace nothing: %+v", results[0])
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("a dry run rewrote the sessions file:\nbefore %s\nafter  %s", before, after)
	}
	if n := len(claudeCalls(t, calls)); n != 1 {
		t.Errorf("a dry run started claude (%d calls, want only the original dispatch)", n)
	}
}
