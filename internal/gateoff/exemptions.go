package gateoff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
)

// manifest is the part of .lacquer.toml that can exempt something from a gate.
//
// Every kind the manifest has, and whether this covers it:
//
//   - [project].exclude: covered. Takes a path out of sync and audit.
//   - [baseline.relax]: covered. Excuses a baseline key until a date.
//   - [[component]].dependabot_ignore: covered. Withholds dependency updates.
//   - [[project.not_run_in_ci]]: covered. Excuses a test target no job runs.
//   - [[project.covered_elsewhere]]: covered. Excuses a target an unmanaged
//     workflow runs; added only, as it has no date to extend.
//   - [[project.retired_secrets]]: covered. Lets a managed workflow stop
//     reading a secret.
//   - [project].retired: covered. Stops every scheduled workflow.
//   - [project].seed_once: covered. Takes a file's content out of the audit.
//     It has no reason field, so its findings always say none was recorded.
//   - [web].biome_ignores: covered. Takes paths out of lint; no reason field.
//   - [[web.biome_overrides]] and [ios].swiftlint_custom_rules: not covered.
//     Both may only add or tighten rules (internal/fragments rejects anything
//     that lowers one), so neither can turn a gate off.
//   - [project].ci_round_cap: not covered. It bounds an agent's pushes, not a
//     check on the code.
//
// Decoded leniently: keys this struct does not name are ignored, so a manifest
// written for a newer lacquer than this one still compares. The entry types are
// config's own, so the bare-string exclude spelling decodes as it does there.
type manifest struct {
	Project struct {
		Exclude          []config.Exclusion        `toml:"exclude"`
		SeedOnce         []string                  `toml:"seed_once"`
		RetiredSecrets   []config.RetiredSecret    `toml:"retired_secrets"`
		Retired          *config.Retirement        `toml:"retired"`
		CoveredElsewhere []config.CoveredElsewhere `toml:"covered_elsewhere"`
		NotRunInCI       []config.NotRunInCI       `toml:"not_run_in_ci"`
	} `toml:"project"`
	Component []struct {
		Path             string                    `toml:"path"`
		DependabotIgnore []config.DependabotIgnore `toml:"dependabot_ignore"`
	} `toml:"component"`
	Baseline struct {
		Relax map[string]baseline.Relax `toml:"relax"`
	} `toml:"baseline"`
	Web struct {
		BiomeIgnores []string `toml:"biome_ignores"`
	} `toml:"web"`
}

func parseManifest(data []byte) (manifest, error) {
	var m manifest
	_, err := toml.Decode(string(data), &m)
	return m, err
}

// exclusionReason is the reason of the head's exclusion covering path, or "".
// An edit to a file the same change excluded is explained by that exclusion.
func (m manifest) exclusionReason(path string) string {
	for _, e := range m.Project.Exclude {
		if e.Matches(path) {
			return strings.TrimSpace(e.Reason)
		}
	}
	return ""
}

// entry is one exemption entry, flattened for comparison: key is what it
// exempts, until its end date ("" for none), extra anything else that widens it
// (dependabot_ignore's version ranges).
type entry struct {
	key, until, reason string
	extra              []string
}

// full identifies an entry by everything in it, for the multiset compare.
func (e entry) full() string {
	return e.key + "\x00" + e.until + "\x00" + e.reason + "\x00" + strings.Join(e.extra, "\x01")
}

