package console

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Separate processes dispatched from two linked checkouts must share the
// repository's lock. A process-local mutex or a lock in --git-dir cannot do it.
func TestWorktreeCreationAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("LACQUER_WORKTREE_TEST_DIR"); dir != "" {
		_, err := createWorktree(dir, "")
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, repo, "worktree", "add", "-q", "-b", "linked", linked)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	shellQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	gate := filepath.Join(bin, "adding")
	log := filepath.Join(bin, "calls")
	// Widen the real git operation's critical window, and reject overlap.
	// All successful calls still run real git and must register a worktree.
	script := fmt.Sprintf(`#!/bin/sh
if [ "$3" = worktree ] && [ "$4" = add ]; then
  gate=%s
  mkdir "$gate" 2>/dev/null || { echo 'overlapping git worktree add' >&2; exit 99; }
  trap 'rmdir "$gate"' EXIT
  echo add >> %s
  sleep 0.1
  %s "$@"
  exit $?
fi
exec %s "$@"
`, shellQuote(gate), shellQuote(log), shellQuote(realGit), shellQuote(realGit))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	const n = 8
	results := make(chan string, n)
	for i := 0; i < n; i++ {
		dir := repo
		if i%2 != 0 {
			dir = linked
		}
		go func() {
			cmd := exec.Command(os.Args[0], "-test.run=^TestWorktreeCreationAcrossProcesses$")
			cmd.Env = append(os.Environ(), "LACQUER_WORKTREE_TEST_DIR="+dir,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				results <- fmt.Sprintf("%v: %s", err, out)
				return
			}
			results <- ""
		}()
	}
	for i := 0; i < n; i++ {
		if failure := <-results; failure != "" {
			t.Error(failure)
		}
	}
	calls, err := os.ReadFile(log)
	if err != nil || strings.Count(string(calls), "add\n") != n {
		t.Errorf("want %d real adds, got %q (%v)", n, calls, err)
	}
	if paths, err := worktreePaths(repo); err != nil || len(paths) != n+2 {
		t.Errorf("registered worktrees = %v (%v), want %d", paths, err, n+2)
	}
}
