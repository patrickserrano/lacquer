package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/producers"
)

// The exit codes of `lacquer wait pr`. 0-3 are the four ways a wait can END and
// are never conflated; 4 is the wait itself failing. Documented in usage() and
// site/src/content/docs/reference/commands.md — keep the three in step.
const waitExitCouldNotWait = 4

// waitCmd is `lacquer wait pr <N>`: block, in one process, until every check on
// a pull request is terminal. It costs no model tokens while it waits — the
// process sleeps — so an agent runs it in the background and is woken once,
// when it returns.
func waitCmd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 1 || args[0] != "pr" {
		fmt.Fprintln(stderr, "usage: lacquer wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json] [--inbox F] [--no-inbox]")
		return waitExitCouldNotWait
	}
	fs := flag.NewFlagSet("wait pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "repository as owner/name (default: inferred by gh from this checkout)")
	timeout := fs.Duration("timeout", 20*time.Minute, "give up, exit 2, if a check is still running after this long")
	interval := fs.Duration("interval", 15*time.Second, "time between polls")
	grace := fs.Duration("empty-grace", 30*time.Second, "how long an empty check list is re-checked before it is reported as exit 3")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	inboxFlag := fs.String("inbox", "", "inbox file to raise an ACTION in when the wait ends timed out, untested or unable to run (default: $LACQUER_INBOX, else $XDG_STATE_HOME/lacquer/inbox.jsonl, else ~/.local/state/lacquer/inbox.jsonl)")
	noInbox := fs.Bool("no-inbox", false, "do not write the inbox, whatever the outcome")

	// Flags may sit on either side of the PR number: `wait pr 12 --json` and
	// `wait pr --json 12` both work, where flag.Parse alone stops at the number.
	var pos []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return waitExitCouldNotWait
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, "usage: lacquer wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json] [--inbox F] [--no-inbox]")
		return waitExitCouldNotWait
	}
	n, err := strconv.Atoi(pos[0])
	if err != nil || n < 1 {
		fmt.Fprintf(stderr, "lacquer wait pr: %q is not a pull request number\n", pos[0])
		return waitExitCouldNotWait
	}
	if *timeout <= 0 || *interval <= 0 || *grace < 0 {
		fmt.Fprintln(stderr, "lacquer wait pr: --timeout and --interval must be positive")
		return waitExitCouldNotWait
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res := ciwait.Wait(ctx, ciwait.Options{
		PR: n, Repo: *repo,
		Timeout: *timeout, Interval: *interval, EmptyGrace: *grace, MaxErrors: 5,
		Run: ciwait.GH,
	})
	if *asJSON {
		fmt.Fprint(stdout, ciwait.FormatJSON(res))
	} else {
		fmt.Fprint(stdout, ciwait.Format(res))
	}
	if !*noInbox {
		recordGate(*inboxFlag, getenv, res, ctx.Err() != nil, stderr)
	}
	return res.Outcome.ExitCode()
}

// recordGate puts the outcomes that need a human in the operator's inbox. It
// can only ever warn: the exit code is the wait's answer, and an inbox that
// cannot be written must not turn a 2 into a 0 or a 4, so callers keep theirs.
func recordGate(inboxFlag string, getenv func(string) string, res ciwait.Result, interrupted bool, stderr io.Writer) {
	path, _, err := inbox.Path(inboxFlag, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "lacquer wait pr: WARNING: the inbox was NOT written (%v); the exit code is unchanged\n", err)
		return
	}
	e, added, err := producers.GateRejection(path, res, interrupted)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "lacquer wait pr: WARNING: the inbox %s was NOT written (%v); this outcome needs a human and is not in their inbox; the exit code is unchanged\n", path, err)
	case added:
		fmt.Fprintf(stderr, "lacquer wait pr: inbox ACTION %s written to %s: %s\n", e.ID, path, e.Title)
	}
}

func usageWait(w io.Writer) {
	fmt.Fprintln(w, "  wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json] [--inbox F] [--no-inbox]")
	fmt.Fprintln(w, "                               block, in one process, until every check on PR N is terminal, then")
	fmt.Fprintln(w, "                               print each one's name, conclusion and duration. It sleeps between")
	fmt.Fprintln(w, "                               polls, so waiting costs no model tokens: run it in the background")
	fmt.Fprintln(w, "                               and you are woken once, when it returns. Defaults: --timeout 20m,")
	fmt.Fprintln(w, "                               --interval 15s; --repo is inferred from the checkout. Exit codes,")
	fmt.Fprintln(w, "                               four outcomes that are never conflated:")
	fmt.Fprintln(w, "                                 0  every check finished and none failed (skipped ones are named:")
	fmt.Fprintln(w, "                                    a skipped job did not run, so read that line)")
	fmt.Fprintln(w, "                                 1  at least one check FAILED (named). A failure is decisive: if")
	fmt.Fprintln(w, "                                    --timeout hit with others still running, it is still 1, and")
	fmt.Fprintln(w, "                                    the running ones are listed as abandoned")
	fmt.Fprintln(w, "                                 2  TIMED OUT: still running when --timeout hit and NONE failed")
	fmt.Fprintln(w, "                                    (which ones are named); neither a failure nor a pass")
	fmt.Fprintln(w, "                                 3  NO CHECKS found: an empty check list is never a pass")
	fmt.Fprintln(w, "                                 4  the wait itself failed: gh missing or failing repeatedly, PR")
	fmt.Fprintln(w, "                                    closed or merged, bad usage. The PR's state is UNKNOWN")
	fmt.Fprintln(w, "                               Exits 2, 3 and 4 also raise an inbox ACTION (an exit 1 does not: the")
	fmt.Fprintln(w, "                               author is already fixing it; a PR that was merged or closed, or a")
	fmt.Fprintln(w, "                               wait you interrupted, needs none). Same PR and head commit: one entry.")
	fmt.Fprintln(w, "                               A failed write only warns on stderr; the exit code never changes.")
	fmt.Fprintln(w, "                               --no-inbox turns it off; --inbox F picks the file, as for console.")
	fmt.Fprintln(w, "                               A new head commit mid-wait discards the old commit's results and")
	fmt.Fprintln(w, "                               waits on the new one's (the timeout is not reset)")
}
