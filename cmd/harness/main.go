package main

import (
	"fmt"
	"os"

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
	case "sync":
		if err := syncpkg.Run(harnessRoot, projectRoot); err != nil {
			fail(err)
		}
		fmt.Println("sync complete")
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
	fmt.Fprintln(os.Stderr, "commands: sync, status")
	fmt.Fprintln(os.Stderr, "env: HARNESS_ROOT (path to harness repo, default '.')")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
