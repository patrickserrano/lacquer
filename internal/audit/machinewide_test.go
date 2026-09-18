package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fleet's iOS CI shares one Mac. Every line below is a real one, and every
// one of them, run by any repository's job, breaks the jobs every OTHER
// repository has running on that Mac at the time. The victims report "Test
// crashed with signal trap before establishing connection", which reads as a
// flaky test in the victim, not as a neighbour's cleanup step.
//
// Copied verbatim, indentation included, so a tokeniser change that only
// copes with tidy input fails here.
var machineWideLines = []struct {
	name string
	line string
	kind string
}{
	// momfriend's EXCLUDED ios-ci.yml before PixelFoxStudio/momfriend#386. Its
	// runs matched all four CoreSimulatorService restarts on 2026-09-17 to the
	// minute, and it was still carrying these long after the lacquer's own
	// ios-ci.yml dropped them — an exclusion freezes a file.
	{"momfriend ios-ci pkill xctest", `          pkill -9 -f "xctest" 2>/dev/null || true`, KindPkill},
	{"momfriend ios-ci pkill XCTestRunner", `          pkill -9 -f "XCTestRunner" 2>/dev/null || true`, KindPkill},
	{"momfriend ios-ci killall testmanagerd", `          killall -9 testmanagerd 2>/dev/null || true`, KindKillall},
	{"momfriend ios-ci killall Simulator", `          killall -9 Simulator 2>/dev/null || true`, KindKillall},
	{"momfriend ios-ci killall CoreSimulatorService", `          killall -9 com.apple.CoreSimulator.CoreSimulatorService 2>/dev/null || true`, KindKillall},

	// profiles/ios/workflows/cleanup-ci.yml as of v1.37.10, rendered into every
	// iOS repository and run nightly.
	{"cleanup pkill xcodebuild", `          pkill -9 xcodebuild 2>/dev/null || echo "No xcodebuild processes found"`, KindPkill},
	{"cleanup killall CoreSimulatorService", `          killall -9 com.apple.CoreSimulator.CoreSimulatorService 2>/dev/null || echo "CoreSimulator not running"`, KindKillall},
	{"cleanup killall Simulator", `          killall -9 Simulator 2>/dev/null || echo "Simulator not running"`, KindKillall},
	{"cleanup pkill xctest", `          pkill -9 -f "xctest" 2>/dev/null || echo "No xctest processes found"`, KindPkill},
	{"cleanup pkill XCTestRunner", `          pkill -9 -f "XCTestRunner" 2>/dev/null || echo "No XCTestRunner processes found"`, KindPkill},
	{"cleanup simctl shutdown all", `          xcrun simctl shutdown all 2>/dev/null || echo "No simulators to shutdown"`, KindSimctlAll},
	{"cleanup simctl erase all", `          xcrun simctl erase all 2>/dev/null || echo "No simulators to erase"`, KindSimctlAll},
	{"cleanup killall launchd_sim", `          killall -9 launchd_sim 2>/dev/null || true`, KindKillall},
	{"cleanup killall simctl", `          killall -9 simctl 2>/dev/null || true`, KindKillall},
	{"cleanup killall iOS Simulator", `          killall -9 "iOS Simulator" 2>/dev/null || true`, KindKillall},
	{"cleanup killall SimulatorTrampoline", `          killall -9 SimulatorTrampoline 2>/dev/null || true`, KindKillall},
	{"cleanup simctl delete all", `          xcrun simctl delete all 2>/dev/null || echo "No simulators to delete"`, KindSimctlAll},
	{"cleanup rm DerivedData", `          rm -rf ~/Library/Developer/Xcode/DerivedData/* 2>/dev/null || true`, KindSharedCache},
	{"cleanup rm CoreSimulator Caches", `          rm -rf ~/Library/Developer/CoreSimulator/Caches/* 2>/dev/null || true`, KindSharedCache},
	{"cleanup rm ModuleCache", `          rm -rf ~/Library/Developer/Xcode/ModuleCache.noindex/* 2>/dev/null || true`, KindSharedCache},

	// The same commands in the other spellings a workflow uses.
	{"inline run", `      - run: killall -9 Simulator`, KindKillall},
	{"after then", `          if pgrep -q Simulator; then killall Simulator; fi`, KindKillall},
	{"under sudo", `          sudo killall -9 com.apple.CoreSimulator.CoreSimulatorService`, KindKillall},
	{"through xargs", `          echo Simulator | xargs killall -9`, KindKillall},
	{"killall by user", `          killall -u runner`, KindKillall},
	{"killall of an app nobody listed", `          killall Finder`, KindKillall},
	{"pkill by name", `          pkill testmanagerd`, KindPkill},
	{"pkill unscoped path", `          pkill -f "/Applications/Xcode.app"`, KindPkill},
	{"bare simctl", `          simctl shutdown all`, KindSimctlAll},
	{"simctl boot all", `          xcrun simctl boot all`, KindSimctlAll},
	{"kill every process the user owns", `          kill -9 -1`, KindKillAll},
	{"kill -1 after --", `          kill -- -1`, KindKillAll},
	{"kill -s with -1", `          kill -s KILL -1`, KindKillAll},
	{"rm DerivedData via HOME", `          rm -rf "$HOME/Library/Developer/Xcode/DerivedData"`, KindSharedCache},
	{"rm DerivedData via braced HOME", `          rm -rf "${HOME}/Library/Developer/Xcode/DerivedData/"`, KindSharedCache},
	{"rm DerivedData absolute", `          rm -rf /Users/runner/Library/Developer/Xcode/DerivedData/*`, KindSharedCache},
	{"rm all of CoreSimulator", `          rm -rf ~/Library/Developer/CoreSimulator/*`, KindSharedCache},
	{"rm every device", `          rm -rf ~/Library/Developer/CoreSimulator/Devices/*`, KindSharedCache},
	{"rm ModuleCache glob", `          rm -rf ~/Library/Developer/Xcode/ModuleCache*`, KindSharedCache},
	{"rm ModuleCache under DerivedData", `          rm -rf ~/Library/Developer/Xcode/DerivedData/ModuleCache.noindex`, KindSharedCache},
	{"rm all of Developer", `          rm -rf ~/Library/Developer`, KindSharedCache},
	{"rm continued over two lines", "          rm -rf \\\n            ~/Library/Developer/Xcode/DerivedData/*", KindSharedCache},
}

