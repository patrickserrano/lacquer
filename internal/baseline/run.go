package baseline

import (
	"fmt"
	"os"
	"path/filepath"
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
