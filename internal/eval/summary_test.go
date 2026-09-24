//go:build eval

package eval

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Run real failing tests in a subprocess so testing.T's failure propagation
// cannot turn the parent regression test red when the tally is correct.
func TestSummaryReportsFailures(t *testing.T) {
	for _, mode := range []string{"setup", "verdict", "cleanup", "subtest", "unregistered"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSummaryFailureFixture$", "-test.v")
			cmd.Env = append(os.Environ(), "LACQUER_EVAL_FAILURE="+mode)
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
				t.Fatalf("want test exit 1, got %v:\n%s", err, out)
			}
			want := "eval summary: 0 pass, 0 expected-fail, 1 fail"
			if mode == "unregistered" {
				want = "eval summary: 0 pass, 0 expected-fail, 0 fail"
			}
			if !strings.Contains(string(out), want) {
				t.Errorf("failure missing from summary:\n%s", out)
			}
			if !strings.Contains(string(out), "eval package: FAIL (exit 1)") {
				t.Errorf("package failure missing from report:\n%s", out)
			}
		})
	}
}

func TestSummaryFailureFixture(t *testing.T) {
	mode := os.Getenv("LACQUER_EVAL_FAILURE")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	if mode == "unregistered" {
		t.Fatal("mutated failure before registration")
	}
	recordScenario(t)
	switch mode {
	case "setup":
		t.Fatal("mutated setup")
	case "verdict":
		t.Error("mutated verdict")
	case "subtest":
		t.Run("parallel", func(t *testing.T) { t.Parallel(); t.Fatal("mutated subtest") })
	case "cleanup":
		t.Cleanup(func() { t.Error("mutated cleanup") })
	default:
		t.Fatalf("unknown failure mode %q", mode)
	}
}
