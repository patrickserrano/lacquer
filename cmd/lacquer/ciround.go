package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/patrickserrano/lacquer/internal/cirounds"
	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/config"
)

const ciRoundUsage = "usage: lacquer ci-round <begin|status> <N> [--repo O/N] [--reason TEXT] [--sha SHA] [--inbox F] [--manifest-ref REF]"

// ciRoundCmd is `lacquer ci-round`: the cap on how many rounds of CI an agent
// may spend on one pull request. See internal/cirounds for the model.
func ciRoundCmd(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 1 || (args[0] != "begin" && args[0] != "status") {
		fmt.Fprintln(stderr, ciRoundUsage)
		return cirounds.CodeUnavailable
	}
	sub := args[0]
	fs := flag.NewFlagSet("ci-round "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "repository as owner/name (default: inferred by gh from this checkout)")
	reason := fs.String("reason", "", "begin, round 2 and later: what you changed, naming a check the previous round reported failing")
	sha := fs.String("sha", "", "begin: the commit you are about to push (default: git rev-parse HEAD)")
	inboxPath := fs.String("inbox", getenv("LACQUER_INBOX"), "inbox file to raise an ACTION in when the budget is exhausted (or $LACQUER_INBOX)")
	manifestRef := fs.String("manifest-ref", "origin/main", "git ref whose .lacquer.toml sets the cap. Not the working tree: a branch must not be able to raise its own cap")

	// Flags may sit on either side of the PR number, as in `wait pr`.
	var pos []string
	rest := args[1:]
	for {
		if err := fs.Parse(rest); err != nil {
			return cirounds.CodeUnavailable
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(pos) != 1 {
		fmt.Fprintln(stderr, ciRoundUsage)
		return cirounds.CodeUnavailable
	}
	n, err := strconv.Atoi(pos[0])
	if err != nil || n < 1 {
		fmt.Fprintf(stderr, "lacquer ci-round: %q is not a pull request number\n", pos[0])
		return cirounds.CodeUnavailable
	}

	limit, err := ciRoundCap(*manifestRef)
	if err != nil {
		fmt.Fprintf(stderr, "lacquer ci-round: %v\nThe tool did NOT record a round and cannot say one is allowed. Do not push; escalate.\n", err)
		return cirounds.CodeUnavailable
	}
	o := cirounds.Options{PR: n, Repo: *repo, Cap: limit, Reason: *reason, Inbox: *inboxPath, Run: ciwait.GH}

	var res cirounds.Result
	ctx := context.Background()
	if sub == "status" {
		res = cirounds.Status(ctx, o)
	} else {
		o.SHA = *sha
		if o.SHA == "" {
			out, err := exec.Command("git", "rev-parse", "HEAD").Output()
			if err != nil {
				fmt.Fprintf(stderr, "lacquer ci-round: git rev-parse HEAD: %v (pass --sha)\n", err)
				return cirounds.CodeUnavailable
			}
			o.SHA = strings.TrimSpace(string(out))
		}
		res = cirounds.Begin(ctx, o)
	}
	// Grants and status are for the agent's stdout; every refusal is too, so
	// a caller capturing only stdout still reads why. Exit codes carry the rest.
	fmt.Fprint(stdout, res.Text)
	return res.Code
}

// ciRoundCap is [project].ci_round_cap as committed at ref, or the default when
// that ref carries no manifest. It reads the ref, not the working tree, for the
// reason the CLAUDE memory notes give for exclusions: the checkout is whatever a
// session left in it, and here the session is the thing being capped.
func ciRoundCap(ref string) (int, error) {
	cmd := exec.Command("git", "show", ref+":.lacquer.toml")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		// No such ref or no manifest on it: a project that has declared
		// nothing, so the default applies. Anything else is git failing, which
		// is not that.
		if _, ok := err.(*exec.ExitError); ok && manifestAbsent(errb.String()) {
			return config.DefaultCIRoundCap, nil
		}
		return 0, fmt.Errorf("git show %s:.lacquer.toml: %v: %s", ref, err, strings.TrimSpace(errb.String()))
	}
	dir, err := os.MkdirTemp("", "ci-round-manifest-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, ".lacquer.toml")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		return 0, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return 0, fmt.Errorf("%s:.lacquer.toml does not load: %v", ref, err)
	}
	return cfg.Project.CIRoundCap(), nil
}

func manifestAbsent(stderr string) bool {
	return strings.Contains(stderr, "does not exist") || strings.Contains(stderr, "exists on disk, but not in") ||
		strings.Contains(stderr, "unknown revision") || strings.Contains(stderr, "bad revision") || strings.Contains(stderr, "invalid object name")
}

func usageCIRound(w io.Writer) {
	fmt.Fprintln(w, "  ci-round begin <N> [--reason TEXT] [--sha SHA] [--repo O/N] [--inbox F] [--manifest-ref REF]")
	fmt.Fprintln(w, "                               ask for one of the (default 2) rounds of CI an AGENT gets on PR N,")
	fmt.Fprintln(w, "                               BEFORE the push it covers; the count lives on the PR, so a fresh")
	fmt.Fprintln(w, "                               session on an exhausted PR is blocked too. Round 2 needs --reason")
	fmt.Fprintln(w, "                               naming a check the previous round reported failing. A head the tool")
	fmt.Fprintln(w, "                               never recorded is a human push and resets the budget. Exit codes:")
	fmt.Fprintln(w, "                                  0  granted (or already recorded): push exactly this commit")
	fmt.Fprintln(w, "                                 10  EXHAUSTED: a report, not a failure. Comments on the PR with")
	fmt.Fprintln(w, "                                     what is still failing, raises an inbox ACTION; do NOT push")
	fmt.Fprintln(w, "                                 11  round 2+ needs a --reason naming a failing check")
	fmt.Fprintln(w, "                                 12  nothing to spend a round on: the last round reported no")
	fmt.Fprintln(w, "                                     failure (still running, timed out, no checks, passed), or the")
	fmt.Fprintln(w, "                                     commit is already the PR's head, pushed by a person")
	fmt.Fprintln(w, "                                 13  could not check (gh failing, PR closed, bad usage): nothing")
	fmt.Fprintln(w, "                                     was recorded, so do not push; escalate")
	fmt.Fprintln(w, "                               The cap is [project].ci_round_cap, read from --manifest-ref")
	fmt.Fprintln(w, "                               (default origin/main), never from the working tree")
	fmt.Fprintln(w, "  ci-round status <N> [--repo O/N] [--manifest-ref REF]")
	fmt.Fprintln(w, "                               read-only: rounds spent and left (exit 10 if exhausted, else 0)")
}
