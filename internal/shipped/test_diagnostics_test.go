package shipped

import (
	"strings"
	"testing"
)

// After a test session, xcodebuild starts its own child, `simctl diagnose -l -b
// --timeout=600`, to collect simulator diagnostics into the xcresult, on a
// failing run always and on a passing one intermittently. On the pinned iOS 27.0
// runtime that child outlived the rendered 10-minute step: the suite had
// finished in under 30 s and the job sat red or hung until the step was killed,
// which also orphaned the diagnose process on the shared runner.
//
// `-collect-test-diagnostics never` removes the collection and nothing else.
// Measured on a throwaway app with a planted host crash (iOS 27.0): the crash
// report, with its faulting thread, stays attached to the failing test in the
// xcresult and in ~/Library/Logs/DiagnosticReports, and the run exits in about
// 90 s instead of about 640 s. `on-failure` was measured too and does not help:
// a host crash is a failure, so it collects exactly as the default does.

const collectDiagnosticsFlag = "-collect-test-diagnostics"

// value is the argument that follows flag, or "" if the call does not carry it.
func (c xcodebuildCall) value(flag string) string {
	for i, a := range c.args {
		if a == flag && i+1 < len(c.args) {
			return c.args[i+1]
		}
	}
	return ""
}

// runsTests reports whether the call starts a simulator test session, which is
// the only thing that triggers post-run diagnostics collection.
func (c xcodebuildCall) runsTests() bool {
	switch c.action() {
	case "test", "test-without-building":
		return true
	}
	return false
}

// Every rendered `xcodebuild test` call, in every job and every rendering,
// carries `-collect-test-diagnostics never`. One call without it is the one
// that wedges its job, and nothing else about that job looks different.
func TestEveryXcodebuildTestCallSkipsPostRunDiagnostics(t *testing.T) {
	renders := map[string]string{
		"ci.yml (solo)":     renderIOSCI(t, soloConfig()),
		"ci.yml (products)": renderIOSCI(t, twoIOSProducts()),
		"ci.yml (watch)":    renderIOSCI(t, watchProject()),
	}
	jobsSeen := map[string]bool{}
	for name, rendered := range renders {
		doc := parseSPMDoc(t, rendered)
		for job, j := range doc.Jobs {
			for _, st := range j.Steps {
				for _, c := range xcodebuildCalls(st) {
					if !c.runsTests() {
						continue
					}
					jobsSeen[job] = true
					if got := c.value(collectDiagnosticsFlag); got != "never" {
						t.Errorf("%s: job %s, step %q runs `xcodebuild %s` with %s %q, want \"never\": the post-run `simctl diagnose` can outlive the step and wedge the job",
							name, job, st.Name, strings.Join(c.args, " "), collectDiagnosticsFlag, got)
					}
				}
			}
		}
	}
	// Not vacuous: both jobs that run a test session were found. A parser that
	// matched nothing would pass every assertion above.
	for _, job := range []string{"test", "watch-test"} {
		if !jobsSeen[job] {
			t.Errorf("found no `xcodebuild test` call in the %q job; the workflow changed shape or this test's parser stopped matching", job)
		}
	}
}