// What must stay quiet. A finding that fires on the scoped replacement teaches
// people to ignore the one that fires on the real thing.
var scopedLines = []struct {
	name string
	line string
}{
	// The scoped form momfriend#386 moved to, and the form unit M's cleanup-ci
	// rewrite uses: a PID this job selected.
	{"momfriend pkill workspace", `          pkill -9 -f "$GITHUB_WORKSPACE" 2>/dev/null || true`},
	{"pkill braced workspace", `          pkill -f "${GITHUB_WORKSPACE}/build/xctest"`},
	{"pkill runner workspace", `          pkill -f "$RUNNER_WORKSPACE"`},
	{"pkill runner temp", `          pkill -f "$RUNNER_TEMP/derived"`},
	{"pkill expression workspace", `          pkill -f "${{ github.workspace }}"`},
	{"pkill work path", `          pkill -f /Users/runner/actions-runner/_work/rail/rail`},
	{"kill a pid", `          kill 12345`},
	{"kill a var", `          kill -9 "$pid" 2>/dev/null || true`},
	{"kill a list", `          kill -9 $pids`},
	{"kill via xargs", `          pgrep -f "$GITHUB_WORKSPACE" | xargs kill -9`},
	{"kill SIGHUP to a pid", `          kill -1 "$pid"`},
	{"kill -s with a pid", `          kill -s KILL "$pid"`},
	{"simctl delete unavailable", `          xcrun simctl delete unavailable`},
	{"simctl delete one device", `          xcrun simctl delete "$UDID"`},
	{"simctl shutdown one device", `          xcrun simctl shutdown "$UDID" || true`},
	{"simctl list all", `          xcrun simctl list devices all`},
	{"simctl erase this device", `          xcrun simctl erase "$SIM_UDID"`},
	{"rm one project's derived data", `          rm -rf ~/Library/Developer/Xcode/DerivedData/Rail-*`},
	{"rm one device", `          rm -rf ~/Library/Developer/CoreSimulator/Devices/$UDID`},
	{"rm this run's derived data", `          rm -rf "$RUNNER_TEMP/DerivedData"`},
	{"rm workspace derived data", `          rm -rf "$GITHUB_WORKSPACE/DerivedData"`},
	{"rm simulator logs", `          rm -rf ~/Library/Logs/CoreSimulator/* 2>/dev/null || true`},
	{"du of DerivedData", `          echo "  DerivedData: $(du -sh ~/Library/Developer/Xcode/DerivedData 2>/dev/null | cut -f1 || echo 'N/A')"`},

	// Comments, in every place one sits. momfriend's current ios-ci.yml:466 is
	// the first: it NAMES the old kills to explain why they went.
	{"yaml comment naming the old kills", "          # to be `pkill -9 -f xctest` plus `killall -9 testmanagerd`, `Simulator`"},
	{"trailing comment", `          true # killall -9 Simulator`},
	// A `;` or `(` inside a comment would otherwise start a second "command".
	{"comment with a semicolon", `          # was: pkill -9 -f xctest; killall -9 Simulator`},
	{"trailing comment with parentheses", `          true # reset (xcrun simctl erase all)`},
	{"comment after a command", `          echo done  # xcrun simctl erase all`},

	// Words, not commands.
	{"echo naming killall", `          echo "Killing xcodebuild processes..."`},
	{"echo unquoted killall", `          echo killall Simulator`},
	{"step name", `      - name: killall Simulator (aggressive+)`},
	// cleanup-ci.yml's own step names carry parentheses, and `(` splits a line
	// into commands — a mapping line is dropped whole, not command by command.
	{"step name with a command in parentheses", `      - name: Reset Simulators (xcrun simctl erase all)`},
	{"step name with a semicolon", `      - name: Tidy up; killall Simulator`},
	{"yaml description", `        description: 'xcrun simctl erase all'`},
	{"ps grep", `          ps aux | grep -E "(xcodebuild|Simulator|xctest|CoreSimulator)" | grep -v grep || echo "none"`},
	{"pgrep alone", `          pgrep -f xctest`},
	{"killall listing signals", `          killall -l`},
}

