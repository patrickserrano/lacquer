package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/version"
)

// `audit --ci` (lacquer#522, U11). CI audits at the version .lacquer.lock names,
// so a unit whose content matches the lock's hash but not that version's render
// is a lock the named version never wrote. Without --ci that row is Behind and
// passes, which is right locally and wrong in CI. See internal/audit/ci.go.

// syncedFixture syncs a real fixture project against this checkout and returns
// its directory and the env to run the CLI with.
func syncedFixture(t *testing.T) (string, func(string) string) {
	t.Helper()
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})
	var out, errb bytes.Buffer
	if code := run([]string{"sync"}, env, &out, &errb); code != 0 {
		t.Fatalf("sync exited %d\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
	return dir, env
}

// plantEditWithMatchingLock edits a managed asset AND rewrites its lock hash to
// match, which is exactly what a lock written by a different build looks like:
// the project agrees with the lock, and neither agrees with the named release.
func plantEditWithMatchingLock(t *testing.T, dir string) string {
	t.Helper()
	lk, ok, err := lock.Read(dir)
	if err != nil || !ok {
		t.Fatalf("read lock: ok=%v err=%v", ok, err)
	}
	var assets []string
	for k := range lk.Files {
		if !strings.Contains(k, "#") {
			if _, err := os.Stat(filepath.Join(dir, k)); err == nil {
				assets = append(assets, k)
			}
		}
	}
	if len(assets) == 0 {
		t.Fatal("the synced fixture has no asset in its lock — this test would plant nothing")
	}
	sort.Strings(assets)
	dest := assets[0]
	p := filepath.Join(dir, dest)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(data) + "\n# planted by a build the lock does not name\n"
	if err := os.WriteFile(p, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	lk.Files[dest] = lock.Hash(edited)
	if err := lock.Write(dir, lk); err != nil {
		t.Fatal(err)
	}
	return dest
}

func runAudit(t *testing.T, env func(string) string, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(append([]string{"audit"}, args...), env, &out, &errb)
	return code, out.String() + errb.String()
}

func TestAuditCIFailsOnALockTheNamedVersionDidNotWrite(t *testing.T) {
	dir, env := syncedFixture(t)
	dest := plantEditWithMatchingLock(t, dir)

	code, out := runAudit(t, env, "--ci")
	if code != 8 {
		t.Fatalf("audit --ci exited %d, want 8 for a planted edit with a matching lock hash\n%s", code, out)
	}
	for _, want := range []string{"lock mismatch", "does not render them", "Re-sync with the released lacquer", dest} {
		if !strings.Contains(out, want) {
			t.Errorf("audit --ci output lacks %q:\n%s", want, out)
		}
	}
}

func TestAuditWithoutCIKeepsBehindAsANormalState(t *testing.T) {
	dir, env := syncedFixture(t)
	dest := plantEditWithMatchingLock(t, dir)

	code, out := runAudit(t, env)
	if code != 0 {
		t.Fatalf("audit exited %d, want 0: locally Behind means the lacquer advanced\n%s", code, out)
	}
	if !strings.Contains(out, "behind:\n  "+dest) {
		t.Errorf("the planted unit is not reported as behind:\n%s", out)
	}
	if strings.Contains(out, "lock mismatch") {
		t.Errorf("a local audit reported a lock mismatch:\n%s", out)
	}
}

func TestAuditCleanProjectPassesWithAndWithoutCI(t *testing.T) {
	_, env := syncedFixture(t)
	for _, args := range [][]string{nil, {"--ci"}} {
		if code, out := runAudit(t, env, args...); code != 0 {
			t.Errorf("audit %v exited %d on a freshly synced project\n%s", args, code, out)
		}
	}
}

// The premise --ci rests on. Run against a lacquer other than the one the lock
// names, Behind CAN mean the lacquer advanced, so --ci must refuse rather than
// pass or misreport.
func TestAuditCIRefusesWhenTheLacquerIsNotTheLockVersion(t *testing.T) {
	dir, env := syncedFixture(t)
	lk, _, err := lock.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	lk.Version = version.Version{Major: lk.Version.Major, Minor: lk.Version.Minor + 1}
	if err := lock.Write(dir, lk); err != nil {
		t.Fatal(err)
	}
	code, out := runAudit(t, env, "--ci")
	if code == 0 || code == 8 {
		t.Fatalf("audit --ci exited %d against a lacquer that is not the lock's version, want a refusal\n%s", code, out)
	}
	if !strings.Contains(out, "lock's own version") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}

func TestAuditCIRefusesWithoutALock(t *testing.T) {
	dir, env := syncedFixture(t)
	if err := os.Remove(filepath.Join(dir, lock.Name)); err != nil {
		t.Fatal(err)
	}
	code, out := runAudit(t, env, "--ci")
	if code == 0 {
		t.Fatalf("audit --ci passed with no lock, where nothing can be attributed\n%s", out)
	}
	if !strings.Contains(out, "--ci needs a "+lock.Name) {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
}
