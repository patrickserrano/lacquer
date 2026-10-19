package producers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// The loosening harvest (#571): a merged PR that changed .lacquer.ratchet.toml
// has its head commit's check-run annotations read, and each
// lacquer-ratchet-loosened/v1 line becomes one UNREAD entry.

// loosenGH answers the two `pr list` looks and the `gh api` reads.
type loosenGH struct {
	mu     sync.Mutex
	list   string            // the merges look
	files  string            // the files look
	api    map[string]string // api path -> JSON
	apiErr map[string]error
	calls  []string
}

func (f *loosenGH) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	if args[0] == "api" {
		if e := f.apiErr[args[1]]; e != nil {
			return nil, e
		}
		body, ok := f.api[args[1]]
		if !ok {
			return nil, fmt.Errorf("unscripted api call %s", args[1])
		}
		return []byte(body), nil
	}
	if strings.Contains(strings.Join(args, " "), "--json url,files") {
		return []byte(f.files), nil
	}
	return []byte(f.list), nil
}

func (f *loosenGH) apiCalls() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "api ") {
			n++
		}
	}
	return n
}

const (
	head9    = "9999999999999999999999999999999999999999"
	runsPath = "repos/acme/widgets/commits/" + head9 + "/check-runs?filter=latest&per_page=100&page=1"
	// The message GitHub stores for the gate's ::notice, pinned literally: this
	// is the documented v1 format, not whatever the encoder happens to emit.
	loosenedMsg = `lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines","from":100,"to":150,"reason":"vendored parser"}`
)

func filesOf(n int, paths ...string) string {
	var fs []string
	for _, p := range paths {
		fs = append(fs, fmt.Sprintf(`{"path":%q}`, p))
	}
	return fmt.Sprintf(`{"url":"https://github.com/acme/widgets/pull/%d","files":[%s]}`, n, strings.Join(fs, ","))
}

func annotations(msgs ...string) string {
	var out []string
	for _, m := range msgs {
		out = append(out, fmt.Sprintf(`{"message":%q,"annotation_level":"notice"}`, m))
	}
	return "[" + strings.Join(out, ",") + "]"
}

// A primed harvest with PR #9 (touching the ratchet) merged, and two matrix
// legs each carrying the same loosening plus an unrelated warning.
func loosenFixture(t *testing.T) (string, *loosenGH) {
	t.Helper()
	path := newInbox(t)
	g := &loosenGH{api: map[string]string{}, apiErr: map[string]error{}}
	harvest9(path, t0, g) // primes the cursor
	// The gate-off check (#576) needs to know which paths the lock manages
	// before it can pass a merge over unread; this project has no lock, so
	// none. A merge it must read anyway (its file list is missing) leaves
	// every gate alone.
	seedManaged(t, path)
	scriptMerge(g, merge9, parent9, tree{"Sources/Parser.swift": "a\n"}, tree{"Sources/Parser.swift": "b\n"})
	g.list = "[" + mergedWithCommit(9, "loosen coverage", "2026-09-24T12:30:00Z", head9, merge9) + "]"
	g.files = "[" + filesOf(9, ".lacquer.ratchet.toml", "Sources/Parser.swift") + "]"
	g.api[runsPath] = `{"total_count":3,"check_runs":[{"id":1,"output":{"annotations_count":2}},{"id":2,"output":{"annotations_count":1}},{"id":3,"output":{"annotations_count":0}}]}`
	g.api["repos/acme/widgets/check-runs/1/annotations?per_page=100&page=1"] = annotations("Xcode 26 is deprecated", loosenedMsg)
	g.api["repos/acme/widgets/check-runs/2/annotations?per_page=100&page=1"] = annotations(loosenedMsg)
	return path, g
}

func harvest9(path string, now time.Time, g *loosenGH) HarvestResult {
	return HarvestMerges(HarvestOptions{InboxPath: path, Roster: roster("widgets", "acme/widgets"), Now: now, Run: g.run})
}

