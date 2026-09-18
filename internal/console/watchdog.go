package console

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// MaxFailedLaunches is how many consecutive failed launch attempts a record
// may carry before Watch stops relaunching it. A launch that fails for a
// standing reason (not a git repository, tmux missing, a bad path) fails the
// same way every time, and each `watch --relaunch` pass -- by hand or from a
// cron sweep -- would otherwise try it again forever. At the cap it is
// reported and left for the operator: kill the record, or fix the cause and
// dispatch again.
const MaxFailedLaunches = 3

// WatchResult is one record's outcome from a Watch pass.
type WatchResult struct {
	Record Record
	Status Status
	Detail string
	// CheckErr means Check could not determine liveness at all (see
	// Record.Check) -- Status is Alive in that case, by the same
	// conservative default, but CheckErr is carried through so a report can
	// still surface "this one could not be verified" distinctly from a
	// confirmed-alive session.
	CheckErr    error
	Relaunched  bool
	RelaunchOut string
	RelaunchErr error
	// Held is why a Failed record was not relaunched (MaxFailedLaunches).
	Held string
	// Replacement is the record that took this one's place in the sessions
	// file after a real relaunch, and RecordErr any failure to write it.
	Replacement *Record
	RecordErr   error
}

// Watch checks every record in the sessions file and, if relaunch is true,
// re-dispatches every Failed one. It never acts on Alive or Missing records:
// Alive needs nothing, and Missing means there is no state to relaunch
// FROM (see Status.Missing) -- treated as an operator problem to look into,
// not a crash this papers over automatically.
//
// A real (non-dry-run) relaunch replaces the dead record in the sessions
// file with the relaunched session's own: its new daemon id, tmux session,
// worktree, or launch error. Without that, the dead record stayed Failed and
// every later pass relaunched it again -- for a bg session, another claude in
// the same worktree each time -- while the sessions actually started were
// recorded nowhere, invisible to watch and kill. A relaunch that starts
// nothing (a refusal, or a tmux session found already running) leaves the
// record as it is.
func Watch(path string, roster fleet.Roster, roles RoleRoster, sessions []Session, relaunch, dryRun bool) ([]WatchResult, error) {
	records, err := ReadRecords(path)
	if err != nil {
		return nil, err
	}
	out := make([]WatchResult, 0, len(records))
	for _, r := range records {
		status, detail, checkErr := r.Check()
		wr := WatchResult{Record: r, Status: status, Detail: detail, CheckErr: checkErr}
		if relaunch && status == Failed {
			if r.FailedLaunches >= MaxFailedLaunches {
				wr.Held = fmt.Sprintf("not relaunched: its last %d launch attempts all failed, so this is left for the operator -- kill it, or fix the cause and dispatch again", r.FailedLaunches)
			} else {
				launch, relErr := Relaunch(r, roster, roles, sessions, dryRun)
				wr.Relaunched = true
				wr.RelaunchOut = launch.Output
				wr.RelaunchErr = relErr
				if !dryRun && launch.Record != nil {
					next := *launch.Record
					// The relaunch ran with a handoff task built from this
					// record; the record keeps the original, or the next
					// relaunch would wrap one handoff inside another.
					next.Task = r.Task
					if next.LaunchError != "" {
						next.FailedLaunches = r.FailedLaunches + 1
					}
					if err := ReplaceRecord(path, r, next); err != nil {
						wr.RecordErr = err
					} else {
						wr.Replacement = &next
					}
				}
			}
		}
		out = append(out, wr)
	}
	return out, nil
}

// Relaunch re-dispatches r's session under the same name/mode/dir, with a
// task that says it died and hands it the cheapest available context to
// resume from -- its original task, plus `git log -1` and `git status
// --short` of the directory it was running in. That is everything this can
// get without reading the dead session's own transcript, which is the
// minimum viable handoff lacquer#207's design notes call for rather than
// bikeshedding a richer one before there is real experience to design from.
//
// It returns the whole Launch, whose Record is the relaunched session's, so
// the caller can put it in r's place (Watch does).
func Relaunch(r Record, roster fleet.Roster, roles RoleRoster, sessions []Session, dryRun bool) (Launch, error) {
	task := buildRelaunchTask(r)
	// A bg session resumes in the worktree it was recorded in, where its own
	// commits and uncommitted work are, while that is still a registered
	// worktree (resumeWorktree, worktree.go). A tmux session only ever has a
	// recorded worktree if its dispatcher assigned one (Placement.Worktree),
	// and returns to it the same way -- checked again, and refused if it is
	// no longer registered, rather than falling back to editing the checkout.
	var resume string
	var place Placement
	switch r.Mode {
	case Background:
		resume = r.Worktree
	case Tmux:
		place.Worktree = r.Worktree
	}
	switch r.Kind {
	case ProjectKind:
		return dispatchProject(roster, sessions, r.Name, task, r.Mode, dryRun, place, resume)
	case RoleKind:
		return dispatchRole(roles, sessions, r.Name, task, dryRun, place, resume)
	default:
		return Launch{}, fmt.Errorf("record %q has unknown kind %q", r.Name, r.Kind)
	}
}

func buildRelaunchTask(r Record) string {
	var b strings.Builder
	if r.LaunchError != "" {
		b.WriteString("A previous dispatch of this task failed to launch (")
		b.WriteString(r.LaunchError)
		b.WriteString("), so it may never have run. This is a new attempt; ")
		b.WriteString("check the state below before assuming any of it was done.\n\n")
	} else {
		b.WriteString("Your previous session on this task died and is being relaunched. ")
		b.WriteString("Pick up from wherever it left off rather than starting over from scratch.\n\n")
	}
	b.WriteString("Original task: ")
	b.WriteString(r.Task)
	// The worktree is where a bg session's work actually is; the checkout's
	// state says nothing about it.
	dir := r.Dir
	if r.Worktree != "" {
		if _, err := os.Stat(r.Worktree); err == nil {
			dir = r.Worktree
		}
	}
	if log := gitLogOne(dir); log != "" {
		b.WriteString("\n\nLast commit (git log -1): ")
		b.WriteString(log)
	}
	if status := gitStatusShort(dir); status != "" {
		b.WriteString("\n\nUncommitted changes (git status --short):\n")
		b.WriteString(status)
	} else {
		b.WriteString("\n\nWorking tree is clean (no uncommitted changes).")
	}
	return b.String()
}

func gitLogOne(dir string) string {
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%h %s").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitStatusShort(dir string) string {
	out, err := exec.Command("git", "-C", dir, "status", "--short").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// WatchText renders Watch results as a human-readable report.
func WatchText(w io.Writer, results []WatchResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "no sessions recorded")
		return
	}
	for _, r := range results {
		fmt.Fprintf(w, "%s (%s, %s): %s", r.Record.Name, r.Record.Kind, r.Record.Mode, r.Status)
		if r.Detail != "" {
			fmt.Fprintf(w, " — %s", r.Detail)
		}
		fmt.Fprintln(w)
		if r.CheckErr != nil {
			fmt.Fprintf(w, "  could not verify: %v\n", r.CheckErr)
		}
		if r.Held != "" {
			fmt.Fprintf(w, "  %s\n", r.Held)
		}
		if r.Relaunched {
			fmt.Fprintf(w, "  relaunching: %s", r.RelaunchOut)
			if r.RelaunchErr != nil {
				fmt.Fprintf(w, "  relaunch failed: %v\n", r.RelaunchErr)
			}
			if r.RecordErr != nil {
				fmt.Fprintf(w, "  could not record the relaunch -- the next pass will relaunch it again: %v\n", r.RecordErr)
			}
		}
	}
}
