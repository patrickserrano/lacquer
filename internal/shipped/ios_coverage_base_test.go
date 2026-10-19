package shipped

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// The coverage ratchet's base compare (#571), driven end to end: the RENDERED
// Check Coverage step, the REAL lacquer binary built from this tree, and a real
// checkout shaped like the one actions/checkout makes for a pull_request run: a
// depth-1 fetch of GitHub's PR merge commit, whose first parent (the base) is
// therefore not in the clone. A stub stands in for xcrun only.
//
// Before #571 a PR that deleted .lacquer.ratchet.toml read as "not enrolled"
// and passed, and a PR that hand-raised a number passed with no reason. These
// run the step as CI does, so dropping the flag from the step, or the compare
// from the binary, fails here.

var (
	gateBinOnce sync.Once
	gateBin     string
	gateBinErr  error
)

// lacquerBinary builds cmd/lacquer from this tree once per test binary.
func lacquerBinary(t *testing.T) string {
	t.Helper()
	gateBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "lacquer-gate-bin-")
		if err != nil {
			gateBinErr = err
			return
		}
		root, err := filepath.Abs("../..")
		if err != nil {
			gateBinErr = err
			return
		}
		bin := filepath.Join(dir, "lacquer")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/lacquer")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			gateBinErr = fmt.Errorf("go build ./cmd/lacquer: %w\n%s", err, out)
			return
		}
		gateBin = dir
	})
	if gateBinErr != nil {
		t.Fatal(gateBinErr)
	}
	return gateBin
}

const (
	baseManifest = "[project]\nname = \"demo\"\nproject_name = \"Demo\"\nscheme = \"Demo\"\nxcodeproj = \"Demo.xcodeproj\"\n\n" +
		"[[component]]\npath = \".\"\nprofiles = [\"ios\"]\n"
	// The stub report has 1,000 executable lines, so the slack is 20 and a
	// measurement of 100 uncovered is 90%, above the 80% floor.
	ratchetAt100 = "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines = 100\n"
	ratchetAt150 = "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines = 150\n"
)

// prCheckout is a depth-1 checkout of a PR merge commit whose base enrolls
// ios_uncovered_lines at 100 and whose PR writes files (an empty body deletes).
func prCheckout(t *testing.T, files map[string]string) string {
	t.Helper()
	base := map[string]string{".lacquer.toml": baseManifest, ".lacquer.ratchet.toml": ratchetAt100}
	return gittest.NewPR(t, base, func(work string) { gittest.Write(t, work, files) }).Checkout
}

