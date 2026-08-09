package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file exists because of lacquer#118, a fleet-wide breakage this repo
// shipped and did not notice.
//
// #114 made `audit` re-run stack detection. The `No lacquer drift` CI job clones
// the lacquer into `.lacquer-checkout` INSIDE the project workspace so it has a
// binary to audit with — and that clone promptly registered as undeclared
// project stacks:
//
//	stacks on disk that .lacquer.toml does not declare:
//	  .lacquer-checkout/site -> web
//	lacquer audit failed (exit 6).
//
// Every project carrying the drift job failed that way, on a directory the job
// itself creates. Nothing here caught it: the unit tests all passed, because
// each one tested a function rather than the shape the job actually runs in. It
// surfaced only because one repo happened to have a PR open.
//
// So these tests drive the real CLI end to end against a throwaway project,
// reproducing the drift job's own layout. The drift job is the check that
// verifies every other check is in place; it was the one thing nothing checked.

// fixtureProject builds a throwaway project that looks like a real one: a git
// repo, a manifest, and the markers detection keys on.
func fixtureProject(t *testing.T, lacquerRoot string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []struct{ path, body string }{
		{"package.json", "{}"},
		{"server/supabase/config.toml", ""},
		{".lacquer.toml", "[project]\nname = \"fixture\"\ngithub_org = \"acme\"\n\n" +
			"[[component]]\npath = \".\"\nprofiles = [\"web\"]\n\n" +
			"[[component]]\npath = \"server\"\nprofiles = [\"supabase\"]\n"},
	} {
		p := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// assets.Preflight refuses to write outside a git work tree.
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// realLacquer is this checkout — the actual shipped content, not a fixture of
// it. A stub lacquer would not have caught #118, because the bug was in how
// detection read a directory containing this repo's own layout.
func realLacquer(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "VERSION")); err != nil {
		t.Skipf("not running from a lacquer checkout: %v", err)
	}
	return root
}

// The end-to-end shape: sync a real project, then audit it. Both must succeed
// against the content this repo ships right now.
func TestFixtureSyncThenAuditIsClean(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"audit"}, env, &out, &errb); code != 0 {
		t.Fatalf("audit exited %d on a freshly synced project\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
}

// lacquer#118 itself. The drift job clones this repo into `.lacquer-checkout`
// inside the project and audits from there; that directory must never read as
// project source. Regression-locked with the real checkout, since a stub would
// not reproduce it.
func TestAuditIgnoresTheLacquerCheckoutItPlantsInTheProject(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)

	// Exactly what the drift job does: put the lacquer inside the workspace.
	cmd := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+lq,
		filepath.Join(dir, ".lacquer-checkout"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot clone the lacquer into the fixture: %v\n%s", err, out)
	}

	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d with a lacquer checkout present\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
	out.Reset()
	errb.Reset()
	code := run([]string{"audit"}, env, &out, &errb)
	if code != 0 {
		t.Fatalf("audit exited %d — the drift job's own .lacquer-checkout is being read as project source (this is #118)\nstdout: %s\nstderr: %s",
			code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), ".lacquer-checkout") {
		t.Errorf("audit output mentions .lacquer-checkout as a component:\n%s", out.String())
	}
}

// Agent worktrees are full checkouts of the project (lacquer#121). Same class:
// a directory inside the project that is not project source.
func TestAuditIgnoresAgentWorktrees(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	for _, p := range []string{
		".claude/worktrees/agent-1/package.json",
		".codex/worktrees/x/server/supabase/config.toml",
	} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstderr: %s", code, errb.String())
	}
	out.Reset()
	if code := run([]string{"audit"}, env, &out, &errb); code != 0 {
		t.Fatalf("audit exited %d — an agent worktree is being read as project source\nstdout: %s",
			code, out.String())
	}
}

