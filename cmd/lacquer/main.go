package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/patrickserrano/lacquer/internal/adoptcmd"
	"github.com/patrickserrano/lacquer/internal/archetype"
	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/console"
	"github.com/patrickserrano/lacquer/internal/depignore"
	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/exclusion"
	"github.com/patrickserrano/lacquer/internal/fixcmd"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/initcmd"
	"github.com/patrickserrano/lacquer/internal/onboardcmd"
	"github.com/patrickserrano/lacquer/internal/pluginbootstrap"
	"github.com/patrickserrano/lacquer/internal/retire"
	"github.com/patrickserrano/lacquer/internal/rootcheck"
	"github.com/patrickserrano/lacquer/internal/skillsync"
	"github.com/patrickserrano/lacquer/internal/status"
	syncpkg "github.com/patrickserrano/lacquer/internal/sync"
	"github.com/patrickserrano/lacquer/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is the testable entry point: it dispatches one CLI invocation and returns
// the process exit code. args is os.Args[1:]; getenv resolves environment (chiefly
// LACQUER_ROOT); stdout/stderr receive command output. main() is a thin wrapper.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}

	// help is answered before anything else: print to STDOUT and exit 0, so
	// `lacquer --help` isn't a non-zero "unknown command" with output on stderr.
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	}

	// lacquerRoot is the directory holding this repo's VERSION/core/profiles,
	// resolved from LACQUER_ROOT and defaulting to ".".
	lacquerRoot := getenv("LACQUER_ROOT")
	if lacquerRoot == "" {
		lacquerRoot = "."
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return fail(stderr, err)
	}

	switch args[0] {
	case "init":
		// init reads lacquerRoot to gate detected profiles to those that ship;
		// with it unset (default ".") every profile would be silently dropped.
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(stderr)
		stack := fs.String("stack", "", "archetype to seed the manifest from (see --list-stacks)")
		list := fs.Bool("list-stacks", false, "print the available archetypes and exit")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *list {
			return listStacks(lacquerRoot, stdout, stderr)
		}
		summary, err := initcmd.Run(lacquerRoot, projectRoot, *stack)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, summary)
	case "onboard":
		// onboard invokes init, which reads lacquerRoot (see init above).
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("onboard", flag.ContinueOnError)
		fs.SetOutput(stderr)
		// No default org: the lacquer must not bake in any one org's identity, so
		// repo creation requires an explicit --org (see onboardcmd.Run).
		org := fs.String("org", "", "GitHub org for repo creation (required unless --no-repo)")
		noRepo := fs.Bool("no-repo", false, "do not create a repo even if no remote exists")
		stack := fs.String("stack", "", "archetype to seed the manifest from (see `lacquer init --list-stacks`)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		summary, err := onboardcmd.Run(lacquerRoot, projectRoot, *org, *stack, !*noRepo)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, summary)
	case "adopt":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		summary, changed, err := adoptcmd.Run(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, summary)
		if changed {
			fmt.Fprintln(stdout, "Run `lacquer sync` to apply the newly-managed profiles.")
		}
	case "sync":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
		fs.SetOutput(stderr)
		force := fs.Bool("force", false, "overwrite local changes the lacquer did not make (see `lacquer audit`)")
		// Opt-in, not default. sync's contract is that it writes lacquer-managed
		// files and nothing else; --fix deliberately breaks that by rewriting
		// project SOURCE, so it has to be asked for rather than discovered in a
		// diff. Adoption is when it earns its keep: the first sync of a mature
		// app is exactly when hundreds of mechanical violations appear at once.
		doFix := fs.Bool("fix", false, "after syncing, run the profiles' autofixers over the project source")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		// Say which checkout this is rendering from, before rendering. sync is
		// only ever as current as LACQUER_ROOT, and a stale root does not fail
		// — it reports success and writes the previous version, which is
		// indistinguishable from having had nothing to do.
		root := rootcheck.Inspect(lacquerRoot, getenv("LACQUER_NO_FETCH") == "")
		fmt.Fprintln(stdout, root.Describe())

		// Fail closed on a stale binary. It renders TODAY's profiles with the
		// logic of whatever tree it was compiled from, and every symptom looks
		// like a bug in the current version: a 1.3.0 binary reading 1.5.4 content
		// rewrote a project's lefthook.yml from 135 lines to 60 — dropping a whole
		// profile's hooks — and reported success.
		//
		// This is the same principle as the clobber guard and the placeholder
		// preflight: a project is better off unsynced than synced by something
		// that cannot say what it is. LACQUER_ALLOW_STALE_BINARY keeps the
		// deliberate case possible, because building from a feature worktree to
		// test an unreleased change is a normal thing to do here.
		if root.StaleBinary() && getenv("LACQUER_ALLOW_STALE_BINARY") == "" {
			return fail(stderr, fmt.Errorf(
				"this lacquer binary was built from %s but is reading %s content.\n"+
					"The render would mix %s logic with %s profiles and report success either way.\n"+
					"Rebuild:  go build -o <dest> ./cmd/lacquer   (from %s)\n"+
					"Override: LACQUER_ALLOW_STALE_BINARY=1, if you are deliberately testing an unreleased change",
				root.BuiltVersion, root.Version, root.BuiltVersion, root.Version, root.Root))
		}

		res, err := syncpkg.Run(lacquerRoot, projectRoot, *force)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "sync complete: %d regions, %d assets\n", res.Regions, res.Assets)
		// After the success line on purpose: a warning above it reads as part of
		// the preamble and is scrolled past.
		if w := root.Warning(); w != "" {
			fmt.Fprintln(stderr, w)
		}
		if *doFix {
			if code := runFixers(lacquerRoot, projectRoot, stdout, stderr); code != 0 {
				return code
			}
		}
	case "doctor":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		dfs := flag.NewFlagSet("doctor", flag.ContinueOnError)
		dfs.SetOutput(stderr)
		// Repeatable: --profile ios --profile supabase. A CI job proves the
		// toolchain it actually has; omitting it proves everything, which is
		// what a developer wants locally.
		var only profileList
		dfs.Var(&only, "profile", "prove only this profile's checks (repeatable; default all)")
		if err := dfs.Parse(args[1:]); err != nil {
			return 2
		}
		manifest := filepath.Join(projectRoot, ".lacquer.toml")
		cfg, err := config.Load(manifest)
		if err != nil {
			return fail(stderr, fmt.Errorf("load %s: %w", manifest, err))
		}
		fmt.Fprintln(stdout, "proving each check can fail:")
		results, err := doctor.Run(lacquerRoot, projectRoot, cfg, only, stdout)
		if err != nil {
			return fail(stderr, err)
		}
		if len(results) == 0 {
			fmt.Fprintln(stdout, "  (no profile in this project ships self-tests)")
			return 0
		}
		bad := doctor.Failures(results)
		fmt.Fprintf(stdout, "\n%d/%d checks proved they can fail.\n", len(results)-len(bad), len(results))
		if len(bad) > 0 {
			// Exit 5, distinct from audit's 3 (drift) and 4 (baseline), so a
			// caller can tell "a check is broken" from "the project is wrong".
			fmt.Fprintln(stderr, "a check that cannot fail is not a check.")
			return 5
		}
	case "fix":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		if code := runFixers(lacquerRoot, projectRoot, stdout, stderr); code != 0 {
			return code
		}
	case "skills":
		// skills only reads the project's own .lacquer.toml — it needs no
		// lacquerRoot (unlike sync/audit/status, which render/compare against
		// this checkout's shipped content).
		manifest := filepath.Join(projectRoot, ".lacquer.toml")
		cfg, err := config.Load(manifest)
		if err != nil {
			return fail(stderr, fmt.Errorf("load %s: %w", manifest, err))
		}
		entries, err := cfg.Project.ParsedSkills()
		if err != nil {
			return fail(stderr, err)
		}
		if len(entries) == 0 {
			fmt.Fprintln(stdout, "no [project].skills declared in .lacquer.toml; nothing to install")
			return 0
		}
		res, err := skillsync.Install(projectRoot, entries, cfg.Project.EffectiveTools())
		if err != nil {
			return fail(stderr, err)
		}
		for _, name := range res.Installed {
			fmt.Fprintf(stdout, "installed: %s\n", name)
		}
		for name, out := range res.Failed {
			fmt.Fprintf(stderr, "failed: %s\n%s\n", name, out)
		}
		if len(res.Undeclared) > 0 {
			fmt.Fprintln(stdout, "installed but not declared in [project].skills (review, then `skills remove` if unwanted):")
			for _, name := range res.Undeclared {
				fmt.Fprintf(stdout, "  %s\n", name)
			}
		}
		if len(res.Failed) > 0 {
			return 1
		}
	case "plugins":
		// plugins installs the machine-level manifest shipped in the lacquer
		// repo itself (core/bootstrap/plugins.toml) — unlike skills, this is
		// not project-scoped, so it needs lacquerRoot but not projectRoot.
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		manifestPath := filepath.Join(lacquerRoot, "core", "bootstrap", "plugins.toml")
		manifest, err := pluginbootstrap.Load(manifestPath)
		if err != nil {
			return fail(stderr, fmt.Errorf("load %s: %w", manifestPath, err))
		}
		res := pluginbootstrap.Apply(manifest)
		for _, name := range res.Marketplaces {
			fmt.Fprintf(stdout, "marketplace: %s\n", name)
		}
		for _, name := range res.Plugins {
			fmt.Fprintf(stdout, "installed: %s\n", name)
		}
		for name, out := range res.Failed {
			fmt.Fprintf(stderr, "failed: %s\n%s\n", name, out)
		}
		if len(res.Failed) > 0 {
			return 1
		}
	case "audit":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
		if err != nil {
			return fail(stderr, fmt.Errorf("load manifest: %w", err))
		}
		// Ahead of the classification, because it explains it. A retired project's
		// report is SHORT — the scheduled workflows and dependabot.yml are simply
		// not managed units any more — and a short clean report is exactly what a
		// healthy project produces. Without this line the two are indistinguishable.
		fmt.Fprint(stdout, retire.Notice(cfg))
		rows, ver, err := audit.Classify(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, audit.Format(rows, ver))

		reports, err := baselineReports(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		if out := baseline.FormatReports(reports); out != "" {
			fmt.Fprint(stdout, "\n"+out)
		}

		// Every remaining report is computed and PRINTED before any exit code is
		// chosen. It used to return 3 here, which meant a project with a single
		// clobbered file saw no drift report and no exclusion report at all — the
		// projects furthest out of date got the least information, and had to fix
		// one problem before being told about the next. Reporting is not the same
		// decision as gating: print everything known, then rank.
		findings, err := detect.Drift(lacquerRoot, projectRoot, cfg)
		if err != nil {
			return fail(stderr, fmt.Errorf("re-detect components: %w", err))
		}
		fmt.Fprint(stdout, formatDrift(findings))

		// [project].exclude is the other exemption mechanism, and it is the one
		// `formatDrift` above actively recommends ("add the path to
		// [project].exclude to keep it unmanaged"). Reviewing it here means the
		// escape hatch this tool points people at is held to the same account as
		// the one it already gated on.
		suppressed, err := assets.Suppressed(lacquerRoot, cfg)
		if err != nil {
			return fail(stderr, fmt.Errorf("resolve exclusions: %w", err))
		}
		exclusions := exclusion.Review(cfg.Project.Exclude, suppressed, time.Now())
		fmt.Fprint(stdout, exclusion.Format(exclusions))

		// The third exemption mechanism, reviewed here for the same reason the
		// other two are. An ignore withholds a dependency update from a file that
		// otherwise promises every update opens a PR, and the only thing standing
		// between "a known incompatibility" and "we stopped looking" is that the
		// term is read by something on every run.
		ignores := depignore.Review(cfg.Components, cfg.Root, time.Now())
		fmt.Fprint(stdout, depignore.Format(ignores))

		// The one finding nothing could see before: a file the lacquer WROTE and
		// has stopped shipping. It is not drift (the lacquer would not write it
		// now), not missing, and not an exclusion, so it fell through every
		// report while sitting in the repository still running. It is reported
		// and deliberately does not appear in the exit codes below — an orphan is
		// a leftover file, not a broken project, and gating on something that
		// endangers nothing teaches people that this output is noise.
		orphans, err := audit.Orphans(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, fmt.Errorf("resolve orphans: %w", err))
		}
		fmt.Fprint(stdout, audit.FormatOrphans(orphans))

		// Exit codes, in precedence order. Unchanged from when each returned
		// early — only the reporting above moved.
		switch {
		// Exit 3 when a project change would be clobbered, so `lacquer audit` is
		// usable as a CI drift gate (documented in usage()). Clobbering takes
		// precedence over a baseline violation when both fire: losing a local
		// change is destructive, a policy violation is not.
		case len(audit.Clobbered(rows)) > 0:
			return 3
		// Exit 4 on a baseline violation — a distinct code so a CI gate can tell
		// "sync would destroy work" apart from "this project is out of standard".
		// An expired exclusion shares the code: it is the same finding (a
		// time-boxed exemption whose term ran out) wearing a different spelling,
		// and a separate code would mean touching every project's CI to teach it
		// one more number for no diagnostic gain — the output already says which.
		// An expired dependabot ignore shares this code with an expired exclusion
		// and a baseline violation, for the reason already argued for the second
		// of those: it is the same finding — a time-boxed exemption whose term ran
		// out — wearing a different spelling. A fourth number would mean teaching
		// every project's CI one more exit code for no diagnostic gain, when the
		// output above already says which one fired.
		case baseline.Blocking(reports) > 0 || exclusion.Blocking(exclusions) > 0 || depignore.Blocking(ignores) > 0:
			return 4
		// Exit 6 when the project runs a stack the lacquer manages but the manifest
		// never declared. Distinct from 3/4 because the fix is different in kind:
		// nothing is wrong with the code, the manifest is just out of date with it.
		case len(detect.Adoptable(findings)) > 0:
			return 6
		}
	case "fleet":
		fs := flag.NewFlagSet("fleet", flag.ContinueOnError)
		fs.SetOutput(stderr)
		rosterPath := fs.String("roster", getenv("LACQUER_ROSTER"), "path to the roster file (or $LACQUER_ROSTER)")
		asJSON := fs.Bool("json", false, "emit the sweep as JSON for a later run to diff against")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		// `fleet diff a.json b.json` compares two snapshots and needs no roster:
		// the snapshots already record what was swept, and requiring a roster
		// would make it impossible to diff history after the roster changed.
		if rest := fs.Args(); len(rest) > 0 && rest[0] == "diff" {
			if len(rest) != 3 {
				return fail(stderr, fmt.Errorf("usage: lacquer fleet diff <before.json> <after.json>"))
			}
			before, err := fleet.LoadSnapshot(rest[1])
			if err != nil {
				return fail(stderr, err)
			}
			after, err := fleet.LoadSnapshot(rest[2])
			if err != nil {
				return fail(stderr, err)
			}
			changes := fleet.Diff(before, after)
			fleet.FormatDiff(stdout, changes)
			if fleet.Regressions(changes) > 0 {
				return 4
			}
			return 0
		}
		// The sweep compares projects against a lacquer, so it needs one; the
		// diff above does not, and requiring a checkout to read two files would
		// make history un-diffable from anywhere but a lacquer clone.
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		if *rosterPath == "" {
			return fail(stderr, fmt.Errorf("fleet needs a roster: pass --roster <path> or set LACQUER_ROSTER"))
		}
		roster, err := fleet.LoadRoster(*rosterPath)
		if err != nil {
			return fail(stderr, err)
		}
		reports := fleet.Run(lacquerRoot, roster, time.Now())
		if *asJSON {
			if err := fleet.JSON(stdout, reports); err != nil {
				return fail(stderr, err)
			}
		} else {
			fleet.Text(stdout, reports)
		}
		// Exit 4 when any project would fail its own audit. One code, not the
		// per-project 3/4/6 — a sweep's caller wants "is anything wrong", and
		// the report already says which project and why. Mapping four codes
		// onto one summary would lose information, not add it.
		for _, r := range reports {
			if r.Blocking() {
				return 4
			}
		}
	case "console":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("console", flag.ContinueOnError)
		fs.SetOutput(stderr)
		rosterPath := fs.String("roster", getenv("LACQUER_ROSTER"), "path to the roster file (or $LACQUER_ROSTER)")
		rolesPath := fs.String("roles", getenv("LACQUER_ROLES"), "path to the roles file (or $LACQUER_ROLES) — dispatch-role/watch only")
		sessionsPath := fs.String("sessions", getenv("LACQUER_SESSIONS"), "path to the sessions file (or $LACQUER_SESSIONS) — enables tracking for `watch`")
		mode := fs.String("mode", "", "dispatch target: bg (worktree-isolated background agent) or tmux (interactive, edits the checkout)")
		dryRun := fs.Bool("dry-run", false, "with dispatch/dispatch-role/watch --relaunch: print the command without starting anything")
		relaunch := fs.Bool("relaunch", false, "with watch: re-dispatch every session found dead")
		live := fs.Bool("live", false, "with watch: keep refreshing in place every --interval until Ctrl-C, instead of checking once")
		interval := fs.Duration("interval", 2*time.Second, "with watch --live: refresh interval")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		rest := fs.Args()
		// watch and dispatch-role both need neither --mode nor a project
		// roster's own gate below, so both are checked first: a watch-only or
		// dispatch-role-only invocation should never have to set up config it
		// will not use.
		if len(rest) > 0 && rest[0] == "watch" {
			if *sessionsPath == "" {
				return fail(stderr, fmt.Errorf("watch needs a sessions file: pass --sessions <path> or set LACQUER_SESSIONS"))
			}
			var roster fleet.Roster
			if *rosterPath != "" {
				var err error
				roster, err = fleet.LoadRoster(*rosterPath)
				if err != nil {
					return fail(stderr, err)
				}
			}
			var roles console.RoleRoster
			if *rolesPath != "" {
				var err error
				roles, err = console.LoadRoleRoster(*rolesPath)
				if err != nil {
					return fail(stderr, err)
				}
			}
			if *live {
				// A live loop re-checks the same records over and over, so it
				// must never also relaunch: Watch has no way to update a
				// record's DaemonID in place, so a relaunched session would
				// still read as Failed on the very next tick (its old
				// DaemonID's state file, now dead) and get relaunched again
				// every --interval, forever. --relaunch stays a one-shot,
				// deliberate action; --live stays a read-only dashboard.
				if *relaunch {
					fmt.Fprintln(stderr, "note: --relaunch is ignored with --live (a live loop would relaunch the same failed record every tick)")
				}
				return watchLive(stdout, *sessionsPath, roster, roles, *interval)
			}
			results, err := console.Watch(*sessionsPath, roster, roles, console.Sessions(), *relaunch, *dryRun)
			if err != nil {
				return fail(stderr, err)
			}
			console.WatchText(stdout, results)
			return 0
		}
		// dispatch-role targets a named role (a lead/PM supervising many
		// projects, not editing one), so it needs --roles, never --roster.
		if len(rest) > 0 && rest[0] == "dispatch-role" {
			if len(rest) < 2 {
				return fail(stderr, fmt.Errorf("usage: lacquer console --roles R dispatch-role <name> [\"<task override>\"]"))
			}
			if *rolesPath == "" {
				return fail(stderr, fmt.Errorf("dispatch-role needs a roles file: pass --roles <path> or set LACQUER_ROLES"))
			}
			roles, err := console.LoadRoleRoster(*rolesPath)
			if err != nil {
				return fail(stderr, err)
			}
			taskOverride := strings.Join(rest[2:], " ")
			out, err := console.DispatchRole(roles, console.Sessions(), rest[1], taskOverride, *dryRun)
			fmt.Fprint(stdout, out)
			if err != nil {
				return fail(stderr, err)
			}
			if *sessionsPath != "" && !*dryRun {
				recordRoleDispatch(stderr, *sessionsPath, roles, rest[1], taskOverride, out)
			}
			return 0
		}
		if *rosterPath == "" {
			return fail(stderr, fmt.Errorf("console needs a roster: pass --roster <path> or set LACQUER_ROSTER"))
		}
		roster, err := fleet.LoadRoster(*rosterPath)
		if err != nil {
			return fail(stderr, err)
		}
		if len(rest) > 0 && rest[0] == "dispatch" {
			if len(rest) < 3 {
				return fail(stderr, fmt.Errorf("usage: lacquer console [--roster F] --mode bg|tmux dispatch <project> \"<task>\""))
			}
			if *mode == "" {
				// No default on purpose: bg is worktree-isolated and cannot edit
				// the main checkout, tmux edits it directly. Guessing would
				// silently change where the work lands.
				return fail(stderr, fmt.Errorf("dispatch needs --mode bg or --mode tmux"))
			}
			task := strings.Join(rest[2:], " ")
			out, err := console.Dispatch(roster, console.Sessions(), rest[1], task, console.Mode(*mode), *dryRun)
			fmt.Fprint(stdout, out)
			if err != nil {
				return fail(stderr, err)
			}
			if *sessionsPath != "" && !*dryRun {
				recordProjectDispatch(stderr, *sessionsPath, roster, rest[1], task, console.Mode(*mode), out)
			}
			return 0
		}
		console.Text(stdout, console.Gather(lacquerRoot, roster, time.Now()))
	case "status":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
		if err != nil {
			return fail(stderr, fmt.Errorf("load manifest: %w", err))
		}
		// First line of the first thing anyone runs. `status` is the command
		// people use to ask "is this project fine?", and a retired one must never
		// answer that question with a table of ok/behind rows and nothing else.
		fmt.Fprint(stdout, retire.Notice(cfg))
		rows, err := status.Rows(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, status.Format(rows))
		// Informational here: status reports, audit is the gate.
		reports, err := baselineReports(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		if out := baseline.FormatReports(reports); out != "" {
			fmt.Fprint(stdout, "\n"+out)
		}
		findings, err := detect.Drift(lacquerRoot, projectRoot, cfg)
		if err != nil {
			return fail(stderr, fmt.Errorf("re-detect components: %w", err))
		}
		fmt.Fprint(stdout, formatDrift(findings))
	case "version":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		v, err := version.Read(lacquerRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, v)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		usage(stderr)
		return 2
	}
	return 0
}

