package console

import (
	"os"
	"path/filepath"
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
