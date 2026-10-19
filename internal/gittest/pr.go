package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PR is a pull-request fixture shaped like a GitHub pull_request run: an origin
// holding the base branch (main) and GitHub's merge commit for one PR at
// refs/pull/1/merge, and Checkout, a depth-1 fetch of that merge commit made the
// way actions/checkout makes it. Checkout therefore lacks the merge commit's
// parents, as CI's does.
type PR struct {
	Origin, Work, Checkout string
	// Merge is the merge commit's sha; Base is its first parent.
	Merge, Base string
}

// NewPR commits files as the base, applies change to a PR branch in a working
// clone, merges the branch into main with --no-ff (main first, PR second, as
// GitHub orders a merge commit's parents) and publishes the merge commit as
// refs/pull/1/merge. main on the origin stays at the base: the merge commit is
// only a ref, as on GitHub.
func NewPR(t testing.TB, files map[string]string, change func(work string)) PR {
	t.Helper()
	root := t.TempDir()
	p := PR{Origin: filepath.Join(root, "origin.git"), Work: filepath.Join(root, "work"), Checkout: filepath.Join(root, "checkout")}
	for _, dir := range []string{p.Origin, p.Work, p.Checkout} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	Init(t, p.Origin, "-q", "--bare", "-b", "main")
	Init(t, p.Work, "-q", "-b", "main")
	Write(t, p.Work, files)
	commit(t, p.Work, "base")
	p.Base = Git(t, p.Work, "rev-parse", "HEAD")
	Git(t, p.Work, "checkout", "-q", "-b", "pr")
	change(p.Work)
	commit(t, p.Work, "pr")
	Git(t, p.Work, "checkout", "-q", "main")
	Git(t, p.Work, "merge", "-q", "--no-ff", "-m", "Merge pr into main", "pr")
	p.Merge = Git(t, p.Work, "rev-parse", "HEAD")
	Git(t, p.Work, "push", "-q", "file://"+p.Origin, p.Base+":refs/heads/main", p.Merge+":refs/pull/1/merge")

	Init(t, p.Checkout, "-q")
	Git(t, p.Checkout, "remote", "add", "origin", "file://"+p.Origin)
	Git(t, p.Checkout, "fetch", "-q", "--no-tags", "--depth=1", "origin", "+refs/pull/1/merge:refs/remotes/pull/1/merge")
	Git(t, p.Checkout, "checkout", "-q", "--detach", "refs/remotes/pull/1/merge")
	if _, err := os.Stat(filepath.Join(p.Checkout, ".git", "shallow")); err != nil {
		t.Fatalf("the PR checkout is not shallow, so it does not model CI: %v", err)
	}
	if out, err := exec.Command("git", "-C", p.Checkout, "cat-file", "-e", p.Base+"^{commit}").CombinedOutput(); err == nil {
		t.Fatalf("the PR checkout already holds the base commit, so a fetch of it is never exercised: %s", out)
	}
	return p
}

// AdvanceBase commits files onto the origin's main AFTER the merge commit was
// made, as a later merge to the base branch does. The event's base.sha can name
// such a commit; the merge commit's first parent still names the base it was
// built on.
func (p PR) AdvanceBase(t testing.TB, files map[string]string) string {
	t.Helper()
	Git(t, p.Work, "checkout", "-q", "-B", "advance", p.Base)
	Write(t, p.Work, files)
	commit(t, p.Work, "base moved on")
	sha := Git(t, p.Work, "rev-parse", "HEAD")
	Git(t, p.Work, "push", "-q", "file://"+p.Origin, sha+":refs/heads/main")
	return sha
}

// Write writes each file under dir; an empty body deletes the file.
func Write(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if body == "" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Git runs git in dir with a fixed identity and no signing, failing the test on
// error, and returns trimmed stdout.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.email=t@example.invalid", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("git %v in %s: %v\n%s%s", args, dir, err, out, stderr)
	}
	return strings.TrimSpace(string(out))
}

func commit(t testing.TB, dir, msg string) {
	t.Helper()
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "--allow-empty", "--no-verify", "-m", msg)
}
