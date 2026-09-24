package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCodexGuards(t *testing.T) {
	p := fromFixture(t, "multistack")
	p.sync()
	var config struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Type, Command string }
		}
	}
	if err := json.Unmarshal([]byte(p.read(".codex/hooks.json")), &config); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tool, command string
		blocked             bool
	}{
		{"force", "Bash", "git push --force origin topic", true},
		{"lease", "Bash", "git -C repo push --force-with-lease=main:abc", true},
		{"no-verify", "Bash", "git commit --no-verify -m fix", true},
		{"admin", "Bash", "gh pr merge 42 --admin", true},
		{"short-force", "Bash", "git push -f origin topic", true},
		{"multiline", "Bash", "git push \\\n --force", true},
		{"safe-git", "Bash", "git status --short", false},
		{"safe-gh", "Bash", "gh pr view 42", false},
		{"safe-force", "Bash", "lacquer sync --force", false},
		{"version-script", "Bash", "scripts/bump-marketing-version.sh 1.2.3", false},
		{"project", "apply_patch", "*** Begin Patch\n*** Update File: ios/App.xcodeproj/project.pbxproj\n@@\n-old\n+new\n*** End Patch", true},
		{"workspace", "apply_patch", "*** Begin Patch\n*** Add File: App.xcworkspace/contents.xcworkspacedata\n+new\n*** End Patch", true},
		{"storyboard", "apply_patch", "*** Begin Patch\n*** Delete File: Main.storyboard\n*** End Patch", true},
		{"xib", "apply_patch", "*** Begin Patch\n*** Add File: Main.xib\n+new\n*** End Patch", true},
		{"entitlements", "apply_patch", "*** Begin Patch\n*** Update File: App.entitlements\n@@\n-old\n+new\n*** End Patch", true},
		{"rename", "apply_patch", "*** Begin Patch\n*** Update File: harmless.txt\n*** Move to: App.entitlements\n@@\n-old\n+new\n*** End Patch", true},
		{"safe-edit", "apply_patch", "*** Begin Patch\n*** Update File: app.go\n@@\n-old\n+new\n*** End Patch", false},
		{"mention", "apply_patch", "*** Begin Patch\n*** Update File: README.md\n@@\n+Do not edit App.entitlements\n*** End Patch", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tc.tool, "tool_input": map[string]string{"command": tc.command}})
			if err != nil {
				t.Fatal(err)
			}
			matched, blocked := false, false
			for _, group := range config.Hooks["PreToolUse"] {
				if !regexp.MustCompile(group.Matcher).MatchString(tc.tool) {
					continue
				}
				for _, hook := range group.Hooks {
					matched = true
					if hook.Type != "command" {
						t.Fatalf("unsupported hook type %q", hook.Type)
					}
					cmd := exec.Command("sh", "-c", hook.Command)
					cmd.Dir = filepath.Join(p.root, "ios") // must work when launched below repo root
					cmd.Stdin = strings.NewReader(string(payload))
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("hook failed instead of returning a decision: %v\n%s", err, out)
					}
					if len(out) != 0 {
						var result struct {
							Output struct {
								Event    string `json:"hookEventName"`
								Decision string `json:"permissionDecision"`
								Reason   string `json:"permissionDecisionReason"`
							} `json:"hookSpecificOutput"`
						}
						if err := json.Unmarshal(out, &result); err != nil {
							t.Fatalf("invalid hook output: %s", out)
						}
						if result.Output.Event != "PreToolUse" || result.Output.Decision != "deny" || result.Output.Reason == "" {
							t.Fatalf("invalid denial: %s", out)
						}
						blocked = true
					}
				}
			}
			if !matched || blocked != tc.blocked {
				t.Fatalf("matched=%v blocked=%v want blocked=%v", matched, blocked, tc.blocked)
			}
		})
	}
}

func TestCodexGuardsToolGating(t *testing.T) {
	for _, tool := range []string{"claude", "antigravity", "codex"} {
		t.Run(tool, func(t *testing.T) {
			p := fromFixture(t, "rootapp")
			manifest := strings.Replace(p.read(".lacquer.toml"), `["claude", "codex", "antigravity"]`, `["`+tool+`"]`, 1)
			if err := os.WriteFile(filepath.Join(p.root, ".lacquer.toml"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			p.sync()
			for _, path := range []string{".codex/hooks.json", ".codex/hooks/lacquer-guard.py"} {
				_, err := os.Stat(filepath.Join(p.root, path))
				if (err == nil) != (tool == "codex") {
					t.Errorf("%s for %s: %v", path, tool, err)
				}
			}
			_, err := os.Stat(filepath.Join(p.root, "AGENTS.md"))
			if (err == nil) != (tool != "claude") {
				t.Errorf("AGENTS.md for %s: %v", tool, err)
			}
		})
	}
}
