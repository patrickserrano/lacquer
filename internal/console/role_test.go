package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRoleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRoleRosterRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "# nothing here\n")
	if _, err := LoadRoleRoster(rp); err == nil {
		t.Error("an empty roles file dispatches nothing and reports success — reject it")
	}
}

func TestLoadRoleRosterRequiresTask(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\n")
	if _, err := LoadRoleRoster(rp); err == nil {
		t.Error("a role has no other source for its starting prompt — a blank task must be rejected, not silently dispatchable with an empty prompt")
	}
}

func TestLoadRoleRosterRejectsDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\ntask=\"a\"\n\n[[role]]\nname=\"lead\"\ntask=\"b\"\n")
	if _, err := LoadRoleRoster(rp); err == nil {
		t.Error("two roles under one name would collide on the same tmux session")
	}
}

func TestLoadRoleRosterRejectsUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\ntask=\"a\"\nbogus=\"x\"\n")
	if _, err := LoadRoleRoster(rp); err == nil {
		t.Error("a misspelled key should not be silently dropped")
	}
}

func TestLoadRoleRosterRejectsUnknownMode(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\ntask=\"a\"\nmode=\"ssh\"\n")
	if _, err := LoadRoleRoster(rp); err == nil {
		t.Error("an unknown mode should be rejected up front, not discovered at dispatch time")
	}
}

func TestLoadRoleRosterDefaultsModeToTmux(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\ntask=\"a\"\n")
	r, err := LoadRoleRoster(rp)
	if err != nil {
		t.Fatal(err)
	}
	if r.Role[0].Mode != Tmux {
		t.Errorf("mode = %q, want %q — a role is long-lived and supervisory, not a one-shot task", r.Role[0].Mode, Tmux)
	}
}

// Empty means the roles file's own directory — the natural default, since
// that's where the project roster and `lacquer console`/`fleet` commands
// expect to be run from.
func TestLoadRoleRosterDirDefaultsToRolesFileDir(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\ntask=\"a\"\n")
	r, err := LoadRoleRoster(rp)
	if err != nil {
		t.Fatal(err)
	}
	if r.Role[0].Dir != dir {
		t.Errorf("dir = %q, want %q", r.Role[0].Dir, dir)
	}
}

func TestLoadRoleRosterResolvesRelativeDirAgainstItself(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "roles.toml")
	writeRoleFile(t, rp, "[[role]]\nname=\"lead\"\ntask=\"a\"\ndir=\"sub\"\n")
	r, err := LoadRoleRoster(rp)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "sub"); r.Role[0].Dir != want {
		t.Errorf("dir = %q, want %q — a roles file must be usable beside a dedicated working directory", r.Role[0].Dir, want)
	}
}

func roleRosterOf(roles ...Role) RoleRoster {
	return RoleRoster{Role: roles}
}

func TestDispatchRoleRejectsUnknownRole(t *testing.T) {
	_, err := DispatchRole(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "t", Dir: "/w"}), nil, "pm", "", true)
	if err == nil {
		t.Fatal("expected rejection of a role not in the roles file")
	}
	if !strings.Contains(err.Error(), "lead") {
		t.Errorf("the error should list the known names, got: %v", err)
	}
}

// A relaunch needs to hand the role a different task ("you died, here's
// where you left off") without editing the roles file — the override must
// win over the declared one.
func TestDispatchRoleTaskOverrideWinsOverDeclared(t *testing.T) {
	out, err := DispatchRole(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "original brief", Dir: "/w"}), nil, "lead", "resume after crash", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "original brief") || !strings.Contains(out, "resume after crash") {
		t.Errorf("an explicit task must override the role's declared one:\n%s", out)
	}
}

// No override means the role's own declared task runs — the whole point of
// a roles file is not retyping a substantial prompt by hand each restart.
func TestDispatchRoleUsesDeclaredTaskWhenNoOverride(t *testing.T) {
	out, err := DispatchRole(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "original brief", Dir: "/w"}), nil, "lead", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "original brief") {
		t.Errorf("expected the role's declared task in the dispatch line:\n%s", out)
	}
}

func TestDispatchRoleWarnsAboutExistingSessionButProceeds(t *testing.T) {
	sessions := []Session{{Name: "lead", Status: "busy"}}
	out, err := DispatchRole(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "t", Dir: "/w"}), sessions, "lead", "", true)
	if err != nil {
		t.Fatalf("an existing session must not block dispatch: %v", err)
	}
	if !strings.Contains(out, "already has a session") {
		t.Errorf("the warning is missing:\n%s", out)
	}
	if !strings.Contains(out, "dispatch role:") {
		t.Errorf("it must still dispatch:\n%s", out)
	}
}

func TestDispatchRoleTmuxTargetsTheRolesDirNotAProjectPath(t *testing.T) {
	out, err := DispatchRole(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "t", Dir: "/fleet-ops"}), nil, "lead", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tmux new-session -A -s lead -c /fleet-ops") {
		t.Errorf("a role's tmux session must run from its declared dir, not any single project's worktree:\n%s", out)
	}
}
