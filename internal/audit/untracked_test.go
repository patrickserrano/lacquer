package audit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/lock"
	syncpkg "github.com/patrickserrano/lacquer/internal/sync"
)

func TestSyncNewlyShippedProjectFile(t *testing.T) {
	for _, tc := range []struct {
		name                                              string
		first, force, identical, absent, emptyLock, dirty bool
	}{
		{name: "refuse"},
		{name: "empty lock", emptyLock: true},
		{name: "force", force: true},
		{name: "first sync", first: true},
		{name: "identical", identical: true},
		{name: "absent", absent: true},
		{name: "force still guards dirty files", force: true, dirty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lacquer, project := setup(t)
			const dest = ".claude/commands/release.md"
			const shipped = "LACQUER RELEASE\n"
			content := "PROJECT RELEASE\n"
			if tc.identical {
				content = shipped
			}
			if !tc.absent {
				writeFile(t, filepath.Join(project, dest), content)
			}
			if tc.first {
				if err := os.Remove(filepath.Join(project, lock.Name)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.emptyLock {
				if err := lock.Write(project, &lock.Lock{Files: map[string]string{}}); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.absent {
				git(t, project, "add", "-A")
				git(t, project, "commit", "-qm", "project release command")
			}
			if tc.dirty {
				content = "UNCOMMITTED RELEASE\n"
				writeFile(t, filepath.Join(project, dest), content)
			}
			writeFile(t, filepath.Join(lacquer, "core/commands/release.md"), shipped)
			// Queue a legitimate region update too: a collision must abort before
			// this unrelated write, rather than leave a half-synced project.
			if !tc.first && !tc.emptyLock {
				writeFile(t, filepath.Join(lacquer, "core/CLAUDE.core.md"), "UPDATED CORE RULES")
			}
			rows, ver, err := audit.Classify(lacquer, project)
			if err != nil {
				t.Fatal(err)
			}
			collision := !tc.first && !tc.identical && !tc.absent
			if collision {
				if clob := audit.Clobbered(rows); len(clob) != 1 || clob[0] != dest {
					t.Errorf("audit must block the project-owned file, got %v", clob)
				}
				report := audit.Format(rows, ver)
				if !strings.Contains(report, "project already has") || !strings.Contains(report, "exclude") {
					t.Errorf("audit must explain ownership and remedies:\n%s", report)
				}
			}
			before := map[string]string{}
			for _, path := range []string{dest, "CLAUDE.md", ".gitignore", ".gitattributes", lock.Name} {
				if data, err := os.ReadFile(filepath.Join(project, path)); err == nil {
					before[path] = string(data)
				}
			}
			res, err := syncpkg.Run(lacquer, project, tc.force)
			if (collision && !tc.force) || tc.dirty {
				if err == nil {
					t.Fatal("sync must refuse before writing")
				}
				if !strings.Contains(err.Error(), dest) {
					t.Errorf("refusal omits destination: %v", err)
				}
				for path, want := range before {
					got, readErr := os.ReadFile(filepath.Join(project, path))
					if readErr != nil || string(got) != want {
						t.Errorf("refusal changed %s: %q (%v)", path, got, readErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.first {
				if len(res.Replaced) != 1 || res.Replaced[0] != dest {
					t.Errorf("first-sync notice = %v", res.Replaced)
				}
			} else if len(res.Replaced) != 0 {
				t.Errorf("unexpected first-sync notice = %v", res.Replaced)
			}
			got, err := os.ReadFile(filepath.Join(project, dest))
			if err != nil || string(got) != shipped {
				t.Fatalf("synced file = %q, %v", got, err)
			}
			lk, _, err := lock.Read(project)
			if err != nil {
				t.Fatal(err)
			}
			if lk.Files[dest] != lock.Hash(shipped) {
				t.Error("successful sync must record new baseline")
			}
		})
	}
}
