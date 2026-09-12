package audit

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
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
//
// Each shown reference is now annotated with WHICH needle matched and the
// first line it matched on (Reference.String) — "creative-review-page.md:30
// (basename \"SKILL.md\")" versus ".pre-commit-config.yaml:113 (path
// \".github/scripts/x.py\")" — so a look-alike match is self-evident from the
// report line itself rather than costing a trip to the file to find out which
// of "path", "qualified" or "basename" actually fired, and where. A prose
// mention in a shipped rule file gets ", doc mention" appended rather than
// being dropped or unmarked, keeping the over-reporting bias intact while
// still letting a reader weigh a CLAUDE.md narration differently from a
// workflow that actually runs the path.
func FormatOrphansWithRefs(orphans []Orphan, refs map[string][]Reference) string {
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
			shown := make([]string, 0, len(r))
			for _, ref := range r {
				shown = append(shown, ref.String())
			}
			extra := ""
			if len(shown) > 3 {
				extra = fmt.Sprintf(" +%d more", len(shown)-3)
				shown = shown[:3]
			}
			fmt.Fprintf(&b, "  %s (%s) — STILL REFERENCED by %s%s\n",
				o.Label(), what, strings.Join(shown, ", "), extra)
			if allProse(r) {
				fmt.Fprintf(&b, "    every match above is a doc mention in a shipped rule file "+
					"(CLAUDE.md/AGENTS.md) — no non-prose caller was found, but this is a text search, "+
					"not proof: verify before deleting.\n")
			}
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
	b.WriteString("Each reference is \"<file>:<line> (<kind> \\\"<needle>\\\")\": a `.github/workflows/*.yml` " +
		"or `.pre-commit-config.yaml` hit is a caller, while a \", doc mention\" tag inside the parens marks " +
		"a CLAUDE.md or AGENTS.md hit — prose mentioning the path, which does not by itself keep the file " +
		"alive. Over-reporting is deliberate — an extra finding costs a look, a missed one costs working CI.\n" +
		"\"Referenced by nothing tracked\" is the safe-to-delete case, but it is a search, not proof: a " +
		"caller that builds the path dynamically will not be found.\n" +
		"Excluded and retirement-dropped paths are not listed here — the lacquer still ships those.\n")
	return b.String()
}

// allProse reports whether every reference in refs is a rule-file prose
// mention rather than a caller.
func allProse(refs []Reference) bool {
	for _, r := range refs {
		if !r.Prose {
			return false
		}
	}
	return len(refs) > 0
}

// Reference is one tracked project file that mentions an orphan's path, with
// enough detail to judge the match without re-deriving it: WHICH needle
// matched (the full path, a parent-qualified form, or the bare basename) and
// the first line it matched on.
//
// The needle+line pair is not decoration. `.agents/skills/ad-creative/
// references/creative-review-page.md` was reported as a referencer of the
// orphan `.agents/skills/rocketsim/SKILL.md` for one reason only: line 30 says
// `see SKILL.md "Define Your Angles"`, a skill's reference doc mentioning its
// OWN SKILL.md, nothing to do with rocketsim. Read the bare finding
// ("creative-review-page.md") and that reads as a real caller; read
// `creative-review-page.md:30 (basename "SKILL.md")` and the false positive is
// obvious without opening the file. That is what taking a needle and a line
// number buys: a wrong theory doesn't survive being told the exact word and
// place a match came from.
type Reference struct {
	// File is the project-relative path of the referencing file.
	File string
	// Line is the 1-based line of the first valid match.
	Line int
	// Needle is the exact text that matched.
	Needle string
	// Kind is "path" (the orphan's full destination), "qualified" (a
	// parent-directory-qualified form of a too-generic basename, e.g.
	// "rocketsim/SKILL.md"), or "basename" (the orphan's bare filename, used
	// only when that filename is not shared by any other tracked file).
	Kind string
	// Prose is true when File is a shipped rule file (CLAUDE.md / AGENTS.md)
	// whose own text may legitimately tell a reader to run a path in prose,
	// which is not the same thing as a workflow or hook actually running it.
	Prose bool
}

// String renders one reference for the report: "<file>:<line> (<kind>
// \"<needle>\")", with ", doc mention" appended for a rule-file prose match.
func (r Reference) String() string {
	// The ", doc mention" tag goes INSIDE the parenthetical, not appended after
	// a comma at the top level — FormatOrphansWithRefs joins references with
	// ", ", and a trailing ", doc mention" outside the parens reads as its own
	// list item ("AGENTS.md:852 (...), doc mention, CLAUDE.md:852 (...)"),
	// which a real fleet dry-run against ios-claude.yml's references showed
	// looks like three items instead of two annotated ones.
	if r.Prose {
		return fmt.Sprintf("%s:%d (%s %q, doc mention)", r.File, r.Line, r.Kind, r.Needle)
	}
	return fmt.Sprintf("%s:%d (%s %q)", r.File, r.Line, r.Kind, r.Needle)
}

// referenceNeedle is one substring References searches for, and the label a
// match on it gets in the report.
type referenceNeedle struct {
	text, kind string
}