// requireLacquerRoot checks that lacquerRoot looks like a lacquer checkout — the
// VERSION file and profiles/ dir both exist — so commands that read them fail
// with an actionable message instead of an opaque "open VERSION: no such file"
// when LACQUER_ROOT is unset and the cwd is not the lacquer repo.
func requireLacquerRoot(lacquerRoot string) error {
	if isFile(filepath.Join(lacquerRoot, "VERSION")) && isDir(filepath.Join(lacquerRoot, "profiles")) {
		return nil
	}
	return fmt.Errorf("%q is not a lacquer checkout (no VERSION file and/or profiles/ dir); "+
		"set LACQUER_ROOT to your lacquer repo, e.g. `LACQUER_ROOT=~/Developer/lacquer lacquer <command>`", lacquerRoot)
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: lacquer <command>")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  init [--stack S]             detect components and write .lacquer.toml")
	fmt.Fprintln(w, "  init --list-stacks           print the archetypes --stack accepts")
	fmt.Fprintln(w, "  onboard --org O [--no-repo]  init, then create a private GitHub repo")
	fmt.Fprintln(w, "  adopt                        record stacks that appeared since init into .lacquer.toml")
	fmt.Fprintln(w, "  sync [--force] [--fix]       render lacquer content into the project")
	fmt.Fprintln(w, "  skills                       install [project].skills via the `skills` CLI (vercel-labs/skills)")
	fmt.Fprintln(w, "  plugins                      install core/bootstrap/plugins.toml via `claude plugin` (machine-level)")
	fmt.Fprintln(w, "  doctor [--profile P]         prove each check can fail (exit 5 if one cannot); --profile")
	fmt.Fprintln(w, "                               limits it to one stack's checks, for a runner that has only that toolchain")
	fmt.Fprintln(w, "  fix                          run the profiles' autofixers (formatters, lint --fix) over the project")
	fmt.Fprintln(w, "  status                       show each region's stamped vs latest version")
	fmt.Fprintln(w, "  audit                        classify project drift and check the project baseline")
	fmt.Fprintln(w, "                               (exit 3 if sync would clobber a local change; exit 4 on a baseline")
	fmt.Fprintln(w, "                               violation or an expired [project].exclude; exit 6 if a stack on")
	fmt.Fprintln(w, "                               disk is undeclared — see `adopt`)")
	fmt.Fprintln(w, "  fleet --roster F [--json]    audit every project in a roster (exit 4 if any would fail its own")
	fmt.Fprintln(w, "                               audit); --json emits a snapshot for a later run to diff against")
	fmt.Fprintln(w, "  fleet diff A.json B.json     what changed between two snapshots (exit 4 on a regression)")
	fmt.Fprintln(w, "  console --roster F           one screen: fleet truth + live sessions + open PRs")
	fmt.Fprintln(w, "  console ... --mode bg|tmux dispatch <project> \"<task>\"")
	fmt.Fprintln(w, "                               start work on one project (bg = isolated worktree; tmux = the checkout)")
	fmt.Fprintln(w, "  console --roles R dispatch-role <name> [\"<task override>\"]")
	fmt.Fprintln(w, "                               start (or re-attach) a named role — a lead/PM supervising many")
	fmt.Fprintln(w, "                               projects, not editing one; mode and task come from the roles file")
	fmt.Fprintln(w, "  console --sessions S [--roster F] [--roles R] watch [--relaunch] [--live] [--interval D]")
	fmt.Fprintln(w, "                               check every recorded dispatch's liveness; --relaunch re-dispatches")
	fmt.Fprintln(w, "                               each one found dead (Blocked and Missing are reported, not")
	fmt.Fprintln(w, "                               auto-relaunched). --live keeps redrawing every --interval (default")
	fmt.Fprintln(w, "                               2s) instead of checking once, until Ctrl-C; ignores --relaunch.")
	fmt.Fprintln(w, "                               --sessions on dispatch/dispatch-role enables recording; nothing")
	fmt.Fprintln(w, "                               is tracked unless you pass it")
	fmt.Fprintln(w, "  version                      print the lacquer version")
	fmt.Fprintln(w, "  help, --help, -h             show this help")
	fmt.Fprintln(w, "env: LACQUER_ROOT (path to the lacquer checkout, default '.')")
}

