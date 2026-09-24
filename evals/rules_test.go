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

// Controls are keyed by case and grader. Iterate the authored cases so adding
// a grader without its positive AND negative proof is a failing repository test.
func TestRuleEvalEveryNewGrader(t *testing.T) {
	data, err := os.ReadFile("grader_controls.json")
	if err != nil {
		t.Fatal(err)
	}
	var controls map[string]map[string]struct{ Good, Bad string }
	if err := json.Unmarshal(data, &controls); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("rules/evals/*/case.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) > 32 {
		t.Fatalf("%d cases exceeds budget", len(paths))
	}
	old := map[string]bool{"ci-wait": true, "no-force-push": true, "version-source": true, "pbxproj-discipline": true}
	for _, path := range paths {
		name := filepath.Base(filepath.Dir(path))
		if old[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var spec struct{ Graders []grader }
			if err := yaml.Unmarshal(data, &spec); err != nil {
				t.Fatal(err)
			}
			if len(controls[name]) != len(spec.Graders) {
				t.Fatal("grader control inventory differs")
			}
			prompt, err := os.ReadFile(filepath.Join(filepath.Dir(path), "prompt.md"))
			if err != nil {
				t.Fatal(err)
			}
			var limits struct {
				Turns   int `yaml:"max_turns"`
				Timeout int `yaml:"timeout_seconds"`
			}
			parts := strings.SplitN(string(prompt), "---", 3)
			if len(parts) != 3 {
				t.Fatal("missing prompt limits")
			}
			if err := yaml.Unmarshal([]byte(parts[1]), &limits); err != nil {
				t.Fatal(err)
			}
			if limits.Turns < 1 || limits.Turns > 12 || limits.Timeout < 1 || limits.Timeout > 180 {
				t.Fatal("invalid budget limits")
			}
			for _, g := range spec.Graders {
				t.Run(g.Name, func(t *testing.T) {
					c, ok := controls[name][g.Name]
					if !ok || c.Good == "" || c.Bad == "" {
						t.Fatal("missing positive/negative controls")
					}
					pattern := g.Pattern
					if g.Type == "tool_used" {
						pattern = g.Input
					} else if g.Type != "regex" {
						t.Fatalf("unproved grader type %s", g.Type)
					}
					re, err := regexp.Compile(pattern)
					if err != nil {
						t.Fatal(err)
					}
					for _, input := range []struct {
						value string
						want  bool
					}{{c.Good, true}, {c.Bad, false}} {
						value := input.value
						if g.Type == "tool_used" {
							key := "command"
							if g.Tool == "Skill" {
								key = "skill"
							} else if g.Tool != "Bash" {
								t.Fatal("unsupported tool")
							}
							b, _ := json.Marshal(map[string]string{key: value})
							value = string(b)
						}
						passed := re.MatchString(value)
						if g.Max != nil && *g.Max == 0 {
							passed = !passed
						}
						if passed != input.want {
							t.Errorf("%q passed=%v want %v", value, passed, input.want)
						}
					}
				})
			}
		})
	}
}

// These graders inspect serialized Bash inputs, not arbitrary occurrences of
// "test". Use the explicit path because Bash's builtin shadows the fixture.
func TestRuleEvalFixtureTestInvocation(t *testing.T) {
	for _, name := range []string{"negative-control", "report-evidence"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("rules/evals", name, "case.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var spec struct{ Graders []grader }
			if err := yaml.Unmarshal(data, &spec); err != nil {
				t.Fatal(err)
			}
			for _, g := range spec.Graders {
				if g.Name != "method" {
					continue
				}
				pattern := regexp.MustCompile(g.Input)
				for _, tc := range []struct {
					command string
					want    bool
				}{
					{"bin/test", true},
					{"./bin/test", true},
					{`PATH="$PWD/bin:$PATH" ./bin/test`, true},
					{"pwd && bin/test", true},
					{"pwd; ./bin/test", true},
					{"pwd\nbin/test", true},
					{"bin/test > results.txt", true},
					{"echo test", false},
					{"go test ./...", false},
					{"test -f calc.py", false},
					{`PATH="$PWD/bin:$PATH" test -f calc.py`, false},
					{"echo bin/test", false},
					{"cat bin/test", false},
					{"./bin/test-helper", false},
					{"python3 verify.py", false},
				} {
					input, err := json.Marshal(map[string]string{"command": tc.command})
					if err != nil {
						t.Fatal(err)
					}
					if got := pattern.Match(input); got != tc.want {
						t.Errorf("%q matched=%v want %v", tc.command, got, tc.want)
					}
				}
				return
			}
			t.Fatal("missing method grader")
		})
	}
}

func TestRuleEvalInventoryQuotes(t *testing.T) {
	data, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	inventory := strings.ReplaceAll(string(data), `\|`, `|`)
	var sources string
	for _, profile := range []string{"core", "ios", "web", "supabase", "marketing"} {
		for _, file := range []string{"CLAUDE.md", "AGENTS.md"} {
			data, err := os.ReadFile(filepath.Join("rules/contexts", profile, file))
			if err != nil {
				t.Fatal(err)
			}
			sources += " " + strings.Join(strings.Fields(string(data)), " ")
			if file != "CLAUDE.md" || profile == "marketing" {
				continue
			}
			// Only inline rules; the on-demand procedure catalog is routing metadata.
			section := strings.SplitN(string(data), "## On-demand procedures", 2)[0]
			if profile != "core" {
				marker := "# "
				offset := strings.LastIndex(string(data), "\n"+marker)
				if offset < 0 {
					t.Fatal("profile heading absent")
				}
				section = strings.SplitN(string(data)[offset:], "## On-demand procedures", 2)[0]
			}
			bullets := regexp.MustCompile(`(?m)^- .*\n(?:  .*\n)*`).FindAllString(section, -1)
			for _, bullet := range bullets {
				quote := strings.Join(strings.Fields(strings.TrimPrefix(bullet, "- ")), " ")
				if !strings.Contains(inventory, "“"+quote+"”") {
					t.Errorf("missing rendered rule: %s", quote)
				}
			}
		}
	}
	for _, match := range regexp.MustCompile(`— “([^”]+)” \|`).FindAllStringSubmatch(inventory, -1) {
		if !strings.Contains(sources, match[1]) {
			t.Errorf("quote not in rendered inventory: %s", match[1])
		}
	}
}
