package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// gitRun runs one git command in dir, failing the test on error. gitInit in
// assets_test.go only covers init+add; the Preflight cases below need commits,
// deletions and edits on top of it.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// preflightFixture builds a lacquer home shipping one asset and a committed
// project that already contains it, then returns the project root and the plan.
// Each test below varies only what happens to the destination file afterwards,
// which is the entire surface of the dirty guard.
func preflightFixture(t *testing.T) (project string, plan []Asset) {
	t.Helper()
	h := t.TempDir()
	project = t.TempDir()
	write(t, filepath.Join(h, "core", "commands", "build.md"), "SHIPPED\n")

	write(t, filepath.Join(project, ".claude", "commands", "build.md"), "SHIPPED\n")
	gitInit(t, project)
	gitRun(t, project, "commit", "-qm", "init")

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) == 0 {
		t.Fatal("empty plan; the fixture would prove nothing")
	}
	return project, plan
}

// TestPreflightProceedsWhenAssetsAreClean is the other half of
// TestCopyRefusesDirtyTarget. A guard that refused everything would satisfy the
// refusal test on its own, so the permissive case needs asserting too.
func TestPreflightProceedsWhenAssetsAreClean(t *testing.T) {
	project, plan := preflightFixture(t)
	targets, err := Preflight(project, plan)
	if err != nil {
		t.Fatalf("Preflight on a clean project: %v", err)
	}
	if len(targets) != len(plan) {
		t.Errorf("got %d targets for %d assets", len(targets), len(plan))
	}
}

// TestPreflightRefusesADirtyAsset exercises the guard at the level where the
// old code called git once per asset, so the batched lookup is covered directly
// and not only through Copy.
func TestPreflightRefusesADirtyAsset(t *testing.T) {
	project, plan := preflightFixture(t)
	write(t, filepath.Join(project, ".claude", "commands", "build.md"), "LOCAL UNSAVED EDIT\n")

	_, err := Preflight(project, plan)
	if err == nil {
		t.Fatal("expected Preflight to refuse a dirty asset, got nil")
	}
	if !strings.Contains(err.Error(), "build.md") {
		t.Errorf("error should name the dirty asset: %v", err)
	}
}

// TestPreflightRefusesAnAssetInANewUntrackedDirectory is the
// --untracked-files=all guard stated at the level that matters. A project
// adopting the lacquer has no tracked .claude/ at all, so anything it hand-wrote
// there sits inside a wholly-untracked directory. Default porcelain collapses
// that to a single ".claude/" entry, which matches no asset path — and the sync
// would overwrite the file while reporting success.
func TestPreflightRefusesAnAssetInANewUntrackedDirectory(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	write(t, filepath.Join(h, "core", "commands", "build.md"), "SHIPPED\n")

	write(t, filepath.Join(project, "README.md"), "seed\n")
	gitInit(t, project)
	gitRun(t, project, "commit", "-qm", "init")
	write(t, filepath.Join(project, ".claude", "commands", "build.md"), "HAND WRITTEN, NOT COMMITTED\n")

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(project, plan); err == nil {
		t.Fatal("Preflight accepted an untracked asset nested in a new directory; the guard is not protecting it")
	}
}

// TestPreflightProceedsWhenAnAssetWasDeleted pins the behaviour the batched
// query could most easily have broken. git reports a tracked-but-deleted file as
// changed, but the old per-path check never asked git — Lstat short-circuited
// first. Sync must still proceed: there is no unsaved work on disk to lose.
func TestPreflightProceedsWhenAnAssetWasDeleted(t *testing.T) {
	project, plan := preflightFixture(t)
	if err := os.Remove(filepath.Join(project, ".claude", "commands", "build.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(project, plan); err != nil {
		t.Fatalf("Preflight refused a sync because an asset was deleted from the worktree: %v", err)
	}
}

// TestPreflightGuardsAProjectNestedInARepo covers the case where projectRoot is
// NOT the repository root — a component inside a monorepo, which InWorkTree
// accepts. `git status --porcelain` reports paths relative to the REPOSITORY
// root, so an unadjusted set-membership lookup against an asset's Dest would
// miss every time and the guard would silently protect nothing.
func TestPreflightGuardsAProjectNestedInARepo(t *testing.T) {
	h := t.TempDir()
	repo := t.TempDir()
	write(t, filepath.Join(h, "core", "commands", "build.md"), "SHIPPED\n")

	write(t, filepath.Join(repo, "README.md"), "seed\n")
	gitInit(t, repo)
	gitRun(t, repo, "commit", "-qm", "init")

	project := filepath.Join(repo, "apps", "web")
	write(t, filepath.Join(project, ".claude", "commands", "build.md"), "HAND WRITTEN, NOT COMMITTED\n")

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Preflight(project, plan); err == nil {
		t.Fatal("Preflight accepted a dirty asset in a project nested inside a repo; the guard is not protecting it")
	}

	// And the mirror image: committing it must let the sync through, so the
	// test above is not passing because nested projects are refused wholesale.
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-qm", "adopt")
	if _, err := Preflight(project, plan); err != nil {
		t.Fatalf("Preflight refused a clean nested project: %v", err)
	}
}

// TestPreflightIgnoresDirtElsewhereInTheRepo guards the one hazard the batched
// form introduces: DirtyPaths answers for the WHOLE worktree, so a version that
// asked "is anything dirty" rather than "is this asset dirty" would block every
// sync in any repository with work in progress — which is most of them.
func TestPreflightIgnoresDirtElsewhereInTheRepo(t *testing.T) {
	project, plan := preflightFixture(t)
	write(t, filepath.Join(project, "src", "main.go"), "package main // uncommitted work\n")
	write(t, filepath.Join(project, "NOTES.md"), "scratch\n")

	if _, err := Preflight(project, plan); err != nil {
		t.Fatalf("Preflight refused because of dirt outside the plan: %v", err)
	}
}
