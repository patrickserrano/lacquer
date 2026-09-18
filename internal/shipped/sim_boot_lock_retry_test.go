package shipped

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// The fleet's Mac runners share one machine. Every iOS Test job, and every watch
// leg, used to create and COLD-BOOT a fresh simulator the moment it started. On
// the iOS 27.0 runtime a first boot is ~250 processes and heavy indexing, and
// several of them at once pushed the host to load ~700: flare's tests then died
// at bootstrap ("... before establishing connection" / "never finished
// bootstrapping") with zero tests run, twice. Separately, rail's suite went
// silent MID-run twice, and the heartbeat just printed "still running" until the
// step timeout: ~12.5 minutes of Mac time per stall, and no evidence of why.
//
// Three behaviours answer that, and every one of them is a way to be wrong
// silently, so these tests run the SHIPPED shell rather than read it:
//
//   - a host-wide lock around create + boot + readiness (not around the tests),
//     which a killed job must never leave held;
//   - a stall detector on the tee'd xcodebuild log that captures diagnostics and
//     FAILS, and never retries, because a retry could hide a real hang;
//   - exactly one retry, and only for the pre-connection signature with ZERO
//     tests executed.
//
// The helpers are one shell library, written by the step that creates the
// simulator and sourced by the steps after it. It ships in two places, the iOS
// Test job in ci.yml and the watch-test job in internal/tokens/watch.go, and
// the first test here requires the two copies to be byte-identical, so the lock
// path and every constant are one fact.

const simLibOpen = "<<'LACQUER_SIM_LIB'\n"
const simLibClose = "\nLACQUER_SIM_LIB\n"

// simLibFrom extracts the library's heredoc body from a step's script.
func simLibFrom(t *testing.T, where, script string) string {
	t.Helper()
	i := strings.Index(script, simLibOpen)
	if i < 0 {
		t.Fatalf("%s does not write the simulator library (no %q heredoc)", where, strings.TrimSpace(simLibOpen))
	}
	body := script[i+len(simLibOpen):]
	j := strings.Index(body, simLibClose)
	if j < 0 {
		t.Fatalf("%s opens the simulator library heredoc and never closes it", where)
	}
	return body[:j+1]
}

// simStep is a step, typed far enough for these tests.
type simStep struct {
	ID             string            `yaml:"id"`
	Name           string            `yaml:"name"`
	If             string            `yaml:"if"`
	Run            string            `yaml:"run"`
	Uses           string            `yaml:"uses"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	With           map[string]string `yaml:"with"`
}

type simJob struct {
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Env            map[string]string `yaml:"env"`
	Steps          []simStep         `yaml:"steps"`
}

func simJobs(t *testing.T, cfg *config.Config) map[string]simJob {
	t.Helper()
	var doc struct {
		Jobs map[string]simJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(renderIOSCI(t, cfg)), &doc); err != nil {
		t.Fatalf("rendered ci.yml is not valid YAML: %v", err)
	}
	return doc.Jobs
}

func simStepNamed(t *testing.T, job simJob, jobName, step string) simStep {
	t.Helper()
	for _, s := range job.Steps {
		if s.Name == step {
			return s
		}
	}
	t.Fatalf("no %q step in the %s job", step, jobName)
	return simStep{}
}

// simTargets is every job that boots a simulator: which step creates it, which
// runs the suite, and what the suite's files and artifact are called.
type simTarget struct {
	job, setup, run, log, xcresult, artifact, upload string
}

var iosSimTarget = simTarget{
	job: "test", setup: "Setup Simulator", run: "Run Tests",
	log: "xcodebuild.log", xcresult: "TestResults.xcresult",
	artifact: "ios-test-diagnostics", upload: "Upload test diagnostics",
}

var watchSimTarget = simTarget{
	job: "watch-test", setup: "Create an UNPAIRED watch simulator", run: "Run Watch Tests",
	log: "watch-xcodebuild.log", xcresult: "WatchTestResults.xcresult",
	artifact: "watch-test-diagnostics-${{ matrix.watch.slug }}", upload: "Upload watch test diagnostics",
}

func TestSimLibIsIdenticalInEveryJob(t *testing.T) {
	ios := simJobs(t, soloConfig())
	watch := simJobs(t, watchProject())

	iosLib := simLibFrom(t, "the iOS Setup Simulator step",
		simStepNamed(t, ios["test"], "test", iosSimTarget.setup).Run)
	watchLib := simLibFrom(t, "the watch simulator step",
		simStepNamed(t, watch["watch-test"], "watch-test", watchSimTarget.setup).Run)

	if iosLib != watchLib {
		t.Errorf("the simulator library differs between the iOS Test job and the watch-test job. "+
			"They share ONE host-wide lock, so a different lock path or constant makes them two "+
			"locks that do not exclude each other.\n%s", firstDiff(iosLib, watchLib))
	}
	for _, want := range []string{"SIM_BOOT_LOCK=/tmp/", "sim_boot_lock_acquire()", "sim_test_run()"} {
		if !strings.Contains(iosLib, want) {
			t.Errorf("the simulator library carries no %q; this test would compare two empty shells", want)
		}
	}
	// The watch job is a Go string with its own placeholders; neither side's
	// substitution syntax may leak into text that must match the other.
	for _, bad := range []string{"{{", "@@", "${{", "`"} {
		if strings.Contains(iosLib, bad) {
			t.Errorf("the simulator library contains %q, which one of its two homes would substitute or cannot hold", bad)
		}
	}
}

// libConst reads NAME=value from the library.
func libConst(t *testing.T, lib, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^`+name+`=(.*)$`).FindAllStringSubmatch(lib, -1)
	if len(m) != 1 {
		t.Fatalf("expected exactly one %s= assignment in the simulator library, found %d", name, len(m))
	}
	return m[0][1]
}

func libSeconds(t *testing.T, lib, name string) int {
	t.Helper()
	v, err := strconv.Atoi(libConst(t, lib, name))
	if err != nil {
		t.Fatalf("%s is not a whole number of seconds: %v", name, err)
	}
	return v
}

// TestSimBootIsLockedAndOnlyTheBoot pins the shape of the setup steps: the lock
// is taken before the device is created, the boot runs with the lock's
// descriptor closed, and the lock is released before the step ends. The tests
// still run concurrently; only the cold boot is serialised.
func TestSimBootIsLockedAndOnlyTheBoot(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
	}{{soloConfig(), iosSimTarget}, {watchProject(), watchSimTarget}} {
		t.Run(tc.tg.job, func(t *testing.T) {
			script := simStepNamed(t, simJobs(t, tc.cfg)[tc.tg.job], tc.tg.job, tc.tg.setup).Run
			// Everything after the library's heredoc: the step's own commands.
			own := script[strings.Index(script, simLibClose)+len(simLibClose):]
			order := []string{
				`. "$RUNNER_TEMP/lacquer-sim.sh"`,
				"sim_boot_lock_acquire",
				"xcrun simctl create",
				"sim_boot_and_wait",
				"sim_boot_lock_release",
			}
			last := -1
			for _, cmd := range order {
				i := commandIndex(own, cmd)
				if i < 0 {
					t.Fatalf("%s does not run %q", tc.tg.setup, cmd)
				}
				if i < last {
					t.Errorf("%s runs %q out of order; want %v", tc.tg.setup, cmd, order)
				}
				last = i
			}
			if !regexp.MustCompile(`(?m)^[^#\n]*sim_boot_and_wait [^\n#]*9<&-`).MatchString(own) {
				t.Errorf("%s boots without closing fd 9 (`sim_boot_and_wait ... 9<&-`). A child that inherits "+
					"the lock's descriptor holds the host-wide lock for as long as it lives.", tc.tg.setup)
			}
		})
	}
}

