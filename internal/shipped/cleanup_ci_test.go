package shipped

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// This file guards profiles/ios/workflows/cleanup-ci.yml, the nightly janitor
// that runs in every iOS repository on the shared self-hosted Mac.
//
// Until 2026-09 its scheduled level killed every xcodebuild, xctest runner,
// Simulator app and CoreSimulatorService ON THE HOST, by name, fourteen times a
// night (once per repository), guarded only by a `gh run list` of the CURRENT
// repository. Every other repository's CI job, every agent's local build and the
// operator's own Simulator were fair game. Its manual levels erased and deleted
// every simulator the runner's OS user owned and wiped that user's whole
// DerivedData, which on the runner that runs as the operator is the operator's.
//
// The replacement selects by evidence of ownership instead of by name: orphaned
// processes (parent 1) that live under THIS runner's work directory and are
// older than any job could be, and simulators carrying the names ios-ci.yml
// gives the devices it creates. Two families of tests follow:
//
//   - render tests, which read the shipped workflow and reject any machine-wide
//     command in any level, and pin the inputs;
//   - shell tests, which extract the selection library from the rendered
//     workflow and run it against PATH shims for ps, lsof, xcrun and lockf. No
//     test kills or deletes anything real: `kill` goes through a logging shim
//     (CLEANUP_KILL), HOME points into a temporary directory, and every path the
//     library would touch is redirected there.

const (
	cleanupWorkflow  = "cleanup-ci.yml"
	cleanupLibStart  = "# >>> lacquer-cleanup-lib"
	cleanupLibEnd    = "# <<< lacquer-cleanup-lib"
	cleanupEntryCall = "cleanup_main "
	// The argv word the cleanup step re-execs itself with, so that every
	// runner's cleanup can recognise every other runner's in `ps -A`.
	cleanupMarker = "lacquer-ci-cleanup"
)

