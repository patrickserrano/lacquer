package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// Mode is where dispatched work runs.
type Mode string

const (
	// Background starts a background agent (`claude --bg`) in a git worktree
	// and branch that dispatch makes for it under <repo>/.claude/worktrees/,
	// so it does not edit the checkout it was dispatched against. If the
	// worktree cannot be made, nothing is launched. Right for speculative or
	// parallel work.
	Background Mode = "bg"
	// Tmux starts claude in a new, detached tmux session named for the
	// project or role, running in the real checkout -- this edits it
	// directly. An already-running session of that name is left alone.
	// Right for focused work you intend to steer: attach to watch it.
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
// The mode is not cosmetic. A background agent runs in a git worktree made
// for it and does not touch the main checkout; a tmux session edits it
// directly. Choosing wrong silently changes where the work lands, which is
// why there is no default.
func Dispatch(roster fleet.Roster, sessions []Session, name, task string, mode Mode, dryRun bool) (Launch, error) {
	return dispatchProject(roster, sessions, name, task, mode, dryRun, "")
}

// dispatchProject is Dispatch, plus the recorded worktree a bg relaunch
// resumes in (Relaunch, watchdog.go).
func dispatchProject(roster fleet.Roster, sessions []Session, name, task string, mode Mode, dryRun bool, resume string) (Launch, error) {
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
		return Launch{}, fmt.Errorf("no project named %q in the roster (known: %s)", name, strings.Join(names(roster), ", "))
	}
	if task = strings.TrimSpace(task); task == "" {
		return Launch{}, fmt.Errorf("dispatch needs a task")
	}

	// Refuse, do not warn. An archived repo is read-only at the API level --
	// no push, no PR -- so a session dispatched against one does not fail
	// loudly, it just sits there doing nothing a human would ever see, while
	// `console watch` keeps reporting it "alive" because the daemon process
	// itself is fine. That silence is the bug (lacquer#227): a wrong project
	// name gets refused above; a right project name pointed at a dead repo
	// must be refused just as loudly, not discovered later by an operator
	// wondering why nothing happened.
	if mode == Tmux {
		if err := tmuxCollision(entry.Name, names(roster)); err != nil {
			return Launch{}, err
		}
	}

	if yes, ok := archived(entry.Repo); ok && yes {
		return Launch{}, fmt.Errorf("refusing to dispatch %s: %s is archived on GitHub -- archived repos are read-only (no push, no pull request), so a dispatched session would run with nothing it can actually do; unarchive it on GitHub first if this was not intentional", entry.Name, entry.Repo)
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

	return runDispatch(launchSpec{verb: "dispatch", kind: ProjectKind, name: entry.Name, dir: entry.Path, task: task, mode: mode, warning: warning, dryRun: dryRun, resume: resume})
}

// Launch is what one Dispatch or DispatchRole call did.
type Launch struct {
	// Output is what to show whoever ran the dispatch.
	Output string
	// Record describes the session this call started, for a caller that
	// keeps a sessions file to append (AppendRecord). Nil when there is
	// nothing to record.
	Record *Record
}

// launchSpec is everything runDispatch needs, resolved by its caller.
type launchSpec struct {
	verb    string // labels the display line: "dispatch" or "dispatch role"
	kind    Kind
	name    string
	dir     string
	task    string
	mode    Mode
	warning string
	dryRun  bool
	// resume is a bg relaunch's recorded worktree, to run in again if it is
	// still a registered worktree. Empty for a first dispatch.
	resume string
}

// record builds the Record for a launch attempt from sp. Dir is made
// absolute: a relaunch may run from a different working directory than the
// original dispatch (a later invocation, a cron sweep), and a relative Dir
// recorded against today's CWD would silently point somewhere else by then.
func (sp launchSpec) record() *Record {
	dir := sp.dir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &Record{
		Kind:      sp.kind,
		Name:      sp.name,
		Mode:      sp.mode,
		Dir:       dir,
		Task:      sp.task,
		StartedAt: time.Now().UTC(),
	}
}

// bypassFlags are passed to every dispatched claude, in both modes. The
// operator's standing decision is that every fleet session runs with bypass
// permissions: a dispatched session has nobody to click "allow" on a
// tool-call prompt. Without these a bg session stalls on its first git
// push/build/edit, which is indistinguishable from success until you check
// ~/.claude/jobs/<id>/state.json (state: "blocked") instead of the peer
// session list (which just says "idle"); and a tmux session comes up in
// auto mode asking the operator to approve its commands, with cross-session
// messages to it held for approval too.
//
// --dangerously-skip-permissions bypasses the permission-PROMPT layer only
// -- it is a different mechanism from the sandbox, an execution-level
// restriction that runs alongside it. Without also disabling the sandbox,
// every Bash command stays sandboxed with no one to grant the extra access
// it needs: git add/commit/push, checkout -b, worktree remove/unlock, and
// lacquer sync itself (which needs to read LACQUER_ROOT outside the
// worktree) all get silently denied, with only a narrow read-only set (git
// status/log/diff, cat/ls, echo, which) actually working.
var bypassFlags = []string{"--dangerously-skip-permissions", "--settings", `{"sandbox":{"enabled":false}}`}

// runDispatch starts one session, or with dryRun shows what it would start.
// Shared by Dispatch (a named project) and DispatchRole (a named role,
// role.go), which differ only in how name/dir/task/warning get resolved.
//
// What it returns for recording (Launch.Record) is deliberate:
//   - a session it started: recorded.
//   - a launch it attempted that failed (the worktree could not be made, or
//     tmux or claude failed to start): recorded, with LaunchError set, and
//     the error returned too. Check reports such a record Failed, so `watch`
//     shows it and `watch --relaunch` retries it. Before this, a failed
//     launch left no trace at all: the dispatching agent got an error nobody
//     else saw, and watch had nothing to look at.
//   - a tmux session already running under that name: nothing started, so
//     nothing recorded. A second claude is never started inside it.
//   - a dry run, or a refusal before any launch was attempted (unknown name,
//     empty task, unknown mode, archived repo, tmux name collision): nothing
//     recorded. There is no session to watch, and relaunching an input error
//     could never succeed.
func runDispatch(sp launchSpec) (Launch, error) {
	switch sp.mode {
	case Background:
		return runBackground(sp)
	case Tmux:
		return runTmux(sp)
	default:
		return Launch{}, fmt.Errorf("unknown mode %q (want %q or %q)", sp.mode, Background, Tmux)
	}
}

// launchFailed is the Launch for an attempt that failed: its output so far,
// and a record carrying the failure.
func launchFailed(sp launchSpec, output string, rec *Record, err error) (Launch, error) {
	if rec == nil {
		rec = sp.record()
	}
	rec.LaunchError = err.Error()
	rec.FailedLaunches = 1 // Watch adds the attempts before it (relaunched)
	return Launch{Output: output, Record: rec}, fmt.Errorf("%s failed: %w", sp.verb, err)
}

// runBackground starts `claude --bg` inside a git worktree made for this
// session (worktree.go), never in the checkout itself.
//
// The working directory is set via exec.Cmd.Dir, not a CLI flag. `claude`
// has no `--cwd` option; an earlier version passed `--cwd <dir>` anyway,
// which made every background dispatch fail immediately ("unknown option
// '--cwd'") while still printing an optimistic "backgrounded · <id>" line,
// because that message is emitted before the daemon's own init check runs.
func runBackground(sp launchSpec) (Launch, error) {
	argv := append(append([]string{"claude", "--bg"}, bypassFlags...), sp.task)
	claudeLine := strings.Join(argv, " ")
	prefix := sp.warning + sp.verb + ": "

	if sp.dryRun {
		root, rel, path, branch, base, note, err := planWorktree(sp.dir, false)
		if err != nil {
			return Launch{Output: prefix + "(no worktree possible)\n"}, err
		}
		return Launch{Output: prefix + strings.Join(worktreeAddArgs(root, path, branch, base), " ") + "\n  (" + note + ")\n" +
			prefix + "(cd " + filepath.Join(path, rel) + " && " + claudeLine + ")\n" +
			"(dry run — nothing started)\n"}, nil
	}

	var wt dispatchWorktree
	var err error
	if sp.resume != "" {
		wt, err = resumeWorktree(sp.dir, sp.resume)
	} else {
		wt, err = createWorktree(sp.dir)
	}
	if err != nil {
		return launchFailed(sp, prefix+"no worktree, so nothing was launched\n", nil, err)
	}
	rec := sp.record()
	rec.Worktree = wt.path
	rec.Branch = wt.branch
	line := prefix + wt.setup + prefix + "(cd " + wt.runDir + " && " + claudeLine + ")\n"

	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 -- argv is built from the roster/roles file and an operator-supplied task, never a shell string
	cmd.Dir = wt.runDir
	cmd.Stderr = os.Stderr
	// Buffered, not streamed: the daemon detaches on its own, so this returns
	// almost immediately, and `claude --bg`'s own "backgrounded · <id>" line
	// is the ONLY place the daemon id appears -- lacquer#207's watchdog needs
	// it later to find the job's state file (~/.claude/jobs/<id>/state.json).
	var captured bytes.Buffer
	cmd.Stdout = &captured
	runErr := cmd.Run()
	line += captured.String()
	rec.DaemonID = DaemonID(captured.String())
	if runErr != nil {
		// The worktree stays: it may be all a relaunch has to resume from, and
		// lacquer never removes a worktree (see worktree.go).
		return launchFailed(sp, line, rec, runErr)
	}
	return Launch{Output: line, Record: rec}, nil
}

// runTmux starts claude in a new, detached tmux session running in the
// checkout itself.
//
// Detached (-d), always. Without it, `tmux new-session` attaches the
// caller's terminal, and an agent's shell has none: every dispatch an agent
// ran failed with "open terminal failed: not a terminal". Not -A either:
// with -A an existing session is attached instead, which fails the same way
// even alongside -d (verified on tmux 3.7c). An existing session is looked
// up explicitly instead, and left alone.
func runTmux(sp launchSpec) (Launch, error) {
	session := tmuxSessionName(sp.name)
	argv := append(append([]string{"tmux", "new-session", "-d", "-s", session, "-c", sp.dir, "claude"}, bypassFlags...), sp.task)
	line := sp.warning + sp.verb + ": " + strings.Join(argv, " ") + "\n"
	if sp.dryRun {
		return Launch{Output: line + "(dry run — nothing started)\n"}, nil
	}

	// The raw name too: a session an older lacquer started under a dotted
	// name on tmux 3.7c is this same project's, and must not get a twin.
	_, running, found, err := findTmuxSession(Record{Name: sp.name}.tmuxNames())
	if err != nil {
		return launchFailed(sp, line, nil, err)
	}
	if found {
		return Launch{Output: sp.warning + sp.verb + ": tmux session " + running + " is already running; not starting a second claude in it (attach: tmux attach -t '" + running + "')\n"}, nil
	}

	rec := sp.record()
	rec.TmuxSession = session
	// tmux runs its command directly (no shell) when given several
	// arguments, so the task and the settings JSON arrive verbatim.
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput() // #nosec G204 -- argv is built from the roster/roles file and an operator-supplied task, never a shell string
	line += string(out)
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			err = fmt.Errorf("%s (%w)", msg, err)
		}
		return launchFailed(sp, line, rec, err)
	}
	return Launch{Output: line + "attach: tmux attach -t '" + session + "'\n", Record: rec}, nil
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

