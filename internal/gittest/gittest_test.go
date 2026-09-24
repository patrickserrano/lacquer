package gittest_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// hostileGlobalConfig writes a global git config that asks for background
// maintenance as loudly as a developer's machine could. Without it the probe
// depends on whoever runs it: this machine's global config sets none of these,
// so a helper relying on gc.auto=0 alone passed here, while maintenance.auto=true
// in someone's ~/.gitconfig overrides gc.auto and would have brought the flake
// back for them.
func hostileGlobalConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gitconfig")
	body := "[maintenance]\n\tauto = true\n\tautoDetach = true\n[gc]\n\tauto = 6700\n\tautoDetach = true\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// commitTraced commits one new file in repo with git's trace2 output captured,
// and returns every child process git started. extra is passed to git before
// the subcommand.
func commitTraced(t *testing.T, repo string, extra ...string) []string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(repo), 0o644); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace2")
	global := hostileGlobalConfig(t)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "c"}} {
		cmd := exec.Command("git", append(append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, extra...), args...)...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_TRACE2="+trace, "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	body, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("git wrote no trace2 output: %v", err)
	}
	// A trace that never saw the commit would report no children whatever git
	// did, so the absence assertions below would pass on a probe that is blind.
	if !strings.Contains(string(body), "cmd_name commit") {
		t.Fatalf("trace2 output does not cover the commit:\n%s", body)
	}
	var children []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, "child_start") {
			children = append(children, line)
		}
	}
	return children
}

// autoMaintenance returns the children that are git's post-command housekeeping:
// `maintenance run --auto` since git 2.29, `gc --auto` before it.
func autoMaintenance(children []string) []string {
	var hits []string
	for _, c := range children {
		if strings.Contains(c, "maintenance run --auto") || strings.Contains(c, "gc --auto") {
			hits = append(hits, c)
		}
	}
	return hits
}

// The probe must be able to see the thing it asserts is absent. A plain
// `git init` repository runs auto-maintenance after a commit; it is forced into
// the foreground here so this control cannot itself race TempDir cleanup.
func TestProbeSeesAutoMaintenanceInAPlainRepository(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	children := commitTraced(t, repo, "-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false")
	if len(autoMaintenance(children)) == 0 {
		t.Fatalf("a plain repository's commit started no auto-maintenance, so the probe cannot "+
			"tell the fix from a blind check; children:\n%s", strings.Join(children, "\n"))
	}
}

// The defect: git 2.55's commit starts `git maintenance run --auto --detach`,
// which outlives the commit and can repack into .git/objects while t.TempDir's
// cleanup is removing it ("unlinkat .git/objects: directory not empty").
func TestCommitInAnInitRepositoryStartsNoAutoMaintenance(t *testing.T) {
	repo := t.TempDir()
	gittest.Init(t, repo, "-q")
	if hits := autoMaintenance(commitTraced(t, repo)); len(hits) != 0 {
		t.Fatalf("commit in a gittest.Init repository started background maintenance:\n%s",
			strings.Join(hits, "\n"))
	}
}

// A clone is a new repository with its own config: Init's settings on the
// origin do not travel with it, so Clone must write them itself.
func TestCommitInACloneStartsNoAutoMaintenance(t *testing.T) {
	origin := t.TempDir()
	gittest.Init(t, origin, "-q")
	commitTraced(t, origin)
	clone := filepath.Join(t.TempDir(), "clone")
	if err := gittest.Clone(origin, clone, "-q"); err != nil {
		t.Fatal(err)
	}
	if hits := autoMaintenance(commitTraced(t, clone)); len(hits) != 0 {
		t.Fatalf("commit in a gittest.Clone repository started background maintenance:\n%s",
			strings.Join(hits, "\n"))
	}
}

// rawRepoCreation matches a test creating a repository with git directly
// instead of through this package: `"git", "init"`, `git(t, dir, "init", ...)`,
// `run("init", ...)`, `{"init", "-q"}` and the same shapes for clone. A string
// list naming lacquer's own subcommands ("audit", "init") does not match, and
// neither does a commit message (`p.commit("init")`): "init" there follows
// another string or a call that is not a git runner.
var rawRepoCreation = regexp.MustCompile(`(\b(git|gitIn|gitRun|runGit|run)\(|\{|"git",\s*|\bt,\s*\w+,\s*)"(init|clone)"`)

// Every repository a test makes has to come from Init or Clone, or the flake
// comes back one test at a time, rarely enough that nobody connects it to the
// test that brought it. This fails on the first test that bypasses them.
func TestNoTestCreatesARepositoryWithoutGittest(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var scanned int
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", ".claude":
				return filepath.SkipDir
			}
			if path == self {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Test code only: _test.go files, plus the build-tagged helpers
		// (internal/eval) that import testing without being _test.go files.
		if !strings.HasSuffix(path, "_test.go") && !strings.Contains(string(body), "\t\"testing\"\n") {
			return nil
		}
		scanned++
		for i, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if rawRepoCreation.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A walk that found no test files would pass whatever the tree contains.
	if scanned < 50 {
		t.Fatalf("scanned only %d test files under %s; the walk is not seeing the module", scanned, root)
	}
	if len(offenders) > 0 {
		t.Errorf("create these repositories with gittest.Init / gittest.Clone:\n%s", strings.Join(offenders, "\n"))
	}
}
