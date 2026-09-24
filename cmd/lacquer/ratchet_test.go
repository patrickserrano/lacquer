package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRatchetAuditRejectsRegression(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &out); code != 0 {
		t.Fatalf("sync: %d %s", code, &out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.ratchet.toml"), []byte("[ratchet]\nclaude_lines = 100000\nunjustified_suppressions = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.ts"), []byte("// eslint-disable-next-line no-console\nconsole.log('bad');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "bad.ts")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, b)
	}
	out.Reset()
	if code := run([]string{"audit"}, env, &out, &out); code != 4 || !strings.Contains(out.String(), "ratchet: unjustified_suppressions regressed 0 → 1") {
		t.Fatalf("audit = %d, want 4 and old/new metric:\n%s", code, &out)
	}
}

func TestRatchetDoctorProvesTightenThenRegression(t *testing.T) {
	r := ratchetProbe()
	if !r.OK {
		t.Fatal(r.Detail)
	}
}

func TestRatchetCLILooseningAndHistoricalReason(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out bytes.Buffer
	invoke := func(want int, args ...string) {
		t.Helper()
		out.Reset()
		if code := run(args, env, &out, &out); code != want {
			t.Fatalf("%v = %d, want %d: %s", args, code, want, &out)
		}
	}
	invoke(0, "sync")
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "synced fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, b)
		}
	}
	invoke(0, "ratchet", "--write")
	path := filepath.Join(dir, "source.ts")
	if err := os.WriteFile(path, []byte("// eslint-disable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "source.ts")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, b)
	}
	invoke(4, "ratchet", "--write")
	invoke(4, "sync")
	invoke(1, "ratchet", "--loosen", "unjustified_suppressions")
	invoke(0, "ratchet", "--loosen", "unjustified_suppressions", "--reason", "legacy migration")
	invoke(0, "audit")
	if err := os.WriteFile(path, []byte("// eslint-disable\n// eslint-disable-next-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(4, "audit")
	invoke(2, "ratchet", "--write", "--loosen", "claude_lines")
	invoke(2, "ratchet", "--reason", "orphan reason")
}
