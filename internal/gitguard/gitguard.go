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

// DirtyPaths reports every path in projectRoot that has uncommitted
// modifications or is untracked, as a set of forward-slash paths relative to
// projectRoot itself — NOT to the repository root, which is what git reports and
// what the prefix handling below corrects for. A committed-and-unmodified file
// is absent from the set, and
// so is a path that no longer exists on disk — a tracked file DELETED from the
// worktree is not "work that would be clobbered", because there is nothing there
// to clobber. Errors from git are returned, so a directory that is not a
// repository fails closed rather than reporting a comfortable empty set.
//
// This answers the whole-worktree question in ONE subprocess. It replaced a
// per-path `git status --porcelain -- <path>`, which sync's asset preflight ran
// once per asset: a three-profile project plans ~1500 assets, so a second sync —
// the run where every asset is already on disk, and so nothing short-circuits —
// spawned ~1500 git processes to answer 1500 copies of the same question.
//
// The flags are load-bearing, and each one is a silent-wrongness guard:
//
//   - -z emits NUL-separated paths, which turns OFF the C-style quoting plain
//     --porcelain applies to any path with a space, a quote or a non-ASCII byte.
//     Quoted output would not compare equal to an asset's Dest, and the guard
//     would stop protecting exactly the files whose names are hardest to notice.
//   - --untracked-files=all lists untracked files individually. The default
//     collapses a wholly-untracked directory to a single "dir/" entry, so an
//     untracked asset nested in a new directory would never match by exact path
//     and would be silently unguarded — the failure mode being a sync that
//     overwrites new work while reporting success.
//   - `-- .` scopes the query to projectRoot's own subtree, so a component
//     inside a large monorepo does not pay to have its siblings scanned. This
//     one is about cost, not correctness: the prefix check below is what
//     actually keeps another component's work-in-progress out of the answer,
//     and it would still do so if this pathspec were dropped.
func DirtyPaths(projectRoot string) (map[string]bool, error) {
	// Porcelain paths are relative to the REPOSITORY root, not to the working
	// directory — including when git is run from a subdirectory. projectRoot is
	// not always the repository root (InWorkTree happily accepts a component
	// directory inside a monorepo), so the prefix has to be stripped or every
	// lookup would miss and the guard would silently protect nothing. The old
	// per-path form never had to care: it passed a pathspec and only tested
	// whether the output was empty, so the two path roots never had to agree.
	prefix, err := repoPrefix(projectRoot)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command("git", "status", "--porcelain", "-z", "--untracked-files=all", "--", ".")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	dirty := make(map[string]bool)
	// Entries are "XY <path>\0", except a rename/copy, which is
	// "XY <newpath>\0<origpath>\0" — the origin path is a bare field with no
	// status prefix. Consuming it explicitly is what keeps the parser from
	// reading it as the next entry and mis-slicing every path after it.
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			// Trailing empty field after the final NUL, or a truncated line.
			continue
		}
		x, y := entry[0], entry[1]
		path := entry[3:]
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			// Skip the origin path field. It is the pre-rename name, which by
			// definition no longer exists in the worktree, so the existence
			// check below would drop it anyway; consuming it here is about
			// framing, not filtering.
			i++
		}
		path = filepath.ToSlash(path)
		// Re-root onto projectRoot. The `-- .` pathspec should make every
		// reported path fall under the prefix; anything that does not is outside
		// the caller's tree and cannot correspond to one of its assets.
		if prefix != "" {
			if !strings.HasPrefix(path, prefix) {
				continue
			}
			path = path[len(prefix):]
		}
		if path == "" {
			continue
		}
		// Preserve the pre-batch rule exactly: a path git considers changed but
		// that is not on disk (a deletion) is NOT dirty. Reporting it would make
		// sync refuse where it used to proceed.
		if _, err := os.Lstat(filepath.Join(projectRoot, filepath.FromSlash(path))); err != nil {
			continue
		}
		dirty[path] = true
	}
	return dirty, nil
}

// repoPrefix returns dir's path relative to the root of its repository, as a
// forward-slash path with a trailing slash, or "" when dir IS the repository
// root. A non-repository is an error, so DirtyPaths fails closed.
func repoPrefix(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-prefix")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
