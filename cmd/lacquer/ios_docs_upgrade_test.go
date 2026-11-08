package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/sync"
)

func TestAuditCIAfterDocumentationHookRetirement(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "linked"}[linked], func(t *testing.T) {
			lq, dir := realLacquer(t), t.TempDir()
			if err := os.CopyFS(dir, os.DirFS(filepath.Join(lq, "internal/shipped/testdata/projects/rootapp"))); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(dir, ".lacquer.toml")
			oldManifest, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			gittest.Init(t, dir, "-q")
			gittest.Git(t, dir, "add", "-A")
			gittest.Git(t, dir, "commit", "-qm", "initial")
			// The opt-in is pinned to the old release's exact bytes. Seed that
			// render, then restore its original manifest before the upgrade.
			if err := os.WriteFile(manifest, append(oldManifest, []byte("\n[ios]\ndocumentation_hook = true\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := sync.Run(lq, dir, false); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifest, oldManifest, 0600); err != nil {
				t.Fatal(err)
			}
			gittest.Git(t, dir, "add", "-A")
			gittest.Git(t, dir, "commit", "-qm", "old documentation hook render")
			if linked {
				work := filepath.Join(t.TempDir(), "linked")
				gittest.Git(t, dir, "worktree", "add", "-q", "-b", "topic", work)
				dir = work
			}
			chdir(t, dir)
			env := envMap(map[string]string{"LACQUER_ROOT": lq})
			var out, errb bytes.Buffer
			if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
				t.Fatalf("upgrade sync exited %d\n%s\n%s", code, &out, &errb)
			}
			// No hand edits or deletions between the new sync and the CI audit.
			if code, output := runAudit(t, env, "--ci"); code != 0 {
				t.Fatalf("audit --ci after upgrade exited %d\n%s", code, output)
			}
			lk, _, err := lock.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, rel := range []string{"scripts/docs-hook.sh", ".swiftlint-docs.yml", "scripts/docs-relaxation.sh"} {
				if _, err := os.Lstat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
					t.Errorf("retired file remains: %s (%v)", rel, err)
				}
				if _, present := lk.Files[rel]; present {
					t.Errorf("retired lock entry remains: %s", rel)
				}
			}
		})
	}
}