// commandIndex is the offset of the first real (uncommented) line running cmd.
func commandIndex(script, cmd string) int {
	off := 0
	for _, line := range strings.SplitAfter(script, "\n") {
		if hasCommand(line, cmd) {
			return off
		}
		off += len(line)
	}
	return -1
}

// TestSimTestRunsThroughTheWatchdog pins the run steps: the suite runs through
// sim_test_run with an artifact name the upload step actually uploads, from the
// directory the library writes diagnostics to, whether or not the step failed.
func TestSimTestRunsThroughTheWatchdog(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
	}{{soloConfig(), iosSimTarget}, {twoIOSProducts(), iosSimTarget}, {watchProject(), watchSimTarget}} {
		t.Run(tc.tg.job, func(t *testing.T) {
			jobs := simJobs(t, tc.cfg)
			job := jobs[tc.tg.job]
			lib := simLibFrom(t, tc.tg.setup, simStepNamed(t, job, tc.tg.job, tc.tg.setup).Run)
			run := simStepNamed(t, job, tc.tg.job, tc.tg.run).Run

			if commandIndex(run, `. "$RUNNER_TEMP/lacquer-sim.sh"`) < 0 {
				t.Errorf("%s does not source the simulator library", tc.tg.run)
			}
			call := regexp.MustCompile(`(?m)^\s*sim_test_run "\$DEVICE_ID" (\S+) ` +
				regexp.QuoteMeta(tc.tg.log) + ` ` + regexp.QuoteMeta(tc.tg.xcresult) + ` "([^"]+)" -- \\\n\s*xcodebuild test`).
				FindStringSubmatch(run)
			if call == nil {
				t.Fatalf("%s does not run `xcodebuild test` through sim_test_run with %s and %s", tc.tg.run, tc.tg.log, tc.tg.xcresult)
			}
			if n := strings.Count(run, "xcodebuild test"); n != 1 {
				t.Errorf("%s runs `xcodebuild test` %d times; every run must go through the watchdog", tc.tg.run, n)
			}
			// The run step names it through the job's env (WATCH_SLUG) where the
			// upload step uses the expression directly; resolve one to the other.
			artifact := call[2]
			for k, v := range job.Env {
				artifact = strings.ReplaceAll(artifact, "$"+k, v)
			}

			up := simStepNamed(t, job, tc.tg.job, tc.tg.upload)
			if !strings.HasPrefix(up.Uses, "actions/upload-artifact@") {
				t.Errorf("%s is not an upload-artifact step", tc.tg.upload)
			}
			if !strings.Contains(up.If, "always()") {
				t.Errorf("%s runs `if: %s`; a stall FAILS the run step, so without always() the diagnostics never upload", tc.tg.upload, up.If)
			}
			if up.With["name"] != artifact {
				t.Errorf("the ::error names artifact %q but %s uploads %q", artifact, tc.tg.upload, up.With["name"])
			}
			if dir := libConst(t, lib, "SIM_TEST_DIAG_DIR"); strings.TrimSpace(up.With["path"]) != dir {
				t.Errorf("%s uploads %q, but diagnostics are written to %q", tc.tg.upload, up.With["path"], dir)
			}
		})
	}
}

// TestSimTimeoutsAccountForTheLockAndTheRetry checks the step and job ceilings
// against the library's own constants, so raising a wait cannot silently turn
// into a step timeout that fires first.
func TestSimTimeoutsAccountForTheLockAndTheRetry(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
		// bootBudget is what the setup step was allowed for everything but the
		// lock wait before the lock existed; attemptBudget is one test attempt.
		bootBudget, attemptBudget int
	}{
		{soloConfig(), iosSimTarget, 6, 13},
		{watchProject(), watchSimTarget, 6, 15},
	} {
		t.Run(tc.tg.job, func(t *testing.T) {
			job := simJobs(t, tc.cfg)[tc.tg.job]
			setup := simStepNamed(t, job, tc.tg.job, tc.tg.setup)
			lib := simLibFrom(t, tc.tg.setup, setup.Run)
			wait := libSeconds(t, lib, "SIM_BOOT_LOCK_WAIT_SECONDS")
			stall := libSeconds(t, lib, "SIM_TEST_STALL_SECONDS")
			if stall%60 != 0 {
				t.Errorf("SIM_TEST_STALL_SECONDS=%d is not whole minutes; the ::error reports it in minutes", stall)
			}

			// A step with no timeout-minutes inherits the job's, which is what
			// the watch setup step did before it booted under a lock.
			if setup.TimeoutMinutes*60 < wait+tc.bootBudget*60 {
				t.Errorf("%s timeout-minutes=%d cannot cover a %ds lock wait plus the %d-minute boot budget",
					tc.tg.setup, setup.TimeoutMinutes, wait, tc.bootBudget)
			}
			run := simStepNamed(t, job, tc.tg.job, tc.tg.run)
			// One attempt, then one retry that waits for the lock, reboots, and
			// runs again.
			needRun := 2*tc.attemptBudget*60 + wait
			if run.TimeoutMinutes*60 < needRun {
				t.Errorf("%s timeout-minutes=%d cannot cover two %d-minute attempts plus a %ds lock wait",
					tc.tg.run, run.TimeoutMinutes, tc.attemptBudget, wait)
			}
			// The pre-connection window must outlast xcodebuild's own give-up
			// (421.5s measured on flare), or the one retryable failure is
			// killed as a stall before it can report itself.
			connect := libSeconds(t, lib, "SIM_TEST_CONNECT_SECONDS")
			if connect < 480 || connect <= stall {
				t.Errorf("SIM_TEST_CONNECT_SECONDS=%d must exceed xcodebuild's measured 421.5s pre-connection give-up with margin, and SIM_TEST_STALL_SECONDS=%d", connect, stall)
			}
			if connect%60 != 0 {
				t.Errorf("SIM_TEST_CONNECT_SECONDS=%d is not whole minutes; the ::error reports it in minutes", connect)
			}
			if connect+180 > tc.attemptBudget*60 {
				t.Errorf("an attempt that builds for ~3 minutes and then waits SIM_TEST_CONNECT_SECONDS=%d does not fit the %d-minute attempt budget", connect, tc.attemptBudget)
			}
			if stall >= tc.attemptBudget*60 {
				t.Errorf("SIM_TEST_STALL_SECONDS=%d is not under one attempt's %d minutes; the step timeout would fire first and the diagnostics would never be captured",
					stall, tc.attemptBudget)
			}
			if job.TimeoutMinutes < setup.TimeoutMinutes+run.TimeoutMinutes {
				t.Errorf("the %s job's timeout-minutes=%d is below its setup (%d) plus run (%d) ceilings, so the job backstop fires first",
					tc.tg.job, job.TimeoutMinutes, setup.TimeoutMinutes, run.TimeoutMinutes)
			}
		})
	}
}