// cleanupDoc is just enough of the rendered workflow for these tests.
type cleanupDoc struct {
	On struct {
		WorkflowDispatch struct {
			Inputs map[string]struct {
				Type    string   `yaml:"type"`
				Default any      `yaml:"default"`
				Options []string `yaml:"options"`
			} `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Env   map[string]string `yaml:"env"`
		Steps []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func parseCleanupCI(t *testing.T) cleanupDoc {
	t.Helper()
	var doc cleanupDoc
	if err := yaml.Unmarshal([]byte(renderIOSWorkflow(t, cleanupWorkflow, soloProject(), "")), &doc); err != nil {
		t.Fatalf("rendered %s: %v", cleanupWorkflow, err)
	}
	return doc
}

// cleanupRunScripts is every run: script in the rendered workflow. It fails on
// an empty result so the guards below cannot pass by reading nothing.
func cleanupRunScripts(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, j := range parseCleanupCI(t).Jobs {
		for _, s := range j.Steps {
			if s.Run != "" {
				out = append(out, s.Run)
			}
		}
	}
	var sawEntry bool
	for _, s := range out {
		if hasCommand(s, cleanupEntryCall) {
			sawEntry = true
		}
	}
	if !sawEntry {
		t.Fatalf("%s: no run step calls %q; the scan below would be reading the wrong file", cleanupWorkflow, cleanupEntryCall)
	}
	return out
}

// machineWide is every command shape that reaches past this runner's own
// processes, devices or files. A comment may still describe them (stripComment
// strips comments); a command may not.
var machineWide = []struct {
	name string
	re   *regexp.Regexp
}{
	{"killall (kills by name, host-wide)", regexp.MustCompile(`\bkillall\b`)},
	{"pkill (kills by pattern; every earlier use here was host-wide)", regexp.MustCompile(`\bpkill\b`)},
	{"simctl <verb> all (every simulator the OS user owns)", regexp.MustCompile(`\bsimctl\s+\w+\s+all\b`)},
	{"the OS user's whole DerivedData", regexp.MustCompile(`Library/Developer/Xcode/DerivedData`)},
	{"the OS user's CoreSimulator caches or logs", regexp.MustCompile(`Library/(Developer|Logs)/CoreSimulator/(Caches|\*)`)},
	{"the OS user's module cache", regexp.MustCompile(`ModuleCache\.noindex`)},
	{"a per-repository busy check (it cannot see the other thirteen)", regexp.MustCompile(`\bgh\s+run\s+list\b`)},
}

// TestCleanupCIHasNoMachineWideCommands is the defect itself: no level of the
// shipped cleanup may kill by name, act on every simulator, or delete a cache
// that belongs to the OS user rather than to this runner.
func TestCleanupCIHasNoMachineWideCommands(t *testing.T) {
	for _, script := range cleanupRunScripts(t) {
		for _, line := range strings.Split(script, "\n") {
			for _, m := range machineWide {
				if m.re.MatchString(stripComment(line)) {
					t.Errorf("%s runs %s:\n    %s\nThe cleanup runs in every iOS repository on a Mac shared by all "+
						"of them and by the operator; select this runner's own orphans and CI devices instead.",
						cleanupWorkflow, m.name, strings.TrimSpace(line))
				}
			}
		}
	}
}

// stripComment drops a trailing `# ...` the same way hasCommand does, and
// returns "" for a line that is only a comment.
func stripComment(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return ""
	}
	if i := strings.Index(line, " #"); i != -1 {
		return line[:i]
	}
	return line
}

// TestCleanupCIMachineWideScanBites proves the scan above can fail: each
// pattern must match the command it names, and none may match the shapes the
// fix uses instead.
func TestCleanupCIMachineWideScanBites(t *testing.T) {
	bad := []string{
		"pkill -9 xcodebuild",
		"killall -9 com.apple.CoreSimulator.CoreSimulatorService",
		"xcrun simctl delete all",
		"xcrun simctl erase all 2>/dev/null",
		"rm -rf ~/Library/Developer/Xcode/DerivedData/*",
		"rm -rf ~/Library/Developer/CoreSimulator/Caches/*",
		"rm -rf ~/Library/Logs/CoreSimulator/*",
		"rm -rf ~/Library/Developer/Xcode/ModuleCache.noindex/*",
		"IOS=$(gh run list --workflow=ios-ci.yml)",
	}
	for _, line := range bad {
		var hit bool
		for _, m := range machineWide {
			if m.re.MatchString(stripComment(line)) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("no machine-wide pattern matches %q", line)
		}
	}
	good := []string{
		`"$kill_cmd" -TERM "$pid"`,
		`xcrun simctl delete "$udid"`,
		`xcrun simctl delete unavailable`,
		`rm -rf -- "$dd"`,
		`# the old cleanup ran killall -9 Simulator here`,
	}
	for _, line := range good {
		for _, m := range machineWide {
			if m.re.MatchString(stripComment(line)) {
				t.Errorf("pattern %q matches the scoped command %q", m.name, line)
			}
		}
	}
}

// TestCleanupCIInputs pins the dispatch surface: the three levels, and a dry
// run that is off unless asked for.
func TestCleanupCIInputs(t *testing.T) {
	doc := parseCleanupCI(t)
	in := doc.On.WorkflowDispatch.Inputs

	level, ok := in["cleanup_level"]
	if !ok {
		t.Fatalf("workflow_dispatch has no cleanup_level input")
	}
	if got := strings.Join(level.Options, ","); got != "standard,aggressive,nuclear" {
		t.Errorf("cleanup_level options = %q, want standard,aggressive,nuclear", got)
	}
	if level.Default != "standard" {
		t.Errorf("cleanup_level default = %v, want standard", level.Default)
	}

	dry, ok := in["dry_run"]
	if !ok {
		t.Fatalf("workflow_dispatch has no dry_run input; there is no way to see what a run would do before it does it")
	}
	if dry.Type != "boolean" {
		t.Errorf("dry_run type = %q, want boolean", dry.Type)
	}
	if dry.Default != false {
		t.Errorf("dry_run default = %v, want false", dry.Default)
	}

	var wired bool
	for _, j := range doc.Jobs {
		if strings.Contains(j.Env["CLEANUP_DRY_RUN"], "inputs.dry_run") {
			wired = true
		}
	}
	if !wired {
		t.Errorf("no job sets CLEANUP_DRY_RUN from inputs.dry_run; the input would be accepted and ignored")
	}
}

// cleanupLib extracts the selection library from the rendered workflow: the
// lines between the two marker comments, which is also how a human runs it
// locally.
func cleanupLib(t *testing.T) string {
	t.Helper()
	for _, script := range cleanupRunScripts(t) {
		var lib []string
		in := false
		for _, line := range strings.Split(script, "\n") {
			switch strings.TrimSpace(line) {
			case cleanupLibStart:
				in = true
			case cleanupLibEnd:
				if in {
					return strings.Join(lib, "\n") + "\n"
				}
			}
			if in {
				lib = append(lib, line)
			}
		}
	}
	t.Fatalf("%s: no run step holds a library between %q and %q", cleanupWorkflow, cleanupLibStart, cleanupLibEnd)
	return ""
}

// cleanupHost is a fake Mac: fixtures for ps, lsof and simctl, a lockf whose
// answer per descriptor the test chooses, and a kill that only logs.
type cleanupHost struct {
	t                                       *testing.T
	dir, bin, runner, work, devices, stamps string
	ps, lsof, simctl, simctlUnavailable     []string
	// selfParent is the parent of the shell that runs cleanup_main, and
	// ownWorker whether the fixture holds this runner's Runner.Worker at
	// ownWorkerPID. By default the shell is a run: step's bash, a direct child
	// of that worker, which is what the quiet-window gate walks up to.
	selfParent int
	ownWorker  bool
}

const (
	// The library's threshold is three hours; these sit either side of it.
	oldEtime   = "05:00:00"
	youngEtime = "00:10:00"

	// This job's own Runner.Worker, and its Runner.Listener (not in the fixture).
	ownWorkerPID   = 900
	ownListenerPID = 890
)

func newCleanupHost(t *testing.T) *cleanupHost {
	t.Helper()
	dir := t.TempDir()
	h := &cleanupHost{
		t:          t,
		dir:        dir,
		bin:        filepath.Join(dir, "bin"),
		runner:     filepath.Join(dir, "runner"),
		work:       filepath.Join(dir, "runner", "_work"),
		devices:    filepath.Join(dir, "devices"),
		stamps:     filepath.Join(dir, "stamps"),
		selfParent: ownWorkerPID,
		ownWorker:  true,
	}
	for _, d := range []string{h.bin, filepath.Join(h.work, "repo", "repo"), h.devices, h.stamps, filepath.Join(dir, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shims := map[string]string{
		// @SELF@ is the pid of the shell running cleanup_main ($$), which the
		// test cannot know before it starts.
		"ps": `sed "s/@SELF@/$(cat "$FAKE_DIR/self.pid")/" "$FAKE_DIR/ps.txt"`,
		"lsof": `pid=""
while [ $# -gt 0 ]; do [ "$1" = -p ] && pid=$2; shift; done
awk -v p="$pid" '$1==p {print "p" p; print "n" $2}' "$FAKE_DIR/lsof.txt"`,
		"xcrun": `echo "xcrun $*" >>"$FAKE_DIR/calls.log"
case "$*" in
  "simctl list devices") cat "$FAKE_DIR/simctl.txt" ;;
  "simctl list devices unavailable") cat "$FAKE_DIR/simctl-unavailable.txt" ;;
  "simctl delete "*|"simctl shutdown "*) ;;
  *) echo "unexpected: xcrun $*" >&2; exit 97 ;;
esac`,
		"lockf": `fd=""; for a in "$@"; do fd=$a; done
echo "lockf $*" >>"$FAKE_DIR/calls.log"
if [ -f "$FAKE_DIR/lockf-rc-$fd" ]; then exit "$(cat "$FAKE_DIR/lockf-rc-$fd")"; fi
exit 0`,
		"fakekill": `echo "kill $*" >>"$FAKE_DIR/calls.log"
if [ "$1" = -0 ]; then grep -qx "$2" "$FAKE_DIR/survivors" 2>/dev/null; exit $?; fi
exit 0`,
	}
	for name, body := range shims {
		if err := os.WriteFile(filepath.Join(h.bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// proc adds a ps row owned by the current user.
func (h *cleanupHost) proc(pid, ppid int, etime, cmd string) {
	h.ps = append(h.ps, fmt.Sprintf("%6d %6d %5d %11s %s", pid, ppid, os.Getuid(), etime, cmd))
}

// procAs adds a ps row owned by another OS user (the github-user runners).
func (h *cleanupHost) procAs(uid, pid, ppid int, etime, cmd string) {
	h.ps = append(h.ps, fmt.Sprintf("%6d %6d %5d %11s %s", pid, ppid, uid, etime, cmd))
}

// cwd records a process's working directory for the lsof shim.
func (h *cleanupHost) cwd(pid int, dir string) {
	h.lsof = append(h.lsof, fmt.Sprintf("%d %s", pid, dir))
}

// device adds a simulator to the listing and gives its device.plist an age.
func (h *cleanupHost) device(name, udid, state string, age time.Duration) {
	h.simctl = append(h.simctl, fmt.Sprintf("    %s (%s) (%s) ", name, udid, state))
	d := filepath.Join(h.devices, udid)
	if err := os.MkdirAll(d, 0o755); err != nil {
		h.t.Fatal(err)
	}
	p := filepath.Join(d, "device.plist")
	if err := os.WriteFile(p, []byte("plist"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		h.t.Fatal(err)
	}
}

// lockfRC makes the lockf shim answer rc for descriptor fd (8 is the boot-lock
// probe, 9 the cleanup's own host lock).
func (h *cleanupHost) lockfRC(fd, rc int) {
	h.write(fmt.Sprintf("lockf-rc-%d", fd), fmt.Sprint(rc))
}

func (h *cleanupHost) write(name, body string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.dir, name), []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *cleanupHost) stampPath() string {
	u, err := exec.Command("id", "-un").Output()
	if err != nil {
		h.t.Fatal(err)
	}
	return filepath.Join(h.stamps, "lacquer-ci-cleanup."+strings.TrimSpace(string(u))+".test-runner.stamp")
}

type cleanupRun struct {
	out   string
	calls string
	code  int
}

func (r cleanupRun) killed(pid int) bool {
	return strings.Contains(r.calls, fmt.Sprintf("kill -TERM %d\n", pid))
}

func (r cleanupRun) deleted(udid string) bool {
	return strings.Contains(r.calls, "xcrun simctl delete "+udid+"\n")
}

func (r cleanupRun) destructive() bool {
	return strings.Contains(r.calls, "kill -TERM") || strings.Contains(r.calls, "kill -KILL") ||
		strings.Contains(r.calls, "simctl delete") || strings.Contains(r.calls, "simctl shutdown")
}

// run executes `cleanup_main level dry event` against the fake host, under
// the same `bash -e` GitHub uses for a run: step.
func (h *cleanupHost) run(level, dry, event string, extraEnv ...string) cleanupRun {
	h.t.Helper()
	ps := []string{fmt.Sprintf("%6s %6d %5d %11s %s", "@SELF@", h.selfParent, os.Getuid(), "00:00:05",
		"/opt/homebrew/bin/bash -e "+h.work+"/_temp/step.sh "+cleanupMarker)}
	if h.ownWorker {
		ps = append(ps, fmt.Sprintf("%6d %6d %5d %11s %s", ownWorkerPID, ownListenerPID, os.Getuid(), "00:00:30",
			h.runner+"/bin/Runner.Worker spawnclient 105 108"))
	}
	h.write("ps.txt", strings.Join(append(ps, h.ps...), "\n")+"\n")
	h.write("lsof.txt", strings.Join(h.lsof, "\n")+"\n")
	h.write("simctl.txt", "== Devices ==\n-- iOS 27.0 --\n"+strings.Join(h.simctl, "\n")+"\n")
	h.write("simctl-unavailable.txt", strings.Join(h.simctlUnavailable, "\n")+"\n")
	h.write("calls.log", "")
	lib := filepath.Join(h.dir, "lib.sh")
	if err := os.WriteFile(lib, []byte(cleanupLib(h.t)), 0o644); err != nil {
		h.t.Fatal(err)
	}

	cmd := exec.Command("bash", "-e", "-c", `echo $$ >"$FAKE_DIR/self.pid"; set -uo pipefail; . "$LIB"; cleanup_main "$@"`, "_", level, dry, event)
	cmd.Env = append([]string{
		"PATH=" + h.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + filepath.Join(h.dir, "home"),
		"USER=" + os.Getenv("USER"),
		"FAKE_DIR=" + h.dir,
		"LIB=" + lib,
		"RUNNER_WORKSPACE=" + filepath.Join(h.work, "repo"),
		"GITHUB_WORKSPACE=" + filepath.Join(h.work, "repo", "repo"),
		"RUNNER_NAME=test-runner",
		"CLEANUP_HOST_LOCK=" + filepath.Join(h.dir, "host.lock"),
		"CLEANUP_BOOT_LOCK=" + filepath.Join(h.dir, "boot.lock"),
		"CLEANUP_STAMP_DIR=" + h.stamps,
		"CLEANUP_SIM_DEVICES_DIR=" + h.devices,
		"CLEANUP_KILL=" + filepath.Join(h.bin, "fakekill"),
		"CLEANUP_KILL_GRACE_SECONDS=0",
	}, extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		h.t.Fatalf("could not run the cleanup library: %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(filepath.Join(h.dir, "calls.log"))
	return cleanupRun{out: string(out), calls: string(calls), code: code}
}

func (h *cleanupHost) mustRun(level, dry, event string, extraEnv ...string) cleanupRun {
	h.t.Helper()
	r := h.run(level, dry, event, extraEnv...)
	if r.code != 0 {
		h.t.Fatalf("cleanup_main %s dry=%s %s exited %d\n--- output\n%s--- calls\n%s", level, dry, event, r.code, r.out, r.calls)
	}
	return r
}

// TestCleanupCISelectsOnlyThisRunnersOrphans is requirement 1: a process is
// killed only when it is orphaned, belongs to this runner's work directory
// (by argv, or by working directory for tools like xcodebuild that are given
// relative paths), is old enough that no live job can own it, and is not one
// of the host's shared simulator services.
func TestCleanupCISelectsOnlyThisRunnersOrphans(t *testing.T) {
	h := newCleanupHost(t)
	w := h.work
	sibling := filepath.Join(filepath.Dir(w), "_work2") // shares the prefix, not the directory

	h.proc(101, 1, oldEtime, w+"/repo/repo/DerivedData/Build/Products/Debug/Demo.xctest/Contents/MacOS/Demo")
	h.proc(102, 1, "1-02:00:00", "/usr/bin/xctest "+w+"/other/other/DerivedData/Foo.xctest")
	h.proc(103, 1, "03:00:00", w+"/repo/repo/.build/debug/helper --flag") // exactly at the threshold
	h.proc(104, 1, oldEtime, "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild test -project Demo.xcodeproj")
	h.cwd(104, w+"/repo/repo")

	h.proc(201, 1, youngEtime, w+"/repo/repo/DerivedData/Young.xctest")
	h.proc(202, 1, "02:59:59", w+"/repo/repo/DerivedData/JustUnder.xctest")
	h.proc(203, 4242, oldEtime, w+"/repo/repo/DerivedData/Parented.xctest")
	h.proc(204, 1, oldEtime, sibling+"/repo/repo/DerivedData/Sibling.xctest")
	h.proc(205, 1, oldEtime, "/Users/someone/Developer/app/DerivedData/Local.xctest")
	h.proc(206, 1, oldEtime, "/Library/Developer/PrivateFrameworks/CoreSimulator.framework/Versions/A/XPCServices/com.apple.CoreSimulator.CoreSimulatorService.xpc/Contents/MacOS/com.apple.CoreSimulator.CoreSimulatorService --work "+w+"/repo")
	h.proc(207, 1, oldEtime, "/Applications/Xcode.app/Contents/Developer/Applications/Simulator.app/Contents/MacOS/Simulator -CurrentDeviceUDID X "+w+"/repo")
	h.proc(208, 1, oldEtime, "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild build")
	h.cwd(208, "/Users/someone/Developer/app")
	h.proc(209, 1, oldEtime, "/usr/bin/xcodebuild build")
	h.cwd(209, sibling+"/repo/repo")
	// Another OS user's process; the uid column is what excludes it.
	h.ps = append(h.ps, fmt.Sprintf("%6d %6d %5d %11s %s", 210, 1, os.Getuid()+1, oldEtime, w+"/repo/repo/DerivedData/Theirs.xctest"))

	r := h.mustRun("standard", "false", "schedule")

	for _, pid := range []int{101, 102, 103, 104} {
		if !r.killed(pid) {
			t.Errorf("pid %d is an old orphan under this runner's work directory and was not killed\n%s", pid, r.out)
		}
	}
	for pid, why := range map[int]string{
		201: "younger than the threshold",
		202: "one second under the threshold",
		203: "not orphaned (a live parent owns it)",
		204: "under a sibling directory that merely shares the work root's prefix",
		205: "outside the work directory",
		206: "CoreSimulatorService, shared by every simulator on the host",
		207: "the Simulator app",
		208: "working directory outside the work directory",
		209: "working directory under the sibling directory",
		210: "owned by another OS user",
	} {
		if strings.Contains(r.calls, fmt.Sprintf(" %d\n", pid)) {
			t.Errorf("pid %d was signalled but is %s\n%s", pid, why, r.calls)
		}
	}
	// The log caps commands at 160 characters. A worktree-local TMPDIR can
	// make even this prefix longer; still require every character it can show.
	commandPrefix := w + "/repo/repo/DerivedData"
	if len(commandPrefix) > 160 {
		commandPrefix = commandPrefix[:160]
	}
	if !strings.Contains(r.out, "TERM pid=101 age=5h00m cmd="+commandPrefix) {
		t.Errorf("the kill of pid 101 is not logged with pid, age and command:\n%s", r.out)
	}
}

// TestCleanupCIKillsWithTermThenKill: TERM first, a grace, then KILL only for
// what survived.
func TestCleanupCIKillsWithTermThenKill(t *testing.T) {
	h := newCleanupHost(t)
	h.proc(301, 1, oldEtime, h.work+"/repo/repo/DerivedData/Stubborn.xctest")
	h.proc(302, 1, oldEtime, h.work+"/repo/repo/DerivedData/Polite.xctest")
	h.write("survivors", "301\n")

	r := h.mustRun("standard", "false", "schedule")
	for _, want := range []string{"kill -TERM 301\n", "kill -TERM 302\n", "kill -KILL 301\n"} {
		if !strings.Contains(r.calls, want) {
			t.Errorf("missing %q\n%s", want, r.calls)
		}
	}
	if strings.Contains(r.calls, "kill -KILL 302") {
		t.Errorf("pid 302 exited on TERM and was still sent KILL\n%s", r.calls)
	}
	if strings.Index(r.calls, "kill -TERM 301") > strings.Index(r.calls, "kill -KILL 301") {
		t.Errorf("KILL was sent before TERM\n%s", r.calls)
	}
}

// TestCleanupCIDeletesOnlyOldIdleCIDevices is requirement 2.
func TestCleanupCIDeletesOnlyOldIdleCIDevices(t *testing.T) {
	h := newCleanupHost(t)
	old, young := 5*time.Hour, 10*time.Minute
	h.device("CI-iPhone-35334646991", "11111111-1111-1111-1111-111111111111", "Shutdown", old)
	h.device("CI-Watch-35334646991-demo", "22222222-2222-2222-2222-222222222222", "Shutdown", old)
	h.device("CI-iPhone-35334646992-paid", "33333333-3333-3333-3333-333333333333", "Shutdown", old)

	h.device("CI-iPhone-40000000001", "44444444-4444-4444-4444-444444444444", "Shutdown", young)
	h.device("CI-iPhone", "55555555-5555-5555-5555-555555555555", "Shutdown", old)
	h.device("CI-DailyBread", "66666666-6666-6666-6666-666666666666", "Shutdown", old)
	h.device("dailybread-store-teardown-iPhone17Pro-ios27", "77777777-7777-7777-7777-777777777777", "Shutdown", old)
	h.device("iPhone 17 Pro", "88888888-8888-8888-8888-888888888888", "Shutdown", old)
	h.device("My CI-iPhone-7", "99999999-9999-9999-9999-999999999999", "Shutdown", old)
	// Booted but old: leaked by a killed job. Not deleted (it is booted), and
	// it must not wedge the nightly pass either (see the busy-gate test).
	h.device("CI-iPhone-30000000000", "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", "Booted", old)

	r := h.mustRun("standard", "false", "schedule")

	for _, u := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"} {
		if !r.deleted(u) {
			t.Errorf("old shut-down CI device %s was not deleted\n%s", u, r.out)
		}
	}
	for u, why := range map[string]string{
		"44444444-4444-4444-4444-444444444444": "younger than the threshold",
		"55555555-5555-5555-5555-555555555555": "the bare pre-run-id name, not CI-iPhone-*",
		"66666666-6666-6666-6666-666666666666": "not a CI-iPhone-*/CI-Watch-* name",
		"77777777-7777-7777-7777-777777777777": "a human's device",
		"88888888-8888-8888-8888-888888888888": "a default device",
		"99999999-9999-9999-9999-999999999999": "a name that only contains the prefix",
		"AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA": "booted",
	} {
		if strings.Contains(r.calls, u) {
			t.Errorf("device %s was touched but is %s\n%s", u, why, r.calls)
		}
	}
	// Not even considered: a device outside the two CI name families must not
	// reach the decision at all, where a mangled UDID would only be saved by
	// the missing device.plist.
	for _, u := range []string{"66666666-6666-6666-6666-666666666666", "77777777-7777-7777-7777-777777777777",
		"88888888-8888-8888-8888-888888888888", "99999999-9999-9999-9999-999999999999"} {
		if strings.Contains(r.out, u) {
			t.Errorf("non-CI device %s was a candidate\n%s", u, r.out)
		}
	}
	if !strings.Contains(r.calls, "xcrun simctl delete unavailable\n") {
		t.Errorf("simctl delete unavailable was not run\n%s", r.calls)
	}
	if !strings.Contains(r.out, "leaked") {
		t.Errorf("an old booted CI device was not reported as leaked\n%s", r.out)
	}
}

// TestCleanupCIOncePerRunnerPerNight is requirement 3: a fresh stamp skips a
// scheduled pass, a stale one does not, and a real pass writes it.
func TestCleanupCIOncePerRunnerPerNight(t *testing.T) {
	seed := func(h *cleanupHost) {
		h.proc(401, 1, oldEtime, h.work+"/repo/repo/DerivedData/X.xctest")
		h.device("CI-iPhone-1", "11111111-1111-1111-1111-111111111111", "Shutdown", 5*time.Hour)
	}
	stamp := func(h *cleanupHost, age time.Duration) {
		p := h.stampPath()
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("fresh stamp skips the scheduled pass", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		stamp(h, time.Hour)
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("a pass ran although this runner cleaned an hour ago\n%s", r.calls)
		}
		if !strings.Contains(r.out, "already cleaned") {
			t.Errorf("the skip is not logged\n%s", r.out)
		}
	})
	t.Run("stale stamp runs and refreshes it", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		stamp(h, 21*time.Hour)
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(401) {
			t.Errorf("a stamp 21h old suppressed the pass\n%s", r.out)
		}
		fi, err := os.Stat(h.stampPath())
		if err != nil || time.Since(fi.ModTime()) > time.Minute {
			t.Errorf("the stamp was not refreshed by a real pass (err=%v)", err)
		}
	})
	t.Run("no stamp runs and writes one", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.mustRun("standard", "false", "schedule")
		if _, err := os.Stat(h.stampPath()); err != nil {
			t.Errorf("a real pass did not write its stamp: %v", err)
		}
	})
	t.Run("a manual run ignores the stamp", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		stamp(h, time.Hour)
		r := h.mustRun("standard", "false", "workflow_dispatch")
		if !r.killed(401) {
			t.Errorf("a manual run was suppressed by the nightly stamp\n%s", r.out)
		}
	})
	t.Run("another pass holding the host lock skips this one", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.lockfRC(9, 75)
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("ran without the host lock\n%s", r.calls)
		}
		if _, err := os.Stat(h.stampPath()); err == nil {
			t.Errorf("a skipped pass wrote the stamp, suppressing the retry")
		}
	})
}

// TestCleanupCIBusyHostSkips is requirement 4: a simulator mid-boot anywhere on
// the host, or a young booted CI device, means a job is live.
func TestCleanupCIBusyHostSkips(t *testing.T) {
	seed := func(h *cleanupHost) {
		h.proc(501, 1, oldEtime, h.work+"/repo/repo/DerivedData/X.xctest")
		h.device("CI-iPhone-1", "11111111-1111-1111-1111-111111111111", "Shutdown", 5*time.Hour)
	}

	t.Run("boot lock held", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.write("boot.lock", "")
		h.lockfRC(8, 75)
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("killed or deleted while a simulator was cold-booting\n%s", r.calls)
		}
		if !strings.Contains(r.out, "boot lock") {
			t.Errorf("the skip does not say why\n%s", r.out)
		}
		if _, err := os.Stat(h.stampPath()); err == nil {
			t.Errorf("a skipped pass wrote the stamp")
		}
		if strings.Contains(r.calls, "lockf -s -t 0 8") == false {
			t.Errorf("the boot lock was not probed with a zero timeout\n%s", r.calls)
		}
	})
	t.Run("boot lock free", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.write("boot.lock", "")
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(501) {
			t.Errorf("a free boot lock blocked the pass\n%s", r.out)
		}
	})
	t.Run("boot lock file absent", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(501) {
			t.Errorf("an absent boot lock blocked the pass\n%s", r.out)
		}
		if _, err := os.Stat(filepath.Join(h.dir, "boot.lock")); err == nil {
			t.Errorf("probing created the boot lock file")
		}
	})
	t.Run("young booted CI device", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.device("CI-iPhone-9", "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB", "Booted", 5*time.Minute)
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("killed or deleted while a CI device was booted\n%s", r.calls)
		}
	})
	t.Run("a human's booted device is not a CI job", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.device("iPhone 17 Pro", "CCCCCCCC-CCCC-CCCC-CCCC-CCCCCCCCCCCC", "Booted", 5*time.Minute)
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(501) {
			t.Errorf("the operator's booted simulator blocked the pass\n%s", r.out)
		}
	})
}

