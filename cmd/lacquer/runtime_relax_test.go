package main

import (
	"bytes"
	"strings"
	"testing"
)

// auditRuntimeRelax audits a compliant iOS project that declares the given
// [baseline.relax].simulator_runtime fields.
func auditRuntimeRelax(t *testing.T, fields string) (int, string) {
	t.Helper()
	hr, pr := auditFixture(t, pbxCompliant, "\n[baseline.relax]\nsimulator_runtime = { "+fields+", reason = \"test host crash-loops on the pin\" }\n")
	chdir(t, pr)
	var out, errb bytes.Buffer
	code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	return code, out.String() + errb.String()
}

// Past its date the override is debt nobody renewed, and the audit says so the
// way it does for every other relaxation: EXPIRED, and exit 4.
func TestSimulatorRuntimeRelaxExpiredFails(t *testing.T) {
	code, out := auditRuntimeRelax(t, `major = "26", minor = "2", until = "2020-01-01"`)
	if code != 4 {
		t.Errorf("audit exited %d, want 4 for an expired simulator_runtime\n%s", code, out)
	}
	if !strings.Contains(out, "simulator_runtime") || !strings.Contains(out, "EXPIRED 2020-01-01") {
		t.Errorf("the report does not name the expired override\n%s", out)
	}
}

// In term it is visible debt, not a failure, and not "NOT CHECKED": the audit
// can see everything this override does.
func TestSimulatorRuntimeRelaxInTermIsReportedNotGated(t *testing.T) {
	code, out := auditRuntimeRelax(t, `major = "26", minor = "2", until = "2099-12-31"`)
	if code != 0 {
		t.Errorf("audit exited %d, want 0 for an override in term\n%s", code, out)
	}
	if !strings.Contains(out, "simulator_runtime") || !strings.Contains(out, "RELAXED until 2099-12-31") {
		t.Errorf("the report does not show the override as relaxed\n%s", out)
	}
	if strings.Contains(out, "simulator_runtime relaxation NOT CHECKED") {
		t.Errorf("the override is reported as unchecked, which it is not\n%s", out)
	}
}

// An override naming the fleet pin changes nothing and should be deleted.
func TestSimulatorRuntimeRelaxOnThePinIsDead(t *testing.T) {
	code, out := auditRuntimeRelax(t, `major = "27", minor = "0", until = "2099-12-31"`)
	if code != 0 {
		t.Errorf("audit exited %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "simulator_runtime dead relaxation") {
		t.Errorf("an override on the fleet pin is not reported as removable\n%s", out)
	}
}
