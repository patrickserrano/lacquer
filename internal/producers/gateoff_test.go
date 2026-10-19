package producers

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/lock"
)

// The gate-off harvest (#576): a merged PR that downgraded .lacquer.lock, added
// or extended an exemption in .lacquer.toml, or edited or deleted a managed file
// outside a sync gets one UNREAD entry per finding, ahead of its merge entry.

const (
	merge9  = "8888888888888888888888888888888888888888"
	parent9 = "7777777777777777777777777777777777777777"
	pr9URL  = "https://github.com/acme/widgets/pull/9"
	wfPath  = ".github/workflows/ci.yml"
	wf      = "name: CI\non: pull_request\n"
)

// tree is one commit's files, path -> content.
type tree map[string]string

func blobOf(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

func treeJSON(sha string, files tree) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var es []string
	for _, p := range paths {
		es = append(es, fmt.Sprintf(`{"path":%q,"mode":"100644","type":"blob","sha":%q}`, p, blobOf(files[p])))
	}
	// A directory entry, as GitHub lists them; it is not a file.
	es = append(es, `{"path":".github","mode":"040000","type":"tree","sha":"dddddddddddddddddddddddddddddddddddddddd"}`)
	return fmt.Sprintf(`{"sha":%q,"truncated":false,"tree":[%s]}`, sha, strings.Join(es, ","))
}

func commitPath(sha string) string { return "repos/acme/widgets/git/commits/" + sha }
func treePath(sha string) string   { return "repos/acme/widgets/git/trees/" + sha + "?recursive=1" }
func blobPath(content string) string {
	return "repos/acme/widgets/git/blobs/" + blobOf(content)
}

// scriptMerge answers the gate-off reads for a merge commit whose first parent
// holds base and which itself holds head.
func scriptMerge(g *loosenGH, merge, parent string, base, head tree) {
	g.api[commitPath(merge)] = fmt.Sprintf(`{"sha":%q,"tree":{"sha":"tttttttttttttttttttttttttttttttttttttttt"},"parents":[{"sha":%q}]}`, merge, parent)
	g.api[treePath(parent)] = treeJSON(parent, base)
	g.api[treePath(merge)] = treeJSON(merge, head)
	for _, t := range []tree{base, head} {
		for _, c := range t {
			// GitHub wraps base64 at 60 columns; the decoder must cope.
			enc := base64.StdEncoding.EncodeToString([]byte(c))
			var wrapped []string
			for len(enc) > 60 {
				wrapped = append(wrapped, enc[:60])
				enc = enc[60:]
			}
			wrapped = append(wrapped, enc)
			g.api[blobPath(c)] = fmt.Sprintf(`{"sha":%q,"content":%q,"encoding":"base64","size":%d}`, blobOf(c), strings.Join(wrapped, "\n")+"\n", len(c))
		}
	}
}

func lockJSON(t *testing.T, ver string, files map[string]string, downgrade ...string) string {
	t.Helper()
	l := map[string]any{"version": ver, "files": files}
	if len(downgrade) == 2 {
		l["downgrade"] = map[string]string{"from": downgrade[0], "reason": downgrade[1]}
	}
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func mergedWithCommit(n int, title, mergedAt, head, merge string) string {
	return fmt.Sprintf(`{"number":%d,"title":%q,"url":"https://github.com/acme/widgets/pull/%d","mergedAt":%q,"headRefOid":%q,"mergeCommit":{"oid":%q}}`, n, title, n, mergedAt, head, merge)
}

// gateFixture is a primed harvest with PR #9 merged, changing files, from base
// to head.
func gateFixture(t *testing.T, base, head tree, files ...string) (string, *loosenGH) {
	t.Helper()
	path := newInbox(t)
	g := &loosenGH{api: map[string]string{}, apiErr: map[string]error{}}
	harvest9(path, t0, g) // primes the cursor
	g.list = "[" + mergedWithCommit(9, "chore", "2026-09-24T12:30:00Z", head9, merge9) + "]"
	g.files = "[" + filesOf(9, files...) + "]"
	scriptMerge(g, merge9, parent9, base, head)
	return path, g
}

// synced is a project the lacquer manages one workflow for, as v<ver> wrote it.
func synced(t *testing.T, ver, workflow string, extra tree) tree {
	t.Helper()
	tr := tree{wfPath: workflow, lock.Name: lockJSON(t, ver, map[string]string{wfPath: lock.Hash(workflow)}), "Sources/App.swift": "app\n"}
	for p, c := range extra {
		tr[p] = c
	}
	return tr
}

// gateEntries runs one harvest an hour after priming and returns everything it
// added except the merge entry, which it checks is last.
func gateEntries(t *testing.T, path string, g *loosenGH) []inbox.Entry {
	t.Helper()
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 0 {
		t.Fatalf("unavailable: %v", res.Unavailable)
	}
	if len(res.Added) == 0 || res.Added[len(res.Added)-1].Ref != pr9URL {
		t.Fatalf("added %+v; want the merge entry last", res.Added)
	}
	return res.Added[:len(res.Added)-1]
}

func wantGateOff(t *testing.T, got []inbox.Entry, want ...[3]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d gate-off entries, want %d:\n%s", len(got), len(want), describe(got))
	}
	for i, w := range want {
		e := got[i]
		if e.Type != inbox.Unread || e.Title != "acme/widgets#9 "+w[0] || e.Body != w[1] || e.Ref != pr9URL+"#"+w[2] || e.Project != "widgets" {
			t.Errorf("entry %d =\n  %q | %q | %q\nwant\n  %q | %q | %q", i, e.Title, e.Body, e.Ref, "acme/widgets#9 "+w[0], w[1], pr9URL+"#"+w[2])
		}
	}
}

