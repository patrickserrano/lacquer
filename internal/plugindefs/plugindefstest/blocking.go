package plugindefstest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Blocking is a fake `claude` that never answers --version: it parks the way a
// quarantined binary waiting on a first-launch prompt does. It also starts a
// helper that starts a sleeper, so a test can tell "the direct child was killed"
// from "the whole tree was".
type Blocking struct {
	// Path is the fake `claude`.
	Path string
	dir  string
}

// BlockingClaude installs the fake. Anything it left running is killed when the
// test ends, so a failing (unbounded) implementation cannot leak sleepers.
//
// Any call other than --version drops a marker file, which ValidateRan reports:
// the proof that validate was, or was not, attempted.
func BlockingClaude(t *testing.T) *Blocking {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	script := `#!/bin/sh
D='` + dir + `'
if [ "$1" = "--version" ]; then
  echo $$ > "$D/pid.claude"
  sh -c 'echo $$ > "$0/pid.helper"; sleep 60 & echo $! > "$0/pid.grandchild"; wait' "$D" &
  wait
  exit 0
fi
touch "$D/validate.ran"
exit 1
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &Blocking{Path: p, dir: dir}
	t.Cleanup(func() {
		for _, n := range []string{"claude", "helper", "grandchild"} {
			if pid := b.pid(n); pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	return b
}

func (b *Blocking) pid(name string) int {
	raw, err := os.ReadFile(filepath.Join(b.dir, "pid."+name))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return n
}

// ValidateRan reports whether the fake was ever called for anything but --version.
func (b *Blocking) ValidateRan() bool {
	_, err := os.Stat(filepath.Join(b.dir, "validate.ran"))
	return err == nil
}

// AssertGone fails unless the named process ("claude" is the fake itself,
// "helper" its child, "grandchild" the sleeper) was started and is now dead.
// "Was started" matters: a pid file that never appeared means the fake never ran
// as intended, which must not read as "reaped".
func (b *Blocking) AssertGone(t *testing.T, name string) {
	t.Helper()
	pid := b.pid(name)
	if pid <= 0 {
		t.Fatalf("the fake's %s never started (no pid recorded); nothing was proven", name)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s (pid %d) outlived the audit", name, pid)
}
