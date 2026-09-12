package audit_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
)

// refProject builds a plain git repository with the given tracked files and
// returns its root. audit.References only reads `git ls-files` and file
// bodies, so a full lacquer sync is not needed here — orphan_test.go covers
// the Orphans() + sync round trip; this file is about the text-matching
// mechanism inside References() in isolation.
func refProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		writeFile(t, filepath.Join(dir, rel), content)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func refByFile(refs []audit.Reference, file string) (audit.Reference, bool) {
	for _, r := range refs {
		if r.File == file {
			return r, true
		}
	}
	return audit.Reference{}, false
}

// THE #1 FALSE POSITIVE: a plain strings.Contains on the bare basename matched
// "fetch_testflight_feedback.py" inside the UNRELATED, longer filename
// "test_fetch_testflight_feedback.py" — reporting the orphan referenced by a
// file whose only "mention" is inside a different name entirely.
//
// MUTATION: reverting findNeedleLine (or referenceNeedles) to plain
// strings.Contains makes this test fail — it wrongly finds ".pre-commit-config.yaml".
func TestSubstringInsideALongerFilenameIsNotAReference(t *testing.T) {
	project := refProject(t, map[string]string{
		".pre-commit-config.yaml": "repos:\n  - hooks:\n" +
			"      - entry: python3 .github/scripts/test_fetch_testflight_feedback.py\n",
		".github/scripts/test_fetch_testflight_feedback.py": "import sys\n",
	})
	o := audit.Orphan{Key: ".github/scripts/fetch_testflight_feedback.py", Dest: ".github/scripts/fetch_testflight_feedback.py"}

	refs := audit.References(project, o)
	if len(refs) != 0 {
		t.Fatalf(".github/scripts/fetch_testflight_feedback.py falsely reported as referenced, matching "+
			"only as a substring of the different file test_fetch_testflight_feedback.py: %+v", refs)
	}
}

// A real caller of the same script (a genuine whole-path mention) must still
// be found — the boundary fix must not overcorrect into missing real callers.
func TestFullPathIsStillFoundAsACaller(t *testing.T) {
	project := refProject(t, map[string]string{
		".pre-commit-config.yaml": "repos:\n  - hooks:\n" +
			"      - entry: python3 .github/scripts/fetch_testflight_feedback.py\n",
	})
	o := audit.Orphan{Key: ".github/scripts/fetch_testflight_feedback.py", Dest: ".github/scripts/fetch_testflight_feedback.py"}

	refs := audit.References(project, o)
	r, ok := refByFile(refs, ".pre-commit-config.yaml")
	if !ok {
		t.Fatalf("a genuine full-path mention was not found: %+v", refs)
	}
	if r.Kind != "path" || r.Line != 3 {
		t.Errorf("got %+v, want kind=path line=3", r)
	}
}

// THE ROCKETSIM SHAPE: SKILL.md is shipped once per skill (97 times fleet-wide
// in this lacquer's own tree), so a bare "SKILL.md" mention in one skill's own
// reference doc — narrating ITS OWN SKILL.md, unrelated to any other skill —
// used to report every such doc as a referencer of every orphaned SKILL.md.
// The real case: .agents/skills/ad-creative/references/creative-review-page.md
// line 30 says `// each concept is one strategic ANGLE (see SKILL.md "Define
// Your Angles")`, which has nothing to do with rocketsim.
//
// A basename shared by more than one tracked file (genericBasenames) must
// therefore not be usable bare; only the full path or a parent-qualified form
// ("rocketsim/SKILL.md") counts.
//
// MUTATION: removing the generic-basename check in referenceNeedles (always
// adding the bare basename needle) makes this test fail — it wrongly finds
// notes.md.
func TestGenericBasenameOnlyMatchesQualifiedOrFullPath(t *testing.T) {
	project := refProject(t, map[string]string{
		".agents/skills/rocketsim/SKILL.md": "# rocketsim\n",
		".agents/skills/other/SKILL.md":     "# other\n",
		".agents/skills/other/references/creative-review-page.md": "line one\nline two\n" +
			"each concept is one strategic ANGLE (see SKILL.md \"Define Your Angles\")\n",
		"workflow.yml": "run: cat rocketsim/SKILL.md\n",
	})
	o := audit.Orphan{Key: ".agents/skills/rocketsim/SKILL.md", Dest: ".agents/skills/rocketsim/SKILL.md"}

	refs := audit.References(project, o)

	if _, ok := refByFile(refs, ".agents/skills/other/references/creative-review-page.md"); ok {
		t.Fatalf("a bare, generic \"SKILL.md\" mention in an unrelated skill's own reference doc was "+
			"reported as a reference: %+v", refs)
	}
	r, ok := refByFile(refs, "workflow.yml")
	if !ok {
		t.Fatalf("a parent-qualified mention (rocketsim/SKILL.md) was not found: %+v", refs)
	}
	if r.Kind != "qualified" || r.Needle != "rocketsim/SKILL.md" || r.Line != 1 {
		t.Errorf("got %+v, want kind=qualified needle=\"rocketsim/SKILL.md\" line=1", r)
	}
}

