package ciwait

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- fixtures -------------------------------------------------------------

// run is a CheckRun node as `gh pr view --json statusCheckRollup` renders it.
func run(name, status, conclusion string) string {
	return fmt.Sprintf(`{"__typename":"CheckRun","name":%q,"status":%q,"conclusion":%q,"startedAt":"2026-09-20T03:00:00Z","completedAt":%q,"detailsUrl":"https://example/%s"}`,
		name, status, conclusion, completedAt(status), name)
}

func completedAt(status string) string {
	if status == "COMPLETED" {
		return "2026-09-20T03:03:12Z"
	}
	return "0001-01-01T00:00:00Z"
}

// ctxNode is a StatusContext node: a legacy commit status. It has `state` and
// NO `status` field, which is the whole trap.
func ctxNode(name, state string) string {
	return fmt.Sprintf(`{"__typename":"StatusContext","context":%q,"state":%q,"startedAt":"2026-09-20T03:00:00Z","targetUrl":"https://example/%s"}`, name, state, name)
}

func resp(state, head string, nodes ...string) string {
	return fmt.Sprintf(`{"state":%q,"headRefOid":%q,"statusCheckRollup":[%s]}`, state, head, strings.Join(nodes, ","))
}

func open(nodes ...string) string { return resp("OPEN", "aaaa111", nodes...) }

// script replays canned gh outputs, one per call, repeating the last. A step
// that starts with "ERR:" is a failing gh call carrying that message.
type script struct {
	steps []string
	calls int
	args  [][]string
}

func (s *script) runner() Runner {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		s.args = append(s.args, args)
		i := s.calls
		if i >= len(s.steps) {
			i = len(s.steps) - 1
		}
		s.calls++
		step := s.steps[i]
		if msg, ok := strings.CutPrefix(step, "ERR:"); ok {
			if msg == "NOGH" {
				return nil, ErrGHNotFound
			}
			return nil, errors.New(msg)
		}
		return []byte(step), nil
	}
}

// clock is a fake time source: Sleep advances it, so a 20-minute timeout runs
// in microseconds and the test controls exactly how many polls fit.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep(_ context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

func wait(t *testing.T, s *script, mod func(*Options)) Result {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 20, 3, 5, 0, 0, time.UTC)}
	o := Options{
		PR: 425, Timeout: 20 * time.Minute, Interval: 15 * time.Second,
		EmptyGrace: 30 * time.Second, MaxErrors: 3,
		Run: s.runner(), Sleep: c.sleep, Now: c.now,
	}
	if mod != nil {
		mod(&o)
	}
	return Wait(context.Background(), o)
}

func names(cs []Check) string {
	var n []string
	for _, c := range cs {
		n = append(n, c.Name)
	}
	return strings.Join(n, ",")
}

// --- the four outcomes ----------------------------------------------------

// The defect this package exists to replace: a check in flight reports
// conclusion "" and a `// "PENDING"` default never fires on it.
func TestInFlightCheckWithEmptyConclusionDoesNotReturn(t *testing.T) {
	inflight := open(run("test", "IN_PROGRESS", ""), run("lint", "COMPLETED", "SUCCESS"))
	done := open(run("test", "COMPLETED", "SUCCESS"), run("lint", "COMPLETED", "SUCCESS"))
	s := &script{steps: []string{inflight, inflight, inflight, inflight, inflight, done}}
	r := wait(t, s, nil)
	if r.Outcome != Passed {
		t.Fatalf("outcome = %v (%s), want Passed", r.Outcome, r.Message)
	}
	// Five polls in flight, then done, then one settling poll: returning on the
	// first poll (the bug) would leave calls at 1.
	if s.calls < 6 {
		t.Fatalf("returned after %d polls; it must keep blocking while a check is in flight", s.calls)
	}
}

func TestQueuedAndWaitingChecksAreNotTerminal(t *testing.T) {
	for _, st := range []string{"QUEUED", "IN_PROGRESS", "WAITING", "PENDING", "REQUESTED", "SOMETHING_NEW"} {
		s := &script{steps: []string{open(run("test", st, ""))}}
		r := wait(t, s, func(o *Options) { o.Timeout = time.Minute })
		if r.Outcome != TimedOut {
			t.Errorf("status %s: outcome = %v, want TimedOut (still running)", st, r.Outcome)
		}
	}
}

