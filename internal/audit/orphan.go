package audit

import (
	"fmt"
	"os"
	"os/exec"
	"path"
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
	return FormatOrphansWithRefs(orphans, nil)
}

// FormatOrphansWithRefs renders the orphan report, annotating each entry with
// the project files that still reference it.
//
// The annotation exists because the unannotated wording actively misled people.
// "No longer shipped by the lacquer" means UNMANAGED and reads as DEAD. Three
// separate readers reached "retired, safe to delete" from that line inside one
// day, for `scripts/build-docs.sh` — a file `ios-docs.yml` runs in CI and
// `.pre-commit-config.yaml` runs on every commit, in eight repositories. The
// report was accurate and the conclusion it produced would have broken working
// pipelines, which makes it the report's problem.
//
// So the decisive fact goes on the line itself, not in prose underneath it.
func FormatOrphansWithRefs(orphans []Orphan, refs map[string][]string) string {
	if len(orphans) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nno longer managed by the lacquer, still in this project:\n")
	anyReferenced := false
	for _, o := range orphans {
		what := "file"
		if o.IsRegion() {
			what = "managed region"
		}
		r := refs[o.Key]
		switch {
		case o.IsRegion():
			fmt.Fprintf(&b, "  %s (%s)\n", o.Label(), what)
		case len(r) > 0:
			anyReferenced = true
			shown := r
			extra := ""
			if len(shown) > 3 {
				extra = fmt.Sprintf(" +%d more", len(shown)-3)
				shown = shown[:3]
			}
			fmt.Fprintf(&b, "  %s (%s) — STILL REFERENCED by %s%s\n",
				o.Label(), what, strings.Join(shown, ", "), extra)
		default:
			fmt.Fprintf(&b, "  %s (%s) — referenced by nothing tracked\n", o.Label(), what)
		}
	}
	b.WriteString("The lacquer wrote each of these and has stopped producing it — a workflow it retired, " +
		"or a profile or tool this manifest no longer asks for. It means UNMANAGED, not unused. " +
		"`sync` never deletes a project file, deliberately, so these are yours to keep or remove.\n")
	if anyReferenced {
		b.WriteString("DO NOT delete anything marked STILL REFERENCED without following the reference first: " +
			"the lacquer stopped shipping it, but something in this project still runs it, and removing it " +
			"breaks that caller.\n")
	}
	b.WriteString("The reference list is a text search of tracked files, so read WHICH files it names: " +
		"a `.github/workflows/*.yml` or `.pre-commit-config.yaml` hit is a caller, while a CLAUDE.md or " +
		"AGENTS.md hit is prose mentioning the path and does not keep the file alive. Over-reporting is " +
		"deliberate — an extra finding costs a look, a missed one costs working CI.\n" +
		"\"Referenced by nothing tracked\" is the safe-to-delete case, but it is a search, not proof: a " +
		"caller that builds the path dynamically will not be found.\n" +
		"Excluded and retirement-dropped paths are not listed here — the lacquer still ships those.\n")
	return b.String()
}

// References returns the project files that mention this orphan's path, so the
// report can tell "nothing calls this, delete it" apart from "this is still
// wired into your CI".
//
// The distinction is the whole point. "No longer shipped by the lacquer" means
// UNMANAGED, and it reads as DEAD. Three separate readers drew the wrong
// conclusion from that line within one day and were a `rm` away from deleting
// `scripts/build-docs.sh` out of eight repositories — where `ios-docs.yml`
// runs it in CI and `.pre-commit-config.yaml` runs it on every commit. An
// orphan that something still calls is not a leftover; it is project-owned
// content the project depends on.
//
// Searches tracked files only, via `git ls-files`, because the interesting
// callers are committed ones and an untracked scratch file mentioning the path
// is not a dependency. Matches the destination path and its basename: a
// workflow says `scripts/build-docs.sh`, while a sibling script may say
// `./build-docs.sh`, and missing the second would report "unreferenced" for a
// file with a live caller — the failure direction that loses working CI.
func References(projectRoot string, o Orphan) []string {
	if o.IsRegion() {
		// A region lives inside a file the project owns and keeps. There is no
		// path for anything to reference, so the question does not apply.
		return nil
	}
	tracked, err := trackedFiles(projectRoot)
	if err != nil {
		// Not a git repo, or git unavailable. Report nothing rather than
		// claiming "unreferenced" — an unverified all-clear on this question is
		// exactly what gets a live file deleted.
		return nil
	}
	needles := []string{o.Dest}
	if base := path.Base(o.Dest); base != o.Dest && base != "" {
		needles = append(needles, base)
	}

	var out []string
	for _, rel := range tracked {
		if rel == o.Dest {
			continue // the file itself
		}
		if rel == lock.Name {
			continue // the lock records what was written; that is not a caller
		}
		data, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		body := string(data)
		for _, n := range needles {
			if strings.Contains(body, n) {
				out = append(out, rel)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// trackedFiles lists the project's git-tracked paths.
func trackedFiles(projectRoot string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, filepath.ToSlash(f))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no tracked files")
	}
	return files, nil
}