// TestCleanupCIDryRunChangesNothing is requirement 6.
func TestCleanupCIDryRunChangesNothing(t *testing.T) {
	for _, level := range []string{"standard", "aggressive", "nuclear"} {
		t.Run(level, func(t *testing.T) {
			h := newCleanupHost(t)
			h.proc(601, 1, oldEtime, h.work+"/repo/repo/DerivedData/X.xctest")
			h.device("CI-iPhone-1", "11111111-1111-1111-1111-111111111111", "Shutdown", 5*time.Hour)
			dd := filepath.Join(h.work, "repo", "repo", "DerivedData")
			if err := os.MkdirAll(dd, 0o755); err != nil {
				t.Fatal(err)
			}
			r := h.mustRun(level, "true", "workflow_dispatch")
			if r.destructive() {
				t.Errorf("dry run killed or deleted\n%s", r.calls)
			}
			for _, want := range []string{"would kill pid=601", "would delete CI-iPhone-1 (11111111-1111-1111-1111-111111111111)"} {
				if !strings.Contains(r.out, want) {
					t.Errorf("dry run output lacks %q\n%s", want, r.out)
				}
			}
			if _, err := os.Stat(dd); err != nil {
				t.Errorf("dry run removed DerivedData")
			}
			if _, err := os.Stat(h.stampPath()); err == nil {
				t.Errorf("dry run wrote the stamp")
			}
			if _, err := os.Stat(filepath.Join(h.dir, "host.lock")); err == nil {
				t.Errorf("dry run created the host lock file")
			}
		})
	}
}