// ---- Running the shipped shell against fakes --------------------------------

// simHarness is a temp workspace, a directory of fakes that shadow every tool
// the steps touch, and the environment a step runs with.
type simHarness struct {
	t                        *testing.T
	dir, ws, fakes, state    string
	runnerTemp, lock, output string
	home, crashDir           string
	device                   string
	lib                      string
}

// crashReport is a trimmed copy of flare's real report from 2026-09-18
// (Flare-2026-09-18-031523.ips): a header line, then the body, with the
// escaped slashes ReportCrash writes, for an app installed on device.
func crashReport(app, device string) string {
	return `{"app_name":"` + app + `","timestamp":"2026-09-18 03:15:23.00 -0400","bug_type":"309","name":"` + app + `"}
{
  "procName" : "` + app + `",
  "procPath" : "\/Users\/USER\/Library\/Developer\/CoreSimulator\/Devices\/` + device + `\/data\/Containers\/Bundle\/Application\/4F27B49B-4443-4FCA-8508-A2094E32E489\/` + app + `.app\/` + app + `",
  "exception" : {"codes":"0x0000000000000001, 0x00000001e64510a4","type":"EXC_BREAKPOINT","signal":"SIGTRAP"},
  "faultingThread" : 1,
  "threads" : [
    {"id":1,"queue":"com.apple.main-thread","frames":[{"symbol":"mach_msg2_trap"}]},
    {"id":2,"name":"com.apple.SwiftUI.AsyncRenderer","triggered":true,"frames":[
      {"symbol":"_dispatch_assert_queue_fail"},{"symbol":"dispatch_assert_queue$V2.cold.1"},
      {"symbol":"dispatch_assert_queue"},{"symbol":"_swift_task_checkIsolatedSwift"},
      {"symbol":"swift_task_isCurrentExecutorWithFlagsImpl"},{"symbol":"closure #1 in ` + app + `App.body.getter"}]}
  ]
}
`
}

func newUUID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// testLib rewrites the library's constants to test scale. Every rewrite must
// land exactly once; a constant that moved and was not rewritten would make a
// scenario wait minutes, or run against the REAL host-wide lock.
func testLib(t *testing.T, lib, lock string) string {
	t.Helper()
	for name, val := range map[string]string{
		"SIM_BOOT_LOCK":              lock,
		"SIM_BOOT_LOCK_WAIT_SECONDS": "4",
		"SIM_BOOT_LOCK_POLL_SECONDS": "1",
		"SIM_TEST_STALL_SECONDS":     "3",
		"SIM_TEST_CONNECT_SECONDS":   "8",
		"SIM_TEST_HEARTBEAT_SECONDS": "1",
	} {
		re := regexp.MustCompile(`(?m)^` + name + `=.*$`)
		if n := len(re.FindAllString(lib, -1)); n != 1 {
			t.Fatalf("expected exactly one %s= in the simulator library, found %d", name, n)
		}
		lib = re.ReplaceAllLiteralString(lib, name+"="+val)
	}
	if strings.Contains(lib, "/tmp/lacquer-simulator-boot.lock") {
		t.Fatal("the test library still points at the host's real boot lock")
	}
	return lib
}

