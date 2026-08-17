// Package gitguard answers questions about a project's git state: whether a file
// has uncommitted changes, so sync can refuse to overwrite unsaved work, and
// which files the repository actually tracks, for the renders that must describe
// the repo as a service reading github.com would see it.
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

// Tracked lists the repository files under dir matching pathspecs, as
// forward-slash paths relative to dir. A directory that is not a git repository
// yields no files and no error: nothing is tracked there, definitively.
//
// This exists because "the project contains this file" and "the repository
// contains this file" are different questions, and only the second one is what a
// service reading the repo on github.com can see. rail has a
// Package.resolved on disk at Rail.xcodeproj/project.xcworkspace/xcshareddata/
// swiftpm/Package.resolved and `*.resolved` in its .gitignore, so the file is
// real locally and absent from the repository — and a Dependabot entry rendered
// from the working tree therefore pointed at a manifest Dependabot could never
// fetch, failing the job every day. Queueify is the same story via
// `*.xcworkspace`.
//
// Pathspecs are passed after `--`, so a pattern that looks like a flag is a
// pattern.
func Tracked(dir string, pathspecs ...string) ([]string, error) {
	// -z: paths are NUL-separated, so a filename containing a newline or a
	// quote-worthy byte comes through verbatim rather than as git's quoted form.
	args := append([]string{"ls-files", "-z", "--"}, pathspecs...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, filepath.ToSlash(f))
		}
	}
	return files, nil
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
