package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/console"
)

// A failed launch must reach the sessions file through the CLI, not only in
// console's return value: that file is what `watch` reads, and before this a
// failed dispatch-role left nothing in it, so nobody but the agent that ran
// it could see it had failed.
func TestConsoleDispatchRoleRecordsAFailedLaunch(t *testing.T) {
	lq := realLacquer(t)
	dir := t.TempDir()

	// A tmux that fails every new-session the way the pre-fix launch did in
	// an agent's shell, and reports no sessions.
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in\n  new-session) echo 'open terminal failed: not a terminal' >&2; exit 1 ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	rolesPath := filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(rolesPath, []byte("[[role]]\nname = \"lead\"\ntask = \"lead the fleet\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionsPath := filepath.Join(dir, "sessions.jsonl")

	_, errb, code := runConsole(t, lq, []string{"--roles", rolesPath, "--sessions", sessionsPath, "dispatch-role", "lead"})
	if code == 0 {
		t.Fatal("a failed launch must exit non-zero")
	}
	if !strings.Contains(errb, "not a terminal") {
		t.Errorf("stderr must carry tmux's own error:\n%s", errb)
	}
	records, err := console.ReadRecords(sessionsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("sessions file has %d records, want the 1 failed launch", len(records))
	}
	r := records[0]
	if r.Name != "lead" || r.Kind != console.RoleKind || r.Mode != console.Tmux || r.Task != "lead the fleet" {
		t.Errorf("record = %+v", r)
	}
	if !strings.Contains(r.LaunchError, "not a terminal") {
		t.Errorf("record LaunchError = %q", r.LaunchError)
	}
}

// A dry run records nothing, even with a sessions file configured.
func TestConsoleDispatchRoleDryRunRecordsNothing(t *testing.T) {
	lq := realLacquer(t)
	dir := t.TempDir()
	rolesPath := filepath.Join(dir, "roles.toml")
	if err := os.WriteFile(rolesPath, []byte("[[role]]\nname = \"lead\"\ntask = \"lead the fleet\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionsPath := filepath.Join(dir, "sessions.jsonl")

	out, errb, code := runConsole(t, lq, []string{"--roles", rolesPath, "--sessions", sessionsPath, "--dry-run", "dispatch-role", "lead"})
	if code != 0 {
		t.Fatalf("dry run failed (code %d): %s", code, errb)
	}
	if !strings.Contains(out, "tmux new-session -d -s lead") {
		t.Errorf("dry run output:\n%s", out)
	}
	if _, err := os.Stat(sessionsPath); !os.IsNotExist(err) {
		t.Errorf("a dry run wrote the sessions file (err=%v)", err)
	}
}
