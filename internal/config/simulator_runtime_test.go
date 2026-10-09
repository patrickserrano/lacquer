package config

import (
	"strings"
	"testing"
)

const runtimeBase = "[project]\nname = \"demo\"\nproject_name = \"Demo\"\nscheme = \"Demo\"\n" +
	"xcodeproj = \"Demo.xcodeproj\"\n\n[baseline.relax]\n"

// The shape the plan names: two numeric fields, a date and a reason. It must
// load, and resolve to the pin the jobs will test on.
func TestSimulatorRuntimeRelaxLoads(t *testing.T) {
	cfg, err := loadString(t, runtimeBase+
		`simulator_runtime = { major = "26", minor = "2", until = "2026-12-31", reason = "test host crash-loops on 27.0" }`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	pin, r, ok := cfg.SimulatorRuntimeOverride()
	if !ok {
		t.Fatal("SimulatorRuntimeOverride() reported no override for a manifest that declares one")
	}
	if pin != (RuntimePin{Major: "26", Minor: "2"}) {
		t.Errorf("pin = %+v, want 26.2", pin)
	}
	if r.Until != "2026-12-31" || r.Reason == "" {
		t.Errorf("relax = %+v, want the declared until and reason", r)
	}
}

// minor is optional and means .0: watchOS and iOS both number a major's first
// release that way.
func TestSimulatorRuntimeRelaxMinorDefaultsToZero(t *testing.T) {
	cfg, err := loadString(t, runtimeBase+
		`simulator_runtime = { major = "26", until = "2026-12-31", reason = "r" }`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if pin, _, _ := cfg.SimulatorRuntimeOverride(); pin != (RuntimePin{Major: "26", Minor: "0"}) {
		t.Errorf("pin = %+v, want 26.0", pin)
	}
}

func TestNoSimulatorRuntimeRelaxMeansNoOverride(t *testing.T) {
	cfg, err := loadString(t, strings.TrimSuffix(runtimeBase, "\n[baseline.relax]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := cfg.SimulatorRuntimeOverride(); ok {
		t.Error("a manifest with no simulator_runtime reports an override")
	}
}

// A runtime override is a relaxation like any other: no date and it is a
// permanent redefinition of the fleet's pin, no reason and nobody can tell later
// whether it is still needed. And it needs a major: there is nothing to test on
// without one.
func TestSimulatorRuntimeRelaxRequiresUntilAndReason(t *testing.T) {
	for name, tc := range map[string]struct{ line, want string }{
		"no until":  {`simulator_runtime = { major = "26", minor = "2", reason = "r" }`, "until"},
		"no reason": {`simulator_runtime = { major = "26", minor = "2", until = "2026-12-31" }`, "reason"},
		"no major":  {`simulator_runtime = { minor = "2", until = "2026-12-31", reason = "r" }`, "major"},
	} {
		_, err := loadString(t, runtimeBase+tc.line+"\n")
		if err == nil {
			t.Errorf("%s: loaded, want an error", name)
			continue
		}
		if !strings.Contains(err.Error(), "simulator_runtime") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name simulator_runtime and %q", name, err, tc.want)
		}
	}
}

// The two fields reach `simctl create` as part of an argument. They are digits
// and nothing else, so no manifest can write shell through them.
func TestSimulatorRuntimeRelaxRejectsNonNumericFields(t *testing.T) {
	for _, tc := range []string{
		`major = "26 ", minor = "2"`,
		`major = "26/2", minor = "0"`,
		`major = "26", minor = "2 "`,
		`major = "26", minor = "2;id"`,
		`major = "x", minor = "0"`,
		`major = "026", minor = "0"`,
		`major = "26", minor = "-1"`,
		`major = "$(id)", minor = "0"`,
		`major = "26", minor = "2.1"`,
	} {
		_, err := loadString(t, runtimeBase+"simulator_runtime = { "+tc+`, until = "2026-12-31", reason = "r" }`+"\n")
		if err == nil {
			t.Errorf("{ %s } loaded, want it rejected", tc)
			continue
		}
		if !strings.Contains(err.Error(), "simulator_runtime") {
			t.Errorf("{ %s }: error %q does not name simulator_runtime", tc, err)
		}
	}
}

// major and minor mean something only to simulator_runtime. On any other key
// they would be silently ignored, which reads as a setting that took effect.
func TestRuntimeFieldsAreRejectedOnOtherRelaxKeys(t *testing.T) {
	_, err := loadString(t, runtimeBase+
		`swift_version = { major = "5", until = "2026-12-31", reason = "r" }`+"\n")
	if err == nil || !strings.Contains(err.Error(), "swift_version") {
		t.Errorf("major on swift_version: err = %v, want a rejection naming the key", err)
	}
}
