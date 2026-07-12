// Package audit detects how a project has diverged from what the harness would
// produce now, and classifies each managed unit so deviations are scrutinized
// (adopted up, reset down, or accepted) instead of silently overwritten.
//
// It is the read side of the bidirectional loop: sync pushes the baseline down;
// audit surfaces where a project has pushed back. Classification is three-way —
// the project's on-disk content, the .harness.lock baseline (what harness last
// wrote), and what harness would render now — which is what tells "the project
// edited this" apart from "the harness moved on".
package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/harness/internal/assets"
	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/lock"
	"github.com/patrickserrano/harness/internal/region"
	"github.com/patrickserrano/harness/internal/tokens"
	"github.com/patrickserrano/harness/internal/version"
)

// Status is the classification of one managed unit.
type Status string

const (
	OK        Status = "ok"               // on-disk matches what harness would write now
	Add       Status = "add"              // harness has it; the project doesn't (sync would create it)
	Behind    Status = "behind"           // project matches the lock; harness advanced (sync updates it)
	Modified  Status = "locally-modified" // project changed from the lock; harness didn't (a deviation)
	Conflict  Status = "conflict"         // project AND harness both changed from the lock
	Untracked Status = "untracked"        // differs from harness-now, but no lock baseline to attribute it
)

// Clobbers reports whether syncing over this status would overwrite a local
// change the harness did not make. Only Modified and Conflict qualify — they are
// detectable only with a lock baseline, so an Untracked (no-lock) project never
// blocks and the lock simply bootstraps on the next sync.
func (s Status) Clobbers() bool { return s == Modified || s == Conflict }

// Row is one unit's audit result.
type Row struct {
	Dest    string // project-relative destination path
	Kind    string // "region" or "asset"
	Detail  string // region marker key, or "" for assets
	Status  Status
	Stamped int // region: version in the on-disk start marker (0 if absent/asset)
}

// unit is one thing the harness manages: a region body merged into a file, or a
// whole-file asset. Content is what the harness would write now (post-token).
type unit struct {
	lockKey   string // ".harness.lock" key
	dest      string // project-relative path
	kind      string // "region" | "asset"
	regionKey string // marker key (regions only)
	content   string // rendered content the harness would produce
}

// managed re-derives every unit the harness would write for this project: the
// core + per-profile CLAUDE.md regions (mirrored into AGENTS.md when a tool that
// reads it is enabled), then the whole-file assets. It mirrors sync's set exactly
// so the lock written by sync and the units audited here line up.
func managed(harnessRoot, projectRoot string) ([]unit, int, error) {
	ver, err := version.Read(harnessRoot)
	if err != nil {
		return nil, 0, fmt.Errorf("read version: %w", err)
	}
	cfg, err := config.Load(filepath.Join(projectRoot, ".harness.toml"))
	if err != nil {
		return nil, 0, fmt.Errorf("load manifest: %w", err)
	}

	type regionSrc struct {
		dest, key, body, prefix string
	}
	var srcs []regionSrc
	coreBody, err := os.ReadFile(filepath.Join(harnessRoot, "core", "CLAUDE.core.md"))
	if err != nil {
		return nil, 0, fmt.Errorf("read core body: %w", err)
	}
	srcs = append(srcs, regionSrc{"CLAUDE.md", "core", string(coreBody), ""})
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			body, err := os.ReadFile(filepath.Join(harnessRoot, "profiles", p, "CLAUDE."+p+".md"))
			if err != nil {
				return nil, 0, fmt.Errorf("read profile %s body: %w", p, err)
			}
			srcs = append(srcs, regionSrc{filepath.Join(c.Path, "CLAUDE.md"), p, string(body), tokens.Prefix(c.Path)})
		}
	}
	if cfg.Project.WantsAgentsMd() {
		mirror := make([]regionSrc, 0, len(srcs))
		for _, r := range srcs {
			m := r
			m.dest = filepath.Join(filepath.Dir(r.dest), "AGENTS.md")
			mirror = append(mirror, m)
		}
		srcs = append(srcs, mirror...)
	}

	var units []unit
	for _, r := range srcs {
		body, _ := tokens.Substitute(r.body, tokens.Values(cfg.Project, r.prefix))
		units = append(units, unit{
			lockKey:   r.dest + "#" + r.key,
			dest:      r.dest,
			kind:      "region",
			regionKey: r.key,
			content:   body,
		})
	}

	plan, err := assets.Plan(harnessRoot, cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("plan assets: %w", err)
	}
	for _, a := range plan {
		data, err := os.ReadFile(a.Src)
		if err != nil {
			return nil, 0, fmt.Errorf("read asset %s: %w", a.Src, err)
		}
		content, _ := tokens.Substitute(string(data), tokens.Values(cfg.Project, a.Prefix))
		units = append(units, unit{lockKey: a.Dest, dest: a.Dest, kind: "asset", content: content})
	}
	return units, ver, nil
}