// TestCleanupCIManualLevelsStayScoped is requirement 5: aggressive and nuclear
// drop the age threshold and reach booted CI devices, but never anything that
// is not this runner's.
func TestCleanupCIManualLevelsStayScoped(t *testing.T) {
	h := newCleanupHost(t)
	h.proc(701, 1, youngEtime, h.work+"/repo/repo/DerivedData/Young.xctest")
	h.proc(702, 1, oldEtime, "/Users/someone/Developer/app/DerivedData/Local.xctest")
	h.device("CI-iPhone-1", "11111111-1111-1111-1111-111111111111", "Booted", 5*time.Minute)
	h.device("iPhone 17 Pro", "88888888-8888-8888-8888-888888888888", "Booted", 5*time.Minute)
	ours := filepath.Join(h.work, "repo", "repo", "DerivedData")
	component := filepath.Join(h.work, "other", "other", "App", "DerivedData")
	tool := filepath.Join(h.work, "_tool", "DerivedData")
	theirs := filepath.Join(h.dir, "home", "Library", "Developer", "Xcode", "DerivedData")
	for _, d := range []string{ours, component, tool, theirs} {
		if err := os.MkdirAll(filepath.Join(d, "Build"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r := h.mustRun("aggressive", "false", "workflow_dispatch")
	if !r.killed(701) {
		t.Errorf("aggressive did not reach a young orphan under the work directory\n%s", r.out)
	}
	if !strings.Contains(r.calls, "xcrun simctl shutdown 11111111-1111-1111-1111-111111111111\n") || !r.deleted("11111111-1111-1111-1111-111111111111") {
		t.Errorf("aggressive did not shut down and delete the booted CI device\n%s", r.calls)
	}
	if _, err := os.Stat(ours); err != nil {
		t.Errorf("aggressive removed DerivedData; that is nuclear's")
	}

	r = h.mustRun("nuclear", "false", "workflow_dispatch")
	for _, run := range []cleanupRun{r} {
		if strings.Contains(run.calls, " 702\n") || strings.Contains(run.calls, "88888888-8888-8888-8888-888888888888") {
			t.Errorf("a manual level touched something that is not this runner's\n%s", run.calls)
		}
	}
	for _, d := range []string{ours, component} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("nuclear left this runner's %s", d)
		}
	}
	for _, d := range []string{tool, theirs} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("nuclear removed %s, which is not a job's DerivedData", d)
		}
	}
}

