package tokens

import (
	"strings"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
)

// IOSCISimRuntimeOverride and IOSCIRelaxKeys render a project's
// [baseline.relax].simulator_runtime, and are EMPTY for every project that
// declares none: that is the fleet, and it must keep receiving a byte-identical
// ci.yml.
const (
	// IOSCISimRuntimeOverride follows the Setup Simulator step's PINNED_RUNTIME
	// assignment with the dated override, or nothing.
	IOSCISimRuntimeOverride = "{{IOS_CI_SIM_RUNTIME_OVERRIDE}}"
	// IOSCIRelaxKeys adds simulator_runtime to the Lint job's relaxation step,
	// which is what fails the run once the date has passed.
	IOSCIRelaxKeys = "{{IOS_CI_RELAX_KEYS}}"
)

// CISimRuntimeOverride renders the iOS half of the override.
func CISimRuntimeOverride(cfg *config.Config) string {
	pin, r, ok := cfg.SimulatorRuntimeOverride()
	if !ok {
		return ""
	}
	return runtimeOverrideGuard("PINNED_RUNTIME", `"`+pin.Runtime("iOS")+`"`, pin, r, "          ")
}

// CIRelaxKeys renders the extra relaxation key, or nothing.
func CIRelaxKeys(cfg *config.Config) string {
	if _, _, ok := cfg.SimulatorRuntimeOverride(); !ok {
		return ""
	}
	return " " + baseline.SimulatorRuntimeKey
}

// watchRuntimeOverride renders the watch half: the same guard on WATCH_RUNTIME.
//
// The value keeps the leg's platform and swaps only the version, so the step
// text stays shared across legs: "…watchOS-27-0" becomes "…watchOS-26-2".
func watchRuntimeOverride(cfg *config.Config) string {
	pin, r, ok := cfg.SimulatorRuntimeOverride()
	if !ok {
		return ""
	}
	return runtimeOverrideGuard("WATCH_RUNTIME", `"${WATCH_RUNTIME%-*-*}-`+pin.Major+"-"+pin.Minor+`"`, pin, r, "          ")
}

// runtimeOverrideGuard is the dated override, in shell, for the variable v that
// holds a job's runtime, assigned value when it applies.
//
// The date is compared when the step RUNS rather than when the file renders, so
// the override stops applying on the day after `until` without a sync, and a
// render never depends on the calendar. Every value spliced in was validated at
// load as digits or a YYYY-MM-DD date; the free-text reason is deliberately not
// rendered.
//
// A missing override runtime FAILS rather than reaching the job's fallback. The
// fallback picks the newest installed runtime, which for an override is the very
// OS it exists to avoid, and the crash that follows reads as a product bug.
func runtimeOverrideGuard(v, value string, pin config.RuntimePin, r baseline.Relax, ind string) string {
	lines := []string{
		"",
		"",
		"# [baseline.relax].simulator_runtime in .lacquer.toml: test on " + pin.String() + " instead of",
		"# the fleet pin through " + r.Until + ". After that date it stops applying and this",
		"# step tests on the fleet pin again, and the Lint job fails the run until the",
		"# entry is removed or deliberately extended.",
		`if [ "$(date -u +%Y-%m-%d)" \> "` + r.Until + `" ]; then`,
		`  echo "::warning::[baseline.relax].simulator_runtime expired on ` + r.Until + `, so this run tests on the fleet pin $` + v + `. Remove the entry from .lacquer.toml, or extend it deliberately."`,
		"else",
		"  " + v + "=" + value,
		`  echo "::warning::simulator_runtime is relaxed until ` + r.Until + `: testing on $` + v + ` instead of the fleet pin."`,
		`  if ! xcrun simctl list runtimes | grep -q "$` + v + `"; then`,
		`    echo "::error::[baseline.relax].simulator_runtime names $` + v + `, which this runner does not have installed. Falling back to another runtime would test on the OS this override exists to avoid: install it on the runner, or change the entry."`,
		"    xcrun simctl list runtimes",
		"    exit 1",
		"  fi",
		"fi",
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		if l != "" {
			b.WriteString(ind + l)
		}
	}
	return b.String()
}
