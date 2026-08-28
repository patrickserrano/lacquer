package console

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// Mode is where dispatched work runs.
type Mode string

const (
	// Background starts a background agent. Claude Code isolates these in their
	// own git worktree, so they CANNOT edit the main working directory.
	// Right for speculative or parallel work.
	Background Mode = "bg"
	// Tmux starts an interactive session in a tmux session for the project,
	// creating it if absent. This edits the real checkout.
	// Right for focused work you intend to steer.
	Tmux Mode = "tmux"
)

// Dispatch starts work on one project.
//
// Always explicit: the console prints a table and never decides on its own to
// start anything. This is the only function here that causes an effect, it is
// reached only by an operator naming a project and a task, and it starts a
// session rather than making a change — every edit that follows still happens
// where a human can see it.
//
// The mode is not cosmetic. A background agent is worktree-isolated and cannot
// touch the main checkout; a tmux session edits it directly. Choosing wrong
// silently changes where the work lands, which is why there is no default.
func Dispatch(roster fleet.Roster, sessions []Session, name, task string, mode Mode, dryRun bool) (string, error) {
	var entry *fleet.Entry
	for i := range roster.Project {
		if roster.Project[i].Name == name {
			entry = &roster.Project[i]
			break
		}
	}
	if entry == nil {
		// Named rather than fuzzy-matched. Dispatching to the wrong project
		// because a name was close enough is worse than being told to retype it.
		return "", fmt.Errorf("no project named %q in the roster (known: %s)", name, strings.Join(names(roster), ", "))
	}
	if task = strings.TrimSpace(task); task == "" {
		return "", fmt.Errorf("dispatch needs a task")
	}

	// Warn, do not refuse. A second agent in the same project is sometimes
	// exactly right; a second agent nobody knows about is not.
	var warning string
	var live []string
	for _, s := range sessions {
		if under(s.CWD, entry.Path) {
			live = append(live, fmt.Sprintf("%s (%s)", s.Name, s.Status))
		}
	}
	if len(live) > 0 {
		warning = fmt.Sprintf("note: %s already has %d session(s): %s\n",
			entry.Name, len(live), strings.Join(live, ", "))
	}

	return runDispatch("dispatch", entry.Name, entry.Path, task, mode, warning, dryRun)
}

// runDispatch builds the argv for one session, shows it, and (unless dryRun)
// runs it. Shared by Dispatch (a named project) and DispatchRole (a named
// role, role.go) — the two differ only in how name/dir/task/warning get
// resolved (a roster lookup tied to worktree semantics vs a roles-file lookup
// with none), not in how a session actually gets started.
//
// verb labels the display line ("dispatch" vs "dispatch role") so dry-run
// output and logs read naturally for whichever caller this is.
func runDispatch(verb, name, dir, task string, mode Mode, warning string, dryRun bool) (string, error) {
	// cmdDir is where the subprocess actually runs, set via exec.Cmd.Dir — not
	// a CLI flag. `claude` has no `--cwd` option; an earlier version of this
	// function passed `--cwd <dir>` anyway, which made every background
	// dispatch fail immediately ("unknown option '--cwd'") while still
	// printing an optimistic "backgrounded · <id>" line, because that message
	// is emitted before the daemon's own init check runs. The failure was only
	// visible in the job's own state file (~/.claude/jobs/<id>/state.json),
	// never in this command's own stdout/exit code.
	var argv []string
	var cmdDir string
	switch mode {
	case Background:
		argv = []string{"claude", "--bg", task}
		cmdDir = dir
	case Tmux:
		argv = []string{"tmux", "new-session", "-A", "-s", name, "-c", dir,
			"claude", task}
	default:
		return "", fmt.Errorf("unknown mode %q (want %q or %q)", mode, Background, Tmux)
	}

	display := strings.Join(argv, " ")
	if cmdDir != "" {
		display = "(cd " + cmdDir + " && " + display + ")"
	}
	line := warning + verb + ": " + display + "\n"
	if dryRun {
		return line + "(dry run — nothing started)\n", nil
	}

	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is built from the roster/roles file and an operator-supplied task, never a shell string
	cmd.Dir = cmdDir
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr

	if mode == Tmux {
		// `tmux new-session -A` attaches the operator INTO the session
		// interactively -- stdout must stream live and unbuffered, or the
		// operator sees nothing until they detach (if ever). Nothing here
		// needs capturing: the session NAME (already known) is its own
		// addressable handle, unlike Background mode below.
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			return line, fmt.Errorf("%s failed: %w", verb, err)
		}
		return line, nil
	}

	// Background mode returns almost immediately (the daemon detaches on its
	// own), so buffering the whole thing before printing costs nothing and
	// avoids a subtler problem: `claude --bg`'s own "backgrounded · <id>"
	// line is the ONLY place the daemon id appears, and lacquer#207's
	// watchdog needs that id later to find the job's own state file
	// (~/.claude/jobs/<id>/state.json) and check whether it is still alive.
	// Capturing (rather than tee-ing to a live os.Stdout AND appending a
	// separately-formatted note) means the single returned line is the one
	// and only place this output appears -- no risk of printing it twice.
	var captured bytes.Buffer
	cmd.Stdout = &captured
	runErr := cmd.Run()
	line += captured.String()
	if runErr != nil {
		return line, fmt.Errorf("%s failed: %w", verb, runErr)
	}
	return line, nil
}

// daemonLineRe matches `claude --bg`'s own confirmation line, e.g.
// "backgrounded · 81ba5f89". This is printed before the daemon's own init
// check runs (see the comment above), so its presence is not proof the
// session is actually alive -- only that a launch was attempted and an id
// was assigned. Confirming it is genuinely running is checkBackground's job
// (record.go), which reads the id's own state file.
var daemonLineRe = regexp.MustCompile(`backgrounded · ([0-9a-fA-F]+)`)

// DaemonID extracts a `claude --bg` daemon id from captured command output,
// or "" if none is present (dry runs, Tmux mode, or a launch that failed
// before printing one).
func DaemonID(output string) string {
	m := daemonLineRe.FindStringSubmatch(output)
	if m == nil {
		return ""
	}
	return m[1]
}

func names(r fleet.Roster) []string {
	out := make([]string, 0, len(r.Project))
	for _, e := range r.Project {
		out = append(out, e.Name)
	}
	return out
}

// Sessions exposes the live session list so a caller can pass it to Dispatch
// without a second process launch.
func Sessions() []Session {
	s, err := listSessions()
	if err != nil {
		return nil
	}
	return s
}
