package audit_test

import (
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
)

func TestGateExitCodesAndPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name string
		gate audit.Gate
		want int
	}{
		{"clean", audit.Gate{}, 0},
		{"clobber", audit.Gate{Clobbered: 1}, 3},
		{"baseline", audit.Gate{Baseline: 1}, 4},
		{"exclusion", audit.Gate{Exclusions: 1}, 4},
		{"ignore", audit.Gate{DepIgnores: 1}, 4},
		{"not run", audit.Gate{NotRunInCI: 1}, 4},
		{"orphan", audit.Gate{Orphans: 1}, 4},
		{"undeclared", audit.Gate{Undeclared: 1}, 6},
		{"clobber first", audit.Gate{Clobbered: 1, Orphans: 1, Undeclared: 1}, 3},
		{"policy before stack", audit.Gate{Orphans: 1, Undeclared: 1}, 4},
		// --ci only: the lock vouches for content its own version never
		// rendered, so every other attribution made against it is suspect too.
		{"lock mismatch", audit.Gate{LockMismatch: 1}, 8},
		{"lock mismatch first", audit.Gate{LockMismatch: 1, Clobbered: 1, Orphans: 1, Undeclared: 1}, 8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.gate.ExitCode(); got != tt.want {
				t.Fatalf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}
