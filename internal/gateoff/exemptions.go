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

// dated is one exemption entry with an optional end date, for the added/
// extended comparison every dated kind shares.
type dated struct {
	until, reason string
	// extra is anything else that widens the entry, keyed for comparison
	// (dependabot_ignore's version ranges).
	extra []string
}

// exemptionChanges lists each exemption head has that base did not, and each
// one head extended. Removing or shortening one is silent.
func exemptionChanges(base, head manifest) []Finding {
	var out []Finding
	compare := func(kind, label string, hasDates bool, b, h map[string]dated) {
		for _, subject := range sortedKeys(h) {
			he := h[subject]
			be, had := b[subject]
			name := fmt.Sprintf("%s %q", label, subject)
			switch {
			case !had:
				out = append(out, Finding{Kind: kind, Subject: subject, Reason: he.reason,
					What: "added " + name + untilText(hasDates, he.until)})
			case extends(be.until, he.until):
				out = append(out, Finding{Kind: kind, Subject: subject, Reason: he.reason,
					What: fmt.Sprintf("extended %s until %s→%s", name, be.until, orUndated(he.until))})
			default:
				if wider := missing(be.extra, he.extra); len(wider) > 0 {
					out = append(out, Finding{Kind: kind, Subject: subject, Reason: he.reason,
						What: fmt.Sprintf("widened %s to also withhold %s", name, strings.Join(wider, ", "))})
				}
			}
		}
	}

	excl := func(m manifest) map[string]dated {
		out := map[string]dated{}
		for _, e := range m.Project.Exclude {
			out[e.Path] = dated{until: e.Until, reason: strings.TrimSpace(e.Reason)}
		}
		return out
	}
	compare("exclude", "[project].exclude", true, excl(base), excl(head))

	relax := func(m manifest) map[string]dated {
		out := map[string]dated{}
		for k, r := range m.Baseline.Relax {
			out[k] = dated{until: r.Until, reason: strings.TrimSpace(r.Reason)}
		}
		return out
	}
	compare("relax", "[baseline.relax]", true, relax(base), relax(head))

	ignores := func(m manifest) map[string]dated {
		out := map[string]dated{}
		for _, c := range m.Component {
			for _, d := range c.DependabotIgnore {
				out[c.Path+":"+d.Dependency] = dated{until: d.Until, reason: strings.TrimSpace(d.Reason), extra: d.Versions}
			}
		}
		return out
	}
	compare("dependabot_ignore", "dependabot_ignore", true, ignores(base), ignores(head))

	notRun := func(m manifest) map[string]dated {
		out := map[string]dated{}
		for _, n := range m.Project.NotRunInCI {
			out[n.Target] = dated{until: n.Until, reason: strings.TrimSpace(n.Reason)}
		}
		return out
	}
	compare("not_run_in_ci", "[[project.not_run_in_ci]]", true, notRun(base), notRun(head))

	covered := func(m manifest) map[string]dated {
		out := map[string]dated{}
		for _, c := range m.Project.CoveredElsewhere {
			out[c.Target] = dated{reason: strings.TrimSpace(c.Reason)}
		}
		return out
	}
	compare("covered_elsewhere", "[[project.covered_elsewhere]]", false, covered(base), covered(head))

	secrets := func(m manifest) map[string]dated {
		out := map[string]dated{}
		for _, s := range m.Project.RetiredSecrets {
			out[s.Name] = dated{reason: strings.TrimSpace(s.Reason)}
		}
		return out
	}
	compare("retired_secrets", "[[project.retired_secrets]]", false, secrets(base), secrets(head))

	retired := func(m manifest) map[string]dated {
		if m.Project.Retired == nil {
			return nil
		}
		return map[string]dated{"project": {reason: strings.TrimSpace(m.Project.Retired.Reason)}}
	}
	compare("retired", "[project].retired", false, retired(base), retired(head))

	names := func(list []string) map[string]dated {
		out := map[string]dated{}
		for _, s := range list {
			out[s] = dated{}
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

func sortedKeys(m map[string]dated) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
