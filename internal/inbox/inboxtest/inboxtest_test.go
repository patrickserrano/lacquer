package inboxtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// The guard must actually veto a write to the real default path, and leave a
// temp file alone. Run installs it per binary, so this test installs it the same way.
func TestRunVetoesTheRealInboxAndOnlyThat(t *testing.T) {
	state := t.TempDir()
	t.Setenv("LACQUER_INBOX", "")
	t.Setenv("XDG_STATE_HOME", state)
	real := filepath.Join(state, "lacquer", "inbox.jsonl")

	code := Run(nil, func() int {
		if _, err := inbox.Add(filepath.Join(t.TempDir(), "other.jsonl"), inbox.Entry{Type: inbox.Unread, Title: "x"}); err != nil {
			t.Errorf("a temp inbox must be writable: %v", err)
		}
		if _, err := inbox.Add(real, inbox.Entry{Type: inbox.Unread, Title: "x"}); err == nil {
			t.Error("a write to the real default inbox was allowed")
		}
		return 0
	})
	if code != 1 {
		t.Errorf("Run returned %d, want 1 after an attempted real write", code)
	}
	if _, err := os.Stat(real); err == nil {
		t.Error("the real inbox file was created")
	}
}