func TestHarvestRaisesAnUnreadItemForALoosening(t *testing.T) {
	path, g := loosenFixture(t)
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 0 {
		t.Fatalf("unavailable: %v", res.Unavailable)
	}
	got := entries(t, path)
	if len(got) != 2 {
		t.Fatalf("inbox holds %d entries, want the loosening and the merge: %+v", len(got), got)
	}
	l := got[0]
	if l.Type != inbox.Unread || l.Title != "acme/widgets#9 loosened ios_uncovered_lines 100→150" ||
		l.Body != "reason: vendored parser" || l.Ref != "https://github.com/acme/widgets/pull/9#ratchet-loosened-ios_uncovered_lines" || l.Project != "widgets" {
		t.Errorf("loosening entry = %+v", l)
	}
	if got[1].Ref != "https://github.com/acme/widgets/pull/9" {
		t.Errorf("the merge entry is missing or out of order: %+v", got[1])
	}
	if n := g.apiCalls(); n != 3 {
		t.Errorf("made %d api calls, want 3 (check runs, then the two runs with annotations): %v", n, g.calls)
	}

	// A rerun with the cursor rewound sees the PR again and adds nothing.
	if err := writeCursors(filepath.Join(filepath.Dir(path), CursorFile), cursors{Repos: map[string]time.Time{"acme/widgets": t0}}); err != nil {
		t.Fatal(err)
	}
	if res := harvest9(path, t0.Add(2*time.Hour), g); len(res.Added) != 0 || len(entries(t, path)) != 2 {
		t.Fatalf("a rerun added %+v", res.Added)
	}
}

func TestHarvestReadsNoAnnotationsForAMergeThatLeavesTheRatchetAlone(t *testing.T) {
	path, g := loosenFixture(t)
	g.files = "[" + filesOf(9, "Sources/Parser.swift") + "]"
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Added) != 1 || g.apiCalls() != 0 {
		t.Fatalf("added %+v with %d api calls; want the merge alone and no api call", res.Added, g.apiCalls())
	}
}

// A merge the files look did not return is checked anyway: "not listed" is not
// "did not touch the ratchet".
func TestHarvestChecksAMergeTheFilesLookMissed(t *testing.T) {
	path, g := loosenFixture(t)
	g.files = "[]"
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Added) != 2 || !strings.Contains(res.Added[0].Title, "loosened") {
		t.Fatalf("added %+v; want the loosening found anyway", res.Added)
	}
}

// A look that fails is reported, the PR is not marked done, and the next
// harvest finds the loosening.
func TestHarvestReportsAFailedLooseningLookAndRetries(t *testing.T) {
	path, g := loosenFixture(t)
	g.apiErr["repos/acme/widgets/check-runs/2/annotations?per_page=100&page=1"] = errors.New("HTTP 502")
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "acme/widgets#9") || !strings.Contains(res.Unavailable[0], "HTTP 502") {
		t.Fatalf("unavailable = %v; want the failed look named", res.Unavailable)
	}
	if n := len(entries(t, path)); n != 0 {
		t.Fatalf("a failed look still wrote %d entries", n)
	}
	delete(g.apiErr, "repos/acme/widgets/check-runs/2/annotations?per_page=100&page=1")
	if res := harvest9(path, t0.Add(2*time.Hour), g); len(res.Added) != 2 {
		t.Fatalf("the retry added %+v (unavailable %v); want the loosening and the merge", res.Added, res.Unavailable)
	}
}

func runsPage(n int) string {
	return fmt.Sprintf("repos/acme/widgets/commits/%s/check-runs?filter=latest&per_page=100&page=%d", head9, n)
}

func annPage(id, n int) string {
	return fmt.Sprintf("repos/acme/widgets/check-runs/%d/annotations?per_page=100&page=%d", id, n)
}

// checkRuns is one page of `total` runs: ids from..to, the one named annotated
// carrying `count` annotations, the rest none.
func checkRuns(total, from, to, annotated, count int) string {
	var rs []string
	for id := from; id <= to; id++ {
		n := 0
		if id == annotated {
			n = count
		}
		rs = append(rs, fmt.Sprintf(`{"id":%d,"output":{"annotations_count":%d}}`, id, n))
	}
	return fmt.Sprintf(`{"total_count":%d,"check_runs":[%s]}`, total, strings.Join(rs, ","))
}

func filler(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("warning %d", i)
	}
	return out
}

// Over 100 check runs and over 100 annotations: both are paged, and the
// loosening on the second page of the second page's run is found.
func TestHarvestPaginatesCheckRunsAndAnnotations(t *testing.T) {
	path, g := loosenFixture(t)
	g.api = map[string]string{
		runsPage(1):     checkRuns(150, 1, 100, 0, 0),
		runsPage(2):     checkRuns(150, 101, 150, 140, 120),
		annPage(140, 1): annotations(filler(100)...),
		annPage(140, 2): annotations(append(filler(19), loosenedMsg)...),
	}
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 0 || len(res.Added) != 2 || res.Added[0].Title != "acme/widgets#9 loosened ios_uncovered_lines 100→150" {
		t.Fatalf("added %+v, unavailable %v; want the loosening from page 2", res.Added, res.Unavailable)
	}
}

