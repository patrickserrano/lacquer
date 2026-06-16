package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
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

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirty(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")

	// Committed + unmodified => clean.
	write(t, filepath.Join(repo, "a.txt"), "v1\n")
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-qm", "add a")
	if dirty, err := Dirty(repo, "a.txt"); err != nil || dirty {
		t.Errorf("committed file: dirty=%v err=%v, want clean", dirty, err)
	}

	// Modified after commit => dirty.
	write(t, filepath.Join(repo, "a.txt"), "v2 local edit\n")
	if dirty, err := Dirty(repo, "a.txt"); err != nil || !dirty {
		t.Errorf("modified file: dirty=%v err=%v, want dirty", dirty, err)
	}

	// Untracked existing file => dirty.
	write(t, filepath.Join(repo, "b.txt"), "new\n")
	if dirty, err := Dirty(repo, "b.txt"); err != nil || !dirty {
		t.Errorf("untracked file: dirty=%v err=%v, want dirty", dirty, err)
	}

	// Non-existent file => clean (nothing to clobber).
	if dirty, err := Dirty(repo, "missing.txt"); err != nil || dirty {
		t.Errorf("missing file: dirty=%v err=%v, want clean", dirty, err)
	}
}