func describe(es []inbox.Entry) string {
	var b strings.Builder
	for _, e := range es {
		fmt.Fprintf(&b, "  %q | %q | %q\n", e.Title, e.Body, e.Ref)
	}
	return b.String()
}

// --- the lock -------------------------------------------------------------

func TestGateOffRaisesALockDowngrade(t *testing.T) {
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), synced(t, "1.77.0", wf, nil), lock.Name)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"downgraded .lacquer.lock v1.78.1→v1.77.0", "no reason recorded", "gate-off-downgrade-1.78.1-1.77.0"})
}

func TestGateOffQuotesTheDowngradeReasonTheLockRecords(t *testing.T) {
	head := synced(t, "1.77.0", wf, nil)
	head[lock.Name] = lockJSON(t, "1.77.0", map[string]string{wfPath: lock.Hash(wf)}, "1.78.1", "1.78.1 breaks the release job")
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, lock.Name)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"downgraded .lacquer.lock v1.78.1→v1.77.0", "reason: 1.78.1 breaks the release job", "gate-off-downgrade-1.78.1-1.77.0"})
}

// A reason recorded for leaving another version does not explain this one.
func TestGateOffIgnoresADowngradeReasonForAnotherVersion(t *testing.T) {
	head := synced(t, "1.77.0", wf, nil)
	head[lock.Name] = lockJSON(t, "1.77.0", map[string]string{wfPath: lock.Hash(wf)}, "1.79.0", "stale note")
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, lock.Name)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"downgraded .lacquer.lock v1.78.1→v1.77.0", "no reason recorded", "gate-off-downgrade-1.78.1-1.77.0"})
}

func TestGateOffIsSilentOnAnUpgradeOrTheSameVersion(t *testing.T) {
	for _, tc := range []struct{ name, from, to string }{{"upgrade", "1.77.0", "1.78.1"}, {"same", "1.78.1", "1.78.1"}, {"patch upgrade", "1.78.0", "1.78.1"}} {
		t.Run(tc.name, func(t *testing.T) {
			newWF := wf + "# " + tc.to + "\n"
			path, g := gateFixture(t, synced(t, tc.from, wf, nil), synced(t, tc.to, newWF, nil), lock.Name, wfPath)
			wantGateOff(t, gateEntries(t, path, g))
		})
	}
}

func TestGateOffRaisesARemovedLock(t *testing.T) {
	head := synced(t, "1.78.1", wf, nil)
	delete(head, lock.Name)
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, lock.Name)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"removed .lacquer.lock, which named v1.78.1", "no reason recorded", "gate-off-lock-removed-1.78.1"})
}

// "Absent at the base" is a first sync, and silent. It is a different state
// from "could not read it", which TestGateOffNeverReadsAFailedLockReadAsAbsent
// pins.
func TestGateOffTreatsALockAbsentAtTheBaseAsAFirstSync(t *testing.T) {
	base := tree{"Sources/App.swift": "app\n"}
	path, g := gateFixture(t, base, synced(t, "1.78.1", wf, nil), lock.Name, wfPath)
	wantGateOff(t, gateEntries(t, path, g))
}

