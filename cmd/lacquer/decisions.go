package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/decisions"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/protection"
)

// defaultFleetRepo holds the decisions that span repositories (#427): one issue
// there, rather than a copy in every project's, where the copies would diverge.
// It is the one place this repository is named; $LACQUER_FLEET_REPO and
// --fleet-repo replace it, and it is only ever written to if it is also in the
// roster or $LACQUER_EXTRA_REPOS, like any other repository.
const defaultFleetRepo = "patrickserrano/fleet-ops"

const envFleetRepo = "LACQUER_FLEET_REPO"

// fleetRepoFrom is the fleet-wide repository: the flag, else the environment,
// else the default.
func fleetRepoFrom(flagVal string, getenv func(string) string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := getenv(envFleetRepo); v != "" {
		return v
	}
	return defaultFleetRepo
}

// The GitHub reads `lacquer decisions` makes, and where a checkout's repository
// comes from. Tests replace them, so a test can never reach the real gh.
var (
	decisionsRunner ciwait.Runner = ciwait.GH
	originSlug                    = protection.Slug
)

const decisionsUsage = "usage: lacquer decisions [<owner/name> | --fleet] [--fleet-repo O/N]"

// decisionsCmd is `lacquer decisions`: print the operator's recorded decisions,
// oldest first. It only reads. "None recorded" exits 0 and a gh that could not
// answer exits 1, so the two never look alike.
func decisionsCmd(args []string, getenv func(string) string, projectRoot string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("decisions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fleetWide := fs.Bool("fleet", false, "the fleet-wide decisions, from the fleet repository")
	fleetFlag := fs.String("fleet-repo", "", "the fleet repository (or $"+envFleetRepo+"; default "+defaultFleetRepo+")")
	var pos []string
	rest := args
	for { // flags on either side of the repository
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		pos = append(pos, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	fleetRepoSet := false
	fs.Visit(func(f *flag.Flag) { fleetRepoSet = fleetRepoSet || f.Name == "fleet-repo" })
	if len(pos) > 1 || (*fleetWide && len(pos) > 0) || (fleetRepoSet && !*fleetWide) {
		fmt.Fprintln(stderr, decisionsUsage)
		return 2
	}
	var repo string
	switch {
	case *fleetWide:
		repo = fleetRepoFrom(*fleetFlag, getenv)
	case len(pos) == 1:
		repo = pos[0]
	default:
		slug, err := originSlug(projectRoot)
		if err != nil { // its own hint names a flag this command does not have
			return fail(stderr, errors.New("decisions: cannot tell which repository this is (no GitHub origin remote here); name it, `lacquer decisions owner/name`, or read the fleet's with `lacquer decisions --fleet`"))
		}
		repo = slug
	}
	if o, n, ok := strings.Cut(repo, "/"); !ok || o == "" || n == "" || strings.ContainsAny(repo, " \t\r\n") || strings.HasPrefix(repo, "-") || strings.Contains(n, "/") {
		return fail(stderr, fmt.Errorf("decisions: %q is not owner/name", repo))
	}
	err := decisions.Print(stdout, decisionsRunner, repo)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, decisions.ErrNone):
		fmt.Fprintf(stdout, "no decisions recorded for %s\n", decisions.Clean(repo))
		return 0
	}
	return fail(stderr, fmt.Errorf("decisions: could not read the decisions for %s: %w", decisions.Clean(repo), err))
}

// projectRepoFlags carries the roster's project-to-repository mapping to the
// popup, which has no roster of its own: "which repository is this entry's
// project?" is how a decision from an item with no issue ref finds its repo.
// The name goes hex-encoded, like an id, so nothing in it can be a tmux format.
type projectRepoFlags struct{ list []fleet.Entry }

func (p *projectRepoFlags) String() string { return "" }
func (p *projectRepoFlags) Set(v string) error {
	name, repo, ok := strings.Cut(v, "=")
	raw, err := hex.DecodeString(name)
	if !ok || err != nil || repo == "" {
		return fmt.Errorf("%q is not <hex name>=owner/name", v)
	}
	p.list = append(p.list, fleet.Entry{Name: string(raw), Repo: repo})
	return nil
}

func projectRepoArgs(roster fleet.Roster) []string {
	var out []string
	for _, p := range roster.Project {
		if p.Repo != "" && p.Name != "" {
			out = append(out, "--project-repo="+hex.EncodeToString([]byte(p.Name))+"="+p.Repo)
		}
	}
	return out
}