// TestCleanupCIRefusesAnUnprovableWorkRoot: without a work directory there is
// nothing to scope to, and an empty scope must not become "everything".
func TestCleanupCIRefusesAnUnprovableWorkRoot(t *testing.T) {
	for name, env := range map[string]func(h *cleanupHost) []string{
		"unset": func(*cleanupHost) []string { return []string{"RUNNER_WORKSPACE=", "GITHUB_WORKSPACE="} },
		"root directory": func(*cleanupHost) []string {
			return []string{"RUNNER_WORKSPACE=/x", "GITHUB_WORKSPACE="}
		},
		"home directory": func(h *cleanupHost) []string {
			return []string{"RUNNER_WORKSPACE=" + filepath.Join(h.dir, "home", "x"), "GITHUB_WORKSPACE="}
		},
		"missing": func(*cleanupHost) []string {
			return []string{"RUNNER_WORKSPACE=/nonexistent/lacquer/_work/x", "GITHUB_WORKSPACE="}
		},
		"variables disagree": func(h *cleanupHost) []string {
			return []string{"GITHUB_WORKSPACE=" + filepath.Join(h.dir, "elsewhere", "x", "x")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCleanupHost(t)
			h.proc(803, 1, oldEtime, filepath.Join(h.dir, "home")+"/x/anything")
			h.proc(801, 1, oldEtime, "/x/anything")
			h.proc(802, 1, oldEtime, "/nonexistent/lacquer/_work/y")
			r := h.run("standard", "false", "schedule", env(h)...)
			if r.code == 0 {
				t.Errorf("exited 0 with no provable work directory; a pass that did nothing must not look like one that ran\n%s", r.out)
			}
			if r.destructive() {
				t.Errorf("acted without a work directory\n%s", r.calls)
			}
		})
	}
}

