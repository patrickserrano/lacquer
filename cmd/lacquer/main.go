package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/initcmd"
	"github.com/patrickserrano/lacquer/internal/onboardcmd"
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
		summary, err := initcmd.Run(lacquerRoot, projectRoot)
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
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		summary, err := onboardcmd.Run(lacquerRoot, projectRoot, *org, !*noRepo)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, summary)
	case "sync":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		fs := flag.NewFlagSet("sync", flag.ContinueOnError)
		fs.SetOutput(stderr)
		force := fs.Bool("force", false, "overwrite local changes the lacquer did not make (see `lacquer audit`)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		res, err := syncpkg.Run(lacquerRoot, projectRoot, *force)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "sync complete: %d regions, %d assets\n", res.Regions, res.Assets)
	case "audit":
		if err := requireLacquerRoot(lacquerRoot); err != nil {
			return fail(stderr, err)
		}
		rows, ver, err := audit.Classify(lacquerRoot, projectRoot)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, audit.Format(rows, ver))
		// Exit 3 when a project change would be clobbered, so `lacquer audit` is
		// usable as a CI drift gate (documented in usage()).
		if len(audit.Clobbered(rows)) > 0 {
			return 3
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
	fmt.Fprintln(w, "  init                         detect components and write .lacquer.toml")
	fmt.Fprintln(w, "  onboard --org O [--no-repo]  init, then create a private GitHub repo")
	fmt.Fprintln(w, "  sync [--force]               render lacquer content into the project")
	fmt.Fprintln(w, "  status                       show each region's stamped vs latest version")
	fmt.Fprintln(w, "  audit                        classify project drift (exit 3 if sync would clobber a local change)")
	fmt.Fprintln(w, "  version                      print the lacquer version")
	fmt.Fprintln(w, "  help, --help, -h             show this help")
	fmt.Fprintln(w, "env: LACQUER_ROOT (path to the lacquer checkout, default '.')")
}

func fail(w io.Writer, err error) int {
	fmt.Fprintln(w, "error:", err)
	return 1
}
