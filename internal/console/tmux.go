package console

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// tmuxSessionName is the tmux session name lacquer uses for a dispatch named
// name.
//
// tmux reads '.' and ':' in a target as window and pane separators, so a
// session whose name contains one can never be addressed by name: verified
// on tmux 3.7c, `tmux has-session -t example.com` fails with "can't
// find pane: com" while that session runs. 3.7c keeps such a name verbatim;
// older releases rewrite both characters to '_' on creation. Mapping them
// here makes the created name addressable, and the same on every version.
func tmuxSessionName(name string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(name)
}

// tmuxCollision refuses a tmux dispatch of name when another name in the same
// roster maps to the same session (a.b and a_b): dispatching one would find,
// and later check or kill, the other's session.
func tmuxCollision(name string, all []string) error {
	session := tmuxSessionName(name)
	for _, other := range all {
		if other != name && tmuxSessionName(other) == session {
			return fmt.Errorf("refusing to dispatch %s in tmux mode: %s maps to the same tmux session name, %q (tmux cannot address '.' or ':' in a session name, so both become '_'); rename one of them", name, other, session)
		}
	}
	return nil
}

// tmuxSessionID resolves an exact session name to tmux's own session id
// ("$3"), which is what every tmux command here then targets.
//
// Never `-t <name>`: tmux resolves a target by exact name, then by PREFIX,
// then as an fnmatch pattern. Verified on 3.7c: with only probe-long
// running, `has-session -t probe` succeeds and `kill-session -t probe` kills
// probe-long. `-t =<name>` would force an exact match but still splits on
// '.', which an old record's raw dotted name contains; listing and comparing
// names here has neither problem.
//
// found is false with a nil error when no session has that name, including
// when no tmux server is running at all. err means tmux itself could not be
// run, which says nothing about whether the session exists.
func tmuxSessionID(name string) (id string, found bool, err error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", false, nil // "no server running": there are no sessions
		}
		return "", false, fmt.Errorf("list tmux sessions: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		sid, sname, ok := strings.Cut(line, "\t")
		if ok && sname == name {
			return sid, true, nil
		}
	}
	return "", false, nil
}

// findTmuxSession looks for the first of names that is a live session.
func findTmuxSession(names []string) (id, name string, found bool, err error) {
	for _, n := range names {
		id, found, err := tmuxSessionID(n)
		if err != nil || found {
			return id, n, found, err
		}
	}
	return "", "", false, nil
}

// tmuxNames is every name r's session may be under, most specific first.
// Records written before lacquer recorded TmuxSession name the session only
// by r.Name: raw, as tmux 3.7c created it, or rewritten to '_' by an older
// tmux.
func (r Record) tmuxNames() []string {
	if r.TmuxSession != "" {
		return []string{r.TmuxSession}
	}
	names := []string{r.Name}
	if mapped := tmuxSessionName(r.Name); mapped != r.Name {
		names = append(names, mapped)
	}
	return names
}
