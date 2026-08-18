package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The retired manifest for the fixture project (web + supabase components, so
// both profiles' scheduled workflows are in play).
const retiredStanza = "retired = { since = \"2026-08-18\", reason = \"not a viable app\" }"

// syncRetired builds a fixture, retires it, and syncs. Returns the project dir
// and the env the CLI runs under.
func syncRetired(t *testing.T) (string, func(string) string) {
	t.Helper()
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	writeManifest(t, dir, retiredStanza)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d on a retired project\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	return dir, env
}

// The whole point: a retired project stops receiving scheduled work and keeps
// everything else. Checked on disk after a real sync, not in the plan.
func TestSyncRetiredDropsScheduledWorkOnly(t *testing.T) {
	dir, _ := syncRetired(t)

	for _, rel := range []string{
		".github/dependabot.yml",
		".github/workflows/web-docs.yml",
		".github/workflows/supabase-docs.yml",
		".github/workflows/supabase-health.yml",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("%s was written to a retired project — it costs money on a timer", rel)
		}
	}
	for _, rel := range []string{
		".github/workflows/web-ci.yml",
		".github/workflows/supabase-ci.yml",
		".github/workflows/web-dependency-review.yml",
		".github/workflows/web-env-validation.yml",
		"biome.json",
		"lefthook.yml",
		"CLAUDE.md",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("%s is missing from a retired project: %v — retirement stops the spend, not the sync", rel, err)
		}
	}
}

// A retired project must audit clean. The dropped files must not read as drift,
// as missing, or as undeclared — otherwise every retired repo's CI goes red and
// stays red, which is how people learn to ignore the gate.
func TestAuditIsCleanOnRetiredProject(t *testing.T) {
	_, env := syncRetired(t)
	var out, errb bytes.Buffer
	code := run([]string{"audit"}, env, &out, &errb)
	all := out.String() + errb.String()
	if code != 0 {
		t.Fatalf("audit exited %d on a freshly synced retired project\n%s", code, all)
	}
	// The dropped assets are not managed units, so they must not appear under any
	// status heading — `add` in particular, which is what "missing" would look
	// like. Checked past the retirement banner, which names dependabot on purpose.
	_, report, ok := strings.Cut(all, "lacquer audit —")
	if !ok {
		t.Fatalf("no audit report in the output:\n%s", all)
	}
	for _, rel := range []string{".github/dependabot.yml", ".github/workflows/web-docs.yml"} {
		if strings.Contains(report, rel) {
			t.Errorf("audit mentions %s; a dropped asset must not read as a finding\n%s", rel, all)
		}
	}
	if strings.Contains(all, "exclusions needing attention") {
		t.Errorf("retirement is not an exclusion and must not be reported as one\n%s", all)
	}
}

// The realistic case: a live project that already synced, then retired. The
// scheduled workflows are still sitting in the repo (sync does not delete), and
// they must still not register as drift.
func TestAuditIsCleanWhenRetiredAfterTheFilesLanded(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "workflows", "web-docs.yml")); err != nil {
		t.Fatalf("expected the live sync to write web-docs.yml: %v", err)
	}
	writeManifest(t, dir, retiredStanza)

	out.Reset()
	errb.Reset()
	code := run([]string{"audit"}, env, &out, &errb)
	all := out.String() + errb.String()
	if code != 0 {
		t.Fatalf("audit exited %d after retiring a synced project\n%s", code, all)
	}
}

// audit must say the project is retired. Its report is SHORT on a retired
// project, and a short clean report is exactly what a healthy one produces.
func TestAuditReportsRetirement(t *testing.T) {
	_, env := syncRetired(t)
	var out, errb bytes.Buffer
	if code := run([]string{"audit"}, env, &out, &errb); code != 0 {
		t.Fatalf("audit exited %d\n%s%s", code, out.String(), errb.String())
	}
	assertRetirementReported(t, "audit", out.String())
}

// status is the command people run to ask "is this project fine?".
func TestStatusReportsRetirement(t *testing.T) {
	_, env := syncRetired(t)
	var out, errb bytes.Buffer
	if code := run([]string{"status"}, env, &out, &errb); code != 0 {
		t.Fatalf("status exited %d\n%s%s", code, out.String(), errb.String())
	}
	assertRetirementReported(t, "status", out.String())
}

func assertRetirementReported(t *testing.T, cmd, out string) {
	t.Helper()
	for _, want := range []string{"RETIRED", "2026-08-18", "not a viable app"} {
		if !strings.Contains(out, want) {
			t.Errorf("`lacquer %s` output does not contain %q — a retired project must never "+
				"be mistaken for a healthy one:\n%s", cmd, want, out)
		}
	}
}

// A live project prints no retirement banner at all. A report that decorates
// healthy output is one people learn to skip.
func TestStatusSaysNothingAboutRetirementOnALiveProject(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out, errb bytes.Buffer
	if code := run([]string{"status"}, env, &out, &errb); code != 0 {
		t.Fatalf("status exited %d\n%s%s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "RETIRED") {
		t.Errorf("a live project must not be reported as retired:\n%s", out.String())
	}
	_ = dir
}

// A malformed retirement must stop the command, not be ignored. A tolerated
// half-entry is a project that believes it is retired while still paying.
func TestCommandsRejectMalformedRetirement(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	writeManifest(t, dir, "retired = { since = \"2026-08-18\" }")

	for _, cmd := range []string{"status", "audit", "sync"} {
		var out, errb bytes.Buffer
		code := run([]string{cmd}, env, &out, &errb)
		all := out.String() + errb.String()
		if code == 0 {
			t.Errorf("`lacquer %s` exited 0 on a retirement with no reason\n%s", cmd, all)
		}
		if !strings.Contains(all, "reason") {
			t.Errorf("`lacquer %s` must say what is missing\n%s", cmd, all)
		}
	}
}
