//go:build eval

package eval

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Scenario: vacuous-filter.
//
// `go test -run <filter>` where the filter matches zero tests prints `ok` and
// exits 0 — Go's documented behaviour, not a bug in Go. The defect is
// downstream: treating that `ok`/exit-0 pair as "the test passed" rather than
// "the test never ran." This brief calls out that getting this specific check
// wrong here would be self-refuting, so the grader below is the one place in
// this suite where the fixture IS the mechanism under test: a real `go test`
// subprocess against a real throwaway module, not a simulation of one.
//
// Known-correct verdict (machine-checkable): exit 0 alone must not be read as
// "passed." Counting `=== RUN` lines in `-v` output (the same technique this
// task's own brief specifies) distinguishes a real pass (count > 0) from a
// vacuous one (count == 0, exit 0) — and the two must be told apart.
func TestScenarioVacuousFilter(t *testing.T) {
	defer recordScenario(t) // unmarked: tallied into the package summary as-is.
	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, "go.mod"), "module vacuousfixture\n\ngo 1.23\n")
	writeFile(t, filepath.Join(mod, "probe_test.go"),
		"package probe\n\nimport \"testing\"\n\nfunc TestRealThing(t *testing.T) {\n\tif 1+1 != 2 {\n\t\tt.Fatal(\"arithmetic broke\")\n\t}\n}\n")

	// The filter that matches nothing: a plausible typo'd or stale test name.
	vacuous := goTestRun(t, mod, "TestThisNameDoesNotExist")
	// The filter that matches the one real test.
	real := goTestRun(t, mod, "TestRealThing")

	if vacuous.exit != 0 {
		t.Fatalf("setup failed: `go test -run <non-matching>` exited %d, want 0 (Go's own documented behaviour for a vacuous match):\n%s", vacuous.exit, vacuous.output)
	}
	if !strings.Contains(vacuous.output, "ok") {
		t.Fatalf("setup failed: vacuous run's output does not even say \"ok\" — the premise this scenario tests against does not hold here:\n%s", vacuous.output)
	}
	if real.exit != 0 || real.runs == 0 {
		t.Fatalf("setup failed: the REAL test did not actually run (exit=%d runs=%d) — the fixture module is broken:\n%s", real.exit, real.runs, real.output)
	}

	// The verdict: a naive grader (exit code only) calls the vacuous run a
	// pass. The correct grader (=== RUN count) must not.
	naiveWouldPass := vacuous.exit == 0
	if !naiveWouldPass {
		t.Fatal("setup failed: the naive exit-code-only check did not even reproduce the trap this scenario exists to catch")
	}
	if vacuous.runs != 0 {
		t.Fatalf("setup failed: the non-matching filter somehow ran %d test(s) — fixture is not testing a vacuous match", vacuous.runs)
	}
	// This is the actual assertion: the correct verdict function must label
	// the vacuous run as NOT a pass, despite exit 0.
	if verdict := vacuousFilterVerdict(vacuous.exit, vacuous.runs); verdict != "vacuous" {
		t.Errorf("verdict not reached: a `go test -run` invocation matching zero tests "+
			"(exit 0, 0 \"=== RUN\" lines) was classified %q instead of \"vacuous\" — "+
			"exit code alone was trusted as proof of a passing test", verdict)
	}
	if verdict := vacuousFilterVerdict(real.exit, real.runs); verdict != "pass" {
		t.Errorf("a real, matching test run (exit 0, %d \"=== RUN\" line(s)) was classified %q instead of \"pass\"", real.runs, verdict)
	}
}

// vacuousFilterVerdict is the correct classifier this scenario grades: it is
// deliberately small enough to read at a glance, because the whole point is
// that "did it actually run" requires looking past the exit code.
func vacuousFilterVerdict(exitCode, runCount int) string {
	if exitCode != 0 {
		return "fail"
	}
	if runCount == 0 {
		return "vacuous"
	}
	return "pass"
}

type goTestResult struct {
	exit   int
	output string
	runs   int
}

func goTestRun(t *testing.T, dir, filter string) goTestResult {
	t.Helper()
	cmd := exec.Command("go", "test", "-run", "^"+filter+"$", "-v", "./...")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("setup failed: run go test in %s: %v", dir, err)
		}
	}
	text := out.String()
	runs := strings.Count(text, "=== RUN   ")
	return goTestResult{exit: exit, output: text, runs: runs}
}

// Mutation-tested: changing vacuousFilterVerdict to `if exitCode != 0 {
// return "fail" }; return "pass"` (dropping the runCount==0 branch entirely —
// the naive, exit-code-only classifier) makes the second-to-last assertion in
// TestScenarioVacuousFilter fail with "was classified \"pass\" instead of
// \"vacuous\"", confirming the runCount check is load-bearing. Reverted after
// confirming.