// baselineReports loads the project manifest and checks every component against
// its profile's asserted baseline. Shared by `audit` (which gates on it) and
// `status` (which only reports it).
func baselineReports(lacquerRoot, projectRoot string) ([]baseline.Report, error) {
	cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	return baseline.Run(lacquerRoot, projectRoot, cfg.BaselineTargets(), cfg.Baseline.Relax, time.Now())
}

// listStacks prints every archetype the lacquer ships, for `init --list-stacks`.
func listStacks(lacquerRoot string, stdout, stderr io.Writer) int {
	all, err := archetype.All(lacquerRoot)
	if err != nil {
		return fail(stderr, err)
	}
	if len(all) == 0 {
		fmt.Fprintln(stdout, "this lacquer ships no archetypes (nothing in archetypes/)")
		return 0
	}
	fmt.Fprintln(stdout, "stacks (use with `lacquer init --stack <name>`):")
	for _, a := range all {
		fmt.Fprintf(stdout, "  %-18s %s\n", a.Name, a.Description)
		for _, c := range a.Components {
			fmt.Fprintf(stdout, "  %-18s   %s -> %s\n", "", c.Path, strings.Join(c.Profiles, ", "))
		}
	}
	return 0
}

// formatDrift renders re-detection findings for `audit` and `status`, or "" when
// the manifest already accounts for everything on disk.
//
// The two halves read differently on purpose. An adoptable finding is the
// project's to fix and gates CI; an unsupported one is the LACQUER's gap, so it
// gates nothing — but it is still printed on every run, forever, because the
// alternative (record it once with an empty profile list and fall silent) is
// precisely how a repo's Swift went a month with no hooks, no CI, and no
// complaint from anything.
func formatDrift(findings []detect.Finding) string {
	var b strings.Builder
	if adoptable := detect.Adoptable(findings); len(adoptable) > 0 {
		b.WriteString("\nstacks on disk that .lacquer.toml does not declare:\n")
		for _, f := range adoptable {
			fmt.Fprintf(&b, "  %s -> %s\n", f.Path, f.Profile)
		}
		b.WriteString("run `lacquer adopt` to record them, or add the path to [project].exclude to keep it unmanaged.\n")
	}
	if unsupported := detect.Unsupported(findings); len(unsupported) > 0 {
		b.WriteString("\nstacks on disk that no lacquer profile covers (nothing gates them):\n")
		for _, f := range unsupported {
			fmt.Fprintf(&b, "  %s -> %s\n", f.Path, f.Profile)
		}
		b.WriteString("this is a gap in the lacquer, not in the project — add profiles/<name>/ to close it.\n")
	}
	return b.String()
}

