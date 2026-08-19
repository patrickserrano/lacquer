// Package depignore reviews a project's [[component]].dependabot_ignore entries.
//
// An ignore is the one place this tool withholds a dependency update, and the
// generated .github/dependabot.yml otherwise promises the opposite: every update
// opens a PR. So the exemption is held to the same account as the other two —
// [baseline.relax] and an attributed [project].exclude — and for the same
// reason. An exemption nothing reports is an exemption nobody revisits, and the
// fleet has already shown where that ends: reasons written as TOML comments, in
// the one place no tool can read, report, or expire.
//
// Two things are reported, and only one of them gates:
//
//   - expired — the `until` date has passed. Blocks, exactly as an expired
//     exclusion does, and for the identical reason: the project declared this
//     temporary and the term ran out. The update it withholds may now be
//     perfectly mergeable, and nothing else would ever ask.
//   - stale — the ignore names a dependency this component does not declare.
//     Reported, never gates. It is the analogue of an exclusion that suppresses
//     nothing (assets.Suppressed), and it is the harder kind to notice: the
//     entry is attributed, in term, and doing nothing at all.
//
// There is no "unattributed" or "permanent" status here, unlike exclusions.
// config rejects both at load — every ignore has a reason and a date or the
// manifest does not parse — so those states cannot reach this package.
//
// Staleness is answered from the component's dependency manifest, and honestly:
// npm, Go and Cargo declare their dependencies in a file this package can read,
// and SwiftPM does not (its resolved graph lives inside an .xcodeproj bundle,
// keyed by repository URL rather than by the name Dependabot reports). A
// component whose manifest cannot be read is reported as NOT stale rather than
// guessed at — a false stale report on a live ignore would be advice to delete
// something load-bearing, which is much worse than saying nothing.
package depignore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
)

// Status is what a single ignore is, once its date is read.
type Status string

const (
	// StatusExpired is an ignore whose until date has passed.
	StatusExpired Status = "expired"
	// StatusDated is an ignore still within its term.
	StatusDated Status = "dated"
)

// Finding is one reviewed ignore.
type Finding struct {
	Component  string
	Dependency string
	Versions   []string
	Status     Status
	Reason     string
	Until      string
	// Stale means this ignore withheld nothing: the component's dependency
	// manifest does not name this dependency. Orthogonal to Status — an ignore
	// can be perfectly attributed, well inside its term, and still be dead text.
	//
	// False whenever the answer is unknown (no readable manifest, unknown project
	// root, SwiftPM). Absence of evidence is not reported as evidence.
	Stale bool
}

// Blocks reports whether this finding should fail an audit.
//
// Only expiry blocks, matching exclusion.Finding.Blocks exactly. A stale ignore
// is a documentation defect that endangers nothing; gating on it would teach
// people that this output is noise to work around, which is the reliable way to
// lose the gate that does matter.
func (f Finding) Blocks() bool { return f.Status == StatusExpired }

// Review classifies every ignore across every component. root is the project
// root (config.Config.Root); "" means unknown, and staleness is not evaluated.
//
// now is passed rather than read so the expiry boundary is testable — the same
// reason internal/baseline and internal/exclusion take it.
func Review(comps []config.Component, root string, now time.Time) []Finding {
	var out []Finding
	for _, c := range comps {
		var declared map[string]bool
		var known bool
		if len(c.DependabotIgnore) > 0 {
			declared, known = declaredDeps(root, c.Path)
		}
		for _, d := range c.DependabotIgnore {
			f := Finding{
				Component:  c.Path,
				Dependency: d.Dependency,
				Versions:   d.Versions,
				Reason:     strings.TrimSpace(d.Reason),
				Until:      d.Until,
			}
			// An unparseable date cannot reach here: config rejects it at load.
			// If that ever changes, treat it as expired rather than silently
			// valid — an exemption whose term cannot be read is not in term.
			until, err := d.UntilDate()
			// The whole of the until DAY is in term, which is why the boundary is
			// the last instant of it rather than midnight. "until = 2026-11-30"
			// reads as "through the 30th" to everyone who writes one, and
			// expiring at 00:00 on the 30th would blame a project for a day it
			// was promised. Same construction as exclusion.Review.
			if err != nil || now.After(until.AddDate(0, 0, 1).Add(-time.Nanosecond)) {
				f.Status = StatusExpired
			} else {
				f.Status = StatusDated
			}
			f.Stale = known && !declared[d.Dependency]
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Component != out[j].Component {
			return out[i].Component < out[j].Component
		}
		return out[i].Dependency < out[j].Dependency
	})
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
// An in-term ignore that really is withholding something prints nothing. The
// report exists to surface what needs attention, and a project keeping its
// ignores honest should not have to read a wall of text confirming it — the same
// stance exclusion.Format takes.
func Format(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		var note string
		if f.Status == StatusExpired {
			note = fmt.Sprintf("EXPIRED %s — %s", f.Until, f.Reason)
		}
		if f.Stale {
			s := fmt.Sprintf("names a dependency %s does not declare — it withholds nothing and can be deleted", componentLabel(f.Component))
			if note == "" {
				note = s
			} else {
				note += "; also " + s
			}
		}
		if note == "" {
			continue // in term and doing something: nothing to report
		}
		if b.Len() == 0 {
			b.WriteString("\ndependabot ignores needing attention:\n")
		}
		fmt.Fprintf(&b, "  %s: %s (%s)\n", componentLabel(f.Component), f.Dependency, strings.Join(f.Versions, ", "))
		fmt.Fprintf(&b, "    %s\n", note)
	}
	if b.Len() > 0 {
		b.WriteString("[[component]].dependabot_ignore withholds a dependency update that cannot be merged. " +
			"Unlike [project].exclude it has no permanent form: past `until` it expires and blocks, " +
			"so an ignore that outlived its reason comes back for review instead of persisting.\n")
	}
	return b.String()
}

