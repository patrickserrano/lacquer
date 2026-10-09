package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
)

// A project whose test host cannot run on the fleet pin used to have one way
// out: exclude ci.yml entirely, which freezes it out of every later profile
// change. [baseline.relax].simulator_runtime is the narrow version of that.
// These tests RUN the rendered shell against a stub `date` and `xcrun`, because
// the property that matters is which runtime the device is created on, on which
// day, and that is behaviour rather than text.

const (
	overrideUntil = "2026-12-31"
	iosPin        = "com.apple.CoreSimulator.SimRuntime.iOS-27-0"
	iosOverride   = "com.apple.CoreSimulator.SimRuntime.iOS-26-2"
	watchPin      = "com.apple.CoreSimulator.SimRuntime.watchOS-27-0"
	watchOverride = "com.apple.CoreSimulator.SimRuntime.watchOS-26-2"
)

// overrideProject is a watch project that declares a 26.2 override.
func overrideProject() *config.Config {
	cfg := watchProject()
	cfg.Baseline.Relax = map[string]baseline.Relax{
		"simulator_runtime": {Major: "26", Minor: "2", Until: overrideUntil, Reason: "test host crash-loops on 27.0"},
	}
	return cfg
}

// runtimeStubs puts a `date` that answers +%Y-%m-%d with $STUB_TODAY, and an
// `xcrun` whose `simctl list runtimes` prints $STUB_RUNTIMES, on PATH.
func runtimeStubs(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	stubTool(t, bin, "date", `for a in "$@"; do [ "$a" = "+%Y-%m-%d" ] && { echo "$STUB_TODAY"; exit 0; }; done
exec /bin/date "$@"
`)
	stubTool(t, bin, "xcrun", `case "$1" in
  simctl) printf '%s\n' $STUB_RUNTIMES ;;
  --sdk) echo 27.0 ;;
esac
`)
	stubTool(t, bin, "xcodebuild", "exit 0\n")
	return bin
}

// runRuntimeScript runs script under bash -e with the stubs first on PATH, and
// returns its output and whether it succeeded.
func runRuntimeScript(t *testing.T, script, today string, installed []string, env ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command("bash", "-e", "-c", script)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + runtimeStubs(t) + ":/usr/bin:/bin",
		"STUB_TODAY=" + today,
		"STUB_RUNTIMES=" + strings.Join(installed, " "),
	}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// iosRuntimeScript is the Setup Simulator step from its pin assignment through
// the line that reports the runtime it chose, plus a line that prints it.
func iosRuntimeScript(t *testing.T, cfg *config.Config) string {
	t.Helper()
	run := cfgStepRun(t, cfg, "test", "Setup Simulator")
	start := strings.Index(run, `PINNED_RUNTIME="com.apple`)
	end := strings.Index(run, `echo "Using runtime:`)
	if start < 0 || end < start {
		t.Fatal("Setup Simulator no longer assigns PINNED_RUNTIME and then reports it; this harness cannot find the part it runs")
	}
	end += strings.Index(run[end:], "\n")
	return run[start:end] + "\necho \"PIN=$PINNED_RUNTIME\"\n"
}

// cfgStepRun is one step's `run:` from the rendered workflow.
func cfgStepRun(t *testing.T, cfg *config.Config, job, step string) string {
	t.Helper()
	for _, s := range parseIOSCI(t, cfg).Jobs[job].Steps {
		if s.Name == step {
			return s.Run
		}
	}
	t.Fatalf("job %s has no step %q", job, step)
	return ""
}

// watchRuntime runs the watch job's "Resolve the watch simulator runtime" step
// whole, and returns the runtime it published.
func watchRuntime(t *testing.T, cfg *config.Config, today string, installed []string) (string, string, bool) {
	t.Helper()
	leg := watchLeg(t, cfg)
	out := filepath.Join(t.TempDir(), "output")
	log, ok := runRuntimeScript(t, cfgStepRun(t, cfg, "watch-test", "Resolve the watch simulator runtime"), today, installed,
		"WATCH_RUNTIME="+leg["runtime"].(string), "WATCH_DOWNLOAD_PLATFORM=watchOS", "GITHUB_OUTPUT="+out)
	b, _ := os.ReadFile(out)
	return strings.TrimPrefix(strings.TrimSpace(string(b)), "runtime="), log, ok
}

