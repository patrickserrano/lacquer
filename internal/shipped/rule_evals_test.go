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
	p := fromFixture(t, "rootapp")
	p.sync()
	var want string
	for _, name := range []string{"core", "ios"} {
		body, ok := region.ExtractBody(p.read("CLAUDE.md"), name)
		if !ok {
			t.Fatalf("missing %s region", name)
		}
		want += body + "\n"
	}
	want = strings.TrimRight(want, "\n") + "\n"
	plugin := filepath.Join(root(t), "evals/rules")
	got, err := os.ReadFile(filepath.Join(plugin, "rules.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("eval context drifted from sync; run go run ./evals/render")
	}
	script, err := os.ReadFile(filepath.Join(plugin, "fixtures/bump-marketing-version.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(script) != p.read("scripts/bump-marketing-version.sh") {
		t.Fatal("eval version helper drifted from sync")
	}
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