func TestFailedCheckIsNamed(t *testing.T) {
	s := &script{steps: []string{open(
		run("test", "COMPLETED", "FAILURE"),
		run("lint", "COMPLETED", "SUCCESS"),
		run("Site builds", "COMPLETED", "TIMED_OUT"),
	)}}
	r := wait(t, s, nil)
	if r.Outcome != Failed || r.Outcome.ExitCode() != 1 {
		t.Fatalf("outcome = %v, want Failed/1", r.Outcome)
	}
	if got := names(r.Failed()); got != "test,Site builds" {
		t.Errorf("Failed() = %q, want test,Site builds", got)
	}
	out := Format(r)
	for _, want := range []string{"FAILED: 2 of 3 checks failed: test, Site builds"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestEveryNonSuccessTerminalConclusionThatIsNotBenignFails(t *testing.T) {
	for _, c := range []string{"FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE", "", "MYSTERY"} {
		s := &script{steps: []string{open(run("test", "COMPLETED", c))}}
		if r := wait(t, s, nil); r.Outcome != Failed {
			t.Errorf("COMPLETED with conclusion %q: outcome = %v, want Failed (fail closed)", c, r.Outcome)
		}
	}
}

func TestNeutralPasses(t *testing.T) {
	s := &script{steps: []string{open(run("test", "COMPLETED", "SUCCESS"), run("note", "COMPLETED", "NEUTRAL"))}}
	if r := wait(t, s, nil); r.Outcome != Passed {
		t.Fatalf("outcome = %v, want Passed", r.Outcome)
	}
}

func TestTimeoutIsNotFailureAndNamesWhatWasRunning(t *testing.T) {
	s := &script{steps: []string{open(
		run("test", "IN_PROGRESS", ""),
		run("lint", "COMPLETED", "SUCCESS"),
		run("build", "QUEUED", ""),
	)}}
	r := wait(t, s, func(o *Options) { o.Timeout = 2 * time.Minute })
	if r.Outcome != TimedOut || r.Outcome.ExitCode() != 2 {
		t.Fatalf("outcome = %v, want TimedOut/2", r.Outcome)
	}
	if got := names(r.Running()); got != "test,build" {
		t.Errorf("Running() = %q, want test,build", got)
	}
	out := Format(r)
	if !strings.Contains(out, "TIMED OUT") || !strings.Contains(out, "still running: test, build") {
		t.Errorf("output must say TIMED OUT and list what was running:\n%s", out)
	}
	if strings.Contains(out, "FAILED") {
		t.Errorf("a timeout must not read as a failure:\n%s", out)
	}
	if r.Elapsed < 2*time.Minute {
		t.Errorf("Elapsed = %v, timed out early", r.Elapsed)
	}
}

// A known failure plus a check still running at the ceiling is Failed, not
// TimedOut: the failure is a fact and CI cannot go green from there, whereas a
// timeout means "not known yet". A caller deciding whether to spend another CI
// round keys on the exit code alone. The abandoned checks are still listed.
func TestFailureBeatsTimeoutAndTheRunningChecksAreListedAsAbandoned(t *testing.T) {
	s := &script{steps: []string{open(run("a", "COMPLETED", "FAILURE"), run("b", "IN_PROGRESS", ""), run("c", "COMPLETED", "SUCCESS"))}}
	r := wait(t, s, func(o *Options) { o.Timeout = time.Minute })
	if r.Outcome != Failed || r.Outcome.ExitCode() != 1 {
		t.Fatalf("outcome = %v, want Failed/1 (a known failure is decisive)", r.Outcome)
	}
	if got := names(r.Running()); got != "b" {
		t.Errorf("Running() = %q, want b still listed", got)
	}
	out := Format(r)
	for _, want := range []string{"FAILED: 1 of 3 checks failed: a", "The failure is decisive", "abandoned", "still running", ": b"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TIMED OUT") {
		t.Errorf("must not read as a timeout:\n%s", out)
	}
	if r.Elapsed < time.Minute {
		t.Errorf("Elapsed = %v: it must still wait out every running check until the ceiling", r.Elapsed)
	}
}

// Exit 2 is for "still running, nothing failed" only.
func TestTimeoutWithNothingFailedIsStillTimedOut(t *testing.T) {
	s := &script{steps: []string{open(run("a", "COMPLETED", "SUCCESS"), run("b", "IN_PROGRESS", ""), run("c", "COMPLETED", "SKIPPED"), run("d", "COMPLETED", "NEUTRAL"))}}
	if r := wait(t, s, func(o *Options) { o.Timeout = time.Minute }); r.Outcome != TimedOut {
		t.Fatalf("outcome = %v, want TimedOut", r.Outcome)
	}
}

// A failed check that lands while others run does not end the wait early: the
// brief is to block until every check is terminal.
func TestAFailureDoesNotEndTheWaitWhileOthersAreRunning(t *testing.T) {
	early := open(run("a", "COMPLETED", "FAILURE"), run("b", "IN_PROGRESS", ""))
	late := open(run("a", "COMPLETED", "FAILURE"), run("b", "COMPLETED", "SUCCESS"))
	s := &script{steps: []string{early, early, early, late}}
	r := wait(t, s, nil)
	if r.Outcome != Failed || len(r.Running()) != 0 || s.calls < 4 {
		t.Fatalf("outcome %v, running %q, calls %d: returned before b finished", r.Outcome, names(r.Running()), s.calls)
	}
}

// --- trap 1: the empty rollup --------------------------------------------

func TestEmptyRollupIsNoChecksAndNeverGreen(t *testing.T) {
	s := &script{steps: []string{open()}}
	r := wait(t, s, nil)
	if r.Outcome != NoChecks || r.Outcome.ExitCode() != 3 {
		t.Fatalf("outcome = %v, want NoChecks/3 — zero checks is not 'none failed'", r.Outcome)
	}
	out := Format(r)
	if !strings.Contains(out, "NO CHECKS") {
		t.Errorf("output must say NO CHECKS:\n%s", out)
	}
	for _, bad := range []string{"PASSED", "green"} {
		if strings.Contains(out, bad) {
			t.Errorf("an empty rollup must never read as %q:\n%s", bad, out)
		}
	}
}

// Checks register a moment after a PR opens. Re-check rather than latch on the
// empty first reading, and evaluate what shows up.
func TestEmptyThenPopulatedIsEvaluatedNotLatched(t *testing.T) {
	s := &script{steps: []string{
		open(), open(),
		open(run("test", "IN_PROGRESS", "")),
		open(run("test", "COMPLETED", "FAILURE")),
	}}
	r := wait(t, s, nil)
	if r.Outcome != Failed {
		t.Fatalf("outcome = %v (%s), want Failed: the checks appeared and one failed", r.Outcome, r.Message)
	}
}

func TestEmptyThenPopulatedAllGreenPasses(t *testing.T) {
	s := &script{steps: []string{open(), open(run("test", "COMPLETED", "SUCCESS"))}}
	if r := wait(t, s, nil); r.Outcome != Passed {
		t.Fatalf("outcome = %v, want Passed", r.Outcome)
	}
}

func TestEmptyPastGraceIsNoChecks(t *testing.T) {
	s := &script{steps: []string{open()}}
	r := wait(t, s, func(o *Options) { o.EmptyGrace = time.Minute })
	if r.Outcome != NoChecks {
		t.Fatalf("outcome = %v, want NoChecks", r.Outcome)
	}
	if s.calls < 4 {
		t.Errorf("polled %d times; an empty rollup must be re-checked through the grace window", s.calls)
	}
}

// An empty rollup at the ceiling is NoChecks, not TimedOut: nothing was running.
func TestEmptyAtTimeoutIsNoChecksNotTimedOut(t *testing.T) {
	s := &script{steps: []string{open()}}
	r := wait(t, s, func(o *Options) { o.Timeout = 20 * time.Second; o.EmptyGrace = time.Hour })
	if r.Outcome != NoChecks {
		t.Fatalf("outcome = %v, want NoChecks", r.Outcome)
	}
}

func TestMissingRollupKeyIsAnErrorNotEmpty(t *testing.T) {
	for _, body := range []string{`{"state":"OPEN","headRefOid":"a"}`, `{"state":"OPEN","headRefOid":"a","statusCheckRollup":null}`, `not json`, ``} {
		s := &script{steps: []string{body}}
		r := wait(t, s, nil)
		if r.Outcome != Error {
			t.Errorf("body %q: outcome = %v, want Error — an unreadable response is not an empty rollup", body, r.Outcome)
		}
	}
}

// --- trap 2: two node shapes ---------------------------------------------

func TestPendingStatusContextIsNotTerminal(t *testing.T) {
	for _, st := range []string{"PENDING", "EXPECTED"} {
		s := &script{steps: []string{open(run("test", "COMPLETED", "SUCCESS"), ctxNode("ci/legacy", st))}}
		r := wait(t, s, func(o *Options) { o.Timeout = time.Minute })
		if r.Outcome != TimedOut {
			t.Errorf("state %s: outcome = %v, want TimedOut: a pending commit status has no `status` field and must not read as complete", st, r.Outcome)
		}
		if got := names(r.Running()); got != "ci/legacy" {
			t.Errorf("state %s: Running() = %q", st, got)
		}
	}
}

func TestStatusContextSuccessFailureError(t *testing.T) {
	s := &script{steps: []string{open(ctxNode("ok", "SUCCESS"))}}
	if r := wait(t, s, nil); r.Outcome != Passed {
		t.Errorf("SUCCESS context: %v, want Passed", r.Outcome)
	}
	for _, st := range []string{"FAILURE", "ERROR"} {
		s := &script{steps: []string{open(run("test", "COMPLETED", "SUCCESS"), ctxNode("ci/legacy", st))}}
		r := wait(t, s, nil)
		if r.Outcome != Failed || names(r.Failed()) != "ci/legacy" {
			t.Errorf("%s context: outcome %v failed=%q, want Failed naming ci/legacy", st, r.Outcome, names(r.Failed()))
		}
	}
}

func TestUnrecognisedNodeIsNeverTerminal(t *testing.T) {
	s := &script{steps: []string{open(run("test", "COMPLETED", "SUCCESS"), `{"__typename":"SomethingNew","foo":"bar"}`)}}
	r := wait(t, s, func(o *Options) { o.Timeout = time.Minute })
	if r.Outcome != TimedOut {
		t.Fatalf("outcome = %v, want TimedOut: a node we cannot classify must not count as done", r.Outcome)
	}
}

func TestShapeIsDetectedWithoutTypename(t *testing.T) {
	// PENDING/IN_PROGRESS time out whether or not the shape is recognised, so
	// the terminal ones are what prove the shape was read: an unrecognised node
	// would never finish.
	cases := []struct {
		node string
		want Outcome
	}{
		{`{"context":"ci/legacy","state":"PENDING"}`, TimedOut},
		{`{"name":"test","status":"IN_PROGRESS","conclusion":""}`, TimedOut},
		{`{"context":"ci/legacy","state":"SUCCESS"}`, Passed},
		{`{"context":"ci/legacy","state":"FAILURE"}`, Failed},
		{`{"name":"test","status":"COMPLETED","conclusion":"SUCCESS"}`, Passed},
		{`{"name":"test","status":"COMPLETED","conclusion":"FAILURE"}`, Failed},
	}
	for _, c := range cases {
		s := &script{steps: []string{open(c.node)}}
		if r := wait(t, s, func(o *Options) { o.Timeout = time.Minute }); r.Outcome != c.want {
			t.Errorf("%s: outcome = %v, want %v", c.node, r.Outcome, c.want)
		}
	}
}

// --- trap 3: skipped ------------------------------------------------------

func TestSkippedIsDistinctAndNamedButStillExitsZero(t *testing.T) {
	s := &script{steps: []string{open(
		run("test", "COMPLETED", "SUCCESS"),
		run("deploy", "COMPLETED", "SKIPPED"),
		run("e2e", "COMPLETED", "SKIPPED"),
	)}}
	r := wait(t, s, nil)
	if r.Outcome != Passed || r.Outcome.ExitCode() != 0 {
		t.Fatalf("outcome = %v, want Passed/0", r.Outcome)
	}
	if got := names(r.Skipped()); got != "deploy,e2e" {
		t.Errorf("Skipped() = %q", got)
	}
	out := Format(r)
	if !strings.Contains(out, "skipped: deploy, e2e") {
		t.Errorf("output must name the skipped checks:\n%s", out)
	}
	if strings.Contains(out, "success      deploy") || !strings.Contains(out, "skipped") {
		t.Errorf("skipped must not be printed as success:\n%s", out)
	}
}

func TestAllSkippedSaysNothingRan(t *testing.T) {
	s := &script{steps: []string{open(run("a", "COMPLETED", "SKIPPED"), run("b", "COMPLETED", "SKIPPED"))}}
	r := wait(t, s, nil)
	if r.Outcome != Passed {
		t.Fatalf("outcome = %v, want Passed (documented: skipped alone exits 0)", r.Outcome)
	}
	if out := Format(r); !strings.Contains(out, "every check was skipped") {
		t.Errorf("all-skipped must be unmissable:\n%s", out)
	}
}

// --- trap 4: transient errors --------------------------------------------

func TestTransientErrorsAreRetriedThenSucceed(t *testing.T) {
	ok := open(run("test", "COMPLETED", "SUCCESS"))
	s := &script{steps: []string{"ERR:dial tcp: i/o timeout", "ERR:HTTP 502", ok}}
	r := wait(t, s, nil) // MaxErrors 3: two errors are tolerated
	if r.Outcome != Passed {
		t.Fatalf("outcome = %v (%s), want Passed after retries", r.Outcome, r.Message)
	}
}

func TestPersistentErrorIsAnErrorNeverSuccessOrNoChecks(t *testing.T) {
	s := &script{steps: []string{"ERR:HTTP 502"}}
	r := wait(t, s, nil)
	if r.Outcome != Error || r.Outcome.ExitCode() == 0 || r.Outcome.ExitCode() <= 3 {
		t.Fatalf("outcome = %v/%d, want Error with an exit code outside 0-3", r.Outcome, r.Outcome.ExitCode())
	}
	if s.calls != 3 {
		t.Errorf("calls = %d, want exactly MaxErrors (3): bounded retries", s.calls)
	}
	if !strings.Contains(Format(r), "HTTP 502") {
		t.Errorf("the underlying error must be shown:\n%s", Format(r))
	}
}

func TestErrorCountIsConsecutiveNotCumulative(t *testing.T) {
	inflight := open(run("test", "IN_PROGRESS", ""))
	s := &script{steps: []string{
		"ERR:x", "ERR:x", inflight, "ERR:x", "ERR:x", inflight,
		open(run("test", "COMPLETED", "SUCCESS")),
	}}
	if r := wait(t, s, nil); r.Outcome != Passed {
		t.Fatalf("outcome = %v (%s): a success between errors must reset the count", r.Outcome, r.Message)
	}
}

func TestMissingGHFailsImmediatelyWithAClearError(t *testing.T) {
	s := &script{steps: []string{"ERR:NOGH"}}
	r := wait(t, s, nil)
	if r.Outcome != Error {
		t.Fatalf("outcome = %v, want Error", r.Outcome)
	}
	if s.calls != 1 {
		t.Errorf("calls = %d; a missing gh is not transient and must not be retried", s.calls)
	}
	if !strings.Contains(Format(r), "gh") {
		t.Errorf("message should mention gh:\n%s", Format(r))
	}
}

// An error at the ceiling means the state is unknown: not "timed out", not green.
func TestErrorAtTheCeilingIsAnError(t *testing.T) {
	s := &script{steps: []string{open(run("test", "IN_PROGRESS", "")), "ERR:x"}}
	r := wait(t, s, func(o *Options) { o.Timeout = 45 * time.Second; o.MaxErrors = 100 })
	if r.Outcome != Error {
		t.Fatalf("outcome = %v, want Error", r.Outcome)
	}
}

func TestContextCancelIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &script{steps: []string{open(run("test", "IN_PROGRESS", ""))}}
	c := &clock{t: time.Now()}
	r := Wait(ctx, Options{PR: 1, Timeout: time.Hour, Interval: time.Second, MaxErrors: 3, Run: s.runner(), Now: c.now,
		Sleep: func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }})
	if r.Outcome != Error {
		t.Fatalf("outcome = %v, want Error on interrupt", r.Outcome)
	}
}

