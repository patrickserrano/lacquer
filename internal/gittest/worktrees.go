package gittest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// Run checks that a package's tests leave the enclosing checkout's registered
// worktrees unchanged. An empty TempDir inside a checkout is NOT a repository
// boundary: git can discover and modify the enclosing repository (#461).
func Run(m *testing.M) int {
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