// profileList collects a repeatable --profile flag.
type profileList []string

func (p *profileList) String() string     { return strings.Join(*p, ",") }
func (p *profileList) Set(v string) error { *p = append(*p, v); return nil }

func fail(w io.Writer, err error) int {
	fmt.Fprintln(w, "error:", err)
	return 1
}

// watchLive redraws `watch`'s one-shot output every interval until the
// terminal sends interrupt/terminate, instead of a single check-and-exit.
// Modeled on tmuxwatch's live tmux-session dashboard (evaluated this session
// for ideas worth borrowing) — that tool's core liveness logic is weaker than
// Check's own (no state-file read, no auto-relaunch), but its always-current
// view is something a one-shot `watch` lacks: an operator otherwise has to
// keep re-running the same command to see a Blocked session clear.
//
// Deliberately re-reads the sessions file every tick (not just the roster/
// roles, which do not change mid-loop) so a dispatch made in another terminal
// while this is running shows up without restarting the loop.
func watchLive(w io.Writer, sessionsPath string, roster fleet.Roster, roles console.RoleRoster, interval time.Duration) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	const clearScreen = "\033[H\033[2J"
	for {
		results, err := console.Watch(sessionsPath, roster, roles, console.Sessions(), false, false)
		fmt.Fprint(w, clearScreen)
		fmt.Fprintf(w, "lacquer console watch --live  (refreshing every %s, Ctrl-C to stop)\n\n", interval)
		if err != nil {
			fmt.Fprintln(w, "error:", err)
		} else {
			console.WatchText(w, results)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(interval):
		}
	}
}

