package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/actionlint"
	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/version"
	"gopkg.in/yaml.v3"
)

// Keep ShellCheck enabled: workflow schema lint alone misses invalid directive
// comments and the redirection patterns in embedded shell scripts.
func TestIOSWorkflowActionlintWithShellcheck(t *testing.T) {
	for _, tool := range []string{"actionlint", "shellcheck"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed (CI requires both tools)", tool)
		}
	}
	cfg := watchProject()
	if _, err := toml.Decode("[ios]\npackage_resolved = 'ci_scripts/Package.resolved'\n", cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Baseline.Relax = map[string]baseline.Relax{"lint": {Until: "2099-01-01", Reason: "existing lint debt"}}
	for name, c := range map[string]*config.Config{"defaults": soloConfig(), "products": twoIOSProducts(), "options": cfg} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			workflow := filepath.Join(dir, "ci.yml")
			if err := os.WriteFile(workflow, []byte(renderIOSCI(t, c)), 0600); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "actionlint.yaml")
			body, err := actionlint.Merge("", version.Version{Major: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("actionlint", "-config-file", configPath, "-pyflakes=", workflow)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("actionlint with ShellCheck: %v\n%s", err, out)
			}
		})
	}
}

// Compare actual runs-on values in synced workflows with the production list,
// not another handwritten test config. A new workflow label must fail here.
func TestActionlintLabelsEqualRenderedWorkflowLabels(t *testing.T) {
	p := fromFixture(t, "multistack")
	p.sync()
	files, err := filepath.Glob(filepath.Join(p.root, ".github/workflows/*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflows checked")
	}
	builtin := map[string]bool{"self-hosted": true, "linux": true, "macos": true, "arm64": true, "x64": true, "windows": true}
	have := map[string]bool{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatal(err)
		}
		for _, job := range workflow.Jobs {
			var labels []string
			switch runs := job.RunsOn.(type) {
			case string:
				// A hosted job's runs-on is the switch expression; the label
				// actionlint must know is its literal fallback.
				if err := actionlint.CheckHostedRunsOn(runs); err == nil {
					runs = actionlint.HostedDefault
				}
				labels = []string{runs}
			case []any:
				for _, v := range runs {
					s, ok := v.(string)
					if !ok {
						t.Fatalf("non-string runs-on in %s", file)
					}
					labels = append(labels, s)
				}
			default:
				t.Fatalf("unhandled runs-on in %s: %#v", file, runs)
			}
			for _, label := range labels {
				if !builtin[strings.ToLower(label)] {
					have[label] = true
				}
			}
		}
	}
	var got []string
	for label := range have {
		got = append(got, label)
	}
	sort.Strings(got)
	want := strings.Split(actionlint.Labels, "\n")
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow custom labels=%q; managed labels=%q", got, want)
	}
	if err := actionlint.Check([]byte(p.read(actionlint.Name))); err != nil {
		t.Fatal(err)
	}
}
