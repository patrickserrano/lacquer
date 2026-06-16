// Package gitguard reports whether a file in a project has uncommitted changes,
// so sync can refuse to overwrite unsaved work.
package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InWorkTree reports whether dir is inside a git working tree. A directory that
// is not a git repository returns (false, nil). Any non-exit failure (e.g. git
// not installed) is returned as an error. Callers that require git for safety
// should treat a false result as a refusal, not as permission to proceed.
func InWorkTree(dir string) (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		// A non-zero git exit (e.g. 128 "not a git repository") is a definitive
		// "no", not an operational error.
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// Dirty reports whether relPath inside projectRoot has uncommitted modifications
// or is an untracked existing file. A committed-and-unmodified file, or a file
// that does not exist, is clean (false). Errors from git are returned.
func Dirty(projectRoot, relPath string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(projectRoot, relPath)); os.IsNotExist(err) {
		return false, nil
	}
	cmd := exec.Command("git", "status", "--porcelain", "--", relPath)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	// Any porcelain output for the path means it differs from HEAD/index or is
	// untracked. No output means clean.
	return strings.TrimSpace(string(out)) != "", nil
}
