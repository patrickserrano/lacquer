package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantOrphan records a file in .lacquer.lock that the lacquer does not ship,
// and puts it on disk — the state a project is left in when the lacquer retires
// an asset it once distributed. The real checkout is the lacquer under test, so
// the asset cannot be deleted from it; planting the leftover is the same shape
// from the project's side.
func plantOrphan(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, ".lacquer.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lk struct {
		Version any               `json:"version"`
		Files   map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &lk); err != nil {
		t.Fatal(err)
	}
	lk.Files[rel] = "0000000000000000000000000000000000000000000000000000000000000000"
	out, err := json.Marshal(lk)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("name: gone\non:\n  push:\njobs: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The wiring, not the classification: audit must actually consult the orphan
// check and print it. A function passing its own test says nothing about the
// shape CI runs.
func TestAuditReportsOrphansWithoutGating(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	plantOrphan(t, dir, ".github/workflows/web-legacy.yml")

	out.Reset()
	errb.Reset()
	code := run([]string{"audit"}, env, &out, &errb)
	all := out.String() + errb.String()

	// An orphan is a leftover file, not a broken project. Gating on something
	// that endangers nothing is the reliable way to teach people that lacquer
	// output is noise to be worked around.
	if code != 0 {
		t.Errorf("audit exited %d over an orphan; reporting is not the same decision as gating\n%s", code, all)
	}
	if !strings.Contains(all, ".github/workflows/web-legacy.yml") {
		t.Errorf("audit does not report the orphan:\n%s", all)
	}
	if !strings.Contains(all, "no longer shipped") {
		t.Errorf("audit has no orphan section:\n%s", all)
	}
}

// The property cmd/lacquer/retired_test.go pins from the drift side, checked
// against the orphan report specifically: a retired project drops a dozen
// destinations at once, and if those read as orphans the advice is to delete
// files the lacquer still ships — which would make retirement unusable.
func TestAuditReportsNoOrphansOnARetiredProject(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	// Sync LIVE first, so the scheduled workflows really do land and really are
	// recorded in the lock. Retiring a never-synced project would prove nothing.
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
	if strings.Contains(all, "no longer shipped") {
		t.Errorf("a retired project's dropped assets were reported as orphans:\n%s", all)
	}
	// Checked past the retirement banner, which names dependabot on purpose.
	_, report, ok := strings.Cut(all, "lacquer audit —")
	if !ok {
		t.Fatalf("no audit report in the output:\n%s", all)
	}
	for _, rel := range []string{".github/dependabot.yml", ".github/workflows/web-docs.yml"} {
		if strings.Contains(report, rel) {
			t.Errorf("audit names %s; the lacquer still ships it, this project has just stopped receiving it\n%s", rel, all)
		}
	}
}
