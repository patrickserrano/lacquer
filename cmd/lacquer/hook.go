package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/patrickserrano/lacquer/internal/console"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/producers"
)

// hookStdin is where a hook's payload is read from; tests replace it.
var hookStdin io.Reader = os.Stdin

// hookDeadline is the whole hook's budget. The shipped hook sets a timeout of
// 10s, and a hook that gets there is killed and shown to the operator as an
// error, so this one gives up first.
const hookDeadline = 5 * time.Second

// isInboxHookStop reports whether the console arguments are `inbox hook stop`.
// It is decided before the lacquer-root checks that gate every other console
// subcommand: those can fail, and this one must never fail a session.
func isInboxHookStop(args []string) bool {
	return len(args) >= 3 && args[0] == "inbox" && args[1] == "hook" && args[2] == "stop"
}

// runInboxHookStop is `lacquer console inbox hook stop`, the Claude Code Stop
// hook shipped in .claude/settings.json. It ALWAYS returns 0: a hook that fails
// is shown in the session it watches, and one that blocks or exits 2 can keep
// the agent from stopping. Every problem is a warning on stderr.
func runInboxHookStop(args []string, getenv func(string) string, sessions console.SessionSource, stderr io.Writer) int {
	warn := func(format string, a ...any) {
		fmt.Fprintf(stderr, "lacquer inbox hook stop: "+format+"\n", a...)
	}
	fs := flag.NewFlagSet("inbox hook stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	inboxFlag := fs.String("inbox", "", "")
	rosterPath := fs.String("roster", getenv("LACQUER_ROSTER"), "")
	if err := fs.Parse(args); err != nil {
		warn("%v", err)
		return 0
	}
	if getenv("CLAUDE_JOB_DIR") == "" {
		return 0 // an interactive session: the operator is right there
	}

	type outcome struct {
		warns []string
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		var o outcome
		raw, err := io.ReadAll(hookStdin)
		if err != nil {
			done <- outcome{err: fmt.Errorf("reading stdin: %w", err)}
			return
		}
		in, err := producers.ParseStopHook(raw)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		path, _, err := inbox.Path(*inboxFlag, getenv)
		if err != nil {
			done <- outcome{err: err}
			return
		}
		var roster fleet.Roster
		if *rosterPath != "" {
			if roster, err = fleet.LoadRoster(*rosterPath); err != nil {
				o.warns = append(o.warns, fmt.Sprintf("roster unreadable, naming the project by its directory: %v", err))
			}
		}
		_, _, w, err := producers.AgentIdle(producers.AgentIdleOptions{
			InboxPath: path,
			JobDir:    getenv("CLAUDE_JOB_DIR"),
			Name: func(id string) (string, error) {
				list, err := sessions.List(context.Background())
				if err != nil {
					return "", err
				}
				for _, s := range list {
					if s.SessionID == id {
						return s.Name, nil
					}
				}
				return "", nil
			},
			Project: func(cwd string) string { return console.ProjectFor(cwd, roster) },
		}, in)
		if w != "" {
			o.warns = append(o.warns, w)
		}
		o.err = err
		done <- o
	}()

	select {
	case o := <-done:
		for _, w := range o.warns {
			warn("%s", w)
		}
		if o.err != nil {
			warn("%v", o.err)
		}
	case <-time.After(hookDeadline):
		warn("gave up after %s; no inbox entry was written", hookDeadline)
	}
	return 0
}
