package plugindefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
