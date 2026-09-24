// Package rootcheck verifies that a content root is a clean, detached release
// checkout and reports its path, tag and commit. Unknown state fails closed.
package rootcheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	lacquer "github.com/patrickserrano/lacquer"
)

// fetchTimeout bounds the upstream refresh. Staleness is a nicety and the sync
// is the job, so a slow or unreachable remote degrades to "unknown" rather than
// holding up the command.
const fetchTimeout = 10 * time.Second

// State is what a command knows about the checkout it is rendering from.
type State struct {
	// BuiltVersion is the VERSION compiled INTO this binary, as opposed to
	// Version, which is the VERSION read from the root at run time. They differ
	// whenever a binary outlives the tree it was built from — routine here, where
	// feature worktrees are built and then linger.
	BuiltVersion string
	// Root is the absolute, symlink-resolved checkout path.
	Root string
	// Version is the contents of the checkout's VERSION file, if any.
	Version string
	// Branch and Commit identify HEAD. Branch is "HEAD" when detached.
	Branch string
	Commit string
	// Tag is a tag pointing at HEAD, preferring one matching VERSION.
	// It is resolved only when Branch == "HEAD" (detached). Empty
	// when HEAD is on a branch, or when it is detached but does not sit exactly
	// on any tag — the "detached at an unknown commit" case issue #350 requires
	// to refuse as unverifiable rather than pass.
	Tag string
	// Dirty reports tracked changes and untracked, non-ignored files.
	Dirty bool
	// Behind counts commits the upstream has that HEAD does not. It is -1 when
	// unknown: no upstream configured, not a git checkout, or the fetch failed.
	Behind int
	// NotGit is set when Root is not a git checkout at all — a tarball or a
	// copied directory, where staleness cannot be determined by any means. Also
	// set when the `git` binary itself could not be run (not installed, not on
	// PATH): the underlying exec fails the same way a non-repo does, and both
	// are the same answer to "can this be verified" — no.
	NotGit bool
	// GitError records a failed path, HEAD, tag, or working-tree inspection.
	// Distinct from NotGit only for diagnostics; both mean the same
	// thing to Verify: unverifiable, which must refuse exactly like a confirmed
	// bad state (issue #350) rather than read as safety.
	GitError bool
}

