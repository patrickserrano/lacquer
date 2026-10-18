package skillsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
)

// hangingNpx puts an `npx` on PATH that never exits, as one stuck on a registry
// or a prompt does, and shortens the limit so the test is quick.
func hangingNpx(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	orig := installLimit
	installLimit = 500 * time.Millisecond
	t.Cleanup(func() { installLimit = orig })
}

func within(t *testing.T, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("still running after %s: the call is not bounded", d)
	}
}

func TestRealRunnerReturnsANamedTimeoutInsteadOfHanging(t *testing.T) {
	hangingNpx(t)
	var err error
	within(t, 10*time.Second, func() { _, err = Runner(t.TempDir(), "add", "o/r", "-s", "x", "-p", "-y") })
	if err == nil {
		t.Fatal("a hung npx returned nil")
	}
	for _, want := range []string{"npx --yes skills@latest add", "500ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// A timed-out install must be a Failed entry, never an Installed one.
func TestInstallReportsATimedOutCallAsFailed(t *testing.T) {
	hangingNpx(t)
	dir := t.TempDir()
	var res Result
	within(t, 15*time.Second, func() {
		res, _ = Install(dir, []config.SkillEntry{{Name: "x", Source: "o/r"}}, nil)
	})
	if len(res.Installed) != 0 {
		t.Errorf("a timed-out install counted as success: %v", res.Installed)
	}
	if !strings.Contains(res.Failed["x"], "did not finish within 500ms") {
		t.Errorf("Failed[x] = %q, want a named timeout", res.Failed["x"])
	}
}
