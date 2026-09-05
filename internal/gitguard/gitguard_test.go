package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
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

// gitOut runs git for its stdout, so a test can check what git itself reports
// before asserting what gitguard makes of it.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirtyIn is a readability helper: DirtyPaths answers for the whole worktree at
// once, so every assertion below is a membership question about one path.
func dirtyIn(t *testing.T, repo string) map[string]bool {
	t.Helper()
	set, err := DirtyPaths(repo)
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}
	return set
}

func TestDirtyPaths(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")

	// Committed + unmodified => clean.
	write(t, filepath.Join(repo, "a.txt"), "v1\n")
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-qm", "add a")
	if dirtyIn(t, repo)["a.txt"] {
		t.Error("committed, unmodified file reported dirty")
	}

	// Modified after commit => dirty.
	write(t, filepath.Join(repo, "a.txt"), "v2 local edit\n")
	if !dirtyIn(t, repo)["a.txt"] {
		t.Error("modified file reported clean")
	}

	// Staged change => dirty. Staged-and-not-further-modified shows as "M " —
	// a clean worktree column — and the guard must still refuse, or `git add`
	// alone would be enough to make sync discard the work.
	git(t, repo, "add", "a.txt")
	if !dirtyIn(t, repo)["a.txt"] {
		t.Error("staged change reported clean")
	}
	git(t, repo, "commit", "-qm", "commit the staged edit")

	// Untracked existing file at the repo root => dirty.
	write(t, filepath.Join(repo, "b.txt"), "new\n")
	if !dirtyIn(t, repo)["b.txt"] {
		t.Error("untracked file reported clean")
	}

	// A path that simply does not exist is not in the set at all.
	if dirtyIn(t, repo)["missing.txt"] {
		t.Error("non-existent path reported dirty")
	}
}

// TestDirtyPathsSeesInsideAnUntrackedDirectory is the --untracked-files=all
// regression guard. Plain --porcelain collapses a wholly-untracked directory to
// a single "dir/" entry, so a nested asset would never match by exact path and
// the guard would silently stop protecting it — a sync that clobbers new work
// while reporting success. That silent weakening is the failure class this
// package exists to prevent, so it gets a test of its own.
func TestDirtyPathsSeesInsideAnUntrackedDirectory(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "seed.txt"), "x\n")
	git(t, repo, "add", "seed.txt")
	git(t, repo, "commit", "-qm", "seed")

	// Nothing under .claude/ is tracked, so the whole directory is new — the
	// exact shape of a project adopting the lacquer for the first time.
	write(t, filepath.Join(repo, ".claude", "commands", "build.md"), "LOCAL\n")

	// Sanity: without -uall git really would collapse this to a directory entry.
	if out := gitOut(t, repo, "status", "--porcelain"); strings.Contains(out, "build.md") {
		t.Logf("note: default status already lists the file (%q); the -uall assertion is still the contract", strings.TrimSpace(out))
	}

	if !dirtyIn(t, repo)[".claude/commands/build.md"] {
		t.Errorf("untracked file nested in a new directory reported clean; set = %v", dirtyIn(t, repo))
	}
}

