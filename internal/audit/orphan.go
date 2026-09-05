package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitattributes"
	"github.com/patrickserrano/lacquer/internal/gitignore"
	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/region"
)

// Orphan is a managed unit the lacquer used to ship and no longer does, still
// sitting in the project.
//
// Nothing saw these before. Classify derives its unit set from the CURRENT plan,
// so a file the lacquer dropped simply stopped being a unit: not drift, not
// stale, not reported anywhere. And nothing in this tool ever deletes a project
// file — sync's refusal to delete is a deliberate and correct contract, since a
// tool that removes files it no longer recognises is a tool nobody can trust
// with a repository. But not deleting and not TELLING are different decisions,
// and only the first was made. One retired workflow lived on in thirteen
// repositories and came out by hand, one repository at a time.
//
// .lacquer.lock is the evidence: it records what the lacquer wrote last time. A
// key in the lock that no source in the lacquer produces any more is an orphan.
type Orphan struct {
	// Key is the .lacquer.lock key: a destination path, or "<dest>#<marker>"
	// for a managed region.
	Key string
	// Dest is the project-relative file.
	Dest string
	// Region is the marker key when this orphan is a managed region inside a
	// file the project otherwise owns, and "" when the whole file was the
	// lacquer's. The difference is what the operator has to delete.
	Region string
}

// IsRegion reports whether this orphan is a marked region rather than a whole
// file.
func (o Orphan) IsRegion() bool { return o.Region != "" }

// Label is how the orphan is named in a report.
func (o Orphan) Label() string {
	if o.IsRegion() {
		return o.Key
	}
	return o.Dest
}

// Orphans returns every managed unit the lock records that the lacquer would no
// longer produce for this project, and that is still on disk.
//
// Three things are deliberately NOT orphans, and each would make the report
// worse than useless if it were:
//
//   - A path in [project].exclude. The lacquer still ships it; this project
//     opted out. Advising deletion would delete a file that comes straight back
//     the day the exclusion is lifted. internal/exclusion already reports a
//     dead exclusion, which is the finding that actually applies here.
//   - A retired project's dropped scheduled workflows. Same argument, and worse:
//     retirement drops a whole set at once, so treating them as orphans would
//     bury a real finding under a dozen false ones and make retirement unusable.
//     cmd/lacquer/retired_test.go pins that a retired project audits clean.
//   - A unit already removed by hand. There is nothing left to do about it, and
//     the next sync clears the lock entry.
//
// The first two fall out of comparing against assets.Shipped — the plan with
// exclusion and retirement lifted — rather than against the plan itself, so they
// cannot be reintroduced by someone adding a third way to drop a destination
// without also remembering this file.
//
// A project that has never been synced (no lockfile) has no orphans: there is no
// record of what the lacquer wrote, so nothing can be attributed to it.
func Orphans(lacquerRoot, projectRoot string) ([]Orphan, error) {
	cfg, err := config.Load(filepath.Join(projectRoot, ".lacquer.toml"))
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	lk, locked, err := lock.Read(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("read lock: %w", err)
	}
	if !locked {
		return nil, nil
	}
	shipped, err := shippedKeys(lacquerRoot, cfg)
	if err != nil {
		return nil, err
	}
	var out []Orphan
	for key := range lk.Files {
		if shipped[key] {
			continue
		}
		o := orphanFor(key)
		if !filepath.IsLocal(filepath.FromSlash(o.Dest)) {
			// A lockfile is committed and hand-editable, so its keys are not a
			// trusted source of paths. Anything that would read outside the
			// project is dropped rather than followed.
			continue
		}
		if !stillPresent(projectRoot, o) {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// shippedKeys is every lock key the lacquer could produce for this project with
// nothing opted out.
func shippedKeys(lacquerRoot string, cfg *config.Config) (map[string]bool, error) {
	plan, err := assets.Plan(lacquerRoot, cfg)
	if err != nil {
		return nil, fmt.Errorf("plan assets: %w", err)
	}
	// Regions are not filtered by exclusion or retirement, so the ordinary plan
	// is the right input here: it only feeds the .gitignore region's body, which
	// this does not read.
	srcs, err := regions(lacquerRoot, cfg, plan)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(srcs)+len(plan))
	for _, r := range srcs {
		out[regionKey(r.dest, r.key)] = true
	}
	dests, err := assets.Shipped(lacquerRoot, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve shipped assets: %w", err)
	}
	for _, d := range dests {
		out[d] = true
	}
	return out, nil
}

// orphanFor splits a lock key back into its destination and (for a region) its
// marker. The inverse of regionKey.
func orphanFor(key string) Orphan {
	if dest, marker, ok := strings.Cut(key, "#"); ok {
		return Orphan{Key: key, Dest: dest, Region: marker}
	}
	return Orphan{Key: key, Dest: key}
}

// stillPresent reports whether there is anything left for the operator to
// remove: the file for an asset, the marked block for a region.
//
// Checked rather than assumed because the lock outlives the file. Somebody who
// has already deleted a leftover workflow must not keep being told about it
// until the next sync happens to rewrite the lock.
func stillPresent(projectRoot string, o Orphan) bool {
	data, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(o.Dest)))
	if err != nil {
		return false
	}
	if !o.IsRegion() {
		return true
	}
	// .gitignore and .gitattributes are the managed regions that are not
	// markdown. Getting this wrong would silently under-report rather than fail
	// — ExtractBody would look for `<!-- lacquer:...:start -->` in a `#`-comment
	// file, not find it, and the orphan would be dropped as already-removed — so
	// it is asserted in a test rather than left to read correctly.
	syntax := region.Markdown
	switch o.Dest {
	case gitignore.Name:
		syntax = gitignore.Syntax
	case gitattributes.Name:
		syntax = gitattributes.Syntax
	}
	_, found := syntax.ExtractBody(string(data), o.Region)
	return found
}

// FormatOrphans renders the orphan report, or "" when there is nothing to say.
//
// It does NOT gate, and no caller should make it. The repo's own argument for
// reporting a stale exclusion without failing on it applies unchanged: gating on
// something that endangers nothing is the reliable way to teach people that
// lacquer output is noise to be worked around. An orphan is a leftover file. It
// runs — which is exactly why it is worth saying out loud — but a project is not
// broken for having one, and a CI failure is not the way to ask someone to
// delete a file the tool refuses to delete itself.
func FormatOrphans(orphans []Orphan) string {
	if len(orphans) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nno longer shipped by the lacquer, still in this project:\n")
	for _, o := range orphans {
		what := "file"
		if o.IsRegion() {
			what = "managed region"
		}
		fmt.Fprintf(&b, "  %s (%s)\n", o.Label(), what)
	}
	b.WriteString("The lacquer wrote each of these and has stopped producing it — a workflow it retired, " +
		"or a profile or tool this manifest no longer asks for. `sync` never deletes a project file, " +
		"deliberately, so these are yours: delete the file (or just the marked region) if you do not want " +
		"it, or keep it and it becomes ordinary project-owned content. Excluded and retirement-dropped " +
		"paths are not listed here — the lacquer still ships those.\n")
	return b.String()
}
