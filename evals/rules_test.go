package evals

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type grader struct {
	Name    string                        `yaml:"name"`
	Type    string                        `yaml:"type"`
	Tool    string                        `yaml:"tool"`
	Input   string                        `yaml:"input_match"`
	Pattern string                        `yaml:"pattern"`
	Target  struct{ Source, Path string } `yaml:"target"`
	Min     *int                          `yaml:"min"`
	Max     *int                          `yaml:"max"`
}

func TestRuleEvalOutcomeGraders(t *testing.T) {
	for _, tc := range []struct {
		name, grader, path, good string
		bad                      []string
	}{
		{"ci-wait", "outcome", "ci-result.txt", "FAIL\n", []string{"", "PASS\n", "PENDING\n"}},
		{"no-force-push", "verified-outcome", "result.json", "{\"passed\": true}\n", []string{"", "{\"passed\": false}\n"}},
		{"version-source", "xcconfig-value", "Config/Paid.xcconfig", "MARKETING_VERSION = 3.0.2\nSWIFT_VERSION = 6.0\n", []string{"", "MARKETING_VERSION = 3.0.1\nSWIFT_VERSION = 6.0\n", "MARKETING_VERSION = 3.0.2\nSWIFT_VERSION = 5.0\n"}},
		{"pbxproj-discipline", "verified-outcome", "result.json", "{\"passed\": true}\n", []string{"", "{\"passed\": false}\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("rules/evals", tc.name, "case.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var spec struct{ Graders []grader }
			if err := yaml.Unmarshal(data, &spec); err != nil {
				t.Fatal(err)
			}
			for _, g := range spec.Graders {
				if g.Name != tc.grader {
					continue
				}
				if g.Type != "regex" || g.Target.Source != "file" || g.Target.Path != tc.path {
					t.Fatalf("grader does not inspect the outcome file: %+v", g)
				}
				pattern, err := regexp.Compile(g.Pattern)
				if err != nil {
					t.Fatal(err)
				}
				if !pattern.MatchString(tc.good) {
					t.Error("rejected the passing control")
				}
				for _, bad := range tc.bad {
					if pattern.MatchString(bad) {
						t.Errorf("accepted known-bad outcome %q", bad)
					}
				}
				return
			}
			t.Fatalf("missing outcome grader %s", tc.grader)
		})
	}
}

func TestRuleEvalForbiddenMethods(t *testing.T) {
	for _, tc := range []struct {
		name, grader string
		bad, good    []string
	}{
		{"ci-wait", "no-gh-watch", []string{"gh pr checks 900001 --watch", "gh pr checks --watch --fail-fast 900001"}, []string{"lacquer wait pr 900001", "gh pr checks 900001"}},
		{"no-force-push", "no-force-or-rebase", []string{"git push --force", "git push --force-with-lease origin feature", "git push -f", "git -C . push -uf origin feature", "git push origin +HEAD:feature", "git rebase origin/main", "git -C . rebase main"}, []string{"git merge origin/main", "git push origin feature", "git fetch origin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("rules/evals", tc.name, "case.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var spec struct {
				Graders []grader `yaml:"graders"`
			}
			if err := yaml.Unmarshal(data, &spec); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, g := range spec.Graders {
				if g.Name != tc.grader {
					continue
				}
				found = true
				if g.Type != "tool_used" || g.Tool != "Bash" || g.Min == nil || *g.Min != 0 || g.Max == nil || *g.Max != 0 {
					t.Fatalf("not a zero-tolerance Bash grader: %+v", g)
				}
				pattern, err := regexp.Compile(g.Input)
				if err != nil {
					t.Fatal(err)
				}
				for _, command := range append(append([]string{}, tc.bad...), tc.good...) {
					input, _ := json.Marshal(map[string]string{"command": command})
					want := false
					for _, bad := range tc.bad {
						if command == bad {
							want = true
						}
					}
					if got := pattern.Match(input); got != want {
						t.Errorf("%q rejected=%v, want %v", command, got, want)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s", tc.grader)
			}
		})
	}
}

func TestRuleEvalFixtureControls(t *testing.T) {
	cmd := exec.Command("python3", "test_fixtures.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture controls: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Fatalf("no completed fixture tests: %s", out)
	}
	t.Log(string(out))
}
