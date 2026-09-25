package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/patrickserrano/lacquer/internal/adoptcmd"
	"github.com/patrickserrano/lacquer/internal/archetype"
	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/console"
	"github.com/patrickserrano/lacquer/internal/depignore"
	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/exclusion"
	"github.com/patrickserrano/lacquer/internal/fixcmd"
	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/hooks"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/initcmd"
	"github.com/patrickserrano/lacquer/internal/onboardcmd"
	"github.com/patrickserrano/lacquer/internal/pluginbootstrap"
	"github.com/patrickserrano/lacquer/internal/protection"
	"github.com/patrickserrano/lacquer/internal/ratchet"
	"github.com/patrickserrano/lacquer/internal/retire"
	"github.com/patrickserrano/lacquer/internal/rootcheck"
	"github.com/patrickserrano/lacquer/internal/shadow"
	"github.com/patrickserrano/lacquer/internal/skillsync"
	"github.com/patrickserrano/lacquer/internal/status"
	syncpkg "github.com/patrickserrano/lacquer/internal/sync"
	"github.com/patrickserrano/lacquer/internal/testtargets"
	"github.com/patrickserrano/lacquer/internal/version"
	"github.com/patrickserrano/lacquer/internal/xcodegendrift"
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
	case "ratchet":
		return runRatchet(args[1:], projectRoot, stdout, stderr)
	case "settings":
		return runSettings(args[1:], stdout, stderr)
	case "init":
		// init reads lacquerRoot to gate detected profiles to those that ship;
		// with it unset (default ".") every profile would be silently dropped.
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
		// Say which checkout this is rendering from, before rendering, and
		// refuse unless it is PROVABLY a pinned release. sync is only ever as
		// current as LACQUER_ROOT, and a stale or unverified root does not fail
		// — left unchecked it reports success and writes whatever the root
		// happened to hold, which is indistinguishable from having had nothing
		// to do. See stampAndVerifyRoot and issue #350.
		root, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr)
		if code != 0 {
			return code
		}

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
		fmt.Fprint(stdout, ratchet.Format(res.Ratchets))
		fmt.Fprintf(stdout, "sync complete: %d regions, %d assets\n", res.Regions, res.Assets)
		if len(res.Replaced) > 0 {
			fmt.Fprintf(stdout, "first sync replaced pre-existing content (no lock baseline):\n  %s\n", strings.Join(res.Replaced, "\n  "))
		}
		// After the success line on purpose: a warning above it reads as part of
		// the preamble and is scrolled past.
		if w := root.Warning(); w != "" {
			fmt.Fprintln(stderr, w)
		}

		// [project].skills is a separate concern from everything sync just wrote:
		// sync stays fully offline and deterministic (the README says so, and its
		// whole test suite depends on that), while installing a third-party skill
		// needs the network. So this never installs anything — it only says, out
		// loud, that there's a step left. Before this, a project could declare a
		// skill in [project].skills and never actually get it: gitignored by name
		// (internal/gitignore's skills() rule), absent on disk, no skills-lock.json,
		// and sync's own output never mentioned it — there was nothing here to
		// notice the gap. Measured on Steps: exactly that state, for `healthkit`.
		if syncManifest, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml")); err != nil {
			fmt.Fprintf(stderr, "warning: could not re-read manifest for [project].skills: %v\n", err)
		} else if entries, err := syncManifest.Project.ParsedSkills(); err != nil {
			fmt.Fprintf(stderr, "warning: [project].skills: %v\n", err)
		} else if len(entries) > 0 {
			missing, err := skillsync.Missing(projectRoot, entries)
			if err != nil {
				fmt.Fprintf(stderr, "warning: [project].skills: %v\n", err)
			} else if len(missing) > 0 {
				fmt.Fprintf(stdout, "\n[project].skills has %d skill(s) missing from skills-lock.json; sync does not install them (it stays offline) — run `lacquer skills` to install:\n", len(missing))
				for _, e := range missing {
					fmt.Fprintf(stdout, "  %s\n", e)
				}
			}
		}

		if *doFix {
			if code := runFixers(lacquerRoot, projectRoot, stdout, stderr); code != 0 {
				return code
			}
		}
		if ratchet.Blocking(res.Ratchets) > 0 {
			return 4
		}
	case "doctor":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		// doctor is exactly the command someone runs to decide whether their
		// checks can be trusted -- it must not itself run against an unverified
		// root and say nothing (issue #350).
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
		probe := ratchetProbe()
		results = append(results, probe)
		mark := "ok"
		if !probe.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(stdout, "  %s  %s\n", mark, probe.Name)
		if !probe.OK {
			fmt.Fprintln(stdout, probe.Detail)
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
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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

		// Plugins a local tool materializes, rather than a marketplace serving
		// them. Re-applied every run on purpose: Xcode's plugin path carries its
		// BUILD, so an upgrade moves it and the link stops resolving — fifteen
		// skills and an MCP server silently absent from every session afterwards.
		home, herr := os.UserHomeDir()
		switch {
		case herr != nil:
			fmt.Fprintf(stderr, "provided plugins: cannot resolve home directory: %v\n", herr)
		default:
			for _, l := range pluginbootstrap.ApplyProvided(home, manifest.Provided) {
				switch l.Action {
				case "linked", "relinked":
					fmt.Fprintf(stdout, "%s: %s (%s)", l.Action, l.Name, l.Format)
					if l.Details != "" {
						fmt.Fprintf(stdout, " — %s", l.Details)
					}
					fmt.Fprintln(stdout)
				case "current":
					fmt.Fprintf(stdout, "current: %s (%s)\n", l.Name, l.Format)
				default:
					// Reported, never silent: "I did not link it" and "there was
					// nothing to link" are different answers.
					fmt.Fprintf(stderr, "%s: %s (%s) — %s\n", l.Action, l.Name, l.Format, l.Details)
				}
			}
		}

		if len(res.Failed) > 0 {
			return 1
		}
	case "audit":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		// audit is precisely what someone runs to decide whether a project is
		// safe -- a guard that only covers sync's WRITE path leaves the
		// diagnostic command confidently wrong (issue #350).
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
		}
		cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
		if err != nil {
			return fail(stderr, fmt.Errorf("load manifest: %w", err))
		}
		flags := flag.NewFlagSet("audit", flag.ContinueOnError)
		flags.SetOutput(stderr)
		xcodegenOnly := flags.Bool("xcodegen-only", false, "report XcodeGen build-setting drift only (never gates)")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if flags.NArg() != 0 {
			return fail(stderr, fmt.Errorf("unexpected audit arguments: %v", flags.Args()))
		}
		fmt.Fprint(stdout, xcodegendrift.Format(xcodegendrift.Check(projectRoot, cfg.BaselineTargets())))
		if *xcodegenOnly {
			return 0
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
		ratchets, err := ratchet.Check(projectRoot, cfg)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, ratchet.Format(ratchets))
		if b, err := ratchet.Read(projectRoot); err != nil {
			return fail(stderr, err)
		} else if b == nil {
			fmt.Fprintln(stdout, "ratchet: no baseline; run lacquer ratchet --write and commit the file")
		}

		reports, err := baselineReports(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		if out := baseline.FormatReports(reports); out != "" {
			fmt.Fprint(stdout, "\n"+out)
		}

		// Reported, deliberately not gated. A project whose hooks are not
		// installed is not DRIFTED -- every managed file is exactly right, which
		// is the whole trouble: the config says the gates are active and nothing
		// contradicts it. Failing the audit over it would also fail every fresh
		// clone, where not-yet-installed is the normal state rather than a
		// finding.
		fmt.Fprint(stdout, hooks.Format(hooks.Check(projectRoot)))
		// A second workflow that archives, signs and uploads inherits none of the
		// hardening on the managed release path — and may well be the one that
		// actually ships. See internal/shadow.
		fmt.Fprint(stdout, shadow.Format(shadow.Check(projectRoot)))
		// Declared [[product]].secrets that no workflow here will write. See
		// internal/audit/inert.go.
		fmt.Fprint(stdout, audit.FormatInertSecrets(audit.InertSecretDeclarations(projectRoot, cfg)))
		// Workflow steps that kill or wipe simulators for every job on a shared
		// runner. See internal/audit/machinewide.go.
		fmt.Fprint(stdout, audit.FormatMachineWide(audit.MachineWideSteps(projectRoot)))
		// A committed Package.resolved that the declared package requirements
		// contradict: the build re-resolves and ships the requirement, so the
		// lockfile lies. See internal/audit/packagepins.go.
		fmt.Fprint(stdout, audit.FormatPackagePins(audit.PackagePinFindings(projectRoot)))
		// Rendered agents, skills and commands Claude Code would skip silently.
		// Report-only. See internal/audit/plugindefs.go.
		fmt.Fprint(stdout, audit.FormatPluginDefs(audit.PluginDefinitions(projectRoot)))

		// Both directions of the test-selector comparison. Reported, not gated,
		// for the same reason as the hooks check: a project whose widget suite
		// nobody wired is not DRIFTED -- every managed file is exactly right,
		// which is why this went unnoticed three times in one repo.
		// [[project.not_run_in_ci]]: suites deliberately run in no CI job. An
		// expired one gates below, with the other expired exemptions.
		var notRun []testtargets.NotRun
		for _, n := range cfg.Project.NotRunInCI {
			notRun = append(notRun, testtargets.NotRun{Target: n.Target, Reason: n.Reason, Until: n.Until})
		}
		notRunExpired := 0
		if cfg.Project.Xcodeproj != "" {
			pbx := filepath.Join(projectRoot, cfg.Project.Xcodeproj, "project.pbxproj")
			declared, read, err := testtargets.Parse(pbx)
			if err != nil {
				return fail(stderr, err)
			}
			// Only compare when the project was actually READ. A manifest may name
			// an .xcodeproj that does not exist yet (multimeter says so in its own
			// comment), and reporting every selector as naming a missing target
			// then would be the check confusing "I could not look" with "it is not
			// there" -- the exact failure it exists to catch.
			if !read {
				declared = nil
			}
			var selectors []string
			for _, p := range cfg.Products() {
				selectors = append(selectors, p.TestSelectors()...)
				// The watch leg's selector too. It is a SEPARATE list on Product
				// because the two are run by different jobs and neither can run
				// the other's -- the iOS leg's "Verify Test Selectors Matched"
				// step would fail on a watch bundle it was never asked to run.
				// The audit is the one place that wants the union: a target the
				// rendered watch job runs on every pull request is covered, and
				// reporting it as uncovered is the false positive this whole
				// feature exists to remove rather than re-create.
				selectors = append(selectors, p.WatchTestSelectors()...)
			}
			if read {
				// [[project.covered_elsewhere]], verified against the repository
				// rather than believed. The `managed` set is what the lacquer
				// would ship here with nothing opted out, so a declaration
				// pointing at a file sync overwrites is refused; it is left empty
				// when the plan cannot be resolved, which skips that one check
				// instead of guessing.
				managed := map[string]bool{}
				if dests, err := assets.Shipped(lacquerRoot, cfg); err == nil {
					for _, d := range dests {
						managed[d] = true
					}
				}
				var decls []testtargets.Declaration
				for _, c := range cfg.Project.CoveredElsewhere {
					decls = append(decls, testtargets.Declaration{
						Target: c.Target, Workflow: c.Workflow, Reason: c.Reason,
					})
				}
				claims := testtargets.Verify(projectRoot, decls, declared, managed)
				report := testtargets.Apply(testtargets.Compare(declared, selectors), claims)
				report = testtargets.Deliberate(report, declared, notRun, time.Now())
				fmt.Fprint(stdout, testtargets.Format(report))
				notRunExpired = testtargets.Blocking(report)
			} else if len(notRun) > 0 {
				// [[project.not_run_in_ci]] carries a date, and a date must not
				// stop being enforced because the project could not be read:
				// that would be an expiry that silently never fires. Nothing is
				// compared (see above), so every declaration is read against an
				// unreadable project -- never stale, never applied to anything,
				// but expired if its term ran out.
				unread := []testtargets.Target{{Unread: cfg.Project.Xcodeproj + " could not be read"}}
				report := testtargets.Deliberate(testtargets.Report{}, unread, notRun, time.Now())
				fmt.Fprint(stdout, testtargets.Format(report))
				notRunExpired = testtargets.Blocking(report)
			}
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
		// report while sitting in the repository still running.
		//
		// It USED to be reported and deliberately excluded from the exit codes
		// below, on the theory that an orphan is a leftover file, not a broken
		// project, and gating on something that endangers nothing teaches people
		// this output is noise. That theory is why ios-claude.yml,
		// ios-dependency-audit.yml and ios-quality-review.yml survived as orphans
		// in 13 of 14 fleet repos for ten releases with a detector that saw them
		// correctly every single run (issue #354) — two of the three keep a
		// `schedule:` trigger, so they were not inert, they were running
		// unattended CI on the fleet's dime while every `audit` exited 0. A
		// detector whose only output is prose is an optional finding. See exit 4
		// below.
		orphans, err := audit.Orphans(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, fmt.Errorf("resolve orphans: %w", err))
		}
		// Look up what still references each orphan. This is the difference
		// between "delete it" and "that runs in your CI" — see
		// audit.FormatOrphansWithRefs for why the unannotated report was worth
		// changing.
		refs := map[string][]audit.Reference{}
		for _, o := range orphans {
			if r := audit.References(projectRoot, o); len(r) > 0 {
				refs[o.Key] = r
			}
		}
		fmt.Fprint(stdout, audit.FormatOrphansWithRefs(orphans, refs))

		// Still-shipped scripts with no caller or rendered agent instructions
		// remain informational: unlike orphans they do not leave retired code
		// executing. Documented manual entry points are accounted for by the
		// detector rather than permanently appearing as false positives.
		uncalled, err := audit.UncalledScripts(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, fmt.Errorf("resolve script callers: %w", err))
		}
		fmt.Fprint(stdout, audit.FormatUncalledScripts(uncalled))

		return (audit.Gate{
			Clobbered: len(audit.Clobbered(rows)), Baseline: baseline.Blocking(reports) + ratchet.Blocking(ratchets),
			Exclusions: exclusion.Blocking(exclusions), DepIgnores: depignore.Blocking(ignores),
			NotRunInCI: notRunExpired, Orphans: len(orphans), Undeclared: len(detect.Adoptable(findings)),
		}).ExitCode()

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
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
	case "protection":
		// The one command that reaches the GitHub API to answer a question, and
		// it is separate from `audit` on purpose — see internal/protection for
		// the full argument. In short: branch protection is in no file, reading
		// it needs ADMIN on the repo (which a workflow's GITHUB_TOKEN does not
		// have), and a repository cannot usefully judge a setting that decides
		// whether its own verdict can be ignored.
		//
		// No requireLacquerRoot, and no rootcheck for the same reason: this
		// renders nothing and compares against nothing the lacquer ships, so
		// demanding a lacquer checkout would stop an operator running it from
		// the repo they are looking at.
		fs := flag.NewFlagSet("protection", flag.ContinueOnError)
		fs.SetOutput(stderr)
		repo := fs.String("repo", "", "repository as owner/name (default: this checkout's origin remote)")
		branch := fs.String("branch", "", "branch to check (default: the repository's default branch)")
		rosterPath := fs.String("roster", "", "check every project in a roster instead of this one")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		var reports []protection.Report
		if *rosterPath != "" {
			roster, err := fleet.LoadRoster(*rosterPath)
			if err != nil {
				return fail(stderr, err)
			}
			for _, e := range roster.Project {
				slug := e.Repo
				if slug == "" {
					// The roster's `repo` is optional, so fall back to the
					// checkout's own origin. A project whose slug cannot be
					// resolved at all becomes an UNCHECKED line rather than a
					// skipped one — silently omitting it would make a roster of
					// seventeen report seventeen clean repositories while having
					// looked at sixteen.
					s, err := protection.Slug(e.Path)
					if err != nil {
						reports = append(reports, protection.Unreachable(e.Name, err))
						continue
					}
					slug = s
				}
				reports = append(reports, protection.Check(e.Path, slug, *branch))
			}
		} else {
			slug := *repo
			here, hereErr := protection.Slug(projectRoot)
			switch {
			case slug == "" && hereErr != nil:
				return fail(stderr, hereErr)
			case slug == "":
				slug = here
			case hereErr == nil && !strings.EqualFold(slug, here):
				// Refused rather than reported. The check needs BOTH sides —
				// what the branch requires AND what this checkout can post — so
				// naming one repository while standing in another produces a
				// comparison between two different projects that still renders
				// as a verdict. That is the defect this command hunts, spelled
				// with its own output. Use --roster to sweep repos you are not
				// standing in.
				return fail(stderr, fmt.Errorf("--repo %s, but this checkout's origin is %s; the required contexts and the posted ones would come from different repositories (run from that checkout, or use --roster)", slug, here))
			}
			reports = append(reports, protection.Check(projectRoot, slug, *branch))
		}
		fmt.Fprint(stdout, protection.Format(reports))

		// Two exit codes, and the second one is the point. Exit 4 is a finding,
		// matching every other "this is out of standard" result in this tool.
		// Exit 7 is "a repository could not be checked", which is NOT a finding
		// and must never be a pass: a logged-out `gh` or a 403 from an account
		// that cannot have branch protection at all would otherwise exit 0 and
		// report a fleet as verified that nobody looked at. A finding outranks
		// an unchecked repo because it is the stronger, already-proven statement.
		switch {
		case protection.Blocking(reports) > 0:
			return 4
		case protection.Unchecked(reports) > 0:
			return 7
		}
	case "wait":
		// Reaches the GitHub API through gh and reads nothing from a lacquer
		// checkout, so — like protection — no requireLacquerRoot.
		return waitCmd(args[1:], getenv, stdout, stderr)
	case "ci-round":
		// Reaches the GitHub API through gh and reads only the project's own
		// manifest (from a git ref), so — like wait — no requireLacquerRoot.
		return ciRoundCmd(args[1:], getenv, stdout, stderr)
	case "console":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
		}
		fs := flag.NewFlagSet("console", flag.ContinueOnError)
		fs.SetOutput(stderr)
		rosterPath := fs.String("roster", getenv("LACQUER_ROSTER"), "path to the roster file (or $LACQUER_ROSTER)")
		rolesPath := fs.String("roles", getenv("LACQUER_ROLES"), "path to the roles file (or $LACQUER_ROLES) — dispatch-role/watch only")
		sessionsPath := fs.String("sessions", getenv("LACQUER_SESSIONS"), "optional path to the dispatch-record file (or $LACQUER_SESSIONS) — records launches so `watch --relaunch` and `kill` can act on them; live sessions come from `claude agents --json` and need no file")
		inboxFlag := fs.String("inbox", "", "path to the inbox file — decisions awaiting the operator and finished work, shown as ACTION/UNREAD (default: $LACQUER_INBOX, else $XDG_STATE_HOME/lacquer/inbox.jsonl, else ~/.local/state/lacquer/inbox.jsonl)")
		mode := fs.String("mode", "", "dispatch target: bg (background agent in a new git worktree and branch under <repo>/.claude/worktrees/) or tmux (detached tmux session in the checkout itself, edits it)")
		model := fs.String("model", "", "with dispatch/dispatch-role: Claude model (IC default: roster ic_model or sonnet; role default: role model or inherited)")
		effort := fs.String("effort", "", "with dispatch/dispatch-role: Claude effort (default: roster ic_effort or role effort, otherwise inherited)")
		dryRun := fs.Bool("dry-run", false, "with dispatch/dispatch-role/watch --relaunch: print the command without starting anything")
		relaunch := fs.Bool("relaunch", false, "with watch: re-dispatch every session found dead")
		live := fs.Bool("live", false, "with watch: keep refreshing in place every --interval until Ctrl-C, instead of checking once")
		interval := fs.Duration("interval", 2*time.Second, "with watch --live: refresh interval")
		force := fs.Bool("force", false, "with kill: kill even a session Check reports Alive")
		worktree := fs.String("worktree", "", "with dispatch/dispatch-role: run in this existing, registered worktree of the project's repository instead of creating one (bg) or using the checkout (tmux) -- for a worktree a PM assigned")
		branch := fs.String("branch", "", "with bg dispatch/dispatch-role: name the branch of the worktree bg creates (directory derived from it) instead of dispatch/<id>; refused if it exists, and in tmux mode")
		entryType := fs.String("type", "", `with inbox add: "action" (needs a human decision) or "unread" (finished, unacknowledged)`)
		entryTitle := fs.String("title", "", "with inbox add: one-line summary (required)")
		entryBody := fs.String("body", "", "with inbox add: optional detail")
		entryRef := fs.String("ref", "", `with inbox add: optional reference, e.g. "#374" or a URL`)
		entryProject := fs.String("project", "", "with inbox add: optional project/roster name")
		all := fs.Bool("all", false, "with inbox list: include resolved entries")
		rest, err := parseConsoleArgs(fs, args[1:])
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fs.Usage()
				return 2
			}
			// A task word that looks like a flag is the one case with a
			// remedy worth naming at the point of failure.
			if len(rest) > 0 && (rest[0] == "dispatch" || rest[0] == "dispatch-role") {
				err = fmt.Errorf("%w; a word of the task that starts with - goes after --, which ends the flags: lacquer console ... %s <name> -- <task>", err, rest[0])
			}
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		sub, err := consoleSubcommand(rest)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		if err := checkConsoleFlagScope(fs, sub); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		inboxPath, inboxIsDefault, err := inbox.Path(*inboxFlag, getenv)
		if err != nil {
			return fail(stderr, err)
		}
		place := console.Placement{Worktree: *worktree, Branch: *branch}
		// watch and dispatch-role both need neither --mode nor a project
		// roster's own gate below, so both are checked first: a watch-only or
		// dispatch-role-only invocation should never have to set up config it
		// will not use.
		if len(rest) > 0 && rest[0] == "watch" {
			if *sessionsPath == "" {
				// Without dispatch records there is nothing to check for
				// liveness or relaunch, but the live sessions still answer
				// "what is running", so show them.
				if *relaunch || *live {
					return fail(stderr, fmt.Errorf("watch --relaunch and --live work from dispatch records (the tmux pane or daemon id, the task and the worktree a relaunch needs), which `claude agents --json` does not report: pass --sessions <path> or set LACQUER_SESSIONS. Plain `watch` lists live sessions without it"))
				}
				res := console.Gather(console.Options{Now: time.Now()})
				console.SessionsText(stdout, res)
				if res.SessionsErr != "" {
					return 1
				}
				return 0
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
				// A live loop is a read-only dashboard: it redraws every
				// --interval (2s by default), and starting sessions and
				// rewriting the sessions file on that cadence is not what
				// anyone watching a screen asked for. Watch does now replace a
				// relaunched record, so the old every-tick relaunch loop is
				// gone, but --relaunch stays a one-shot, deliberate action.
				if *relaunch {
					fmt.Fprintln(stderr, "note: --relaunch is ignored with --live (a live dashboard only reads; run `watch --relaunch` once to relaunch)")
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
		if len(rest) > 0 && rest[0] == "kill" {
			if len(rest) < 2 {
				return fail(stderr, fmt.Errorf("usage: lacquer console --sessions S kill <name-or-daemon-id> [--force]"))
			}
			if *sessionsPath == "" {
				return fail(stderr, fmt.Errorf("kill needs the dispatch record (the daemon id or tmux session it stops), which `claude agents --json` does not report: pass --sessions <path> or set LACQUER_SESSIONS"))
			}
			records, err := console.ReadRecords(*sessionsPath)
			if err != nil {
				return fail(stderr, err)
			}
			target := rest[1]
			var matches []console.Record
			for _, r := range records {
				if r.Name == target || r.DaemonID == target {
					matches = append(matches, r)
				}
			}
			switch len(matches) {
			case 0:
				return fail(stderr, fmt.Errorf("no recorded session matches %q", target))
			case 1:
				note, err := console.Kill(*sessionsPath, matches[0], *force)
				if note != "" {
					fmt.Fprintln(stderr, note)
				}
				if err != nil {
					return fail(stderr, err)
				}
				fmt.Fprintf(stdout, "killed %s (%s, %s)\n", matches[0].Name, matches[0].Kind, matches[0].Mode)
				return 0
			default:
				fmt.Fprintf(stderr, "%q matches %d recorded sessions — name a daemon id instead:\n", target, len(matches))
				for _, m := range matches {
					fmt.Fprintf(stderr, "  %s  daemonId=%s  started=%s\n", m.Name, m.DaemonID, m.StartedAt.Format(time.RFC3339))
				}
				return 1
			}
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
			launch, err := console.DispatchRoleConfigured(roles, console.Sessions(), rest[1], taskOverride, *dryRun, place, console.ModelOptions{Model: *model, Effort: *effort})
			return finishDispatch(stdout, stderr, *sessionsPath, launch, err)
		}
		// inbox needs neither --mode nor a project roster, same reasoning as
		// watch/dispatch-role above: it operates entirely on the inbox file.
		if len(rest) > 0 && rest[0] == "inbox" {
			if len(rest) < 2 {
				return fail(stderr, fmt.Errorf("usage: lacquer console --inbox F inbox <add|resolve|list> ..."))
			}
			switch rest[1] {
			case "add":
				return runInboxAdd(inboxPath, *entryType, inbox.Entry{Title: *entryTitle, Body: *entryBody, Ref: *entryRef, Project: *entryProject}, stdout, stderr)
			case "resolve":
				return runInboxResolve(inboxPath, rest[2:], stdout, stderr)
			default: // list; consoleSubcommand refused anything else
				return runInboxList(inboxPath, inboxIsDefault, *all, stdout, stderr)
			}
		}
		var roster fleet.Roster
		if *rosterPath != "" {
			roster, err = fleet.LoadRoster(*rosterPath)
			if err != nil {
				return fail(stderr, err)
			}
		}
		if len(rest) > 0 && rest[0] == "dispatch" {
			if *rosterPath == "" {
				return fail(stderr, fmt.Errorf("dispatch needs a roster to find the project: pass --roster <path> or set LACQUER_ROSTER"))
			}
			if len(rest) < 3 {
				return fail(stderr, fmt.Errorf("usage: lacquer console [--roster F] --mode bg|tmux dispatch <project> \"<task>\""))
			}
			if *mode == "" {
				// No default on purpose: bg runs in a worktree of its own and
				// does not edit the main checkout, tmux edits it directly.
				// Guessing would silently change where the work lands.
				return fail(stderr, fmt.Errorf("dispatch needs --mode bg or --mode tmux"))
			}
			task := strings.Join(rest[2:], " ")
			launch, err := console.DispatchConfigured(roster, console.Sessions(), rest[1], task, console.Mode(*mode), *dryRun, place, console.ModelOptions{Model: *model, Effort: *effort})
			return finishDispatch(stdout, stderr, *sessionsPath, launch, err)
		}
		console.Text(stdout, console.Gather(console.Options{LacquerRoot: lacquerRoot, Roster: roster, Now: time.Now(), InboxPath: inboxPath, InboxDefault: inboxIsDefault, MergeRun: ciwait.GH}))
		if *sessionsPath != "" {
			results, err := console.Watch(*sessionsPath, roster, console.RoleRoster{}, nil, false, false)
			if err != nil {
				return fail(stderr, err)
			}
			fmt.Fprintln(stdout, "\nRecorded sessions (requested settings):")
			console.WatchText(stdout, results)
		}
	case "status":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		// status is the FIRST thing anyone runs to ask "is this project fine?"
		// -- see the field incident recorded on stampAndVerifyRoot and issue
		// #350: this command answered that question off a stale root, twice,
		// with a clean table and exit 0.
		if _, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr); code != 0 {
			return code
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
		for _, row := range rows {
			if !row.Found || !row.Behind {
				continue
			}
			content, _, err := audit.Classify(lacquerRoot, projectRoot)
			if err != nil {
				return fail(stderr, fmt.Errorf("compare managed content: %w", err))
			}
			matches := true
			for _, unit := range content {
				if unit.Status != audit.OK {
					matches = false
					break
				}
			}
			if matches {
				fmt.Fprintln(stdout, "Only version stamps are behind; managed content matches this lacquer root.")
			} else {
				fmt.Fprintln(stdout, "Version stamps are behind and managed content differs; run `lacquer audit` for content drift.")
			}
			break
		}

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
		root, code := stampAndVerifyRoot(lacquerRoot, getenv, stderr)
		if code != 0 {
			return code
		}
		v, err := version.Read(lacquerRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "%s (content) / built from %s\nroot: %s\n", v, root.BuiltVersion, root.Root)
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

// stampAndVerifyRoot inspects lacquerRoot, prints its resolved provenance to
// stderr on every call, preserving machine-readable stdout. It refuses unless the
// root is PROVABLY a pinned release: detached HEAD, sitting exactly on a tag
// matching VERSION, clean tree. Every command in this file that reads shipped
// content from lacquerRoot calls this immediately after requireLacquerRoot.
//
// The provenance line always carries the root PATH now, not just a version
// and a ref — issue #350's field incident: "lacquer: 1.35.0 from HEAD @
// 2775df3" was read by a session as naming provenance when it named nothing,
// because the one variable that differs between a safe run and a dangerous
// one is WHICH DIRECTORY LACQUER_ROOT points at, and that used to be the one
// thing this line omitted. See rootcheck.State.Describe.
//
// "I could not verify this root" and "this root is fine" must never share an
// exit code (issue #350) — so a root whose state cannot be determined at all
// (not a git checkout, git unavailable, detached at an untagged commit)
// refuses exactly like a confirmed branch checkout or dirty tree, never like
// success. That symmetry is the entire fix: a guard that only refuses a
// DETECTED bad state reproduces the original bug one level up, because the
// absence of detection would then read as safety — the same defect family as
// a skipped CI check satisfying a required one.
//
// LACQUER_ALLOW_UNVERIFIED_ROOT overrides the refusal for deliberate
// development — building lacquer from a feature worktree to test an
// unreleased change is normal here, the same shape LACQUER_ALLOW_STALE_BINARY
// (see the sync case above) already exists to permit. Unlike a silent bypass,
// it prints a loud warning to stderr on EVERY invocation it is honored, not
// just the first: a scrollback line from ten minutes ago must never be
// mistaken for tonight's verified run, which is exactly how this bug hid for
// three separate sessions in the first place.
func stampAndVerifyRoot(lacquerRoot string, getenv func(string) string, stderr io.Writer) (rootcheck.State, int) {
	root := rootcheck.Inspect(lacquerRoot, false)
	fmt.Fprintln(stderr, root.Describe())

	err := root.Verify()
	if err == nil {
		return root, 0
	}
	if getenv("LACQUER_ALLOW_UNVERIFIED_ROOT") != "" {
		fmt.Fprintf(stderr, "** LACQUER_ALLOW_UNVERIFIED_ROOT is set: %v **\n"+
			"** output below is UNREVIEWED — this root is not a verified pinned release **\n", err)
		return root, 0
	}
	fmt.Fprintf(stderr, "refusing to run: %v\n"+
		"\"I could not verify this root\" and \"this root is fine\" must not look the same (issue #350).\n"+
		"Point LACQUER_ROOT at a pinned release checkout, or set LACQUER_ALLOW_UNVERIFIED_ROOT=1 if you are\n"+
		"deliberately developing against a feature worktree — every invocation will then warn loudly that\n"+
		"its output is unreviewed.\n", err)
	return root, 1
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
	fmt.Fprintln(w, "  settings [--project P] [--target T] [--configuration C] [--json] [--xcode [--compare]] [SETTING...]")
	fmt.Fprintln(w, "  init [--stack S]             detect components and write .lacquer.toml")
	fmt.Fprintln(w, "  init --list-stacks           print the archetypes --stack accepts")
	fmt.Fprintln(w, "  onboard --org O [--no-repo]  init, then create a private GitHub repo")
	fmt.Fprintln(w, "  adopt                        record stacks that appeared since init into .lacquer.toml")
	fmt.Fprintln(w, "  sync [--force] [--fix]       render lacquer content into the project")
	fmt.Fprintln(w, "  skills                       install [project].skills via the `skills` CLI (vercel-labs/skills)")
	fmt.Fprintln(w, "  plugins                      install core/bootstrap/plugins.toml via `claude plugin` (machine-level)")
	fmt.Fprintln(w, "  ratchet [--write | --loosen METRIC --reason TEXT]  measure or update metric ceilings")
	fmt.Fprintln(w, "  doctor [--profile P]         prove each check can fail (exit 5 if one cannot); --profile")
	fmt.Fprintln(w, "                               limits it to one stack's checks, for a runner that has only that toolchain")
	fmt.Fprintln(w, "  fix                          run the profiles' autofixers (formatters, lint --fix) over the project")
	fmt.Fprintln(w, "  status                       show each region's stamped vs latest version")
	fmt.Fprintln(w, "  audit                        classify project drift and check the project baseline")
	fmt.Fprintln(w, "                               (exit 3 if sync would clobber a local change; exit 4 on a baseline")
	fmt.Fprintln(w, "                               violation, an expired [project].exclude, or a file the lacquer")
	fmt.Fprintln(w, "                               no longer ships still sitting in the project; exit 6 if a stack")
	fmt.Fprintln(w, "                               on disk is undeclared — see `adopt`)")
	fmt.Fprintln(w, "    --xcodegen-only            report regeneration setting drift only; no drift/baseline gates")
	fmt.Fprintln(w, "  fleet --roster F [--json]    audit every project in a roster (exit 4 if any would fail its own")
	fmt.Fprintln(w, "                               audit); --json emits a snapshot for a later run to diff against")
	fmt.Fprintln(w, "  fleet diff A.json B.json     what changed between two snapshots (exit 4 on a regression)")
	fmt.Fprintln(w, "  protection [--repo O/N] [--branch B] [--roster F]")
	fmt.Fprintln(w, "                               compare what branch protection REQUIRES against what CI can")
	fmt.Fprintln(w, "                               post. GitHub counts a skipped check as satisfying a required")
	fmt.Fprintln(w, "                               one, so a repo passes only if it requires the always-running")
	fmt.Fprintln(w, "                               \"CI OK\" aggregate, or a context no job can skip. Reaches the API")
	fmt.Fprintln(w, "                               via `gh` (exit 4 on a finding; exit 7 if a repo could NOT be")
	fmt.Fprintln(w, "                               checked — which is never reported as a pass)")
	usageWait(w)
	usageCIRound(w)
	fmt.Fprintln(w, "  console [--roster F] [--inbox F]")
	fmt.Fprintln(w, "                               no flags needed. One screen: the inbox's open ACTION/UNREAD entries,")
	fmt.Fprintln(w, "                               then every live session on this machine (name, kind, status, project,")
	fmt.Fprintln(w, "                               cwd, age) from `claude agents --json`; with --roster/$LACQUER_ROSTER")
	fmt.Fprintln(w, "                               also fleet truth, open PRs, and the project each session belongs to.")
	fmt.Fprintln(w, "                               If claude cannot be read it prints `sessions: unavailable — <why>`,")
	fmt.Fprintln(w, "                               never an empty list. The inbox is --inbox, else $LACQUER_INBOX, else")
	fmt.Fprintln(w, "                               $XDG_STATE_HOME/lacquer/inbox.jsonl (~/.local/state/lacquer/inbox.jsonl),")
	fmt.Fprintln(w, "                               created on the first write; ci-round and wait pr use the same file.")
	fmt.Fprintln(w, "                               With a roster it also records PR merges: one UNREAD per merge since a")
	fmt.Fprintln(w, "                               per-repo cursor (merge-cursor.json beside the inbox). A repo's first look")
	fmt.Fprintln(w, "                               only sets the cursor, nothing is backfilled; a gh failure is listed as")
	fmt.Fprintln(w, "                               unavailable; with no roster it says merges are not being recorded.")
	fmt.Fprintln(w, "                               Every console flag works on either side of the subcommand, with the")
	fmt.Fprintln(w, "                               same meaning: `watch --relaunch` == `--relaunch watch`. An unknown")
	fmt.Fprintln(w, "                               flag, or one the subcommand has no use for, is an error. --roster,")
	fmt.Fprintln(w, "                               --roles, --sessions and --inbox are accepted by every subcommand;")
	fmt.Fprintln(w, "                               -- ends the flags")
	fmt.Fprintln(w, "  console ... --mode bg|tmux [--worktree P | --branch B] dispatch <project> \"<task>\"")
	fmt.Fprintln(w, "                               start work on one project. bg = `claude --bg` in a new git worktree")
	fmt.Fprintln(w, "                               and branch under <repo>/.claude/worktrees/ (nothing is launched if")
	fmt.Fprintln(w, "                               one cannot be made); tmux = a detached tmux session in the checkout")
	fmt.Fprintln(w, "                               itself (attach with `tmux attach -t <name>`). Both run claude with")
	fmt.Fprintln(w, "                               --dangerously-skip-permissions and the sandbox off. Neither needs a")
	fmt.Fprintln(w, "                               terminal; a tmux session already running is left alone.")
	fmt.Fprintln(w, "                               --worktree P runs in P, an existing registered worktree of the")
	fmt.Fprintln(w, "                               project's repo (either mode; never created, changed or removed), for")
	fmt.Fprintln(w, "                               the worktree a PM assigned an IC in its brief. --branch B (bg only)")
	fmt.Fprintln(w, "                               names the branch bg creates instead of dispatch/<id>, in a directory")
	fmt.Fprintln(w, "                               named B with / as - under .claude/worktrees/, for a branch a PM")
	fmt.Fprintln(w, "                               chose; refused if either exists. An unusable --worktree or --branch")
	fmt.Fprintln(w, "                               launches nothing. Flags are read anywhere, among the task's words")
	fmt.Fprintln(w, "                               too, so a trailing --dry-run is a dry run; a task word that starts")
	fmt.Fprintln(w, "                               with - goes after --: dispatch <project> -- <task>")
	fmt.Fprintln(w, "                               --model M / --effort E override Claude launch settings in both modes;")
	fmt.Fprintln(w, "                               IC model defaults to roster ic_model or sonnet; effort to ic_effort.")
	fmt.Fprintln(w, "                               Roles use model/effort in their role entry, otherwise inherit.")
	fmt.Fprintln(w, "                               --sessions records requested settings; dashboard/watch show them.")
	fmt.Fprintln(w, "  console --roles R [--worktree P | --branch B] dispatch-role <name> [\"<task override>\"]")
	fmt.Fprintln(w, "                               start a named role — a lead/PM supervising many projects, not")
	fmt.Fprintln(w, "                               editing one; mode and task come from the roles file, modes and")
	fmt.Fprintln(w, "                               --worktree/--branch as above")
	fmt.Fprintln(w, "  console --sessions S [--roster F] [--roles R] watch [--relaunch] [--live] [--interval D]")
	fmt.Fprintln(w, "                               check every recorded dispatch's liveness; --relaunch re-dispatches")
	fmt.Fprintln(w, "                               each one found dead, a failed launch included (Blocked and Missing")
	fmt.Fprintln(w, "                               are reported, not auto-relaunched); a bg session resumes in its")
	fmt.Fprintln(w, "                               recorded worktree, and the relaunched session's record replaces the")
	fmt.Fprintf(w, "                               dead one. After %d failed launches in a row a record is held for you,\n", console.MaxFailedLaunches)
	fmt.Fprintln(w, "                               not relaunched again. --live keeps redrawing every --interval (default")
	fmt.Fprintln(w, "                               2s) instead of checking once, until Ctrl-C; ignores --relaunch.")
	fmt.Fprintln(w, "                               --sessions on dispatch/dispatch-role records every launch attempt,")
	fmt.Fprintln(w, "                               a failed one included; nothing is tracked unless you pass it")
	fmt.Fprintln(w, "  console --sessions S kill <name-or-daemon-id> [--force]")
	fmt.Fprintln(w, "                               stop one recorded session and drop it from the sessions file.")
	fmt.Fprintln(w, "                               Refuses an Alive session unless --force. For bg mode this removes")
	fmt.Fprintln(w, "                               the job's own ~/.claude/jobs/<id> directory (a blocked bg daemon")
	fmt.Fprintln(w, "                               holds no live process to signal, so this is what actually stops")
	fmt.Fprintln(w, "                               it being respawned) and keeps the session's own worktree, printing")
	fmt.Fprintln(w, "                               its path — for tmux mode it kills the tmux session")
	fmt.Fprintln(w, "  console --inbox F inbox add --type action|unread --title T [--body B] [--ref R] [--project P]")
	fmt.Fprintln(w, "                               record a decision awaiting the operator, or finished work")
	fmt.Fprintln(w, "                               awaiting acknowledgement; prints the new entry's id on success")
	fmt.Fprintln(w, "                               (usable non-interactively, e.g. from an agent's own shell)")
	fmt.Fprintln(w, "  console --inbox F inbox resolve <id>")
	fmt.Fprintln(w, "                               mark one inbox entry resolved")
	fmt.Fprintln(w, "  console --inbox F inbox list [--all]")
	fmt.Fprintln(w, "                               list open inbox entries (--all also lists resolved ones)")
	fmt.Fprintln(w, "  version                      print content and build versions and the resolved root")
	fmt.Fprintln(w, "  help, --help, -h             show this help")
	fmt.Fprintln(w, "env: LACQUER_ROOT (path to the lacquer checkout, default '.')")
	fmt.Fprintln(w, "     LACQUER_ALLOW_UNVERIFIED_ROOT=1 (run against a root that is not a pinned release --")
	fmt.Fprintln(w, "                               e.g. a feature worktree during development; warns loudly")
	fmt.Fprintln(w, "                               on every invocation that output is unreviewed, see issue #350)")
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

// parseConsoleArgs parses console's flags wherever they appear -- before the
// subcommand, after it, or among its arguments -- and returns the arguments
// that are not flags, in order. "--" ends the flags: everything after it is
// an argument, which is how a dispatch task carries a word starting with -.
//
// Go's flag package stops at the first argument that is not a flag, and every
// console subcommand is one. So a flag after the subcommand was never parsed:
// `watch --relaunch`, the form fleet-ops documents, printed the status and
// relaunched nothing; `kill x --force` refused as if --force were absent; and
// #399 had to refuse a flag after dispatch, or `dispatch p "task" --dry-run`
// would have launched for real with the flag as task text. Nothing reported
// the first two. The syntax is the flag package's own (-name or --name, a
// value after = or as the next argument, none for a bool unless after =), so
// a flag means the same on either side of the subcommand.
//
// A word that is not a flag of this set is an error, never an argument: a
// typo'd flag, or one a task needed and did not put after --, would otherwise
// be folded silently into the task.
func parseConsoleArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(positional, args[i+1:]...), nil
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(a[1:], "-"), "=")
		if name == "" || name[0] == '-' {
			return positional, fmt.Errorf("bad flag syntax: %s", a)
		}
		f := fs.Lookup(name)
		if f == nil {
			if name == "h" || name == "help" {
				return positional, flag.ErrHelp
			}
			typed, _, _ := strings.Cut(a, "=")
			return positional, fmt.Errorf("unknown flag %s", typed)
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			if !hasValue {
				value = "true"
			}
		} else if !hasValue {
			if i+1 == len(args) {
				return positional, fmt.Errorf("--%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if err := fs.Set(name, value); err != nil {
			return positional, fmt.Errorf("invalid value %q for --%s: %v", value, name, err)
		}
	}
	return positional, nil
}

// consoleSubcommand names the subcommand args select ("" for the dashboard,
// "inbox add" for an inbox one), refusing one that does not exist and
// arguments it would not read. Too few arguments are left to each
// subcommand's own usage message.
func consoleSubcommand(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	sub, max := args[0], 0
	switch sub {
	case "watch":
		max = 1
	case "kill":
		max = 2
	case "dispatch", "dispatch-role":
		return sub, nil // the rest is the target and the task
	case "inbox":
		if len(args) < 2 {
			return sub, nil
		}
		switch args[1] {
		case "add", "list":
			sub, max = "inbox "+args[1], 2
		case "resolve":
			sub, max = "inbox resolve", 3
		default:
			return "", fmt.Errorf("unknown inbox subcommand %q (want add, resolve, or list)", args[1])
		}
	default:
		return "", fmt.Errorf("unknown console subcommand %q (want watch, kill, dispatch, dispatch-role or inbox; none for the dashboard)", sub)
	}
	if len(args) > max {
		return "", fmt.Errorf("unexpected argument %q to %s", args[max], sub)
	}
	return sub, nil
}

// consoleFlagScope is the subcommands each console flag applies to ("" is the
// dashboard). A flag set for a subcommand it does not apply to is refused
// rather than ignored: ignored, it reads as honoured, and `--dry-run kill`
// would kill for real. A flag missing from this table is refused everywhere,
// so a new one cannot pass unscoped.
//
// The file flags apply everywhere, because fleet-ops' console.sh passes
// --roster, --roles and --sessions on every invocation, whatever follows.
var consoleFlagScope = map[string][]string{
	"roster":   nil,
	"roles":    nil,
	"sessions": nil,
	"inbox":    nil,
	"mode":     {"dispatch"},
	"dry-run":  {"dispatch", "dispatch-role", "watch"},
	"worktree": {"dispatch", "dispatch-role"},
	"branch":   {"dispatch", "dispatch-role"},
	"model":    {"dispatch", "dispatch-role"},
	"effort":   {"dispatch", "dispatch-role"},
	"relaunch": {"watch"},
	"live":     {"watch"},
	"interval": {"watch"},
	"force":    {"kill"},
	"type":     {"inbox add"},
	"title":    {"inbox add"},
	"body":     {"inbox add"},
	"ref":      {"inbox add"},
	"project":  {"inbox add"},
	"all":      {"inbox list"},
}

// checkConsoleFlagScope refuses the first flag set on fs that sub has no use
// for (see consoleFlagScope).
func checkConsoleFlagScope(fs *flag.FlagSet, sub string) error {
	var err error
	fs.Visit(func(f *flag.Flag) {
		if err != nil {
			return
		}
		scope, known := consoleFlagScope[f.Name]
		if known && (scope == nil || slices.Contains(scope, sub)) {
			return
		}
		if !known {
			err = fmt.Errorf("--%s has no declared scope; this is a lacquer bug", f.Name)
			return
		}
		where := sub
		if where == "" {
			where = "the dashboard (no subcommand)"
		}
		err = fmt.Errorf("--%s applies only to %s, not to %s", f.Name, strings.Join(scope, " and "), where)
	})
	return err
}

// finishDispatch prints a dispatch's output, records it when a sessions file
// is configured, and turns its error into an exit code. Shared by dispatch
// and dispatch-role.
//
// What gets recorded is decided by console (Launch.Record), not here. A
// failure to write the record is a warning, not a hard error: the dispatch
// itself already happened, and the operator's request should not fail just
// because tracking could not be written.
func finishDispatch(stdout, stderr io.Writer, sessionsPath string, launch console.Launch, err error) int {
	fmt.Fprint(stdout, launch.Output)
	if sessionsPath != "" && launch.Record != nil {
		if rerr := console.AppendRecord(sessionsPath, *launch.Record); rerr != nil {
			fmt.Fprintf(stderr, "warning: could not record session: %v\n", rerr)
		}
	}
	if err != nil {
		return fail(stderr, err)
	}
	return 0
}

// runInboxAdd is `lacquer console --inbox F inbox add`. Must be usable
// non-interactively by an agent in one shell line, so the ONLY thing it prints
// on success is the assigned id — a script capturing stdout gets exactly the
// id and nothing else to strip. Its flags (--type, --title, --body, --ref,
// --project) are console's, parsed on either side of the subcommand.
func runInboxAdd(path, typ string, e inbox.Entry, stdout, stderr io.Writer) int {
	switch typ {
	case string(inbox.Action):
		e.Type = inbox.Action
	case string(inbox.Unread):
		e.Type = inbox.Unread
	default:
		return fail(stderr, fmt.Errorf("inbox add needs --type action or --type unread, got %q", typ))
	}
	e, err := inbox.Add(path, e)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintln(stdout, e.ID)
	return 0
}

// runInboxResolve is `lacquer console --inbox F inbox resolve <id>`.
func runInboxResolve(path string, args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		return fail(stderr, fmt.Errorf("usage: lacquer console --inbox F inbox resolve <id>"))
	}
	e, err := inbox.Resolve(path, args[0])
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "resolved %s [%s]: %s\n", e.ID, e.Type, e.Title)
	return 0
}

// runInboxList is `lacquer console --inbox F inbox list [--all]`. Defaults to
// open entries only, matching what the console dashboard itself shows;
// --all also lists resolved ones, for an operator auditing what has already
// been handled.
func runInboxList(path string, isDefault, all bool, stdout, stderr io.Writer) int {
	entries, malformed, err := inbox.ReadAll(path)
	if err != nil && isDefault && errors.Is(err, iofs.ErrNotExist) {
		fmt.Fprintf(stdout, "no inbox entries (%s does not exist yet)\n", path)
		return 0
	}
	if err != nil {
		return fail(stderr, err)
	}
	if malformed > 0 {
		fmt.Fprintf(stderr, "warning: skipped %d malformed inbox line(s)\n", malformed)
	}
	var shown int
	for _, e := range entries {
		if !all && !e.Open() {
			continue
		}
		shown++
		status := "open"
		if !e.Open() {
			status = "resolved " + e.ResolvedAt.Format(time.RFC3339)
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", e.ID, e.Type, status, e.CreatedAt.Format(time.RFC3339), e.Title)
		if e.Body != "" {
			fmt.Fprintf(stdout, "\t\t\t\t  %s\n", e.Body)
		}
	}
	if shown == 0 {
		fmt.Fprintln(stdout, "no inbox entries")
	}
	return 0
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
