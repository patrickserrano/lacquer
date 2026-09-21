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
)

// The exit codes of `lacquer wait pr`. 0-3 are the four ways a wait can END and
// are never conflated; 4 is the wait itself failing. Documented in usage() and
// site/src/content/docs/reference/commands.md — keep the three in step.
const waitExitCouldNotWait = 4

// waitCmd is `lacquer wait pr <N>`: block, in one process, until every check on
// a pull request is terminal. It costs no model tokens while it waits — the
// process sleeps — so an agent runs it in the background and is woken once,
// when it returns.
func waitCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 || args[0] != "pr" {
		fmt.Fprintln(stderr, "usage: lacquer wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json]")
		return waitExitCouldNotWait
	}
	fs := flag.NewFlagSet("wait pr", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "repository as owner/name (default: inferred by gh from this checkout)")
	timeout := fs.Duration("timeout", 20*time.Minute, "give up, exit 2, if a check is still running after this long")
	interval := fs.Duration("interval", 15*time.Second, "time between polls")
	grace := fs.Duration("empty-grace", 30*time.Second, "how long an empty check list is re-checked before it is reported as exit 3")
	asJSON := fs.Bool("json", false, "print the result as JSON")

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
		fmt.Fprintln(stderr, "usage: lacquer wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json]")
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
	return res.Outcome.ExitCode()
}

func usageWait(w io.Writer) {
	fmt.Fprintln(w, "  wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json]")
	fmt.Fprintln(w, "                               block, in one process, until every check on PR N is terminal, then")
	fmt.Fprintln(w, "                               print each one's name, conclusion and duration. It sleeps between")
	fmt.Fprintln(w, "                               polls, so waiting costs no model tokens: run it in the background")
	fmt.Fprintln(w, "                               and you are woken once, when it returns. Defaults: --timeout 20m,")
	fmt.Fprintln(w, "                               --interval 15s; --repo is inferred from the checkout. Exit codes,")
	fmt.Fprintln(w, "                               four outcomes that are never conflated:")
	fmt.Fprintln(w, "                                 0  every check finished and none failed (skipped ones are named:")
	fmt.Fprintln(w, "                                    a skipped job did not run, so read that line)")
	fmt.Fprintln(w, "                                 1  at least one check FAILED (named)")
	fmt.Fprintln(w, "                                 2  TIMED OUT: still running when --timeout hit (which ones are")
	fmt.Fprintln(w, "                                    named); neither a failure nor a pass")
	fmt.Fprintln(w, "                                 3  NO CHECKS found: an empty check list is never a pass")
	fmt.Fprintln(w, "                                 4  the wait itself failed: gh missing or failing repeatedly, PR")
	fmt.Fprintln(w, "                                    closed or merged, bad usage. The PR's state is UNKNOWN")
	fmt.Fprintln(w, "                               A new head commit mid-wait discards the old commit's results and")
	fmt.Fprintln(w, "                               waits on the new one's (the timeout is not reset)")
}
