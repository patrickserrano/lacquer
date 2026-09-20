package plugindefs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// ShippedRoots must find every directory lacquer ships definitions from by
// walking core/ and profiles/, not from a list. The expectation below is built a
// different way (a glob per component name, one level and two levels down), so a
// new profile that the walker missed shows up here rather than as a silently
// unchecked directory.
func TestShippedRootsCoversEverythingUnderCoreAndProfiles(t *testing.T) {
	repo := repoRoot(t)
	got, err := ShippedRoots(repo)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, pat := range []string{"core", "profiles/*"} {
		dirs, _ := filepath.Glob(filepath.Join(repo, pat))
		for _, d := range dirs {
			for _, comp := range []string{"agents", "skills", "commands"} {
				if fi, err := os.Stat(filepath.Join(d, comp)); err == nil && fi.IsDir() {
					want = append(want, d)
					break
				}
			}
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ShippedRoots = %v\nwant %v", got, want)
	}
	if len(got) < 4 {
		// core + ios + web + supabase today. Fewer means the walk found nothing,
		// which would make every check below vacuous.
		t.Fatalf("only %d shipped roots found: %v", len(got), got)
	}
}

// What lacquer ships must pass its own check. A green result here is only
// meaningful if something was checked, hence the floor.
func TestShippedDefinitionsAreLoadable(t *testing.T) {
	repo := repoRoot(t)
	roots, err := ShippedRoots(repo)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, root := range roots {
		files := Files(root)
		checked += len(files)
		for _, f := range Check(files) {
			t.Errorf("%s:%d %s", f.Path, f.Line, f.Problem)
		}
	}
	if checked < 50 {
		t.Fatalf("only %d definitions checked across %d roots; the walk is not seeing what lacquer ships", checked, len(roots))
	}
}
