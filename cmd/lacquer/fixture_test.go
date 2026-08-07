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