// TestCleanupCIQuietWindow: GitHub fires the 07:00Z schedule when it likes (on
// 2026-09-18 it was five hours late and landed at peak CI), so the cron time
// says nothing about whether the host is idle. The pass therefore runs only
// when no OTHER job is executing anywhere on the host: any Runner.Worker, of
// any runner and any OS user, other than the one this shell runs under.
func TestCleanupCIQuietWindow(t *testing.T) {
	seed := func(h *cleanupHost) {
		h.proc(1001, 1, oldEtime, h.work+"/repo/repo/DerivedData/X.xctest")
		h.device("CI-iPhone-1", "11111111-1111-1111-1111-111111111111", "Shutdown", 5*time.Hour)
	}
	githubUID := os.Getuid() + 1
	theirRunner := "/Users/github/actions-runner-2"

	t.Run("another Runner.Worker skips the pass", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		other := filepath.Join(h.dir, "pixelfox-2")
		h.procAs(githubUID, 2001, 2000, "00:04:00", theirRunner+"/bin/Runner.Worker spawnclient 164 167")
		// A runner that has self-updated runs its worker from bin.<version>.
		h.proc(2011, 2010, "00:03:00", other+"/bin.2.335.1/Runner.Worker spawnclient 160 163")
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("killed or deleted while two other jobs were running on the host\n%s", r.calls)
		}
		if strings.Contains(r.calls, "simctl") {
			t.Errorf("a skipped pass still listed or touched simulators\n%s", r.calls)
		}
		want := "skipped: 2 other job(s) running on this host (" + theirRunner + ", " + other + ")"
		if !strings.Contains(r.out, want) {
			t.Errorf("the skip is not logged as %q\n%s", want, r.out)
		}
		if _, err := os.Stat(h.stampPath()); err == nil {
			t.Errorf("a skipped pass wrote the stamp, suppressing the retry")
		}
	})
	t.Run("another OS user's worker alone skips the pass", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.procAs(githubUID, 2001, 2000, "00:04:00", theirRunner+"/bin/Runner.Worker spawnclient 164 167")
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("a job under another OS user did not hold the pass back\n%s", r.calls)
		}
		if !strings.Contains(r.out, "skipped: 1 other job(s) running on this host ("+theirRunner+")") {
			t.Errorf("the skip is not logged\n%s", r.out)
		}
	})
	t.Run("a manual aggressive run is held back too", func(t *testing.T) {
		// aggressive deletes BOOTED CI devices, and this OS user's other runners
		// boot theirs under the same names: the gate matters most here.
		h := newCleanupHost(t)
		seed(h)
		h.device("CI-iPhone-2", "22222222-2222-2222-2222-222222222222", "Booted", 5*time.Minute)
		h.proc(2011, 2010, "00:03:00", filepath.Join(h.dir, "pixelfox-2")+"/bin/Runner.Worker spawnclient 160 163")
		r := h.mustRun("aggressive", "false", "workflow_dispatch")
		if r.destructive() {
			t.Errorf("an aggressive run acted while another job was running\n%s", r.calls)
		}
	})
	t.Run("a dry run reports the skip and changes nothing", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.procAs(githubUID, 2001, 2000, "00:04:00", theirRunner+"/bin/Runner.Worker spawnclient 164 167")
		r := h.mustRun("standard", "true", "schedule")
		if r.destructive() {
			t.Errorf("dry run killed or deleted\n%s", r.calls)
		}
		if !strings.Contains(r.out, "would be skipped: 1 other job(s) running on this host ("+theirRunner+")") {
			t.Errorf("the dry run does not say a real run would skip\n%s", r.out)
		}
		if !strings.Contains(r.out, "would kill pid=1001") {
			t.Errorf("the dry run hid the selection it exists to show\n%s", r.out)
		}
	})
	t.Run("only its own worker, up the parent chain, proceeds", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		// Two hops: the step's bash under a wrapper under this job's worker.
		h.selfParent = 950
		h.proc(950, ownWorkerPID, "00:00:20", "/bin/sh -c "+h.work+"/_temp/wrapper.sh")
		// Neither is a Runner.Worker: one names it only as an argument, the
		// other is the runner's diagnostics.
		h.proc(2101, 2100, "00:00:01", "grep Runner.Worker")
		h.proc(2102, 2100, "01:00:00", "tail -f "+h.runner+"/_diag/Runner.Worker_20260918.log")
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(1001) || !r.deleted("11111111-1111-1111-1111-111111111111") {
			t.Errorf("the pass was held back by its own job, or by a process that merely names Runner.Worker\n%s\n%s", r.out, r.calls)
		}
		if strings.Contains(r.out, "skipped") {
			t.Errorf("reported a skip with no other job running\n%s", r.out)
		}
	})
	t.Run("no worker in the parent chain skips with a warning", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.ownWorker = false
		h.selfParent = 950
		h.proc(950, 1, "00:00:20", "/bin/zsh -l")
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("proceeded without finding its own Runner.Worker\n%s", r.calls)
		}
		if !strings.Contains(r.out, "::warning::") || !strings.Contains(r.out, "no Runner.Worker in its parent chain") {
			t.Errorf("the fail-safe skip carries no warning\n%s", r.out)
		}
		if _, err := os.Stat(h.stampPath()); err == nil {
			t.Errorf("a skipped pass wrote the stamp")
		}
	})
	t.Run("a worker that is not in the chain is not its own", func(t *testing.T) {
		// This runner's worker exists, but the shell is not under it: it is
		// another job's worker as far as this shell can prove.
		h := newCleanupHost(t)
		seed(h)
		h.selfParent = 950
		h.proc(950, 1, "00:00:20", "/bin/zsh -l")
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("claimed a worker outside its parent chain as its own\n%s", r.calls)
		}
		if !strings.Contains(r.out, "skipped: 1 other job(s) running on this host ("+h.runner+")") {
			t.Errorf("the unclaimed worker was not counted as another job\n%s", r.out)
		}
	})
	t.Run("ps failing fails the run without acting", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		h.write("bin/ps", "#!/bin/sh\nexit 1\n")
		if err := os.Chmod(filepath.Join(h.bin, "ps"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := h.run("standard", "false", "schedule")
		if r.code == 0 {
			t.Errorf("exited 0 without being able to count the jobs on the host\n%s", r.out)
		}
		if r.destructive() {
			t.Errorf("acted without a process listing\n%s", r.calls)
		}
	})
	t.Run("an unreadable count fails safe", func(t *testing.T) {
		// awk dies before printing, as the gate's own awk (the only one given
		// CLEANUP_SELF before the orphan selection) would on a bad program. An
		// empty count must not read as "no other job".
		h := newCleanupHost(t)
		seed(h)
		h.write("bin/awk", "#!/bin/sh\nif [ -n \"${CLEANUP_SELF:-}\" ]; then exit 2; fi\nPATH=${PATH#*:} exec awk \"$@\"\n")
		if err := os.Chmod(filepath.Join(h.bin, "awk"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := h.run("standard", "false", "schedule")
		if r.code == 0 {
			t.Errorf("exited 0 on an unreadable job count\n%s", r.out)
		}
		if r.destructive() {
			t.Errorf("acted on an unreadable job count\n%s", r.calls)
		}
	})
}

// cleanupJob adds another runner's job: its Runner.Worker and, under it, the
// given chain of descendants, each the parent of the next.
func (h *cleanupHost) cleanupJob(uid, worker int, runnerDir string, descendants ...string) {
	h.procAs(uid, worker, worker-1, "00:02:00", runnerDir+"/bin/Runner.Worker spawnclient 160 163")
	parent := worker
	for i, cmd := range descendants {
		pid := worker + 1 + i
		h.procAs(uid, pid, parent, "00:01:00", cmd)
		parent = pid
	}
}

// TestCleanupCIOtherCleanupsDoNotBlock: every repository's nightly cleanup is
// itself a Runner.Worker, and GitHub fires fourteen of them in a burst. Were
// they "other jobs" to each other, the more they overlapped the less the host
// would be cleaned, and every run would only say "skipped". They are harmless
// to each other (each acts on its own runner's orphans and on shut-down, old
// CI devices, serialised by the host lock), so a worker running this same
// cleanup, recognised by the marker in a descendant's argv, is not counted.
func TestCleanupCIOtherCleanupsDoNotBlock(t *testing.T) {
	seed := func(h *cleanupHost) {
		h.proc(1001, 1, oldEtime, h.work+"/repo/repo/DerivedData/X.xctest")
		h.device("CI-iPhone-1", "11111111-1111-1111-1111-111111111111", "Shutdown", 5*time.Hour)
	}
	githubUID := os.Getuid() + 1
	cleanupStep := func(runnerDir string) string {
		return "/opt/homebrew/bin/bash -e " + runnerDir + "/_work/_temp/a1b2.sh " + cleanupMarker
	}

	t.Run("another runner's cleanup does not block this one", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		theirs := "/Users/github/actions-runner-2"
		// Waiting on the host lock: the marked step shell, and lockf under it.
		h.cleanupJob(githubUID, 3000, theirs, cleanupStep(theirs), "lockf -s -t 180 9")
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(1001) || !r.deleted("11111111-1111-1111-1111-111111111111") {
			t.Errorf("another runner's cleanup held this one back\n%s\n%s", r.out, r.calls)
		}
		if strings.Contains(r.out, "skipped") {
			t.Errorf("reported a skip for a concurrent cleanup\n%s", r.out)
		}
		if !strings.Contains(r.out, "1 other cleanup job(s) running ("+theirs+")") {
			t.Errorf("the ignored cleanup is not logged, so an overlap is invisible\n%s", r.out)
		}
		if !strings.Contains(r.calls, "lockf -s -t 180 9") {
			t.Errorf("the pass did not serialise on the host lock\n%s", r.calls)
		}
	})
	t.Run("a marker deeper in the tree still identifies the cleanup", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		theirs := filepath.Join(h.dir, "pixelfox-2")
		h.cleanupJob(os.Getuid(), 3000, theirs, "/bin/sh -c wrapper", cleanupStep(theirs))
		r := h.mustRun("standard", "false", "schedule")
		if !r.killed(1001) {
			t.Errorf("a cleanup whose marked shell is a grandchild of its worker blocked this one\n%s", r.out)
		}
	})
	t.Run("a cleanup and a normal job skip", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		cleanup, busy := "/Users/github/actions-runner-2", filepath.Join(h.dir, "pixelfox-2")
		h.cleanupJob(githubUID, 3000, cleanup, cleanupStep(cleanup))
		h.cleanupJob(os.Getuid(), 4000, busy, "/opt/homebrew/bin/bash -e "+busy+"/_work/_temp/c3d4.sh",
			"/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild test")
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("acted while a normal job was running beside a cleanup\n%s", r.calls)
		}
		if !strings.Contains(r.out, "skipped: 1 other job(s) running on this host ("+busy+")") {
			t.Errorf("the skip does not name exactly the normal job\n%s", r.out)
		}
	})
	t.Run("a worker with no children yet counts as a job", func(t *testing.T) {
		// Just starting: nothing says what it will run, so it is a job.
		h := newCleanupHost(t)
		seed(h)
		theirs := "/Users/github/actions-runner-2"
		h.cleanupJob(githubUID, 3000, theirs)
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("a worker with no children yet was assumed to be a cleanup\n%s", r.calls)
		}
		if !strings.Contains(r.out, "skipped: 1 other job(s) running on this host ("+theirs+")") {
			t.Errorf("the starting worker was not counted\n%s", r.out)
		}
	})
	t.Run("the marker only as part of a word is not a cleanup", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		theirs := filepath.Join(h.dir, "pixelfox-2")
		h.cleanupJob(os.Getuid(), 3000, theirs, "/opt/homebrew/bin/bash -e "+theirs+"/_work/_temp/e5f6.sh",
			"cat /tmp/"+cleanupMarker+".lock", "echo --"+cleanupMarker+"-x")
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("a normal job that mentions the marker inside a word was taken for a cleanup\n%s", r.calls)
		}
	})
	t.Run("a marked process under no worker excludes nothing", func(t *testing.T) {
		h := newCleanupHost(t)
		seed(h)
		theirs := "/Users/github/actions-runner-2"
		h.cleanupJob(githubUID, 3000, theirs)
		h.procAs(githubUID, 3500, 1, "00:10:00", "/bin/bash /tmp/x.sh "+cleanupMarker)
		r := h.mustRun("standard", "false", "schedule")
		if r.destructive() {
			t.Errorf("a stray marked process excused an unrelated worker\n%s", r.calls)
		}
	})
}

