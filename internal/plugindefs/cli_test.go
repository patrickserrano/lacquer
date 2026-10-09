package plugindefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/plugindefs/plugindefstest"
)

func TestValidateWithCLIPassesAValidSet(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "a.md"), "---\nname: a\ndescription: d\n---\nbody\n")
	res, err := ValidateWithCLI(plugindefstest.FakeClaude(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 0 || res.Checked != 1 || res.Version != "9.9.9" {
		t.Fatalf("got %+v, want 0 issues, 1 checked, version 9.9.9", res)
	}
}

func TestValidateWithCLIMapsAFailureBackToTheOriginalFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "good.md"), "---\nname: good\ndescription: d\n---\n")
	bad := write(t, filepath.Join(root, "agents", "bad.md"), "---\nname: bad\ndescription: d\n---\nBROKEN-BY-CLI\n")
	res, err := ValidateWithCLI(plugindefstest.FakeClaude(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 1 || res.Issues[0].Path != bad || !strings.Contains(res.Issues[0].Message, "No frontmatter") {
		t.Fatalf("got %+v, want one issue on %s", res.Issues, bad)
	}
}

func TestValidateWithCLIFailsWhenTheCLIFailsForAReasonItCannotName(t *testing.T) {
	// A CLI that exits non-zero and names no file must not read as "clean".
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	os.WriteFile(p, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1; exit 0; fi\necho '{\"success\":false,\"contents\":[]}'\nexit 1\n"), 0o755)
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "a.md"), "---\nname: a\n---\n")
	if _, err := ValidateWithCLI(p, root); err == nil {
		t.Fatal("a failing CLI run that names no file was reported as a pass")
	}
}

func TestValidateWithCLIErrorsOnGarbageOutput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	os.WriteFile(p, []byte("#!/bin/sh\necho 'boom' >&2\nexit 3\n"), 0o755)
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "a.md"), "---\nname: a\n---\n")
	if _, err := ValidateWithCLI(p, root); err == nil {
		t.Fatal("unparseable CLI output was reported as a pass")
	}
}

// bounded runs ValidateWithCLI under the TEST's own deadline, so an unbounded
// implementation fails here rather than hanging the suite.
func bounded(t *testing.T, claude string) (CLIResult, error, time.Duration) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "a.md"), "---\nname: a\ndescription: d\n---\n")
	type out struct {
		res CLIResult
		err error
	}
	done := make(chan out, 1)
	start := time.Now()
	go func() {
		r, err := ValidateWithCLI(claude, root)
		done <- out{r, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err, time.Since(start)
	case <-time.After(30 * time.Second):
		t.Fatal("ValidateWithCLI still blocked after 30s: claude --version is unbounded")
		return CLIResult{}, nil, 0
	}
}

func TestValidateWithCLITimesOutAClaudeThatNeverAnswersVersion(t *testing.T) {
	t.Parallel()
	fake := plugindefstest.BlockingClaude(t)
	res, err, took := bounded(t, fake.Path)
	if !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("err = %v, want ErrVersionTimeout", err)
	}
	if took > versionTimeout+5*time.Second {
		t.Errorf("took %s, want about %s", took, versionTimeout)
	}
	if res.Version != "" || len(res.Issues) != 0 {
		t.Errorf("a timed-out claude produced a result: %+v", res)
	}
	if fake.ValidateRan() {
		t.Error("validate ran after --version timed out")
	}
	fake.AssertGone(t, "claude")
}

func TestValidateWithCLIReapsTheGrandchildOfAHungClaude(t *testing.T) {
	t.Parallel()
	fake := plugindefstest.BlockingClaude(t)
	if _, err, _ := bounded(t, fake.Path); !errors.Is(err, ErrVersionTimeout) {
		t.Fatalf("err = %v, want ErrVersionTimeout", err)
	}
	fake.AssertGone(t, "helper")
	fake.AssertGone(t, "grandchild")
}

func TestValidateWithCLIRunsValidateWhenVersionAnswersFast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "validate.ran")
	p := filepath.Join(dir, "claude")
	os.WriteFile(p, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo '7.7.7 (Claude Code)'; exit 0; fi\ntouch '"+marker+"'\necho '{\"success\":true,\"contents\":[]}'\n"), 0o755)
	res, err, took := bounded(t, p)
	if err != nil || res.Version != "7.7.7" {
		t.Fatalf("got %+v, %v; want version 7.7.7", res, err)
	}
	if _, serr := os.Stat(marker); serr != nil {
		t.Error("validate did not run after a fast --version")
	}
	if took > 5*time.Second {
		t.Errorf("fast claude took %s", took)
	}
}
