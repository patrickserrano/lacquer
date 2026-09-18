package inbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func path(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "inbox.jsonl")
}

// Round-trip: Add, then ReadAll must see exactly what was added, with an ID
// and CreatedAt the caller never supplied.
func TestAddThenReadAllRoundTrips(t *testing.T) {
	p := path(t)
	got, err := Add(p, Entry{Type: Action, Title: "ship the gizmo eval as blocking?", Ref: "#374", Project: "glowroot-widget-co"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatal("Add did not assign an id")
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("Add did not assign CreatedAt")
	}

	entries, malformed, err := ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (a fixture that reads zero entries proves nothing)", len(entries))
	}
	if entries[0].ID != got.ID || entries[0].Title != "ship the gizmo eval as blocking?" || entries[0].Ref != "#374" || entries[0].Project != "glowroot-widget-co" {
		t.Errorf("round-tripped entry does not match what was added: %+v", entries[0])
	}
	if !entries[0].Open() {
		t.Errorf("a freshly added entry must be open")
	}
}

func TestAddRejectsUnknownType(t *testing.T) {
	if _, err := Add(path(t), Entry{Type: "wherever", Title: "x"}); err == nil {
		t.Fatal("expected rejection of an unknown type")
	}
}

func TestAddRejectsEmptyTitle(t *testing.T) {
	if _, err := Add(path(t), Entry{Type: Action, Title: "   "}); err == nil {
		t.Fatal("expected rejection of a blank title")
	}
}

// MUTATION 2 (CLAUDE.md rule 2): drop the malformed-line tolerance and this
// must fail. A truncated line (an Add interrupted mid-write, or hand-edited
// garbage) must not sink visibility into every entry recorded around it.
func TestMalformedLineIsSkippedAndCounted(t *testing.T) {
	p := path(t)
	good1, err := json.Marshal(Entry{ID: "aaaa111111", Type: Action, Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	good2, err := json.Marshal(Entry{ID: "bbbb222222", Type: Unread, Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	content := string(good1) + "\n" + `{"id":"truncated-garb` + "\n" + string(good2) + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, malformed, err := ReadAll(p)
	if err != nil {
		t.Fatalf("a malformed LINE must not fail the whole read: %v", err)
	}
	if malformed != 1 {
		t.Fatalf("malformed = %d, want 1", malformed)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d good entries, want 2 -- the malformed line must not swallow its neighbors", len(entries))
	}
	ids := map[string]bool{entries[0].ID: true, entries[1].ID: true}
	if !ids["aaaa111111"] || !ids["bbbb222222"] {
		t.Errorf("wrong entries survived: %+v", entries)
	}
}

// MUTATION 3: make a missing inbox file a hard error (a panic, or something
// that takes the whole console down) instead of a reported, recoverable
// error, and this fails. ReadAll returning a plain error here is exactly what
// lets console.Gather fold it into Unavailable rather than crashing -- see
// inbox.go's ReadAll doc comment for why this differs from
// console/record.go's ReadRecords (which treats a missing sessions file as
// "nothing dispatched yet", not an error).
func TestReadAllOnMissingFileReturnsAnError(t *testing.T) {
	_, _, err := ReadAll(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("expected an error for a missing inbox file, got nil -- a caller cannot distinguish 'never created' from 'misconfigured path' without one")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected a wrapped os.IsNotExist error, got: %v", err)
	}
}

func TestListOpenExcludesResolvedEntries(t *testing.T) {
	p := path(t)
	open, err := Add(p, Entry{Type: Action, Title: "open one"})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := Add(p, Entry{Type: Unread, Title: "closed one"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(p, closed.ID); err != nil {
		t.Fatal(err)
	}

	got, _, err := ListOpen(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d open entries, want 1: %+v", len(got), got)
	}
	if got[0].ID != open.ID {
		t.Errorf("wrong entry survived the filter: %+v", got[0])
	}
}

func TestResolveMarksTheEntryResolved(t *testing.T) {
	p := path(t)
	e, err := Add(p, Entry{Type: Action, Title: "decide the thing"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(p, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Open() {
		t.Fatal("Resolve's own return value still reads as open")
	}

	all, _, err := ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d entries after resolve, want 1 -- resolve must rewrite in place, not append a second copy", len(all))
	}
	if all[0].Open() {
		t.Fatal("the entry on disk still reads as open after Resolve")
	}
}

func TestResolveRejectsUnknownID(t *testing.T) {
	p := path(t)
	if _, err := Add(p, Entry{Type: Action, Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(p, "nonexistent"); err == nil {
		t.Fatal("expected rejection of an unknown id")
	}
}

func TestResolveRejectsAlreadyResolved(t *testing.T) {
	p := path(t)
	e, err := Add(p, Entry{Type: Action, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(p, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(p, e.ID); err == nil {
		t.Fatal("expected rejection of a double resolve")
	}
}

// MUTATION 5: append without O_APPEND + a single Write (e.g. read-modify-write
// the whole file per Add, or write in two syscalls) and this must fail under
// `go test -race`, which this package is required to stay under -- see
// .github/workflows/ci.yml's race-set guard and CLAUDE.md rule 2.
//
// It does not just assert no data race; it asserts every line SURVIVES and
// PARSES and every id is UNIQUE, so a mutation that serializes writes but
// drops or corrupts one under contention still fails this test even if it
// happens not to trip the race detector on a given run.
func TestConcurrentAddsProduceNoTornLines(t *testing.T) {
	p := path(t)
	const n = 50

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			typ := Action
			if i%2 == 0 {
				typ = Unread
			}
			if _, err := Add(p, Entry{Type: typ, Title: "concurrent entry"}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("Add failed under concurrency: %v", err)
	}

	entries, malformed, err := ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0 -- a torn line under concurrent Add is exactly the failure this test exists to catch", malformed)
	}
	if len(entries) != n {
		t.Fatalf("got %d entries, want %d -- a lost or duplicated write under concurrency", len(entries), n)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.ID == "" {
			t.Fatal("an entry survived with no id")
		}
		if seen[e.ID] {
			t.Fatalf("duplicate id %q -- ids must be unique even when many goroutines mint them at once", e.ID)
		}
		seen[e.ID] = true
	}
}

// A fixture with an empty raw string must be treated as no line at all, not a
// malformed one -- trailing newlines are routine, not corruption.
func TestBlankLinesAreNotCountedMalformed(t *testing.T) {
	p := path(t)
	if err := os.WriteFile(p, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, malformed, err := ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if malformed != 0 || len(entries) != 0 {
		t.Errorf("got entries=%d malformed=%d, want 0 and 0", len(entries), malformed)
	}
}