// --- trap 5: the PR changes under us --------------------------------------

func TestMergedOrClosedPRIsNotWaitedOn(t *testing.T) {
	for _, st := range []string{"MERGED", "CLOSED"} {
		s := &script{steps: []string{resp(st, "aaaa111", run("test", "IN_PROGRESS", ""))}}
		r := wait(t, s, nil)
		if r.Outcome != Error || !strings.Contains(r.Message, st) {
			t.Errorf("%s: outcome %v msg %q, want Error naming the state", st, r.Outcome, r.Message)
		}
	}
}

func TestPRClosedMidWaitStops(t *testing.T) {
	s := &script{steps: []string{open(run("test", "IN_PROGRESS", "")), resp("MERGED", "aaaa111", run("test", "COMPLETED", "SUCCESS"))}}
	if r := wait(t, s, nil); r.Outcome != Error {
		t.Fatalf("outcome = %v, want Error", r.Outcome)
	}
}

// A push mid-wait means new checks. Results for the old commit must not be
// reported as the PR's result.
func TestNewHeadCommitDiscardsTheOldResultsAndWaitsOnTheNew(t *testing.T) {
	oldGreen := resp("OPEN", "aaaa111", run("test", "COMPLETED", "SUCCESS"))
	newRunning := resp("OPEN", "bbbb222", run("test", "IN_PROGRESS", ""))
	newFailed := resp("OPEN", "bbbb222", run("test", "COMPLETED", "FAILURE"))
	s := &script{steps: []string{oldGreen, newRunning, newRunning, newFailed}}
	r := wait(t, s, nil)
	if r.Outcome != Failed {
		t.Fatalf("outcome = %v, want Failed: the old head's green must not be the answer", r.Outcome)
	}
	if r.Head != "bbbb222" {
		t.Errorf("Head = %q, want the new head", r.Head)
	}
	if len(r.HeadChanges) != 1 || r.HeadChanges[0] != "aaaa111 -> bbbb222" {
		t.Errorf("HeadChanges = %v", r.HeadChanges)
	}
	if !strings.Contains(Format(r), "head moved") {
		t.Errorf("the head change must be visible in the output:\n%s", Format(r))
	}
}

