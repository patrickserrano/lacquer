package fixcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCommandsMissingFileIsNotAnError(t *testing.T) {
	cmds, err := LoadCommands(t.TempDir(), "nosuch")
	if err != nil || cmds != nil {
		t.Fatalf("got (%v, %v), want (nil, nil) — a profile need not ship fixers", cmds, err)
	}
}

func TestLoadCommandsRejectsIncompleteCommand(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profiles", "p", "fix.toml"), "[[command]]\nname = \"x\"\n")
	if _, err := LoadCommands(root, "p"); err == nil {
		t.Fatal("a command with no argv must be rejected, not silently skipped")
	}
}

// A fixer whose tool is absent must SKIP loudly and let the run continue. The
// opposite of the rule for checks: a check that cannot run has to block, but a
// fixer that cannot run has merely left work undone, and failing here would let
// one missing Homebrew formula block adoption entirely.
func TestMissingToolSkipsRatherThanFails(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profiles", "p", "fix.toml"),
		"[[command]]\nname = \"ghost\"\nargv = [\"lacquer-no-such-binary\", \"--x\"]\n")
	proj := t.TempDir()

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"p"}}}}
	var out bytes.Buffer
	res, err := Run(root, proj, cfg, &out)
	if err != nil {
		t.Fatalf("Run returned an error for a missing tool: %v", err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Status, "not installed") {
		t.Fatalf("status = %+v, want a skip naming the missing tool", res)
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Errorf("the skip must be visible in the output, got:\n%s", out.String())
	}
}

// Fixers run inside the component, because every config they read
// (.swiftformat, biome.json, deno.jsonc) is written there by sync.
func TestRunsInTheComponentDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profiles", "p", "fix.toml"),
		"[[command]]\nname = \"pwd\"\nargv = [\"sh\", \"-c\", \"pwd > where.txt\"]\n")
	proj := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proj, "ios"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Components: []config.Component{{Path: "ios", Profiles: []string{"p"}}}}
	if _, err := Run(root, proj, cfg, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(proj, "ios", "where.txt"))
	if err != nil {
		t.Fatalf("command did not run in the component dir: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(got)), "ios") {
		t.Errorf("ran in %q, want the component directory", strings.TrimSpace(string(got)))
	}
}