// absDir resolves dir to an absolute path, falling back to the original
// string if that fails for any reason. A relaunch may run from a different
// working directory than the original dispatch (a later invocation, a cron
// sweep) -- a relative Dir recorded against today's CWD would silently point
// somewhere else, or nowhere, by then.
func absDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// recordProjectDispatch appends a Record for a successful project dispatch,
// so a later `watch` pass can find it again. A failure to record is a
// warning, not a hard error: the dispatch itself already succeeded, and the
// operator's actual request should not fail just because tracking couldn't
// be written.
func recordProjectDispatch(stderr io.Writer, sessionsPath string, roster fleet.Roster, name, task string, mode console.Mode, dispatchOut string) {
	for i := range roster.Project {
		if roster.Project[i].Name != name {
			continue
		}
		rec := console.Record{
			Kind:      console.ProjectKind,
			Name:      roster.Project[i].Name,
			Mode:      mode,
			Dir:       absDir(roster.Project[i].Path),
			Task:      task,
			DaemonID:  console.DaemonID(dispatchOut),
			StartedAt: time.Now().UTC(),
		}
		if err := console.AppendRecord(sessionsPath, rec); err != nil {
			fmt.Fprintf(stderr, "warning: could not record session: %v\n", err)
		}
		return
	}
}