func newSimHarness(t *testing.T, tg simTarget, cfg *config.Config) *simHarness {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("jq is required: the run steps parse xcresulttool JSON with it")
	}
	dir := t.TempDir()
	h := &simHarness{
		t: t, dir: dir,
		ws:         filepath.Join(dir, "ws"),
		fakes:      filepath.Join(dir, "fakes"),
		state:      filepath.Join(dir, "state"),
		runnerTemp: filepath.Join(dir, "runner-temp"),
		lock:       filepath.Join(dir, "boot.lock"),
		output:     filepath.Join(dir, "github-output"),
		device:     newUUID(t),
	}
	h.home = filepath.Join(dir, "home")
	h.crashDir = filepath.Join(h.home, "Library", "Logs", "DiagnosticReports")
	for _, d := range []string{h.ws, h.fakes, h.state, h.runnerTemp, h.crashDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(h.output, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setup := simStepNamed(t, simJobs(t, cfg)[tg.job], tg.job, tg.setup).Run
	h.lib = testLib(t, simLibFrom(t, tg.setup, setup), h.lock)
	for name, body := range map[string]string{
		"crash-this.ips":  crashReport("Flare", h.device),
		"crash-other.ips": crashReport("DailyBread", newUUID(t)),
	} {
		if err := os.WriteFile(filepath.Join(h.dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.writeFakes()
	if err := os.WriteFile(filepath.Join(h.runnerTemp, "lacquer-sim.sh"), []byte(h.lib), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *simHarness) fake(name, body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.fakes, name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

func (h *simHarness) writeFakes() {
	// xcodebuild: one MODE per invocation, from FAKE_XCODEBUILD_MODES. It writes
	// the result bundle a real run would leave, so the retry decision reads a
	// bundle rather than an environment variable.
	h.fake("xcodebuild", `
n=$(( $(cat "$FAKE_STATE/xcodebuild.count" 2>/dev/null || echo 0) + 1 ))
echo "$n" >"$FAKE_STATE/xcodebuild.count"
mode=$(printf '%s' "$FAKE_XCODEBUILD_MODES" | cut -d, -f"$n")
if [ ! -e "$FAKE_LOCK" ]; then
  echo free >>"$FAKE_STATE/lock-during-test" # no lock file: nobody has ever taken it
else
  exec 8<"$FAKE_LOCK"
  if lockf -s -t 0 8; then echo free >>"$FAKE_STATE/lock-during-test"; else echo held >>"$FAKE_STATE/lock-during-test"; fi
  exec 8<&-
fi
bundle=""; prev=""
for a in "$@"; do [ "$prev" = "-resultBundlePath" ] && bundle=$a; prev=$a; done
# result <summary-json> <tests-json>: the bundle a real run leaves behind.
result() { mkdir -p "$bundle"; printf '%s' "$1" >"$bundle/fake-summary.json"; printf '%s' "$2" >"$bundle/fake-tests.json"; }
REAL_PASS='{"testNodes":[{"nodeType":"Test Plan","name":"Demo","result":"Passed","children":[{"nodeType":"Unit test bundle","name":"DemoTests","result":"Passed","children":[{"nodeType":"Test Suite","name":"DemoTests","result":"Passed","children":[{"nodeType":"Test Case","name":"test()","result":"Passed"}]}]}]}]}'
REAL_FAIL='{"testNodes":[{"nodeType":"Test Plan","name":"Demo","result":"Failed","children":[{"nodeType":"Unit test bundle","name":"DemoTests","result":"Failed","children":[{"nodeType":"Test Suite","name":"DemoTests","result":"Failed","children":[{"nodeType":"Test Case","name":"test()","result":"Failed"}]}]}]}]}'
# Verbatim shape of flare's real pre-connection failure (run 35303586062):
# ONE failed "Test Case", under the "System Failures" suite.
SYSFAIL='{"devices":[{"deviceName":"CI-iPhone-35303586062","platform":"iOS Simulator"}],"testNodes":[{"children":[{"children":[{"children":[{"children":[{"name":"The test runner hung before establishing connection.","nodeType":"Failure Message"}],"name":"Flare (42625) encountered an error","nodeIdentifier":"Flare (42625) encountered an error","nodeType":"Test Case","result":"Failed"}],"name":"System Failures","nodeType":"Test Suite","result":"Failed"}],"name":"FlareTests","nodeIdentifierURL":"test://com.apple.xcode/Flare/FlareTests","nodeType":"Unit test bundle","result":"Failed"}],"name":"Flare","nodeType":"Test Plan","result":"Failed"}],"testPlanConfigurations":[{"configurationId":"1","configurationName":"Test Scheme Action"}]}'
SOME_THEN_SYSFAIL='{"testNodes":[{"nodeType":"Test Plan","name":"Demo","result":"Failed","children":[{"nodeType":"Unit test bundle","name":"DemoTests","result":"Failed","children":[{"nodeType":"Test Suite","name":"DemoTests","result":"Passed","children":[{"nodeType":"Test Case","name":"first()","result":"Passed"}]},{"nodeType":"Test Suite","name":"System Failures","result":"Failed","children":[{"nodeType":"Test Case","name":"Demo (4243) encountered an error","result":"Failed"}]}]}]}]}'
SUM_PASS='{"result":"Passed","totalTestCount":1,"passedTests":1,"failedTests":0,"skippedTests":0,"expectedFailures":0}'
SUM_FAIL1='{"result":"Failed","totalTestCount":1,"passedTests":0,"failedTests":1,"skippedTests":0,"expectedFailures":0,"testFailures":[]}'
connected() { echo "Test Suite 'All tests' started at 2026-09-18 02:38:18.597."; echo "◇ Test run started."; }
case "$mode" in
  pass)
    connected; echo "✔ Test test() passed after 0.001 seconds."; echo "** TEST SUCCEEDED **"
    result "$SUM_PASS" "$REAL_PASS"; exit 0 ;;
  fail)
    connected; echo "✘ Test test() failed after 0.001 seconds."; echo "** TEST FAILED **"
    result "$SUM_FAIL1" "$REAL_FAIL"; exit 65 ;;
  preconnect)
    echo "Testing started"
    echo "	Flare (42625) encountered an error (Early unexpected exit, operation never finished bootstrapping - no restart will be attempted. (Underlying Error: Test crashed with signal term before establishing connection.))"
    echo "** TEST FAILED **"
    result "$SUM_FAIL1" "$SYSFAIL"; exit 65 ;;
  preconnect-crash|preconnect-other-crash)
    # The app dies at launch; ReportCrash writes its report into the host's
    # DiagnosticReports, which every repository on the Mac shares.
    if [ "$mode" = preconnect-crash ]; then src=$FAKE_CRASH_THIS; else src=$FAKE_CRASH_OTHER; fi
    cp "$src" "$HOME/Library/Logs/DiagnosticReports/$(basename "$src" .ips)-$n.ips"
    echo "Testing started"
    echo "	Flare (42625) encountered an error (Early unexpected exit, operation never finished bootstrapping - no restart will be attempted. (Underlying Error: Test crashed with signal trap before establishing connection.))"
    echo "** TEST FAILED **"
    result "$SUM_FAIL1" "$SYSFAIL"; exit 65 ;;
  preconnect-hang)
    # flare's attempt: silent for longer than the MID-SUITE window, then
    # xcodebuild gives up on its own with the signature.
    echo "Testing started"; sleep 5
    echo "	Flare (42625) encountered an error (The test runner hung before establishing connection.)"
    echo "** TEST FAILED **"
    result "$SUM_FAIL1" "$SYSFAIL"; exit 65 ;;
  preconnect-some)
    connected; echo "✔ Test first() passed after 0.001 seconds."
    echo "	Demo (4243) encountered an error (The test runner crashed before establishing connection)"
    echo "** TEST FAILED **"
    result '{"result":"Failed","totalTestCount":2,"passedTests":1,"failedTests":1,"skippedTests":0,"expectedFailures":0}' "$SOME_THEN_SYSFAIL"; exit 65 ;;
  preconnect-nobundle)
    echo "Early unexpected exit, operation never finished bootstrapping"
    exit 65 ;;
  silent)
    # A helper that inherited xcodebuild's output pipe, as xcodebuild's own
    # children do: the pipeline cannot reach EOF while it lives, so killing
    # xcodebuild alone would leave the step hanging on tee.
    ( sleep 45 ) &
    connected; printf "✔ Test slow() pass"
    while :; do sleep 1; done ;;
  never-connects)
    ( sleep 45 ) &
    echo "Testing started"
    while :; do sleep 1; done ;;
  signature-then-silence)
    echo "The test runner crashed before establishing connection"
    result "$SUM_FAIL1" "$SYSFAIL"
    while :; do sleep 1; done ;;
  steady)
    connected
    for i in 1 2 3 4 5 6 7; do echo "✔ Test t$i() passed after 1.000 seconds."; sleep 1; done
    result "$SUM_PASS" "$REAL_PASS"; exit 0 ;;
  *) echo "fake xcodebuild: no mode for invocation $n" >&2; exit 99 ;;
esac
`)
	// xcrun: simctl and xcresulttool, every call recorded. `simctl boot` leaves
	// a child running, the way a real boot can, and records whether the boot
	// lock was held while it ran.
	h.fake("xcrun", `
echo "$*" >>"$FAKE_STATE/xcrun.calls"
if [ "$1" = "--sdk" ]; then echo 27.0; exit 0; fi
if [ "$1" = "xcresulttool" ]; then
  prev=""; for a in "$@"; do [ "$prev" = "--path" ] && p=$a; prev=$a; done
  case "$*" in
    *"test-results tests"*) f="$p/fake-tests.json" ;;
    *) f="$p/fake-summary.json" ;;
  esac
  [ -f "$f" ] || exit 1
  cat "$f"; exit 0
fi
[ "$1" = "simctl" ] || exit 0
case "$2" in
  list)
    case "$3" in
      runtimes) echo "iOS 27.0 (27.0 - 24A0) - com.apple.CoreSimulator.SimRuntime.iOS-27-0"
                echo "watchOS 27.0 (27.0 - 24R0) - com.apple.CoreSimulator.SimRuntime.watchOS-27-0" ;;
      *) : ;;
    esac ;;
  create) echo "$FAKE_DEVICE_ID" ;;
  boot)
    exec 8<"$FAKE_LOCK"
    if lockf -s -t 0 8; then echo free >>"$FAKE_STATE/lock-during-boot"; else echo held >>"$FAKE_STATE/lock-during-boot"; fi
    exec 8<&-
    ( sleep "${FAKE_BOOT_CHILD_SECONDS:-8}" ) >/dev/null 2>&1 &
    ;;
  spawn)
    case "$*" in
      *"launchctl list"*) echo "123 0 com.apple.SpringBoard"; echo "124 0 com.apple.Carousel" ;;
      *"log show"*) echo "device log line for $3" ;;
    esac ;;
esac
exit 0
`)
	h.fake("xcbeautify", `cat`)
	h.fake("sample", `echo "sample $*" >>"$FAKE_STATE/sample.calls"; prev=""; for a in "$@"; do [ "$prev" = "-file" ] && echo "fake sample of $1" >"$a"; prev=$a; done`)
	// pkill: recorded, and refused outright for any pattern short enough to be
	// machine-wide. The real one then runs, so a stalled fake really dies.
	h.fake("pkill", `
echo "pkill $*" >>"$FAKE_STATE/pkill.calls"
pat="${@: -1}"
if [ "${#pat}" -lt 20 ]; then echo "fake pkill: refusing broad pattern '$pat'" >&2; exit 97; fi
exec `+realTool(h.t, "pkill")+` "$@"
`)
	// lockf is macOS's; on Linux (this repo's CI) stand it in with util-linux
	// flock, which takes the same flock(2) lock on the same descriptor.
	if _, err := exec.LookPath("lockf"); err != nil {
		h.fake("lockf", `
t=-1
while getopts "skt:" o; do case $o in t) t=$OPTARG ;; *) : ;; esac; done
shift $((OPTIND - 1))
if [ "$t" = 0 ]; then exec flock -n -E 75 "$1"; fi
if [ "$t" -gt 0 ]; then exec flock -w "$t" -E 75 "$1"; fi
exec flock "$1"
`)
	}
}

func realTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required: %v", name, err)
	}
	return p
}

func (h *simHarness) env(extra ...string) []string {
	base := append(os.Environ(),
		"PATH="+h.fakes+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GITHUB_WORKSPACE="+h.ws,
		"GITHUB_OUTPUT="+h.output,
		"GITHUB_RUN_ID=424242",
		"GITHUB_REPOSITORY=acme/demo",
		"GITHUB_JOB=test",
		"RUNNER_TEMP="+h.runnerTemp,
		"FAKE_STATE="+h.state,
		"FAKE_LOCK="+h.lock,
		"FAKE_DEVICE_ID="+h.device,
		"FAKE_CRASH_THIS="+filepath.Join(h.dir, "crash-this.ips"),
		"FAKE_CRASH_OTHER="+filepath.Join(h.dir, "crash-other.ips"),
		"HOME="+h.home,
		"WATCH_SLUG=demo",
		"WATCH_READY_SERVICE=com.apple.Carousel",
		"WATCH_DESTINATION_PREFIX=platform=watchOS Simulator",
		"WATCH_SCHEME=Demo Watch",
		"WATCH_TEST_TARGET=DemoWatchTests",
		"WATCH_SIM_PREFIX=CI-Watch",
		"WATCH_DEVICE_TYPE=Apple Watch",
		"WATCH_RUNTIME=com.apple.CoreSimulator.SimRuntime.watchOS-27-0",
	)
	// exec keeps the LAST value of a duplicated key, so extra overrides base.
	return append(base, extra...)
}

// runScript runs a script the way Actions does (`bash -e`), from the
// workspace, with the script file OUTSIDE the workspace so the step's own
// `pkill -f "$GITHUB_WORKSPACE"` cannot match its own shell.
//
// Each script runs in its own process group, and cleanup kills that whole
// group, so a fake the shipped shell failed to stop cannot outlive the test.
// That is the backstop; assertNoSurvivors is the check.
func (h *simHarness) runScript(name, script string, timeout time.Duration, extra ...string) (string, error) {
	h.t.Helper()
	path := filepath.Join(h.dir, name+".sh")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		h.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-e", path)
	cmd.Dir = h.ws
	cmd.Env = h.env(extra...)
	h.inOwnGroup(cmd)
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		h.t.Fatalf("%s did not finish within %s (the script hung):\n%s", name, timeout, out)
	}
	return string(out), err
}

// inOwnGroup starts cmd as the leader of a new process group and registers a
// cleanup that
// terminates whatever is left of the group and waits for it to be empty.
func (h *simHarness) inOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	h.t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		pgid := cmd.Process.Pid
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		for i := 0; i < 50 && syscall.Kill(-pgid, 0) == nil; i++ {
			time.Sleep(100 * time.Millisecond)
		}
	})
}

