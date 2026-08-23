// Package rootcheck reports which lacquer checkout a command is reading its
// content from, and whether that checkout is behind its upstream.
//
// Why this exists: `sync` renders whatever is in LACQUER_ROOT, and a stale root
// fails SILENTLY. It prints "sync complete", writes the old content, and leaves
// a diff indistinguishable from "there was nothing to do" — the two states
// produce identical output. That is not hypothetical: a fix was merged to the
// lacquer, a project was synced from a root pulled before that merge, the sync
// reported success without touching the file the fix was in, and the same
// production job failed a second time with the identical error before anyone
// suspected the root rather than the fix.
//
// The fix is to make the source visible on every run and to say so out loud
// when it is behind. Both halves matter: the warning catches the case where a
// fetch is possible, and the always-printed provenance line catches the rest,
// because "synced from a1b2c3d on main" in a scrollback is enough to spot the
// problem later even when nothing warned at the time.
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
	// Root is the checkout's path, as given.
	Root string
	// Version is the contents of the checkout's VERSION file, if any.
	Version string
	// Branch and Commit identify HEAD. Branch is "HEAD" when detached.
	Branch string
	Commit string
	// Dirty reports uncommitted changes to tracked files.
	Dirty bool
	// Behind counts commits the upstream has that HEAD does not. It is -1 when
	// unknown: no upstream configured, not a git checkout, or the fetch failed.
	Behind int
	// NotGit is set when Root is not a git checkout at all — a tarball or a
	// copied directory, where staleness cannot be determined by any means.
	NotGit bool
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

	if b, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil {
		s.Version = strings.TrimSpace(string(b))
	}

	if out, err := git(root, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		s.NotGit = true
		return s
	}

	s.Branch, _ = trimmed(git(root, "rev-parse", "--abbrev-ref", "HEAD"))
	s.Commit, _ = trimmed(git(root, "rev-parse", "--short", "HEAD"))

	if out, err := git(root, "status", "--porcelain", "--untracked-files=no"); err == nil {
		s.Dirty = strings.TrimSpace(out) != ""
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
func (s State) Describe() string {
	var b strings.Builder
	b.WriteString("lacquer: ")
	if s.Version != "" {
		b.WriteString(s.Version)
		b.WriteString(" ")
	}
	if s.NotGit {
		fmt.Fprintf(&b, "from %s (not a git checkout)", s.Root)
		return b.String()
	}
	fmt.Fprintf(&b, "from %s @ %s", s.Branch, s.Commit)
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