func TestGateOffNeverReadsAFailedLockReadAsAbsent(t *testing.T) {
	base := synced(t, "1.78.1", wf, nil)
	path, g := gateFixture(t, base, synced(t, "1.77.0", wf, nil), lock.Name)
	g.apiErr[blobPath(base[lock.Name])] = errors.New("HTTP 502")
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "acme/widgets#9") || !strings.Contains(res.Unavailable[0], "HTTP 502") {
		t.Fatalf("unavailable = %v; a failed read must be reported, never read as a missing lock", res.Unavailable)
	}
	if n := len(entries(t, path)); n != 0 {
		t.Fatalf("a failed read still wrote %d entries", n)
	}
}

// --- the manifest ---------------------------------------------------------

func TestGateOffRaisesEachAddedOrExtendedExemption(t *testing.T) {
	cases := []struct {
		name, base, head string
		want             [3]string
	}{
		{"exclude added", ``,
			`[project]
exclude = [{ path = ".github/workflows/ios-ci.yml", reason = "local fix", until = "2026-12-01" }]`,
			[3]string{`added [project].exclude ".github/workflows/ios-ci.yml" (until 2026-12-01)`, "reason: local fix", "gate-off-exclude-.github/workflows/ios-ci.yml"}},
		{"bare exclude added", ``, `[project]
exclude = ["typedoc.json"]`,
			[3]string{`added [project].exclude "typedoc.json" (undated)`, "no reason recorded", "gate-off-exclude-typedoc.json"}},
		{"exclude extended", `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-11-01" }]`, `[project]
exclude = [{ path = "a.yml", reason = "still broken", until = "2026-12-01" }]`,
			[3]string{`extended [project].exclude "a.yml" until 2026-11-01→2026-12-01`, "reason: still broken", "gate-off-exclude-a.yml"}},
		{"exclude made undated", `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-11-01" }]`, `[project]
exclude = [{ path = "a.yml", reason = "permanent now" }]`,
			[3]string{`extended [project].exclude "a.yml" until 2026-11-01→undated`, "reason: permanent now", "gate-off-exclude-a.yml"}},
		{"exclude widened to a directory", `[project]
exclude = [{ path = ".github/workflows/a.yml", reason = "r" }]`, `[project]
exclude = [{ path = ".github/workflows", reason = "all of them" }]`,
			[3]string{`added [project].exclude ".github/workflows" (undated)`, "reason: all of them", "gate-off-exclude-.github/workflows"}},
		{"relax added", ``, `[baseline.relax.warnings_as_errors]
until = "2026-12-01"
reason = "vendored SDK"`,
			[3]string{`added [baseline.relax] "warnings_as_errors" (until 2026-12-01)`, "reason: vendored SDK", "gate-off-relax-warnings_as_errors"}},
		{"relax extended", `[baseline.relax.coverage]
until = "2026-11-01"
reason = "r"`, `[baseline.relax.coverage]
until = "2027-01-01"
reason = "r2"`,
			[3]string{`extended [baseline.relax] "coverage" until 2026-11-01→2027-01-01`, "reason: r2", "gate-off-relax-coverage"}},
		{"dependabot ignore added", `[[component]]
path = "."`, `[[component]]
path = "."
dependabot_ignore = [{ dependency = "typescript", versions = ["7.x"], reason = "breaks build", until = "2026-12-01" }]`,
			[3]string{`added dependabot_ignore ".:typescript" (until 2026-12-01)`, "reason: breaks build", "gate-off-dependabot_ignore-.:typescript"}},
		{"dependabot ignore widened", `[[component]]
path = "."
dependabot_ignore = [{ dependency = "typescript", versions = ["7.x"], reason = "r", until = "2026-12-01" }]`, `[[component]]
path = "."
dependabot_ignore = [{ dependency = "typescript", versions = ["7.x", "8.x"], reason = "r", until = "2026-12-01" }]`,
			[3]string{`widened dependabot_ignore ".:typescript" to also withhold 8.x`, "reason: r", "gate-off-dependabot_ignore-.:typescript"}},
		{"not run in CI added", ``, `[[project.not_run_in_ci]]
target = "AppUITests"
reason = "needs a device"
until = "2026-12-01"`,
			[3]string{`added [[project.not_run_in_ci]] "AppUITests" (until 2026-12-01)`, "reason: needs a device", "gate-off-not_run_in_ci-AppUITests"}},
		{"covered elsewhere added", ``, `[[project.covered_elsewhere]]
target = "Watch Tests"
workflow = ".github/workflows/watch.yml"
job = "test"
reason = "watch.yml test job runs it"`,
			[3]string{`added [[project.covered_elsewhere]] "Watch Tests"`, "reason: watch.yml test job runs it", "gate-off-covered_elsewhere-Watch Tests"}},
		{"retired secret added", ``, `[[project.retired_secrets]]
name = "SENTRY_DSN"
reason = "moved off Sentry"`,
			[3]string{`added [[project.retired_secrets]] "SENTRY_DSN"`, "reason: moved off Sentry", "gate-off-retired_secrets-SENTRY_DSN"}},
		{"project retired", `[project]
name = "x"`, `[project]
name = "x"
retired = { since = "2026-10-19", reason = "not viable" }`,
			[3]string{`added [project].retired "project"`, "reason: not viable", "gate-off-retired-project"}},
		{"seed once added", ``, `[project]
seed_once = ["ios/Secrets.xcconfig.example"]`,
			[3]string{`added [project].seed_once "ios/Secrets.xcconfig.example"`, "no reason recorded", "gate-off-seed_once-ios/Secrets.xcconfig.example"}},
		{"biome ignore added", ``, `[web]
biome_ignores = ["!**/generated"]`,
			[3]string{`added [web].biome_ignores "!**/generated"`, "no reason recorded", "gate-off-biome_ignores-!**/generated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := synced(t, "1.78.1", wf, tree{".lacquer.toml": tc.base + "\n"})
			head := synced(t, "1.78.1", wf, tree{".lacquer.toml": tc.head + "\n"})
			path, g := gateFixture(t, base, head, ".lacquer.toml")
			wantGateOff(t, gateEntries(t, path, g), tc.want)
		})
	}
}

