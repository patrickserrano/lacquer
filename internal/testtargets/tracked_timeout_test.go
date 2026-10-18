package testtargets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/procgroup"
)

// hangingGit puts a `git` on PATH that never exits, after the repository was
// built with the real one, and shortens the limit so the test is quick.
func hangingGit(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	orig := GitLimit
	GitLimit = 500 * time.Millisecond
	t.Cleanup(func() { GitLimit = orig })
}

// ReadForAudit asks git whether the pbxproj is tracked. A git that hangs must
// surface as an error the audit fails on, not as "not tracked", which would make
// the audit read the XcodeGen spec instead of the project and carry on.
func TestAGitTimeoutInReadForAuditIsAnErrorNotUntracked(t *testing.T) {
	dir := t.TempDir()
	gittest.Init(t, dir, "-q")
	if err := os.MkdirAll(filepath.Join(dir, "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "App", "project.yml"), []byte("name: App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hangingGit(t)

	var err error
	done := make(chan struct{})
	go func() { _, _, _, err = ReadForAudit(dir, "App/App.xcodeproj"); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("ReadForAudit still running after 15s: the git call is not bounded")
	}
	if !procgroup.IsTimeout(err) {
		t.Fatalf("got %v, want a procgroup timeout", err)
	}
	if !strings.Contains(err.Error(), "500ms") {
		t.Errorf("error %q does not name the limit", err)
	}
}