func TestMachineWideCommandsAreFlagged(t *testing.T) {
	for _, tc := range machineWideLines {
		t.Run(tc.name, func(t *testing.T) {
			got := machineWideIn("ci.yml", tc.line)
			if len(got) != 1 {
				t.Fatalf("want exactly one finding for\n  %s\ngot %+v", tc.line, got)
			}
			if got[0].Kind != tc.kind {
				t.Errorf("kind = %q, want %q", got[0].Kind, tc.kind)
			}
		})
	}
}

func TestScopedCommandsAreNotFlagged(t *testing.T) {
	for _, tc := range scopedLines {
		t.Run(tc.name, func(t *testing.T) {
			if got := machineWideIn("ci.yml", tc.line); len(got) != 0 {
				t.Fatalf("flagged a command that does not reach other jobs:\n  %s\ngot %+v", tc.line, got)
			}
		})
	}
}

// Line numbers are the thing a reader acts on, so pin them — including across a
// continuation, where the command is reported at the line it STARTS on.
func TestFindingsCarryTheLineTheCommandStartsOn(t *testing.T) {
	body := "name: x\n" + // 1
		"jobs:\n" + // 2
		"  a:\n" + // 3
		"    steps:\n" + // 4
		"      - run: |\n" + // 5
		"          # killall -9 Simulator\n" + // 6
		"          rm -rf \\\n" + // 7
		"            ~/Library/Developer/Xcode/DerivedData/*\n" + // 8
		"          echo ok\n" + // 9
		"          xcrun simctl erase all\n" // 10
	got := machineWideIn("cleanup.yml", body)
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %+v", got)
	}
	if got[0].Line != 7 || got[0].Kind != KindSharedCache {
		t.Errorf("first finding = line %d %s, want line 7 %s", got[0].Line, got[0].Kind, KindSharedCache)
	}
	if got[1].Line != 10 || got[1].Kind != KindSimctlAll {
		t.Errorf("second finding = line %d %s, want line 10 %s", got[1].Line, got[1].Kind, KindSimctlAll)
	}
	if !strings.Contains(got[1].Command, "xcrun simctl erase all") {
		t.Errorf("command not reported: %q", got[1].Command)
	}
}

// Every workflow file is read — managed, project-owned and excluded alike.
// momfriend's offender was EXCLUDED: the one class of file the lacquer stops
// looking after is the one that still carried the kill.
func TestEveryWorkflowIsScannedWhoeverOwnsIt(t *testing.T) {
	dir := t.TempDir()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"ios-ci.yml":         "jobs:\n  t:\n    steps:\n      - run: killall -9 testmanagerd\n",
		"ios-cleanup-ci.yml": "jobs:\n  c:\n    steps:\n      - run: xcrun simctl delete all\n",
		"project-owned.yaml": "jobs:\n  p:\n    steps:\n      - run: pkill -f xctest\n",
		"clean.yml":          "jobs:\n  q:\n    steps:\n      - run: pkill -f \"$GITHUB_WORKSPACE\"\n",
		"README.md":          "killall Simulator\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(wf, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := MachineWideSteps(dir)
	var where []string
	for _, f := range got {
		where = append(where, f.Workflow)
	}
	want := []string{
		".github/workflows/ios-ci.yml",
		".github/workflows/ios-cleanup-ci.yml",
		".github/workflows/project-owned.yaml",
	}
	if strings.Join(where, ",") != strings.Join(want, ",") {
		t.Fatalf("scanned %v, want %v", where, want)
	}
}

