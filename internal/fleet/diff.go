package fleet

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

// LoadSnapshot reads a snapshot written by JSON.
func LoadSnapshot(path string) ([]Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var reports []Report
	if err := json.Unmarshal(data, &reports); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	return reports, nil
}

// Change is one difference between two sweeps.
type Change struct {
	Project string
	Kind    string
	Detail  string
	// Regression marks a change in the wrong direction. Diff output is read to
	// decide where attention goes, and "something got worse" is a different
	// question from "something got better" even when both are noteworthy.
	Regression bool
}

// Diff reports what changed between two sweeps.
//
// This is the point of keeping snapshots at all. A single sweep says the fleet
// has four shared exclusions and six blocking projects — true, and after the
// second reading, wallpaper. Two sweeps say a FIFTH exclusion appeared and a
// project that was clean is not, which is a decision someone has to make.
//
// The asymmetry is deliberate: things getting worse are reported individually
// and marked, while things getting better are summarised. Nobody needs a
// per-item breakdown of problems that solved themselves, and burying one new
// regression among twenty resolutions is how a diff stops being read.
func Diff(before, after []Report) []Change {
	old := index(before)
	cur := index(after)
	var out []Change

	names := make([]string, 0, len(cur))
	for n := range cur {
		names = append(names, n)
	}
	for n := range old {
		if _, ok := cur[n]; !ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	for _, n := range names {
		o, hadOld := old[n]
		c, hasNew := cur[n]

		switch {
		case !hadOld:
			out = append(out, Change{n, "appeared", "new in the roster", false})
			if c.Blocking() {
				out = append(out, Change{n, "blocking", "and it is already blocking", true})
			}
			continue
		case !hasNew:
			// Not a regression: a project leaving the roster is a decision
			// someone made, not something that went wrong.
			out = append(out, Change{n, "gone", "no longer in the roster", false})
			continue
		}

		if !o.Blocking() && c.Blocking() {
			out = append(out, Change{n, "now blocking", "was clean at the previous sweep", true})
		}
		if o.Blocking() && !c.Blocking() {
			out = append(out, Change{n, "now clear", "was blocking at the previous sweep", false})
		}
		if o.Error == "" && c.Error != "" {
			out = append(out, Change{n, "unreadable", c.Error, true})
		}

		// Exclusions, keyed by path: a new one is a decision worth seeing, and
		// one that expired since the last sweep is a deadline that has passed.
		oe, ce := exclIndex(o), exclIndex(c)
		for _, p := range sortedKeys(ce) {
			prev, had := oe[p]
			now := ce[p]
			if !had {
				out = append(out, Change{n, "exclusion added", describeExcl(now), true})
				continue
			}
			if prev.Status != "expired" && now.Status == "expired" {
				out = append(out, Change{n, "exclusion EXPIRED", fmt.Sprintf("%s (was due %s)", p, now.Until), true})
			}
			if !prev.Stale && now.Stale {
				out = append(out, Change{n, "exclusion went stale", p + " — it suppresses nothing now", true})
			}
		}
		for _, p := range sortedKeys(oe) {
			if _, still := ce[p]; !still {
				out = append(out, Change{n, "exclusion removed", p, false})
			}
		}

		// Undeclared stacks, keyed by path+profile.
		od, cd := driftIndex(o), driftIndex(c)
		for _, k := range sortedKeys(cd) {
			if _, had := od[k]; !had {
				out = append(out, Change{n, "undeclared stack", k, true})
			}
		}

		// Baseline violations, keyed by profile/component/key.
		ob, cb := baselineIndex(o), baselineIndex(c)
		for _, k := range sortedKeys(cb) {
			if _, had := ob[k]; !had {
				out = append(out, Change{n, "baseline violation", k, true})
			}
		}
		var fixed int
		for k := range ob {
			if _, still := cb[k]; !still {
				fixed++
			}
		}
		if fixed > 0 {
			out = append(out, Change{n, "baseline fixed", fmt.Sprintf("%d violation(s) resolved", fixed), false})
		}
	}

	// Fleet-level: a path newly excluded by more than one project is the signal
	// that the shared asset, not the projects, is what needs changing.
	for _, p := range newlyShared(before, after) {
		out = append(out, Change{"(fleet)", "newly shared exclusion", p, true})
	}
	return out
}

func describeExcl(e Exclusion) string {
	s := e.Path
	switch {
	case e.Status == "unattributed":
		s += " — no reason recorded"
	case e.Until != "":
		s += " — until " + e.Until
	}
	return s
}

func index(rs []Report) map[string]Report {
	m := make(map[string]Report, len(rs))
	for _, r := range rs {
		m[r.Name] = r
	}
	return m
}

func exclIndex(r Report) map[string]Exclusion {
	m := map[string]Exclusion{}
	for _, e := range r.Exclusions {
		m[e.Path] = e
	}
	return m
}

func driftIndex(r Report) map[string]struct{} {
	m := map[string]struct{}{}
	for _, d := range r.Drift {
		m[d.Path+" -> "+d.Profile] = struct{}{}
	}
	return m
}

func baselineIndex(r Report) map[string]struct{} {
	m := map[string]struct{}{}
	for _, b := range r.Baseline {
		for _, v := range b.Violations {
			m[b.Profile+"/"+b.Component+": "+v] = struct{}{}
		}
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// newlyShared returns paths excluded by 2+ projects now, but not before.
func newlyShared(before, after []Report) []string {
	count := func(rs []Report) map[string]int {
		m := map[string]int{}
		for _, r := range rs {
			for _, e := range r.Exclusions {
				m[e.Path]++
			}
		}
		return m
	}
	ob, cb := count(before), count(after)
	var out []string
	for p, n := range cb {
		if n >= 2 && ob[p] < 2 {
			out = append(out, fmt.Sprintf("%s (now %d projects)", p, n))
		}
	}
	sort.Strings(out)
	return out
}

// FormatDiff renders the changes, regressions first.
func FormatDiff(w io.Writer, changes []Change) {
	if len(changes) == 0 {
		fmt.Fprintln(w, "no change since the previous sweep")
		return
	}
	var bad, good []Change
	for _, c := range changes {
		if c.Regression {
			bad = append(bad, c)
		} else {
			good = append(good, c)
		}
	}
	if len(bad) > 0 {
		fmt.Fprintln(w, "changed for the worse:")
		for _, c := range bad {
			fmt.Fprintf(w, "  %-24s %-22s %s\n", c.Project, c.Kind, c.Detail)
		}
	}
	if len(good) > 0 {
		if len(bad) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "changed for the better:")
		for _, c := range good {
			fmt.Fprintf(w, "  %-24s %-22s %s\n", c.Project, c.Kind, c.Detail)
		}
	}
	fmt.Fprintf(w, "\n%d change(s): %d worse, %d better\n", len(changes), len(bad), len(good))
}

// Regressions counts the changes in the wrong direction, for an exit code.
func Regressions(changes []Change) int {
	var n int
	for _, c := range changes {
		if c.Regression {
			n++
		}
	}
	return n
}
