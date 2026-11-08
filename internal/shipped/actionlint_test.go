package shipped

import (
	"fmt"
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
	"github.com/patrickserrano/lacquer/internal/tokens"
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
	cfg.Baseline.Relax = map[string]baseline.Relax{"lint": {Until: "2099-01-01", Reason: "existing lint debt"}, "format": {Until: "2099-02-01", Reason: "existing format debt"}}
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

// Cover every non-iOS shell variant, rather than trusting a shared-looking
// template comment to parse the same way in every profile.
func TestWebAndSupabaseActionlintWithShellcheck(t *testing.T) {
	for _, tool := range []string{"actionlint", "shellcheck"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required for rendered workflow coverage", tool)
		}
	}
	for _, profile := range []string{"web", "supabase"} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nested=%t", profile, nested), func(t *testing.T) {
				cfg := ciConfig()
				cfg.Root = t.TempDir()
				prefix := ""
				if nested {
					prefix = "component/"
					cfg.Project.BuildEnv = []string{"PUBLIC_API_URL", "SERVER_TOKEN"}
					if err := os.MkdirAll(filepath.Join(cfg.Root, "component"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(cfg.Root, "component/package.json"), []byte(`{"packageManager":"pnpm@10.0.0"}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				raw, err := os.ReadFile(filepath.Join(root(t), "profiles", profile, "workflows", "ci.yml"))
				if err != nil {
					t.Fatal(err)
				}
				rendered, missing := tokens.Substitute(string(raw), tokens.Values(cfg, prefix))
				if len(missing) != 0 {
					t.Fatal(missing)
				}
				if profile == "web" {
					want := "npm ci"
					if nested {
						want = "pnpm install --frozen-lockfile"
					}
					if !strings.Contains(rendered, want) {
						t.Fatalf("package-manager variant missing %q", want)
					}
					if nested && !strings.Contains(rendered, "PUBLIC_API_URL:") {
						t.Fatal("build environment variant was not rendered")
					}
				}
				dir := t.TempDir()
				workflow, configPath := filepath.Join(dir, "ci.yml"), filepath.Join(dir, "actionlint.yaml")
				if err := os.WriteFile(workflow, []byte(rendered), 0600); err != nil {
					t.Fatal(err)
				}
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