// recordRoleDispatch mirrors recordProjectDispatch for a role dispatch.
// taskOverride, when blank, means the role's own declared task was used --
// the record needs the ACTUAL task that ran, not an empty override, so the
// role's declared Task is substituted here rather than left blank.
func recordRoleDispatch(stderr io.Writer, sessionsPath string, roles console.RoleRoster, name, taskOverride, dispatchOut string) {
	for i := range roles.Role {
		if roles.Role[i].Name != name {
			continue
		}
		task := strings.TrimSpace(taskOverride)
		if task == "" {
			task = roles.Role[i].Task
		}
		rec := console.Record{
			Kind:      console.RoleKind,
			Name:      roles.Role[i].Name,
			Mode:      roles.Role[i].Mode,
			Dir:       absDir(roles.Role[i].Dir),
			Task:      task,
			DaemonID:  console.DaemonID(dispatchOut),
			StartedAt: time.Now().UTC(),
		}
		if err := console.AppendRecord(sessionsPath, rec); err != nil {
			fmt.Fprintf(stderr, "warning: could not record session: %v\n", err)
		}
		return
	}
}

// runFixers loads the manifest and runs every declared profile's autofixers.
// Shared by `fix` and `sync --fix`.
func runFixers(lacquerRoot, projectRoot string, stdout, stderr io.Writer) int {
	manifest := filepath.Join(projectRoot, ".lacquer.toml")
	cfg, err := config.Load(manifest)
	if err != nil {
		return fail(stderr, fmt.Errorf("load %s: %w", manifest, err))
	}
	fmt.Fprintln(stdout, "running autofixers:")
	results, err := fixcmd.Run(lacquerRoot, projectRoot, cfg, stdout)
	if err != nil {
		return fail(stderr, err)
	}
	if len(results) == 0 {
		fmt.Fprintln(stdout, "  (no profile in this project ships autofixers)")
	}
	return 0
}
