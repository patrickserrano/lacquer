package console

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
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
	outLaunch, err := Dispatch(rosterOf("alpha"), sessions, "alpha", "task", Tmux, true)
	out := outLaunch.Output
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
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "alpha", Path: repo}}}
	bgLaunch, err := Dispatch(roster, nil, "alpha", "task", Background, true)
	bg := bgLaunch.Output
	if err != nil {
		t.Fatal(err)
	}
	// claude has no --cwd flag; the working directory is set via exec.Cmd.Dir,
	// not an argument, so the display line shows it as a `cd` prefix instead
	// -- into a worktree of its own, never the checkout.
	//
	// --dangerously-skip-permissions bypasses the permission-PROMPT layer only
	// -- it has nothing to do with the sandbox, a separate execution-level
	// restriction. Without also disabling the sandbox, a bg session still runs
	// every Bash command sandboxed with no one to grant the extra access it
	// needs, and git add/commit/push/checkout -b/worktree remove all get
	// silently denied -- indistinguishable from success until the operator
	// reads the job's own transcript.
	if !strings.Contains(bg, "(cd "+filepath.Join(repo, ".claude", "worktrees", "dispatch-")) ||
		!strings.Contains(bg, ` && claude --bg --dangerously-skip-permissions --settings {"sandbox":{"enabled":false}} --model sonnet task)`) {
		t.Errorf("bg mode must launch a background agent in a worktree of its own with the sandbox disabled, not just permission prompts skipped:\n%s", bg)
	}
	if strings.Contains(bg, "(cd "+repo+" &&") {
		t.Errorf("bg mode must not run in the checkout:\n%s", bg)
	}
	tmLaunch, err := Dispatch(rosterOf("alpha"), nil, "alpha", "task", Tmux, true)
	tm := tmLaunch.Output
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tm, `tmux new-session -d -s alpha -c /w/alpha claude --dangerously-skip-permissions --settings {"sandbox":{"enabled":false}} --model sonnet task`) {
		t.Errorf("tmux mode must start a detached session in the checkout, with bypass permissions:\n%s", tm)
	}
	if strings.Contains(tm, " -A ") {
		t.Errorf("-A attaches an existing session, which fails with no terminal even alongside -d:\n%s", tm)
	}
}

// Dry run is the guard that makes the two modes safe to explore.
func TestDryRunStartsNothing(t *testing.T) {
	outLaunch, err := Dispatch(rosterOf("alpha"), nil, "alpha", "task", Tmux, true)
	out := outLaunch.Output
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing started") {
		t.Errorf("a dry run must say it did nothing:\n%s", out)
	}
}

// A retired project has no findings, so its summary would read "clear" — the
// same word a healthy project gets. That is the one row where "clear" means
// "nobody is looking at this" rather than "this is fine", so it has to say so.
func TestRetiredRowDoesNotReadAsClear(t *testing.T) {
	got := summary(Row{Name: "atlas", Retired: true})
	for _, s := range got {
		if s == "clear" {
			t.Fatalf("retired project summarised as %q — indistinguishable from a healthy one: %v", "clear", got)
		}
	}
	if len(got) == 0 || got[0] != "retired" {
		t.Errorf("retired not surfaced first in the summary: %v", got)
	}
}

func TestLiveRowStillReadsAsClear(t *testing.T) {
	got := summary(Row{Name: "kit"})
	if len(got) != 1 || got[0] != "clear" {
		t.Errorf("a live project with nothing to report should still read clear, got %v", got)
	}
}

// MUTATION 3: make a missing inbox file a hard error instead of Unavailable,
// and this fails. Gather must degrade exactly the way it already does for
// `gh` and `claude agents` -- see console.go's package doc, "A MISSING TOOL
// DEGRADES, IT DOES NOT FAIL" -- rather than propagating a Go error out of a
// function whose whole contract is that it never returns one.
func TestGatherDegradesOnAnUnreadableInbox(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	res := Gather("", fleet.Roster{}, time.Now(), missing)
	var found bool
	for _, u := range res.Unavailable {
		if strings.Contains(u, "inbox") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an inbox entry in Unavailable, got: %v", res.Unavailable)
	}
	if len(res.Actions) != 0 || len(res.Unread) != 0 {
		t.Errorf("an unreadable inbox must yield no entries, got actions=%v unread=%v", res.Actions, res.Unread)
	}
}

