package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The iOS coverage gate (#522 U1) and the two annotation fixes that ride with
// it. Each test EXECUTES the rendered step against stubs on PATH, because what
// matters is what the step does with an exit code, not what its text says.

// runStep runs a rendered step body the way GitHub's default shell does
// (`bash -e {0}`), in a scratch directory, with bin first on PATH.
func runCoverageStep(t *testing.T, body, bin string, env ...string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + bin + ":" + os.Getenv("PATH")}, env...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func stubTool(t *testing.T, bin, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// coverageStubs puts an xcrun that prints a valid xccov report, and a lacquer
// that records its argv and exits with $STUB_EXIT, on PATH.
func coverageStubs(t *testing.T) (bin, argv string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	bin = t.TempDir()
	argv = filepath.Join(bin, "argv")
	stubTool(t, bin, "xcrun", `echo '{"targets":[{"name":"Demo.app","executableLines":100,"coveredLines":90,"lineCoverage":0.9}]}'`+"\n")
	stubTool(t, bin, "lacquer", `printf '%s\n' "LACQUER_ROOT=$LACQUER_ROOT" "$@" > "`+argv+`"; exit "${STUB_EXIT:-0}"`+"\n")
	return bin, argv
}

func TestCheckCoverageStepCallsTheLacquerBinary(t *testing.T) {
	for name, tc := range map[string]struct {
		run, product string
		env          []string
	}{
		"lone product": {stepRun(t, parseIOSCI(t, soloConfig()), "test", "Check Coverage"), "-", nil},
		"matrix leg":   {stepRun(t, parseIOSCI(t, twoIOSProducts()), "test", "Check Coverage"), "free", []string{"PRODUCT_SLUG=free", "APP_TARGET=Demo.app"}},
	} {
		t.Run(name, func(t *testing.T) {
			bin, argv := coverageStubs(t)
			summary := filepath.Join(t.TempDir(), "summary.md")
			env := append([]string{"GITHUB_STEP_SUMMARY=" + summary, "GITHUB_EVENT_NAME=pull_request", "TEST_RESULT=passed"}, tc.env...)
			out, code := runCoverageStep(t, tc.run, bin, env...)
			if code != 0 {
				t.Fatalf("step exited %d with the gate passing:\n%s", code, out)
			}
			data, err := os.ReadFile(argv)
			if err != nil {
				t.Fatalf("the step never ran the lacquer binary: %v\n%s", err, out)
			}
			got := "\n" + string(data)
			for _, want := range []string{
				"\nratchet\n", "\n--coverage-report\ncoverage-report.json\n", "\n--product\n" + tc.product + "\n",
				"\n--ci-event\npull_request\n", "\n--test-result\npassed\n", "\n--summary\n" + summary + "\n",
				"/.lacquer-checkout\n",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("lacquer argv lacks %q:\n%s", want, data)
				}
			}
			for _, legacy := range []string{"bc -l", "below 80% threshold"} {
				if strings.Contains(tc.run, legacy) {
					t.Errorf("the step still carries the shell comparison %q", legacy)
				}
			}

			out, code = runCoverageStep(t, tc.run, bin, append(env, "STUB_EXIT=4")...)
			if code == 0 {
				t.Fatalf("the gate exited 4 and the step still passed:\n%s", out)
			}
		})
	}
}

// The binary the step runs is downloaded and checksummed, and the floor comes
// from a checkout at the same locked tag. Each must be wired to that tag.
func TestCoverageGateFetchesTheLockedLacquer(t *testing.T) {
	doc := parseIOSCI(t, soloConfig())
	with := stepWith(t, doc, "test", "Fetch the coverage floor")
	if with["ref"] != "${{ steps.covrel.outputs.tag }}" || with["repository"] != "patrickserrano/lacquer" || with["path"] != ".lacquer-checkout" {
		t.Errorf("the floor checkout is not pinned to the locked tag: %v", with)
	}
	if !strings.Contains(with["sparse-checkout"], "profiles/ios/baseline.toml") {
		t.Errorf("the floor checkout does not fetch profiles/ios/baseline.toml: %v", with)
	}
	fetch := stepRun(t, doc, "test", "Fetch the lacquer for the coverage gate")
	for _, want := range []string{"set -euo pipefail", "sha256sum -c", "$GITHUB_PATH"} {
		if !strings.Contains(fetch, want) {
			t.Errorf("the binary fetch lacks %q", want)
		}
	}
	if !strings.Contains(stepRun(t, doc, "lint", "Read the baseline relaxations"), "strict_concurrency coverage; do") {
		t.Error("the relaxations step does not read [baseline.relax].coverage")
	}
}

// Small item A: the drift step says which exit code the audit returned, in the
// same vocabulary ci-ok uses for the verdict.
func TestDriftStepPrintsTheAuditExitCode(t *testing.T) {
	doc := parseIOSCI(t, soloConfig())
	var run string
	for _, st := range doc.Jobs["changes"].Steps {
		if st.ID == "drift" {
			run = st.Run
		}
	}
	_, tail, ok := strings.Cut(run, "\ncode=$?\n")
	if !ok {
		t.Fatal("the drift step has no `code=$?` line")
	}
	out, _ := runCoverageStep(t, "set +e\nresult=fail\ncode=4\n"+tail, t.TempDir(), "GITHUB_OUTPUT=/dev/null", "LACQUER_TAG=v1.0.0")
	if !strings.Contains(out, "lacquer audit exit=4") {
		t.Errorf("the drift step did not print the audit's exit code:\n%s", out)
	}
	ciOK := doc.Jobs["ci-ok"].Steps
	var all string
	for _, st := range ciOK {
		all += st.Run
	}
	if !strings.Contains(all, `echo "drift=${drift:-<not run>} (the Detect changed paths job prints the audit's exit code)"`) {
		t.Error("ci-ok's drift line does not point at where the exit code is printed")
	}
}

// Small item B: an out-of-date Package.resolved is named as such, not as a
// generic "cannot verify the baseline".
func TestBaselineStepNamesResolvedFileDrift(t *testing.T) {
	run := stepRun(t, parseIOSCI(t, soloConfig()), "lint", "Assert the project baseline")
	for name, tc := range map[string]struct{ stderr, want, notWant string }{
		"resolved drift": {
			"xcodebuild: error: Could not resolve package dependencies:\n  an out-of-date resolved file was detected at /w/Demo.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved, which is not allowed when automatic dependency resolution is disabled",
			"::error::Package.resolved is out of date: xcodebuild refused -onlyUsePackageVersionsFromResolvedFile. Resolve packages locally and commit Demo.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved.",
			"cannot verify the baseline",
		},
		"any other failure": {
			"xcodebuild: error: The project named \"Demo\" does not contain a scheme named \"Demo\".",
			"::error::xcodebuild -showBuildSettings failed; cannot verify the baseline.",
			"Package.resolved is out of date",
		},
	} {
		t.Run(name, func(t *testing.T) {
			bin := t.TempDir()
			stubTool(t, bin, "xcodebuild", "cat >&2 <<'EOF'\n"+tc.stderr+"\nEOF\nexit 74\n")
			out, code := runCoverageStep(t, run, bin)
			if code == 0 {
				t.Fatalf("the step passed with xcodebuild failing:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
			if strings.Contains(out, tc.notWant) {
				t.Errorf("unexpected %q in:\n%s", tc.notWant, out)
			}
			if !strings.Contains(out, "does not contain a scheme") && !strings.Contains(out, "out-of-date resolved file") {
				t.Errorf("the captured stderr was not printed:\n%s", out)
			}
		})
	}
}
