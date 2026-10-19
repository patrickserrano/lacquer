package producers

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

const (
	endedID  = "11111111-aaaa-4bbb-8ccc-000000000001"
	liveID   = "22222222-aaaa-4bbb-8ccc-000000000002"
	otherLiv = "33333333-aaaa-4bbb-8ccc-000000000003"
)

func live(ids ...string) func() ([]string, error) {
	return func() ([]string, error) { return ids, nil }
}

func addEntry(t *testing.T, path string, e inbox.Entry) inbox.Entry {
	t.Helper()
	got, err := inbox.Add(path, e)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func inboxPath(t *testing.T) string { return filepath.Join(t.TempDir(), "inbox.jsonl") }

func openIDs(t *testing.T, path string) map[string]bool {
	t.Helper()
	m := map[string]bool{}
	for _, e := range mustOpenOrNone(t, path) {
		m[e.ID] = true
	}
	return m
}

func TestAutoClearResolvesIdleEntryOfEndedSession(t *testing.T) {
	p := inboxPath(t)
	e := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + endedID})

	res := AutoClear(AutoClearOptions{InboxPath: p, Live: live(otherLiv)})

	if len(res.Entries) != 1 || res.Entries[0].ID != e.ID {
		t.Fatalf("entries = %+v, want exactly %s", res.Entries, e.ID)
	}
	if openIDs(t, p)[e.ID] {
		t.Fatal("the entry for an ended session is still open")
	}
	all, _, _ := inbox.ReadAll(p)
	if !strings.Contains(all[0].ResolvedNote, "session ended") {
		t.Fatalf("resolved note = %q, want it to say the session ended", all[0].ResolvedNote)
	}
}

func TestAutoClearKeepsEntryOfListedSession(t *testing.T) {
	p := inboxPath(t)
	e := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + liveID})

	res := AutoClear(AutoClearOptions{InboxPath: p, Live: live(liveID)})

	if len(res.Entries) != 0 || !openIDs(t, p)[e.ID] {
		t.Fatalf("a still-listed session's entry was touched: %+v", res.Entries)
	}
}

// An ACTION that shares the session ref is a decision someone owes the
// operator. It must survive its session ending.
func TestAutoClearNeverResolvesAnActionSharingTheSessionRef(t *testing.T) {
	p := inboxPath(t)
	action := addEntry(t, p, inbox.Entry{Type: inbox.Action, Title: "needs a decision", Ref: "session:" + endedID})
	idle := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + endedID})

	res := AutoClear(AutoClearOptions{InboxPath: p, Live: live(otherLiv)})

	open := openIDs(t, p)
	if !open[action.ID] {
		t.Fatal("an ACTION entry was resolved because its session ended")
	}
	if open[idle.ID] || len(res.Entries) != 1 {
		t.Fatalf("the idle entry beside it should still clear: entries=%+v", res.Entries)
	}
}

func TestAutoClearKeepsUnreadWithoutASessionRef(t *testing.T) {
	p := inboxPath(t)
	noRef := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle in widgets: done"})
	other := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "merged", Ref: "#12"})
	bare := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "bare prefix", Ref: "session:"})

	res := AutoClear(AutoClearOptions{InboxPath: p, Live: live(otherLiv)})

	open := openIDs(t, p)
	if len(res.Entries) != 0 || !open[noRef.ID] || !open[other.ID] || !open[bare.ID] {
		t.Fatalf("an entry without a usable session ref was resolved: %+v", res.Entries)
	}
}

func TestAutoClearLeavesAnAlreadyResolvedEntryUntouched(t *testing.T) {
	p := inboxPath(t)
	e := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + endedID})
	if _, err := inbox.Resolve(p, e.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)

	res := AutoClear(AutoClearOptions{InboxPath: p, Live: live(otherLiv)})

	after, _ := os.ReadFile(p)
	if len(res.Entries) != 0 || !bytes.Equal(before, after) {
		t.Fatalf("a resolved entry was rewritten: entries=%+v", res.Entries)
	}
}

func TestAutoClearResolvesNothingWhenTheSessionListCannotBeRead(t *testing.T) {
	cases := map[string]func() ([]string, error){
		"command failed": func() ([]string, error) { return nil, errors.New("claude: not found") },
		// A failing call that also hands back ids must still be an error.
		"error with partial list": func() ([]string, error) { return []string{liveID}, errors.New("timed out") },
	}
	for name, list := range cases {
		t.Run(name, func(t *testing.T) {
			p := inboxPath(t)
			e := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + endedID})
			before, _ := os.ReadFile(p)

			res := AutoClear(AutoClearOptions{InboxPath: p, Live: list})

			after, _ := os.ReadFile(p)
			if len(res.Entries) != 0 || !openIDs(t, p)[e.ID] || !bytes.Equal(before, after) {
				t.Fatalf("an unreadable session list resolved something: %+v", res.Entries)
			}
			if len(res.Notes) == 0 || !strings.Contains(res.Notes[0], "resolved nothing") {
				t.Fatalf("notes = %q, want one saying nothing was resolved", res.Notes)
			}
		})
	}
}

func TestAutoClearWithoutASessionSourceResolvesNothing(t *testing.T) {
	p := inboxPath(t)
	e := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + endedID})
	res := AutoClear(AutoClearOptions{InboxPath: p})
	if len(res.Entries) != 0 || !openIDs(t, p)[e.ID] || len(res.Notes) == 0 {
		t.Fatalf("no source must mean no resolution and a note: %+v", res)
	}
}

func TestAutoClearDryRunListsWhatItWouldResolveAndChangesNothing(t *testing.T) {
	p := inboxPath(t)
	idle := addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "ic is idle", Ref: "session:" + endedID})
	addEntry(t, p, inbox.Entry{Type: inbox.Action, Title: "needs a decision", Ref: "session:" + endedID})
	addEntry(t, p, inbox.Entry{Type: inbox.Unread, Title: "still here", Ref: "session:" + liveID})
	before, _ := os.ReadFile(p)

	res := AutoClear(AutoClearOptions{InboxPath: p, Live: live(liveID), DryRun: true})

	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("a dry run changed the inbox file")
	}
	if len(res.Entries) != 1 || res.Entries[0].ID != idle.ID || res.Entries[0].Ref != "session:"+endedID {
		t.Fatalf("dry run listed %+v, want only %s", res.Entries, idle.ID)
	}
}