// start runs a script in the background, in its own process group.
func (h *simHarness) start(script string) (*exec.Cmd, io.ReadCloser) {
	h.t.Helper()
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Env = h.env()
	h.inOwnGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	return cmd, stdout
}

// assertNoSurvivors fails if any process from this fixture outlived the step:
// every fake runs from h.dir, so its argv carries that path. A pipeline member
// the shipped kill missed would survive the same way on a real runner.
func (h *simHarness) assertNoSurvivors() {
	h.t.Helper()
	var left string
	for i := 0; i < 30; i++ {
		out, _ := exec.Command("pgrep", "-fl", regexp.QuoteMeta(h.dir)).Output()
		left = strings.TrimSpace(string(out))
		if left == "" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Errorf("processes from this fixture outlived the step:\n%s", left)
}

func (h *simHarness) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(h.state, name))
	return string(b)
}

func (h *simHarness) count(name string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(h.read(name)))
	return n
}

// runStep substitutes the one Actions expression a run step reads and runs it.
func (h *simHarness) runStep(script string, modes string) (string, error) {
	h.t.Helper()
	script = strings.ReplaceAll(script, "${{ steps.simulator.outputs.device_id }}", h.device)
	if strings.Contains(script, "${{") {
		h.t.Fatalf("unsubstituted Actions expression in the run step:\n%s", script)
	}
	out, err := h.runScript("run-step", script, 60*time.Second, "FAKE_XCODEBUILD_MODES="+modes)
	h.assertNoSurvivors()
	return out, err
}

