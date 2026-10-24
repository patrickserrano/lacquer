package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// The Setup Simulator step asks "is the pinned runtime installed?" of
// `simctl list runtimes`. Xcode 27.2 installs a device-specific runtime whose id
// the pin is a PREFIX of (…iOS-27-2 and …iOS-27-2-iPhone19-4), so a substring
// test answers yes on a host that has only the device-specific one, and the
// fallback's `iOS[0-9-]*` then extracts the truncated "…iOS-27-2-", which is no
// runtime at all and sorts after the real one. These tests run the rendered
// shell against realistic `simctl list runtimes` text, because both defects are
// invisible to a test that only reads the template.

// simctlRuntimeLine is one line of `simctl list runtimes`, in the real format.
func simctlRuntimeLine(id string) string {
	return "iOS 99.9 (99.9 - 99A999) - " + id
}

// runExactRuntimeCheck runs the rendered presence check and fallback against the
// given runtime ids as simctl prints them, and returns the output and success.
func runExactRuntimeCheck(t *testing.T, ids ...string) (string, bool) {
	t.Helper()
	list := filepath.Join(t.TempDir(), "runtimes.txt")
	lines := []string{"== Runtimes =="}
	for _, id := range ids {
		lines = append(lines, simctlRuntimeLine(id))
	}
	if err := os.WriteFile(list, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stubTool(t, bin, "xcrun", `case "$1" in
  simctl) cat "$STUB_RUNTIME_LIST" ;;
  --sdk) echo 27.2 ;;
esac
`)
	cmd := exec.Command("bash", "-eo", "pipefail", "-c", iosRuntimeScript(t, soloConfig()))
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "STUB_RUNTIME_LIST=" + list}
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

const runtimePrefix = "com.apple.CoreSimulator.SimRuntime."

// A host with only the device-specific runtime does not have the pin. It must
// fall back to a plain runtime, never select the device-specific one or the
// truncated id the old fallback regex produced.
func TestPinnedRuntimeIsMatchedExactlyNotAsAPrefix(t *testing.T) {
	pin := config.DefaultRuntimePin.Runtime("iOS")
	duo := pin + "-iPhone19-4"
	older := runtimePrefix + "iOS-26-5"

	log, ok := runExactRuntimeCheck(t, older, duo)
	if !ok {
		t.Fatalf("a plain older runtime is installed, so the fallback must succeed:\n%s", log)
	}
	if !strings.Contains(log, "::warning::Pinned runtime "+pin+" not found") {
		t.Errorf("only %s is installed, so the pin is absent and the step must say it fell back:\n%s", duo, log)
	}
	if !strings.Contains(log, "PIN="+older+"\n") {
		t.Errorf("want the fallback to select %s, got:\n%s", older, log)
	}
	if strings.Contains(log, "PIN="+duo) || strings.Contains(log, "PIN="+pin+"-\n") || strings.Contains(log, "PIN="+pin+"\n") {
		t.Errorf("selected a runtime that is not installed or is device-specific:\n%s", log)
	}

	// With nothing but the device-specific runtime there is nothing to fall back
	// to; the old fallback "found" the truncated id here and went on to fail
	// inside simctl create, far from the cause.
	log, ok = runExactRuntimeCheck(t, duo)
	if ok || !strings.Contains(log, "::error::No iOS simulator runtimes installed") {
		t.Errorf("only the device-specific runtime installed: want the no-runtime error, got ok=%v:\n%s", ok, log)
	}
}

// The plain pin is found even when the device-specific runtime is listed beside
// it, and nothing falls back.
func TestPinnedRuntimeIsFoundBesideTheDeviceSpecificOne(t *testing.T) {
	pin := config.DefaultRuntimePin.Runtime("iOS")
	log, ok := runExactRuntimeCheck(t, runtimePrefix+"iOS-26-5", pin, pin+"-iPhone19-4")
	if !ok || !strings.Contains(log, "PIN="+pin+"\n") {
		t.Errorf("want PIN=%s, got ok=%v:\n%s", pin, ok, log)
	}
	if strings.Contains(log, "falling back") {
		t.Errorf("the pin is installed, so nothing should fall back:\n%s", log)
	}
}
