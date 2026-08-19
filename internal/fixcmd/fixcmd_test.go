package fixcmd

import (
	"bytes"
	"os"
	"os/exec"
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

// swiftformat with organizeDeclarations is not idempotent in one pass:
// reordering members changes their nesting, and the indent rule only sees the
// new layout on the next run. A single pass left kit with 6 files its own
// pre-push hook rejected, AFTER `lacquer fix` reported success. So the runner
// re-runs a fixer until the tree stops moving.
func TestFixerRunsToAFixedPoint(t *testing.T) {
	root := t.TempDir()
	// A fixer that changes something on its first two runs, then stops.
	writeFile(t, filepath.Join(root, "profiles", "p", "fix.toml"),
		"[[command]]\nname = \"grow\"\nargv = [\"sh\", \"-c\", \"n=$(cat n 2>/dev/null || echo 0); "+
			"if [ \\\"$n\\\" -lt 2 ]; then echo $((n+1)) > n; fi\"]\n")
	proj := t.TempDir()
	for _, c := range [][]string{{"init"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		cmd := exec.Command("git", c...)
		cmd.Dir = proj
		if err := cmd.Run(); err != nil {
			t.Skipf("git unavailable: %v", err)
		}
	}

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"p"}}}}
	res, err := Run(root, proj, cfg, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if res[0].Passes < 2 {
		t.Errorf("ran %d pass(es); a fixer still changing the tree must run again", res[0].Passes)
	}
	if res[0].Passes > maxPasses {
		t.Errorf("ran %d passes, above the %d cap", res[0].Passes, maxPasses)
	}
}

// A component-local binary must be found in the COMPONENT, not in whatever
// directory the lacquer process happens to be sitting in.
//
// Both exec.LookPath and exec.Command resolve a name carrying a separator
// against this process's working directory rather than against cmd.Dir, so
// without Binary a project laid out with components would silently report
// "./node_modules/.bin/biome is not installed" and skip the fixer — the failure
// mode that looks like a successful run.
func TestComponentLocalBinaryIsResolvedAgainstTheComponent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "profiles", "p", "fix.toml"),
		"[[command]]\nname = \"local\"\nargv = [\"./node_modules/.bin/tool\"]\n")

	proj := t.TempDir()
	// The binary exists ONLY inside the component. A run that looks anywhere
	// else finds nothing.
	bin := filepath.Join(proj, "admin", "node_modules", ".bin", "tool")
	// Writes into its working directory, which fixcmd sets to the component.
	writeFile(t, bin, "#!/bin/sh\necho ran > ran.txt\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// And the process cwd is the project ROOT, which is where the unresolved
	// lookup would have gone.
	chdir(t, proj)

	cfg := &config.Config{Components: []config.Component{{Path: "admin", Profiles: []string{"p"}}}}
	var out bytes.Buffer
	res, err := Run(root, proj, cfg, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("results = %+v, want one", res)
	}
	if strings.Contains(res[0].Status, "not installed") {
		t.Fatalf("a binary that exists in the component was reported missing: %s\n%s", res[0].Status, out.String())
	}
	if _, err := os.Stat(filepath.Join(proj, "admin", "ran.txt")); err != nil {
		t.Errorf("the component-local binary never ran: %v\n%s", err, out.String())
	}
}

// A bare name is still a PATH lookup, and an absolute path is taken as given.
// Rewriting either would break every existing fixer (swiftformat, deno).
func TestBinaryLeavesPathLookupsAlone(t *testing.T) {
	cases := []struct{ dir, argv0, want string }{
		{"/proj/admin", "swiftformat", "swiftformat"},
		{"/proj/admin", "deno", "deno"},
		{"/proj/admin", "/usr/local/bin/biome", "/usr/local/bin/biome"},
		{"/proj/admin", "./node_modules/.bin/biome", "/proj/admin/node_modules/.bin/biome"},
		{"/proj/admin", "node_modules/.bin/biome", "/proj/admin/node_modules/.bin/biome"},
	}
	for _, c := range cases {
		if got := Binary(c.dir, c.argv0); got != c.want {
			t.Errorf("Binary(%q, %q) = %q, want %q", c.dir, c.argv0, got, c.want)
		}
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
}
