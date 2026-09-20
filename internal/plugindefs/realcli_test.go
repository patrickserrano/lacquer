//go:build claudecli

package plugindefs

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Runs the REAL `claude plugin validate --strict` over everything lacquer ships.
// Behind a build tag because it needs the CLI installed; CI's "Plugin
// definitions" job installs a pinned version and runs
//
//	go test -tags claudecli -run 'TestRealCLI' -v ./internal/plugindefs/
//
// A missing binary FAILS here rather than skipping: a skipped test reports
// green, and this check exists to prove something ran.

func realClaude(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("claude")
	if err != nil {
		t.Fatalf("claude CLI not on PATH: %v (install step failed or was skipped)", err)
	}
	return p
}

// The control. The CLI reports "Validation passed" when it reads nothing, so a
// pass over the shipped tree only means something if the same invocation
// demonstrably FAILS on a broken definition.
func TestRealCLIRejectsAKnownBadDefinition(t *testing.T) {
	claude := realClaude(t)
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "no-frontmatter.md"), "You are an agent with no frontmatter.\n")
	// Line 1 blank: the opening --- is not on line 1.
	write(t, filepath.Join(root, "agents", "late-opening.md"), "\n---\nname: late-opening\ndescription: d\n---\nbody\n")
	res, err := ValidateWithCLI(claude, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) < 2 {
		t.Fatalf("CLI %s reported %d issues for two broken agents, want >= 2: %+v (a validator that cannot fail proves nothing)", res.Version, len(res.Issues), res.Issues)
	}
}

func TestRealCLIAcceptsWhatLacquerShips(t *testing.T) {
	claude := realClaude(t)
	roots, err := ShippedRoots(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) < 4 {
		t.Fatalf("only %d shipped roots: %v", len(roots), roots)
	}
	total := 0
	for _, root := range roots {
		res, err := ValidateWithCLI(claude, root)
		if err != nil {
			t.Errorf("%s: %v", root, err)
			continue
		}
		total += res.Checked
		for _, i := range res.Issues {
			t.Errorf("%s (%s): %s", i.Path, i.Field, i.Message)
		}
		t.Logf("%s: %d definitions, claude %s", root, res.Checked, res.Version)
	}
	if total < 50 {
		t.Fatalf("only %d definitions handed to the CLI", total)
	}
}
