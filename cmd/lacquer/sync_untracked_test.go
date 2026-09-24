package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/lock"
)

func TestSyncUntrackedNoticeAndAuditExit(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	const dest = "scripts/check-secrets.sh"
	target := filepath.Join(dir, dest)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# project-owned scanner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "project scanner"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v\n%s", err, out)
		}
	}
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync = %d: %s", code, &errb)
	}
	if !strings.Contains(out.String(), "first sync replaced pre-existing content (no lock baseline):\n  "+dest) {
		t.Fatalf("missing first-sync notice:\n%s", &out)
	}
	lk, _, err := lock.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	delete(lk.Files, dest)
	if err := lock.Write(dir, lk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# project-owned scanner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"audit"}, env, &out, &errb); code != 3 {
		t.Fatalf("audit = %d, want clobber exit 3:\n%s\n%s", code, &out, &errb)
	}
	if !strings.Contains(out.String(), "untracked-conflict:\n  "+dest) {
		t.Fatalf("missing collision heading:\n%s", &out)
	}
}