// A basename that is NOT shared by any other tracked file is unambiguous, so
// the bare form is still used — this is the case genericBasenames must not
// over-apply to.
func TestUniqueBasenameStillMatchesBare(t *testing.T) {
	project := refProject(t, map[string]string{
		"notes.md": "line one\nsee docs-relaxation.sh for details\n",
	})
	o := audit.Orphan{Key: "scripts/docs-relaxation.sh", Dest: "scripts/docs-relaxation.sh"}

	refs := audit.References(project, o)
	r, ok := refByFile(refs, "notes.md")
	if !ok {
		t.Fatalf("a unique bare basename mention was not found: %+v", refs)
	}
	if r.Kind != "basename" || r.Line != 2 {
		t.Errorf("got %+v, want kind=basename line=2", r)
	}
}

// THE URL SHAPE: a needle appearing inside a documentation link is not a
// reference to a repo file of the same name.
//
// MUTATION: removing the URL exclusion in findNeedleLine makes this test
// fail — it wrongly finds docs/links.md.
func TestNeedleInsideAURLIsNotAReference(t *testing.T) {
	project := refProject(t, map[string]string{
		"docs/links.md": "See https://example.com/scripts/foo.sh for details.\n",
	})
	o := audit.Orphan{Key: "scripts/foo.sh", Dest: "scripts/foo.sh"}

	refs := audit.References(project, o)
	if len(refs) != 0 {
		t.Fatalf("a needle inside a URL was reported as a reference: %+v", refs)
	}
}

// A needle that looks like the URL case but is NOT inside one (same path,
// mentioned in ordinary text after the URL) must still be found.
func TestNeedleAfterAURLOnTheSameLineStillMatches(t *testing.T) {
	project := refProject(t, map[string]string{
		"docs/links.md": "See https://example.com/docs — also run scripts/foo.sh locally.\n",
	})
	o := audit.Orphan{Key: "scripts/foo.sh", Dest: "scripts/foo.sh"}

	refs := audit.References(project, o)
	if _, ok := refByFile(refs, "docs/links.md"); !ok {
		t.Fatalf("a real mention on the same line as an unrelated URL was missed: %+v", refs)
	}
}

// True-positive shapes that must survive the boundary fix: a pre-commit
// entry, a shell `source`, a `run:` with a leading "./", and a bare path in a
// YAML list.
//
// MUTATION: requiring a leading "./" (or otherwise over-tightening the
// boundary check) makes "bare path in YAML list" fail, since it has none.
func TestRealCallerShapesStillMatch(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantLine int
	}{
		{"pre-commit entry", "repos:\n  - hooks:\n      - entry: scripts/x.sh\n", 3},
		{"shell source", "#!/bin/bash\nsource scripts/x.sh\n", 2},
		{"run with leading dot-slash", "pre-commit:\n  commands:\n    x:\n      run: ./scripts/x.sh\n", 4},
		{"bare path in a YAML list", "steps:\n  - scripts/x.sh\n", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			project := refProject(t, map[string]string{"caller.yml": c.content})
			o := audit.Orphan{Key: "scripts/x.sh", Dest: "scripts/x.sh"}

			refs := audit.References(project, o)
			if len(refs) != 1 {
				t.Fatalf("got %d reference(s), want 1: %+v", len(refs), refs)
			}
			if refs[0].Line != c.wantLine {
				t.Errorf("line = %d, want %d", refs[0].Line, c.wantLine)
			}
			if refs[0].Kind != "path" {
				t.Errorf("kind = %s, want path", refs[0].Kind)
			}
		})
	}
}

// A needle embedded in a longer, unrelated FILENAME with no separator (the
// general form of the fetch_testflight_feedback.py shape: "x.sh" must not
// match inside the different file "notx.sh") must not match. This is
// different from a different DIRECTORY ending in the same basename (see
// TestUniqueBasenameStillMatchesBare's sibling case in
// TestRealCallerShapesStillMatch), which the over-reporting bias deliberately
// still catches.
func TestNeedleGluedToALongerFilenameIsNotAMatch(t *testing.T) {
	project := refProject(t, map[string]string{
		"notes.md": "see scripts/notx.sh for something unrelated\n",
	})
	o := audit.Orphan{Key: "scripts/x.sh", Dest: "scripts/x.sh"}

	refs := audit.References(project, o)
	if len(refs) != 0 {
		t.Fatalf("scripts/x.sh matched glued inside the unrelated filename notx.sh: %+v", refs)
	}
}

