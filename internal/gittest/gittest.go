// Package gittest creates the throwaway git repositories tests run against, with
// git's background housekeeping switched off in each one.
//
// Every test repository in this module goes through Init or Clone. The reason
// is a flake that turned unrelated pull requests red at random: t.TempDir's
// cleanup failing with "unlinkat .../.git/objects: directory not empty".
//
// Since git 2.47 a commit (and merge, rebase, am, fetch, a push's receive-pack)
// ends by starting `git maintenance run --auto --detach`. That process takes
// .git/objects/maintenance.lock, forks, and the commit returns while the fork is
// still running. From git 2.5x the auto strategy is "geometric", whose repack
// trigger estimates loose objects from a sample, so a repository of a few dozen
// objects trips it at random (about 1 in 60, measured) and the fork writes new
// packs into .git/objects after the test has already returned. testing's cleanup
// lists the directory, removes what it saw, and fails on the entries the fork
// created meanwhile; it retries only on Windows.
//
// The settings live in the repository's own config rather than the test's
// environment so they also govern every git command the CODE UNDER TEST runs in
// that repository, and so they cannot collide with t.Parallel (t.Setenv panics
// there, and internal/shipped runs in parallel).
package gittest

import (
	"fmt"
	"os/exec"
	"testing"
)

// Config is written into every repository Init and Clone create.
//
// Only the first entry is load-bearing on a current git, and it is the only one
// a test here can break: the rest are backstops for older gits or for paths
// that stop mattering once the first is set, so removing one fails nothing.
var Config = []struct{ Key, Value string }{
	// The switch that matters: commands no longer start `maintenance run
	// --auto` at all (git >= 2.29). It must be set explicitly, because
	// maintenance.auto=true in a developer's global config overrides gc.auto.
	{"maintenance.auto", "false"},
	// Before 2.29 commands start `gc --auto` directly, and 0 disables that.
	// Later gits also treat it as "off" when maintenance.auto is unset.
	{"gc.auto", "0"},
	// If anything still runs auto-maintenance, run it in the foreground, so the
	// command that started it has finished writing by the time it returns.
	{"maintenance.autoDetach", "false"},
	{"gc.autoDetach", "false"},
	// A developer's global core.fsmonitor=true makes `git status` start a
	// daemon that keeps writing under .git after the test. Not seen causing
	// this flake; it is the same shape, and costs one line to rule out.
	{"core.fsmonitor", "false"},
}

// Init runs `git init` with args in dir, then writes Config into the new
// repository. A failure is fatal: a test that cannot build its repository has
// not tested anything.
func Init(t testing.TB, dir string, args ...string) {
	t.Helper()
	run(t, dir, append([]string{"init"}, args...)...)
	for _, c := range Config {
		run(t, dir, "config", c.Key, c.Value)
	}
}

// Clone runs `git clone` with args, src and dst, writing Config into the clone
// via `-c` (which persists into the new repository's config). It returns the
// error with git's output rather than failing, because callers differ on
// whether an unclonable source is a failure or a reason to skip.
func Clone(src, dst string, args ...string) error {
	full := []string{"clone"}
	for _, c := range Config {
		full = append(full, "-c", c.Key+"="+c.Value)
	}
	full = append(append(full, args...), src, dst)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %v %s %s: %w\n%s", args, src, dst, err, out)
	}
	return nil
}

func run(t testing.TB, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}
