package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDerivedDataHook(t *testing.T) {
	for _, fixture := range []string{"rootapp", "multistack"} {
		t.Run(fixture, func(t *testing.T) {
			p := fromFixture(t, fixture)
			p.sync()
			var settings struct {
				Hooks map[string][]struct {
					Matcher string
					Hooks   []struct{ Command string }
				}
			}
			if err := json.Unmarshal([]byte(p.read(".claude/settings.json")), &settings); err != nil {
				t.Fatal(err)
			}
			var hook string
			for _, entry := range settings.Hooks["PreToolUse"] {
				for _, h := range entry.Hooks {
					if entry.Matcher == "Bash" && strings.Contains(h.Command, "derived-data") {
						hook = h.Command
					}
				}
			}
			if hook == "" {
				t.Fatal("rendered settings have no Bash DerivedData guard")
			}
			for _, tc := range []struct {
				command string
				deny    bool
			}{
				{"flowdeck build", true}, {"flowdeck run", true}, {"flowdeck test", true}, {"flowdeck clean", true},
				{"xcodebuild -showBuildSettings", true}, {"xcodebuild -list", true}, {"xcodebuild -resolvePackageDependencies", true},
				{"xcrun xcodebuild -showBuildSettings", true}, {"env FOO=bar /usr/bin/xcodebuild -list", true},
				{`flowdeck build -d ""`, true}, {`flowdeck build --derived-data-path`, true},
				{`flowdeck build; echo -d DerivedData`, true}, {`echo -d DerivedData && flowdeck build`, true},
				{`flowdeck build -d DerivedData && flowdeck test`, true},
				{`bash -c 'flowdeck build'`, true}, {"cd ios\nflowdeck build", true},
				{`flowdeck build -d "$(git rev-parse --show-toplevel)/DerivedData"`, false},
				{`flowdeck test --derived-data-path="/repo with spaces/DerivedData"`, false},
				{`flowdeck clean -d DerivedData`, false}, {`xcodebuild -showBuildSettings -derivedDataPath DerivedData`, false},
				{`xcrun xcodebuild -list -derivedDataPath "./DerivedData"`, false},
				{`flowdeck build -d DerivedData | tee build.log`, false},
				{`echo "flowdeck build"`, false}, {`flowdeck config get --json`, false}, {`git status`, false},
				{`flowdeck project schemes -s build`, false},
				{`if flowdeck build; then echo done; fi`, true},
				{"cat > notes.md <<'EOF'\nIt doesn't matter\nEOF\n", false},
				{"cat > notes.md <<'EOF'\nflowdeck build\nEOF\n", false},
				{`echo "it's"'`, false},
				{"flowdeck build <<'EOF'\ndon't\nEOF", true},
				{`xcodebuild -list '`, true},
				{"cat <<EOF\nflowdeck build\nEOF\n", false},
				{"cat <<\"EOF\"\nflowdeck build\nEOF\n", false},
				{"cat <<-EOF\n\tflowdeck build\n\tEOF\n", false},
				{"cat <<-'EOF'\n\tdon't run flowdeck build\n\tEOF\n", false},
				{"cat <<EOF\nEOF suffix\nflowdeck build\nEOF\n", false},
				{"cat <<EOF\n EOF\nflowdeck build\nEOF\n", false},
				{"cat <<A <<'B'\nflowdeck build\nA\ndon't run xcodebuild\nB\n", false},
				{"cat <<'EOF'\nflowdeck build\nEOF\nxcodebuild -list", true},
				{"cat <<-EOF\n\tflowdeck build\n\tEOF\nxcodebuild -list", true},
				{"flowdeck build -d DerivedData <<'EOF'\ndon't\nEOF", false},
				{"echo '<<EOF'\nxcodebuild -list", true},
				{"echo ok; # <<EOF\nxcodebuild -list", true},
				{"cat <<< 'example'\nxcodebuild -list", true},
			} {
				t.Run(tc.command, func(t *testing.T) {
					payload, _ := json.Marshal(map[string]any{"tool_input": map[string]string{"command": tc.command}})
					cmd := exec.Command("bash", "-c", hook)
					cmd.Dir = p.root
					cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+p.root)
					cmd.Stdin = strings.NewReader(string(payload))
					out, err := cmd.CombinedOutput()
					code := 0
					if err != nil {
						if e, ok := err.(*exec.ExitError); ok {
							code = e.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					if tc.deny {
						if code != 2 {
							t.Fatalf("exit %d, want denial (2): %s", code, out)
						}
						wantMessage := `$(git rev-parse --show-toplevel)/DerivedData`
						if tc.command == `xcodebuild -list '` {
							wantMessage = "BLOCKED: cannot inspect Bash command for an explicit DerivedData path."
						}
						if !strings.Contains(string(out), wantMessage) {
							t.Fatalf("missing fix: %s", out)
						}
					} else if code != 0 {
						t.Fatalf("safe command rejected: %s", out)
					}
				})
			}
		})
	}
}
