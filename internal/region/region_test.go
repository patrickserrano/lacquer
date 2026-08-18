package region

import (
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/version"
)

func TestStampedVersion(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		key       string
		wantVer   string // rendered form; a legacy `vN` stamp reads as 0.N.0
		wantFound bool
	}{
		{
			name:      "present",
			content:   "intro\n<!-- lacquer:core:start v4 -->\nbody\n<!-- lacquer:core:end -->\noutro",
			key:       "core",
			wantVer:   "0.4.0",
			wantFound: true,
		},
		{
			name:      "absent",
			content:   "no markers here",
			key:       "core",
			wantVer:   "0.0.0",
			wantFound: false,
		},
		{
			name:      "different key absent",
			content:   "<!-- lacquer:ios:start v2 -->\nx\n<!-- lacquer:ios:end -->",
			key:       "core",
			wantVer:   "0.0.0",
			wantFound: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ver, found := StampedVersion(c.content, c.key)
			if ver.String() != c.wantVer || found != c.wantFound {
				t.Fatalf("StampedVersion(%q) = (%s,%v), want (%s,%v)",
					c.key, ver, found, c.wantVer, c.wantFound)
			}
		})
	}
}

func TestMergeReplacesExistingBlock(t *testing.T) {
	content := "# CLAUDE.md\n\nlocal top\n\n" +
		"<!-- lacquer:core:start v3 -->\nOLD shared body\n<!-- lacquer:core:end -->\n\n" +
		"local bottom\n"
	got, err := Merge(content, "core", version.Version{Minor: 5}, "NEW shared body")
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	want := "# CLAUDE.md\n\nlocal top\n\n" +
		"<!-- lacquer:core:start v0.5.0 -->\nNEW shared body\n<!-- lacquer:core:end -->\n\n" +
		"local bottom\n"
	if got != want {
		t.Fatalf("Merge mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMergeAppendsWhenAbsent(t *testing.T) {
	content := "# CLAUDE.md\n\nProject Identity: acme\n"
	got, err := Merge(content, "ios", version.Version{Minor: 2}, "iOS shared rules")
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	want := "# CLAUDE.md\n\nProject Identity: acme\n\n" +
		"<!-- lacquer:ios:start v0.2.0 -->\niOS shared rules\n<!-- lacquer:ios:end -->\n"
	if got != want {
		t.Fatalf("Merge append mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMergeAppendsToEmpty(t *testing.T) {
	got, err := Merge("", "core", version.Version{Minor: 1}, "rules")
	if err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	want := "<!-- lacquer:core:start v0.1.0 -->\nrules\n<!-- lacquer:core:end -->\n"
	if got != want {
		t.Fatalf("Merge empty mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMergeRejectsDanglingStart(t *testing.T) {
	content := "<!-- lacquer:core:start v1 -->\nbody with no end marker\n"
	_, err := Merge(content, "core", version.Version{Minor: 2}, "new")
	if err == nil {
		t.Fatal("expected error for dangling start marker, got nil")
	}
}

func TestMergeRejectsBodyContainingEndMarker(t *testing.T) {
	body := "docs say markers look like <!-- lacquer:core:end -->"
	_, err := Merge("local\n", "core", version.Version{Minor: 1}, body)
	if err == nil {
		t.Fatal("expected error: body contains the end marker literal, got nil")
	}
}

func TestMergeRejectsDuplicateBlocks(t *testing.T) {
	content := "<!-- lacquer:core:start v1 -->\na\n<!-- lacquer:core:end -->\n\n" +
		"<!-- lacquer:core:start v1 -->\nb\n<!-- lacquer:core:end -->\n"
	_, err := Merge(content, "core", version.Version{Minor: 2}, "x")
	if err == nil {
		t.Fatal("expected error for duplicate core blocks, got nil")
	}
}

func TestMergeRejectsEndBeforeStart(t *testing.T) {
	content := "<!-- lacquer:core:end -->\nstuff\n<!-- lacquer:core:start v1 -->\n"
	_, err := Merge(content, "core", version.Version{Minor: 2}, "x")
	if err == nil {
		t.Fatal("expected error for end marker preceding start, got nil")
	}
}

func TestExtractBody(t *testing.T) {
	// Body round-trips through Merge: what Merge writes, ExtractBody recovers.
	merged, err := Merge("intro\n", "core", version.Version{Minor: 7}, "line one\nline two")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	body, found := ExtractBody(merged, "core")
	if !found {
		t.Fatal("ExtractBody did not find the core block")
	}
	if body != "line one\nline two" {
		t.Errorf("body = %q, want %q", body, "line one\nline two")
	}
	// Absent key.
	if _, found := ExtractBody(merged, "ios"); found {
		t.Error("ExtractBody found a block for an absent key")
	}
}

// --- semver stamps, and back-compat with the legacy integer form ---

func mustParse(t *testing.T, s string) version.Version {
	t.Helper()
	v, err := version.Parse(s)
	if err != nil {
		t.Fatalf("version.Parse(%q): %v", s, err)
	}
	return v
}

// A project stamped by an older lacquer carries `v72`. It must still be read —
// and read as 0.72.0, so it orders against new semver versions instead of being
// treated as missing (which would report every project as never-synced).
func TestStampedVersionReadsLegacyInteger(t *testing.T) {
	content := "<!-- lacquer:core:start v72 -->\nbody\n<!-- lacquer:core:end -->"
	got, found := StampedVersion(content, "core")
	if !found {
		t.Fatal("legacy v72 stamp not found")
	}
	if got.String() != "0.72.0" {
		t.Errorf("got %s, want 0.72.0", got)
	}
}

func TestStampedVersionReadsSemver(t *testing.T) {
	content := "<!-- lacquer:core:start v0.73.1 -->\nbody\n<!-- lacquer:core:end -->"
	got, found := StampedVersion(content, "core")
	if !found {
		t.Fatal("semver stamp not found")
	}
	if got.String() != "0.73.1" {
		t.Errorf("got %s, want 0.73.1", got)
	}
}

// bodyRe has its own `v\d+`. If it isn't widened too, ExtractBody silently fails
// to find a semver-stamped block — and audit would classify every region as
// changed.
func TestExtractBodyFindsSemverStampedBlock(t *testing.T) {
	content := "<!-- lacquer:core:start v0.73.0 -->\nthe body\n<!-- lacquer:core:end -->"
	body, ok := ExtractBody(content, "core")
	if !ok {
		t.Fatal("semver-stamped block body not found")
	}
	if body != "the body" {
		t.Errorf("body = %q, want %q", body, "the body")
	}
}

// blockRe has a third `v\d+`. If it isn't widened, Merge fails to MATCH the
// existing block and appends a second one instead of replacing it — silent
// duplication in every project's CLAUDE.md.
func TestMergeReplacesSemverStampedBlock(t *testing.T) {
	content := "intro\n<!-- lacquer:core:start v0.73.0 -->\nold\n<!-- lacquer:core:end -->\noutro"
	got, err := Merge(content, "core", mustParse(t, "0.74.0"), "new")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := "intro\n<!-- lacquer:core:start v0.74.0 -->\nnew\n<!-- lacquer:core:end -->\noutro"
	if got != want {
		t.Errorf("Merge =\n%q\nwant\n%q", got, want)
	}
	if strings.Count(got, ":start") != 1 {
		t.Errorf("block was duplicated rather than replaced:\n%s", got)
	}
}

// The migration case: an existing legacy-stamped project syncs and gets
// re-stamped in the new form, in place.
func TestMergeUpgradesLegacyStampInPlace(t *testing.T) {
	content := "intro\n<!-- lacquer:core:start v72 -->\nold\n<!-- lacquer:core:end -->\noutro"
	got, err := Merge(content, "core", mustParse(t, "0.73.0"), "new")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := "intro\n<!-- lacquer:core:start v0.73.0 -->\nnew\n<!-- lacquer:core:end -->\noutro"
	if got != want {
		t.Errorf("Merge =\n%q\nwant\n%q", got, want)
	}
	if strings.Count(got, ":start") != 1 {
		t.Errorf("legacy block was duplicated rather than upgraded:\n%s", got)
	}
}

// A dangling legacy start marker must still be rejected, not silently appended to.
func TestMergeRejectsDanglingLegacyStart(t *testing.T) {
	if _, err := Merge("<!-- lacquer:core:start v72 -->\nno end", "core", mustParse(t, "0.73.0"), "b"); err == nil {
		t.Fatal("want error for a dangling legacy start marker")
	}
}

// --- comment syntaxes ---

// The markdown markers are load-bearing BYTES, not a formatting choice: every
// CLAUDE.md and AGENTS.md in the fleet already carries them on disk. Change one
// character and Merge stops matching the existing block, appends a second one,
// and every project grows duplicate regions on its next sync. Asserted as a
// literal so a refactor of Syntax has to notice.
func TestMarkdownMarkersAreExactBytes(t *testing.T) {
	got, err := Merge("", "core", mustParse(t, "0.84.1"), "BODY")
	if err != nil {
		t.Fatal(err)
	}
	want := "<!-- lacquer:core:start v0.84.1 -->\nBODY\n<!-- lacquer:core:end -->\n"
	if got != want {
		t.Errorf("markdown region bytes changed:\n got %q\nwant %q", got, want)
	}
}

// A .gitignore comment runs to end of line, so the marker carries no closing
// delimiter. A stray ` -->` here would be part of the comment text, and — worse
// — the `#` prefix would be missing from nothing, so the file would still parse
// as a .gitignore and the breakage would be invisible.
func TestHashMarkersAreExactBytes(t *testing.T) {
	got, err := Hash.Merge("", "gitignore", mustParse(t, "0.84.1"), "*.p8")
	if err != nil {
		t.Fatal(err)
	}
	want := "# lacquer:gitignore:start v0.84.1\n*.p8\n# lacquer:gitignore:end\n"
	if got != want {
		t.Errorf("hash region bytes changed:\n got %q\nwant %q", got, want)
	}
}

// Every marker line a Hash region writes must be a comment. A marker git reads
// as a PATTERN would silently ignore a file named after it, and nothing would
// report that.
func TestHashMarkerLinesAreComments(t *testing.T) {
	got, err := Hash.Merge("own\n", "gitignore", mustParse(t, "1.0.0"), "*.p8")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "lacquer:") && !strings.HasPrefix(line, "#") {
			t.Errorf("marker line is not a comment, so git reads it as a pattern: %q", line)
		}
	}
}

// The two syntaxes must not see each other's blocks. If Hash matched a markdown
// marker, syncing the .gitignore region into a file that happens to contain one
// would replace the wrong block; if Markdown matched a hash marker, the
// CLAUDE.md path would start rewriting .gitignore text.
func TestSyntaxesDoNotCrossMatch(t *testing.T) {
	md, err := Merge("", "core", mustParse(t, "1.0.0"), "MD")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := Hash.Merge("", "core", mustParse(t, "1.0.0"), "HASH")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := Hash.ExtractBody(md, "core"); found {
		t.Error("Hash matched a markdown block")
	}
	if _, found := Markdown.ExtractBody(hash, "core"); found {
		t.Error("Markdown matched a hash block")
	}
	// And a merge in the other syntax appends rather than replacing, leaving the
	// foreign block untouched.
	both, err := Hash.Merge(md, "core", mustParse(t, "1.0.0"), "HASH")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(both, "<!-- lacquer:core:start v1.0.0 -->\nMD\n<!-- lacquer:core:end -->") {
		t.Errorf("a hash merge damaged the markdown block:\n%s", both)
	}
}

// A hash region replaces in place, keeping the file's own lines. Same guarantee
// as markdown, asserted separately because it is a different regex path.
func TestHashMergeReplacesInPlace(t *testing.T) {
	content := "DerivedData/\n\n# lacquer:gitignore:start v0.80.0\nOLD\n# lacquer:gitignore:end\n\n*.log\n"
	got, err := Hash.Merge(content, "gitignore", mustParse(t, "0.84.1"), "NEW")
	if err != nil {
		t.Fatal(err)
	}
	want := "DerivedData/\n\n# lacquer:gitignore:start v0.84.1\nNEW\n# lacquer:gitignore:end\n\n*.log\n"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if strings.Count(got, ":start") != 1 {
		t.Errorf("block duplicated rather than replaced:\n%s", got)
	}
}