// ArchivedRunner executes `gh repo view <repo> --json isArchived` and returns
// its combined output. Injectable for tests -- the real implementation shells
// to the `gh` binary, which is not installed or authenticated in every
// environment this package runs in, and every real call is a network round
// trip this package would otherwise pay on every dispatch.
var ArchivedRunner = func(repo string) ([]byte, error) {
	return exec.Command("gh", "repo", "view", repo, "--json", "isArchived").Output()
}

// archived reports whether repo is archived on GitHub.
//
// ok is false whenever the answer could not be determined -- no repo
// configured for this roster entry, gh missing, not authenticated, network
// down, or unparsable output -- and callers MUST NOT read a false ok as
// "confirmed not archived". Dispatch only ever refuses on a CONFIRMED
// archived repo (ok && yes); an inconclusive check degrades to "proceed", the
// same graceful-degradation this fleet already applies to `gh pr list` in
// console.go, rather than blocking every dispatch the moment gh itself is
// merely unavailable.
func archived(repo string) (yes bool, ok bool) {
	if repo == "" {
		return false, false
	}
	out, err := ArchivedRunner(repo)
	if err != nil {
		return false, false
	}
	var v struct {
		IsArchived bool `json:"isArchived"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return false, false
	}
	return v.IsArchived, true
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
