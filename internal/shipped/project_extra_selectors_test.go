package shipped

import (
	"strings"
	"testing"
)

// End-to-end for the [project].extra_test_targets fallback: the selector must
// reach the rendered workflow AND turn on the step that fails when a selector
// matches nothing.
//
// Both halves matter. Without the first, the extra suite is run by nothing
// while CI stays green — the gap this field exists to close. Without the
// second, the field becomes what its own doc calls "a per-line licence to run
// nothing and call it green", because a misspelled target would select an
// empty set and still exit 0.
func TestProjectExtraTestTargetsReachTheWorkflowWithTheirGuard(t *testing.T) {
	cfg := soloConfig()
	cfg.Project.ExtraTestTargets = []string{"DailyBreadWidgetsTests"}

	rendered := renderIOSCI(t, cfg)

	if !strings.Contains(rendered, "DailyBreadWidgetsTests") {
		t.Error("the rendered ci.yml never names DailyBreadWidgetsTests — a [project]-declared extra " +
			"test target did not reach `-only-testing:`, so those tests run nowhere while the job is green")
	}
	if !strings.Contains(rendered, "Verify Test Selectors Matched") {
		t.Error("declaring extra_test_targets via [project] did not turn on the " +
			"\"Verify Test Selectors Matched\" step. Without it a selector that matches no tests " +
			"exits 0, which is exactly the silent-pass this field's guard exists to prevent")
	}
}

// The control: a project declaring none must render byte-identically, guard
// absent. Most of the fleet declares none, so a stray step here is a diff in
// every repository at once.
func TestNoExtraTestTargetsRendersTheGuardAbsent(t *testing.T) {
	cfg := soloConfig()
	if got := cfg.Products()[0].TestSelectors(); len(got) == 0 {
		t.Fatalf("fixture has no selectors at all: %v", got)
	}
	if strings.Contains(renderIOSCI(t, cfg), "Verify Test Selectors Matched") {
		t.Error("a project declaring no extra_test_targets rendered the verification step — " +
			"that is a diff in every repository in the fleet")
	}
}
