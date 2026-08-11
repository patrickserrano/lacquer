// Package fleet runs the per-project checks across many projects at once and
// returns the result as data.
//
// Every other command in this tool answers a question about ONE project, which
// is correct for the thing a project's CI runs. It is the wrong shape for the
// question an operator actually has — "what is true across everything I own?"
// — and answering that by hand does not scale past about three repositories.
//
// It was answered by hand once, across seventeen. That sweep found a CI test
// parser that had been dead on every iOS project for months, three projects
// that had each independently worked around the same defect in this tool, and a
// duplicated CI pipeline burning a third of a month's Actions minutes. None of
// those are hard to see; all three needed someone to look at seventeen projects
// in one sitting, and nothing made that cheap.
//
// So this package makes it cheap, and deliberately makes it DATA rather than
// prose: a report an operator can diff between runs, because the valuable
// signal is rarely the snapshot. It is "three projects now share an exclusion
// reason" and "this expires in nine days".
//
// Two boundaries keep this honest, and both are enforced by tests:
//
//   - It is READ-ONLY. It never writes to a project, never opens a pull
//     request, never syncs. Acting on a finding belongs to whatever consumes
//     this output, where a human can see it happen.
//   - It is FLEET-AGNOSTIC. No project name, owner, or path appears in this
//     package. The roster is the caller's, and it is the caller's business
//     whether that list is public.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/detect"
	"github.com/patrickserrano/lacquer/internal/exclusion"
	"github.com/patrickserrano/lacquer/internal/suppress"
)

// Entry is one project in the roster.
type Entry struct {
	// Name identifies the project in output. Defaults to the path's base name.
	Name string `toml:"name"`
	// Repo is the "owner/name" this checkout pushes to. Optional, and carried
	// through to the report untouched.
	//
	// It exists because a local directory name is not a reliable key for the
	// project it holds: in the fleet this was built for, three of seventeen
	// checkouts sit in a directory named differently from their repository, and
	// the roster spans three owners. Anything that later acts on a finding —
	// opening a pull request, linking an issue — needs the slug, and inferring
	// it from the path would be wrong for those three without ever saying so.
	Repo string `toml:"repo"`
	// Path is the local checkout. Relative paths resolve against the roster
	// file's own directory, so a roster can live beside the projects it lists.
	Path string `toml:"path"`
}

// Roster is the list of projects to sweep.
type Roster struct {
	Project []Entry `toml:"project"`
}

// LoadRoster reads and validates a roster file.
func LoadRoster(path string) (Roster, error) {
	var r Roster
	data, err := os.ReadFile(path)
	if err != nil {
		return r, fmt.Errorf("read roster: %w", err)
	}
	md, err := toml.Decode(string(data), &r)
	if err != nil {
		return r, fmt.Errorf("parse roster %s: %w", path, err)
	}
	// A misspelled key would otherwise be dropped in silence, and a roster that
	// quietly ignores half its own entry is the same class of defect as an
	// exclusion with no reason: it reads as configured while doing nothing.
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		return r, fmt.Errorf("roster %s has unknown key(s): %s (known: name, path, repo)", path, strings.Join(keys, ", "))
	}
	if len(r.Project) == 0 {
		return r, fmt.Errorf("roster %s lists no projects", path)
	}
	base := filepath.Dir(path)
	seen := map[string]bool{}
	for i := range r.Project {
		e := &r.Project[i]
		if e.Path == "" {
			return r, fmt.Errorf("roster %s: entry %d has no path", path, i)
		}
		if !filepath.IsAbs(e.Path) {
			e.Path = filepath.Join(base, e.Path)
		}
		e.Path = filepath.Clean(e.Path)
		if e.Name == "" {
			e.Name = filepath.Base(e.Path)
		}
		if seen[e.Name] {
			// Two entries reporting under one name would silently collapse in
			// any consumer that keys by name — including a diff between runs,
			// which is the whole point of the output.
			return r, fmt.Errorf("roster %s: %q is listed twice", path, e.Name)
		}
		seen[e.Name] = true
	}
	sort.Slice(r.Project, func(i, j int) bool { return r.Project[i].Name < r.Project[j].Name })
	return r, nil
}

// AuditCounts is the per-status tally of managed units.
type AuditCounts struct {
	OK        int `json:"ok"`
	Add       int `json:"add"`
	Behind    int `json:"behind"`
	Modified  int `json:"locally_modified"`
	Conflict  int `json:"conflict"`
	Untracked int `json:"untracked"`
}

// BaselineReport is one profile/component's standard check.
type BaselineReport struct {
	Profile    string   `json:"profile"`
	Component  string   `json:"component"`
	Violations []string `json:"violations,omitempty"`
	Unchecked  string   `json:"unchecked,omitempty"`
}

// DriftFinding is a stack on disk the manifest does not declare.
type DriftFinding struct {
	Path    string `json:"path"`
	Profile string `json:"profile"`
	// Adoptable means this lacquer ships the profile, so the project can act on
	// it now. Unadoptable findings are the lacquer's gap, not the project's.
	Adoptable bool `json:"adoptable"`
}

