//go:build eval

package eval

import (
	"fmt"
	"sync"
	"testing"
)

// Known, tracked bugs a marked scenario is allowed to reproduce without
// turning the eval-suite job red. Each one names the issue tracking whether
// and how it gets fixed. Do not add to this list to make an unrelated
// scenario quiet — see expectKnownFailure's doc comment.
const (
	issueStaleRoot    = "https://github.com/patrickserrano/lacquer/issues/350"
	issueCommentMatch = "https://github.com/patrickserrano/lacquer/issues/363"
)

// summary tallies scenario-level outcomes for one run of
// `go test -tags eval ./internal/eval/...`: pass (an unmarked scenario
// reached its verdict, or a marked scenario's bug got fixed and was reported
// as such), expectedFail (a marked scenario reproduced its known, tracked
// bug — the normal, expected state for it today), and fail (anything else:
// an unmarked scenario failing, a marked scenario's setup failing, or a
// marked scenario's bug silently getting fixed). TestMain (main_test.go)
// prints report() once, after m.Run(), specifically so a GREEN run of this
// job still names its known bugs in the log — see doc.go and this repo's own
// CLAUDE.md ("a state indistinguishable from working").
type summary struct {
	mu           sync.Mutex
	pass         int
	expectedFail int
	fail         int
	expectedLog  []string
}

var results summary

type summarySnapshot struct {
	pass, expectedFail, fail int
}

func (s *summary) snapshot() summarySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return summarySnapshot{pass: s.pass, expectedFail: s.expectedFail, fail: s.fail}
}

func (s *summary) recordPass() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pass++
}

func (s *summary) recordFail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail++
}

func (s *summary) recordExpectedFail(scenario, issue string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expectedFail++
	s.expectedLog = append(s.expectedLog, fmt.Sprintf("%s (known bug, %s)", scenario, issue))
}

func (s *summary) report() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := fmt.Sprintf("eval summary: %d pass, %d expected-fail, %d fail", s.pass, s.expectedFail, s.fail)
	for _, l := range s.expectedLog {
		out += "\n  expected-fail: " + l
	}
	return out
}

// recordScenario tallies t's own final pass/fail state into the package-level
// summary. Call it as the FIRST line of an UNMARKED scenario test function —
// `defer recordScenario(t)` — and nowhere else.
//
// This works whether the test fails via t.Errorf (soft) or t.Fatalf (hard):
// Fatal calls t.FailNow, which calls runtime.Goexit, and Goexit still runs
// deferred functions in the failing goroutine before the goroutine exits — so
// this defer observes the test's real, final t.Failed() state either way,
// including a setup failure that fires before the scenario ever reaches its
// own verdict check.
//
// Never add this to a MARKED scenario's test function. expectKnownFailure
// below already owns that scenario's bookkeeping, and a marked scenario that
// reaches its verdict check without erroring (the normal, expected-for-this-
// suite outcome) has NOT called t.Errorf/t.Fatalf — t.Failed() is false — so
// a second recordScenario on the same test would double-count it as an
// ordinary pass on top of the expected-fail expectKnownFailure already
// recorded.
func recordScenario(t *testing.T) {
	recordScenarioInto(t, &results)
}

// failedReporter is the one method recordScenarioInto needs. *testing.T
// satisfies it structurally. expect_test.go's meta-tests pass a tiny fake
// instead of a real *testing.T here on purpose: a *testing.T subtest that
// itself fails would propagate that failure up to ITS parent via Go's own
// testing package (t.Run marks every ancestor failed, unconditionally, the
// moment a child fails) — which would make a meta-test that deliberately
// exercises the "setup failure" path always report red, even when
// recordScenarioInto is behaving exactly as intended. A fake sidesteps that
// entirely: it has no ancestors to propagate to.
type failedReporter interface {
	Failed() bool
}

// recordScenarioInto is recordScenario against an explicit summary rather
// than the package-global results — the indirection exists so
// expect_test.go's meta-tests can pin this function's contract against a
// throwaway *summary without polluting the real report every other test in
// this package contributes to (see TestMain in main_test.go).
func recordScenarioInto(t failedReporter, s *summary) {
	if t.Failed() {
		s.recordFail()
		return
	}
	s.recordPass()
}

// expectKnownFailure is the strict expected-failure marker: the ONLY path a
// marked scenario's VERDICT assertion may go through. It must never wrap a
// scenario's setup — every setup helper in this package (harness.go and every
// scenario_*_test.go) calls t.Fatalf directly, on purpose, so a setup failure
// always terminates the test immediately via runtime.Goexit and can never
// reach this function to be absorbed. Keeping setup and verdict on two
// structurally different call paths — t.Fatalf directly vs. this function —
// is what makes "a marked scenario's setup errors must FAIL, never be
// absorbed" true by construction rather than by convention.
//
//   - ok == false: the scenario's verdict was NOT reached, i.e. it currently
//     reproduces the known bug tracked by issue. That is the expected state
//     for a marked scenario today, so this does NOT fail the test: it is
//     logged and tallied as an expected failure.
//   - ok == true: the scenario's verdict WAS reached — the bug tracked by
//     issue no longer reproduces. That is news, not a pass: this fails the
//     test loudly, naming the issue and instructing whoever sees it to
//     remove this scenario's marker.
//
// format/args describe the underlying verdict check exactly as an unmarked
// scenario's t.Errorf would, so the message is informative either way this
// resolves.
func expectKnownFailure(t *testing.T, issue string, ok bool, format string, args ...any) {
	expectKnownFailureInto(t, &results, issue, ok, format, args...)
}

// verdictReporter is the small slice of *testing.T that expectKnownFailureInto
// needs. *testing.T satisfies it structurally. expect_test.go's meta-tests
// pass a tiny fake instead — see failedReporter's doc comment above for why a
// real *testing.T subtest cannot safely stand in for the "this should fail"
// case: Go's own testing package would propagate that failure up to the
// meta-test itself, permanently, even when the failure is the correct,
// intended outcome being verified.
type verdictReporter interface {
	Helper()
	Name() string
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
}

// expectKnownFailureInto is expectKnownFailure against an explicit summary
// rather than the package-global results — see recordScenarioInto's doc
// comment for why this indirection exists.
func expectKnownFailureInto(t verdictReporter, s *summary, issue string, ok bool, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	name := t.Name()
	if ok {
		s.recordFail()
		t.Errorf("scenario %s now passes — the bug tracked in %s appears fixed; remove its "+
			"expected-failure marker (see internal/eval/expect.go's issue constants and this "+
			"scenario's expectKnownFailure call).\nunderlying verdict message:\n%s", name, issue, msg)
		return
	}
	s.recordExpectedFail(name, issue)
	t.Logf("expected-fail (%s): %s", issue, msg)
}