func TestGateOffIsSilentOnARemovedOrShortenedExemption(t *testing.T) {
	cases := []struct{ name, base, head string }{
		{"exclude removed", `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-11-01" }]`, ``},
		{"exclude shortened", `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-12-01" }]`, `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-11-01" }]`},
		{"undated exclude given a date", `[project]
exclude = [{ path = "a.yml", reason = "r" }]`, `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-11-01" }]`},
		{"relax removed", `[baseline.relax.coverage]
until = "2026-11-01"
reason = "r"`, ``},
		{"reason reworded", `[project]
exclude = [{ path = "a.yml", reason = "r", until = "2026-11-01" }]`, `[project]
exclude = [{ path = "a.yml", reason = "clearer", until = "2026-11-01" }]`},
		{"ignore narrowed", `[[component]]
path = "."
dependabot_ignore = [{ dependency = "ts", versions = ["7.x", "8.x"], reason = "r", until = "2026-12-01" }]`, `[[component]]
path = "."
dependabot_ignore = [{ dependency = "ts", versions = ["7.x"], reason = "r", until = "2026-12-01" }]`},
		{"unrelated key", `[project]
name = "x"`, `[project]
name = "y"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := synced(t, "1.78.1", wf, tree{".lacquer.toml": tc.base + "\n"})
			head := synced(t, "1.78.1", wf, tree{".lacquer.toml": tc.head + "\n"})
			path, g := gateFixture(t, base, head, ".lacquer.toml")
			wantGateOff(t, gateEntries(t, path, g))
		})
	}
}

// --- managed files ----------------------------------------------------------

func TestGateOffRaisesAManagedFileEditedByHand(t *testing.T) {
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), synced(t, "1.78.1", wf, tree{wfPath: wf + "  # skip the slow job\n"}), wfPath)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"edited managed .github/workflows/ci.yml outside a sync", "no reason recorded", "gate-off-edited-files"})
}

// Several managed files in one PR are one entry each for edits and deletions,
// naming every file: a Dependabot bump touches many at once.
func TestGateOffGroupsAPRsManagedFilesIntoOneEntryPerKind(t *testing.T) {
	other := ".github/workflows/release.yml"
	files := map[string]string{wfPath: lock.Hash(wf), other: lock.Hash("name: Release\n"), "lefthook.yml": lock.Hash("pre-commit:\n")}
	base := tree{wfPath: wf, other: "name: Release\n", "lefthook.yml": "pre-commit:\n", lock.Name: lockJSON(t, "1.78.1", files)}
	head := tree{wfPath: wf + "# a\n", other: "name: Release\n# b\n", lock.Name: base[lock.Name]}
	path, g := gateFixture(t, base, head, wfPath, other, "lefthook.yml")
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"edited managed .github/workflows/ci.yml, .github/workflows/release.yml outside a sync", "no reason recorded", "gate-off-edited-files"},
		[3]string{"deleted managed lefthook.yml outside a sync", "no reason recorded", "gate-off-deleted-files"})
}

// An edit that only moves `uses:` versions says so; one that swaps the action
// for another of the same name under a different owner is an ordinary edit.
func TestGateOffLabelsAnEditThatOnlyBumpsActionVersions(t *testing.T) {
	steps := func(uses string) string { return "jobs:\n  t:\n    steps:\n      - uses: " + uses + "\n" }
	before := steps("actions/checkout@v6.0.2")
	for _, tc := range []struct{ name, after, want string }{
		{"bump", steps("actions/checkout@v7.0.1"), "bumped only action versions (actions/checkout v6.0.2→v7.0.1) in managed .github/workflows/ci.yml outside a sync"},
		{"swap", steps("evil/checkout@v7.0.1"), "edited managed .github/workflows/ci.yml outside a sync"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := synced(t, "1.78.1", before, nil)
			head := synced(t, "1.78.1", before, nil)
			head[wfPath] = tc.after
			path, g := gateFixture(t, base, head, wfPath)
			wantGateOff(t, gateEntries(t, path, g), [3]string{tc.want, "no reason recorded", "gate-off-edited-files"})
		})
	}

	// A bump in one file beside a real edit in another is not "only versions":
	// the label is a claim about the whole PR.
	other := ".github/workflows/release.yml"
	files := map[string]string{wfPath: lock.Hash(before), other: lock.Hash("name: Release\n")}
	base := tree{wfPath: before, other: "name: Release\n", lock.Name: lockJSON(t, "1.78.1", files)}
	head := tree{wfPath: steps("actions/checkout@v7.0.1"), other: "name: Release\n# skip signing\n", lock.Name: base[lock.Name]}
	path, g := gateFixture(t, base, head, wfPath, other)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"edited managed .github/workflows/ci.yml, .github/workflows/release.yml outside a sync", "no reason recorded", "gate-off-edited-files"})
}

// A sync rewrites the file and the lock together, so the file hashes to the
// head lock's value: silent, whatever else the PR changed.
func TestGateOffIsSilentOnASync(t *testing.T) {
	newWF := wf + "  timeout-minutes: 10\n"
	path, g := gateFixture(t, synced(t, "1.78.0", wf, nil), synced(t, "1.78.1", newWF, tree{"Sources/App.swift": "app 2\n"}), lock.Name, wfPath, "Sources/App.swift")
	wantGateOff(t, gateEntries(t, path, g))
}

// A hand edit that also bumps the lock's version is not a sync: the lock's
// hash still records what the lacquer wrote, not the edit.
func TestGateOffRaisesAHandEditBesideALockBump(t *testing.T) {
	base := synced(t, "1.78.0", wf, nil)
	head := synced(t, "1.78.1", wf, nil)
	head[wfPath] = wf + "  # by hand\n"
	path, g := gateFixture(t, base, head, lock.Name, wfPath)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"edited managed .github/workflows/ci.yml outside a sync", "no reason recorded", "gate-off-edited-files"})
}

func TestGateOffRaisesADeletedManagedFile(t *testing.T) {
	head := synced(t, "1.78.1", wf, nil)
	delete(head, wfPath)
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, wfPath)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"deleted managed .github/workflows/ci.yml outside a sync", "no reason recorded", "gate-off-deleted-files"})
}

// A sync can stop committing a file it still writes (a skill directory the new
// .gitignore region ignores): the file leaves the commit, the lock still lists
// it, and nothing was turned off. Measured in the fleet dry-run on every sync
// that moved skills out of git.
func TestGateOffIsSilentOnASyncThatStopsCommittingAFile(t *testing.T) {
	skill := ".claude/skills/review/SKILL.md"
	files := func(ver string) map[string]string {
		return map[string]string{wfPath: lock.Hash(wf), skill: lock.Hash("# review\n")}
	}
	base := synced(t, "1.47.0", wf, tree{skill: "# review\n", lock.Name: lockJSON(t, "1.47.0", files("1.47.0"))})
	head := synced(t, "1.48.0", wf, tree{".gitignore": ".claude/skills/\n", lock.Name: lockJSON(t, "1.48.0", files("1.48.0"))})
	path, g := gateFixture(t, base, head, lock.Name, skill, ".gitignore")
	wantGateOff(t, gateEntries(t, path, g))
}

// Deleting the file and its lock entry by hand is not a sync either: with the
// same lacquer version and the same manifest, no sync drops an entry.
func TestGateOffRaisesAManagedFileDeletedWithItsLockEntry(t *testing.T) {
	head := tree{lock.Name: lockJSON(t, "1.78.1", map[string]string{}), "Sources/App.swift": "app\n"}
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, lock.Name, wfPath)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"deleted managed .github/workflows/ci.yml (and its .lacquer.lock entry) outside a sync", "no reason recorded", "gate-off-deleted-files"})
}

// An exclusion the same PR added explains the edit, and is quoted with it.
func TestGateOffQuotesTheExclusionThatCoversAnEditedFile(t *testing.T) {
	base := synced(t, "1.78.1", wf, tree{".lacquer.toml": "[project]\nname = \"x\"\n"})
	head := synced(t, "1.78.1", wf+"# local\n", tree{".lacquer.toml": "[project]\nname = \"x\"\nexclude = [{ path = \".github/workflows\", reason = \"runner fix pending upstream\" }]\n"})
	head[lock.Name] = base[lock.Name]
	path, g := gateFixture(t, base, head, ".lacquer.toml", wfPath)
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{`added [project].exclude ".github/workflows" (undated)`, "reason: runner fix pending upstream", "gate-off-exclude-.github/workflows"},
		[3]string{"edited managed .github/workflows/ci.yml outside a sync", "reason: runner fix pending upstream", "gate-off-edited-files"})
}

const claudeRegion = "<!-- lacquer:core:start v1.78.1 -->\nRun the tests.\n<!-- lacquer:core:end -->\n"

// A region is compared by its body: the project owns the rest of the file.
func TestGateOffComparesARegionByItsBody(t *testing.T) {
	lockWith := func(ver string) string {
		return lockJSON(t, ver, map[string]string{wfPath: lock.Hash(wf), "CLAUDE.md#core": lock.Hash("Run the tests.")})
	}
	for _, tc := range []struct {
		name, claude string
		want         [][3]string
	}{
		{"outside the region", "# Project notes\n\n" + claudeRegion, nil},
		{"inside the region", strings.Replace(claudeRegion, "Run the tests.", "Skip the tests.", 1),
			[][3]string{{"edited managed CLAUDE.md outside a sync", "no reason recorded", "gate-off-edited-files"}}},
		{"region removed", "# Project notes\n",
			[][3]string{{"deleted managed region CLAUDE.md#core outside a sync", "no reason recorded", "gate-off-deleted-files"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := synced(t, "1.78.1", wf, tree{"CLAUDE.md": claudeRegion, lock.Name: lockWith("1.78.1")})
			head := synced(t, "1.78.1", wf, tree{"CLAUDE.md": tc.claude, lock.Name: lockWith("1.78.1")})
			path, g := gateFixture(t, base, head, "CLAUDE.md")
			wantGateOff(t, gateEntries(t, path, g), tc.want...)
		})
	}
}

// A `#`-comment region (.gitignore) is read with its own syntax.
func TestGateOffReadsAHashCommentRegion(t *testing.T) {
	ignore := "# lacquer:gitignore:start v1.78.1\n.build/\n# lacquer:gitignore:end\n"
	files := func(body string) tree {
		return synced(t, "1.78.1", wf, tree{".gitignore": body, lock.Name: lockJSON(t, "1.78.1", map[string]string{wfPath: lock.Hash(wf), ".gitignore#gitignore": lock.Hash(".build/")})})
	}
	path, g := gateFixture(t, files(ignore), files("local/\n"+ignore), ".gitignore")
	wantGateOff(t, gateEntries(t, path, g))

	path, g = gateFixture(t, files(ignore), files(strings.Replace(ignore, ".build/", ".build/\n!Secrets.xcconfig", 1)), ".gitignore")
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"edited managed .gitignore outside a sync", "no reason recorded", "gate-off-edited-files"})
}

// --- unreadable, transient, dedupe -----------------------------------------

func TestGateOffTurnsAnUnparseableLockIntoAnUnreadItemAndMovesOn(t *testing.T) {
	head := synced(t, "1.78.1", wf, nil)
	head[lock.Name] = "{nope"
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, lock.Name)
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 0 {
		t.Fatalf("unavailable %v; a file that does not parse must not hold the cursor", res.Unavailable)
	}
	if len(res.Added) != 2 || res.Added[0].Type != inbox.Unread ||
		!strings.HasPrefix(res.Added[0].Title, "couldn't check acme/widgets#9 for gates turned off: .lacquer.lock at the merge does not parse") ||
		!strings.HasSuffix(res.Added[0].Title, "; check by hand") || res.Added[0].Ref != pr9URL+"#gate-off-unreadable" || res.Added[1].Ref != pr9URL {
		t.Fatalf("added:\n%s want the unreadable item, then the merge", describe(res.Added))
	}
	cur, err := readCursors(filepath.Join(filepath.Dir(path), CursorFile))
	if err != nil || !cur.Repos["acme/widgets"].After(t0) {
		t.Fatalf("cursor %+v (%v) did not advance past %s", cur, err, t0)
	}
}

