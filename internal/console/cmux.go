package console

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// CmuxRunner executes `cmux sessions list --session <id> --json` and returns
// its combined output. Injectable for tests -- the real implementation shells
// to the `cmux` binary, which is not present in every environment this
// package runs in (it is this operator's local machine tool, not a lacquer
// dependency).
var CmuxRunner = func(sessionID string) ([]byte, error) {
	return exec.Command("cmux", "sessions", "list", "--session", sessionID, "--json").Output()
}

// CmuxSessionInfo is the subset of one `cmux sessions list --json` entry this
// package cares about: an authoritative, pid-backed liveness signal that
// exists independently of a claude job's own state file. Discovered this
// session after manually reconstructing a dead session's pid via `ps aux |
// grep --resume <uuid>` to kill it -- `cmux sessions list --session <id>
// --json` reports the pid directly, plus whether it still exists.
//
// Verified limit: cmux's own session store (~/.cmuxterm/claude-hook-sessions.json)
// only records sessions launched through a cmux-managed terminal surface —
// a detached `claude --bg` daemon (Background mode, this package's actual bg
// fleet) never registers there at all, confirmed against a live dispatch
// (zero matches, session id absent from the store file). So this enrichment
// currently only ever fires for Tmux-mode records, which DO run inside a
// real cmux surface. It is wired into checkBackground anyway, harmlessly, so
// it starts working automatically the moment bg dispatch either routes
// through a cmux-tracked surface or cmux starts tracking bg daemons itself
// -- no call site needs to change when that happens.
type CmuxSessionInfo struct {
	PID             int    `json:"pid"`
	StoredPIDExists bool   `json:"stored_pid_exists"`
	AgentLifecycle  string `json:"agent_lifecycle"`
}

type cmuxSessionsResponse struct {
	Sessions []CmuxSessionInfo `json:"sessions"`
}

// CmuxInfo looks up sessionID via cmux, returning (info, true, nil) on a
// match, (CmuxSessionInfo{}, false, nil) when cmux is installed and ran fine
// but reported no matching session (a normal outcome -- not every dispatch is
// a cmux-tracked agent, and a session cmux never saw is not an error), or a
// non-nil error only when cmux itself could not be run at all (not
// installed, or a real invocation failure). Callers should treat a non-nil
// error as "enrichment unavailable," never as a liveness verdict -- this is
// supplementary corroboration for Record.Check's own job-state read, not a
// replacement for it.
func CmuxInfo(sessionID string) (CmuxSessionInfo, bool, error) {
	if sessionID == "" {
		return CmuxSessionInfo{}, false, nil
	}
	out, err := CmuxRunner(sessionID)
	if err != nil {
		return CmuxSessionInfo{}, false, fmt.Errorf("cmux sessions list --session %s: %w", sessionID, err)
	}
	var resp cmuxSessionsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return CmuxSessionInfo{}, false, fmt.Errorf("parse cmux sessions list output: %w", err)
	}
	if len(resp.Sessions) == 0 {
		return CmuxSessionInfo{}, false, nil
	}
	return resp.Sessions[0], true, nil
}
