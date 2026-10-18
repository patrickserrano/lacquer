package procgroup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hangScript writes an executable that starts a grandchild sleeper, records the
// grandchild's pid in pidFile, then sleeps itself. It models a launcher (npx,
// claude) whose helper outlives a kill of only the direct child.
func hangScript(t *testing.T) (script, pidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "grandchild.pid")
	script = filepath.Join(dir, "hang.sh")
	body := "#!/bin/sh\nsleep 300 &\necho $! > " + pidFile + "\nsleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, pidFile
}

func readPid(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("grandchild never recorded its pid")
	return 0
}

// alive reports whether pid is a live process. A reaped-or-zombie grandchild
// reparented to init is gone for our purposes, so poll briefly.
func gone(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// within fails the test, instead of hanging it, when f does not return by the
// test's own deadline, so an unbounded implementation is red rather than stuck.
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

func TestTimeoutKillsTheWholeGroupAndNamesTheCommand(t *testing.T) {
	script, pidFile := hangScript(t)
	const limit = 500 * time.Millisecond
	cmd := New(limit, script, "--flag")
	start := time.Now()
	var err error
	within(t, 10*time.Second, func() { err = cmd.Run() })
	took := time.Since(start)

	if err == nil {
		t.Fatal("a hung command returned nil")
	}
	if took > limit+ReapWait {
		t.Errorf("returned after %s, want within %s + %s", took, limit, ReapWait)
	}
	var te *TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("error %v is not a *TimeoutError", err)
	}
	if !strings.Contains(err.Error(), filepath.Base(script)) || !strings.Contains(err.Error(), "--flag") {
		t.Errorf("error %q does not name the command", err)
	}
	if !strings.Contains(err.Error(), limit.String()) {
		t.Errorf("error %q does not name the limit %s", err, limit)
	}
	// Callers such as gitguard classify `err.(*exec.ExitError)` as "git said no".
	// A timeout is not an answer from the command, so it must not match.
	if _, ok := err.(*exec.ExitError); ok {
		t.Error("a TimeoutError must not be an *exec.ExitError")
	}
	pid := readPid(t, pidFile)
	if !gone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("grandchild %d survived the timeout", pid)
	}
}

func TestOutputAndCombinedOutputAreBoundedToo(t *testing.T) {
	script, _ := hangScript(t)
	for name, run := range map[string]func(*Bounded) error{
		"Output":         func(b *Bounded) error { _, err := b.Output(); return err },
		"CombinedOutput": func(b *Bounded) error { _, err := b.CombinedOutput(); return err },
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			within(t, 10*time.Second, func() { err = run(New(300*time.Millisecond, script)) })
			var te *TimeoutError
			if !errors.As(err, &te) {
				t.Fatalf("got %v, want *TimeoutError", err)
			}
		})
	}
}

func TestACommandThatFinishesIsNotATimeout(t *testing.T) {
	out, err := New(10*time.Second, "sh", "-c", "echo hi").Output()
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
	err = New(10*time.Second, "sh", "-c", "exit 3").Run()
	var te *TimeoutError
	if err == nil || errors.As(err, &te) {
		t.Fatalf("a plain failure must stay a plain failure, got %v", err)
	}
}
