// Package gitguard reports whether a file in a project has uncommitted changes,
// so sync can refuse to overwrite unsaved work.
package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