// exemptionChanges lists each exemption head has that base did not, and each
// one head extended. Removing or shortening one is silent.
//
// Entries are compared as multisets of whole entries, never as a map by key:
// a manifest may list the same path twice, and a map keeps whichever came last,
// so a later-dated duplicate written above the old entry would vanish. Every
// head entry the base does not hold verbatim is weighed against the most
// permissive base entry for its key (the latest end, undated beating any date,
// the union of version ranges): no base entry is "added", a later end or a
// dated entry made undated is "extended", new version ranges are "widened",
// and anything else (a reworded reason, a shorter duplicate) is silent.
func exemptionChanges(base, head manifest) []Finding {
	var out []Finding
	compare := func(kind, label string, hasDates bool, b, h []entry) {
		left := map[string]int{}
		for _, e := range b {
			left[e.full()]++
		}
		type widest struct {
			until   string
			undated bool
			extra   []string
		}
		most := map[string]*widest{}
		for _, e := range b {
			w := most[e.key]
			if w == nil {
				w = &widest{until: e.until}
				most[e.key] = w
			}
			if e.until == "" {
				w.undated = true
			} else if e.until > w.until {
				w.until = e.until
			}
			w.extra = append(w.extra, e.extra...)
		}
		sort.SliceStable(h, func(i, j int) bool { return h[i].key < h[j].key })
		said := map[string]bool{}
		for _, he := range h {
			if left[he.full()] > 0 {
				left[he.full()]--
				continue
			}
			name := fmt.Sprintf("%s %q", label, he.key)
			var what string
			w := most[he.key]
			switch {
			case w == nil:
				what = "added " + name + untilText(hasDates, he.until)
			case !w.undated && extends(w.until, he.until):
				what = fmt.Sprintf("extended %s until %s→%s", name, w.until, orUndated(he.until))
			default:
				if wider := missing(w.extra, he.extra); len(wider) > 0 {
					what = fmt.Sprintf("widened %s to also withhold %s", name, strings.Join(wider, ", "))
				}
			}
			if what == "" || said[what] {
				continue
			}
			said[what] = true
			out = append(out, Finding{Kind: kind, Subject: he.key, What: what, Reason: he.reason})
		}
	}

	excl := func(m manifest) []entry {
		var out []entry
		for _, e := range m.Project.Exclude {
			out = append(out, entry{key: e.Path, until: e.Until, reason: strings.TrimSpace(e.Reason)})
		}
		return out
	}
	compare("exclude", "[project].exclude", true, excl(base), excl(head))

	relax := func(m manifest) []entry {
		var out []entry
		for k, r := range m.Baseline.Relax {
			out = append(out, entry{key: k, until: r.Until, reason: strings.TrimSpace(r.Reason)})
		}
		return out
	}
	compare("relax", "[baseline.relax]", true, relax(base), relax(head))

	ignores := func(m manifest) []entry {
		var out []entry
		for _, c := range m.Component {
			for _, d := range c.DependabotIgnore {
				out = append(out, entry{key: c.Path + ":" + d.Dependency, until: d.Until, reason: strings.TrimSpace(d.Reason), extra: d.Versions})
			}
		}
		return out
	}
	compare("dependabot_ignore", "dependabot_ignore", true, ignores(base), ignores(head))

	notRun := func(m manifest) []entry {
		var out []entry
		for _, n := range m.Project.NotRunInCI {
			out = append(out, entry{key: n.Target, until: n.Until, reason: strings.TrimSpace(n.Reason)})
		}
		return out
	}
	compare("not_run_in_ci", "[[project.not_run_in_ci]]", true, notRun(base), notRun(head))

	covered := func(m manifest) []entry {
		var out []entry
		for _, c := range m.Project.CoveredElsewhere {
			out = append(out, entry{key: c.Target, reason: strings.TrimSpace(c.Reason)})
		}
		return out
	}
	compare("covered_elsewhere", "[[project.covered_elsewhere]]", false, covered(base), covered(head))

	secrets := func(m manifest) []entry {
		var out []entry
		for _, s := range m.Project.RetiredSecrets {
			out = append(out, entry{key: s.Name, reason: strings.TrimSpace(s.Reason)})
		}
		return out
	}
	compare("retired_secrets", "[[project.retired_secrets]]", false, secrets(base), secrets(head))

	retired := func(m manifest) []entry {
		if m.Project.Retired == nil {
			return nil
		}
		return []entry{{key: "project", reason: strings.TrimSpace(m.Project.Retired.Reason)}}
	}
	compare("retired", "[project].retired", false, retired(base), retired(head))

	names := func(list []string) []entry {
		var out []entry
		for _, s := range list {
			out = append(out, entry{key: s})
		}
		return out
	}
	compare("seed_once", "[project].seed_once", false, names(base.Project.SeedOnce), names(head.Project.SeedOnce))
	compare("biome_ignores", "[web].biome_ignores", false, names(base.Web.BiomeIgnores), names(head.Web.BiomeIgnores))
	return out
}

// extends reports whether moving an end date from old to new lengthens it. An
// undated entry never ends, so dated→undated extends and undated→anything does
// not. Dates are YYYY-MM-DD, which order as strings.
func extends(old, new string) bool {
	if old == "" {
		return false
	}
	return new == "" || new > old
}

func untilText(hasDates bool, until string) string {
	if !hasDates {
		return ""
	}
	if until == "" {
		return " (undated)"
	}
	return " (until " + until + ")"
}

func orUndated(until string) string {
	if until == "" {
		return "undated"
	}
	return until
}

// missing is every entry of h that b lacks, in h's order.
func missing(b, h []string) []string {
	have := map[string]bool{}
	for _, s := range b {
		have[s] = true
	}
	var out []string
	for _, s := range h {
		if !have[s] {
			out = append(out, s)
		}
	}
	return out
}