// TestDirtyPathsIgnoresAWorktreeDeletion pins the pre-batch behaviour exactly:
// git reports a tracked-but-deleted file as changed, yet there is no file on
// disk to clobber, so sync must still proceed. The old per-path Dirty() reached
// this answer by short-circuiting on Lstat before ever consulting git; a naive
// set-membership port would call it dirty and make sync refuse where it used to
// work.
func TestDirtyPathsIgnoresAWorktreeDeletion(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "gone.txt"), "x\n")
	write(t, filepath.Join(repo, "staged-gone.txt"), "x\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "two files")

	if err := os.Remove(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	// Without this, a bug that made DirtyPaths return nothing at all would pass.
	if out := gitOut(t, repo, "status", "--porcelain"); !strings.Contains(out, "gone.txt") {
		t.Fatalf("git does not report the deletion; the assertion below would be vacuous. status = %q", out)
	}
	if dirtyIn(t, repo)["gone.txt"] {
		t.Error("deleted tracked file reported dirty; sync would refuse where it used to proceed")
	}

	// A staged deletion is the same story from the index side.
	git(t, repo, "rm", "-q", "staged-gone.txt")
	if dirtyIn(t, repo)["staged-gone.txt"] {
		t.Error("staged deletion reported dirty")
	}
}

// TestDirtyPathsParsesRenames covers the two-field rename record. Under -z a
// rename emits the NEW path and then a bare origin path carrying NO status
// prefix. A parser that does not consume that second field treats it as another
// entry and strips its first three bytes as if they were "XY ".
//
// The fixture is built so that mis-slicing produces a real, clean file rather
// than a harmless nonexistent one: the rename origin is "ab/clean-file.txt", so
// dropping three bytes yields "clean-file.txt", which is committed, untouched,
// and present on disk. A naive parser therefore reports a clean file as dirty
// and sync refuses for no reason. Without that construction the bug hides — the
// mis-sliced path usually fails the existence check and disappears quietly.
func TestDirtyPathsParsesRenames(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "ab", "clean-file.txt"), "content substantial enough for rename detection\n")
	write(t, filepath.Join(repo, "clean-file.txt"), "an innocent bystander\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "seed")

	git(t, repo, "mv", "ab/clean-file.txt", "renamed.txt")
	// Sorts after the rename entry, so it also covers plain framing loss.
	write(t, filepath.Join(repo, "zzz.txt"), "untracked\n")

	// Without an actual R record this test proves nothing, so assert git emitted one.
	if out := gitOut(t, repo, "status", "--porcelain"); !strings.Contains(out, "R ") {
		t.Fatalf("git did not detect a rename; the parser assertions would be vacuous. status = %q", out)
	}

	set := dirtyIn(t, repo)
	if !set["renamed.txt"] {
		t.Errorf("rename destination reported clean; set = %v", set)
	}
	if set["ab/clean-file.txt"] {
		t.Error("rename origin reported dirty, but it no longer exists on disk")
	}
	if set["clean-file.txt"] {
		t.Errorf("the rename's origin field was mis-parsed into an unrelated clean path; set = %v", set)
	}
	if !set["zzz.txt"] {
		t.Errorf("the entry following a rename was lost or corrupted; set = %v", set)
	}
	for k := range set {
		if strings.ContainsRune(k, 0) || strings.HasPrefix(k, " ") {
			t.Errorf("malformed key parsed out of -z output: %q", k)
		}
	}
}

// TestDirtyPathsHandlesAwkwardFilenames is the -z justification. Plain
// --porcelain returns these paths C-quoted — wrapped in double quotes with the
// non-ASCII bytes escaped — which compares unequal to the asset path the caller
// looks up, so the guard would quietly stop protecting exactly the files whose
// names are hardest to eyeball.
func TestDirtyPathsHandlesAwkwardFilenames(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")

	names := []string{
		"a file with spaces.md",
		"café-crème.md",
		".claude/skills/naïve name/SKILL.md",
	}
	for _, n := range names {
		write(t, filepath.Join(repo, filepath.FromSlash(n)), "x\n")
	}

	// Prove the premise: the un-suppressed quoting really does mangle these.
	if out := gitOut(t, repo, "status", "--porcelain"); !strings.Contains(out, `"`) {
		t.Logf("note: this git does not quote these names (%q); the exact-match assertion still stands", strings.TrimSpace(out))
	}

	set := dirtyIn(t, repo)
	for _, n := range names {
		if !set[n] {
			t.Errorf("awkward filename %q reported clean; set = %v", n, set)
		}
	}
}

// TestDirtyPathsReportsAFlagLikeFilename keeps the argument-injection concern
// covered. The batched query passes no path to git at all, so injection is
// structurally impossible now — but a file named like a flag must still be seen,
// which is the half of the old test that was about behaviour rather than
// mechanism.
func TestDirtyPathsReportsAFlagLikeFilename(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "-rf"), "x\n")
	if !dirtyIn(t, repo)["-rf"] {
		t.Error("flag-like filename reported clean")
	}
}