var stallError = regexp.MustCompile(`::error::no test output for \d+ min — stalled (mid-suite|before the test runner connected); diagnostics uploaded as (\S+)`)

// TestSimTestStallDetector drives the shipped run steps against a fake
// xcodebuild that goes silent, one that is slow but steady, and one that
// finishes.
func TestSimTestStallDetector(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
	}{{soloConfig(), iosSimTarget}, {watchProject(), watchSimTarget}} {
		run := simStepNamed(t, simJobs(t, tc.cfg)[tc.tg.job], tc.tg.job, tc.tg.run).Run

		t.Run(tc.tg.job+"/silent", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			start := time.Now()
			out, err := h.runStep(run, "silent,pass")
			if err == nil {
				t.Fatalf("a suite that went silent PASSED the step:\n%s", out)
			}
			if time.Since(start) > 40*time.Second {
				t.Errorf("the stall took %s to fail; the detector is not what ended it", time.Since(start))
			}
			m := stallError.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("no stall ::error annotation:\n%s", out)
			}
			if m[1] != "mid-suite" {
				t.Errorf("a runner that connected and then went silent was reported %q, want mid-suite", m[1])
			}
			wantArtifact := strings.ReplaceAll(tc.tg.artifact, "${{ matrix.watch.slug }}", "demo")
			if m[2] != wantArtifact {
				t.Errorf("the ::error names artifact %q, want %q", m[2], wantArtifact)
			}
			// Diagnostics FIRST: the log tail, the processes, a sample, and
			// this device's log.
			diag := filepath.Join(h.ws, "simulator-diagnostics")
			tail, _ := os.ReadFile(filepath.Join(diag, "xcodebuild-tail.log"))
			if !strings.Contains(string(tail), "slow() pass") {
				t.Errorf("the diagnostics carry no tail of the xcodebuild log (got %q)", tail)
			}
			if _, err := os.Stat(filepath.Join(diag, "summary.txt")); err != nil {
				t.Errorf("no process summary in the diagnostics: %v", err)
			}
			if !strings.Contains(h.read("sample.calls"), "sample ") {
				t.Error("the stalled processes were never sampled")
			}
			if !strings.Contains(h.read("xcrun.calls"), "simctl spawn "+h.device+" log show --last 5m") {
				t.Errorf("this run's device log was not captured:\n%s", h.read("xcrun.calls"))
			}
			// Killed narrowly, never retried.
			if strings.Contains(out, "Retrying ONCE") || h.count("xcodebuild.count") != 1 {
				t.Errorf("a stall entered the retry (xcodebuild ran %d times)", h.count("xcodebuild.count"))
			}
			assertNarrowKills(t, h)
		})

		// The two paths meet here: the pre-connection signature, zero tests
		// executed, and then silence that never ends. xcodebuild never exits,
		// so there is no failure to retry: it is a stall, and a stall never
		// enters the retry.
		t.Run(tc.tg.job+"/signature then silence", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			out, err := h.runStep(run, "signature-then-silence,pass")
			if err == nil || stallError.FindString(out) == "" {
				t.Fatalf("a silent run carrying the pre-connection signature did not fail as a stall:\n%s", out)
			}
			if n := h.count("xcodebuild.count"); n != 1 || strings.Contains(h.read("xcrun.calls"), "simctl erase") {
				t.Errorf("the stall path entered the retry: xcodebuild ran %d times\n%s", n, h.read("xcrun.calls"))
			}
		})

		// Silent from the start and never connecting: bounded by the longer,
		// pre-connection window, and reported as that, not as mid-suite.
		t.Run(tc.tg.job+"/never connects", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			out, err := h.runStep(run, "never-connects,pass")
			m := stallError.FindStringSubmatch(out)
			if err == nil || m == nil || m[1] != "before the test runner connected" {
				t.Fatalf("a runner that never connected was not failed as a pre-connection stall:\n%s", out)
			}
			if h.count("xcodebuild.count") != 1 {
				t.Error("a pre-connection stall was retried")
			}
			if _, err := os.Stat(filepath.Join(h.ws, "simulator-diagnostics", "summary.txt")); err != nil {
				t.Errorf("no diagnostics for a pre-connection stall: %v", err)
			}
		})

		t.Run(tc.tg.job+"/slow but steady", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			out, err := h.runStep(run, "steady")
			if stallError.FindString(out) != "" {
				t.Fatalf("output every second for longer than the stall window was reported as a stall:\n%s", out)
			}
			if tc.tg.job == "test" && err != nil {
				t.Fatalf("a passing, steady suite failed the step: %v\n%s", err, out)
			}
			if !strings.Contains(out, "last output") {
				t.Errorf("no heartbeat while xcodebuild ran:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(h.ws, "simulator-diagnostics")); err == nil {
				t.Error("diagnostics were captured for a run that never stalled")
			}
		})

		t.Run(tc.tg.job+"/finishes", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			start := time.Now()
			out, err := h.runStep(run, "pass")
			if tc.tg.job == "test" && err != nil {
				t.Fatalf("a passing suite failed the step: %v\n%s", err, out)
			}
			if stallError.FindString(out) != "" || h.count("xcodebuild.count") != 1 {
				t.Fatalf("a finished run stalled or retried:\n%s", out)
			}
			if d := time.Since(start); d > 10*time.Second {
				t.Errorf("a run that finished at once took %s; the watchdog is holding the step open", d)
			}
		})
	}
}

// assertNarrowKills requires every pkill to name this workspace or this run's
// device. The fake refuses anything broader, so a machine-wide kill fails here
// rather than on the shared Mac.
func assertNarrowKills(t *testing.T, h *simHarness) {
	t.Helper()
	calls := strings.TrimSpace(h.read("pkill.calls"))
	if calls == "" {
		t.Error("nothing was killed after the stall, so the stalled xcodebuild would squat the runner")
		return
	}
	for _, c := range strings.Split(calls, "\n") {
		if !strings.Contains(c, h.ws) && !strings.Contains(c, h.device) && !strings.Contains(c, "-P ") {
			t.Errorf("a kill not scoped to this workspace or device: %q", c)
		}
	}
}