// PROSE ANNOTATION: CLAUDE.md/AGENTS.md may legitimately narrate a path in
// prose without that being a live caller. Kept (over-reporting bias), but
// marked distinctly rather than either dropped or left indistinguishable from
// a real caller.
func TestRuleFileMentionIsAnnotatedAsProse(t *testing.T) {
	project := refProject(t, map[string]string{
		"CLAUDE.md":    "line one\nscripts/build-docs.sh was left with no caller and has now been unshipped\n",
		"AGENTS.md":    "line one\nscripts/build-docs.sh was left with no caller and has now been unshipped\n",
		"workflow.yml": "line one\nrun: scripts/build-docs.sh\n",
	})
	o := audit.Orphan{Key: "scripts/build-docs.sh", Dest: "scripts/build-docs.sh"}

	refs := audit.References(project, o)
	claude, ok := refByFile(refs, "CLAUDE.md")
	if !ok || !claude.Prose {
		t.Errorf("CLAUDE.md mention not marked Prose: %+v", refs)
	}
	agents, ok := refByFile(refs, "AGENTS.md")
	if !ok || !agents.Prose {
		t.Errorf("AGENTS.md mention not marked Prose: %+v", refs)
	}
	wf, ok := refByFile(refs, "workflow.yml")
	if !ok || wf.Prose {
		t.Errorf("workflow.yml mention wrongly marked Prose: %+v", refs)
	}
}

// Reference.String is the annotation format: "<file>:<line> (<kind>
// \"<needle>\")", with ", doc mention" appended for a prose match. This is
// what makes a look-alike self-evident from the report line alone.
//
// MUTATION: dropping the needle/line from String() (reverting it to just
// r.File) makes this test fail.
func TestReferenceStringFormat(t *testing.T) {
	cases := []struct {
		ref  audit.Reference
		want string
	}{
		{
			audit.Reference{File: ".pre-commit-config.yaml", Line: 113, Needle: ".github/scripts/x.py", Kind: "path"},
			`.pre-commit-config.yaml:113 (path ".github/scripts/x.py")`,
		},
		{
			audit.Reference{File: "creative-review-page.md", Line: 30, Needle: "SKILL.md", Kind: "basename"},
			`creative-review-page.md:30 (basename "SKILL.md")`,
		},
		{
			audit.Reference{File: "CLAUDE.md", Line: 341, Needle: "scripts/build-docs.sh", Kind: "path", Prose: true},
			`CLAUDE.md:341 (path "scripts/build-docs.sh", doc mention)`,
		},
	}
	for _, c := range cases {
		if got := c.ref.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

// FormatOrphansWithRefs must surface the needle+line annotation per reference,
// and call out an orphan whose every reference is a doc mention — without
// dropping the reference or weakening the DO NOT DELETE warning for a mixed
// case.
func TestFormatOrphansWithRefsAnnotatesNeedleAndLine(t *testing.T) {
	orphans := []audit.Orphan{{Key: "scripts/build-docs.sh", Dest: "scripts/build-docs.sh"}}
	refs := map[string][]audit.Reference{
		"scripts/build-docs.sh": {
			{File: "AGENTS.md", Line: 341, Needle: "scripts/build-docs.sh", Kind: "path", Prose: true},
			{File: "CLAUDE.md", Line: 341, Needle: "scripts/build-docs.sh", Kind: "path", Prose: true},
		},
	}
	out := audit.FormatOrphansWithRefs(orphans, refs)

	if !strings.Contains(out, `CLAUDE.md:341 (path "scripts/build-docs.sh", doc mention)`) {
		t.Errorf("missing annotated CLAUDE.md reference:\n%s", out)
	}
	if !strings.Contains(out, "every match above is a doc mention") {
		t.Errorf("missing all-prose callout:\n%s", out)
	}
	if !strings.Contains(out, "DO NOT delete anything marked STILL REFERENCED") {
		t.Errorf("all-prose callout must not remove the DO NOT DELETE warning:\n%s", out)
	}
}

func TestFormatOrphansWithRefsDoesNotFlagAMixedOrphanAsAllProse(t *testing.T) {
	orphans := []audit.Orphan{{Key: "scripts/x.sh", Dest: "scripts/x.sh"}}
	refs := map[string][]audit.Reference{
		"scripts/x.sh": {
			{File: "CLAUDE.md", Line: 5, Needle: "scripts/x.sh", Kind: "path", Prose: true},
			{File: "workflow.yml", Line: 2, Needle: "scripts/x.sh", Kind: "path", Prose: false},
		},
	}
	out := audit.FormatOrphansWithRefs(orphans, refs)
	if strings.Contains(out, "every match above is a doc mention") {
		t.Errorf("a mix of a real caller and a doc mention must not be reported as all-prose:\n%s", out)
	}
}
