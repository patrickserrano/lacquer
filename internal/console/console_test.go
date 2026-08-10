package console

import (
	"bytes"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// A background session runs in a git worktree BENEATH the project, so equality
// matching would orphan every backgrounded agent — the ones most in need of a
// home on this screen.
func TestSessionInAWorktreeBelongsToItsProject(t *testing.T) {
	root := "/w/proj"
	cases := map[string]bool{
		"/w/proj":                         true,
		"/w/proj/.claude/worktrees/abc":   true,
		"/w/proj/sub":                     true,
		"/w/project-other":                false, // prefix must stop at a path boundary
		"/w":                              false,
		"/w/proj-2/.claude/worktrees/abc": false,
	}
	for cwd, want := range cases {
		if got := under(cwd, root); got != want {
			t.Errorf("under(%q, %q) = %v, want %v", cwd, root, got, want)
		}
	}
}

// Observed, not defensive: `claude agents --json` returned two entries with
// different names and the same sessionId. Counting both inflates the per-project
// and total session counts.
func TestDuplicateSessionIDsCollapse(t *testing.T) {
	in := []Session{
		{Name: "a", SessionID: "same", Status: "idle"},
		{Name: "b", SessionID: "other", Status: "idle"},
		{Name: "c", SessionID: "same", Status: "idle"},
	}
	got := normalize(in)
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2 — one live session listed twice must count once", len(got))
	}
}

// Observed: 11 of 40 live sessions carried no status. Rendered raw that is an
// empty "()", which reads as a bug in the console rather than an absent value.
func TestMissingStatusBecomesUnknown(t *testing.T) {
	got := normalize([]Session{{Name: "a", SessionID: "x"}})
	if got[0].Status != "unknown" {
		t.Errorf("status = %q, want %q", got[0].Status, "unknown")
	}
}

// An entry with no id must not be dropped by the dedupe.
func TestSessionsWithoutIDsAreKept(t *testing.T) {
	got := normalize([]Session{{Name: "a"}, {Name: "b"}})
	if len(got) != 2 {
		t.Errorf("got %d, want 2 — an absent id is not a duplicate", len(got))
	}
}

func TestRollupRanksFailureAbovePending(t *testing.T) {
	type c = struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	cases := []struct {
		name string
		in   []c
		want string
	}{
		{"empty", nil, ""},
		{"all pass", []c{{"COMPLETED", "SUCCESS"}}, "pass"},
		{"one pending", []c{{"COMPLETED", "SUCCESS"}, {"IN_PROGRESS", ""}}, "pending"},
		// The reader is deciding where to spend attention. One failure among
		// nine passes needs a human more than one still running, so it must not
		// be buried by a majority verdict.
		{"failure outranks pending", []c{{"IN_PROGRESS", ""}, {"COMPLETED", "FAILURE"}}, "fail"},
		{"skipped is not failure", []c{{"COMPLETED", "SKIPPED"}}, "pass"},
	}
	for _, tc := range cases {
		if got := rollup(tc.in); got != tc.want {
			t.Errorf("%s: rollup = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A console that refuses to run without every dependency is useless exactly
// when something is broken, which is when it is most needed.
func TestMissingSourceIsNamedNotFatal(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, Result{
		Rows:        []Row{{Name: "p", Notes: []string{"something"}}},
		Unavailable: []string{"gh pr list (not installed)"},
	})
	s := buf.String()
	if !strings.Contains(s, "p") || !strings.Contains(s, "something") {
		t.Errorf("the rows it DID gather must still render:\n%s", s)
	}
	if !strings.Contains(s, "unavailable") || !strings.Contains(s, "blank, not empty") {
		t.Errorf("a missing source must be named — otherwise a thin report reads as a healthy fleet:\n%s", s)
	}
}

func rosterOf(names ...string) fleet.Roster {
	var r fleet.Roster
	for _, n := range names {
		r.Project = append(r.Project, fleet.Entry{Name: n, Path: "/w/" + n})
	}
	return r
}

// Dispatching to the wrong project because a name was close enough is worse
// than being told to retype it.
func TestDispatchRejectsUnknownProject(t *testing.T) {
	_, err := Dispatch(rosterOf("alpha", "bravo"), nil, "alfa", "do a thing", Background, true)
	if err == nil {
		t.Fatal("expected rejection of a project not in the roster")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("the error should list the known names, got: %v", err)
	}
}

func TestDispatchRequiresATask(t *testing.T) {
	if _, err := Dispatch(rosterOf("alpha"), nil, "alpha", "   ", Background, true); err == nil {
		t.Error("expected rejection of an empty task")
	}
}

func TestDispatchRejectsUnknownMode(t *testing.T) {
	if _, err := Dispatch(rosterOf("alpha"), nil, "alpha", "task", Mode("wherever"), true); err == nil {
		t.Error("expected rejection of an unknown mode")
	}
}

// A second agent in the same project is sometimes right; a second agent nobody
// knows about is not. Warn, do not refuse.
func TestDispatchWarnsAboutExistingSessionsButProceeds(t *testing.T) {
	sessions := []Session{{Name: "alpha-1", Status: "working", CWD: "/w/alpha"}}
	out, err := Dispatch(rosterOf("alpha"), sessions, "alpha", "task", Background, true)
	if err != nil {
		t.Fatalf("an existing session must not block dispatch: %v", err)
	}
	if !strings.Contains(out, "already has 1 session") {
		t.Errorf("the warning is missing:\n%s", out)
	}
	if !strings.Contains(out, "dispatch:") {
		t.Errorf("it must still dispatch:\n%s", out)
	}
}

// The mode decides whether edits land in an isolated worktree or the real
// checkout, so the command must differ accordingly.
func TestDispatchModesTargetDifferentPlaces(t *testing.T) {
	bg, err := Dispatch(rosterOf("alpha"), nil, "alpha", "task", Background, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bg, "claude --bg --cwd /w/alpha") {
		t.Errorf("bg mode must launch a background agent:\n%s", bg)
	}
	tm, err := Dispatch(rosterOf("alpha"), nil, "alpha", "task", Tmux, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tm, "tmux new-session -A -s alpha -c /w/alpha") {
		t.Errorf("tmux mode must attach-or-create the project session:\n%s", tm)
	}
}

// Dry run is the guard that makes the two modes safe to explore.
func TestDryRunStartsNothing(t *testing.T) {
	out, err := Dispatch(rosterOf("alpha"), nil, "alpha", "task", Background, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing started") {
		t.Errorf("a dry run must say it did nothing:\n%s", out)
	}
}
