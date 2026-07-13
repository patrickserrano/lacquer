package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/pluginbootstrap"
	"github.com/patrickserrano/lacquer/internal/skillsync"
)

// chdir switches the process cwd to dir for the duration of the test (run()
// resolves projectRoot via os.Getwd()) and restores it afterward.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// noEnv resolves every environment lookup to "" — the CLI then defaults
// LACQUER_ROOT to ".".
func noEnv(string) string { return "" }

// envMap returns a getenv backed by m.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRunDispatch(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string // substring expected on stdout
		wantErr  string // substring expected on stderr
	}{
		{"no args", nil, 2, "", "usage: lacquer"},
		{"unknown command", []string{"bogus"}, 2, "", "unknown command: bogus"},
		{"help word", []string{"help"}, 0, "usage: lacquer", ""},
		{"help long", []string{"--help"}, 0, "commands:", ""},
		{"help short", []string{"-h"}, 0, "LACQUER_ROOT", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tt.args, noEnv, &out, &errb)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.wantOut != "" && !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout missing %q:\n%s", tt.wantOut, out.String())
			}
			if tt.wantErr != "" && !strings.Contains(errb.String(), tt.wantErr) {
				t.Errorf("stderr missing %q:\n%s", tt.wantErr, errb.String())
			}
		})
	}
}

// --help must go to STDOUT (a user asking for help shouldn't parse stderr), and
// must not print anything to stderr.
func TestHelpGoesToStdout(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--help"}, noEnv, &out, &errb); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if errb.Len() != 0 {
		t.Errorf("--help wrote to stderr: %q", errb.String())
	}
	if !strings.Contains(out.String(), "audit") || !strings.Contains(out.String(), "exit 3") {
		t.Errorf("usage should name audit and document exit 3:\n%s", out.String())
	}
}

func TestVersionPrints(t *testing.T) {
	hr := t.TempDir()
	if err := os.WriteFile(filepath.Join(hr, "VERSION"), []byte("31\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hr, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"version"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errb.String())
	}
	if strings.TrimSpace(out.String()) != "31" {
		t.Errorf("version output = %q, want 31", out.String())
	}
}

// When LACQUER_ROOT points at a non-lacquer dir, lacquer-root-dependent commands
// must fail with an actionable message (naming LACQUER_ROOT), not an opaque
// "open VERSION: no such file".
func TestMissingLacquerRootIsFriendly(t *testing.T) {
	// init/onboard are included: init reads lacquerRoot to gate profiles, so an
	// unset/wrong LACQUER_ROOT would otherwise silently drop every shipping
	// profile (write an empty profiles list) with exit 0 instead of erroring.
	empty := t.TempDir() // no VERSION, no profiles/
	for _, cmd := range []string{"version", "sync", "status", "audit", "init", "onboard", "plugins"} {
		t.Run(cmd, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run([]string{cmd}, envMap(map[string]string{"LACQUER_ROOT": empty}), &out, &errb)
			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if !strings.Contains(errb.String(), "LACQUER_ROOT") ||
				!strings.Contains(errb.String(), "not a lacquer checkout") {
				t.Errorf("stderr not actionable for %q:\n%s", cmd, errb.String())
			}
		})
	}
}

// skills does not require LACQUER_ROOT — it only reads the project's own
// .lacquer.toml, unlike sync/audit/status/init/onboard above.
func TestSkillsDoesNotRequireLacquerRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte("[project]\nname=\"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	var out, errb bytes.Buffer
	code := run([]string{"skills"}, noEnv, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "nothing to install") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestSkillsMissingManifestFails(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	var out, errb bytes.Buffer
	code := run([]string{"skills"}, noEnv, &out, &errb)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), ".lacquer.toml") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestSkillsInstallsDeclaredEntries(t *testing.T) {
	dir := t.TempDir()
	data := "[project]\nname=\"x\"\nskills=[\"owner/repo@some-skill\"]\n"
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	orig := skillsync.Runner
	var gotArgs []string
	skillsync.Runner = func(d string, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("ok"), nil
	}
	defer func() { skillsync.Runner = orig }()

	var out, errb bytes.Buffer
	code := run([]string{"skills"}, noEnv, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "installed: some-skill") {
		t.Errorf("stdout = %q", out.String())
	}
	want := []string{"add", "owner/repo", "-s", "some-skill", "-p", "-y"}
	if len(gotArgs) != len(want) {
		t.Fatalf("Runner args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("Runner args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestSkillsReportsFailureAndExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	data := "[project]\nname=\"x\"\nskills=[\"owner/repo@bad-skill\"]\n"
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	orig := skillsync.Runner
	skillsync.Runner = func(d string, args ...string) ([]byte, error) {
		return []byte("boom"), errFake{}
	}
	defer func() { skillsync.Runner = orig }()

	var out, errb bytes.Buffer
	code := run([]string{"skills"}, noEnv, &out, &errb)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "failed: bad-skill") {
		t.Errorf("stderr = %q", errb.String())
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }

// lacquerRootWithPlugins builds a minimal lacquer checkout (VERSION,
// profiles/, core/bootstrap/plugins.toml) so `plugins` finds a manifest.
func lacquerRootWithPlugins(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrapDir := filepath.Join(dir, "core", "bootstrap")
	if err := os.MkdirAll(bootstrapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bootstrapDir, "plugins.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginsAppliesManifest(t *testing.T) {
	hr := lacquerRootWithPlugins(t, `
[[marketplace]]
name = "mp"
source = "owner/repo"

[[plugin]]
name = "plugin@mp"
`)
	orig := pluginbootstrap.Runner
	var calls [][]string
	pluginbootstrap.Runner = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte("ok"), nil
	}
	defer func() { pluginbootstrap.Runner = orig }()

	var out, errb bytes.Buffer
	code := run([]string{"plugins"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "marketplace: mp") || !strings.Contains(out.String(), "installed: plugin@mp") {
		t.Errorf("stdout = %q", out.String())
	}
	if len(calls) != 2 {
		t.Fatalf("got %d claude invocations, want 2", len(calls))
	}
}

func TestPluginsReportsFailureAndExitsNonZero(t *testing.T) {
	hr := lacquerRootWithPlugins(t, "[[plugin]]\nname=\"bad@mp\"\n")

	orig := pluginbootstrap.Runner
	pluginbootstrap.Runner = func(args ...string) ([]byte, error) {
		return []byte("boom"), errFake{}
	}
	defer func() { pluginbootstrap.Runner = orig }()

	var out, errb bytes.Buffer
	code := run([]string{"plugins"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "failed: bad@mp") {
		t.Errorf("stderr = %q", errb.String())
	}
}

func TestPluginsMissingManifestFails(t *testing.T) {
	hr := t.TempDir()
	if err := os.WriteFile(filepath.Join(hr, "VERSION"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hr, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	code := run([]string{"plugins"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "plugins.toml") {
		t.Errorf("stderr = %q", errb.String())
	}
}
