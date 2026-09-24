package baseline

import (
	"fmt"
	"strings"
)

// Format renders a baseline section for one profile's component.
//
// Empty output when there are no findings. A declared Swift component with
// empty discovery contributes an explicit UNKNOWN finding instead.
//
// Every non-OK line names the underlying Xcode build setting and the coverage
// ratio, because "non-compliant" without "which setting, in how many
// configurations" is a report an operator learns to skip.
func Format(profile string, fs []Finding) string {
	if len(fs) == 0 {
		return ""
	}

	blocking := Violations(fs)
	var relaxed, expired, unknown int
	for _, f := range fs {
		switch f.Status {
		case StatusUnknown:
			unknown++
		case StatusRelaxed:
			relaxed++
		case StatusExpired:
			expired++
		}
	}

	var b strings.Builder
	switch {
	case len(blocking) == 0 && relaxed == 0 && unknown == 0:
		fmt.Fprintf(&b, "baseline: ok (%s)\n", profile)
		return b.String()
	case unknown > 0:
		fmt.Fprintf(&b, "baseline: UNKNOWN (%s) — %d unresolved checks, %d violations\n", profile, unknown, len(blocking))
	default:
		fmt.Fprintf(&b, "baseline: %s\n", summary(len(blocking), relaxed, expired, profile))
	}

	for _, f := range fs {
		switch f.Status {
		case StatusOK:
			if f.Relax != nil {
				fmt.Fprintf(&b, "  ~ %-19s satisfied — the relaxation until %s is stale, remove it\n", f.Key, f.Relax.Until)
			}
		case StatusImplied:
			// Not a finding an operator needs to act on.
		case StatusRelaxed:
			fmt.Fprintf(&b, "  ~ %-19s RELAXED until %s — %s\n", f.Key, f.Relax.Until, f.Relax.Reason)
		case StatusExpired:
			fmt.Fprintf(&b, "  ! %-19s EXPIRED %s — %s (%s want %s, %s configs compliant)\n",
				f.Key, f.Relax.Until, f.Relax.Reason, f.Setting, f.Want, f.Ratio())
		case StatusUnknown:
			fmt.Fprintf(&b, "  ? %-19s UNKNOWN — %s\n", f.Key, f.Got)
		case StatusViolation:
			fmt.Fprintf(&b, "  x %-19s want %-9s got %-14s %s  (%s)\n",
				f.Key, f.Want, f.Got, f.Ratio(), f.Setting)
		}
		if f.Status != StatusOK && f.Status != StatusImplied {
			for _, detail := range f.Details {
				fmt.Fprintf(&b, "    %s\n", detail)
			}
		}
	}

	if len(blocking) > 0 {
		b.WriteString("\nfix by setting the listed build settings in every Swift build configuration,\n")
		b.WriteString("or add a time-boxed [baseline.relax] entry to .lacquer.toml with a reason.\n")
	}
	if unknown > 0 {
		b.WriteString("Verify unresolved settings with the build toolchain; UNKNOWN is neither compliance nor a baseline violation.\n")
	}
	if len(blocking) > 0 || unknown > 0 {
		b.WriteString("A project-level pbxproj write can be silently outranked by a target-level xcconfig; verify the resolved value before changing settings.\n")
	}
	return b.String()
}

// summary renders the counts line.
func summary(blocking, relaxed, expired int, profile string) string {
	var parts []string
	if n := blocking - expired; n > 0 {
		parts = append(parts, plural(n, "violation"))
	}
	if expired > 0 {
		parts = append(parts, plural(expired, "expired relaxation"))
	}
	if relaxed > 0 {
		parts = append(parts, fmt.Sprintf("%d relaxed", relaxed))
	}
	return strings.Join(parts, ", ") + " (" + profile + ")"
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
