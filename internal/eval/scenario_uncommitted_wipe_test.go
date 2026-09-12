//go:build eval

package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gitguard"
)

// Scenario: uncommitted-wipe.
//
// Real incident, today: uncommitted feature work sat in a file, then
// `git checkout -- <file>` ran on it and the work was gone — redone by hand
// afterward. There is no lacquer command that performs this; the "detector" a
// careful agent (or a hook) would consult before running a destructive git
// command is internal/gitguard.DirtyPaths, which sync's own asset preflight
// already relies on (internal/assets/assets.go) to refuse to overwrite
// unsaved work. This scenario grades that same primitive against the exact
// shape of the incident, both directions:
//
//   - BEFORE anything is committed, the at-risk file must be detectable as
//     holding uncommitted work `git checkout -- <file>` would destroy.
//   - Committing first (the correct verdict: "refuse / commit first") must
//     make the file read as safe, and a checkout run AFTER that commit must
//     provably not alter its content — proving the remedy actually resolves
//     the exact risk that was flagged, not a different one.
//   - As a negative control run against a THROWAWAY copy (never the fixture
//     the rest of the test still needs): actually running
//     `git checkout -- <file>` WITHOUT committing first really does destroy
//     the uncommitted content. This is the known-bad case the doctor
//     principle asks for — proof the scenario is checking something real,
//     not a tautology that would pass no matter what gitguard reported.
func TestScenarioUncommittedWipe(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "feature.go")
	writeFile(t, target, "package x\n\n// v1: baseline\n")
	initGitRepo(t, dir, "baseline")

	// The incident: valuable feature work, edited but never committed.
	writeFile(t, target, "package x\n\n// v2: a day of uncommitted feature work\n")

	dirtyBefore, err := gitguard.DirtyPaths(dir)
	if err != nil {
		t.Fatalf("setup failed: gitguard.DirtyPaths: %v", err)
	}
	if !dirtyBefore["feature.go"] {
		t.Errorf("verdict not reached: gitguard.DirtyPaths did not flag feature.go as holding " +
			"uncommitted work — a check that consulted it before running " +
			"`git checkout -- feature.go` would have found nothing to refuse on, and the work " +
			"would be destroyed exactly as it was in the real incident")
	}

	// The known-bad case, proven on a throwaway COPY so the fixture above is
	// never touched: running the destructive command without committing first
	// really does discard the uncommitted content. If this assertion ever
	// failed, the whole scenario would be moot — there would be nothing to
	// refuse.
	unsafeCopy := t.TempDir()
	copyDir(t, dir, unsafeCopy)
	runGit(t, unsafeCopy, "checkout", "--", "feature.go")
	lost, err := os.ReadFile(filepath.Join(unsafeCopy, "feature.go"))
	if err != nil {
		t.Fatalf("setup failed: read post-checkout file: %v", err)
	}
	if string(lost) != "package x\n\n// v1: baseline\n" {
		t.Fatalf("setup failed: the negative control did not reproduce the destructive checkout "+
			"(got %q) — the scenario's premise does not hold", lost)
	}

	// The correct verdict: commit first. Only then is a checkout safe.
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "save the feature work")

	dirtyAfter, err := gitguard.DirtyPaths(dir)
	if err != nil {
		t.Fatalf("setup failed: gitguard.DirtyPaths after commit: %v", err)
	}
	if dirtyAfter["feature.go"] {
		t.Errorf("verdict not reached: feature.go still reads as at-risk after committing it — " +
			"the guard would refuse the safe procedure forever, which is as unusable as never " +
			"refusing at all")
	}

	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("setup failed: read committed file: %v", err)
	}
	runGit(t, dir, "checkout", "--", "feature.go")
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("setup failed: read post-checkout file: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("verdict not reached: a checkout run AFTER committing altered feature.go's " +
			"content — committing first did not actually make the file safe")
	}
	if string(after) != "package x\n\n// v2: a day of uncommitted feature work\n" {
		t.Errorf("the committed feature work itself does not match what was written: %q", after)
	}
}

// copyTree-equivalent for a small git working tree, including .git: cp -a
// semantics via Go so this has no dependency on the host's aliased cp -i (see
// this repo's own CLAUDE.md on that alias).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("setup failed: copy %s -> %s: %v", src, dst, err)
	}
}

// Mutation-tested: changing `if !dirtyBefore["feature.go"]` to
// `if dirtyBefore["feature.go"]` (inverted) makes TestScenarioUncommittedWipe
// fail immediately, as expected of a mutation that would have the grader
// reject the healthy case. Restoring and instead corrupting the fixture setup
// — skipping the `writeFile(t, target, "...v2...")` edit entirely, so the
// working tree is never actually dirty — makes the same assertion fail with
// "did not flag feature.go", confirming the check is sensitive to the fixture,
// not just to itself. Both reverted after confirming.
