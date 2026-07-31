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