// What could be read is still reported: a manifest that does not parse does
// not hide the downgrade beside it, and the unreadable item says what was not
// checked.
func TestGateOffKeepsWhatItCouldReadBesideAnUnreadablePart(t *testing.T) {
	base := synced(t, "1.78.1", wf, tree{".lacquer.toml": "[project]\n"})
	head := synced(t, "1.77.0", wf, tree{".lacquer.toml": "[project\n"})
	path, g := gateFixture(t, base, head, lock.Name, ".lacquer.toml")
	got := gateEntries(t, path, g)
	if len(got) != 2 || got[0].Title != "acme/widgets#9 downgraded .lacquer.lock v1.78.1→v1.77.0" ||
		!strings.Contains(got[1].Title, ".lacquer.toml at the merge does not parse") || got[1].Ref != pr9URL+"#gate-off-unreadable" {
		t.Fatalf("got:\n%s", describe(got))
	}
}

func TestGateOffTurnsATruncatedTreeIntoAnUnreadItem(t *testing.T) {
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), synced(t, "1.77.0", wf, nil), lock.Name)
	g.api[treePath(merge9)] = strings.Replace(g.api[treePath(merge9)], `"truncated":false`, `"truncated":true`, 1)
	got := gateEntries(t, path, g)
	if len(got) != 1 || !strings.Contains(got[0].Title, "truncated") || got[0].Ref != pr9URL+"#gate-off-unreadable" {
		t.Fatalf("got:\n%s", describe(got))
	}
}