// runGateStep runs the rendered Check Coverage step in the checkout, with the
// real lacquer and an xcrun stub reporting `uncovered` of 1,000 lines.
func runGateStep(t *testing.T, co, event string, uncovered int) (out, summary string, code int) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	run := stepRun(t, parseIOSCI(t, soloConfig()), "test", "Check Coverage")
	stubs := t.TempDir()
	stubTool(t, stubs, "xcrun", fmt.Sprintf(`echo '{"targets":[{"name":"Demo.app","executableLines":1000,"coveredLines":%d,"lineCoverage":0.9}]}'`+"\n", 1000-uncovered))
	floor := filepath.Join(co, ".lacquer-checkout", "profiles", "ios", "baseline.toml")
	if err := os.MkdirAll(filepath.Dir(floor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(floor, []byte("[baseline]\ncoverage_floor = 80\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "step.sh")
	if err := os.WriteFile(script, []byte(run), 0o755); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(t.TempDir(), "summary.md")
	cmd := exec.Command("bash", "-e", script)
	cmd.Dir = co
	cmd.Env = append(os.Environ(), "PATH="+stubs+":"+lacquerBinary(t)+":"+os.Getenv("PATH"),
		"GITHUB_EVENT_NAME="+event, "GITHUB_STEP_SUMMARY="+summaryPath, "TEST_RESULT=passed")
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(summaryPath)
	return buf.String(), string(data), code
}

// Positive control: a PR that leaves the ratchet alone passes through the same
// checkout, fetch and compare. Without it every failure below could be the
// fixture failing.
func TestCoverageStepPassesAPRThatLeavesTheRatchetAlone(t *testing.T) {
	co := prCheckout(t, map[string]string{"README.md": "docs\n"})
	out, _, code := runGateStep(t, co, "pull_request", 100)
	if code != 0 {
		t.Fatalf("step exited %d for a PR that does not touch the ratchet:\n%s", code, out)
	}
}

// Hole 2: deleting the file read as "not enrolled", green, floor included.
func TestCoverageStepFailsAPRThatDeletesTheRatchetFile(t *testing.T) {
	co := prCheckout(t, map[string]string{".lacquer.ratchet.toml": ""})
	out, _, code := runGateStep(t, co, "pull_request", 100)
	if code == 0 || !strings.Contains(out, "ios_uncovered_lines is enrolled on the PR's base (100) and this PR deletes .lacquer.ratchet.toml") {
		t.Fatalf("step exited %d; want a failure naming the deleted key:\n%s", code, out)
	}
}

// Hole 3: a hand-raised number records no reason, and nothing checked for one.
// The measurement sits inside the raised band, so only the base compare can
// fail it.
func TestCoverageStepFailsAHandRaisedNumberWithoutAReason(t *testing.T) {
	co := prCheckout(t, map[string]string{".lacquer.ratchet.toml": ratchetAt150})
	out, _, code := runGateStep(t, co, "pull_request", 150)
	if code == 0 || !strings.Contains(out, "ios_uncovered_lines is raised 100 → 150 on this PR without a new reason") {
		t.Fatalf("step exited %d; want a failure naming the unreasoned raise:\n%s", code, out)
	}
}

// The sanctioned path: a raise with a new reason passes, says so in the job
// summary, and leaves the machine-readable annotation the harvest reads.
func TestCoverageStepReportsALoosenWithANewReason(t *testing.T) {
	co := prCheckout(t, map[string]string{
		".lacquer.ratchet.toml": ratchetAt150 + "\n[reasons]\nios_uncovered_lines = \"vendored parser, 100% untestable\"\n",
	})
	out, summary, code := runGateStep(t, co, "pull_request", 150)
	if code != 0 {
		t.Fatalf("step exited %d for a loosen with a new reason:\n%s", code, out)
	}
	if !strings.Contains(summary, "loosened ios_uncovered_lines 100→150: vendored parser, 100% untestable") {
		t.Errorf("the job summary does not show the loosening:\n%s", summary)
	}
	want := `::notice title=lacquer ratchet loosened::lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines","from":100,"to":150,"reason":"vendored parser, 100%25 untestable"}`
	if !strings.Contains(out, want) {
		t.Errorf("the log lacks the machine-readable line\n%s\nin:\n%s", want, out)
	}
}

// A fetch that fails must fail the gate, naming the fetch: it must never read
// as "the base was not enrolled", which would pass a deleted file.
func TestCoverageStepFailsWhenTheBaseCannotBeFetched(t *testing.T) {
	co := prCheckout(t, map[string]string{".lacquer.ratchet.toml": ""})
	gittest.Git(t, co, "remote", "set-url", "origin", "file://"+filepath.Join(t.TempDir(), "gone.git"))
	out, _, code := runGateStep(t, co, "pull_request", 100)
	if code == 0 || !strings.Contains(out, "could not fetch the base commit") {
		t.Fatalf("step exited %d; want a failure naming the fetch:\n%s", code, out)
	}
}

// No base ref: on push (and dispatch) the step behaves exactly as before #571,
// so a deleted file still reads as not enrolled and passes.
func TestCoverageStepDoesNotCompareOffAPullRequest(t *testing.T) {
	for _, event := range []string{"push", "workflow_dispatch", "schedule"} {
		t.Run(event, func(t *testing.T) {
			co := prCheckout(t, map[string]string{".lacquer.ratchet.toml": ""})
			out, _, code := runGateStep(t, co, event, 100)
			if code != 0 || !strings.Contains(out, "coverage gate not enrolled") || strings.Contains(out, "PR's base") {
				t.Fatalf("step exited %d; want today's not-enrolled warning and no base compare:\n%s", code, out)
			}
		})
	}
}