// componentLabel spells a component path for a human. "" and "." are the root.
func componentLabel(p string) string {
	if p == "" || p == "." {
		return "(root)"
	}
	return p
}

// declaredDeps returns the dependency names the component at comp declares, and
// whether the question could be answered at all.
//
// The second return is the load-bearing one. Every ecosystem this tool renders
// Dependabot entries for is represented below EXCEPT swift, whose dependency
// names Dependabot derives from a Package.resolved buried inside an .xcodeproj
// bundle and keyed by repository URL rather than by the identifier that appears
// in a Dependabot PR title. Reading that well enough to say "this ignore names
// nothing" is not something this package can currently do honestly, so it says
// so by returning known=false, and no swift ignore is ever reported stale.
//
// Unknown is also what an unreadable, absent, or malformed manifest yields. A
// project whose package.json is mid-edit must not be told to delete a live
// ignore; the direction to fail in is silence.
func declaredDeps(root, comp string) (map[string]bool, bool) {
	if root == "" {
		return nil, false
	}
	dir := root
	if comp != "" && comp != "." {
		dir = filepath.Join(root, filepath.FromSlash(comp))
	}
	out := map[string]bool{}
	var known bool
	for _, read := range []func(string, map[string]bool) bool{readPackageJSON, readGoMod, readCargoToml} {
		if read(dir, out) {
			known = true
		}
	}
	return out, known
}

// readPackageJSON collects npm dependency names from every dependency map npm
// supports. All four are read because Dependabot updates all four, and the
// measured case — a docs generator and a compiler — lives entirely in
// devDependencies, which a "dependencies only" reader would have called stale.
func readPackageJSON(dir string, out map[string]bool) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var doc struct {
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		// Malformed rather than absent, but the answer is the same: unknown. A
		// half-written package.json must not produce advice to delete config.
		return false
	}
	for _, m := range []map[string]json.RawMessage{
		doc.Dependencies, doc.DevDependencies, doc.PeerDependencies, doc.OptionalDependencies,
	} {
		for name := range m {
			out[name] = true
		}
	}
	return true
}

// readGoMod collects module paths from go.mod's require directives.
//
// Line-based rather than via golang.org/x/mod: the only thing needed is the set
// of module paths, this repository has no dependency on that module, and adding
// one to answer a staleness question would be a poor trade. Both spellings are
// handled — the parenthesised block and the single-line `require path version`
// form — because a small go.mod uses the second and a generated one the first.
func readGoMod(dir string, out map[string]bool) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	var inBlock bool
	for _, raw := range strings.Split(string(data), "\n") {
		line := raw
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case inBlock && line == ")":
			inBlock = false
		case inBlock:
			if f := strings.Fields(line); len(f) >= 1 {
				out[f[0]] = true
			}
		case line == "require (":
			inBlock = true
		case strings.HasPrefix(line, "require "):
			if f := strings.Fields(line); len(f) >= 2 {
				out[f[1]] = true
			}
		}
	}
	return true
}

// readCargoToml collects crate names from Cargo.toml's three dependency tables.
//
// Decoded into map[string]any rather than a typed struct because a dependency's
// value is either a version string or an inline table, and only the KEY is
// wanted either way.
func readCargoToml(dir string, out map[string]bool) bool {
	var doc map[string]any
	if _, err := toml.DecodeFile(filepath.Join(dir, "Cargo.toml"), &doc); err != nil {
		return false
	}
	for _, table := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
		raw, ok := doc[table]
		if !ok {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for name := range m {
			out[name] = true
		}
	}
	return true
}
