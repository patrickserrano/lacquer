package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cfgOne() *config.Config {
	return &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"p"}}}}
}

// The whole point: a check that PASSES on known-bad input is broken, and doctor
// must say so. This is the shape of every serious defect found onboarding the
// fleet — a hook calling a removed flag, a build setting silently ignored, a
// gate that never ran.
func TestPassingOnBadInputIsReportedAsFailure(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"permissive check\"\nargv = [\"true\"]\nexpect = \"fail\"\n")

	var out bytes.Buffer
	res, err := Run(root, t.TempDir(), cfgOne(), &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a command that always succeeds must fail an expect=fail probe: %+v", res)
	}
	if !strings.Contains(out.String(), "not verifying what it claims") {
		t.Errorf("the output must explain WHY, got:\n%s", out.String())
	}
}

func TestRejectingBadInputPasses(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"strict check\"\nargv = [\"false\"]\nexpect = \"fail\"\n")

	res, err := Run(root, t.TempDir(), cfgOne(), new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("a command that rejects the fixture must satisfy expect=fail: %+v", res)
	}
}

// A missing tool is a finding, not a skip: the check it backs cannot be running
// anywhere this is true. That is the opposite of the rule for FIXERS, where a
// missing tool skips — a fixer leaves work undone, a check asserts compliance.
func TestMissingToolIsAFinding(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"ghost\"\nargv = [\"lacquer-no-such-binary\"]\nexpect = \"fail\"\n")

	res, err := Run(root, t.TempDir(), cfgOne(), new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatal("a missing tool must be reported, not treated as a passing check")
	}
	if !strings.Contains(res[0].Detail, "not installed") {
		t.Errorf("detail should name the missing tool, got %q", res[0].Detail)
	}
}

// The fixture must never be written into the project — a deliberately malformed
// file landing in a working tree could be committed or picked up by a watcher.
func TestFixtureIsWrittenOutsideTheProject(t *testing.T) {
	root := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"where\"\nfile = \"Probe.swift\"\ncontent = \"bad\"\nargv = [\"false\"]\nexpect = \"fail\"\n")

	if _, err := Run(root, proj, cfgOne(), new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, "Probe.swift")); !os.IsNotExist(err) {
		t.Error("the fixture was written into the project directory")
	}
}

func TestRejectsAnUnknownExpectation(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"x\"\nargv = [\"true\"]\nexpect = \"maybe\"\n")
	if _, err := LoadProbes(root, "p"); err == nil {
		t.Fatal("expect must be validated at load, not silently treated as one of the two")
	}
}