func TestGateOffReportsATransientFailureAndRetries(t *testing.T) {
	head := synced(t, "1.78.1", wf, nil)
	head[wfPath] = wf + "# by hand\n"
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), head, wfPath)
	g.apiErr[blobPath(head[wfPath])] = errors.New("HTTP 502")
	res := harvest9(path, t0.Add(time.Hour), g)
	if len(res.Unavailable) != 1 || !strings.Contains(res.Unavailable[0], "acme/widgets#9") || !strings.Contains(res.Unavailable[0], "HTTP 502") {
		t.Fatalf("unavailable = %v; want the failed look named", res.Unavailable)
	}
	if n := len(entries(t, path)); n != 0 {
		t.Fatalf("a failed look still wrote %d entries", n)
	}
	cur, _ := readCursors(filepath.Join(filepath.Dir(path), CursorFile))
	if !cur.Repos["acme/widgets"].Equal(t0) {
		t.Fatalf("the cursor moved to %s past a merge it could not check", cur.Repos["acme/widgets"])
	}
	delete(g.apiErr, blobPath(head[wfPath]))
	wantGateOff(t, gateEntries(t, path, g),
		[3]string{"edited managed .github/workflows/ci.yml outside a sync", "no reason recorded", "gate-off-edited-files"})
}

