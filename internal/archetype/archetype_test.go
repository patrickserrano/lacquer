package archetype

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lacquerWithStacks(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, Dir, "ios-supabase.toml"), `description = "iOS app with a Supabase backend."

[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "server"
profiles = ["supabase"]
`)
	write(t, filepath.Join(root, Dir, "web.toml"), `description = "A web app."

[[component]]
path = "."
profiles = ["web"]
`)
	// Not an archetype: must be ignored by Names, not parsed as one.
	write(t, filepath.Join(root, Dir, "README.md"), "# Archetypes\n")
	return root
}

func TestLoadAndNames(t *testing.T) {
	root := lacquerWithStacks(t)
	names, err := Names(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "ios-supabase" || names[1] != "web" {
		t.Fatalf("names = %v", names)
	}
	a, err := Load(root, "ios-supabase")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "ios-supabase" || len(a.Components) != 2 {
		t.Errorf("archetype = %+v", a)
	}
}

// The name is joined into a filesystem path, so traversal must be rejected
// outright rather than cleaned.
func TestLoadRejectsUnsafeNames(t *testing.T) {
	root := lacquerWithStacks(t)
	for _, bad := range []string{"../secrets", "ios/../../etc", "Ios", "", "a b"} {
		if _, err := Load(root, bad); err == nil {
			t.Errorf("Load(%q) succeeded; want rejection", bad)
		}
	}
}

func TestLoadUnknownNameListsKnownOnes(t *testing.T) {
	root := lacquerWithStacks(t)
	_, err := Load(root, "nope")
	if err == nil || !strings.Contains(err.Error(), "ios-supabase") {
		t.Errorf("error = %v; want it to list the known stacks", err)
	}
}

func TestNamesMissingDirIsNotAnError(t *testing.T) {
	names, err := Names(t.TempDir())
	if err != nil || len(names) != 0 {
		t.Errorf("names = %v, err = %v; want empty and no error", names, err)
	}
}

// The whole point: components that do not exist yet get declared anyway, so the
// stack the PCD decided on is gated from the first commit.
func TestMergeAddsComponentsThatDoNotExistYet(t *testing.T) {
	root := lacquerWithStacks(t)
	a, err := Load(root, "ios-supabase")
	if err != nil {
		t.Fatal(err)
	}
	got := Merge([]config.Component{{Path: "ios", Profiles: []string{"ios"}}}, a)
	if len(got) != 2 {
		t.Fatalf("merged = %+v, want ios + server", got)
	}
	if got[0].Path != "ios" || got[1].Path != "server" || got[1].Profiles[0] != "supabase" {
		t.Errorf("merged = %+v", got)
	}
}

// A manifest may declare a profile only once. An archetype that would place
// `ios` at ios/ when detection already found it at "." must drop its own entry,
// not emit a second one — that manifest would fail to load.
func TestMergeDropsAProfileDetectionAlreadyPlaced(t *testing.T) {
	root := lacquerWithStacks(t)
	a, err := Load(root, "ios-supabase")
	if err != nil {
		t.Fatal(err)
	}
	got := Merge([]config.Component{{Path: ".", Profiles: []string{"ios"}}}, a)

	seen := map[string]string{}
	for _, c := range got {
		for _, p := range c.Profiles {
			if prev, dup := seen[p]; dup {
				t.Fatalf("profile %q declared twice (%s and %s): %+v", p, prev, c.Path, got)
			}
			seen[p] = c.Path
		}
	}
	if seen["ios"] != "." {
		t.Errorf("detection's path must win for ios, got %q", seen["ios"])
	}
	if seen["supabase"] != "server" {
		t.Errorf("supabase should still be added at server, got %q", seen["supabase"])
	}
}

func TestMergeUnionsProfilesOnASharedPath(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, Dir, "rooty.toml"), `description = "x"

[[component]]
path = "."
profiles = ["supabase"]
`)
	a, err := Load(root, "rooty")
	if err != nil {
		t.Fatal(err)
	}
	got := Merge([]config.Component{{Path: ".", Profiles: []string{"ios"}}}, a)
	if len(got) != 1 || len(got[0].Profiles) != 2 {
		t.Fatalf("merged = %+v, want one component with both profiles", got)
	}
	if got[0].Profiles[0] != "ios" || got[0].Profiles[1] != "supabase" {
		t.Errorf("profiles = %v, want sorted [ios supabase]", got[0].Profiles)
	}
}

func TestLoadRejectsAnArchetypeWithNoComponents(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, Dir, "empty.toml"), `description = "nothing"`+"\n")
	if _, err := Load(root, "empty"); err == nil {
		t.Error("an archetype with no components should be rejected")
	}
}

// Every archetype this lacquer actually ships must load, name only profiles that
// ship, and produce a manifest config.Load accepts. A broken archetype is not
// discoverable any other way — nothing else reads these files at build time.
func TestShippedArchetypesAreValid(t *testing.T) {
	root := "../.."
	names, err := Names(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("this lacquer ships no archetypes; expected several")
	}
	for _, n := range names {
		a, err := Load(root, n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if strings.TrimSpace(a.Description) == "" {
			t.Errorf("%s: no description (it is what --list-stacks prints)", n)
		}
		seen := map[string]string{}
		for _, c := range a.Components {
			if len(c.Profiles) == 0 {
				t.Errorf("%s: component %q declares no profiles", n, c.Path)
			}
			for _, p := range c.Profiles {
				body := filepath.Join(root, "profiles", p, "CLAUDE."+p+".md")
				if _, err := os.Stat(body); err != nil {
					t.Errorf("%s: profile %q does not ship (%s missing)", n, p, body)
				}
				if prev, dup := seen[p]; dup {
					t.Errorf("%s: profile %q declared by both %q and %q; config.Load rejects that", n, p, prev, c.Path)
				}
				seen[p] = c.Path
			}
		}
	}
}