// Exclusion is one reviewed [project].exclude entry.
type Exclusion struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Until  string `json:"until,omitempty"`
	Stale  bool   `json:"stale"`
}

// Report is one project's result. A project that could not be read carries Error
// and nothing else — a broken checkout must not abort the sweep or, worse, be
// omitted from it and read as healthy.
type Report struct {
	Name       string           `json:"name"`
	Repo       string           `json:"repo,omitempty"`
	Path       string           `json:"path"`
	Error      string           `json:"error,omitempty"`
	Lacquer    string           `json:"lacquer_version,omitempty"`
	Audit      AuditCounts      `json:"audit"`
	Clobbered  []string         `json:"clobbered,omitempty"`
	Baseline   []BaselineReport `json:"baseline,omitempty"`
	Drift      []DriftFinding   `json:"drift,omitempty"`
	Exclusions []Exclusion      `json:"exclusions,omitempty"`
	// Suppress rolls up inline lint suppressions found in project source. It is
	// a summary, not a list: the snapshot is diffed between runs, and dozens of
	// individual entries would bury the two numbers that matter — how many are
	// file-scoped (unbounded) and how many carry no reason.
	Suppress suppress.Summary `json:"suppressions"`
}

// Blocking reports whether this project would fail its own `lacquer audit`.
// Mirrors that command's exit codes exactly: a clobbered unit, a baseline
// violation, an expired exclusion, or an adoptable undeclared stack.
func (r Report) Blocking() bool {
	if r.Error != "" || len(r.Clobbered) > 0 {
		return true
	}
	for _, b := range r.Baseline {
		if len(b.Violations) > 0 {
			return true
		}
	}
	for _, e := range r.Exclusions {
		if e.Status == string(exclusion.StatusExpired) {
			return true
		}
	}
	for _, d := range r.Drift {
		if d.Adoptable {
			return true
		}
	}
	return false
}

// Run sweeps every project in the roster.
//
// now is passed rather than read so expiry boundaries are testable, the same
// reason internal/baseline and internal/exclusion take it.
func Run(lacquerRoot string, roster Roster, now time.Time) []Report {
	out := make([]Report, 0, len(roster.Project))
	for _, e := range roster.Project {
		out = append(out, inspect(lacquerRoot, e, now))
	}
	return out
}

// inspect runs every check against one project, collecting rather than
// returning the first error: a project with an unreadable manifest should still
// report THAT, not vanish from the sweep.
func inspect(lacquerRoot string, e Entry, now time.Time) Report {
	r := Report{Name: e.Name, Repo: e.Repo, Path: e.Path}

	manifest := filepath.Join(e.Path, ".lacquer.toml")
	cfg, err := config.Load(manifest)
	if err != nil {
		r.Error = fmt.Sprintf("load manifest: %v", err)
		return r
	}

	rows, ver, err := audit.Classify(lacquerRoot, e.Path)
	if err != nil {
		r.Error = fmt.Sprintf("audit: %v", err)
		return r
	}
	r.Lacquer = ver.String()
	for _, row := range rows {
		switch row.Status {
		case audit.OK:
			r.Audit.OK++
		case audit.Add:
			r.Audit.Add++
		case audit.Behind:
			r.Audit.Behind++
		case audit.Modified:
			r.Audit.Modified++
		case audit.Conflict:
			r.Audit.Conflict++
		case audit.Untracked:
			r.Audit.Untracked++
		}
	}
	r.Clobbered = audit.Clobbered(rows)

	reports, err := baseline.Run(lacquerRoot, e.Path, cfg.BaselineTargets(), cfg.Baseline.Relax, now)
	if err != nil {
		r.Error = fmt.Sprintf("baseline: %v", err)
		return r
	}
	for _, b := range reports {
		br := BaselineReport{Profile: b.Profile, Component: b.Component, Unchecked: b.Unchecked}
		for _, f := range baseline.Violations(b.Findings) {
			br.Violations = append(br.Violations, f.Key)
		}
		r.Baseline = append(r.Baseline, br)
	}

	findings, err := detect.Drift(lacquerRoot, e.Path, cfg)
	if err != nil {
		r.Error = fmt.Sprintf("detect: %v", err)
		return r
	}
	for _, f := range findings {
		r.Drift = append(r.Drift, DriftFinding{Path: f.Path, Profile: f.Profile, Adoptable: f.Ships})
	}

	suppressed, err := assets.Suppressed(lacquerRoot, cfg)
	if err != nil {
		r.Error = fmt.Sprintf("exclusions: %v", err)
		return r
	}
	if ss, err := suppress.Scan(e.Path); err == nil {
		r.Suppress = suppress.Summarise(ss)
	}

	for _, f := range exclusion.Review(cfg.Project.Exclude, suppressed, now) {
		r.Exclusions = append(r.Exclusions, Exclusion{
			Path: f.Path, Status: string(f.Status), Reason: f.Reason, Until: f.Until, Stale: f.Stale,
		})
	}
	return r
}
