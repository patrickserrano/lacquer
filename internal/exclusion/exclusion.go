// Package exclusion reviews a project's [project].exclude entries.
//
// [project].exclude and [baseline.relax] are the two ways a project opts out of
// something the lacquer would otherwise impose, and until this package existed
// they were held to opposite standards. A relaxation needs a reason and an
// expiry, is printed on every audit, and hard-fails once the date passes. An
// exclusion needed nothing, was printed nowhere, and lasted forever.
//
// That asymmetry is not academic. The fleet writes exclusion reasons as TOML
// comments — "keep it absent until those three dedicated org secrets are
// provisioned", "until that upstreaming happens deliberately" — which is
// exactly the right instinct expressed in the one place no tool can read,
// report, or expire. Five of seventeen manifests exclude something; the reasons
// were all in comments, and one had no reason at all.
//
// So this package reads the structured form, and reports four distinct things a
// reader would otherwise have to reconstruct by hand:
//
//   - expired — a dated exclusion whose date has passed. Blocks, like a relax.
//   - unattributed — the bare-string form, carrying no reason at all.
//   - permanent — attributed, deliberately undated. Reported, never gates.
//   - stale — suppresses nothing the lacquer ships any more. Dead text.
package exclusion

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
)

// Status is what a single exclusion is, once its date and attribution are read.
type Status string

const (
	// StatusExpired is a dated exclusion whose date has passed. The project
	// declared this exemption temporary and the term ran out.
	StatusExpired Status = "expired"
	// StatusUnattributed is the bare-string form: no reason, no expiry. Nobody
	// can tell whether it is a deliberate divergence or a forgotten workaround.
	StatusUnattributed Status = "unattributed"
	// StatusPermanent is attributed and deliberately undated — a real, ongoing
	// difference between this project and the fleet.
	StatusPermanent Status = "permanent"
	// StatusDated is attributed, dated, and still within its term.
	StatusDated Status = "dated"
)

// Finding is one reviewed exclusion.
type Finding struct {
	Path   string
	Status Status
	Reason string
	Until  string
	// Stale means this exclusion suppressed nothing: the lacquer ships no asset
	// under this path. Orthogonal to Status — an exclusion can be perfectly
	// attributed and still be dead text, which is the harder kind to notice.
	Stale bool
}

// Blocks reports whether this finding should fail an audit.
//
// Only expiry blocks. An unattributed or stale exclusion is a documentation
// defect, and gating on it would break five repos the moment this shipped for
// something that endangers nothing — the reliable way to teach people that
// lacquer output is noise to be worked around.
func (f Finding) Blocks() bool { return f.Status == StatusExpired }

// Review classifies every exclusion. suppressed is the set of destinations the
// exclusions actually kept out of the sync plan (assets.Suppressed).
//
// now is passed rather than read so the expiry boundary is testable — the same
// reason internal/baseline takes it.
func Review(excl []config.Exclusion, suppressed []string, now time.Time) []Finding {
	out := make([]Finding, 0, len(excl))
	for _, e := range excl {
		f := Finding{Path: e.Path, Reason: strings.TrimSpace(e.Reason), Until: e.Until}

		switch {
		case !e.Attributed():
			f.Status = StatusUnattributed
		case e.Until == "":
			f.Status = StatusPermanent
		default:
			// An unparseable date cannot reach here: config rejects it at load.
			// If that ever changes, treat it as expired rather than silently
			// valid — an exemption whose term cannot be read is not in term.
			until, err := e.UntilDate()
			if err != nil || now.After(until.AddDate(0, 0, 1).Add(-time.Nanosecond)) {
				f.Status = StatusExpired
			} else {
				f.Status = StatusDated
			}
		}

		f.Stale = true
		for _, dest := range suppressed {
			if e.Matches(dest) {
				f.Stale = false
				break
			}
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Blocking counts the findings that should fail an audit.
func Blocking(fs []Finding) int {
	var n int
	for _, f := range fs {
		if f.Blocks() {
			n++
		}
	}
	return n
}

// Format renders the review, or "" when there is nothing worth saying.
//
// A fully-attributed, in-term, still-live exclusion prints nothing. The report
// exists to surface what needs attention, and a project that has kept its
// exclusions honest should not have to read a wall of text confirming it.
func Format(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		var note string
		switch f.Status {
		case StatusExpired:
			note = fmt.Sprintf("EXPIRED %s — %s", f.Until, f.Reason)
		case StatusUnattributed:
			note = "no reason recorded — add `{ path = \"…\", reason = \"…\" }` (with `until` if temporary)"
		case StatusPermanent, StatusDated:
			if !f.Stale {
				continue // healthy and doing something: nothing to report
			}
		}
		if f.Stale {
			s := "excludes a path the lacquer no longer ships — it suppresses nothing and can be deleted"
			if note == "" {
				note = s
			} else {
				note += "; also " + s
			}
		}
		if b.Len() == 0 {
			b.WriteString("\nexclusions needing attention:\n")
		}
		fmt.Fprintf(&b, "  %s: %s\n", f.Path, note)
	}
	if b.Len() > 0 {
		b.WriteString("[project].exclude opts a path out of the lacquer entirely; unlike [baseline.relax] an undated one never expires, so it is reported until it is attributed or removed.\n")
	}
	return b.String()
}