// TestCleanupCIStepCarriesTheMarker runs the step's own prelude (everything
// before the library) with the real bash and the real ps, as the runner does
// (`bash -e <script>`), and asserts the step's shell, the process that is a
// direct child of the Runner.Worker, carries the marker as an argv word. The
// shell tests above take that for granted in their ps fixtures.
func TestCleanupCIStepCarriesTheMarker(t *testing.T) {
	var prelude []string
	for _, script := range cleanupRunScripts(t) {
		if !strings.Contains(script, cleanupLibStart) {
			continue
		}
		for _, line := range strings.Split(script, "\n") {
			if strings.TrimSpace(line) == cleanupLibStart {
				break
			}
			prelude = append(prelude, line)
		}
	}
	if len(prelude) == 0 {
		t.Fatalf("no run step holds the cleanup library")
	}
	dir := t.TempDir()
	step := filepath.Join(dir, "step.sh")
	body := strings.Join(prelude, "\n") + "\necho \"pid=$$\"\nps -ww -o command= -p $$\n"
	if err := os.WriteFile(step, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", step)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the step prelude failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	argv := lines[len(lines)-1]
	if !regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(cleanupMarker) + `(\s|$)`).MatchString(argv) {
		t.Errorf("the step's shell does not carry %q as an argv word; ps shows %q\n%s", cleanupMarker, argv, out)
	}
	if strings.Count(string(out), "pid=") != 1 {
		t.Errorf("the step ran its body more than once (a re-exec loop?)\n%s", out)
	}
}

