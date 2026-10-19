package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/version"
)

// The downgrade floor (#576): on pull_request, `audit --ci` fails when the PR's
// .lacquer.lock names an older lacquer than its base's, unless the lock records
// why. It is the extra layer under the post-merge harvest, and only holds for a
// downgrade between two versions that both carry it: CI runs the binary the
// head's lock names. Exercised through the real CLI in a depth-1 checkout of a
// PR merge commit, as CI checks one out.

// floorPR syncs a real project, commits it as a PR's base with its lock naming
// baseVersion ("" for no lock at the base), and has the PR restore the lock as
// this checkout wrote it, plus downgrade when given. It returns the env to run
// the CLI with, from inside the PR's checkout.
func floorPR(t *testing.T, baseVersion string, downgrade *lock.Downgrade) func(string) string {
	t.Helper()
	dir, env := syncedFixture(t)
	files := map[string]string{}
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if len(data) == 0 {
			// gittest.Write deletes a file given "": the fixture's empty
			// supabase config only has to exist.
			data = []byte("\n")
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	head, ok, err := lock.Read(dir)
	if err != nil || !ok {
		t.Fatalf("read the synced lock: ok=%v err=%v", ok, err)
	}
	head.Downgrade = downgrade
	headDir := t.TempDir()
	if err := lock.Write(headDir, head); err != nil {
		t.Fatal(err)
	}
	headLock, err := os.ReadFile(filepath.Join(headDir, lock.Name))
	if err != nil {
		t.Fatal(err)
	}

	if baseVersion == "" {
		delete(files, lock.Name)
	} else {
		base := *head
		base.Downgrade = nil
		v, err := version.Parse(baseVersion)
		if err != nil {
			t.Fatal(err)
		}
		base.Version = v
		baseDir := t.TempDir()
		if err := lock.Write(baseDir, &base); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(baseDir, lock.Name))
		if err != nil {
			t.Fatal(err)
		}
		files[lock.Name] = string(data)
	}
	pr := gittest.NewPR(t, files, func(work string) {
		gittest.Write(t, work, map[string]string{lock.Name: string(headLock)})
	})
	chdir(t, pr.Checkout)
	return env
}

// newer is a version past the one this checkout is, for a base to downgrade from.
func newer(t *testing.T) string {
	t.Helper()
	v, err := version.Read(realLacquer(t))
	if err != nil {
		t.Fatal(err)
	}
	v.Minor++
	return v.String()
}

func TestAuditFloorFailsAPRThatDowngradesTheLock(t *testing.T) {
	from := newer(t)
	env := floorPR(t, from, nil)
	code, out := runAudit(t, env, "--ci", "--ci-event", "pull_request")
	if code != 9 || !strings.Contains(out, "downgrade floor: .lacquer.lock names v") ||
		!strings.Contains(out, "older than v"+from+" on the PR's base") ||
		!strings.Contains(out, `"downgrade": {"from": "`+from+`", "reason": "`) {
		t.Fatalf("exit %d; want 9, naming both versions and how to record a reason:\n%s", code, out)
	}
}

func TestAuditFloorPassesADowngradeWithItsReasonAndReportsIt(t *testing.T) {
	from := newer(t)
	env := floorPR(t, from, &lock.Downgrade{From: from, Reason: "the newer release breaks the release job"})
	code, out := runAudit(t, env, "--ci", "--ci-event", "pull_request")
	if code != 0 || !strings.Contains(out, "reason: the newer release breaks the release job") {
		t.Fatalf("exit %d; a downgrade with its reason passes and prints it:\n%s", code, out)
	}
}

// A reason written for leaving another version does not cover this one.
func TestAuditFloorFailsADowngradeWhoseReasonNamesAnotherVersion(t *testing.T) {
	env := floorPR(t, newer(t), &lock.Downgrade{From: "0.1.0", Reason: "stale"})
	if code, out := runAudit(t, env, "--ci", "--ci-event", "pull_request"); code != 9 {
		t.Fatalf("exit %d; want 9:\n%s", code, out)
	}
}

func TestAuditFloorPassesAnUpgradeAndAFirstSync(t *testing.T) {
	for _, tc := range []struct{ name, base string }{{"upgrade", "1.0.0"}, {"first sync", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			env := floorPR(t, tc.base, nil)
			code, out := runAudit(t, env, "--ci", "--ci-event", "pull_request")
			if code != 0 || strings.Contains(out, "older than") {
				t.Fatalf("exit %d; want 0:\n%s", code, out)
			}
		})
	}
}

// Off the pull_request event there is no base to compare with, and the floor
// stays out of it.
func TestAuditFloorOnlyRunsOnPullRequest(t *testing.T) {
	env := floorPR(t, newer(t), nil)
	for _, args := range [][]string{{"--ci"}, {"--ci", "--ci-event", "push"}} {
		if code, out := runAudit(t, env, args...); code != 0 {
			t.Fatalf("%v: exit %d; want 0:\n%s", args, code, out)
		}
	}
}

// A base that cannot be read fails: it never reads as "no lock at the base".
func TestAuditFloorFailsWhenTheBaseCannotBeRead(t *testing.T) {
	env := floorPR(t, newer(t), nil)
	if err := os.RemoveAll(filepath.Join("..", "origin.git")); err != nil {
		t.Fatal(err)
	}
	code, out := runAudit(t, env, "--ci", "--ci-event", "pull_request")
	if code == 0 || !strings.Contains(out, "could not fetch the base commit") {
		t.Fatalf("exit %d; an unreadable base must fail, not pass as unlocked:\n%s", code, out)
	}
}
