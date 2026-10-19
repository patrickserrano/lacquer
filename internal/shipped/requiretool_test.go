package shipped

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// requireJQ stops a test that needs jq when jq is missing: a failure on a CI
// runner, where a skip would read as green with nothing run, and a skip on a
// developer's machine (#575, #578).
func requireJQ(t testing.TB, what string) {
	t.Helper()
	switch stop, msg := toolMissing("jq", what, exec.LookPath, os.Getenv); stop {
	case "fatal":
		t.Fatal(msg)
	case "skip":
		t.Skip(msg)
	}
}

// toolMissing decides what a test needing tool does: nothing when it is
// installed, "fatal" under CI, "skip" elsewhere. A plain function rather than
// one that stops the test itself, so it is tested without a goroutine: this
// package runs without -race and must stay free of concurrency.
func toolMissing(tool, what string, look func(string) (string, error), getenv func(string) string) (stop, msg string) {
	if _, err := look(tool); err == nil {
		return "", ""
	}
	if getenv("CI") != "" {
		return "fatal", fmt.Sprintf("%s not installed on a CI runner; %s cannot run", tool, what)
	}
	return "skip", tool + " not installed"
}

func TestRequireToolFailsUnderCIAndSkipsLocally(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	found := func(string) (string, error) { return "/usr/bin/jq", nil }
	for _, tc := range []struct {
		name            string
		look            func(string) (string, error)
		ci              string
		wantStop, wantM string
	}{
		{"missing under CI", missing, "true", "fatal", "jq not installed on a CI runner; the step tests cannot run"},
		{"missing locally", missing, "", "skip", "jq not installed"},
		{"present under CI", found, "true", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stop, msg := toolMissing("jq", "the step tests", tc.look, func(k string) string {
				if k == "CI" {
					return tc.ci
				}
				return ""
			})
			if stop != tc.wantStop || msg != tc.wantM {
				t.Errorf("got %q %q, want %q %q", stop, msg, tc.wantStop, tc.wantM)
			}
		})
	}
}
