package console

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Kind is what a Record's session actually is. Needed at relaunch time to
// know whether to re-dispatch via Dispatch (a project, drawing from a
// fleet.Roster) or DispatchRole (a role, drawing from a RoleRoster) -- the
// two have no shared lookup, so the record has to say which one it was.
type Kind string

const (
	ProjectKind Kind = "project"
	RoleKind    Kind = "role"
)

// Record is what a successful (non-dry-run) Dispatch or DispatchRole call
// appends, so a later pass (Check, Relaunch) can find the session again
// without re-deriving anything from the roster/roles file, which may have
// changed since dispatch time.
//
// Appending is the caller's job (the CLI layer in cmd/lacquer), not
// Dispatch/DispatchRole's -- those two stay pure "start a session" functions
// usable without ever touching a sessions file, and every existing caller
// keeps working unchanged. Recording is opt-in per invocation (pass
// --sessions), not a hidden side effect of dispatching.
type Record struct {
	Kind Kind `json:"kind"`
	// Name is the tmux session name (Tmux mode) or the project/role's
	// display name (Background mode, where there is no tmux session and
	// DaemonID is the addressable handle instead).
	Name string `json:"name"`
	Mode Mode   `json:"mode"`
	// Dir is the working directory the session was started in -- a
	// project's checkout, or a role's own directory. Used both to relaunch
	// in the same place and to cheaply inspect what state it left behind
	// (git log/status) when building a relaunch task.
	Dir  string `json:"dir"`
	Task string `json:"task"`
	// DaemonID is the bg-mode session id captured from "backgrounded · <id>"
	// in the dispatch command's own stdout (DaemonID in dispatch.go). Empty
	// for Tmux mode, where Name is itself the addressable handle via
	// `tmux has-session`.
	DaemonID  string    `json:"daemonId,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

// AppendRecord adds one line to the sessions file, creating it (and its
// parent directory) if absent.
//
// One JSON object per line (JSONL), append-only, no locking: this file is
// written by one process at a time -- an operator running dispatch by hand,
// or a cron sweep -- never concurrently. A truncated last line from an
// interrupted write is detectable and skippable (ReadRecords does exactly
// that) rather than corrupting everything before it, which is the property
// that makes JSONL the right call here over a single JSON array.
func AppendRecord(path string, r Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create sessions file directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open sessions file: %w", err)
	}
	defer f.Close()
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode session record: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write session record: %w", err)
	}
	return nil
}

// ReadRecords loads every record from the sessions file. A file that does
// not exist yet is not an error and returns no records -- a fleet with
// nothing dispatched yet is a normal, empty state, not a broken one.
//
// A line that fails to parse is skipped rather than failing the whole read:
// an interrupted AppendRecord (process killed mid-write) leaves a truncated
// last line, and losing visibility into every prior dispatch because of that
// one line would be a worse failure than ignoring it.
func ReadRecords(path string) ([]Record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open sessions file: %w", err)
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // truncated/corrupt line -- skip, do not fail the read
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read sessions file: %w", err)
	}
	return out, nil
}

// Status is the result of checking whether a dispatched session is still
// running.
type Status string

const (
	// Alive means the session is present, or its state is not one this has
	// confirmed means dead. An unrecognized state is deliberately treated as
	// Alive, not Failed: wrongly relaunching a healthy session duplicates
	// real work, while wrongly leaving a genuinely dead one alone just means
	// the next watch pass catches it. That asymmetry is why the conservative
	// read wins here.
	Alive Status = "alive"
	// Failed means the session is confirmed gone: a dead tmux session, or a
	// bg-mode job whose own state file reports "failed".
	Failed Status = "failed"
	// Missing means there is nothing to check at all -- no tmux session by
	// this name, or no job state file for this DaemonID (never captured, or
	// since garbage-collected). Deliberately distinct from Failed: a Failed
	// job's own state file still exists and usually says why, so Relaunch
	// has real context to work with; Missing has none, and Watch does not
	// relaunch a Missing record automatically for that reason -- it is
	// reported for an operator to look at instead.
	Missing Status = "missing"
)

// Check reports whether r's session is still running, and a short detail
// string (a job's own "detail" field for Background mode, or empty for
// Tmux). An error means the check itself could not run (e.g. tmux is not
// installed) -- not that the session was found dead -- so the returned
// Status in that case is Alive: something this cannot verify must not be
// silently reported as a confirmed failure.
func (r Record) Check() (Status, string, error) {
	switch r.Mode {
	case Tmux:
		return r.checkTmux()
	case Background:
		return r.checkBackground()
	default:
		return Alive, "", fmt.Errorf("record %q has unknown mode %q", r.Name, r.Mode)
	}
}

func (r Record) checkTmux() (Status, string, error) {
	err := exec.Command("tmux", "has-session", "-t", r.Name).Run()
	if err == nil {
		return Alive, "", nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// tmux ran fine and reported no such session -- a confident answer.
		return Missing, "no tmux session named " + r.Name, nil
	}
	// tmux itself could not be run (not installed, PATH issue, ...): this
	// cannot tell alive from dead, so it must not claim either.
	return Alive, "", fmt.Errorf("check tmux session %s: %w", r.Name, err)
}

// jobState is the subset of ~/.claude/jobs/<id>/state.json this reads.
type jobState struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// checkBackground reads the daemon's own job-state file directly, rather
// than `claude agents --json` -- verified against real dispatches, not
// assumed: a launch that failed immediately (an invalid flag) printed a
// normal "backgrounded · <id>" line and left a job state file reporting
// "state":"failed", but never appeared in `claude agents --json` at all, at
// any point before or after. A freshly-started, genuinely working session
// was ALSO absent from that listing moments after launch. The job state
// file was the one signal that was present and correct in every case tried.
func (r Record) checkBackground() (Status, string, error) {
	if r.DaemonID == "" {
		return Missing, "no daemon id was captured at dispatch time", nil
	}
	dir, err := claudeJobsDir()
	if err != nil {
		return Alive, "", err
	}
	return checkJobState(filepath.Join(dir, r.DaemonID, "state.json"))
}

// claudeJobsDir resolves ~/.claude/jobs, the directory `claude --bg` writes
// one subdirectory per daemon into.
func claudeJobsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "jobs"), nil
}

// checkJobState reads one daemon's own state file directly -- separated from
// checkBackground so a test can point it at a fixture file instead of the
// real ~/.claude/jobs.
func checkJobState(path string) (Status, string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Missing, "no job state file at " + path, nil
	}
	if err != nil {
		return Alive, "", fmt.Errorf("read job state %s: %w", path, err)
	}
	var st jobState
	if err := json.Unmarshal(data, &st); err != nil {
		return Alive, "", fmt.Errorf("parse job state %s: %w", path, err)
	}
	if st.State == "failed" {
		return Failed, st.Detail, nil
	}
	// "working", "blocked", or any state not yet observed: assume alive.
	// See the Alive doc comment for why that is the safe default.
	return Alive, st.Detail, nil
}