// TestSimTestRetriesOnlyTheNeverConnectedFailure is the retry decision, run
// through the shipped step.
func TestSimTestRetriesOnlyTheNeverConnectedFailure(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
	}{{soloConfig(), iosSimTarget}, {watchProject(), watchSimTarget}} {
		run := simStepNamed(t, simJobs(t, tc.cfg)[tc.tg.job], tc.tg.job, tc.tg.run).Run
		for _, sc := range []struct {
			name, modes string
			wantRuns    int
			wantPass    bool
		}{
			{"signature, zero tests: retries and passes", "preconnect,pass", 2, true},
			// flare's real shape: silent for longer than the MID-SUITE window
			// before the runner connected, then xcodebuild's own give-up. The
			// retry must win over the stall detector.
			{"silent past the stall window before connecting, then the signature: retries", "preconnect-hang,pass", 2, true},
			{"signature, zero tests twice: fails after ONE retry", "preconnect,preconnect,pass", 2, false},
			{"signature, some tests executed: no retry", "preconnect-some,pass", 1, false},
			{"signature, no result bundle: no retry", "preconnect-nobundle,pass", 1, false},
			{"ordinary failure: no retry", "fail,pass", 1, false},
		} {
			t.Run(tc.tg.job+"/"+sc.name, func(t *testing.T) {
				t.Parallel()
				h := newSimHarness(t, tc.tg, tc.cfg)
				out, err := h.runStep(run, sc.modes)
				if n := h.count("xcodebuild.count"); n != sc.wantRuns {
					t.Fatalf("xcodebuild ran %d times, want %d\n%s", n, sc.wantRuns, out)
				}
				retried := sc.wantRuns == 2
				warned := regexp.MustCompile(`::warning::'(before establishing connection|never finished bootstrapping)' with zero tests executed`).MatchString(out)
				if warned != retried {
					t.Errorf("retry warning present=%v, want %v:\n%s", warned, retried, out)
				}
				calls := h.read("xcrun.calls")
				if retried {
					if !strings.Contains(calls, "simctl erase "+h.device) {
						t.Errorf("the retry did not erase THIS run's device:\n%s", calls)
					}
					if !strings.Contains(h.read("lock-during-boot"), "held") {
						t.Error("the retry's cold boot did not take the host-wide boot lock")
					}
					// Only the BOOT is serialised: the retried suite must run with
					// the lock released, or every other job's boot queues behind it.
					if strings.Contains(h.read("lock-during-test"), "held") {
						t.Errorf("a test attempt ran while the boot lock was held: %q", h.read("lock-during-test"))
					}
					if _, err := os.Stat(filepath.Join(h.ws, strings.TrimSuffix(tc.tg.log, ".log")+"-attempt1.log")); err != nil {
						t.Errorf("the first attempt's log was not kept: %v", err)
					}
					assertNarrowKills(t, h)
				} else if strings.Contains(calls, "simctl erase") {
					t.Errorf("no retry was due, but the device was erased:\n%s", calls)
				}
				if tc.tg.job != "test" {
					// The watch run step only RECORDS the exit code; its assert
					// step is the verdict.
					want := "exit_code=0"
					if !sc.wantPass {
						want = "exit_code=65"
					}
					if got, _ := os.ReadFile(h.output); !strings.Contains(string(got), want) {
						t.Errorf("GITHUB_OUTPUT has no %s:\n%s", want, got)
					}
					return
				}
				if sc.wantPass != (err == nil) {
					t.Errorf("step passed=%v, want %v\n%s", err == nil, sc.wantPass, out)
				}
			})
		}
	}
}

// TestSimTestDoesNotRetryAnAppCrash: the pre-connection signature with zero
// tests is also what an app that crashes at launch produces. flare on iOS 27
// trapped deterministically in App.body (SIGTRAP on
// com.apple.SwiftUI.AsyncRenderer in _swift_task_checkIsolatedSwift); retrying
// that costs an attempt, fails again, and blames the simulator for an app bug.
func TestSimTestDoesNotRetryAnAppCrash(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
	}{{soloConfig(), iosSimTarget}, {watchProject(), watchSimTarget}} {
		run := simStepNamed(t, simJobs(t, tc.cfg)[tc.tg.job], tc.tg.job, tc.tg.run).Run

		t.Run(tc.tg.job+"/this app crashed during the attempt: named, not retried", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			out, err := h.runStep(run, "preconnect-crash,pass")
			if n := h.count("xcodebuild.count"); n != 1 {
				t.Fatalf("an app crash at launch was retried (xcodebuild ran %d times)\n%s", n, out)
			}
			m := regexp.MustCompile(`::error::The app under test crashed at launch[^\n]*`).FindString(out)
			for _, want := range []string{"Flare crashed", "SIGTRAP", "com.apple.SwiftUI.AsyncRenderer", "_swift_task_checkIsolatedSwift", "FlareApp.body.getter", "NOT retried"} {
				if !strings.Contains(m, want) {
					t.Errorf("the crash ::error does not name %q:\n%s", want, m)
				}
			}
			if strings.Contains(out, "Retrying ONCE") {
				t.Error("the crash was blamed on the simulator with a retry warning")
			}
			ips, _ := filepath.Glob(filepath.Join(h.ws, "simulator-diagnostics", "*.ips"))
			if len(ips) != 1 {
				t.Errorf("the crash report was not attached to the diagnostics artifact (found %v)", ips)
			}
			if tc.tg.job == "test" && err == nil {
				t.Error("the step passed over an app crash")
			}
		})

		t.Run(tc.tg.job+"/another app's crash on another device: still retries", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			out, _ := h.runStep(run, "preconnect-other-crash,pass")
			if n := h.count("xcodebuild.count"); n != 2 {
				t.Errorf("another repository's crash report blocked this run's retry (xcodebuild ran %d times)\n%s", n, out)
			}
		})

		t.Run(tc.tg.job+"/this app's crash from before the attempt: still retries", func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			old := filepath.Join(h.crashDir, "Flare-2026-09-18-025436.ips")
			if err := os.WriteFile(old, []byte(crashReport("Flare", h.device)), 0o644); err != nil {
				t.Fatal(err)
			}
			hourAgo := time.Now().Add(-time.Hour)
			if err := os.Chtimes(old, hourAgo, hourAgo); err != nil {
				t.Fatal(err)
			}
			out, _ := h.runStep(run, "preconnect,pass")
			if n := h.count("xcodebuild.count"); n != 2 {
				t.Errorf("a crash report older than the attempt blocked the retry (xcodebuild ran %d times)\n%s", n, out)
			}
		})
	}
}

// ---- The lock itself ----------------------------------------------------------

func (h *simHarness) lockScript(body string) string {
	return ". \"$RUNNER_TEMP/lacquer-sim.sh\"\n" + body
}

