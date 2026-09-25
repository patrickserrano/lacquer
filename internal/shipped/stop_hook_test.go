package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A background agent that goes idle writes itself into the operator's inbox
// (lacquer#380) through a Claude Code Stop hook that runs
// `lacquer console inbox hook stop`. The hook has to be in the file a project
// actually receives, so it is read from the RENDERED settings.json, and then
// executed: the two properties that matter are that it reaches lacquer with the
// hook payload on stdin, and that it is silent when lacquer is not installed.

type stopHookCmd struct {
	Command string
	Timeout int
}

func stopHooks(t *testing.T, raw string) []stopHookCmd {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v", err)
	}
	var out []stopHookCmd
	for _, e := range s.Hooks["Stop"] {
		for _, h := range e.Hooks {
			if h.Type == "command" && strings.Contains(h.Command, "console inbox hook stop") {
				out = append(out, stopHookCmd{h.Command, h.Timeout})
			}
		}
	}
	return out
}

func TestRenderedIOSSettingsShipTheStopHook(t *testing.T) {
	p := fromFixture(t, "rootapp")
	p.sync()
	got := stopHooks(t, p.read(".claude/settings.json"))
	if len(got) != 1 {
		t.Fatalf("rendered iOS .claude/settings.json has %d Stop hooks running `console inbox hook stop`, want 1", len(got))
	}
	if got[0].Timeout <= 0 || got[0].Timeout > 15 {
		t.Errorf("timeout %d: the hook needs an explicit, short one", got[0].Timeout)
	}
}

func TestLacquersOwnSettingsHaveTheStopHook(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := stopHooks(t, string(raw)); len(got) != 1 {
		t.Fatalf("lacquer's own .claude/settings.json has %d such Stop hooks, want 1", len(got))
	}
}

func TestStopHookRunsLacquerWithStdinAndIsSilentWithout(t *testing.T) {
	p := fromFixture(t, "rootapp")
	p.sync()
	cmd := stopHooks(t, p.read(".claude/settings.json"))[0].Command
	payload := `{"session_id":"abc","hook_event_name":"Stop"}`

	// lacquer on PATH: it must be called with the subcommand and be handed stdin.
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "rec")
	script := "#!/bin/sh\necho \"$*\" > " + rec + "\ncat >> " + rec + "\n"
	if err := os.WriteFile(filepath.Join(bin, "lacquer"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	c.Stdin = strings.NewReader(payload)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rec)
	if !strings.HasPrefix(string(got), "console inbox hook stop") || !strings.Contains(string(got), payload) {
		t.Fatalf("lacquer got %q, want the subcommand and the payload on stdin", got)
	}

	// lacquer absent: no output, exit 0.
	empty := t.TempDir()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	// The hook re-invokes `bash`, so PATH holds bash and nothing else.
	if err := os.Symlink(bash, filepath.Join(empty, "bash")); err != nil {
		t.Fatal(err)
	}
	c = exec.Command(bash, "-c", cmd)
	c.Env = []string{"PATH=" + empty}
	c.Stdin = strings.NewReader(payload)
	if out, err := c.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("without lacquer the hook must be silent and exit 0, got err=%v out=%q", err, out)
	}
}

// A lacquer that predates the subcommand (or fails for any reason) must not
// surface as a hook error in the session: on an old install every Stop would
// otherwise print lacquer's version banner and "unknown inbox subcommand".
func TestStopHookSwallowsALacquerThatFails(t *testing.T) {
	p := fromFixture(t, "rootapp")
	p.sync()
	cmd := stopHooks(t, p.read(".claude/settings.json"))[0].Command
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'error: unknown inbox subcommand \"hook\"' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "lacquer"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("bash", "-c", cmd)
	c.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	c.Stdin = strings.NewReader(`{}`)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("a failing lacquer must not fail the hook, got %v\n%s", err, out)
	}
}