func TestGateOffDedupesOnReharvest(t *testing.T) {
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), synced(t, "1.77.0", wf, nil), lock.Name)
	if got := gateEntries(t, path, g); len(got) != 1 {
		t.Fatalf("the first harvest raised:\n%s want the downgrade", describe(got))
	}
	n := len(entries(t, path))
	// Rewound, the harvest sees the PR again and adds nothing.
	if err := writeCursors(filepath.Join(filepath.Dir(path), CursorFile), cursors{Repos: map[string]time.Time{"acme/widgets": t0}}); err != nil {
		t.Fatal(err)
	}
	if res := harvest9(path, t0.Add(2*time.Hour), g); len(res.Added) != 0 || len(entries(t, path)) != n {
		t.Fatalf("a rerun added %+v", res.Added)
	}
	// And without the merge entry that normally stops it: a finding is deduped
	// by its own ref.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !strings.Contains(line, `"ref":"`+pr9URL+`"`) {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeCursors(filepath.Join(filepath.Dir(path), CursorFile), cursors{Repos: map[string]time.Time{"acme/widgets": t0}}); err != nil {
		t.Fatal(err)
	}
	res := harvest9(path, t0.Add(3*time.Hour), g)
	if len(res.Added) != 1 || res.Added[0].Ref != pr9URL {
		t.Fatalf("with the merge entry gone, a rerun added:\n%s want only the merge entry again", describe(res.Added))
	}
}