// writeManifest replaces the fixture's manifest, keeping the components so the
// project still syncs. Used to vary only the [project] stanza under test.
func writeManifest(t *testing.T, dir, projectExtra string) {
	t.Helper()
	body := "[project]\nname = \"fixture\"\ngithub_org = \"acme\"\n" + projectExtra + "\n\n" +
		"[[component]]\npath = \".\"\nprofiles = [\"web\"]\n\n" +
		"[[component]]\npath = \"server\"\nprofiles = [\"supabase\"]\n"
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// auditRealFixture syncs a fixture project, rewrites its [project] stanza, and
// returns audit's exit code and combined output.
func auditRealFixture(t *testing.T, projectExtra string) (int, string) {
	t.Helper()
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	writeManifest(t, dir, projectExtra)
	out.Reset()
	errb.Reset()
	code := run([]string{"audit"}, env, &out, &errb)
	return code, out.String() + errb.String()
}

// An expired [project].exclude must fail the audit exactly as an expired
// [baseline.relax] does. Unit tests cover the classification; this proves the
// wiring — that audit actually consults the review and turns it into an exit
// code. #118's lesson was that a function passing its own test says nothing
// about the shape CI runs.
func TestAuditFailsOnExpiredExclusion(t *testing.T) {
	code, out := auditRealFixture(t,
		"exclude = [{ path = \".github/workflows/web-ci.yml\", reason = \"pending\", until = \"2020-01-01\" }]")
	if code != 4 {
		t.Errorf("audit exited %d, want 4 on an expired exclusion\n%s", code, out)
	}
	if !strings.Contains(out, "EXPIRED") {
		t.Errorf("the report must name the expiry\n%s", out)
	}
}

// The bare-string form every existing manifest uses must keep the project
// loading and auditing — reported, but never gated. Shipping a hard failure
// here would have turned five green repos red for a documentation defect.
func TestAuditReportsButDoesNotGateBareStringExclusion(t *testing.T) {
	code, out := auditRealFixture(t, "exclude = [\".github/workflows/web-ci.yml\"]")
	if code != 0 {
		t.Errorf("audit exited %d, want 0: an unattributed exclusion must not gate\n%s", code, out)
	}
	if !strings.Contains(out, "no reason recorded") {
		t.Errorf("an unattributed exclusion must be reported\n%s", out)
	}
}

// A stale exclusion names a path the lacquer does not ship, so it suppresses
// nothing. Reported, never gated.
func TestAuditReportsStaleExclusion(t *testing.T) {
	code, out := auditRealFixture(t,
		"exclude = [{ path = \".github/workflows/retired-long-ago.yml\", reason = \"documented\" }]")
	if code != 0 {
		t.Errorf("audit exited %d, want 0: a stale exclusion must not gate\n%s", code, out)
	}
	if !strings.Contains(out, "suppresses nothing") {
		t.Errorf("a stale exclusion must be reported so it can be deleted\n%s", out)
	}
}

// A well-formed, live exclusion is a legitimate permanent divergence and must
// print nothing at all. A report that nags about correct configuration is how
// people learn to ignore the report.
func TestAuditIsSilentOnHealthyExclusion(t *testing.T) {
	code, out := auditRealFixture(t,
		"exclude = [{ path = \".github/workflows/web-ci.yml\", reason = \"hand-tuned for this project's build env\" }]")
	if code != 0 {
		t.Errorf("audit exited %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "exclusions needing attention") {
		t.Errorf("a healthy exclusion must not be reported\n%s", out)
	}
}

// A clobbered file used to make `audit` return before it reported drift or
// exclusions, so the projects furthest out of date got the least information —
// fix one problem, re-run, learn about the next. Reporting and gating are
// separate decisions: everything known is printed, then the exit code is ranked.
func TestAuditStillReportsExclusionsWhenClobbered(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	// Edit a managed file so sync would clobber it, and add an unattributed
	// exclusion for an unrelated path.
	managed := filepath.Join(dir, ".github", "workflows", "web-ci.yml")
	body, err := os.ReadFile(managed)
	if err != nil {
		t.Fatalf("expected sync to write %s: %v", managed, err)
	}
	if err := os.WriteFile(managed, append(body, []byte("\n# local edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, dir, "exclude = [\"biome.json\"]")

	out.Reset()
	errb.Reset()
	code := run([]string{"audit"}, env, &out, &errb)
	all := out.String() + errb.String()
	if code != 3 {
		t.Errorf("audit exited %d, want 3 (clobber still outranks everything)\n%s", code, all)
	}
	if !strings.Contains(all, "no reason recorded") {
		t.Errorf("exclusions must still be reported when a file is clobbered\n%s", all)
	}
}
