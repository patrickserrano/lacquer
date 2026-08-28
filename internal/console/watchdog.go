package console

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

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
}

// Watch checks every record in the sessions file and, if relaunch is true,
// re-dispatches every Failed one. It never acts on Alive or Missing records:
// Alive needs nothing, and Missing means there is no state to relaunch
// FROM (see Status.Missing) -- treated as an operator problem to look into,
// not a crash this papers over automatically.
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
			relOut, relErr := Relaunch(r, roster, roles, sessions, dryRun)
			wr.Relaunched = true
			wr.RelaunchOut = relOut
			wr.RelaunchErr = relErr
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
func Relaunch(r Record, roster fleet.Roster, roles RoleRoster, sessions []Session, dryRun bool) (string, error) {
	task := buildRelaunchTask(r)
	switch r.Kind {
	case ProjectKind:
		return Dispatch(roster, sessions, r.Name, task, r.Mode, dryRun)
	case RoleKind:
		return DispatchRole(roles, sessions, r.Name, task, dryRun)
	default:
		return "", fmt.Errorf("record %q has unknown kind %q", r.Name, r.Kind)
	}
}

func buildRelaunchTask(r Record) string {
	var b strings.Builder
	b.WriteString("Your previous session on this task died and is being relaunched. ")
	b.WriteString("Pick up from wherever it left off rather than starting over from scratch.\n\n")
	b.WriteString("Original task: ")
	b.WriteString(r.Task)
	if log := gitLogOne(r.Dir); log != "" {
		b.WriteString("\n\nLast commit (git log -1): ")
		b.WriteString(log)
	}
	if status := gitStatusShort(r.Dir); status != "" {
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
		if r.Relaunched {
			fmt.Fprintf(w, "  relaunching: %s", r.RelaunchOut)
			if r.RelaunchErr != nil {
				fmt.Fprintf(w, "  relaunch failed: %v\n", r.RelaunchErr)
			}
		}
	}
}
