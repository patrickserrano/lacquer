package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/ratchet"
)

func TestRatchetRegressionBlocksFleet(t *testing.T) {
	r := Report{Ratchets: []ratchet.Finding{{Metric: ratchet.Suppressions, Before: 0, After: 1}}}
	if r.ExitCode() != 4 {
		t.Fatalf("fleet gate = %d, want 4", r.ExitCode())
	}
	if !strings.Contains(strings.Join(Notes(r), "\n"), "unjustified_suppressions regressed 0 → 1") {
		t.Fatal("fleet hides ratchet regression")
	}
	r.Ratchets[0].Before, r.Ratchets[0].After = 1, 0
	if r.ExitCode() != 0 {
		t.Fatal("improvement blocked fleet")
	}
}

// The coverage gate has no enrollment deadline, so the sweep is what makes the
// state visible: each iOS project says whether it is enrolled, and the summary
// counts them. A non-iOS project says nothing.
func TestFleetShowsCoverageEnrollment(t *testing.T) {
	var b strings.Builder
	Text(&b, []Report{
		{Name: "alpha", Coverage: CoverageEnrolled},
		{Name: "bravo", Coverage: CoverageNotEnrolled},
		{Name: "web", Coverage: ""},
	})
	got := b.String()
	for _, want := range []string{"alpha  coverage enrolled", "bravo  coverage not enrolled", "coverage gate: 1 of 2 iOS project(s) enrolled"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "web  coverage") {
		t.Errorf("a non-iOS project reported coverage:\n%s", got)
	}
}

func TestCoverageStateReadsTheRatchetFile(t *testing.T) {
	ios := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}}}
	root := t.TempDir()
	if got := coverageState(root, ios); got != CoverageNotEnrolled {
		t.Errorf("no ratchet file: %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, ratchet.Name), []byte("[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines_free = 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := coverageState(root, ios); got != CoverageEnrolled {
		t.Errorf("enrolled matrix product: %q", got)
	}
	if got := coverageState(root, &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"web"}}}}); got != "" {
		t.Errorf("web project: %q", got)
	}
}
