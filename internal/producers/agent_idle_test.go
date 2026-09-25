package producers

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

const sid = "e8bfa25b-3acb-41b2-a2eb-7cba69e5454e"

// idleOpts is a background session whose name and project resolve.
func idleOpts(t *testing.T) AgentIdleOptions {
	t.Helper()
	return AgentIdleOptions{
		InboxPath: filepath.Join(t.TempDir(), "inbox.jsonl"),
		JobDir:    "/home/op/.claude/jobs/e8bfa25b",
		Name:      func(id string) (string, error) { return "ic-widgets", nil },
		Project:   func(cwd string) string { return "widgets" },
	}
}

func stop(msg string) StopHook {
	return StopHook{SessionID: sid, CWD: "/work/widgets/.claude/worktrees/x", HookEventName: "Stop", LastAssistantMessage: msg}
}

func mustOpen(t *testing.T, path string) []inbox.Entry {
	t.Helper()
	open, _, err := inbox.ListOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

func TestAgentIdleWithoutJobDirWritesNothing(t *testing.T) {
	o := idleOpts(t)
	o.JobDir = ""
	if _, added, _, err := AgentIdle(o, stop("done")); err != nil || added {
		t.Fatalf("added=%v err=%v: an interactive session must never reach the inbox", added, err)
	}
	if n := len(mustOpenOrNone(t, o.InboxPath)); n != 0 {
		t.Fatalf("inbox has %d entries", n)
	}
}

func mustOpenOrNone(t *testing.T, path string) []inbox.Entry {
	t.Helper()
	open, _, err := inbox.ListOpen(path)
	if err != nil {
		return nil
	}
	return open
}

func TestAgentIdleBackgroundSessionWritesOneEntry(t *testing.T) {
	o := idleOpts(t)
	e, added, warn, err := AgentIdle(o, stop("Opened PR #5.\nSecond line.\n"))
	if err != nil || !added || warn != "" {
		t.Fatalf("added=%v warn=%q err=%v", added, warn, err)
	}
	if e.Type != inbox.Unread {
		t.Errorf("type %q, want unread", e.Type)
	}
	if want := "ic-widgets is idle in widgets: Opened PR #5."; e.Title != want {
		t.Errorf("title %q, want %q", e.Title, want)
	}
	if e.Ref != "session:"+sid {
		t.Errorf("ref %q", e.Ref)
	}
	if e.Project != "widgets" {
		t.Errorf("project %q", e.Project)
	}
	if !strings.Contains(e.Body, "Second line.") || !strings.Contains(e.Body, "cwd: /work/widgets/.claude/worktrees/x") {
		t.Errorf("body should carry the whole message and the cwd, got %q", e.Body)
	}
	if got := mustOpen(t, o.InboxPath); len(got) != 1 {
		t.Fatalf("%d open entries, want 1", len(got))
	}
}

func TestAgentIdleSecondIdleWhileOpenAddsNothing(t *testing.T) {
	o := idleOpts(t)
	if _, added, _, err := AgentIdle(o, stop("first")); err != nil || !added {
		t.Fatalf("first: added=%v err=%v", added, err)
	}
	if _, added, _, err := AgentIdle(o, stop("second")); err != nil || added {
		t.Fatalf("second: added=%v err=%v; at most one open entry per session", added, err)
	}
	if got := mustOpen(t, o.InboxPath); len(got) != 1 {
		t.Fatalf("%d open entries, want 1", len(got))
	}
}

func TestAgentIdleAfterResolveWritesAgain(t *testing.T) {
	o := idleOpts(t)
	first, _, _, err := AgentIdle(o, stop("first"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Resolve(o.InboxPath, first.ID); err != nil {
		t.Fatal(err)
	}
	e, added, _, err := AgentIdle(o, stop("second"))
	if err != nil || !added {
		t.Fatalf("added=%v err=%v: a resolved entry must not block the next idle", added, err)
	}
	if !strings.HasSuffix(e.Title, "second") {
		t.Errorf("title %q", e.Title)
	}
}

func TestAgentIdleOtherSessionsAreNotDeduped(t *testing.T) {
	o := idleOpts(t)
	AgentIdle(o, stop("a"))
	other := stop("b")
	other.SessionID = "0000aaaa-0000-0000-0000-000000000000"
	if _, added, _, err := AgentIdle(o, other); err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
}

func TestAgentIdleSkips(t *testing.T) {
	cases := map[string]func(*StopHook){
		"stop_hook_active true (a Stop hook already continued the turn)": func(h *StopHook) { h.StopHookActive = true },
		"no last message":    func(h *StopHook) { h.LastAssistantMessage = "" },
		"blank last message": func(h *StopHook) { h.LastAssistantMessage = " \n\t\n" },
		"no session id":      func(h *StopHook) { h.SessionID = "" },
		"another hook event": func(h *StopHook) { h.HookEventName = "PreToolUse" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := idleOpts(t)
			h := stop("done")
			mutate(&h)
			if _, added, _, err := AgentIdle(o, h); err != nil || added {
				t.Fatalf("added=%v err=%v", added, err)
			}
			if n := len(mustOpenOrNone(t, o.InboxPath)); n != 0 {
				t.Fatalf("inbox has %d entries", n)
			}
		})
	}
}

// The payload a real `claude --bg` session sent (claude 2.1.282): an ordinary
// turn end has stop_hook_active FALSE and the message present.
func TestAgentIdleRealPayloadShape(t *testing.T) {
	h, err := ParseStopHook([]byte(`{"session_id":"` + sid + `","cwd":"/w/p","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Hi!","background_tasks":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, added, _, err := AgentIdle(idleOpts(t), h); err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
}

func TestAgentIdleNameAndProjectFallbacks(t *testing.T) {
	o := idleOpts(t)
	o.Name = func(string) (string, error) { return "", errors.New("claude agents timed out") }
	o.Project = nil
	e, added, warn, err := AgentIdle(o, stop("done"))
	if err != nil || !added {
		t.Fatalf("added=%v err=%v", added, err)
	}
	if want := "e8bfa25b is idle in x: done"; e.Title != want {
		t.Errorf("title %q, want %q", e.Title, want)
	}
	if !strings.Contains(warn, "timed out") {
		t.Errorf("a failed name lookup should be reported, got %q", warn)
	}
}

func TestAgentIdleCapsTitleAndBodyOnRuneBoundaries(t *testing.T) {
	o := idleOpts(t)
	msg := strings.Repeat("é", 150) + "\n" + strings.Repeat("ü", 3000)
	e, _, _, err := AgentIdle(o, stop(msg))
	if err != nil {
		t.Fatal(err)
	}
	prefix := "ic-widgets is idle in widgets: "
	if got := []rune(strings.TrimPrefix(e.Title, prefix)); len(got) != 101 || got[100] != '…' {
		t.Errorf("title message is %d runes, want 100 plus an ellipsis", len(got))
	}
	if len(e.Body) > bodyMessageMax+200 {
		t.Errorf("body is %d bytes", len(e.Body))
	}
	if strings.ContainsRune(e.Body, '�') {
		t.Error("body cut a multi-byte character in half")
	}
}

func TestAgentIdleUnwritableInboxIsAnError(t *testing.T) {
	o := idleOpts(t)
	o.InboxPath = t.TempDir() // a directory
	if _, added, _, err := AgentIdle(o, stop("done")); err == nil || added {
		t.Fatalf("added=%v err=%v, want an error", added, err)
	}
}

func TestParseStopHookRejectsGarbage(t *testing.T) {
	if _, err := ParseStopHook([]byte("not json")); err == nil {
		t.Fatal("want an error")
	}
}
