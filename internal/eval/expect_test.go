//go:build eval

package eval

import (
	"fmt"
	"strings"
	"testing"
)

// These pin expect.go's own contract directly, independent of any real
// scenario — see CLAUDE.md rule 2 ("a guard is not tested until a mutation
// has failed a named test"). Each exercises expectKnownFailureInto /
// recordScenarioInto through a tiny fake (fakeVerdictReporter /
// fakeFailedReporter) rather than a real *testing.T subtest: a real subtest
// that deliberately fails would propagate that failure up to ITS parent via
// Go's own testing package (t.Run marks every ancestor failed the moment a
// child fails, permanently), which would make a meta-test exercising the
// "this must fail" case always report red — even when the code under test is
// behaving exactly as intended. A fake has no ancestors to propagate to. Each
// also uses its own throwaway *summary rather than the package-global
// `results` that TestMain reports, so these synthetic calls never pollute the
// real pass/expected-fail/fail counts the other tests in this package
// contribute to.

// fakeVerdictReporter implements verdictReporter without any relation to a
// real testing.T's pass/fail state, so tests can inspect exactly what
// expectKnownFailureInto did.
type fakeVerdictReporter struct {
	errored bool
	errMsg  string
	logged  []string
}

func (f *fakeVerdictReporter) Helper()      {}
func (f *fakeVerdictReporter) Name() string { return "fake" }
func (f *fakeVerdictReporter) Errorf(format string, args ...any) {
	f.errored = true
	f.errMsg = fmt.Sprintf(format, args...)
}
func (f *fakeVerdictReporter) Logf(format string, args ...any) {
	f.logged = append(f.logged, fmt.Sprintf(format, args...))
}

// TestExpectKnownFailureRecordsExpectedFailWithoutFailingTest pins the core
// strict-marker semantics table's second row: a marked scenario whose verdict
// fails (ok == false) must NOT call Errorf (i.e. must not fail the real
// test), must be tallied as expected-fail, and must be logged against its
// issue.
//
// Mutation-tested: temporarily changing expectKnownFailureInto's `if ok {`
// branch to `if !ok {` (inverting which outcome is treated as "fixed") makes
// this test fail — ft.errored becomes true, caught by the assertion below by
// name. Reverted after confirming.
func TestExpectKnownFailureRecordsExpectedFailWithoutFailingTest(t *testing.T) {
	var s summary
	ft := &fakeVerdictReporter{}
	expectKnownFailureInto(ft, &s, "#999999", false, "synthetic known-bug message")

	if ft.errored {
		t.Fatalf("expectKnownFailure(ok=false) called Errorf (would fail the real test): %q", ft.errMsg)
	}
	if len(ft.logged) == 0 {
		t.Fatal("expectKnownFailure(ok=false) logged nothing — an expected-fail must still be visible in -v output")
	}
	got := s.snapshot()
	if got.expectedFail != 1 {
		t.Fatalf("expectKnownFailure(ok=false) did not tally an expected-fail: %+v", got)
	}
	if got.fail != 0 {
		t.Fatalf("expectKnownFailure(ok=false) incorrectly tallied a fail: %+v", got)
	}
}

// TestExpectKnownFailurePassingVerdictFailsLoudly pins the semantics table's
// third row: a marked scenario whose verdict now PASSES (ok == true, the bug
// appears fixed) must call Errorf — fail loudly, instructing whoever sees it
// to remove the marker — never be absorbed.
//
// Mutation-tested: temporarily changing expectKnownFailureInto to
// unconditionally call s.recordExpectedFail/t.Logf regardless of ok (i.e.
// making it absorb EVERY outcome — the "broken implementation" mutation
// CLAUDE.md's brief warns about) makes this test fail: ft.errored stays
// false, caught by the assertion below by name. Reverted after confirming.
func TestExpectKnownFailurePassingVerdictFailsLoudly(t *testing.T) {
	var s summary
	ft := &fakeVerdictReporter{}
	expectKnownFailureInto(ft, &s, "#999999", true, "synthetic message")

	if !ft.errored {
		t.Fatal("expectKnownFailure(ok=true) did not call Errorf — a marked scenario whose bug " +
			"appears fixed must fail loudly so its marker gets removed, never pass silently")
	}
	if !strings.Contains(ft.errMsg, "#999999") || !strings.Contains(ft.errMsg, "remove its") {
		t.Fatalf("expectKnownFailure(ok=true)'s failure message does not name the issue and instruct "+
			"removing the marker: %q", ft.errMsg)
	}
	got := s.snapshot()
	if got.fail != 1 {
		t.Fatalf("expectKnownFailure(ok=true) did not tally a fail: %+v", got)
	}
}

// fakeFailedReporter implements failedReporter with a settable outcome,
// standing in for a *testing.T that either did or did not already fail.
type fakeFailedReporter struct{ failed bool }

func (f fakeFailedReporter) Failed() bool { return f.failed }

// TestRecordScenarioNeverAbsorbsASetupFailure pins recordScenario's contract:
// a reporter that already reads as failed (the shape of a *testing.T after
// any setup helper in this package calls t.Fatalf) must be tallied as a fail,
// never a pass.
//
// Mutation-tested: temporarily changing recordScenarioInto to unconditionally
// call s.recordPass() (ignoring Failed() entirely — the "absorbs everything"
// broken implementation this row of CLAUDE.md's mutation list warns about)
// makes this test fail: got.fail stays 0 and got.pass becomes 1, caught by
// the assertions below by name. Reverted after confirming.
func TestRecordScenarioNeverAbsorbsASetupFailure(t *testing.T) {
	var s summary
	recordScenarioInto(fakeFailedReporter{failed: true}, &s)

	got := s.snapshot()
	if got.fail != 1 {
		t.Fatalf("recordScenario did not tally an already-failed reporter as a fail: %+v", got)
	}
	if got.pass != 0 {
		t.Fatalf("recordScenario incorrectly tallied an already-failed reporter as a pass: %+v", got)
	}
}

// TestRecordScenarioTalliesARealPass is the paired positive control: a
// reporter that has NOT failed must be tallied as a pass.
func TestRecordScenarioTalliesARealPass(t *testing.T) {
	var s summary
	recordScenarioInto(fakeFailedReporter{failed: false}, &s)

	got := s.snapshot()
	if got.pass != 1 {
		t.Fatalf("recordScenario did not tally a healthy reporter as a pass: %+v", got)
	}
	if got.fail != 0 {
		t.Fatalf("recordScenario incorrectly tallied a healthy reporter as a fail: %+v", got)
	}
}
