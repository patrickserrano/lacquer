package audit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/lock"
	syncpkg "github.com/patrickserrano/lacquer/internal/sync"
)

// A lacquer with one profile shipping three workflows: a PR gate, a scheduled
// job (which retirement drops), and one more to exclude. Enough to tell the
// three ways a destination can leave the plan apart from each other.
const (
	prWorkflow        = "name: ci\non:\n  pull_request:\njobs: {}\n"
	scheduledWorkflow = "name: nightly\non:\n  schedule:\n    - cron: '0 3 * * *'\njobs: {}\n"
	pushWorkflow      = "name: docs\non:\n  push:\n    branches: [main]\njobs: {}\n"
)

const orphanManifest = "[project]\nname=\"x\"\n\n[[component]]\npath=\".\"\nprofiles=[\"web\"]\n"

// orphanSetup builds the lacquer + project, syncs once, and commits, so the
// lock records everything the lacquer shipped at that moment.
func orphanSetup(t *testing.T) (lacquer, project string) {
	t.Helper()
	lacquer = t.TempDir()
	project = t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE RULES")
	writeFile(t, filepath.Join(lacquer, "core", "skills", "git.md"), "GIT SKILL")
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "CLAUDE.web.md"), "WEB RULES")
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "workflows", "ci.yml"), prWorkflow)
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "workflows", "nightly.yml"), scheduledWorkflow)
	writeFile(t, filepath.Join(lacquer, "profiles", "web", "workflows", "docs.yml"), pushWorkflow)
	writeFile(t, filepath.Join(project, ".lacquer.toml"), orphanManifest)
	git(t, project, "init", "-q")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "init")

	if _, err := syncpkg.Run(lacquer, project, false); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "sync")
	return lacquer, project
}

