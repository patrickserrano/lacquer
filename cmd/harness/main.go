package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/patrickserrano/harness/internal/audit"
	"github.com/patrickserrano/harness/internal/initcmd"
	"github.com/patrickserrano/harness/internal/onboardcmd"
	"github.com/patrickserrano/harness/internal/status"
	syncpkg "github.com/patrickserrano/harness/internal/sync"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	// harnessRoot is the directory containing this repo's VERSION/core/profiles.
	// For now it is resolved from the HARNESS_ROOT env var, defaulting to ".".
	harnessRoot := os.Getenv("HARNESS_ROOT")
	if harnessRoot == "" {
		harnessRoot = "."
	}
	projectRoot, err := os.Getwd()
	if err != nil {
		fail(err)
	}

	switch os.Args[1] {
	case "init":
		summary, err := initcmd.Run(projectRoot)
		if err != nil {
			fail(err)
		}
		fmt.Println(summary)
	case "onboard":
		fs := flag.NewFlagSet("onboard", flag.ExitOnError)
		// No default org: the harness must not bake in any one org's identity, so
		// repo creation requires an explicit --org (see onboardcmd.Run).
		org := fs.String("org", "", "GitHub org for repo creation (required unless --no-repo)")
		noRepo := fs.Bool("no-repo", false, "do not create a repo even if no remote exists")
		_ = fs.Parse(os.Args[2:])
		summary, err := onboardcmd.Run(projectRoot, *org, !*noRepo)
		if err != nil {
			fail(err)
		}
		fmt.Println(summary)
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ExitOnError)
		force := fs.Bool("force", false, "overwrite local changes the harness did not make (see `harness audit`)")
		_ = fs.Parse(os.Args[2:])
		res, err := syncpkg.Run(harnessRoot, projectRoot, *force)
		if err != nil {
			fail(err)
		}
		fmt.Printf("sync complete: %d regions, %d assets\n", res.Regions, res.Assets)
	case "audit":
		rows, ver, err := audit.Classify(harnessRoot, projectRoot)
		if err != nil {
			fail(err)
		}
		fmt.Print(audit.Format(rows, ver))
		// Exit non-zero when a project change would be clobbered, so `harness
		// audit` is usable as a CI drift gate.
		if len(audit.Clobbered(rows)) > 0 {
			os.Exit(3)
		}
	case "status":
		rows, err := status.Rows(harnessRoot, projectRoot)
		if err != nil {
			fail(err)
		}
		fmt.Print(status.Format(rows))
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: harness <command>")
	fmt.Fprintln(os.Stderr, "commands: init, onboard --org O [--no-repo], sync [--force], audit, status")
	fmt.Fprintln(os.Stderr, "env: HARNESS_ROOT (path to harness repo, default '.')")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
