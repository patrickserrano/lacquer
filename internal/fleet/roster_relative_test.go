package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// A roster named relative to the cwd -- `--roster fleet.toml` from the
// fleet-ops directory, which is how every lead runs -- must still yield
// absolute project paths. Joined against filepath.Dir("fleet.toml") == ".",
// "../proj" stayed relative, and a bg dispatch then failed taking
// filepath.Rel of git's absolute toplevel against it (1.37.3 to 1.37.9).
func TestLoadRosterFromARelativePathGivesAbsoluteProjectPaths(t *testing.T) {
	root := t.TempDir()
	fleetOps := filepath.Join(root, "fleet-ops")
	write(t, filepath.Join(fleetOps, "fleet.toml"), "[[project]]\npath=\"../proj\"\n\n[[project]]\npath=\"here\"\n")

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

	r, err := LoadRoster("fleet.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"proj": filepath.Join(filepath.Dir(cwd), "proj"),
		"here": filepath.Join(cwd, "here"),
	}
	for _, e := range r.Project {
		if !filepath.IsAbs(e.Path) {
			t.Errorf("%s: path = %q, want an absolute path", e.Name, e.Path)
		}
		if e.Path != want[e.Name] {
			t.Errorf("%s: path = %q, want %q", e.Name, e.Path, want[e.Name])
		}
	}
}