// genericBasenames returns every basename shared by two or more of the given
// tracked files. Sharing a name with another file in the SAME project is what
// makes a bare basename unable to identify one of them — SKILL.md, README.md,
// LICENSE and index.md all fall out of this in any project with more than one
// directory carrying one, without hardcoding those four names (or anything
// else this project's own layout happens to repeat) as a fixed list some
// other project's layout might not share.
func genericBasenames(tracked []string) map[string]bool {
	count := map[string]int{}
	for _, f := range tracked {
		count[path.Base(f)]++
	}
	out := map[string]bool{}
	for b, c := range count {
		if c > 1 {
			out[b] = true
		}
	}
	return out
}

// referenceNeedles returns the needles searched for an orphan at dest, given
// which basenames this project's own file set has made too generic to stand
// alone (see genericBasenames).
//
// The full destination is always a needle. Its bare basename is a SECOND
// needle only when nothing else in the project answers to that name; when the
// basename is generic, the bare form is dropped (that is the fix — bare
// "SKILL.md" cannot tell rocketsim's SKILL.md apart from any other skill's)
// and a parent-directory-qualified form is used instead, when the orphan has a
// parent directory to qualify with.
func referenceNeedles(dest string, generic map[string]bool) []referenceNeedle {
	needles := []referenceNeedle{{text: dest, kind: "path"}}
	base := path.Base(dest)
	if base == "" || base == dest {
		return needles
	}
	if !generic[base] {
		needles = append(needles, referenceNeedle{text: base, kind: "basename"})
		return needles
	}
	if parent := path.Base(path.Dir(dest)); parent != "" && parent != "." && parent != "/" {
		needles = append(needles, referenceNeedle{text: parent + "/" + base, kind: "qualified"})
	}
	return needles
}

// isPathByte reports whether b can be part of a bare filename — used to keep a
// needle from matching as a substring of a longer name it merely happens to
// sit inside of.
func isPathByte(b byte) bool {
	return b == '_' || b == '.' || b == '-' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// referenceURL matches a URL, so a needle that happens to appear inside one
// (a documentation link to https://rocketsim.app/SKILL.md, say) is not counted
// as a reference to a repo file of the same name.
var referenceURL = regexp.MustCompile(`https?://\S+`)

// findNeedleLine returns the 1-based line number of the first valid match of
// needle in body, or 0 if there is none.
//
// A match must stand as a whole path or filename — not preceded or followed by
// another filename character, so "fetch_testflight_feedback.py" no longer
// matches inside the unrelated "test_fetch_testflight_feedback.py" — and must
// not fall inside a URL.
func findNeedleLine(body, needle string) int {
	if needle == "" {
		return 0
	}
	for i, line := range strings.Split(body, "\n") {
		urlSpans := referenceURL.FindAllStringIndex(line, -1)
		start := 0
		for {
			idx := strings.Index(line[start:], needle)
			if idx < 0 {
				break
			}
			pos := start + idx
			end := pos + len(needle)
			start = pos + 1

			if pos > 0 && isPathByte(line[pos-1]) {
				continue
			}
			if end < len(line) && isPathByte(line[end]) {
				continue
			}
			inURL := false
			for _, sp := range urlSpans {
				if pos >= sp[0] && pos < sp[1] {
					inURL = true
					break
				}
			}
			if inURL {
				continue
			}
			return i + 1
		}
	}
	return 0
}

// kindPriority breaks a tie when two needles match on the same line: the more
// specific needle (the full path) is the more informative one to report.
func kindPriority(kind string) int {
	switch kind {
	case "path":
		return 2
	case "qualified":
		return 1
	default:
		return 0
	}
}

// isProseRuleFile reports whether rel is a shipped rule file whose own text
// may legitimately narrate a path without that being a live caller. Checked by
// basename so a component-nested CLAUDE.md counts the same as a root one.
func isProseRuleFile(rel string) bool {
	b := path.Base(rel)
	return b == "CLAUDE.md" || b == "AGENTS.md"
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
// content the project depends on. That bias toward over-reporting is
// deliberate and this function keeps it: it never drops a match because it
// looks inconvenient, including a CLAUDE.md/AGENTS.md prose mention (annotated
// via Reference.Prose, not silently dropped).
//
// What changed is which NAME gets reported for a real match. A needle used to
// be "the full destination, or its bare basename" with a plain
// strings.Contains — which reported `.agents/skills/rocketsim/SKILL.md` as
// referenced by any file anywhere that happened to say the word "SKILL.md" in
// prose about its OWN skill, and reported the orphan
// `.github/scripts/fetch_testflight_feedback.py` as referenced by
// `.pre-commit-config.yaml` for a match that was actually inside the
// DIFFERENT filename `test_fetch_testflight_feedback.py`. See
// referenceNeedles and findNeedleLine for the fix: a needle must match as a
// whole path or filename, not inside a URL, and a basename shared by more than
// one tracked file is never used bare.
//
// Searches tracked files only, via `git ls-files`, because the interesting
// callers are committed ones and an untracked scratch file mentioning the path
// is not a dependency.
func References(projectRoot string, o Orphan) []Reference {
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
	needles := referenceNeedles(o.Dest, genericBasenames(tracked))

	var out []Reference
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

		var best Reference
		found := false
		for _, n := range needles {
			line := findNeedleLine(body, n.text)
			if line == 0 {
				continue
			}
			if !found || line < best.Line || (line == best.Line && kindPriority(n.kind) > kindPriority(best.Kind)) {
				best = Reference{File: rel, Line: line, Needle: n.text, Kind: n.kind}
				found = true
			}
		}
		if found {
			best.Prose = isProseRuleFile(rel)
			out = append(out, best)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
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
