package fleet

import (
	"strings"
	"testing"

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
