package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUserConfig makes a config file under a temp dir and returns the env that
// points lacquer at it (via $HOME, or $XDG_CONFIG_HOME when xdg), plus its path.
// No test reads the real home directory.
func writeUserConfig(t *testing.T, xdg bool, body string) (env map[string]string, path string) {
	t.Helper()
	root := t.TempDir()
	if xdg {
		env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(root, "xdg"), "HOME": filepath.Join(root, "unused-home")}
		path = filepath.Join(root, "xdg", "lacquer", "config.toml")
	} else {
		env = map[string]string{"HOME": root, "XDG_CONFIG_HOME": ""}
		path = filepath.Join(root, ".config", "lacquer", "config.toml")
	}
	if body != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return env, path
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{a, b} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// The fail paths first. Every one asks gh nothing and exits non-zero.

func TestDecisionsFleetWithNoConfigFileNamesAllThreeSourcesAndThePath(t *testing.T) {
	calls := ghScript(t, nil, nil)
	for name, xdg := range map[string]bool{"home": false, "xdg": true} {
		env, path := writeUserConfig(t, xdg, "") // the file does not exist
		code, stdout, stderr := runDecisions(t, env, "--fleet")
		if code == 0 || stdout != "" || len(*calls) != 0 {
			t.Fatalf("%s: code %d stdout %q calls %v", name, code, stdout, *calls)
		}
		for _, want := range []string{"--fleet-repo", "LACQUER_FLEET_REPO", "fleet_repo", path} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr %q should name %q", name, stderr, want)
			}
		}
	}
	// Neither $XDG_CONFIG_HOME nor $HOME: still an error, and it says why there is no path.
	code, _, stderr := runDecisions(t, map[string]string{"HOME": "", "XDG_CONFIG_HOME": ""}, "--fleet")
	if code == 0 || !strings.Contains(stderr, "XDG_CONFIG_HOME") || !strings.Contains(stderr, "fleet_repo") || len(*calls) != 0 {
		t.Errorf("no home: code %d stderr %q calls %v", code, stderr, *calls)
	}
}

func TestDecisionsFleetRefusesAMalformedUserConfig(t *testing.T) {
	calls := ghScript(t, nil, nil)
	for name, tc := range map[string]struct{ body, want string }{
		"not toml":          {"fleet_repo = = acme/ops", "is not valid"},
		"unquoted":          {"fleet_repo = acme/ops", "is not valid"},
		"wrong type":        {"fleet_repo = 7", "is not valid"},
		"not owner/name":    {`fleet_repo = "ops"`, "not owner/name"},
		"too many segments": {`fleet_repo = "a/b/c"`, "not owner/name"},
		"leading dash":      {`fleet_repo = "-x/ops"`, "not owner/name"},
		"typo'd key":        {`fleet-repo = "acme/ops"`, "unknown key"},
		"extra key":         {"fleet_repo = \"acme/ops\"\nother = 1", "unknown key"},
	} {
		env, path := writeUserConfig(t, false, tc.body)
		code, stdout, stderr := runDecisions(t, env, "--fleet")
		if code == 0 || stdout != "" || len(*calls) != 0 {
			t.Fatalf("%s: code %d stdout %q calls %v", name, code, stdout, *calls)
		}
		if !strings.Contains(stderr, path) || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: stderr %q should name %q and %q", name, stderr, path, tc.want)
		}
	}
	// An unreadable config (here a directory in its place) is an error, not "missing".
	env, path := writeUserConfig(t, false, "")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runDecisions(t, env, "--fleet"); code == 0 || !strings.Contains(stderr, "cannot read") || !strings.Contains(stderr, path) || len(*calls) != 0 {
		t.Errorf("directory: code %d stderr %q calls %v", code, stderr, *calls)
	}
}

func TestDecisionsFleetTreatsAnEmptyOrKeylessUserConfigAsNotSet(t *testing.T) {
	calls := ghScript(t, nil, nil)
	for name, body := range map[string]string{"empty value": `fleet_repo = ""`, "only a comment": "# nothing here\n"} {
		env, path := writeUserConfig(t, false, body)
		code, _, stderr := runDecisions(t, env, "--fleet")
		if code == 0 || !strings.Contains(stderr, "no fleet repository is configured") || !strings.Contains(stderr, path) || len(*calls) != 0 {
			t.Errorf("%s: code %d stderr %q calls %v", name, code, stderr, *calls)
		}
	}
}

