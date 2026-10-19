package inboxwatch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

func harvestEnv(t *testing.T, live func() ([]string, error)) (Env, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	return Env{
		InboxPath:    path,
		Roster:       fleet.Roster{Project: []fleet.Entry{{Name: "widgets", Path: t.TempDir(), Repo: "acme/widgets"}}},
		Now:          func() time.Time { return t0 },
		Run:          func(context.Context, ...string) ([]byte, error) { return []byte("[]"), nil },
		LiveSessions: live,
	}, path
}

// The watcher's harvest pass is where an ended session's idle entry is cleared.
func TestHarvestResolvesTheIdleEntryOfAnEndedSession(t *testing.T) {
	env, path := harvestEnv(t, func() ([]string, error) { return []string{"live-session"}, nil })
	if _, err := inbox.Add(path, inbox.Entry{Type: inbox.Unread, Title: "idle", Ref: "session:gone-session"}); err != nil {
		t.Fatal(err)
	}
	ev := env.Exec(Cmd{Kind: CmdHarvest}).(HarvestedEvent)
	if ev.Cleared != 1 {
		t.Fatalf("event %+v, want one cleared", ev)
	}
	if open, _, _ := inbox.ListOpen(path); len(open) != 0 {
		t.Fatalf("entry still open: %+v", open)
	}
}

func TestHarvestReportsAnUnreadableSessionListAndResolvesNothing(t *testing.T) {
	env, path := harvestEnv(t, func() ([]string, error) { return nil, errors.New("claude: not found") })
	if _, err := inbox.Add(path, inbox.Entry{Type: inbox.Unread, Title: "idle", Ref: "session:gone-session"}); err != nil {
		t.Fatal(err)
	}
	ev := env.Exec(Cmd{Kind: CmdHarvest}).(HarvestedEvent)
	if ev.Cleared != 0 || len(ev.Notes) != 1 || !strings.Contains(ev.Notes[0], "resolved nothing") {
		t.Fatalf("event %+v, want a note that nothing was resolved", ev)
	}
	if open, _, _ := inbox.ListOpen(path); len(open) != 1 {
		t.Fatalf("entry resolved on an unreadable list: %+v", open)
	}
	p, _ := model(t, cfgReply, 80, 10).Update(ev)
	if !strings.Contains(p.(Model).Note, "resolved nothing") {
		t.Fatalf("the watcher's note does not say it: %q", p.(Model).Note)
	}
}

func TestHarvestWithoutASessionSourceLeavesIdleEntriesAlone(t *testing.T) {
	env, path := harvestEnv(t, nil)
	if _, err := inbox.Add(path, inbox.Entry{Type: inbox.Unread, Title: "idle", Ref: "session:gone-session"}); err != nil {
		t.Fatal(err)
	}
	ev := env.Exec(Cmd{Kind: CmdHarvest}).(HarvestedEvent)
	if ev.Cleared != 0 || len(ev.Notes) != 0 {
		t.Fatalf("event %+v", ev)
	}
	if open, _, _ := inbox.ListOpen(path); len(open) != 1 {
		t.Fatal("entry resolved with no session source")
	}
}
