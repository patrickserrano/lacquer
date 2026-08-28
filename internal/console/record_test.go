package console

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendRecordThenReadRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	r1 := Record{Kind: ProjectKind, Name: "alpha", Mode: Tmux, Dir: "/w/alpha", Task: "do a thing", StartedAt: time.Now().UTC()}
	r2 := Record{Kind: RoleKind, Name: "lead", Mode: Background, Dir: "/fleet-ops", Task: "supervise", DaemonID: "abc123", StartedAt: time.Now().UTC()}
	if err := AppendRecord(path, r1); err != nil {
		t.Fatal(err)
	}
	if err := AppendRecord(path, r2); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
	if got[0].Name != "alpha" || got[0].Kind != ProjectKind {
		t.Errorf("record 0 = %+v, want alpha/project", got[0])
	}
	if got[1].Name != "lead" || got[1].DaemonID != "abc123" {
		t.Errorf("record 1 = %+v, want lead/abc123", got[1])
	}
}

// A fleet with nothing dispatched yet is a normal, empty state, not a broken
// one -- watch must not fail just because no one has ever run dispatch.
func TestReadRecordsOnMissingFileReturnsEmpty(t *testing.T) {
	got, err := ReadRecords(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err != nil {
		t.Fatalf("a missing sessions file must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d records, want 0", len(got))
	}
}

// An interrupted AppendRecord (process killed mid-write) leaves a truncated
// last line. Losing visibility into every prior dispatch because of that one
// line would be a worse failure than skipping it.
func TestReadRecordsSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	content := `{"kind":"project","name":"alpha","mode":"tmux","dir":"/w/a","task":"t","startedAt":"2026-01-01T00:00:00Z"}
{"kind":"project","name":"broken` + "\n" + `{"kind":"project","name":"bravo","mode":"tmux","dir":"/w/b","task":"t","startedAt":"2026-01-01T00:00:00Z"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecords(path)
	if err != nil {
		t.Fatalf("a corrupt line must not fail the whole read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2 (the corrupt line skipped): %+v", len(got), got)
	}
	if got[0].Name != "alpha" || got[1].Name != "bravo" {
		t.Errorf("got names %q, %q, want alpha, bravo", got[0].Name, got[1].Name)
	}
}

func TestCheckBackgroundMissingDaemonID(t *testing.T) {
	r := Record{Mode: Background, Name: "x"}
	status, detail, err := r.Check()
	if err != nil {
		t.Fatal(err)
	}
	if status != Missing {
		t.Errorf("status = %q, want %q — no id was ever captured, there is nothing to check", status, Missing)
	}
	if detail == "" {
		t.Error("expected a detail explaining why")
	}
}

// checkBackground enriches its detail with cmux's own pid-backed signal when
// the job state file carries a sessionId and cmux reports a match -- see
// cmux.go. This is corroboration only: it must never change the Status
// checkJobState already decided.
func TestCheckBackgroundEnrichesDetailFromCmux(t *testing.T) {
	jobsDir := t.TempDir()
	t.Setenv("HOME", jobsDir)
	daemonDir := filepath.Join(jobsDir, ".claude", "jobs", "cmuxenr1")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateJSON := `{"state":"working","detail":"doing the thing","sessionId":"full-uuid-here"}`
	if err := os.WriteFile(filepath.Join(daemonDir, "state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	origRunner := CmuxRunner
	defer func() { CmuxRunner = origRunner }()
	CmuxRunner = func(sessionID string) ([]byte, error) {
		if sessionID != "full-uuid-here" {
			t.Fatalf("sessionID = %q, want full-uuid-here", sessionID)
		}
		return []byte(`{"sessions":[{"pid":9999,"stored_pid_exists":true,"agent_lifecycle":"running"}]}`), nil
	}

	r := Record{Mode: Background, DaemonID: "cmuxenr1"}
	status, detail, err := r.Check()
	if err != nil {
		t.Fatal(err)
	}
	if status != Alive {
		t.Errorf("status = %q, want %q -- cmux enrichment must not change the verdict", status, Alive)
	}
	if !strings.Contains(detail, "doing the thing") {
		t.Errorf("detail = %q, lost the job's own detail", detail)
	}
	if !strings.Contains(detail, "9999") || !strings.Contains(detail, "running") {
		t.Errorf("detail = %q, want it enriched with cmux's pid/lifecycle", detail)
	}
}

// A dead cmux binary (or an old job state with no sessionId) must not break
// Check -- enrichment is optional, the job-state verdict must still stand.
func TestCheckBackgroundToleratesCmuxFailure(t *testing.T) {
	jobsDir := t.TempDir()
	t.Setenv("HOME", jobsDir)
	daemonDir := filepath.Join(jobsDir, ".claude", "jobs", "cmuxerr1")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateJSON := `{"state":"working","detail":"doing the thing","sessionId":"full-uuid-here"}`
	if err := os.WriteFile(filepath.Join(daemonDir, "state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	origRunner := CmuxRunner
	defer func() { CmuxRunner = origRunner }()
	CmuxRunner = func(sessionID string) ([]byte, error) {
		return nil, errors.New("cmux not installed")
	}

	r := Record{Mode: Background, DaemonID: "cmuxerr1"}
	status, detail, err := r.Check()
	if err != nil {
		t.Fatal(err)
	}
	if status != Alive {
		t.Errorf("status = %q, want %q", status, Alive)
	}
	if detail != "doing the thing" {
		t.Errorf("detail = %q, want the plain job-state detail with no cmux enrichment attempted", detail)
	}
}

func TestCheckJobStateReportsFailed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"state":"failed","detail":"exit 1 before init"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	status, detail, err := checkJobState(path)
	if err != nil {
		t.Fatal(err)
	}
	if status != Failed {
		t.Errorf("status = %q, want %q", status, Failed)
	}
	if detail != "exit 1 before init" {
		t.Errorf("detail = %q, want the job's own detail field", detail)
	}
}

// "working" and any unrecognized future state are both treated as alive: the
// only confirmed-dead state is "failed".
func TestCheckJobStateTreatsWorkingAsAlive(t *testing.T) {
	for _, state := range []string{"working", "some-future-state"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")
		if err := os.WriteFile(path, []byte(`{"state":"`+state+`","detail":"d"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		status, _, err := checkJobState(path)
		if err != nil {
			t.Fatal(err)
		}
		if status != Alive {
			t.Errorf("state %q: status = %q, want %q — an unconfirmed-dead state must default to alive", state, status, Alive)
		}
	}
}

