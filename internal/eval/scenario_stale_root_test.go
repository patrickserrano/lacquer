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
// Real incident: LACQUER_ROOT pointed at a git checkout that was on a branch
// and locally dirty rather than a pinned release tag. `lacquer status` against
// it exited 0 and printed a clean, plausible table crediting a WRONG version —
// fooling three separate sessions, two of which stated the wrong number to a
// human.
//
// This scenario used to be a marked, strict expected-failure (issue #350):
// `status` never consulted internal/rootcheck at all, so this reproduced a
// known bug rather than proving a fix. That is no longer true —
// cmd/lacquer/main.go's stampAndVerifyRoot (called by status, audit, doctor,
// sync and every other command that reads LACQUER_ROOT) now refuses outright
// whenever the root cannot be proven a pinned release: detached HEAD, exactly
// on a tag matching VERSION, clean tree. A branch checkout — pinned or not,
// dirty or not — fails that test, so `status` here must now REFUSE (non-zero
// exit), not render a misleading version as fact. This test asserts the FIXED
// behaviour directly; TestScenarioStaleRootPinnedRootIsQuiet below is the
// positive control proving a genuinely pinned root still works normally.
func TestScenarioStaleRoot(t *testing.T) {
	recordScenario(t) // unmarked: issue #350 is fixed, this is an ordinary scenario now.
	bin := buildLacquer(t)

	// A synthetic, self-contained lacquer root — decoupled from this repo's
	// real content on purpose (see minimalLacquerRoot's doc comment).
	root := minimalLacquerRoot(t, "2.0.0")

	// The state that fooled three sessions originally: a locally dirty,
	// uncommitted edit to VERSION on a branch (not a detached checkout at a
	// pinned tag). This is exactly what happens when someone hand-edits
	// VERSION to test something and forgets, or points LACQUER_ROOT at a
	// half-finished feature worktree.
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

	// The fix: exit 0 here would BE the bug issue #350 tracks. "I could not
	// verify this root" and "this root is fine" must not share an exit code.
	if res.Code == 0 {
		t.Fatalf("`lacquer status` exited 0 against a dirty, branch-checked-out root — it must refuse "+
			"instead of printing 99.99.99 as fact (issue #350):\nfull output:\n%s", all)
	}

	// And it must say WHY, not just fail silently with a bare exit code.
	unverifiedMarkers := []string{"dirty", "uncommitted", "unverified", "cannot be verified", "not pinned", "branch"}
	flagged := false
	for _, m := range unverifiedMarkers {
		if strings.Contains(strings.ToLower(all), strings.ToLower(m)) {
			flagged = true
			break
		}
	}
	if !flagged {
		t.Errorf("`lacquer status` refused (exit %d) but its output names none of "+
			"dirty/uncommitted/unverified/pinned/branch as the reason:\n%s", res.Code, all)
	}
}

// Positive control: a checkout genuinely detached at a tag matching VERSION,
// with a clean tree, must run `status` normally — the fix must refuse the bad
// state without also refusing the good one.
func TestScenarioStaleRootPinnedRootIsQuiet(t *testing.T) {
	recordScenario(t) // unmarked: tallied into the package summary as-is.
	bin := buildLacquer(t)
	root := pinnedLacquerRoot(t, "2.0.0") // detached at v2.0.0: genuinely pinned
	project := minimalProject(t)

	res := runLacquer(t, bin, project, map[string]string{
		"LACQUER_ROOT":     root,
		"LACQUER_NO_FETCH": "1",
	}, "status")

	if res.Code != 0 {
		t.Fatalf("`lacquer status` exited %d against a genuinely pinned root, want 0:\n%s", res.Code, res.Combined())
	}
	if !strings.Contains(res.Stdout, "2.0.0") {
		t.Errorf("a clean, pinned root's real version does not appear in `lacquer status` output at all:\n%s", res.Combined())
	}
}
