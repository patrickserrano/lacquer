package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/lock"
)

func TestDocumentationRetirementSafety(t *testing.T) {
	for _, tc := range []string{"clean", "still shipped", "past render", "nested", "edited", "untracked", "no ownership", "lock mismatch", "forged baseline", "symlink", "parent symlink", "lock symlink", "excluded", "seed once", "opt in", "unrelated orphan", "sync refused"} {
		t.Run(tc, func(t *testing.T) {
			lq, project := t.TempDir(), t.TempDir()
			component := "."
			if tc == "nested" {
				component = "ios"
			}
			writeFile(t, filepath.Join(lq, "VERSION"), "1\n")
			writeFile(t, filepath.Join(lq, "core/CLAUDE.core.md"), "CORE\n")
			writeFile(t, filepath.Join(lq, "core/root/scripts/docs-relaxation.sh"), "old reader\n")
			writeFile(t, filepath.Join(lq, "profiles/ios/CLAUDE.ios.md"), "IOS\n")
			writeFile(t, filepath.Join(lq, "profiles/ios/root/scripts/docs-hook.sh"), "old hook\n")
			writeFile(t, filepath.Join(lq, "profiles/ios/config/.swiftlint-docs.yml"), "old config\n")
			writeFile(t, filepath.Join(lq, "profiles/ios/root/.pre-commit-config.yaml"), "old hook wiring\n")
			manifest := "[project]\nname='demo'\nPROJECT_EXTRA\n[[component]]\npath='" + component + "'\nprofiles=['ios']\n[ios]\ndocumentation_hook=ENABLED\n"
			writeManifest := func(extra, enabled string) {
				writeFile(t, filepath.Join(project, ".lacquer.toml"), strings.NewReplacer("PROJECT_EXTRA", extra, "ENABLED", enabled).Replace(manifest))
			}
			commit := func() {
				gittest.Git(t, project, "add", "-A")
				gittest.Git(t, project, "-c", "user.name=T", "-c", "user.email=t@e", "commit", "--allow-empty", "-qm", "fixture")
			}
			if tc == "still shipped" {
				writeFile(t, filepath.Join(lq, "core/root/scripts/docs-hook.sh"), "old hook\n")
			}
			writeManifest("", "true")
			gittest.Init(t, project, "-q")
			commit()
			if _, err := Run(lq, project, false); err != nil {
				t.Fatal(err)
			}
			commit()
			extra, enabled := "", "false"
			if tc == "excluded" {
				extra = "exclude=[{path='scripts/docs-hook.sh',reason='Owned by project'}]"
			}
			if tc == "seed once" {
				extra = "seed_once=['scripts/docs-hook.sh']"
			}
			if tc == "opt in" {
				enabled = "true"
			}
			writeManifest(extra, enabled)
			commit()
			lk, _, err := lock.Read(project)
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(project, "scripts/docs-hook.sh")
			switch tc {
			case "past render":
				body, err := os.ReadFile("testdata/legacy-docs-hook.sh")
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, script, string(body))
				lk.Files["scripts/docs-hook.sh"] = lock.Hash(string(body))
			case "edited", "forged baseline":
				writeFile(t, script, "project work\n")
				if tc == "forged baseline" {
					lk.Files["scripts/docs-hook.sh"] = lock.Hash("project work\n")
				}
			case "untracked":
				gittest.Git(t, project, "rm", "--cached", "scripts/docs-hook.sh")
			case "no ownership":
				delete(lk.Files, "scripts/docs-hook.sh")
			case "lock mismatch":
				lk.Files["scripts/docs-hook.sh"] = lock.Hash("different recorded render\n")
			case "symlink":
				if err := os.Remove(script); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "outside")
				writeFile(t, target, "old hook\n")
				if err := os.Symlink(target, script); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				outside := filepath.Join(t.TempDir(), "scripts")
				if err := os.Rename(filepath.Join(project, "scripts"), outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(project, "scripts")); err != nil {
					t.Fatal(err)
				}
			case "lock symlink":
				outside := filepath.Join(t.TempDir(), "baseline")
				data, err := os.ReadFile(filepath.Join(project, lock.Name))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, outside, string(data))
				if err := os.Remove(filepath.Join(project, lock.Name)); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(project, lock.Name)); err != nil {
					t.Fatal(err)
				}
			case "unrelated orphan":
				writeFile(t, filepath.Join(project, "unrelated.sh"), "old workflow helper\n")
				lk.Files["unrelated.sh"] = lock.Hash("old workflow helper\n")
			case "sync refused":
				writeFile(t, filepath.Join(project, ".pre-commit-config.yaml"), "local hook wiring\n")
			}
			if tc != "lock symlink" {
				if err := lock.Write(project, lk); err != nil {
					t.Fatal(err)
				}
			}
			// Commit the forged baseline too: neither clean git nor a matching
			// hand-edited lock can authorize deleting project-owned contents.
			if tc == "forged baseline" || tc == "symlink" || tc == "unrelated orphan" || tc == "no ownership" || tc == "past render" {
				commit()
			}
			_, err = Run(lq, project, false)
			if tc == "sync refused" || tc == "parent symlink" || tc == "lock symlink" {
				if err == nil {
					t.Fatal("local hook edits did not refuse sync")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			removeScript := tc == "past render" || tc == "clean" || tc == "nested" || tc == "unrelated orphan"
			_, statErr := os.Lstat(script)
			if removeScript && !os.IsNotExist(statErr) || !removeScript && statErr != nil {
				t.Errorf("script removed=%t: %v", removeScript, statErr)
			}
			config := filepath.Join(project, component, ".swiftlint-docs.yml")
			_, statErr = os.Lstat(config)
			removeConfig := tc != "opt in" && tc != "sync refused" && tc != "parent symlink" && tc != "lock symlink"
			if removeConfig && !os.IsNotExist(statErr) || !removeConfig && statErr != nil {
				t.Errorf("config removed=%t: %v", removeConfig, statErr)
			}
			_, readerErr := os.Lstat(filepath.Join(project, "scripts/docs-relaxation.sh"))
			if removeConfig && !os.IsNotExist(readerErr) || !removeConfig && readerErr != nil {
				t.Errorf("reader removed=%t: %v", removeConfig, readerErr)
			}
			if tc == "unrelated orphan" {
				orphans, err := audit.Orphans(lq, project)
				if err != nil || len(orphans) != 1 || orphans[0].Dest != "unrelated.sh" || (audit.Gate{Orphans: len(orphans)}).ExitCode() != 4 {
					t.Fatalf("general orphan policy changed: %+v, %v", orphans, err)
				}
			}
		})
	}
}

