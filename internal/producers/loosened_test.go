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
	runsPath = "repos/acme/widgets/commits/" + head9 + "/check-runs?filter=latest&per_page=100"
	// The message GitHub stores for the gate's ::notice, pinned literally: this
	// is the documented v1 format, not whatever the encoder happens to emit.
	loosenedMsg = `lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines","from":100,"to":150,"reason":"vendored parser"}`
)

func mergedWithHead(n int, title, mergedAt, head string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/acme/widgets/pull/%d","mergedAt":%q,"headRefOid":%q}`, n, title, n, mergedAt, head)
}

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
	g.list = "[" + mergedWithHead(9, "loosen coverage", "2026-09-24T12:30:00Z", head9) + "]"
	g.files = "[" + filesOf(9, ".lacquer.ratchet.toml", "Sources/Parser.swift") + "]"
	g.api[runsPath] = `{"total_count":3,"check_runs":[{"id":1,"output":{"annotations_count":2}},{"id":2,"output":{"annotations_count":1}},{"id":3,"output":{"annotations_count":0}}]}`
	g.api["repos/acme/widgets/check-runs/1/annotations?per_page=100"] = annotations("Xcode 26 is deprecated", loosenedMsg)
	g.api["repos/acme/widgets/check-runs/2/annotations?per_page=100"] = annotations(loosenedMsg)
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
	g.apiErr["repos/acme/widgets/check-runs/2/annotations?per_page=100"] = errors.New("HTTP 502")
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "acme/widgets#9") || !strings.Contains(res.Unavailable[0], "HTTP 502") {
		t.Fatalf("unavailable = %v; want the failed look named", res.Unavailable)
	}
	if n := len(entries(t, path)); n != 0 {
		t.Fatalf("a failed look still wrote %d entries", n)
	}
	delete(g.apiErr, "repos/acme/widgets/check-runs/2/annotations?per_page=100")
	if res := harvest9(path, t0.Add(2*time.Hour), g); len(res.Added) != 2 {
		t.Fatalf("the retry added %+v (unavailable %v); want the loosening and the merge", res.Added, res.Unavailable)
	}
}

func TestHarvestReportsADamagedLooseningLine(t *testing.T) {
	path, g := loosenFixture(t)
	g.api["repos/acme/widgets/check-runs/2/annotations?per_page=100"] = annotations(`lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines"`)
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "malformed") {
		t.Fatalf("unavailable = %v; a damaged line must be reported, not dropped", res.Unavailable)
	}
}

func TestHarvestRefusesATruncatedCheckRunList(t *testing.T) {
	path, g := loosenFixture(t)
	g.api[runsPath] = `{"total_count":150,"check_runs":[{"id":1,"output":{"annotations_count":2}}]}`
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "150 runs, only 1 read") {
		t.Fatalf("unavailable = %v; a partial read must not pass as complete", res.Unavailable)
	}
}
