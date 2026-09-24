package gittest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Run checks that a package's tests leave the enclosing checkout's registered
// worktrees unchanged. An empty TempDir inside a checkout is NOT a repository
// boundary: git can discover and modify the enclosing repository (#461).
func Run(m *testing.M) int {
	// Dispatch may update the global excludes file. Never let a test write
	// the developer's config; subprocesses inherit this isolated global file.
	scratch, err := os.MkdirTemp("", "lacquer-git-global-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(scratch)
	config := filepath.Join(scratch, "config")
	if out, err := exec.Command("git", "config", "--file", config, "core.excludesFile", filepath.Join(scratch, "ignore")).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "isolate global excludes: %v: %s", err, out)
		return 1
	}
	if err := os.Setenv("GIT_CONFIG_GLOBAL", config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	snapshot := func() ([]byte, error) {
		return exec.Command("git", "-C", cwd, "worktree", "list", "--porcelain").CombinedOutput()
	}
	before, err := snapshot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot enclosing worktrees: %v\n%s", err, before)
		return 1
	}
	code := m.Run()
	after, err := snapshot()
	if err != nil || !bytes.Equal(before, after) {
		fmt.Fprintf(os.Stderr, "tests changed enclosing worktrees (%v):\nbefore:\n%s\nafter:\n%s", err, before, after)
		return 1
	}
	return code
}