// TestCleanupCINeverKillsSharedXcodeServices: an Xcode build service, SourceKit
// or XPC helper can be orphaned under this runner's work directory and still be
// serving another build; killing it mid-build is the one way an orphan kill
// hurts another job. Anything executing from /Applications/Xcode*.app/ is
// therefore never selected, except the xcodebuild client itself, whose orphan
// is the hung job the cleanup exists for. The match is on the executable, not
// on the command line.
func TestCleanupCINeverKillsSharedXcodeServices(t *testing.T) {
	h := newCleanupHost(t)
	w := h.work + "/repo/repo"
	shared := map[int]string{
		1101: "/Applications/Xcode.app/Contents/SharedFrameworks/SwiftBuild.framework/Versions/A/PlugIns/SWBBuildService.bundle/Contents/MacOS/SWBBuildService",
		1102: "/Applications/Xcode.app/Contents/SharedFrameworks/XCBuild.framework/Versions/A/PlugIns/XCBBuildService.bundle/Contents/MacOS/XCBBuildService " + w,
		1103: "/Applications/Xcode-beta.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/lib/sourcekitd.framework/Versions/A/XPCServices/SourceKitService.xpc/Contents/MacOS/SourceKitService",
		1104: "/Applications/Xcode.app/Contents/Developer/Library/Xcode/Agents/Xcode Service.app/Contents/MacOS/Xcode Service",
		1105: "/Applications/Xcode_26.1.app/Contents/SharedFrameworks/SourceKit.framework/Versions/A/XPCServices/com.apple.dt.SKAgent.xpc/Contents/MacOS/com.apple.dt.SKAgent",
		// Names the xcodebuild client only as an argument: still a service.
		1106: "/Applications/Xcode.app/Contents/SharedFrameworks/XCBuild.framework/Versions/A/PlugIns/XCBBuildService.bundle/Contents/MacOS/XCBBuildService --client /Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild",
	}
	for pid, cmd := range shared {
		h.proc(pid, 1, oldEtime, cmd)
		h.cwd(pid, w)
	}
	// Controls, orphaned under the work directory in exactly the same way.
	h.proc(1111, 1, oldEtime, "/Applications/Xcode.app/Contents/Developer/usr/bin/xcodebuild test -scheme Demo")
	h.cwd(1111, w)
	h.proc(1112, 1, oldEtime, "/Applications/Xcode-beta.app/Contents/Developer/usr/bin/xcodebuild build")
	h.cwd(1112, w)
	h.proc(1113, 1, oldEtime, `/usr/bin/log stream --predicate process contains "altool"`)
	h.cwd(1113, w)
	// Names Xcode only as an argument: the executable is not Xcode's.
	h.proc(1114, 1, oldEtime, "/usr/bin/env /Applications/Xcode.app/Contents/Developer/usr/bin/actool "+w+"/Assets.xcassets")

	r := h.mustRun("standard", "false", "schedule")
	for pid, cmd := range shared {
		if strings.Contains(r.calls, fmt.Sprintf(" %d\n", pid)) {
			t.Errorf("pid %d was signalled but executes from Xcode's bundle: %s\n%s", pid, cmd, r.calls)
		}
	}
	for _, pid := range []int{1111, 1112, 1113, 1114} {
		if !r.killed(pid) {
			t.Errorf("pid %d is an old orphan under this runner's work directory and not a shared Xcode service, and was not killed\n%s", pid, r.out)
		}
	}
}
