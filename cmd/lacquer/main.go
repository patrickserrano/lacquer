package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/adoptcmd"
	"github.com/patrickserrano/lacquer/internal/archetype"
	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/fixcmd"
	"github.com/patrickserrano/lacquer/internal/initcmd"
	"github.com/patrickserrano/lacquer/internal/onboardcmd"
	"github.com/patrickserrano/lacquer/internal/pluginbootstrap"
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
		manifest := filepath.Join(projectRoot, ".lacquer.toml")
		cfg, err := config.Load(manifest)
		if err != nil {
			return fail(stderr, fmt.Errorf("load %s: %w", manifest, err))
		}
		fmt.Fprintln(stdout, "proving each check can fail:")
		results, err := doctor.Run(lacquerRoot, projectRoot, cfg, stdout)
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

		// Exit 3 when a project change would be clobbered, so `lacquer audit` is
		// usable as a CI drift gate (documented in usage()). Clobbering takes
		// precedence over a baseline violation when both fire: losing a local
		// change is destructive, a policy violation is not.
		if len(audit.Clobbered(rows)) > 0 {
			return 3
		}
		cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
		if err != nil {
			return fail(stderr, fmt.Errorf("load manifest: %w", err))
		}
		findings, err := detect.Drift(lacquerRoot, projectRoot, cfg)
		if err != nil {
			return fail(stderr, fmt.Errorf("re-detect components: %w", err))
		}
		fmt.Fprint(stdout, formatDrift(findings))

		// Exit 4 on a baseline violation — a distinct code so a CI gate can tell
		// "sync would destroy work" apart from "this project is out of standard".
		if baseline.Blocking(reports) > 0 {
			return 4
		}
		// Exit 6 when the project runs a stack the lacquer manages but the manifest
		// never declared. Distinct from 3/4 because the fix is different in kind:
		// nothing is wrong with the code, the manifest is just out of date with it.
		if len(detect.Adoptable(findings)) > 0 {
			return 6
		}
	case "status":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
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
		cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
		if err != nil {
			return fail(stderr, fmt.Errorf("load manifest: %w", err))
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
	fmt.Fprintln(w, "  doctor                       prove each check can fail (exit 5 if one cannot)")
	fmt.Fprintln(w, "  fix                          run the profiles' autofixers (formatters, lint --fix) over the project")
	fmt.Fprintln(w, "  status                       show each region's stamped vs latest version")
	fmt.Fprintln(w, "  audit                        classify project drift and check the project baseline")
	fmt.Fprintln(w, "                               (exit 3 if sync would clobber a local change; exit 4 on a baseline")
	fmt.Fprintln(w, "                               violation; exit 6 if a stack on disk is undeclared — see `adopt`)")
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

func fail(w io.Writer, err error) int {
	fmt.Fprintln(w, "error:", err)
	return 1
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
