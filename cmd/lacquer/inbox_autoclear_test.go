package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

func stubLive(t *testing.T, ids []string, err error) {
	t.Helper()
	old := liveSessionIDs
	liveSessionIDs = func() ([]string, error) { return ids, err }
	t.Cleanup(func() { liveSessionIDs = old })
}

func TestConsoleInboxAutoclearDryRunListsAndLiveRunResolves(t *testing.T) {
	lq := realLacquer(t)
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	idle, _ := inbox.Add(path, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:gone"})
	inbox.Add(path, inbox.Entry{Type: inbox.Action, Title: "decide", Ref: "session:gone"})
	stubLive(t, []string{"alive"}, nil)
	before, _ := os.ReadFile(path)

	out, errb, code := runConsole(t, lq, []string{"--inbox", path, "inbox", "autoclear", "--dry-run"})
	after, _ := os.ReadFile(path)
	if code != 0 || string(before) != string(after) {
		t.Fatalf("dry run: code=%d stderr=%q changed=%v", code, errb, string(before) != string(after))
	}
	if !strings.Contains(out, "would resolve "+idle.ID) || !strings.Contains(out, "ref=session:gone") || strings.Contains(out, "decide") {
		t.Fatalf("dry-run listing:\n%s", out)
	}

	out, _, code = runConsole(t, lq, []string{"--inbox", path, "inbox", "autoclear"})
	if code != 0 || !strings.Contains(out, "resolved "+idle.ID) {
		t.Fatalf("live run: code=%d\n%s", code, out)
	}
	if open, _, _ := inbox.ListOpen(path); len(open) != 1 || open[0].Type != inbox.Action {
		t.Fatalf("open after live run: %+v", open)
	}
}

func TestConsoleInboxAutoclearExitsNonZeroWhenTheListIsUnreadable(t *testing.T) {
	lq := realLacquer(t)
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	inbox.Add(path, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:gone"})
	stubLive(t, nil, errors.New("claude: not found"))

	_, errb, code := runConsole(t, lq, []string{"--inbox", path, "inbox", "autoclear"})
	if code == 0 || !strings.Contains(errb, "resolved nothing") {
		t.Fatalf("code=%d stderr=%q: could-not-tell must not look like nothing-due", code, errb)
	}
	if open, _, _ := inbox.ListOpen(path); len(open) != 1 {
		t.Fatal("resolved on an unreadable list")
	}
}