func TestDocumentationRetirementRechecksCandidates(t *testing.T) {
	for _, change := range []string{"committed edit", "mode edit", "symlink"} {
		t.Run(change, func(t *testing.T) {
			project := t.TempDir()
			gittest.Init(t, project, "-q")
			dest := "scripts/docs-hook.sh"
			target := filepath.Join(project, dest)
			writeFile(t, target, "old render\n")
			gittest.Git(t, project, "add", "-A")
			gittest.Git(t, project, "commit", "-qm", "old render")
			files := []documentationRetirement{{dest: dest, hash: lock.Hash("old render\n")}}
			switch change {
			case "committed edit":
				writeFile(t, target, "new project work\n")
				gittest.Git(t, project, "add", "-A")
				gittest.Git(t, project, "commit", "-qm", "project work")
			case "mode edit":
				gittest.Git(t, project, "config", "core.fileMode", "true")
				if err := os.Chmod(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "outside")
				writeFile(t, outside, "old render\n")
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
				gittest.Git(t, project, "add", "-A")
				gittest.Git(t, project, "commit", "-qm", "project symlink")
			}
			removed, preserved, err := removeDocumentationRetirements(project, files)
			if err != nil || len(removed) != 0 || len(preserved) != 1 || preserved[0] != dest {
				t.Fatalf("changed candidate removed=%v preserved=%v err=%v", removed, preserved, err)
			}
			if _, err := os.Lstat(target); err != nil {
				t.Fatalf("changed candidate disappeared: %v", err)
			}
		})
	}
}
