package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	for _, cmd := range []string{"version", "sync", "status", "audit", "init", "onboard"} {
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