// --- cost --------------------------------------------------------------------

// The check costs nothing for a merge whose file list leaves the lock, the
// manifest and every managed path alone, once the harvest knows the managed
// paths. Until then it checks every merge, because it cannot tell.
func TestGateOffCostsNothingForAnOrdinaryMerge(t *testing.T) {
	path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), synced(t, "1.78.1", wf, tree{"Sources/App.swift": "app 2\n"}), "Sources/App.swift")
	gateEntries(t, path, g) // the first merge is read: the managed paths are not known yet
	if g.apiCalls() == 0 {
		t.Fatalf("the first merge was not checked, though nothing told the harvest which paths are managed")
	}

	// #10 touches only source: no read.
	g.calls = nil
	g.list = "[" + mergedWithCommit(10, "feature", "2026-09-24T13:30:00Z", head9, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") + "]"
	g.files = "[" + filesOf(10, "Sources/App.swift", "README.md") + "]"
	res := harvest9(path, t0.Add(2*time.Hour), g)
	if len(res.Added) != 1 || len(res.Unavailable) != 0 || g.apiCalls() != 0 {
		t.Fatalf("an ordinary merge: added %+v, unavailable %v, %d api calls %v; want the merge alone and no api call", res.Added, res.Unavailable, g.apiCalls(), g.calls)
	}

	// #11 touches the managed workflow: read.
	g.calls = nil
	merge11 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	g.list = "[" + mergedWithCommit(11, "tweak ci", "2026-09-24T14:30:00Z", head9, merge11) + "]"
	g.files = "[" + filesOf(11, wfPath) + "]"
	edited := synced(t, "1.78.1", wf, nil)
	edited[wfPath] = wf + "# x\n"
	scriptMerge(g, merge11, merge9, synced(t, "1.78.1", wf, nil), edited)
	res = harvest9(path, t0.Add(3*time.Hour), g)
	if len(res.Added) != 2 || !strings.Contains(res.Added[0].Title, "edited managed "+wfPath) {
		t.Fatalf("a managed-path merge: added:\n%s", describe(res.Added))
	}
	// commit, two trees, the lock once (same blob both sides), the workflow at
	// the merge, and at the base for the action-bump proof.
	if n := g.apiCalls(); n != 6 {
		t.Errorf("a touching merge made %d api calls, want 6: %v", n, g.calls)
	}
}

// "The files look did not list it" and "it lists 100 files, the most gh
// returns" both mean "may have touched a gate".
func TestGateOffChecksAMergeWhoseFileListIsMissingOrFull(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files func() string
	}{
		{"missing", func() string { return "[]" }},
		{"full", func() string {
			var ps []string
			for i := range 100 {
				ps = append(ps, fmt.Sprintf("Sources/F%d.swift", i))
			}
			return "[" + filesOf(9, ps...) + "]"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, g := gateFixture(t, synced(t, "1.78.1", wf, nil), synced(t, "1.77.0", wf, nil), "Sources/App.swift")
			// Teach it the managed paths first, so only the list decides.
			seedManaged(t, path, wfPath)
			g.files = tc.files()
			// The loosening look runs too, for a merge whose list is missing or
			// may have cut the ratchet file off.
			g.api[runsPath] = `{"total_count":0,"check_runs":[]}`
			wantGateOff(t, gateEntries(t, path, g),
				[3]string{"downgraded .lacquer.lock v1.78.1→v1.77.0", "no reason recorded", "gate-off-downgrade-1.78.1-1.77.0"})
		})
	}
}

// seedManaged records, in the cursor file, that acme/widgets's lock manages
// paths, as an earlier harvest would have.
func seedManaged(t *testing.T, inboxPath string, paths ...string) {
	t.Helper()
	p := filepath.Join(filepath.Dir(inboxPath), CursorFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if paths == nil {
		paths = []string{}
	}
	c["managed"] = map[string][]string{"acme/widgets": paths}
	out, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, out, 0o644); err != nil {
		t.Fatal(err)
	}
}