// Inspect describes the checkout at root.
//
// It refreshes the upstream ref first unless fetch is false, because the
// comparison is otherwise only as current as the last manual `git fetch` — and
// the failure this package exists to catch had a stale remote-tracking ref too,
// so comparing against it would have reported "up to date" and been wrong.
func Inspect(root string, fetch bool) State {
	// BuiltVersion comes from the binary, never from the root — that separation
	// is the entire point of the check.
	s := State{Root: root, Behind: -1, BuiltVersion: strings.TrimSpace(lacquer.BuiltVersion)}
	resolved, err := filepath.Abs(root)
	if err == nil {
		resolved, err = filepath.EvalSymlinks(resolved)
	}
	if err != nil {
		s.GitError = true
		return s
	}
	root, s.Root = resolved, resolved

	if b, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil {
		s.Version = strings.TrimSpace(string(b))
	}

	if out, err := git(root, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		s.NotGit = true
		return s
	}

	branch, errB := trimmed(git(root, "rev-parse", "--abbrev-ref", "HEAD"))
	commit, errC := trimmed(git(root, "rev-parse", "--short", "HEAD"))
	if errB != nil || errC != nil {
		// git confirmed this IS a repo but then could not resolve HEAD (an
		// unborn branch, a corrupt ref). Unverifiable, not fine — see Verify's
		// doc comment on why this must refuse rather than silently pass.
		s.GitError = true
		return s
	}
	s.Branch, s.Commit = branch, commit
	// Git searches parent directories. A nested content copy must not borrow
	// its parent's tag and clean state as proof of its own provenance.
	top, err := trimmed(git(root, "rev-parse", "--show-toplevel"))
	if err == nil {
		top, err = filepath.EvalSymlinks(top)
	}
	if err != nil || top != root {
		s.NotGit = true
		return s
	}

	if out, err := git(root, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none"); err == nil {
		s.Dirty = strings.TrimSpace(out) != ""
	} else {
		s.GitError = true
		return s
	}

	if s.Branch == "HEAD" {
		// Multiple tags can point at a release. Select the one matching VERSION,
		// rather than whichever tag git describe happens to prefer.
		tags, err := trimmed(git(root, "tag", "--points-at", "HEAD"))
		if err != nil {
			s.GitError = true
			return s
		}
		for _, tag := range strings.Fields(tags) {
			if s.Tag == "" || tagMatchesVersion(tag, s.Version) {
				s.Tag = tag
			}
			if tagMatchesVersion(tag, s.Version) {
				break
			}
		}
	}

	upstream, err := trimmed(git(root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"))
	if err != nil || upstream == "" {
		return s // no upstream: nothing to be behind of
	}

	if fetch {
		remote := upstream
		if i := strings.Index(upstream, "/"); i > 0 {
			remote = upstream[:i]
		}
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		// Errors are deliberately ignored: offline is a normal state, and the
		// count below simply stays as stale as the local ref in that case.
		cmd := exec.CommandContext(ctx, "git", "-C", root, "fetch", "--quiet", remote)
		cmd.Stdout, cmd.Stderr = nil, nil
		_ = cmd.Run()
	}

	if out, err := trimmed(git(root, "rev-list", "--count", "HEAD..@{u}")); err == nil {
		if n, convErr := strconv.Atoi(out); convErr == nil {
			s.Behind = n
		}
	}
	return s
}

// Describe is the one-line provenance note, printed on every run.
//
// The root PATH is always included — issue #350's field incident had a session
// read "from HEAD @ 2775df3" as naming provenance when it named nothing: the
// one variable that actually changed between a safe run and a dangerous one is
// which directory LACQUER_ROOT pointed at, and that used to be the one thing
// this line omitted.
func (s State) Describe() string {
	var b strings.Builder
	b.WriteString("lacquer: ")
	if s.Version != "" {
		b.WriteString(s.Version)
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "from %s", s.Root)
	if s.NotGit {
		b.WriteString(" (not a git checkout)")
		return b.String()
	}
	if s.GitError {
		b.WriteString(" (could not read git state)")
		return b.String()
	}
	ref := s.Branch
	if s.Branch == "HEAD" && s.Tag != "" {
		// A detached, correctly pinned checkout renders as the literal string
		// "HEAD" if left alone — which carries no information and is exactly the
		// SAFE configuration a reader most needs to identify at a glance. The tag
		// is the useful thing to print here instead (issue #350): the pixelfox
		// session read "from HEAD @ sha" as naming nothing and read a working
		// checkout's branch name as though it were more informative.
		ref = s.Tag
	}
	fmt.Fprintf(&b, " @ %s @ %s", ref, s.Commit)
	if s.Dirty {
		b.WriteString(" (dirty)")
	}
	// The banner's whole job is provenance, and without this it reports the
	// content's version as though it were the binary's. That is how a 1.3.0
	// binary announced "lacquer: 1.5.4" while rendering a project's lefthook.yml
	// with pre-merge logic and dropping a profile's hooks.
	if s.StaleBinary() {
		fmt.Fprintf(&b, "  ** STALE BINARY: built from %s, reading %s **", s.BuiltVersion, s.Version)
	}
	return b.String()
}

// Verify reports why root is NOT provably a pinned release, or nil when it is:
// detached HEAD, sitting exactly on a tag whose name (a leading "v" stripped)
// equals VERSION, with a clean tree.
//
// Every other state — including one that cannot be determined at all — returns
// a non-nil error. That symmetry is the point (issue #350): "I could not
// verify this root" and "this root is fine" must never share an exit code. A
// guard that only refuses a DETECTED branch reproduces the original bug one
// level up, because the absence of detection would then read as safety — the
// same defect family as a skipped CI check satisfying a required one.
func (s State) Verify() error {
	switch {
	case s.NotGit:
		return fmt.Errorf("%s is not a git checkout, or git is unavailable — its pinned state cannot be verified", s.Root)
	case s.GitError:
		return fmt.Errorf("%s: git could not read HEAD or working-tree state — its pinned state cannot be verified", s.Root)
	case s.Commit == "" || s.Version == "":
		return fmt.Errorf("%s: missing commit or VERSION — its pinned state cannot be verified", s.Root)
	case s.Dirty:
		return fmt.Errorf("%s has uncommitted changes — a dirty tree is never a pinned release", s.Root)
	case s.Branch != "HEAD":
		return fmt.Errorf("%s is on branch %q, not detached at a release tag — a branch moves out from under you", s.Root, s.Branch)
	case s.Tag == "":
		return fmt.Errorf("%s is detached at %s, which is not exactly any tag — its pinned state cannot be verified", s.Root, s.Commit)
	case !tagMatchesVersion(s.Tag, s.Version):
		return fmt.Errorf("%s is at tag %s but VERSION reads %q — a pinned release requires these to match", s.Root, s.Tag, s.Version)
	}
	return nil
}

// tagMatchesVersion compares a resolved tag (e.g. "v1.36.8") against VERSION's
// contents (e.g. "1.36.8"), tolerating the tag's conventional "v" prefix.
func tagMatchesVersion(tag, version string) bool {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v") == strings.TrimSpace(version)
}

// StaleBinary reports whether this binary was compiled from a different source
// version than the content it is reading.
//
// An empty BuiltVersion is an UNKNOWN, not a mismatch: a binary predating this
// check embeds nothing, and reporting it stale on no evidence would cry wolf on
// every old install. An empty Version means the root has no VERSION at all,
// which other checks already cover.
func (s State) StaleBinary() bool {
	if s.BuiltVersion == "" || s.Version == "" {
		return false
	}
	return strings.TrimSpace(s.BuiltVersion) != strings.TrimSpace(s.Version)
}

// Warning is the stale-root message, or "" when there is nothing to say.
func (s State) Warning() string {
	if s.Behind <= 0 {
		return ""
	}
	commits := "commits"
	if s.Behind == 1 {
		commits = "commit"
	}
	return fmt.Sprintf(
		"warning: %s is %d %s behind its upstream — this rendered OLD content.\n"+
			"         A stale root does not fail; it reports success and writes the previous\n"+
			"         version, which looks exactly like having nothing to do.\n"+
			"         Run `git -C %s pull` and sync again.",
		s.Root, s.Behind, commits, s.Root)
}

func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return string(out), err
}

func trimmed(out string, err error) (string, error) {
	return strings.TrimSpace(out), err
}
