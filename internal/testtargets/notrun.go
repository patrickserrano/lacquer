package testtargets

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// NotRun is a project's declaration that a test target deliberately runs in no
// CI job. It is [[project.not_run_in_ci]], decoupled from the config package as
// Declaration is.
type NotRun struct {
	// Target is the test target's exact name.
	Target string
	// Reason is why nothing in CI runs it, and where it is run instead.
	Reason string
	// Until is the review date, YYYY-MM-DD.
	Until string
}

// NotRunClaim is a NotRun after Deliberate has read it against the report.
//
// Unlike a covered_elsewhere Claim there is nothing in the repository to verify:
// the declaration's whole content is "nothing runs this", and the audit has
// already worked out whether that is so. What it can check is whether the claim
// is still in term and still describes something, and those are the two ways it
// decays.
type NotRunClaim struct {
	NotRun
	// Applied reports that this declaration took its target out of Uncovered or
	// Unchecked (or would have: see Deliberate on unreadable packages). Only an
	// in-term, non-stale declaration is applied.
	Applied bool
	// Expired reports that the until date has passed, or cannot be read. An
	// expired declaration suppresses nothing and blocks the audit.
	Expired bool
	// Stale is why this declaration describes nothing — the target does not
	// exist, or something now runs it — and "" when it describes a real gap.
	Stale string
}

// Deliberate folds [[project.not_run_in_ci]] declarations into a report. It runs
// after Apply, because whether a suite runs anywhere is Apply's answer, and a
// declaration saying it runs nowhere is only meaningful against that answer.
//
//   - In term, and the target is in Uncovered (or Unchecked, where the audit
//     could not decide): the target moves to a line of its own, with the reason
//     and the date. It is printed, never dropped — an exception nobody can see
//     is an exception nobody reviews.
//   - Past until: the target stays exactly where Apply put it, and the claim is
//     recorded as expired, which blocks the audit. That is the re-surfacing: the
//     finding comes back on a date, rather than being written away for good.
//   - Stale: the target does not exist, or a selector, a verified
//     covered_elsewhere, or a workflow runs it. The declaration is claiming a gap
//     that is not there, and it is reported so it gets removed.
//
// A target in no readable place, while some referenced package could not be
// read, is not called missing: it may be in that package. It is left in term
// and printed, as Compare leaves such a selector Unverified.
//
// now is passed rather than read so the expiry boundary is testable.
func Deliberate(r Report, project []Target, decls []NotRun, now time.Time) Report {
	if len(decls) == 0 {
		return r
	}
	have := map[string]bool{}
	unreadable := false
	for _, t := range project {
		if t.Unread != "" {
			unreadable = true
			continue
		}
		have[t.Name] = true
	}
	elsewhere := map[string]Claim{}
	for _, c := range r.Elsewhere {
		elsewhere[c.Target] = c
	}
	ran := map[string]Claim{}
	for _, c := range r.Ran {
		ran[c.Target] = c
	}
	gap := map[string]bool{}
	for _, t := range r.Uncovered {
		gap[t.Name] = true
	}
	for _, u := range r.Unchecked {
		if u.Suite != "" {
			gap[u.Suite] = true
		}
	}

	sorted := append([]NotRun(nil), decls...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Target < sorted[j].Target })
	applied := map[string]bool{}
	for _, d := range sorted {
		c := NotRunClaim{NotRun: d, Expired: Expired(d.Until, now)}
		switch {
		case gap[d.Target]:
		case !have[d.Target] && unreadable:
			// Could not look. Neither stale nor a gap anyone can see.
		case !have[d.Target]:
			c.Stale = "this project has no test target with that name (renamed, or deleted)"
		case elsewhere[d.Target].Target != "":
			c.Stale = fmt.Sprintf("a [[project.covered_elsewhere]] declaration verified that %s runs it, "+
				"so it IS run in CI", elsewhere[d.Target].Workflow)
		case ran[d.Target].Target != "":
			c.Stale = fmt.Sprintf("%s runs it (%s), so it IS run in CI", ran[d.Target].Workflow, ran[d.Target].Reason)
		default:
			c.Stale = "a managed test selector names it, so it IS run in CI"
		}
		if c.Stale == "" && !c.Expired {
			c.Applied = true
			applied[d.Target] = true
		}
		r.NotRun = append(r.NotRun, c)
	}

	var kept []Target
	for _, t := range r.Uncovered {
		if !applied[t.Name] {
			kept = append(kept, t)
		}
	}
	r.Uncovered = kept
	var unchecked []Unchecked
	for _, u := range r.Unchecked {
		if u.Suite == "" || !applied[u.Suite] {
			unchecked = append(unchecked, u)
		}
	}
	r.Unchecked = unchecked
	return r
}

