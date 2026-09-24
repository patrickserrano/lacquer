package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/region"
)

// Compare against a fresh sync, not against the source templates: token and
// region rendering are part of the context an agent actually receives.
func TestRuleEvalPluginMatchesRenderedContext(t *testing.T) {
	plugin := filepath.Join(root(t), "evals/rules")
	for _, tc := range []struct{ profile, fixture, component string }{
		{"core", "spmpackage", "."},
		{"ios", "rootapp", "."},
		{"web", "multistack", "admin"},
		{"supabase", "multistack", "server"},
		{"marketing", "marketing", "."},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			p := fromFixture(t, tc.fixture)
			p.sync()
			for _, file := range []string{"CLAUDE.md", "AGENTS.md"} {
				body, ok := region.ExtractBody(p.read(file), "core")
				if !ok {
					t.Fatalf("missing core region in %s", file)
				}
				want := body + "\n"
				if tc.profile != "core" {
					body, ok = region.ExtractBody(p.read(filepath.Join(tc.component, file)), tc.profile)
					if !ok {
						t.Fatalf("missing %s region in %s", tc.profile, file)
					}
					want += body + "\n"
				}
				want = strings.TrimRight(want, "\n") + "\n"
				got, err := os.ReadFile(filepath.Join(plugin, "contexts", tc.profile, file))
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("%s context drifted; run go run ./evals/render", file)
				}
				if file == "CLAUDE.md" {
					checkRuleEvalHook(t, plugin, tc.profile, want)
				}
			}
			if tc.profile == "ios" {
				script, err := os.ReadFile(filepath.Join(plugin, "fixtures/bump-marketing-version.sh"))
				if err != nil {
					t.Fatal(err)
				}
				if string(script) != p.read("scripts/bump-marketing-version.sh") {
					t.Fatal("eval version helper drifted from sync")
				}
			}
		})
	}
}

func checkRuleEvalHook(t *testing.T, plugin, profile, want string) {
	t.Helper()
	hooks, err := os.ReadFile(filepath.Join(plugin, "hooks/hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal(hooks, &config); err != nil {
		t.Fatal(err)
	}
	start := config.Hooks["SessionStart"]
	if len(start) != 1 || len(start[0].Hooks) != 1 || start[0].Hooks[0].Type != "command" {
		t.Fatal("eval plugin must wire one SessionStart command")
	}
	cmd := exec.Command("bash", "-c", start[0].Hooks[0].Command)
	cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin)
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if profile != "core" {
		if err := os.WriteFile(filepath.Join(workspace, ".fixture/profile"), []byte(profile+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	input, err := json.Marshal(map[string]string{"cwd": workspace})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = strings.NewReader(string(input))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Output struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Output.Event != "SessionStart" || payload.Output.Context != want {
		t.Fatal("SessionStart did not deliver the rendered context")
	}
}
