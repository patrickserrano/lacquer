package pluginbootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hangingClaude puts a `claude` on PATH that never exits, as a launcher blocked on
// a login or first-launch prompt does, and shortens the limit so the test is quick.
func hangingClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	orig := pluginLimit
	pluginLimit = 500 * time.Millisecond
	t.Cleanup(func() { pluginLimit = orig })
}

// within fails the test, instead of hanging it, when f does not return by the
// test's own deadline. That deadline is what makes this red against an unbounded
// call rather than a stuck test run.
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
	hangingClaude(t)
	var err error
	within(t, 10*time.Second, func() { _, err = Runner("plugin", "install", "x@y") })
	if err == nil {
		t.Fatal("a hung claude returned nil")
	}
	for _, want := range []string{"claude plugin install x@y", "500ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// A timed-out add or install must land in Failed, which main turns into a
// non-zero exit, and never in the installed lists.
func TestApplyReportsATimedOutCallAsFailedNotInstalled(t *testing.T) {
	hangingClaude(t)
	m := &Manifest{
		Marketplaces: []Marketplace{{Name: "mp", Source: "o/r"}},
		Plugins:      []Plugin{{Name: "p@mp"}},
	}
	var res Result
	within(t, 15*time.Second, func() { res = Apply(m) })
	if len(res.Marketplaces) != 0 || len(res.Plugins) != 0 {
		t.Errorf("a timed-out call counted as success: %+v", res)
	}
	for _, key := range []string{"marketplace:mp", "p@mp"} {
		if !strings.Contains(res.Failed[key], "did not finish within 500ms") {
			t.Errorf("Failed[%q] = %q, want a named timeout", key, res.Failed[key])
		}
	}
}
