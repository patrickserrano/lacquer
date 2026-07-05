package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/patrickserrano/harness/internal/audit"
	"github.com/patrickserrano/harness/internal/initcmd"
	"github.com/patrickserrano/harness/internal/onboardcmd"
	"github.com/patrickserrano/harness/internal/status"
	syncpkg "github.com/patrickserrano/harness/internal/sync"
	"github.com/patrickserrano/harness/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run is the testable entry point: it dispatches one CLI invocation and returns
// the process exit code. args is os.Args[1:]; getenv resolves environment (chiefly
// HARNESS_ROOT); stdout/stderr receive command output. main() is a thin wrapper.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}

	// help is answered before anything else: print to STDOUT and exit 0, so
	// `harness --help` isn't a non-zero "unknown command" with output on stderr.
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	}

	// harnessRoot is the directory holding this repo's VERSION/core/profiles,
	// resolved from HARNESS_ROOT and defaulting to ".".
	harnessRoot := getenv("HARNESS_ROOT")
	if harnessRoot == "" {
		harnessRoot = "."
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		return fail(stderr, err)
	}

	switch args[0] {
	case "init":
		// init reads harnessRoot to gate detected profiles to those that ship;
		// with it unset (default ".") every profile would be silently dropped.
		if err := requireHarnessRoot(harnessRoot); err != nil {
			return fail(stderr, err)
		}
		summary, err := initcmd.Run(harnessRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, summary)
	case "onboard":
		// onboard invokes init, which reads harnessRoot (see init above).
		if err := requireHarnessRoot(harnessRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("onboard", flag.ContinueOnError)
		fs.SetOutput(stderr)
		// No default org: the harness must not bake in any one org's identity, so
		// repo creation requires an explicit --org (see onboardcmd.Run).
		org := fs.String("org", "", "GitHub org for repo creation (required unless --no-repo)")
		noRepo := fs.Bool("no-repo", false, "do not create a repo even if no remote exists")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		summary, err := onboardcmd.Run(harnessRoot, projectRoot, *org, !*noRepo)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, summary)
	case "sync":
		if err := requireHarnessRoot(harnessRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
		fs.SetOutput(stderr)
		force := fs.Bool("force", false, "overwrite local changes the harness did not make (see `harness audit`)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		res, err := syncpkg.Run(harnessRoot, projectRoot, *force)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "sync complete: %d regions, %d assets\n", res.Regions, res.Assets)
	case "audit":
		if err := requireHarnessRoot(harnessRoot); err != nil {
			return fail(stderr, err)
		}
		rows, ver, err := audit.Classify(harnessRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, audit.Format(rows, ver))
		// Exit 3 when a project change would be clobbered, so `harness audit` is
		// usable as a CI drift gate (documented in usage()).
		if len(audit.Clobbered(rows)) > 0 {
			return 3
		}
	case "status":
		if err := requireHarnessRoot(harnessRoot); err != nil {
			return fail(stderr, err)
		}
		rows, err := status.Rows(harnessRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, status.Format(rows))
	case "version":
		if err := requireHarnessRoot(harnessRoot); err != nil {
			return fail(stderr, err)
		}
		v, err := version.Read(harnessRoot)
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

// requireHarnessRoot checks that harnessRoot looks like a harness checkout — the
// VERSION file and profiles/ dir both exist — so commands that read them fail
// with an actionable message instead of an opaque "open VERSION: no such file"
// when HARNESS_ROOT is unset and the cwd is not the harness repo.
func requireHarnessRoot(harnessRoot string) error {
	if isFile(filepath.Join(harnessRoot, "VERSION")) && isDir(filepath.Join(harnessRoot, "profiles")) {
		return nil
	}
	return fmt.Errorf("%q is not a harness checkout (no VERSION file and/or profiles/ dir); "+
		"set HARNESS_ROOT to your harness repo, e.g. `HARNESS_ROOT=~/Developer/harness harness <command>`", harnessRoot)
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
	fmt.Fprintln(w, "usage: harness <command>")
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  init                         detect components and write .harness.toml")
	fmt.Fprintln(w, "  onboard --org O [--no-repo]  init, then create a private GitHub repo")
	fmt.Fprintln(w, "  sync [--force]               render harness content into the project")
	fmt.Fprintln(w, "  status                       show each region's stamped vs latest version")
	fmt.Fprintln(w, "  audit                        classify project drift (exit 3 if sync would clobber a local change)")
	fmt.Fprintln(w, "  version                      print the harness version")
	fmt.Fprintln(w, "  help, --help, -h             show this help")
	fmt.Fprintln(w, "env: HARNESS_ROOT (path to the harness checkout, default '.')")
}

func fail(w io.Writer, err error) int {
	fmt.Fprintln(w, "error:", err)
	return 1
}
