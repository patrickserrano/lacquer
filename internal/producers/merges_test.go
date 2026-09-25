package producers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func roster(pairs ...string) fleet.Roster {
	var r fleet.Roster
	for i := 0; i+1 < len(pairs); i += 2 {
		r.Project = append(r.Project, fleet.Entry{Name: pairs[i], Repo: pairs[i+1]})
	}
	return r
}

// fakeGH answers `gh pr list` per repository from a script and records the calls.
type fakeGH struct {
	mu    sync.Mutex
	reply map[string]string // repo -> JSON
	err   map[string]error
	calls []string
}

func (f *fakeGH) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	repo := ""
	for i, a := range args {
		if a == "-R" {
			repo = args[i+1]
		}
	}
	if e := f.err[repo]; e != nil {
		return nil, e
	}
	return []byte(f.reply[repo]), nil
}

func pr(n int, title, mergedAt string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/acme/widgets/pull/%d","mergedAt":%q}`, n, title, n, mergedAt)
}

func newInbox(t *testing.T) string { return filepath.Join(t.TempDir(), "inbox.jsonl") }

func entries(t *testing.T, path string) []inbox.Entry {
	t.Helper()
	e, _, err := inbox.ReadAll(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return e
}

func harvest(path string, r fleet.Roster, now time.Time, g *fakeGH) HarvestResult {
	return HarvestMerges(HarvestOptions{InboxPath: path, Roster: r, Now: now, Run: g.run})
}

func TestFirstRunSetsTheCursorAndBackfillsNothing(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{"acme/widgets": "[" + pr(1, "ancient", "2025-01-01T00:00:00Z") + "]"}}
	res := harvest(path, roster("widgets", "acme/widgets"), t0, g)

	if len(res.Added) != 0 || len(entries(t, path)) != 0 {
		t.Fatalf("a first run must backfill nothing, added %+v", res.Added)
	}
	if len(g.calls) != 0 {
		t.Errorf("a first sighting needs no gh call, made %v", g.calls)
	}
	cur, err := readCursors(filepath.Join(filepath.Dir(path), CursorFile))
	if err != nil || !cur.Repos["acme/widgets"].Equal(t0) {
		t.Fatalf("cursor = %+v (%v), want acme/widgets at %s", cur, err, t0)
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "not backfilled") {
		t.Errorf("the first look must be said out loud: %v", res.Notes)
	}
}

func TestALaterRunAddsExactlyTheNewMerges(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{}}
	r := roster("widgets-project", "acme/widgets")
	harvest(path, r, t0, g) // primes at t0

	later := t0.Add(time.Hour)
	g.reply["acme/widgets"] = "[" + pr(7, "fix the thing", "2026-09-24T12:30:00Z") + "," + pr(8, "add the other", "2026-09-24T12:45:00Z") + "]"
	res := harvest(path, r, later, g)

	if len(res.Added) != 2 {
		t.Fatalf("added %d, want 2: %+v (unavailable %v)", len(res.Added), res.Added, res.Unavailable)
	}
	got := entries(t, path)
	if len(got) != 2 {
		t.Fatalf("inbox holds %d entries, want 2", len(got))
	}
	e := got[0]
	if e.Type != inbox.Unread || e.Title != "acme/widgets#7 merged: fix the thing" || e.Ref != "https://github.com/acme/widgets/pull/7" || e.Project != "widgets-project" {
		t.Errorf("entry = %+v", e)
	}
	if !strings.Contains(g.calls[0], "merged:>="+t0.Format(time.RFC3339)) || !strings.Contains(g.calls[0], "--state merged") {
		t.Errorf("the search must start at the cursor: %s", g.calls[0])
	}
}

func TestARerunAddsNothing(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{}}
	r := roster("w", "acme/widgets")
	harvest(path, r, t0, g)
	g.reply["acme/widgets"] = "[" + pr(7, "fix", "2026-09-24T12:30:00Z") + "]"
	harvest(path, r, t0.Add(time.Hour), g)

	res := harvest(path, r, t0.Add(time.Hour+time.Minute), g) // gh still returns the same merge
	if len(res.Added) != 0 || len(entries(t, path)) != 1 {
		t.Fatalf("a rerun must add nothing; added %+v, inbox has %d", res.Added, len(entries(t, path)))
	}
}

