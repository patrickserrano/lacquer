package gateoff

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/lock"
)

// Direct tests of Check and its parts, with in-memory snapshots. The merge
// harvest's tests (internal/producers) cover the same rules through gh.

// commit is one commit's files, path -> content.
type commit map[string]string

func id(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// snapshots builds both sides and a reader over every blob in them. failing
// names contents whose read fails.
func snapshots(base, head commit, failing ...string) (Snapshot, Snapshot, Reader, *int) {
	blobs := map[string]string{}
	mk := func(c commit) Snapshot {
		s := Snapshot{Blobs: map[string]string{}}
		for p, body := range c {
			s.Blobs[p] = id(body)
			blobs[id(body)] = body
		}
		return s
	}
	bad := map[string]bool{}
	for _, f := range failing {
		bad[id(f)] = true
	}
	reads := 0
	return mk(base), mk(head), func(blob string) ([]byte, error) {
		reads++
		if bad[blob] {
			return nil, errors.New("HTTP 502")
		}
		body, ok := blobs[blob]
		if !ok {
			return nil, fmt.Errorf("no blob %s", blob)
		}
		return []byte(body), nil
	}, &reads
}

func lockOf(ver string, files map[string]string) string {
	var parts []string
	for k, v := range files {
		parts = append(parts, fmt.Sprintf("%q: %q", k, v))
	}
	return fmt.Sprintf(`{"version": %q, "files": {%s}}`, ver, strings.Join(parts, ", "))
}

const workflow = "name: CI\n"

func managed(ver string, extra commit) commit {
	c := commit{".github/workflows/ci.yml": workflow, lock.Name: lockOf(ver, map[string]string{".github/workflows/ci.yml": lock.Hash(workflow)})}
	for p, b := range extra {
		c[p] = b
	}
	return c
}

func whats(r Result) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.What)
	}
	return out
}

