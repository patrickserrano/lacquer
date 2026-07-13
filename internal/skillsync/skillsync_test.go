package skillsync

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// fakeAdd simulates the `skills` CLI's project-scoped `add` behavior closely
// enough to test Install's orchestration: each successful call appends the
// requested skill to a skills-lock.json in dir, matching the real tool's
// accumulate-don't-clobber lockfile behavior observed against the live CLI.
func fakeAdd(t *testing.T, fail map[string]bool) func(dir string, args ...string) ([]byte, error) {
	t.Helper()
	return func(dir string, args ...string) ([]byte, error) {
		// args: "add", source, "-s", name, "-p", "-y"
		if len(args) < 4 || args[0] != "add" || args[2] != "-s" {
			t.Fatalf("unexpected args: %v", args)
		}
		source, name := args[1], args[3]
		if fail[name] {
			return []byte("boom"), fmt.Errorf("exit status 1")
		}
		lockPath := filepath.Join(dir, "skills-lock.json")
		existing := `{"version":1,"skills":{}}`
		if data, err := os.ReadFile(lockPath); err == nil {
			existing = string(data)
		}
		// Minimal hand-rolled JSON splice: good enough for a test fixture.
		injected := fmt.Sprintf(`"%s":{"source":%q}`, name, source)
		updated := existing
		if existing == `{"version":1,"skills":{}}` {
			updated = fmt.Sprintf(`{"version":1,"skills":{%s}}`, injected)
		} else {
			// insert before the closing "}}" of the skills object
			idx := len(existing) - 2
			updated = existing[:idx] + "," + injected + existing[idx:]
		}
		if err := os.WriteFile(lockPath, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}
}

func TestInstallCallsAddForEachEntry(t *testing.T) {
	dir := t.TempDir()
	orig := Runner
	var calls [][]string
	Runner = func(d string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return fakeAdd(t, nil)(d, args...)
	}
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{
		{Source: "dpearson2699/swift-ios-skills", Name: "healthkit"},
		{Source: "patrickserrano/lacquer", Name: "security-review"},
	}
	res, err := Install(dir, entries)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if len(res.Installed) != 2 {
		t.Errorf("Installed = %v", res.Installed)
	}
	if len(res.Failed) != 0 {
		t.Errorf("Failed = %v", res.Failed)
	}
	if len(res.Undeclared) != 0 {
		t.Errorf("Undeclared = %v, want none (both installed entries were declared)", res.Undeclared)
	}
}

func TestInstallContinuesAfterOneFailure(t *testing.T) {
	dir := t.TempDir()
	orig := Runner
	Runner = fakeAdd(t, map[string]bool{"bad-skill": true})
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{
		{Source: "owner/repo", Name: "bad-skill"},
		{Source: "owner/repo", Name: "good-skill"},
	}
	res, err := Install(dir, entries)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 1 || res.Installed[0] != "good-skill" {
		t.Errorf("Installed = %v", res.Installed)
	}
	if _, ok := res.Failed["bad-skill"]; !ok {
		t.Errorf("Failed missing bad-skill: %v", res.Failed)
	}
}

func TestInstallFlagsUndeclaredSkills(t *testing.T) {
	dir := t.TempDir()
	// Pre-seed a lockfile with a skill the manifest no longer declares.
	if err := os.WriteFile(filepath.Join(dir, "skills-lock.json"),
		[]byte(`{"version":1,"skills":{"orphaned-skill":{"source":"owner/repo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	entries := []config.SkillEntry{{Source: "owner/repo", Name: "wanted-skill"}}
	res, err := Install(dir, entries)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Undeclared) != 1 || res.Undeclared[0] != "orphaned-skill" {
		t.Errorf("Undeclared = %v, want [orphaned-skill]", res.Undeclared)
	}
}

func TestInstallWithNoEntriesStillReportsUndeclared(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "skills-lock.json"),
		[]byte(`{"version":1,"skills":{"leftover":{"source":"owner/repo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := Runner
	Runner = fakeAdd(t, nil)
	defer func() { Runner = orig }()

	res, err := Install(dir, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 0 {
		t.Errorf("Installed = %v, want none", res.Installed)
	}
	if len(res.Undeclared) != 1 || res.Undeclared[0] != "leftover" {
		t.Errorf("Undeclared = %v", res.Undeclared)
	}
}

func TestInstallWithNoLockfileYet(t *testing.T) {
	dir := t.TempDir()
	// No skills-lock.json exists and Install is called with zero entries —
	// must not error just because nothing has ever been installed here.
	res, err := Install(dir, nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != 0 || len(res.Undeclared) != 0 {
		t.Errorf("res = %+v, want empty", res)
	}
}