// TestDirtyPathsErrorsOnNonGitDir locks in fail-closed behavior: a non-git
// directory must surface an error, not silently return an empty (clean) set,
// which would bypass the guard entirely.
func TestDirtyPathsErrorsOnNonGitDir(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "x\n")
	if _, err := DirtyPaths(dir); err == nil {
		t.Error("non-git dir: want error, got nil (guard would be bypassed)")
	}
}

// TestDirtyPathsIsScopedToTheWholeRepo documents the property the batched form
// relies on: the set covers every dirty path in the repository, and callers are
// expected to look up only the paths they care about. Dirt elsewhere must not
// leak into a caller's decision, which membership testing gives for free.
func TestDirtyPathsIsScopedToTheWholeRepo(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "unrelated", "notes.txt"), "scratch\n")
	set := dirtyIn(t, repo)
	if !set["unrelated/notes.txt"] {
		t.Errorf("unrelated dirt is missing from the whole-repo set; set = %v", set)
	}
	if set[".claude/commands/build.md"] {
		t.Error("a path that was never touched appears in the set")
	}
}

// TestDirtyPathsInASubdirectoryOfARepo is the path-root guard. Porcelain output
// is relative to the REPOSITORY root even when git runs from a subdirectory, but
// callers pass paths relative to the directory they handed in — InWorkTree
// accepts a component directory inside a monorepo, so those two roots are not
// always the same. Without stripping the prefix, every lookup misses and the
// guard silently protects nothing while still reporting success.
func TestDirtyPathsInASubdirectoryOfARepo(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "README.md"), "seed\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "seed")

	component := filepath.Join(repo, "apps", "web")
	write(t, filepath.Join(component, ".claude", "commands", "build.md"), "local\n")

	// Sanity: git really does report this repo-root-relative.
	if out := gitOut(t, repo, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(out, "apps/web/.claude") {
		t.Fatalf("premise broken: git did not report a repo-root-relative path. status = %q", out)
	}

	set, err := DirtyPaths(component)
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}
	if !set[".claude/commands/build.md"] {
		t.Errorf("path not re-rooted onto the caller's directory; set = %v", set)
	}
	if set["apps/web/.claude/commands/build.md"] {
		t.Errorf("repo-root-relative path leaked through; set = %v", set)
	}
}

// TestDirtyPathsIgnoresDirtOutsideTheGivenDirectory pairs with the test above:
// re-rooting must not be the only thing keeping a sibling component's
// work-in-progress out of the answer. The per-path form this replaced never saw
// it, because it only ever asked about paths under the directory it was given.
func TestDirtyPathsIgnoresDirtOutsideTheGivenDirectory(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, "README.md"), "seed\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "seed")

	component := filepath.Join(repo, "apps", "web")
	write(t, filepath.Join(component, "keep.txt"), "x\n")
	// Uncommitted work in a sibling component and at the repo root.
	write(t, filepath.Join(repo, "apps", "api", "keep.txt"), "x\n")
	write(t, filepath.Join(repo, "NOTES.md"), "x\n")

	set, err := DirtyPaths(component)
	if err != nil {
		t.Fatalf("DirtyPaths: %v", err)
	}
	if !set["keep.txt"] {
		t.Errorf("the component's own dirt is missing; set = %v", set)
	}
	if len(set) != 1 {
		t.Errorf("dirt from outside the given directory leaked in; set = %v", set)
	}
}

func TestInWorkTree(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	if in, err := InWorkTree(repo); err != nil || !in {
		t.Errorf("git repo: in=%v err=%v, want true,nil", in, err)
	}

	nongit := t.TempDir()
	if in, err := InWorkTree(nongit); err != nil || in {
		t.Errorf("non-git dir: in=%v err=%v, want false,nil", in, err)
	}
}