// The new commit's checks have not registered yet: that is an empty rollup for
// the NEW head, not a pass carried over from the old one.
func TestNewHeadWithNoChecksYetDoesNotInheritTheOldGreen(t *testing.T) {
	s := &script{steps: []string{
		resp("OPEN", "aaaa111", run("test", "COMPLETED", "SUCCESS")),
		resp("OPEN", "bbbb222"),
	}}
	r := wait(t, s, nil)
	if r.Outcome != NoChecks {
		t.Fatalf("outcome = %v, want NoChecks for the new head", r.Outcome)
	}
}

// Two readings only confirm each other if they are of the same commit. Head A
// finishing green and head B finishing green with the same check names must not
// count as one reading held twice.
func TestSettlingDoesNotSpanAHeadChange(t *testing.T) {
	a := resp("OPEN", "aaaa111", run("test", "COMPLETED", "SUCCESS"))
	b := resp("OPEN", "bbbb222", run("test", "COMPLETED", "SUCCESS"))
	bLate := resp("OPEN", "bbbb222", run("test", "COMPLETED", "SUCCESS"), run("e2e", "IN_PROGRESS", ""))
	bLateDone := resp("OPEN", "bbbb222", run("test", "COMPLETED", "SUCCESS"), run("e2e", "COMPLETED", "FAILURE"))
	s := &script{steps: []string{a, b, bLate, bLateDone}}
	r := wait(t, s, nil)
	if r.Outcome != Failed || names(r.Failed()) != "e2e" {
		t.Fatalf("outcome = %v failed=%q: a reading of the old head confirmed one of the new", r.Outcome, names(r.Failed()))
	}
}