func TestNoWorkflowsIsQuiet(t *testing.T) {
	if got := MachineWideSteps(t.TempDir()); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	if out := FormatMachineWide(nil); out != "" {
		t.Fatalf("an empty report printed %q", out)
	}
}

// The message has three jobs: say what the command does to OTHER jobs, say
// where it is, and name the scoped replacement. Pinned per kind, because the
// replacement differs by kind and a generic one would be wrong for most.
func TestFormatNamesTheHarmTheLocationAndTheScopedAlternative(t *testing.T) {
	fs := []MachineWide{
		{Workflow: ".github/workflows/ios-cleanup-ci.yml", Line: 90, Command: "killall -9 com.apple.CoreSimulator.CoreSimulatorService", Kind: KindKillall},
		{Workflow: ".github/workflows/ios-cleanup-ci.yml", Line: 98, Command: `pkill -9 -f "xctest"`, Kind: KindPkill},
		{Workflow: ".github/workflows/ios-cleanup-ci.yml", Line: 113, Command: "xcrun simctl shutdown all", Kind: KindSimctlAll},
		{Workflow: ".github/workflows/ios-cleanup-ci.yml", Line: 147, Command: "rm -rf ~/Library/Developer/Xcode/DerivedData/*", Kind: KindSharedCache},
		{Workflow: ".github/workflows/x.yml", Line: 3, Command: "kill -9 -1", Kind: KindKillAll},
	}
	out := FormatMachineWide(fs)
	for _, want := range []string{
		".github/workflows/ios-cleanup-ci.yml:90",
		"killall -9 com.apple.CoreSimulator.CoreSimulatorService",
		".github/workflows/ios-cleanup-ci.yml:98",
		".github/workflows/ios-cleanup-ci.yml:113",
		".github/workflows/ios-cleanup-ci.yml:147",
		".github/workflows/x.yml:3",
		`pkill -f "$GITHUB_WORKSPACE"`,
		"CI-",
		"other job",
		"signal trap before establishing connection",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	// Every kind a finding can carry is reported: a kind missing from the
	// report order would have its findings silently dropped from the output.
	for _, k := range []string{KindKillall, KindPkill, KindSimctlAll, KindSharedCache, KindKillAll} {
		if kindHarm[k] == "" || kindInstead[k] == "" {
			t.Errorf("kind %q has no harm or no alternative in the report", k)
		}
		if !strings.Contains(out, kindHarm[k]) {
			t.Errorf("kind %q is not in the report", k)
		}
	}
}

// Whole files, not lines: the janitor as it ships in v1.37.10, and the scoped
// rewrite that replaces it (lacquer#395). Kept as frozen copies under testdata
// rather than read from profiles/, so the positive fixture keeps its kills after
// the template drops them.
func machineWideFixture(t *testing.T, name string) []MachineWide {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "machinewide", name))
	if err != nil {
		t.Fatal(err)
	}
	return machineWideIn(".github/workflows/ios-cleanup-ci.yml", string(b))
}

// Every one of the v1.37.10 janitor's sixteen host-wide commands, at its line.
func TestTheV1_37_10CleanupWorkflowIsReportedLineByLine(t *testing.T) {
	want := map[int]string{
		86: KindPkill, 90: KindKillall, 94: KindKillall, 98: KindPkill, 99: KindPkill,
		113: KindSimctlAll, 117: KindSimctlAll,
		121: KindKillall, 122: KindKillall, 123: KindKillall, 124: KindKillall,
		143: KindSimctlAll, 147: KindSharedCache, 151: KindSharedCache, 155: KindSharedCache,
		159: KindKillall,
	}
	got := machineWideFixture(t, "cleanup-ci.v1.37.10.yml")
	seen := map[int]bool{}
	for _, f := range got {
		if want[f.Line] != f.Kind {
			t.Errorf("line %d: got %s, want %q (%s)", f.Line, f.Kind, want[f.Line], f.Command)
		}
		seen[f.Line] = true
	}
	for line, kind := range want {
		if !seen[line] {
			t.Errorf("line %d (%s) not reported", line, kind)
		}
	}
}

// The scoped rewrite: kills are `"$CLEANUP_KILL" -TERM "$pid"` on PIDs it
// selected, devices are deleted by UDID or as `unavailable`, and the manual
// full reset (`simctl shutdown all && simctl erase all`) appears only in a
// comment pointing at it. None of that reaches another job.
func TestTheScopedCleanupRewriteIsQuiet(t *testing.T) {
	if got := machineWideFixture(t, "cleanup-ci.pr395.yml"); len(got) != 0 {
		for _, f := range got {
			t.Errorf("flagged %d: %s (%s)", f.Line, f.Command, f.Kind)
		}
	}
}
