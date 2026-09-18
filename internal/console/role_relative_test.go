package console

import (
	"os"
	"path/filepath"
	"testing"
)

// A roles file named relative to the cwd (`--roles roles.toml`) must still
// yield absolute role dirs -- including the default, the roles file's own
// directory, which was "." before. Mirrors fleet.LoadRoster.
func TestLoadRoleRosterFromARelativePathGivesAbsoluteDirs(t *testing.T) {
	root := t.TempDir()
	fleetOps := filepath.Join(root, "fleet-ops")
	writeRoleFile(t, filepath.Join(fleetOps, "roles.toml"),
		"[[role]]\nname=\"pm\"\ntask=\"a\"\ndir=\"../proj\"\n\n[[role]]\nname=\"lead\"\ntask=\"a\"\n")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(fleetOps); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	r, err := LoadRoleRoster("roles.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"pm":   filepath.Join(filepath.Dir(cwd), "proj"),
		"lead": cwd,
	}
	for _, role := range r.Role {
		if !filepath.IsAbs(role.Dir) {
			t.Errorf("%s: dir = %q, want an absolute path", role.Name, role.Dir)
		}
		if role.Dir != want[role.Name] {
			t.Errorf("%s: dir = %q, want %q", role.Name, role.Dir, want[role.Name])
		}
	}
}