func TestSimBootLock(t *testing.T) {
	t.Run("serialises, heartbeats, and hands over", func(t *testing.T) {
		t.Parallel()
		h := newSimHarness(t, iosSimTarget, soloConfig())
		holder, stdout := h.start(h.lockScript("sim_boot_lock_acquire\necho HELD\nsleep 3\n"))
		waitForLine(t, stdout, "HELD")
		out, err := h.runScript("contender", h.lockScript("sim_boot_lock_acquire\necho \"held=$SIM_BOOT_LOCK_HELD\"\n"), 30*time.Second)
		_ = holder.Wait()
		if err != nil {
			t.Fatalf("contender failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "[boot-lock ") {
			t.Errorf("no heartbeat while waiting for the lock:\n%s", out)
		}
		if !strings.Contains(out, "held=1") || strings.Contains(out, "::warning::") {
			t.Errorf("the contender did not get the lock once the holder finished:\n%s", out)
		}
		if !strings.Contains(out, "(last holder: acme/demo run 424242") {
			t.Errorf("the heartbeat does not name the holder:\n%s", out)
		}
	})

	t.Run("bounded: proceeds without it, and says so", func(t *testing.T) {
		t.Parallel()
		h := newSimHarness(t, iosSimTarget, soloConfig())
		holder, stdout := h.start(h.lockScript("sim_boot_lock_acquire\necho HELD\nsleep 20\n"))
		defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
		waitForLine(t, stdout, "HELD")
		start := time.Now()
		out, err := h.runScript("contender", h.lockScript("sim_boot_lock_acquire\necho \"held=$SIM_BOOT_LOCK_HELD\"\n"), 30*time.Second)
		if err != nil {
			t.Fatalf("a lock it could not get FAILED the step: %v\n%s", err, out)
		}
		if d := time.Since(start); d > 12*time.Second {
			t.Errorf("the wait was bounded at 4s and took %s", d)
		}
		if !strings.Contains(out, "::warning::Waited") || !strings.Contains(out, "held=0") {
			t.Errorf("gave up without a ::warning, or claimed the lock:\n%s", out)
		}
	})

	t.Run("a killed holder never blocks the fleet", func(t *testing.T) {
		t.Parallel()
		h := newSimHarness(t, iosSimTarget, soloConfig())
		holder, stdout := h.start(h.lockScript("sim_boot_lock_acquire\necho HELD\nsleep 60\n"))
		waitForLine(t, stdout, "HELD")
		_ = holder.Process.Kill() // SIGKILL: no trap, no cleanup
		_ = holder.Wait()
		start := time.Now()
		out, err := h.runScript("contender", h.lockScript("sim_boot_lock_acquire\necho \"held=$SIM_BOOT_LOCK_HELD\"\n"), 30*time.Second)
		if err != nil || !strings.Contains(out, "held=1") {
			t.Fatalf("the lock was not free after its holder was SIGKILLed: %v\n%s", err, out)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("waited %s on a dead holder's lock", d)
		}
	})

	t.Run("no lockf: proceeds without it, and says so", func(t *testing.T) {
		t.Parallel()
		h := newSimHarness(t, iosSimTarget, soloConfig())
		// A PATH holding bash's needs and nothing else, so `command -v lockf`
		// really finds nothing.
		bin := filepath.Join(h.dir, "bare-bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, tool := range []string{"date", "cat"} {
			if err := os.Symlink(realTool(t, tool), filepath.Join(bin, tool)); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(realTool(t, "bash"), "-e", "-c", h.lockScript("sim_boot_lock_acquire\necho \"held=$SIM_BOOT_LOCK_HELD\"\n"))
		cmd.Env = append(h.env(), "PATH="+bin)
		h.inOwnGroup(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "::warning::lockf(1) is not on this runner") || !strings.Contains(string(out), "held=0") {
			t.Fatalf("a runner without lockf did not proceed with a warning: %v\n%s", err, out)
		}
	})

	t.Run("lockf failing outright: proceeds without it", func(t *testing.T) {
		t.Parallel()
		h := newSimHarness(t, iosSimTarget, soloConfig())
		h.fake("lockf", "exit 64\n")
		out, err := h.runScript("contender", h.lockScript("sim_boot_lock_acquire\necho \"held=$SIM_BOOT_LOCK_HELD\"\n"), 30*time.Second)
		if err != nil || !strings.Contains(out, "::warning::lockf exited 64") || !strings.Contains(out, "held=0") {
			t.Fatalf("a failing lockf did not proceed with a warning: %v\n%s", err, out)
		}
	})
}

// TestSimSetupHoldsTheLockOnlyForTheBoot runs the shipped setup steps end to
// end: the boot happens under the lock, and the lock is free when the step
// ends even though the boot left a child process running.
func TestSimSetupHoldsTheLockOnlyForTheBoot(t *testing.T) {
	for _, tc := range []struct {
		cfg *config.Config
		tg  simTarget
	}{{soloConfig(), iosSimTarget}, {watchProject(), watchSimTarget}} {
		t.Run(tc.tg.job, func(t *testing.T) {
			t.Parallel()
			h := newSimHarness(t, tc.tg, tc.cfg)
			script := simStepNamed(t, simJobs(t, tc.cfg)[tc.tg.job], tc.tg.job, tc.tg.setup).Run
			// The step writes the library itself; point it at the test-scale one.
			script = strings.Replace(script, simLibFrom(t, tc.tg.setup, script), h.lib, 1)
			script = strings.ReplaceAll(script, "${{ steps.runtime.outputs.runtime }}", "com.apple.CoreSimulator.SimRuntime.watchOS-27-0")
			if strings.Contains(script, "${{") {
				t.Fatalf("unsubstituted Actions expression in %s", tc.tg.setup)
			}
			out, err := h.runScript("setup", script, 60*time.Second, "FAKE_BOOT_CHILD_SECONDS=15")
			if err != nil {
				t.Fatalf("%s failed against the fakes: %v\n%s", tc.tg.setup, err, out)
			}
			if strings.Contains(out, "readiness poll timed out") {
				t.Errorf("%s never saw its readiness service:\n%s", tc.tg.setup, out)
			}
			if got := strings.TrimSpace(h.read("lock-during-boot")); got != "held" {
				t.Errorf("the boot ran with the host-wide lock %q, want held", got)
			}
			// The boot's child is still alive. If it had inherited fd 9 it would
			// still hold the lock.
			probe, err := h.runScript("probe", h.lockScript("exec 7<\"$SIM_BOOT_LOCK\"\nif lockf -s -t 0 7; then echo FREE; else echo HELD; fi\n"), 10*time.Second)
			if err != nil || !strings.Contains(probe, "FREE") {
				t.Errorf("the lock is still held after %s finished (a boot child inherited it): %v\n%s", tc.tg.setup, err, probe)
			}
		})
	}
}

func waitForLine(t *testing.T, r interface{ Read([]byte) (int, error) }, want string) {
	t.Helper()
	var got strings.Builder
	buf := make([]byte, 256)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		n, err := r.Read(buf)
		got.Write(buf[:n])
		if strings.Contains(got.String(), want) {
			return
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("never saw %q (got %q)", want, got.String())
}