func orphanLabels(t *testing.T, lacquer, project string) []string {
	t.Helper()
	orphans, err := audit.Orphans(lacquer, project)
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	out := make([]string, 0, len(orphans))
	for _, o := range orphans {
		out = append(out, o.Label())
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A freshly synced project has nothing left over.
func TestNoOrphansOnACleanProject(t *testing.T) {
	lacquer, project := orphanSetup(t)
	if got := orphanLabels(t, lacquer, project); len(got) != 0 {
		t.Errorf("orphans = %v on a just-synced project, want none", got)
	}
}

// The measured case: the lacquer retires a workflow. sync will never delete the
// copy already in the project — deliberately — and before this nothing reported
// it either, so one retired workflow lived on in thirteen repositories.
func TestOrphanWhenTheLacquerStopsShippingAnAsset(t *testing.T) {
	lacquer, project := orphanSetup(t)
	if err := os.Remove(filepath.Join(lacquer, "profiles", "web", "workflows", "docs.yml")); err != nil {
		t.Fatal(err)
	}

	got := orphanLabels(t, lacquer, project)
	if !contains(got, ".github/workflows/web-docs.yml") {
		t.Fatalf("orphans = %v; the lacquer stopped shipping web-docs.yml and the project still has it", got)
	}
	// Only that one. Everything else is still shipped.
	if len(got) != 1 {
		t.Errorf("orphans = %v, want exactly the dropped workflow", got)
	}
	// The report has to say whose the file is now, since the tool will not
	// remove it.
	out := audit.FormatOrphans(mustOrphans(t, lacquer, project))
	if !strings.Contains(out, ".github/workflows/web-docs.yml") {
		t.Errorf("report does not name the orphan:\n%s", out)
	}
	if !strings.Contains(out, "delete") {
		t.Errorf("report does not tell the operator the file is theirs to delete:\n%s", out)
	}
}

// [project].exclude removes a destination from the plan, and the lacquer still
// ships it. Calling that an orphan would advise deleting a file that comes
// straight back the day the exclusion is lifted.
func TestExcludedPathIsNotAnOrphan(t *testing.T) {
	lacquer, project := orphanSetup(t)
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\nexclude=[{ path = \".github/workflows/web-docs.yml\", reason = \"hand-tuned here\" }]\n\n"+
			"[[component]]\npath=\".\"\nprofiles=[\"web\"]\n")

	got := orphanLabels(t, lacquer, project)
	if contains(got, ".github/workflows/web-docs.yml") {
		t.Errorf("an excluded path was reported as an orphan: %v", got)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %v, want none", got)
	}
}

// Retirement drops every scheduled workflow and dependabot.yml at once. If those
// read as orphans, a retired project's audit fills with advice to delete files
// the lacquer still ships — and retirement becomes unusable. cmd/lacquer's
// retired_test.go pins the same property from the other end.
func TestRetiredProjectsDroppedWorkflowsAreNotOrphans(t *testing.T) {
	lacquer, project := orphanSetup(t)
	// The scheduled workflow really did land while the project was live.
	if _, err := os.Stat(filepath.Join(project, ".github", "workflows", "web-nightly.yml")); err != nil {
		t.Fatalf("expected the live sync to write web-nightly.yml: %v", err)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\nretired = { since = \"2026-08-18\", reason = \"not a viable app\" }\n\n"+
			"[[component]]\npath=\".\"\nprofiles=[\"web\"]\n")

	got := orphanLabels(t, lacquer, project)
	if contains(got, ".github/workflows/web-nightly.yml") {
		t.Errorf("a retired project's dropped scheduled workflow was reported as an orphan: %v", got)
	}
	if len(got) != 0 {
		t.Errorf("orphans = %v, want none: retirement stops this project receiving them, it does not stop the lacquer shipping them", got)
	}
}

// Both at once, because the two dropping mechanisms are checked in sequence
// inside the planner and a fix for one could quietly reintroduce the other.
func TestRetiredAndExcludedTogetherAreNotOrphans(t *testing.T) {
	lacquer, project := orphanSetup(t)
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n"+
			"retired = { since = \"2026-08-18\", reason = \"not a viable app\" }\n"+
			"exclude = [{ path = \".github/workflows/web-docs.yml\", reason = \"hand-tuned here\" }]\n\n"+
			"[[component]]\npath=\".\"\nprofiles=[\"web\"]\n")
	if got := orphanLabels(t, lacquer, project); len(got) != 0 {
		t.Errorf("orphans = %v, want none", got)
	}
}

// Once the operator deletes the file there is nothing left to say, and the lock
// entry is cleared by the next sync. Reporting it until then would train people
// to ignore the section.
func TestDeletedOrphanStopsBeingReported(t *testing.T) {
	lacquer, project := orphanSetup(t)
	if err := os.Remove(filepath.Join(lacquer, "profiles", "web", "workflows", "docs.yml")); err != nil {
		t.Fatal(err)
	}
	if got := orphanLabels(t, lacquer, project); len(got) != 1 {
		t.Fatalf("orphans = %v, want the dropped workflow", got)
	}
	if err := os.Remove(filepath.Join(project, ".github", "workflows", "web-docs.yml")); err != nil {
		t.Fatal(err)
	}
	if got := orphanLabels(t, lacquer, project); len(got) != 0 {
		t.Errorf("orphans = %v after the file was deleted, want none", got)
	}
}

// The lock is the evidence, and sync rewrites it from the current plan — so
// without carrying orphans forward, the FIRST sync after the lacquer drops an
// asset erases the only record that the project ever received it. Since a
// lacquer update is normally followed by `sync`, not by `audit`, that erasure
// would usually happen before anyone saw the report.
func TestOrphanSurvivesASync(t *testing.T) {
	lacquer, project := orphanSetup(t)
	if err := os.Remove(filepath.Join(lacquer, "profiles", "web", "workflows", "docs.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := syncpkg.Run(lacquer, project, false); err != nil {
		t.Fatalf("sync after the lacquer dropped an asset: %v", err)
	}
	lk, locked, err := lock.Read(project)
	if err != nil || !locked {
		t.Fatalf("lock.Read: %v (locked=%v)", err, locked)
	}
	if _, ok := lk.Files[".github/workflows/web-docs.yml"]; !ok {
		t.Error("sync dropped the orphan's lock entry; the evidence is gone and the leftover file is invisible again")
	}
	if got := orphanLabels(t, lacquer, project); !contains(got, ".github/workflows/web-docs.yml") {
		t.Errorf("orphans = %v after a sync, want the dropped workflow still reported", got)
	}
}

// A project that never synced has no record of what the lacquer wrote, so
// nothing on disk can be attributed to it.
func TestNoLockMeansNoOrphans(t *testing.T) {
	lacquer, project := orphanSetup(t)
	if err := os.Remove(filepath.Join(lacquer, "profiles", "web", "workflows", "docs.yml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(project, ".lacquer.lock")); err != nil {
		t.Fatal(err)
	}
	if got := orphanLabels(t, lacquer, project); len(got) != 0 {
		t.Errorf("orphans = %v with no lockfile, want none", got)
	}
}

// A managed REGION can be orphaned too — drop the profile and the block stays in
// a file the project otherwise owns. The report has to name the marker, because
// "delete the file" is the wrong advice for a CLAUDE.md the project wrote most
// of.
func TestOrphanedRegionNamesItsMarker(t *testing.T) {
	lacquer, project := orphanSetup(t)
	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\".\"\nprofiles=[]\n")

	got := orphanLabels(t, lacquer, project)
	if !contains(got, "CLAUDE.md#web") {
		t.Fatalf("orphans = %v, want the dropped profile's region labelled with its marker", got)
	}
	out := audit.FormatOrphans(mustOrphans(t, lacquer, project))
	if !strings.Contains(out, "CLAUDE.md#web") || !strings.Contains(out, "managed region") {
		t.Errorf("report does not distinguish a region from a whole file:\n%s", out)
	}
}

// Presence for a region is decided by finding its marker, and .gitignore is the
// one managed region that is not markdown. Reading it with the wrong comment
// syntax would silently UNDER-report — the failure mode that never shows up as
// an error — so the syntax is pinned here rather than left to read correctly.
//
// The second half is the other side of the same guard: a key with no block in
// the file must NOT be reported, or "is it present" degenerates into "does the
// file exist" and every renamed marker becomes a phantom orphan.
func TestGitignoreRegionOrphanIsFoundWithHashSyntax(t *testing.T) {
	lacquer, project := orphanSetup(t)
	ignorePath := filepath.Join(project, ".gitignore")
	data, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	// A region the lacquer used to merge into .gitignore, in that file's OWN
	// comment syntax. Nothing ships it now, so it is an orphan — if it can be
	// found.
	writeFile(t, ignorePath, string(data)+
		"\n# lacquer:legacy-web:start v1\nnode_modules/\n# lacquer:legacy-web:end\n")

	lk, _, err := lock.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	lk.Files[".gitignore#legacy-web"] = "deadbeef"
	lk.Files[".gitignore#never-written"] = "deadbeef"
	if err := lock.Write(project, lk); err != nil {
		t.Fatal(err)
	}

	got := orphanLabels(t, lacquer, project)
	if !contains(got, ".gitignore#legacy-web") {
		t.Errorf("orphans = %v; a `#`-commented region in .gitignore was not found — reading it as markdown under-reports silently", got)
	}
	if contains(got, ".gitignore#never-written") {
		t.Errorf("orphans = %v; a marker that is not in the file was reported, so presence is only checking that the file exists", got)
	}
}

// A hand-edited lockfile is not a trusted source of paths.
func TestOrphanIgnoresEscapingLockKeys(t *testing.T) {
	lacquer, project := orphanSetup(t)
	lk, _, err := lock.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	lk.Files["../outside.yml"] = "deadbeef"
	lk.Files["/etc/hosts"] = "deadbeef"
	if err := lock.Write(project, lk); err != nil {
		t.Fatal(err)
	}
	for _, o := range mustOrphans(t, lacquer, project) {
		if strings.HasPrefix(o.Dest, "..") || filepath.IsAbs(o.Dest) {
			t.Errorf("orphan %q escapes the project root", o.Dest)
		}
	}
}

func mustOrphans(t *testing.T, lacquer, project string) []audit.Orphan {
	t.Helper()
	o, err := audit.Orphans(lacquer, project)
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	return o
}