// pastCap asserts the permanent path: one UNREAD item naming the PR and why,
// the merge recorded, nothing unavailable, the cursor moved on, and a rerun
// adding nothing.
func pastCap(t *testing.T, path string, g *loosenGH, why string) {
	t.Helper()
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 0 {
		t.Fatalf("unavailable %v; a read no retry can fix must not hold the cursor", res.Unavailable)
	}
	if len(res.Added) != 2 || res.Added[0].Type != inbox.Unread ||
		!strings.HasPrefix(res.Added[0].Title, "couldn't read ratchet loosenings for acme/widgets#9: ") ||
		!strings.Contains(res.Added[0].Title, why) || !strings.HasSuffix(res.Added[0].Title, "; check by hand") ||
		res.Added[0].Ref != "https://github.com/acme/widgets/pull/9#ratchet-loosenings-unreadable" ||
		res.Added[1].Ref != "https://github.com/acme/widgets/pull/9" {
		t.Fatalf("added %+v; want the unreadable item, then the merge", res.Added)
	}
	cur, err := readCursors(filepath.Join(filepath.Dir(path), CursorFile))
	if err != nil || !cur.Repos["acme/widgets"].After(t0) {
		t.Fatalf("cursor %+v (%v) did not advance past %s", cur, err, t0)
	}
	// Rewound, the harvest sees the PR again and adds nothing.
	if err := writeCursors(filepath.Join(filepath.Dir(path), CursorFile), cursors{Repos: map[string]time.Time{"acme/widgets": t0}}); err != nil {
		t.Fatal(err)
	}
	if res := harvest9(path, t0.Add(2*time.Hour), g); len(res.Added) != 0 {
		t.Fatalf("a rerun added %+v", res.Added)
	}
}

func TestHarvestPastTheCheckRunPageCapWritesAnUnreadItemAndMovesOn(t *testing.T) {
	path, g := loosenFixture(t)
	g.api = map[string]string{
		runsPage(1): checkRuns(400, 1, 100, 0, 0),
		runsPage(2): checkRuns(400, 101, 200, 0, 0),
		runsPage(3): checkRuns(400, 201, 300, 0, 0),
	}
	pastCap(t, path, g, "400 check runs on "+head9+", only 300 read")
}

func TestHarvestPastTheAnnotationPageCapWritesAnUnreadItemAndMovesOn(t *testing.T) {
	path, g := loosenFixture(t)
	g.api = map[string]string{
		runsPage(1):   checkRuns(1, 7, 7, 7, 350),
		annPage(7, 1): annotations(filler(100)...),
		annPage(7, 2): annotations(filler(100)...),
		annPage(7, 3): annotations(filler(100)...),
	}
	pastCap(t, path, g, "check run 7 has 350 annotations, only 300 read")
}

// A damaged line is in GitHub for good: retrying cannot fix it.
func TestHarvestTurnsADamagedLooseningLineIntoAnUnreadItem(t *testing.T) {
	path, g := loosenFixture(t)
	g.api[annPage(2, 1)] = annotations(`lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines"`)
	pastCap(t, path, g, "malformed")
}

func TestHarvestRaisesAnUnreadItemForARemovedKey(t *testing.T) {
	path, g := loosenFixture(t)
	g.api[annPage(2, 1)] = annotations(`lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines_widget","from":7,"removed":true,"reason":"widget app retired"}`)
	res := harvest9(path, t0.Add(time.Hour), g)
	var titles []string
	for _, e := range res.Added {
		titles = append(titles, e.Title)
	}
	if !strings.Contains(strings.Join(titles, "\n"), "acme/widgets#9 loosened ios_uncovered_lines_widget 7→removed") {
		t.Fatalf("added %q (unavailable %v); want the removal as a loosening", titles, res.Unavailable)
	}
}

// The unreadable item is deduped by its ref on its own, not only through the
// merge entry that normally follows it.
func TestTheUnreadableItemIsDedupedByRef(t *testing.T) {
	path, g := loosenFixture(t)
	g.api[annPage(2, 1)] = annotations(`lacquer-ratchet-loosened/v1 {`)
	o := HarvestOptions{InboxPath: path, Run: g.run}
	pr := mergedPR{Number: 9, URL: "https://github.com/acme/widgets/pull/9", HeadRefOid: head9}
	existing := map[string]bool{}
	r := harvestRepo{name: "widgets", repo: "acme/widgets"}
	first, err := harvestLoosenings(o, r, pr, existing)
	if err != nil || len(first) != 1 {
		t.Fatalf("first look: %+v, %v", first, err)
	}
	again, err := harvestLoosenings(o, r, pr, existing)
	if err != nil || len(again) != 0 || len(entries(t, path)) != 1 {
		t.Fatalf("second look added %+v (%v); want nothing", again, err)
	}
}