// Classify audits projectRoot against harnessRoot, returning one Row per managed
// unit sorted by destination. The current version is returned for reporting.
func Classify(harnessRoot, projectRoot string) ([]Row, int, error) {
	units, ver, err := managed(harnessRoot, projectRoot)
	if err != nil {
		return nil, 0, err
	}
	lk, locked, err := lock.Read(projectRoot)
	if err != nil {
		return nil, 0, fmt.Errorf("read lock: %w", err)
	}

	rows := make([]Row, 0, len(units))
	for _, u := range units {
		row := Row{Dest: u.dest, Kind: u.kind, Detail: u.regionKey}
		onDisk, present, stamped := readUnit(projectRoot, u)
		row.Stamped = stamped

		harnessHash := lock.Hash(u.content)
		switch {
		case !present:
			row.Status = Add
		case lock.Hash(onDisk) == harnessHash:
			row.Status = OK
		default:
			row.Status = classifyDivergence(lk, locked, u.lockKey, lock.Hash(onDisk), harnessHash)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Dest != rows[j].Dest {
			return rows[i].Dest < rows[j].Dest
		}
		return rows[i].Detail < rows[j].Detail
	})
	return rows, ver, nil
}

// classifyDivergence resolves a unit whose on-disk content differs from what the
// harness would write now, using the lock baseline to attribute the change.
func classifyDivergence(lk *lock.Lock, locked bool, key, projectHash, harnessHash string) Status {
	if !locked {
		return Untracked
	}
	base, ok := lk.Files[key]
	if !ok {
		return Untracked // baseline exists but never recorded this unit
	}
	switch {
	case projectHash == base:
		return Behind // project untouched since the lock; harness advanced
	case harnessHash == base:
		return Modified // project changed; harness did not
	default:
		return Conflict // both changed from the baseline
	}
}

// readUnit returns the project's current content for u, whether it is present,
// and (for regions) the stamped version. A region's content is the body between
// its markers; an asset's content is the whole file.
func readUnit(projectRoot string, u unit) (content string, present bool, stamped int) {
	data, err := os.ReadFile(filepath.Join(projectRoot, u.dest))
	if err != nil {
		return "", false, 0
	}
	if u.kind == "region" {
		body, found := region.ExtractBody(string(data), u.regionKey)
		if !found {
			return "", false, 0
		}
		v, _ := region.StampedVersion(string(data), u.regionKey)
		return body, true, v
	}
	return string(data), true, 0
}

// LockFor builds the lockfile contents for projectRoot from what the harness
// would write now. sync calls this after a successful write so the baseline
// reflects exactly what landed on disk.
func LockFor(harnessRoot, projectRoot string) (*lock.Lock, error) {
	units, ver, err := managed(harnessRoot, projectRoot)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(units))
	for _, u := range units {
		files[u.lockKey] = lock.Hash(u.content)
	}
	return &lock.Lock{Version: ver, Files: files}, nil
}

// Clobbered returns the destinations whose sync would overwrite a local change
// (Modified/Conflict). sync uses it to refuse without --force.
func Clobbered(rows []Row) []string {
	var out []string
	for _, r := range rows {
		if r.Status.Clobbers() {
			out = append(out, r.Dest)
		}
	}
	return out
}

// statusOrder is the report order: most-actionable first.
var statusOrder = []Status{Conflict, Modified, Untracked, Behind, Add, OK}

// statusNote explains what each status means for the operator.
var statusNote = map[Status]string{
	Conflict:  "both you and the harness changed it — reconcile",
	Modified:  "you changed it, the harness didn't — adopt up or reset with --force",
	Untracked: "differs, but no lock baseline yet — re-sync to start tracking",
	Behind:    "harness advanced — sync updates it",
	Add:       "harness has it, project doesn't — sync creates it",
	OK:        "matches the harness",
}

// Format renders a human-readable audit report: a per-status summary, then the
// destinations under each non-OK status (most-actionable first).
func Format(rows []Row, ver int) string {
	groups := map[Status][]Row{}
	for _, r := range rows {
		groups[r.Status] = append(groups[r.Status], r)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "harness audit — project vs harness v%d\n\n", ver)
	for _, s := range statusOrder {
		fmt.Fprintf(&b, "  %-16s %3d   %s\n", s, len(groups[s]), statusNote[s])
	}
	for _, s := range statusOrder {
		if s == OK || len(groups[s]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s:\n", s)
		for _, r := range groups[s] {
			label := r.Dest
			if r.Kind == "region" {
				label = fmt.Sprintf("%s#%s", r.Dest, r.Detail)
			}
			fmt.Fprintf(&b, "  %s\n", label)
		}
	}
	if n := len(Clobbered(rows)); n > 0 {
		fmt.Fprintf(&b, "\n%d unit(s) would be overwritten by sync — review before adopting (sync --force) or promote into the harness.\n", n)
	}
	return b.String()
}
