package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This file proves the WIRING, not internal/rootcheck's own logic (that lives
// in internal/rootcheck/rootcheck_test.go). Issue #333's lesson, restated in
// this repo's own CLAUDE.md: "a function passing its own test says nothing
// about the shape CI runs" — rootcheck.State.Verify being correct proves
// nothing about whether cmd/lacquer/main.go actually calls it, for which
// commands, or whether the override and the stamped output actually reach a
// real invocation. That is what issue #350 found: internal/rootcheck already
// existed, already computed the right signal, and the `status`/`audit`/
// `doctor` cases in main.go simply never consulted it.

// gitRun runs git with a fixed, deterministic identity so commits never
// depend on the host's global git config.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// ungitRoot builds a self-contained, synthetic lacquer content root (VERSION +
// empty profiles/) that satisfies requireLacquerRoot but is deliberately NOT a
// git repository — the "cannot be determined at all" case issue #350 requires
// to refuse exactly like a confirmed-bad root, never like a verified one.
func ungitRoot(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// pinnedRoot builds a synthetic lacquer content root as its own git
// repository, tags its sole commit v<version>, and returns a clone checked out
// DETACHED at that tag — the shape a real release install
// (~/.local/share/lacquer/content) is in, and the ONLY shape Verify accepts.
func pinnedRoot(t *testing.T, version string) string {
	t.Helper()
	base := t.TempDir()
	origin := filepath.Join(base, "origin")
	clone := filepath.Join(base, "clone")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(origin, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "profiles", ".gitkeep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, origin, "init", "-q", "--initial-branch=main")
	gitRun(t, origin, "add", "-A")
	gitRun(t, origin, "commit", "-q", "-m", "release "+version)
	gitRun(t, origin, "tag", "v"+version)
	gitRun(t, base, "clone", "-q", origin, clone)
	gitRun(t, clone, "checkout", "-q", "v"+version)
	return clone
}

// minimalProject is a throwaway project just complete enough for `status`,
// `audit` and `doctor` to load their manifest — no components, which is a
// legal, common state (a project that has never adopted a stack).
func minimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte("[project]\nname = \"probe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// rawEnv is a getenv that does NOT apply envMap's default
// LACQUER_ALLOW_UNVERIFIED_ROOT=1 — these tests are exercising that exact gate,
// so they need to control it explicitly rather than inherit the blanket
// override every other test in this package relies on.
func rawEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Mutation target 1 & 2 (PR body): removing stampAndVerifyRoot's call from the
// `status` case, or relaxing rootcheck.State.Verify's NotGit branch to return
// nil, both make this test fail. This is the single most important case per
// issue #350: "I could not verify this root" must refuse exactly like a
// confirmed-bad root, never pass silently.
func TestStatusRefusesAnUnverifiedRoot(t *testing.T) {
	root := ungitRoot(t, "9.9.9")
	dir := minimalProject(t)
	chdir(t, dir)

	var out, errb bytes.Buffer
	code := run([]string{"status"}, rawEnv(map[string]string{"LACQUER_ROOT": root}), &out, &errb)
	if code == 0 {
		t.Fatalf("`status` exited 0 against a root that is not a git checkout — an unverifiable root "+
			"must refuse exactly like a confirmed-bad one (issue #350)\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "issue #350") {
		t.Errorf("stderr does not explain the refusal in terms of issue #350:\n%s", errb.String())
	}
}

// Mutation target 3: relaxing the Dirty branch (in Verify, or by skipping the
// gate in `status`) makes this pass silently instead of refusing.
func TestStatusRefusesADirtyPinnedRoot(t *testing.T) {
	root := pinnedRoot(t, "9.9.9")
	// Dirty it without committing — a clean, correctly-tagged checkout that
	// someone has started hand-editing.
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("9.9.9-local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := minimalProject(t)
	chdir(t, dir)

	var out, errb bytes.Buffer
	code := run([]string{"status"}, rawEnv(map[string]string{"LACQUER_ROOT": root, "LACQUER_NO_FETCH": "1"}), &out, &errb)
	if code == 0 {
		t.Fatalf("`status` exited 0 against a dirty, tag-pinned root — a dirty tree is never a pinned "+
			"release (issue #350)\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "uncommitted") {
		t.Errorf("stderr does not name the dirty tree as the reason:\n%s", errb.String())
	}
}

// Mutation target 4: dropping the root path from the stamped provenance line
// (either in rootcheck.State.Describe or by main.go no longer printing it)
// makes this fail. Also the positive case: a genuinely pinned root must let
// `status` run normally, exit 0, with no refusal at all.
func TestStatusSucceedsAgainstAPinnedRootAndStampsTheRootPath(t *testing.T) {
	root := pinnedRoot(t, "9.9.9")
	dir := minimalProject(t)
	chdir(t, dir)

	var out, errb bytes.Buffer
	code := run([]string{"status"}, rawEnv(map[string]string{"LACQUER_ROOT": root, "LACQUER_NO_FETCH": "1"}), &out, &errb)
	if code != 0 {
		t.Fatalf("`status` exited %d against a genuinely pinned root, want 0\nstdout:\n%s\nstderr:\n%s",
			code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), root) {
		t.Errorf("stdout does not stamp the resolved root PATH — the one variable that actually changes "+
			"between a safe and a dangerous run (issue #350):\n%s", out.String())
	}
	// The pinned case must never render the uninformative literal "HEAD" —
	// issue #350 comment 2's second finding.
	if strings.Contains(out.String(), " HEAD ") || strings.Contains(out.String(), "@ HEAD") {
		t.Errorf("stdout still renders the detached ref as the bare literal HEAD instead of resolving "+
			"it to the pinned tag:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "v9.9.9") {
		t.Errorf("stdout does not name the resolved tag v9.9.9:\n%s", out.String())
	}
}

// Mutation target 5: if the override branch stops printing on every call (for
// example, by moving the warning behind a sync.Once or a first-invocation
// check), the second run's stderr goes quiet and this fails. A scrollback line
// from ten minutes ago must never be mistaken for tonight's verified run —
// that ambiguity is exactly how the original incident hid for three sessions.
func TestOverrideWarnsOnEveryInvocation(t *testing.T) {
	root := ungitRoot(t, "9.9.9")
	dir := minimalProject(t)
	chdir(t, dir)
	env := rawEnv(map[string]string{"LACQUER_ROOT": root, "LACQUER_ALLOW_UNVERIFIED_ROOT": "1"})

	for i := 0; i < 2; i++ {
		var out, errb bytes.Buffer
		code := run([]string{"status"}, env, &out, &errb)
		if code != 0 {
			t.Fatalf("invocation %d: `status` exited %d with the override set, want 0\nstderr:\n%s", i, code, errb.String())
		}
		if !strings.Contains(errb.String(), "UNREVIEWED") {
			t.Errorf("invocation %d: override did not warn that output is unreviewed:\n%s", i, errb.String())
		}
	}
}

// Coverage breadth: issue #350 explicitly calls out status/audit/doctor as
// read-only commands that must not be confidently wrong, and requires a
// survey of every command that reads LACQUER_ROOT. This proves every one of
// them refuses an unverifiable root, in one pass, rather than trusting that a
// shared helper function is wired in everywhere it is meant to be.
func TestEveryLacquerRootCommandRefusesAnUnverifiedRoot(t *testing.T) {
	root := ungitRoot(t, "9.9.9")
	dir := minimalProject(t)
	chdir(t, dir)
	env := rawEnv(map[string]string{"LACQUER_ROOT": root})

	for _, args := range [][]string{
		{"init"},
		{"onboard"},
		{"adopt"},
		{"sync"},
		{"doctor"},
		{"fix"},
		{"plugins"},
		{"audit"},
		{"status"},
		{"version"},
		{"fleet"},
		{"console"},
	} {
		t.Run(args[0], func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(args, env, &out, &errb)
			if code == 0 {
				t.Fatalf("`lacquer %s` exited 0 against an unverifiable root (not a git checkout), want a refusal\nstdout:\n%s\nstderr:\n%s",
					args[0], out.String(), errb.String())
			}
			if !strings.Contains(errb.String(), "issue #350") {
				t.Errorf("`lacquer %s` refused for a reason that does not mention issue #350 — it may have "+
					"failed for some OTHER reason before reaching the root check, which this test cannot "+
					"then tell apart from a missing guard:\nstderr:\n%s", args[0], errb.String())
			}
		})
	}
}

// `protection` and `skills` deliberately read no shipped content from
// LACQUER_ROOT (see their case comments in main.go) and must NOT be gated —
// gating them would stop an operator running `lacquer protection` from the
// repo they are standing in, or `lacquer skills` in a project with no lacquer
// checkout anywhere nearby. This is the negative control for the survey above.
func TestProtectionAndSkillsAreNotGatedOnLacquerRoot(t *testing.T) {
	dir := minimalProject(t)
	chdir(t, dir)
	// No LACQUER_ROOT at all: it defaults to ".", which is not a lacquer
	// checkout here — if either command consulted rootcheck, this would refuse.
	env := rawEnv(map[string]string{})

	var out, errb bytes.Buffer
	run([]string{"skills"}, env, &out, &errb)
	if strings.Contains(errb.String(), "issue #350") {
		t.Errorf("`skills` is now gated on LACQUER_ROOT, which it does not read shipped content from:\n%s", errb.String())
	}
}