// Each precedence step wins over the ones below it.

func TestDecisionsFleetPrecedenceIsFlagThenEnvThenUserConfig(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		repo string
	}{
		"config alone":              {nil, []string{"--fleet"}, "acme/from-config"},
		"env beats config":          {map[string]string{"LACQUER_FLEET_REPO": "acme/from-env"}, []string{"--fleet"}, "acme/from-env"},
		"flag beats config":         {nil, []string{"--fleet", "--fleet-repo", "acme/from-flag"}, "acme/from-flag"},
		"flag beats env and config": {map[string]string{"LACQUER_FLEET_REPO": "acme/from-env"}, []string{"--fleet", "--fleet-repo=acme/from-flag"}, "acme/from-flag"},
	} {
		cfg, _ := writeUserConfig(t, false, `fleet_repo = "acme/from-config"`)
		calls := ghScript(t, map[string]string{listFor(tc.repo): `[]`, closedFor(tc.repo): `[]`}, nil)
		code, stdout, stderr := runDecisions(t, merge(cfg, tc.env), tc.args...)
		if code != 0 || stdout != "no decisions recorded for "+tc.repo+"\n" || len(*calls) != 2 {
			t.Errorf("%s: code %d stdout %q stderr %q calls %v", name, code, stdout, stderr, *calls)
		}
	}
}

// $XDG_CONFIG_HOME is honoured, and wins over $HOME; a relative one is ignored,
// per the XDG rule, rather than resolving against whatever the cwd is.
func TestDecisionsFleetUserConfigHonoursXDGConfigHome(t *testing.T) {
	env, _ := writeUserConfig(t, true, `fleet_repo = "acme/from-xdg"`)
	calls := ghScript(t, map[string]string{listFor("acme/from-xdg"): `[]`, closedFor("acme/from-xdg"): `[]`}, nil)
	if code, stdout, stderr := runDecisions(t, env, "--fleet"); code != 0 || stdout != "no decisions recorded for acme/from-xdg\n" {
		t.Errorf("xdg: code %d stdout %q stderr %q calls %v", code, stdout, stderr, *calls)
	}
	// Relative XDG_CONFIG_HOME: ignored, so $HOME's file is the one read.
	home, _ := writeUserConfig(t, false, `fleet_repo = "acme/from-home"`)
	home["XDG_CONFIG_HOME"] = "relative/dir"
	calls = ghScript(t, map[string]string{listFor("acme/from-home"): `[]`, closedFor("acme/from-home"): `[]`}, nil)
	if code, stdout, stderr := runDecisions(t, home, "--fleet"); code != 0 || stdout != "no decisions recorded for acme/from-home\n" {
		t.Errorf("relative xdg: code %d stdout %q stderr %q calls %v", code, stdout, stderr, *calls)
	}
}

// A broken config must not block a command that was told the repository
// explicitly: the lower sources are not even read.
func TestDecisionsFleetFlagAndEnvDoNotReadTheUserConfig(t *testing.T) {
	broken, _ := writeUserConfig(t, false, "this is not toml = = =")
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"flag": {nil, []string{"--fleet", "--fleet-repo", "acme/ops"}},
		"env":  {map[string]string{"LACQUER_FLEET_REPO": "acme/ops"}, []string{"--fleet"}},
	} {
		calls := ghScript(t, map[string]string{listFor("acme/ops"): `[]`, closedFor("acme/ops"): `[]`}, nil)
		if code, stdout, stderr := runDecisions(t, merge(broken, tc.env), tc.args...); code != 0 || stdout != "no decisions recorded for acme/ops\n" {
			t.Errorf("%s: code %d stdout %q stderr %q calls %v", name, code, stdout, stderr, *calls)
		}
	}
}

// Without --fleet the user config is not consulted at all.
func TestDecisionsWithoutFleetIgnoresTheUserConfig(t *testing.T) {
	env, _ := writeUserConfig(t, false, "not toml = = =")
	calls := ghScript(t, map[string]string{listFor("o/r"): `[]`, closedFor("o/r"): `[]`}, nil)
	if code, stdout, stderr := runDecisions(t, env, "o/r"); code != 0 || stdout != "no decisions recorded for o/r\n" {
		t.Errorf("code %d stdout %q stderr %q calls %v", code, stdout, stderr, *calls)
	}
}