// --- settling -------------------------------------------------------------

// The first reading with everything terminal can be a reading taken before a
// slower workflow registered. Require it to hold for a second poll.
func TestAllTerminalMustHoldAcrossTwoPolls(t *testing.T) {
	fast := open(run("version", "COMPLETED", "SUCCESS"))
	both := open(run("version", "COMPLETED", "SUCCESS"), run("test", "IN_PROGRESS", ""))
	bothDone := open(run("version", "COMPLETED", "SUCCESS"), run("test", "COMPLETED", "FAILURE"))
	s := &script{steps: []string{fast, both, bothDone}}
	r := wait(t, s, nil)
	if r.Outcome != Failed || names(r.Failed()) != "test" {
		t.Fatalf("outcome = %v failed=%q: a late-registering check was missed", r.Outcome, names(r.Failed()))
	}
}

// --- output ---------------------------------------------------------------

func TestFormatShowsNameConclusionAndDuration(t *testing.T) {
	s := &script{steps: []string{open(run("test", "COMPLETED", "SUCCESS"), ctxNode("ci/legacy", "SUCCESS"))}}
	r := wait(t, s, nil)
	out := Format(r)
	for _, want := range []string{"test", "success", "3m12s", "ci/legacy", "PASSED"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestJSONCarriesOutcomeAndExitCode(t *testing.T) {
	s := &script{steps: []string{open(run("test", "COMPLETED", "FAILURE"), run("lint", "COMPLETED", "SKIPPED"))}}
	r := wait(t, s, nil)
	var got struct {
		Outcome  string   `json:"outcome"`
		ExitCode int      `json:"exit_code"`
		PR       int      `json:"pr"`
		Head     string   `json:"head"`
		Failed   []string `json:"failed"`
		Skipped  []string `json:"skipped"`
		Checks   []struct {
			Name       string  `json:"name"`
			Kind       string  `json:"kind"`
			Conclusion string  `json:"conclusion"`
			Terminal   bool    `json:"terminal"`
			Seconds    float64 `json:"duration_seconds"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(FormatJSON(r)), &got); err != nil {
		t.Fatalf("FormatJSON is not JSON: %v\n%s", err, FormatJSON(r))
	}
	if got.Outcome != "failed" || got.ExitCode != 1 || got.PR != 425 || got.Head != "aaaa111" {
		t.Errorf("header = %+v", got)
	}
	if len(got.Failed) != 1 || got.Failed[0] != "test" || len(got.Skipped) != 1 || got.Skipped[0] != "lint" {
		t.Errorf("failed/skipped = %v / %v", got.Failed, got.Skipped)
	}
	if len(got.Checks) != 2 || got.Checks[0].Kind != "check_run" || !got.Checks[0].Terminal || got.Checks[0].Seconds != 192 {
		t.Errorf("checks = %+v", got.Checks)
	}
}

func TestExitCodesAreTheDocumentedFour(t *testing.T) {
	want := map[Outcome]int{Passed: 0, Failed: 1, TimedOut: 2, NoChecks: 3}
	seen := map[int]Outcome{}
	for o, code := range want {
		if o.ExitCode() != code {
			t.Errorf("%v.ExitCode() = %d, want %d", o, o.ExitCode(), code)
		}
		seen[o.ExitCode()] = o
	}
	if Error.ExitCode() != 4 {
		t.Errorf("Error.ExitCode() = %d, want 4", Error.ExitCode())
	}
}

func TestPRArgumentsAndRepoFlagReachGH(t *testing.T) {
	s := &script{steps: []string{open(run("test", "COMPLETED", "SUCCESS"))}}
	wait(t, s, func(o *Options) { o.Repo = "acme/widgets" })
	joined := strings.Join(s.args[0], " ")
	for _, want := range []string{"pr view", "-R acme/widgets", "425", "statusCheckRollup", "headRefOid", "state"} {
		if !strings.Contains(joined, want) {
			t.Errorf("gh args %q missing %q", joined, want)
		}
	}
}