func TestSimulatorRuntimeRelaxRendersBothPlatforms(t *testing.T) {
	all := []string{iosPin, iosOverride, watchPin, watchOverride}
	cfg := overrideProject()

	// In term, including the last day: both jobs test on 26.2.
	for _, today := range []string{"2026-10-09", overrideUntil} {
		log, ok := runRuntimeScript(t, iosRuntimeScript(t, cfg), today, all)
		if !ok || !strings.Contains(log, "PIN="+iosOverride+"\n") {
			t.Errorf("iOS on %s: want the device created on %s, got ok=%v:\n%s", today, iosOverride, ok, log)
		}
		if !strings.Contains(log, "::warning::simulator_runtime is relaxed until "+overrideUntil) {
			t.Errorf("iOS on %s: the override applied without saying so:\n%s", today, log)
		}
		got, wlog, ok := watchRuntime(t, cfg, today, all)
		if !ok || got != watchOverride {
			t.Errorf("watch on %s: runtime = %q, want %s (ok=%v):\n%s", today, got, watchOverride, ok, wlog)
		}
	}
}

// Past the date the override stops applying: both jobs are back on the pin, and
// say why.
func TestSimulatorRuntimeRelaxExpiredFallsBackToThePin(t *testing.T) {
	all := []string{iosPin, iosOverride, watchPin, watchOverride}
	cfg := overrideProject()
	log, ok := runRuntimeScript(t, iosRuntimeScript(t, cfg), "2027-01-01", all)
	if !ok || !strings.Contains(log, "PIN="+iosPin+"\n") {
		t.Errorf("iOS after expiry: want the fleet pin %s, got ok=%v:\n%s", iosPin, ok, log)
	}
	if !strings.Contains(log, "expired on "+overrideUntil) {
		t.Errorf("iOS after expiry: no expiry warning:\n%s", log)
	}
	got, wlog, ok := watchRuntime(t, cfg, "2027-01-01", all)
	if !ok || got != watchPin {
		t.Errorf("watch after expiry: runtime = %q, want %s (ok=%v):\n%s", got, watchPin, ok, wlog)
	}
}

// The fallback to the newest installed runtime exists for the fleet pin. For an
// override it would test on exactly the OS the override exists to avoid, and the
// crash-loop would read as a product bug. So a missing override runtime fails,
// naming it.
func TestSimulatorRuntimeRelaxFailsWhenItsRuntimeIsNotInstalled(t *testing.T) {
	cfg := overrideProject()
	log, ok := runRuntimeScript(t, iosRuntimeScript(t, cfg), "2026-10-09", []string{iosPin, watchPin})
	if ok || !strings.Contains(log, "::error::") || !strings.Contains(log, iosOverride) {
		t.Errorf("iOS with %s absent: want a failure naming it, got ok=%v:\n%s", iosOverride, ok, log)
	}
	if _, wlog, ok := watchRuntime(t, cfg, "2026-10-09", []string{iosPin, watchPin}); ok || !strings.Contains(wlog, watchOverride) {
		t.Errorf("watch with %s absent: want a failure naming it, got ok=%v:\n%s", watchOverride, ok, wlog)
	}
}

// Without an override the step does what it always did. Run, not just diffed,
// so the harness itself is shown to read the pin.
func TestNoRuntimeOverrideTestsOnThePin(t *testing.T) {
	log, ok := runRuntimeScript(t, iosRuntimeScript(t, watchProject()), "2026-10-09", []string{iosPin, iosOverride})
	if !ok || !strings.Contains(log, "PIN="+iosPin+"\n") {
		t.Errorf("no override: want %s, got ok=%v:\n%s", iosPin, ok, log)
	}
}

// The Lint job's relaxation step is what fails the run once the date passes. It
// reads only the keys it is told to, so the override must be one of them.
func TestSimulatorRuntimeRelaxIsReadByTheLintRelaxStep(t *testing.T) {
	run := cfgStepRun(t, overrideProject(), "lint", "Read the baseline relaxations")
	if !strings.Contains(run, "coverage simulator_runtime; do") {
		t.Errorf("the relaxation step's key loop does not read simulator_runtime:\n%s", run)
	}
}

// Every project that declares no override, including one with other
// relaxations, receives exactly the file it had.
func TestRuntimeOverrideRendersNothingWithoutTheKey(t *testing.T) {
	plain := renderIOSCI(t, soloConfig())
	relaxed := soloConfig()
	relaxed.Baseline.Relax = map[string]baseline.Relax{
		"swift_version": {Until: overrideUntil, Reason: "r"},
		"coverage":      {Until: overrideUntil, Reason: "r"},
	}
	if got := renderIOSCI(t, relaxed); got != plain {
		t.Errorf("a project with other relaxations renders a different ci.yml:\n%s", firstDiff(plain, got))
	}
}
