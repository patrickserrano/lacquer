package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// JSON writes the sweep as an indented array, which is the form a later run
// diffs against. Stable field order and sorted input make that diff meaningful
// rather than a reshuffle.
func JSON(w io.Writer, reports []Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(reports)
}

// Text renders the sweep for a human.
//
// A healthy project prints one line. The report exists to surface what needs
// attention, and a fleet that is in good shape should not cost a screenful to
// confirm — that is how a report becomes something people stop reading, which
// this fleet has already demonstrated with a nightly job that failed for months
// while three projects worked around it instead.
func Text(w io.Writer, reports []Report) {
	if len(reports) == 0 {
		fmt.Fprintln(w, "roster is empty")
		return
	}

	width := 0
	for _, r := range reports {
		if len(r.Name) > width {
			width = len(r.Name)
		}
	}

	var blocking, healthy int
	for _, r := range reports {
		notes := Notes(r)
		if r.Blocking() {
			blocking++
		}
		if len(notes) == 0 {
			healthy++
			fmt.Fprintf(w, "  ok    %-*s  %s\n", width, r.Name, drift(r))
			continue
		}
		mark := "warn"
		if r.Blocking() {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  %s  %-*s  %s\n", mark, width, r.Name, notes[0])
		for _, n := range notes[1:] {
			fmt.Fprintf(w, "        %-*s  %s\n", width, "", n)
		}
	}

	fmt.Fprintf(w, "\n%d project(s): %d clean, %d blocking\n", len(reports), healthy, blocking)

	if h := horizon(reports); len(h) > 0 {
		// The single most actionable cross-project signal: every time-boxed
		// exemption in the fleet, soonest first. Each one is a date on which a
		// project's CI starts failing, and without this they are invisible
		// until the morning they fire.
		fmt.Fprintln(w, "\nexpiring exemptions (soonest first):")
		for _, s := range h {
			fmt.Fprintf(w, "  %s\n", s)
		}
	}

	if s := shared(reports); len(s) > 0 {
		// The signal that is worth the whole sweep. When several projects
		// exclude the same path, the shared asset is usually the thing that is
		// wrong — this fleet had three projects independently working around
		// one defect, visible only by reading three manifests' comments.
		fmt.Fprintln(w, "\nexcluded by more than one project (candidates to fix upstream):")
		for _, l := range s {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
}

// Notes summarises what is wrong with one project, most severe first.
//
// Exported because the console renders the same findings beside live sessions
// and open pull requests. Two renderers computing "what is wrong" separately
// would drift, and the one that drifted would be the one nobody was reading.
func Notes(r Report) []string {
	var out []string
	if r.Error != "" {
		return []string{r.Error}
	}
	if n := len(r.Clobbered); n > 0 {
		out = append(out, fmt.Sprintf("%d unit(s) would be overwritten by sync", n))
	}
	for _, b := range r.Baseline {
		if len(b.Violations) > 0 {
			out = append(out, fmt.Sprintf("baseline %s/%s: %s", b.Profile, b.Component, strings.Join(b.Violations, ", ")))
		}
		if b.Unchecked != "" {
			out = append(out, fmt.Sprintf("baseline %s/%s NOT CHECKED: %s", b.Profile, b.Component, b.Unchecked))
		}
	}
	for _, d := range r.Drift {
		if d.Adoptable {
			out = append(out, fmt.Sprintf("undeclared stack %s -> %s (run adopt)", d.Path, d.Profile))
		} else {
			out = append(out, fmt.Sprintf("stack no profile covers: %s -> %s (a gap in the lacquer)", d.Path, d.Profile))
		}
	}
	for _, e := range r.Exclusions {
		switch {
		case e.Status == "expired":
			out = append(out, fmt.Sprintf("EXPIRED exclusion %s (%s)", e.Path, e.Until))
		case e.Status == "unattributed":
			out = append(out, fmt.Sprintf("exclusion with no reason: %s", e.Path))
		case e.Stale:
			out = append(out, fmt.Sprintf("stale exclusion (suppresses nothing): %s", e.Path))
		}
	}
	// Only the two facts that need a decision. A project with attributed,
	// line-scoped suppressions has done the right thing and should not be nagged
	// — the count alone would make this line permanent noise in every project.
	if n := r.Suppress.FileScoped; n > 0 {
		out = append(out, fmt.Sprintf("%d file-scoped lint suppression(s) — unbounded, each hides an unknown number of violations (%s)",
			n, strings.Join(r.Suppress.TopRules(3), ", ")))
	}
	if n := r.Suppress.Unattributed; n > 0 {
		out = append(out, fmt.Sprintf("%d inline lint suppression(s) with no reason (%s)",
			n, strings.Join(r.Suppress.TopRules(3), ", ")))
	}
	if r.Audit.Behind > 0 || r.Audit.Add > 0 {
		out = append(out, fmt.Sprintf("%d behind, %d to add — sync would update it", r.Audit.Behind, r.Audit.Add))
	}
	return out
}

func drift(r Report) string {
	if r.Lacquer == "" {
		return ""
	}
	return "v" + r.Lacquer
}

// horizon lists every dated exemption across the fleet, soonest first.
func horizon(reports []Report) []string {
	type item struct{ until, line string }
	var items []item
	for _, r := range reports {
		for _, e := range r.Exclusions {
			if e.Until == "" {
				continue
			}
			state := ""
			if e.Status == "expired" {
				state = "  ** EXPIRED **"
			}
			items = append(items, item{e.Until, fmt.Sprintf("%s  %s  %s%s", e.Until, r.Name, e.Path, state)})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].until != items[j].until {
			return items[i].until < items[j].until
		}
		return items[i].line < items[j].line
	})
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.line)
	}
	return out
}

// shared returns paths excluded by more than one project.
func shared(reports []Report) []string {
	byPath := map[string][]string{}
	for _, r := range reports {
		for _, e := range r.Exclusions {
			byPath[e.Path] = append(byPath[e.Path], r.Name)
		}
	}
	var out []string
	for p, who := range byPath {
		if len(who) < 2 {
			continue
		}
		sort.Strings(who)
		out = append(out, fmt.Sprintf("%s  (%d: %s)", p, len(who), strings.Join(who, ", ")))
	}
	sort.Strings(out)
	return out
}
