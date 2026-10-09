package shipped

import (
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// pinnedRuntimeLine is the Setup Simulator step's starting assignment: at the
// step's own indentation, and a literal runtime id rather than $FALLBACK.
var pinnedRuntimeLine = regexp.MustCompile(`(?m)^          PINNED_RUNTIME="(com\.apple[^"]*)"`)

// renderedIOSPin is the value the Setup Simulator step starts from.
func renderedIOSPin(t *testing.T, ci string) string {
	t.Helper()
	m := pinnedRuntimeLine.FindAllStringSubmatch(ci, -1)
	if len(m) != 1 {
		t.Fatalf("found %d PINNED_RUNTIME= assignments at the start of a line, want exactly 1", len(m))
	}
	return m[0][1]
}

// The template carried this literal before the pin moved into Go. Every project
// must keep receiving it, or the sync that ships this moves the whole fleet's
// tests to another OS, which is the incident the pin exists to prevent.
func TestSimRuntimeTokenRendersTheSamePinAsBefore(t *testing.T) {
	const before = "com.apple.CoreSimulator.SimRuntime.iOS-27-0"
	for name, cfg := range map[string]*config.Config{
		"lone product":  soloConfig(),
		"two products":  twoIOSProducts(),
		"watch project": watchProject(),
	} {
		if got := renderedIOSPin(t, renderIOSCI(t, cfg)); got != before {
			t.Errorf("%s: PINNED_RUNTIME = %q, want %q", name, got, before)
		}
	}
}

// The watch job's runtime and the iOS job's runtime come from ONE pin. Moving
// the pin must move both: two hard-coded runtimes that agree today are the
// defect, because the next person to bump one will not know about the other.
//
// The pin is moved to a value nobody would hard-code, so a second constant or a
// literal in the platform table cannot pass by coincidence.
func TestWatchRuntimeFollowsTheIOSPin(t *testing.T) {
	saved := config.DefaultRuntimePin
	t.Cleanup(func() { config.DefaultRuntimePin = saved })
	config.DefaultRuntimePin = config.RuntimePin{Major: "91", Minor: "3"}

	ci := renderIOSCI(t, watchProject())
	if got, want := renderedIOSPin(t, ci), "com.apple.CoreSimulator.SimRuntime.iOS-91-3"; got != want {
		t.Errorf("iOS PINNED_RUNTIME = %q after moving the pin, want %q", got, want)
	}
	leg := watchLeg(t, watchProject())
	if got, want := leg["runtime"], "com.apple.CoreSimulator.SimRuntime.watchOS-91-3"; got != want {
		t.Errorf("watch runtime = %v after moving the pin, want %q", got, want)
	}
	if strings.Contains(ci, "-27-0") {
		t.Error("a 27.0 runtime is still rendered after the pin moved: something hard-codes it")
	}
}

// watchLeg is the lone watch matrix leg of a rendered project.
func watchLeg(t *testing.T, cfg *config.Config) map[string]any {
	t.Helper()
	job, ok := renderWatchDoc(t, cfg).Jobs["watch-test"]
	if !ok {
		t.Fatal("no watch-test job rendered")
	}
	legs := job.Strategy.Matrix["watch"]
	if len(legs) != 1 {
		t.Fatalf("got %d watch legs, want 1", len(legs))
	}
	return legs[0]
}