// Expired reports whether a declaration dated until has expired at now. Exported
// so internal/fleet decides expiry with this function rather than a copy of it:
// `lacquer fleet` must block on exactly what `lacquer audit` blocks on.
//
// The whole of the until DAY is in term, so the boundary is the last instant of
// it rather than midnight — "until = 2026-12-31" reads as "through the 31st" to
// everyone who writes one. Same construction as depignore.Review and
// exclusion.Review. An unreadable date is expired: config rejects one at load,
// and if that ever changes, an exemption whose term cannot be read is not in
// term.
func Expired(until string, now time.Time) bool {
	d, err := time.Parse("2006-01-02", until)
	return err != nil || now.After(d.AddDate(0, 0, 1).Add(-time.Nanosecond))
}

// Blocking counts the declarations that should fail an audit: the expired ones.
// Staleness alone never blocks, matching depignore and exclusion — it endangers
// nothing, and gating on it teaches people this output is noise.
func Blocking(r Report) int {
	n := 0
	for _, c := range r.NotRun {
		if c.Expired {
			n++
		}
	}
	return n
}

// formatNotRun renders the three states, in-term first.
func formatNotRun(b *strings.Builder, claims []NotRunClaim) {
	var live, lapsed, stale []NotRunClaim
	for _, c := range claims {
		switch {
		case c.Stale != "":
			stale = append(stale, c)
		case c.Expired:
			lapsed = append(lapsed, c)
		default:
			live = append(live, c)
		}
	}

	if len(live) > 0 {
		b.WriteString("\n")
		for _, c := range live {
			b.WriteString("deliberately not run in CI: " + c.Target + " — " + strings.TrimSpace(c.Reason) + " (until " + c.Until + ")\n")
		}
		b.WriteString("    Declared in [[project.not_run_in_ci]]: nothing in CI runs these, on purpose,\n")
		b.WriteString("    and the reason says where they are run instead. Not a finding while in term.\n")
		b.WriteString("    Past its until date a declaration expires, the suite is reported again, and\n")
		b.WriteString("    this audit fails (exit 4) — so the gap comes back for review.\n")
	}

	if len(lapsed) > 0 {
		b.WriteString("\nnot_run_in_ci declarations that have EXPIRED:\n")
		for _, c := range lapsed {
			b.WriteString("  " + c.Target + " — EXPIRED " + c.Until + ": " + strings.TrimSpace(c.Reason) + "\n")
		}
		b.WriteString("    The term ran out, so the suite is reported again and this audit fails\n")
		b.WriteString("    (exit 4). Run it in CI, delete it, or — if the reason still holds — review\n")
		b.WriteString("    it and set a new until date.\n")
	}

	if len(stale) > 0 {
		b.WriteString("\nnot_run_in_ci declarations that are not doing anything:\n")
		for _, c := range stale {
			line := "  " + c.Target + " — " + c.Stale
			if c.Expired {
				line += "; also EXPIRED " + c.Until + " (exit 4)"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("    Each reads as a known gap in CI coverage and is not one. Remove it, or\n")
		b.WriteString("    correct the target name.\n")
	}
}
