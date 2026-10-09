package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/gitguard"
)

// StrictResolveFlag is the xcodebuild flag that makes package resolution fail
// instead of moving a pin away from Package.resolved. The lacquer's iOS
// workflows pass it on an explicit resolve step and on every build, test and
// archive after it.
const StrictResolveFlag = "-onlyUsePackageVersionsFromResolvedFile"

// How strictly the project's committed workflows resolve Swift packages, as
// recorded on a PinChecked summary for an .xcodeproj/.xcworkspace lockfile.
// Empty for a Package.swift lockfile: `swift build` is not assessed here.
const (
	// GateStrict: at least one xcodebuild build/test/archive/resolve runs, and
	// every one passes StrictResolveFlag. A pin that does not satisfy its
	// requirement fails that CI rather than re-resolving green.
	GateStrict = "strict"
	// GatePartial: some calls are strict and some are not. The loose ones can
	// re-resolve on their own, and a job built only from them stays green.
	GatePartial = "partial"
	// GateNone: no committed workflow builds strictly from the lockfile.
	GateNone = "none"
)

// xcodebuildAction finds xcodebuild where it is run, not where it is mentioned:
// at the start of a command, after a subshell or command-substitution opener.
var xcodebuildAction = regexp.MustCompile(`(^|[\s(;&|])xcodebuild\s+(.*)$`)

// resolvingActions are the xcodebuild actions that resolve the package graph
// and then build from it. -showBuildSettings is left out on purpose: it reads
// settings and ships nothing, so it cannot put a re-resolved package into a
// build on its own.
var resolvingActions = map[string]bool{
	"build": true, "test": true, "archive": true, "build-for-testing": true,
	"-resolvePackageDependencies": true,
}

// resolutionGate reads every committed workflow and classifies how it runs
// xcodebuild. loose lists file:line for each resolving call without the flag.
func resolutionGate(projectRoot string) (state string, strict, loose []string) {
	files, err := gitguard.Tracked(projectRoot, ":(top).github/workflows/*.yml", ":(top).github/workflows/*.yaml")
	if err != nil {
		return GateNone, nil, nil
	}
	sort.Strings(files)
	ix := pinIndex{root: projectRoot}
	for _, f := range files {
		body, err := ix.read(f)
		if err != nil {
			continue
		}
		for _, c := range xcodebuildCallsIn(body) {
			if !resolvingActions[c.action] {
				continue
			}
			if c.strict {
				strict = append(strict, at(f, c.line))
			} else {
				loose = append(loose, at(f, c.line))
			}
		}
	}
	switch {
	case len(strict) > 0 && len(loose) == 0:
		return GateStrict, strict, nil
	case len(strict) > 0:
		return GatePartial, strict, loose
	}
	return GateNone, nil, loose
}

type xcodebuildRun struct {
	line   int
	action string
	strict bool
}

// xcodebuildCallsIn finds the xcodebuild invocations in a workflow file, with
// backslash continuations joined and comment and echo lines skipped: those
// mention xcodebuild without running it.
func xcodebuildCallsIn(body string) []xcodebuildRun {
	lines := strings.Split(body, "\n")
	var out []xcodebuildRun
	for i := 0; i < len(lines); i++ {
		start := i
		cmd := lines[i]
		for strings.HasSuffix(strings.TrimRight(cmd, " \t"), "\\") && i+1 < len(lines) {
			cmd = strings.TrimSuffix(strings.TrimRight(cmd, " \t"), "\\") + " " + lines[i+1]
			i++
		}
		trimmed := strings.TrimSpace(cmd)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "echo ") || strings.HasPrefix(trimmed, "printf ") {
			continue
		}
		m := xcodebuildAction.FindStringSubmatch(cmd)
		if m == nil {
			continue
		}
		run := xcodebuildRun{line: start + 1}
		for _, f := range strings.Fields(m[2]) {
			f = strings.Trim(f, `"'`)
			if run.action == "" && (resolvingActions[f] || f == "-showBuildSettings" || f == "-exportArchive" || f == "-version") {
				run.action = f
			}
			if f == StrictResolveFlag {
				run.strict = true
			}
		}
		out = append(out, run)
	}
	return out
}

// gateLine is the report line saying whether CI enforces the lockfile.
func gateLine(f PinFinding) string {
	switch f.Gate {
	case GateStrict:
		return fmt.Sprintf("  CI resolves strictly from these lockfiles: every xcodebuild build/test/archive in the committed workflows passes %s (%s)\n",
			StrictResolveFlag, strings.Join(f.GateStrict, ", "))
	case GatePartial:
		return fmt.Sprintf("  CI resolves strictly only in part: %s run xcodebuild without %s and can re-resolve away from the lockfile\n",
			strings.Join(f.GateLoose, ", "), StrictResolveFlag)
	case GateNone:
		return fmt.Sprintf("  CI does not resolve strictly from these lockfiles: no committed workflow runs xcodebuild with %s\n", StrictResolveFlag)
	}
	return ""
}
