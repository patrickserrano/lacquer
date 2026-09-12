//go:build eval

package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scenario: stale-root.
//
// Real incident, today: LACQUER_ROOT pointed at a git checkout that was on a
// branch and locally dirty rather than a pinned release tag. `lacquer status`
// against it exited 0 and printed a clean, plausible table crediting a WRONG
// version — fooling three separate sessions, two of which stated the wrong
// number to a human.
//
// internal/rootcheck already exists and already computes exactly this signal
// (Dirty, Behind, Warning()) — `sync` calls it (cmd/lacquer/main.go:149) and
// prints its Describe()/Warning() before rendering. `status` does not call it
// at all (grep -n rootcheck cmd/lacquer/main.go — it appears once, in the sync
// case, never in the status case). status.Rows reads VERSION straight off
// disk via version.Read and reports it as fact.
//
// Known-correct verdict (machine-checkable): given a LACQUER_ROOT that is
// dirty and on a branch, `lacquer status`'s combined output must contain SOME
// signal that the root's content is unverified, before it prints the version
// as though it were settled fact. Silence is a wrong answer, not a missing
// feature — see doc.go on why this suite grades verdicts, not code coverage.
func TestScenarioStaleRoot(t *testing.T) {
	bin := buildLacquer(t)

	// A synthetic, self-contained lacquer root — decoupled from this repo's
	// real content on purpose (see minimalLacquerRoot's doc comment).
	root := minimalLacquerRoot(t, "2.0.0")

	// The state that fooled three sessions today: a locally dirty, uncommitted
	// edit to VERSION on a branch (not a detached checkout at a pinned tag).
	// This is exactly what happens when someone hand-edits VERSION to test
	// something and forgets, or points LACQUER_ROOT at a half-finished feature
	// worktree.
	versionPath := filepath.Join(root, "VERSION")
	if err := os.WriteFile(versionPath, []byte("99.99.99\n"), 0o644); err != nil {
		t.Fatalf("setup failed: dirty the root's VERSION: %v", err)
	}
	if branch := strings.TrimSpace(runGit(t, root, "rev-parse", "--abbrev-ref", "HEAD")); branch != "main" {
		t.Fatalf("setup failed: expected root on branch main, got %q", branch)
	}
	if status := runGit(t, root, "status", "--porcelain"); status == "" {
		t.Fatalf("setup failed: root is not actually dirty after the edit")
	}

	project := minimalProject(t)

	res := runLacquer(t, bin, project, map[string]string{
		"LACQUER_ROOT":     root,
		"LACQUER_NO_FETCH": "1", // no upstream anyway; keeps this test offline
	}, "status")

	all := res.Combined()

	if res.Code != 0 {
		// Not the failure this scenario is about, but a setup problem if it
		// happens: `lacquer status` is documented to exit 0 in this state.
		t.Fatalf("setup failed: `lacquer status` exited %d, want 0 (this scenario is about a MISLEADING success, not a failure):\n%s", res.Code, all)
	}

	// The bogus version must not be reported as unqualified fact: SOMETHING in
	// the output has to flag the root as dirty/unverified/unpinned.
	unverifiedMarkers := []string{"dirty", "uncommitted", "unverified", "cannot be trusted", "not pinned", "STALE"}
	flagged := false
	for _, m := range unverifiedMarkers {
		if strings.Contains(strings.ToLower(all), strings.ToLower(m)) {
			flagged = true
			break
		}
	}
	// Marked, strict expected-failure: issue #350. `flagged` is this
	// scenario's verdict condition (true = the bug is fixed, status now
	// flags a dirty root). See expect.go's expectKnownFailure — this is the
	// ONLY line in this test routed through it; every setup check above
	// still calls t.Fatalf directly and can never be absorbed by this.
	expectKnownFailure(t, issueStaleRoot, flagged,
		"`lacquer status` printed version 99.99.99 as fact "+
			"with no dirty/unverified/unpinned marker in its output, even though the root "+
			"is a dirty checkout on branch main rather than a pinned release. "+
			"internal/rootcheck computes this signal already (Dirty, Warning()) but the "+
			"status case in cmd/lacquer/main.go never calls it — only the sync case does.\nfull output:\n%s", all)
}

// Mutation-tested: internal/version.Read was temporarily changed to always
// return a hardcoded 6.6.6 regardless of the VERSION file on disk; that made
// TestScenarioStaleRootCleanRootIsQuiet below fail ("a clean, pinned root's
// real version does not appear in `lacquer status` output at all"), confirming
// that test actually reads status's real output rather than trusting a
// hardcoded expectation. Reverted after confirming. TestScenarioStaleRoot
// above is the positive case: it reproduces lacquer's current, unmodified
// behaviour (`status` never consults internal/rootcheck) — see the package
// doc's "live finding" note — which is itself the mutation proof for that
// assertion, the un-mutated implementation IS the known-bad state this
// scenario exists to catch. It is marked as a strict expected-failure against
// issue #350 (see expect.go's expectKnownFailure) so this known, tracked bug
// does not turn the eval-suite job red; removing that marker without first
// fixing #350 is itself one of expect.go's own mutation tests (see
// expect_test.go and this scenario's own mutation-testing record in the PR
// that added the marker).
func TestScenarioStaleRootCleanRootIsQuiet(t *testing.T) {
	defer recordScenario(t) // unmarked: tallied into the package summary as-is.
	bin := buildLacquer(t)
	root := minimalLacquerRoot(t, "2.0.0") // clean: no dirty edit, this IS the pinned state
	project := minimalProject(t)

	res := runLacquer(t, bin, project, map[string]string{
		"LACQUER_ROOT":     root,
		"LACQUER_NO_FETCH": "1",
	}, "status")

	if res.Code != 0 {
		t.Fatalf("setup failed: `lacquer status` exited %d against a clean root:\n%s", res.Code, res.Combined())
	}
	if !strings.Contains(res.Stdout, "2.0.0") {
		t.Errorf("a clean, pinned root's real version does not appear in `lacquer status` output at all:\n%s", res.Combined())
	}
}