// An inbox path that is simply never configured (--inbox unset) is a
// different case from one that is configured but unreadable: it must NOT
// appear in Unavailable at all, matching how an unset --sessions/--roles
// simply means that feature is off, not broken.
func TestGatherWithNoInboxConfiguredIsSilentAboutIt(t *testing.T) {
	res := Gather("", fleet.Roster{}, time.Now(), "")
	for _, u := range res.Unavailable {
		if strings.Contains(u, "inbox") {
			t.Fatalf("an unconfigured --inbox must not be reported as unavailable, got: %v", res.Unavailable)
		}
	}
}

// Gather must actually surface open inbox entries, split by type, and leave
// resolved ones out.
func TestGatherPopulatesActionsAndUnreadFromTheInboxFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "inbox.jsonl")
	if _, err := inbox.Add(p, inbox.Entry{Type: inbox.Action, Title: "decide the eval gate"}); err != nil {
		t.Fatal(err)
	}
	unread, err := inbox.Add(p, inbox.Entry{Type: inbox.Unread, Title: "cost analysis done"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := inbox.Add(p, inbox.Entry{Type: inbox.Unread, Title: "already handled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Resolve(p, resolved.ID); err != nil {
		t.Fatal(err)
	}

	res := Gather("", fleet.Roster{}, time.Now(), p)
	if len(res.Actions) != 1 || res.Actions[0].Title != "decide the eval gate" {
		t.Fatalf("Actions = %+v, want exactly the one open action entry", res.Actions)
	}
	if len(res.Unread) != 1 || res.Unread[0].ID != unread.ID {
		t.Fatalf("Unread = %+v, want exactly the one open unread entry (the resolved one must be excluded)", res.Unread)
	}
}

// MUTATION 4: remove the ACTION-before-projects ordering in render, and this
// fails. A blocked decision outranks every project row, so it must render
// first — burying it below a page of project rows is how it gets scrolled
// past.
func TestActionSectionRendersBeforeProjectRows(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, Result{
		Rows: []Row{{Name: "zzz-last-project"}},
		Actions: []inbox.Entry{
			{ID: "abc123", Type: inbox.Action, Title: "decide the eval gate"},
		},
	})
	s := buf.String()
	actionIdx := strings.Index(s, "ACTION")
	projectIdx := strings.Index(s, "zzz-last-project")
	if actionIdx == -1 {
		t.Fatalf("ACTION section did not render at all:\n%s", s)
	}
	if projectIdx == -1 {
		t.Fatalf("the project row did not render at all:\n%s", s)
	}
	if actionIdx > projectIdx {
		t.Fatalf("ACTION section must render before project rows, got:\n%s", s)
	}
	if !strings.Contains(s, "decide the eval gate") {
		t.Errorf("the action's title is missing from the rendered output:\n%s", s)
	}
}

// UNREAD must render too (not just ACTION), and a screen with neither must
// print no inbox sections at all -- an empty "ACTION"/"UNREAD" header with
// nothing under it is the exact "looks like it ran, actually found nothing to
// check" shape this repo's CLAUDE.md warns about.
func TestUnreadSectionRendersAndEmptySectionsStaySilent(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, Result{
		Rows:   []Row{{Name: "p"}},
		Unread: []inbox.Entry{{ID: "def456", Type: inbox.Unread, Title: "cost analysis done"}},
	})
	s := buf.String()
	if !strings.Contains(s, "UNREAD") || !strings.Contains(s, "cost analysis done") {
		t.Fatalf("UNREAD section is missing its entry:\n%s", s)
	}
	if strings.Contains(s, "ACTION") {
		t.Fatalf("no ACTION entries were given; the header must not appear:\n%s", s)
	}

	buf.Reset()
	Text(&buf, Result{Rows: []Row{{Name: "p"}}})
	s = buf.String()
	if strings.Contains(s, "ACTION") || strings.Contains(s, "UNREAD") {
		t.Fatalf("neither section has entries; no header should print at all:\n%s", s)
	}
}

// The bottom summary line must count actions/unread too, not just leave the
// reader to count rendered entries by hand.
func TestSummaryLineCountsActionsAndUnread(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, Result{
		Rows:    []Row{{Name: "p"}},
		Actions: []inbox.Entry{{ID: "a1", Type: inbox.Action, Title: "x"}},
		Unread:  []inbox.Entry{{ID: "u1", Type: inbox.Unread, Title: "y"}, {ID: "u2", Type: inbox.Unread, Title: "z"}},
	})
	s := buf.String()
	if !strings.Contains(s, "1 action(s)") {
		t.Errorf("summary line is missing the action count:\n%s", s)
	}
	if !strings.Contains(s, "2 unread") {
		t.Errorf("summary line is missing the unread count:\n%s", s)
	}
}