// "blocked" is its own status, not folded into Alive: a batch of bg
// dispatches sat stalled on a permission prompt for 25+ minutes this session
// while every liveness check said "alive" because blocked was indistinguishable
// from working. Surfacing it distinctly is the whole fix.
func TestCheckJobStateTreatsBlockedDistinctly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"state":"blocked","detail":"waiting on permission"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	status, detail, err := checkJobState(path)
	if err != nil {
		t.Fatal(err)
	}
	if status != Blocked {
		t.Errorf("status = %q, want %q", status, Blocked)
	}
	if detail != "waiting on permission" {
		t.Errorf("detail = %q, want the job's own detail field", detail)
	}
}

func TestCheckJobStateMissingFileIsMissingNotError(t *testing.T) {
	status, _, err := checkJobState(filepath.Join(t.TempDir(), "no-such-job", "state.json"))
	if err != nil {
		t.Fatalf("a job that was never recorded (or was GC'd) is Missing, not an error: %v", err)
	}
	if status != Missing {
		t.Errorf("status = %q, want %q", status, Missing)
	}
}

func TestCheckTmuxSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in this test environment")
	}
	name := "lacquer-console-test-" + t.Name()
	// Ensure clean slate: kill any leftover session under this name from a
	// prior failed run before asserting Missing.
	_ = exec.Command("tmux", "kill-session", "-t", name).Run()

	r := Record{Mode: Tmux, Name: name}
	status, _, err := r.Check()
	if err != nil {
		t.Fatal(err)
	}
	if status != Missing {
		t.Fatalf("status = %q, want %q before the session exists", status, Missing)
	}

	if err := exec.Command("tmux", "new-session", "-d", "-s", name).Run(); err != nil {
		t.Fatalf("could not start a real tmux session to test against: %v", err)
	}
	defer exec.Command("tmux", "kill-session", "-t", name).Run()

	status, _, err = r.Check()
	if err != nil {
		t.Fatal(err)
	}
	if status != Alive {
		t.Errorf("status = %q, want %q once the session is running", status, Alive)
	}
}
