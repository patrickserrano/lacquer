package shipped

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
)

// requireJQ stops a test that needs jq when jq is missing: a failure on a CI
// runner, where a skip would read as green with nothing run, and a skip on a
// developer's machine (#575, #578).
func requireJQ(t testing.TB, what string) {
	t.Helper()
	requireTool(t, "jq", what, exec.LookPath, os.Getenv)
}

func requireTool(t testing.TB, tool, what string, look func(string) (string, error), getenv func(string) string) {
	t.Helper()
	if _, err := look(tool); err == nil {
		return
	}
	if getenv("CI") != "" {
		t.Fatalf("%s not installed on a CI runner; %s cannot run", tool, what)
	}
	t.Skipf("%s not installed", tool)
}

// stopTB records how a helper stopped the test it was given.
type stopTB struct {
	testing.TB
	how string
}

func (s *stopTB) Helper() {}
func (s *stopTB) Fatalf(format string, args ...any) {
	s.how = "fatal: " + fmt.Sprintf(format, args...)
	runtime.Goexit()
}
func (s *stopTB) Skipf(format string, args ...any) {
	s.how = "skip: " + fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestRequireToolFailsUnderCIAndSkipsLocally(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	found := func(string) (string, error) { return "/usr/bin/jq", nil }
	for _, tc := range []struct {
		name string
		look func(string) (string, error)
		ci   string
		want string
	}{
		{"missing under CI", missing, "true", "fatal: jq not installed on a CI runner; the step tests cannot run"},
		{"missing locally", missing, "", "skip: jq not installed"},
		{"present under CI", found, "true", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &stopTB{TB: t}
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				requireTool(s, "jq", "the step tests", tc.look, func(k string) string {
					if k == "CI" {
						return tc.ci
					}
					return ""
				})
			}()
			wg.Wait()
			if s.how != tc.want {
				t.Errorf("stopped with %q, want %q", s.how, tc.want)
			}
		})
	}
}
