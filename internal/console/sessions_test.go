package console

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

type fakeSessions struct {
	sessions []Session
	err      error
}

func (f fakeSessions) List(context.Context) ([]Session, error) { return f.sessions, f.err }

const agentsFixture = `[
 {"pid":10945,"cwd":"/work/fleet-ops","kind":"interactive","startedAt":1790115781657,"sessionId":"12648fa9-1","name":"Foxy","status":"idle"},
 {"pid":6053,"cwd":"/work/fleet-ops","kind":"interactive","startedAt":1790301580556,"sessionId":"6c2331e7-2","name":"pm-lacquer","status":"busy"},
 {"pid":15085,"id":"2a92a0cb","cwd":"/work/lacquer/.claude/worktrees/x","kind":"background","startedAt":1790301695140,"sessionId":"2a92a0cb-3","name":"ic-1","status":"busy","state":"working"}
]`

func TestParseSessionsReadsClaudeAgentsJSON(t *testing.T) {
	got, err := ParseSessions([]byte(agentsFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}
	pm := got[1]
	if pm.Name != "pm-lacquer" || pm.Kind != "interactive" || pm.Status != "busy" || pm.CWD != "/work/fleet-ops" || pm.PID != 6053 || pm.StartedAt != 1790301580556 {
		t.Errorf("fields not parsed: %+v", pm)
	}
}

// MUTATION: make ParseSessions return an empty list on bad JSON, and this fails.
func TestParseSessionsRefusesGarbageInsteadOfReturningNone(t *testing.T) {
	for _, in := range []string{"", "not json", `{"sessions":[]}`} {
		got, err := ParseSessions([]byte(in))
		if err == nil {
			t.Errorf("ParseSessions(%q) = %v, nil; unreadable output must be an error, not zero sessions", in, got)
		}
	}
}

func TestClaudeAgentsFailureIsAnErrorWithTheReason(t *testing.T) {
	src := ClaudeAgents{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New(`exec: "claude": executable file not found in $PATH`)
	}}
	_, err := src.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want the underlying reason", err)
	}
}

func TestClaudeAgentsTimesOutInsteadOfHanging(t *testing.T) {
	src := ClaudeAgents{Timeout: 20 * time.Millisecond, Run: func(ctx context.Context, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	_, err := src.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v, want a timeout message", err)
	}
}

func TestClaudeAgentsPassesAgentsJSON(t *testing.T) {
	var args []string
	src := ClaudeAgents{Run: func(_ context.Context, a ...string) ([]byte, error) { args = a; return []byte("[]"), nil }}
	if _, err := src.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "agents --json" {
		t.Errorf("args = %v", args)
	}
}

// The point of #380: a broken source must not render like a healthy empty one.
// MUTATION: render SessionsErr as an empty list and the first assertions fail;
// print the zero-sessions text for an error and the last one fails.
func TestFailedSourceRendersDifferentlyFromZeroSessions(t *testing.T) {
	now := time.Now()
	failed := Gather(Options{Now: now, Sessions: fakeSessions{err: errors.New("claude not on PATH")}})
	zero := Gather(Options{Now: now, Sessions: fakeSessions{}})

	var f, z bytes.Buffer
	Text(&f, failed)
	Text(&z, zero)

	if !strings.Contains(f.String(), "sessions: unavailable — claude not on PATH") {
		t.Errorf("failure not loud:\n%s", f.String())
	}
	if strings.Contains(f.String(), "none running") || strings.Contains(f.String(), "0 session(s)") {
		t.Errorf("a failed source must never read as zero sessions:\n%s", f.String())
	}
	if !strings.Contains(z.String(), "none running") || strings.Contains(z.String(), "unavailable") {
		t.Errorf("a healthy empty list must say so, and not claim unavailable:\n%s", z.String())
	}
	if f.String() == z.String() {
		t.Fatal("failure and zero sessions render identically")
	}
}

func TestSessionsListShowsNameKindStatusProjectCwdAge(t *testing.T) {
	sessions, err := ParseSessions([]byte(agentsFixture))
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1790301580556).Add(3*time.Hour + 5*time.Minute)
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "fleet", Path: "/work/fleet-ops"}}}
	res := Gather(Options{Roster: roster, Now: now, Sessions: fakeSessions{sessions: sessions}})

	var buf bytes.Buffer
	Text(&buf, res)
	out := buf.String()
	for _, want := range []string{"SESSIONS", "pm-lacquer", "interactive", "busy", "background", "/work/fleet-ops", "3h05m", "ic-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	var line string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "pm-lacquer") {
			line = l
		}
	}
	if !strings.Contains(line, "fleet") {
		t.Errorf("pm-lacquer's cwd is under the roster's project, its row should name it: %q", line)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "ic-1") && !strings.Contains(l, " -  ") {
			t.Errorf("a session under no roster project should show '-': %q", l)
		}
	}
}

func TestConsoleRunsWithNoRosterAndCountsInbox(t *testing.T) {
	p := filepath.Join(t.TempDir(), "inbox.jsonl")
	inbox.Add(p, inbox.Entry{Type: inbox.Action, Title: "decide"})
	inbox.Add(p, inbox.Entry{Type: inbox.Unread, Title: "done"})
	res := Gather(Options{Now: time.Now(), InboxPath: p, Sessions: fakeSessions{sessions: []Session{{Name: "a", Status: "busy"}}}})
	var buf bytes.Buffer
	Text(&buf, res)
	out := buf.String()
	if !strings.Contains(out, "1 session(s) (1 busy)") || !strings.Contains(out, "1 action(s) · 1 unread") {
		t.Errorf("summary missing counts:\n%s", out)
	}
	if !strings.Contains(out, "roster: none loaded") {
		t.Errorf("no roster should be said, not hidden:\n%s", out)
	}
}

func TestDefaultInboxThatDoesNotExistYetIsEmptyNotUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "state", "lacquer", "inbox.jsonl")
	res := Gather(Options{Now: time.Now(), InboxPath: missing, InboxDefault: true, Sessions: fakeSessions{}})
	if len(res.Unavailable) != 0 {
		t.Errorf("unexpected Unavailable: %v", res.Unavailable)
	}
	if !strings.Contains(res.InboxNote, "does not exist yet") {
		t.Errorf("the missing default file should still be said: %q", res.InboxNote)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("reading must not create the inbox file")
	}
}
