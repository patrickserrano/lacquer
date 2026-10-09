package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/swiftcomponents"
)

// runSwiftComponents is `lacquer swift-components [--check]` (#522 U4).
//
// Without --check it lists the manifest's Swift components and the packages the
// Lint job builds. With --check it is the Lint job's "Every Swift file belongs
// to a declared component" step: it lists the .swift files under no component,
// and exits 1 once the shared gate date has passed. Before that date the same
// finding prints, carries the date, and exits 0.
//
// It needs only the manifest and git, not LACQUER_ROOT: the step runs the
// released binary in the consumer's checkout.
func runSwiftComponents(args []string, projectRoot string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("swift-components", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "fail (exit 1) on Swift files under no declared component, once the gate date has passed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return fail(stderr, fmt.Errorf("unexpected swift-components arguments: %v", fs.Args()))
	}
	cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
	if err != nil {
		return fail(stderr, fmt.Errorf("load manifest: %w", err))
	}
	if !*check {
		comps := swiftcomponents.Components(cfg)
		if len(comps) == 0 {
			fmt.Fprintln(stdout, "no Swift components: no [[component]] carries the ios profile or stack = \"ios\"")
			return 0
		}
		fmt.Fprintln(stdout, "Swift components (each linted from its own directory):")
		for _, c := range comps {
			fmt.Fprintf(stdout, "  %s\n", c.Name())
		}
		pkgs, err := swiftcomponents.Packages(projectRoot, cfg)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintln(stdout, "Packages under package components:")
		if len(pkgs) == 0 {
			fmt.Fprintln(stdout, "  (none)")
		}
		for _, p := range pkgs {
			if p.IOSOnly {
				fmt.Fprintf(stdout, "  %s  NOT built by Lint: iOS-only (its platforms list no macOS)\n", p.Dir)
			} else {
				fmt.Fprintf(stdout, "  %s\n", p.Dir)
			}
		}
		return 0
	}

	r, err := swiftcomponents.Check(projectRoot, cfg, time.Now())
	if err != nil {
		return fail(stderr, err)
	}
	names := make([]string, len(r.Components))
	for i, c := range r.Components {
		names[i] = c.Name()
	}
	if len(r.Stray) == 0 {
		if len(r.Components) == 0 {
			fmt.Fprintln(stdout, "swift-components: no Swift components declared, so there is nothing for Swift files to belong to.")
			return 0
		}
		fmt.Fprintf(stdout, "swift-components: every Swift file belongs to a declared component (%d checked against %s).\n",
			r.Checked, strings.Join(names, ", "))
		return 0
	}
	fmt.Fprint(stdout, swiftcomponents.Format(r))
	if getenv("GITHUB_ACTIONS") == "true" {
		level := "warning"
		if r.Blocks() {
			level = "error"
		}
		fmt.Fprintf(stdout, "::%s title=Swift outside every declared component::%s\n", level, swiftcomponents.Summary(r))
	}
	if r.Blocks() {
		return 1
	}
	return 0
}
