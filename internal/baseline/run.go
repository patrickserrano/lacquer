package baseline

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Target is one component to check: its profile (which selects the standard) and
// the project-relative .xcodeproj that component builds.
//
// Callers assemble these from a project's manifest. This package deliberately
// does not load .lacquer.toml itself: config imports this package to reach the
// Relax type, so importing config back would be an import cycle.
type Target struct {
	Profile   string
	Component string
	Xcodeproj string // project-relative; empty when the manifest names none
}

// Report is one component's baseline result.
type Report struct {
	Profile   string
	Component string
	Findings  []Finding
	Unchecked string // non-empty when the component could not be checked, and why
}

// Run checks every target against its profile's standard.
//
// A target whose profile ships no baseline.toml is skipped entirely — no report,
// because there is no standard to hold it to. A target with a standard but no
// configured xcodeproj yields a report carrying Unchecked: "we could not look"
// must be visible, since rendering it as a pass is how a gap goes unnoticed.
//
// A configured xcodeproj that is absent is an error — a renamed or mistyped path
// must not read as a pass — *unless* the component has no Swift on disk at all.
// That case is a project onboarded from an archetype and not yet written: `sync`
// requires a non-empty {{XCODEPROJ}} to render the iOS CI, commands, and hooks,
// so "declare the stack before the code exists" forces a path that cannot exist
// yet, and every such project audited red from its first commit for doing
// exactly what archetypes/README.md tells it to. Absent xcodeproj + no Swift is
// therefore Unchecked (visible, not a pass); the moment one .swift file lands,
// the same absence is an error again.
func Run(lacquerRoot, projectRoot string, targets []Target, relax map[string]Relax, now time.Time) ([]Report, error) {
	var reports []Report
	for _, t := range targets {
		spec, ok, err := LoadSpec(lacquerRoot, t.Profile)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // this profile asserts no baseline
		}

		rep := Report{Profile: t.Profile, Component: t.Component}
		if t.Xcodeproj == "" {
			rep.Unchecked = "no xcodeproj configured in .lacquer.toml"
			reports = append(reports, rep)
			continue
		}

		path := filepath.Join(projectRoot, filepath.FromSlash(t.Xcodeproj))
		if _, err := os.Stat(path); err != nil {
			// Only a not-yet-created project is excused. An xcodeproj that exists
			// but cannot be read (permissions, a broken symlink) is still an error:
			// that is a real project whose baseline went unverified.
			if os.IsNotExist(err) && !hasSwiftSources(filepath.Join(projectRoot, filepath.FromSlash(t.Component))) {
				rep.Unchecked = fmt.Sprintf("xcodeproj %q is declared but not created yet, and %s has no Swift sources", t.Xcodeproj, t.Component)
				reports = append(reports, rep)
				continue
			}
			return nil, fmt.Errorf("baseline: %s names xcodeproj %q which is not readable: %w", t.Component, t.Xcodeproj, err)
		}
		d, err := ReadXcodeproj(path)
		if err != nil {
			return nil, err
		}
		rep.Findings = Check(spec, d, relax, now)
		reports = append(reports, rep)
	}
	return reports, nil
}

// Blocking counts the findings across all reports that should fail a run.
func Blocking(reports []Report) int {
	var n int
	for _, r := range reports {
		n += len(Violations(r.Findings))
	}
	return n
}

// FormatReports renders every report, including the unchecked ones.
func FormatReports(reports []Report) string {
	var out string
	for _, r := range reports {
		if r.Unchecked != "" {
			out += fmt.Sprintf("baseline: NOT CHECKED (%s/%s) — %s\n", r.Profile, r.Component, r.Unchecked)
			continue
		}
		out += Format(r.Profile, r.Findings)
	}
	return out
}

// hasSwiftSources reports whether dir contains any Swift on disk, which is what
// separates "this project has not been written yet" from "this project's
// xcodeproj path is wrong". It walks rather than globs because a component root
// (often ".") holds its sources several directories down.
//
// Build output and vendored code are skipped: DerivedData and .build carry Swift
// that the project did not write, and node_modules is large enough on a
// component shared with a web profile to dominate the walk. Any unreadable
// subtree is skipped rather than fatal — this answers one yes/no question and
// must not turn a permissions quirk into a failed audit.
func hasSwiftSources(dir string) bool {
	var found bool
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it, do not fail the walk
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "DerivedData") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".swift") {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
