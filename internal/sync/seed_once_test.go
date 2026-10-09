package sync

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/lock"
)

// seedOnceFixture is a lacquer shipping one component-level config file, and a
// git project whose manifest may declare that file seed-once. The file is the
// real case: the ios profile ships a generic Secrets.xcconfig.example, and a
// project's own copy names the app's own keys, so its content is inherently
// per-project.
func seedOnceFixture(t *testing.T, seedOnce bool) (lacquer, project, dest string) {
	t.Helper()
	lacquer, project = t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(lacquer, "profiles", "ios", "config", "Secrets.xcconfig.example"), "GENERIC_KEY = your-key\n")
	gittest.Init(t, project, "-q")
	manifest := "[project]\nname=\"x\"\n"
	if seedOnce {
		manifest += "seed_once = [\"ios/Secrets.xcconfig.example\"]\n"
	}
	manifest += "\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n"
	writeFile(t, filepath.Join(project, ".lacquer.toml"), manifest)
	return lacquer, project, "ios/Secrets.xcconfig.example"
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func rowFor(t *testing.T, lacquer, project, dest string) (audit.Row, bool) {
	t.Helper()
	rows, _, err := audit.Classify(lacquer, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Dest == dest {
			return r, true
		}
	}
	return audit.Row{}, false
}

// Absent: a seed-once file is written exactly as any other asset would be.
// Without this a project adopting the profile would get no template at all,
// and seed-once would be an exclusion by another name.
func TestSeedOnceSeedsAnAbsentFile(t *testing.T) {
	lacquer, project, dest := seedOnceFixture(t, true)
	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, dest))
	if err != nil {
		t.Fatalf("a seed-once file that was absent was not seeded: %v", err)
	}
	if string(got) != "GENERIC_KEY = your-key\n" {
		t.Errorf("seeded %q, want the lacquer's content", got)
	}
}

// Present and edited: sync leaves it alone — with and without --force — even
// when the lacquer's copy has moved on, and audit does not call it drift.
//
// The control half runs the same steps WITHOUT seed_once and requires sync to
// refuse the edit. Without it, a fixture in which the edit was never visible to
// sync (wrong path, file not shipped) would pass the seed-once half for free.
func TestSeedOnceNeverOverwritesAProjectEdit(t *testing.T) {
	const edit = "APP_OWN_KEY = your-own-key\n"
	for _, tc := range []struct {
		name     string
		seedOnce bool
	}{{"seed_once", true}, {"control", false}} {
		t.Run(tc.name, func(t *testing.T) {
			lacquer, project, dest := seedOnceFixture(t, tc.seedOnce)
			if _, err := Run(lacquer, project, false); err != nil {
				t.Fatalf("first sync: %v", err)
			}
			gitIn(t, project, "add", "-A")
			gitIn(t, project, "commit", "-qm", "sync")
			writeFile(t, filepath.Join(project, dest), edit)
			gitIn(t, project, "commit", "-qam", "name the app's own keys")
			// The lacquer moves on too, so a managed file would be in conflict.
			writeFile(t, filepath.Join(lacquer, "profiles", "ios", "config", "Secrets.xcconfig.example"), "GENERIC_KEY = your-key\nNEW_KEY = your-new\n")

			_, err := Run(lacquer, project, false)
			if !tc.seedOnce {
				if err == nil {
					t.Fatal("control: sync accepted a locally edited managed file, so this fixture cannot show seed-once doing anything")
				}
				return
			}
			if err != nil {
				t.Fatalf("sync refused a project-owned seed-once file: %v", err)
			}
			if row, ok := rowFor(t, lacquer, project, dest); ok {
				t.Errorf("audit reports the seed-once file as a managed unit (%s), so a project edit reads as drift", row.Status)
			}
			if _, err := Run(lacquer, project, true); err != nil {
				t.Fatalf("forced sync: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(project, dest))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != edit {
				t.Errorf("sync overwrote a project-owned seed-once file:\n%s", got)
			}
			lk, ok, err := lock.Read(project)
			if err != nil || !ok {
				t.Fatalf("no lock after sync: %v", err)
			}
			if _, tracked := lk.Files[dest]; tracked {
				t.Errorf("%s is recorded in %s, so a later audit would compare it against the lacquer", dest, lock.Name)
			}
		})
	}
}

// A project that already had its own copy before it ever synced is not a
// collision: the file is the project's, which is the whole declaration.
func TestSeedOnceAdoptsAPreexistingFileWithoutAsking(t *testing.T) {
	lacquer, project, dest := seedOnceFixture(t, true)
	writeFile(t, filepath.Join(project, dest), "MINE = 1\n")
	gitIn(t, project, "add", "-A")
	gitIn(t, project, "commit", "-qm", "own example")
	// An earlier sync's lock, so a differing unpaid-for file would be a Collision.
	if err := lock.Write(project, &lock.Lock{Files: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatalf("sync refused a pre-existing seed-once file: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, dest))
	if string(got) != "MINE = 1\n" {
		t.Errorf("sync replaced a pre-existing seed-once file:\n%s", got)
	}
}

// After seeding, the file is gone from the audit entirely; while it is absent
// audit says sync would add it, which is the truth.
func TestSeedOnceAuditSaysAddOnlyWhileAbsent(t *testing.T) {
	lacquer, project, dest := seedOnceFixture(t, true)
	row, ok := rowFor(t, lacquer, project, dest)
	if !ok || row.Status != audit.Add {
		t.Fatalf("before seeding, audit row = %+v (present %v), want %q", row, ok, audit.Add)
	}
	if _, err := Run(lacquer, project, false); err != nil {
		t.Fatal(err)
	}
	if row, ok := rowFor(t, lacquer, project, dest); ok {
		t.Errorf("after seeding the file is still an audited unit (%s)", row.Status)
	}
}