// A failed read is an error, never an absent file: an absent base lock would
// make this downgrade a silent first sync.
func TestCheckReturnsAFailedReadNotAnAbsentFile(t *testing.T) {
	base := managed("1.78.1", nil)
	b, h, read, _ := snapshots(base, managed("1.77.0", nil), base[lock.Name])
	res, err := Check(b, h, read, Options{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || len(res.Findings) != 0 {
		t.Fatalf("res %+v, err %v; want the read error and nothing else", res, err)
	}
}

func TestCheckReportsAnUnparseableLockAndKeepsTheRest(t *testing.T) {
	base := managed("1.78.1", commit{ManifestName: "[project]\n"})
	head := managed("1.78.1", commit{ManifestName: "[project]\nexclude = [\"x.yml\"]\n"})
	head[lock.Name] = "{nope"
	b, h, read, _ := snapshots(base, head)
	res, err := Check(b, h, read, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := whats(res); !reflect.DeepEqual(got, []string{`added [project].exclude "x.yml" (undated)`}) {
		t.Errorf("findings %q; the manifest is still compared", got)
	}
	if len(res.Unreadable) != 1 || !strings.HasPrefix(res.Unreadable[0], ".lacquer.lock at the merge does not parse") {
		t.Errorf("unreadable %q", res.Unreadable)
	}
	if res.ManagedKnown {
		t.Error("the managed paths are known from a lock that did not parse")
	}
}

func TestCheckLearnsTheManagedPathsFromTheHeadLock(t *testing.T) {
	head := commit{lock.Name: lockOf("1.78.1", map[string]string{"b.yml": "1", "CLAUDE.md#core": "2", "CLAUDE.md#ios": "3", "a.yml": "4"})}
	b, h, read, _ := snapshots(commit{}, head)
	res, err := Check(b, h, read, Options{})
	if err != nil || !res.ManagedKnown || !reflect.DeepEqual(res.Managed, []string{"CLAUDE.md", "a.yml", "b.yml"}) {
		t.Fatalf("managed %v known %v (%v); want each file once, sorted", res.Managed, res.ManagedKnown, err)
	}
	// No lock at the head is known, and empty.
	b, h, read, _ = snapshots(head, commit{"x": "y"})
	res, err = Check(b, h, read, Options{})
	if err != nil || !res.ManagedKnown || len(res.Managed) != 0 || res.Managed == nil {
		t.Fatalf("managed %#v known %v (%v); want known and empty", res.Managed, res.ManagedKnown, err)
	}
}

// The lock is read once when it is the same blob on both sides, and the
// manifest not at all when it is unchanged and nothing needs a reason.
func TestCheckReadsOnlyTheBlobsItNeeds(t *testing.T) {
	base := managed("1.78.1", commit{ManifestName: "[project]\n", "Sources/A.swift": "a"})
	head := managed("1.78.1", commit{ManifestName: "[project]\n", "Sources/A.swift": "b"})
	b, h, read, reads := snapshots(base, head)
	if _, err := Check(b, h, read, Options{}); err != nil {
		t.Fatal(err)
	}
	if *reads != 1 {
		t.Errorf("read %d blobs, want 1 (the lock)", *reads)
	}
}

func TestCheckDeletionRules(t *testing.T) {
	wf := ".github/workflows/ci.yml"
	gone := func(c commit) commit { delete(c, wf); return c }
	for _, tc := range []struct {
		name       string
		base, head commit
		want       []string
	}{
		{"lock unchanged", managed("1.78.1", nil), gone(managed("1.78.1", nil)), []string{"deleted managed " + wf + " outside a sync"}},
		{"same-version resync", managed("1.78.1", nil), gone(managed("1.78.1", commit{lock.Name: lockOf("1.78.1", map[string]string{wf: lock.Hash(workflow), "x": "1"})})), []string{"deleted managed " + wf + " outside a sync"}},
		{"entry dropped too", managed("1.78.1", nil), commit{lock.Name: lockOf("1.78.1", map[string]string{})}, []string{"deleted managed " + wf + " (and its .lacquer.lock entry) outside a sync"}},
		{"sync to another version", managed("1.78.0", nil), gone(managed("1.78.1", nil)), nil},
		{"sync from another manifest", managed("1.78.1", commit{ManifestName: "[project]\n"}), gone(managed("1.78.1", commit{ManifestName: "[project]\nskills = []\n", lock.Name: lockOf("1.78.1", map[string]string{wf: lock.Hash(workflow), "y": "2"})})), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, h, read, _ := snapshots(tc.base, tc.head)
			res, err := Check(b, h, read, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got := whats(res); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("findings %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckOnlyDependabotsPureBumpsPass(t *testing.T) {
	wf := ".github/workflows/ci.yml"
	before := "steps:\n  - uses: actions/checkout@v6\n"
	base := commit{wf: before, lock.Name: lockOf("1.78.1", map[string]string{wf: lock.Hash(before)})}
	for _, tc := range []struct {
		name, after string
		bot         bool
		want        int
	}{
		{"dependabot pure bump", "steps:\n  - uses: actions/checkout@v7\n", true, 0},
		{"dependabot bump and a line", "steps:\n  - uses: actions/checkout@v7\n  - run: true\n", true, 1},
		{"pure bump by someone else", "steps:\n  - uses: actions/checkout@v7\n", false, 1},
		{"dependabot renamed action", "steps:\n  - uses: evil/checkout@v7\n", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			head := commit{wf: tc.after, lock.Name: base[lock.Name]}
			b, h, read, _ := snapshots(base, head)
			res, err := Check(b, h, read, Options{ByDependabot: tc.bot})
			if err != nil || len(res.Findings) != tc.want {
				t.Fatalf("findings %q (%v), want %d", whats(res), err, tc.want)
			}
		})
	}
}

func TestExemptionChangesComparesWholeEntriesInAnyOrder(t *testing.T) {
	old := `{ path = "a.yml", reason = "r", until = "2026-11-01" }`
	later := `{ path = "a.yml", reason = "dup", until = "2026-12-01" }`
	sooner := `{ path = "a.yml", reason = "dup", until = "2026-10-01" }`
	parse := func(entries ...string) manifest {
		m, err := parseManifest([]byte("[project]\nexclude = [" + strings.Join(entries, ", ") + "]\n"))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, tc := range []struct {
		name string
		head manifest
		want []string
	}{
		{"later above", parse(later, old), []string{`extended [project].exclude "a.yml" until 2026-11-01→2026-12-01`}},
		{"later below", parse(old, later), []string{`extended [project].exclude "a.yml" until 2026-11-01→2026-12-01`}},
		{"sooner above", parse(sooner, old), nil},
		{"unchanged", parse(old), nil},
		{"removed", parse(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, f := range exemptionChanges(parse(old), tc.head) {
				got = append(got, f.What)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtends(t *testing.T) {
	for _, tc := range []struct {
		old, new string
		want     bool
	}{
		{"2026-11-01", "2026-12-01", true},
		{"2026-11-01", "", true},
		{"2026-11-01", "2026-11-01", false},
		{"2026-11-01", "2026-10-01", false},
		{"", "2026-10-01", false},
		{"", "", false},
	} {
		if got := extends(tc.old, tc.new); got != tc.want {
			t.Errorf("extends(%q, %q) = %v, want %v", tc.old, tc.new, got, tc.want)
		}
	}
}

func TestExtractRegionReadsEitherCommentSyntax(t *testing.T) {
	for _, content := range []string{
		"intro\n<!-- lacquer:core:start v1.0.0 -->\nbody\n<!-- lacquer:core:end -->\n",
		"local/\n# lacquer:core:start v1.0.0\nbody\n# lacquer:core:end\n",
	} {
		if body, ok := extractRegion(content, "core"); !ok || body != "body" {
			t.Errorf("extractRegion(%q) = %q, %v", content, body, ok)
		}
	}
	if _, ok := extractRegion("no markers\n", "core"); ok {
		t.Error("found a region in a file with none")
	}
}