// Two consoles with the same stale cursor both see the merge; only one entry lands
// and, once resolved, it does not come back.
func TestTwoConsolesAndResolvedEntriesDoNotDuplicate(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{}}
	r := roster("w", "acme/widgets")
	harvest(path, r, t0, g)
	g.reply["acme/widgets"] = "[" + pr(7, "fix", "2026-09-24T12:30:00Z") + "]"

	later := t0.Add(time.Hour)
	harvest(path, r, later, g)
	harvest(path, r, later, g)
	if n := len(entries(t, path)); n != 1 {
		t.Fatalf("two harvests at the same cursor made %d entries, want 1", n)
	}

	// Rewind the cursor, as a second console holding a stale one would.
	if err := writeCursors(filepath.Join(filepath.Dir(path), CursorFile), cursors{Repos: map[string]time.Time{"acme/widgets": t0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Resolve(path, entries(t, path)[0].ID); err != nil {
		t.Fatal(err)
	}
	harvest(path, r, later, g)
	if n := len(entries(t, path)); n != 1 {
		t.Fatalf("a resolved merge came back: %d entries", n)
	}
}

func TestAGhErrorIsUnavailableNotZeroMerges(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{}, err: map[string]error{}}
	r := roster("w", "acme/widgets", "o", "acme/other")
	harvest(path, r, t0, g)

	g.err["acme/widgets"] = errors.New("gh pr list: HTTP 502")
	g.reply["acme/other"] = "[]"
	res := harvest(path, r, t0.Add(time.Hour), g)

	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "acme/widgets") || !strings.Contains(res.Unavailable[0], "HTTP 502") {
		t.Fatalf("a failed gh must be reported as unavailable, got %v", res.Unavailable)
	}
	cur, _ := readCursors(filepath.Join(filepath.Dir(path), CursorFile))
	if !cur.Repos["acme/widgets"].Equal(t0) {
		t.Errorf("the cursor of a repo that failed must not advance, is %s", cur.Repos["acme/widgets"])
	}
	// And it recovers: the merge is found once gh works again.
	g.err = map[string]error{}
	g.reply["acme/widgets"] = "[" + pr(9, "late", "2026-09-24T12:40:00Z") + "]"
	res = harvest(path, r, t0.Add(2*time.Hour), g)
	if len(res.Added) != 1 {
		t.Fatalf("the merge behind a failed look must be found next time, added %+v", res.Added)
	}
}

func TestUnreadableGhOutputIsUnavailable(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{}}
	r := roster("w", "acme/widgets")
	harvest(path, r, t0, g)
	g.reply["acme/widgets"] = "<html>rate limited</html>"
	res := harvest(path, r, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 {
		t.Fatalf("garbage from gh must be unavailable, got %v", res.Unavailable)
	}
}

func TestNoRosterSaysSo(t *testing.T) {
	g := &fakeGH{}
	res := harvest(newInbox(t), fleet.Roster{}, t0, g)
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "no roster") {
		t.Fatalf("no roster must be said out loud, got notes %v", res.Notes)
	}
	if len(g.calls) != 0 {
		t.Errorf("nothing to harvest, but gh was called: %v", g.calls)
	}
}

func TestACorruptCursorIsUnavailableNotAFlood(t *testing.T) {
	path := newInbox(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), CursorFile), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := harvest(path, roster("w", "acme/widgets"), t0, &fakeGH{})
	if len(res.Unavailable) != 1 || len(res.Added) != 0 {
		t.Fatalf("unavailable %v added %v", res.Unavailable, res.Added)
	}
}

// A repo that joins the roster later is primed on its own; the others are not
// disturbed.
func TestANewRepoIsPrimedWithoutBackfill(t *testing.T) {
	path := newInbox(t)
	g := &fakeGH{reply: map[string]string{"acme/other": "[" + pr(1, "old", "2020-01-01T00:00:00Z") + "]"}}
	harvest(path, roster("w", "acme/widgets"), t0, g)
	res := harvest(path, roster("w", "acme/widgets", "o", "acme/other"), t0.Add(time.Hour), g)
	if len(res.Added) != 0 {
		t.Fatalf("a repo's first look backfilled %+v", res.Added)
	}
	cur, _ := readCursors(filepath.Join(filepath.Dir(path), CursorFile))
	if _, ok := cur.Repos["acme/other"]; !ok {
		t.Error("the new repo has no cursor")
	}
}
