package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	"gopkg.in/yaml.v3"
)

func TestActionlintRegionPreservesProjectConfig(t *testing.T) {
	for _, existing := range []string{"", "{}\n", "self-hosted-runner: {labels: [project-runner]} # runner comment\n", "# project comment", "# project comment\npaths:\n  .github/workflows/local.yml:\n    ignore: [shellcheck]\nconfig-variables: [DEPLOY_TARGET]\n", "# runner comment\nself-hosted-runner:\n  labels: [dedicated, project-runner] # keep this\n# paths comment\npaths: {}\nconfig-variables: [DEPLOY_TARGET]\n"} {
		t.Run(existing, func(t *testing.T) {
			lacquer, project := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(lacquer, "VERSION"), "1.0.0\n")
			writeFile(t, filepath.Join(lacquer, "core/CLAUDE.core.md"), "core")
			writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"demo\"\n")
			path := filepath.Join(project, ".github/actionlint.yaml")
			if existing != "" {
				writeFile(t, path, existing)
			}
			if _, err := Run(lacquer, project, false); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Runner struct {
					Labels []string `yaml:"labels"`
				} `yaml:"self-hosted-runner"`
				Paths map[string]any `yaml:"paths"`
				Vars  []string       `yaml:"config-variables"`
			}
			if err := yaml.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"dedicated", "pi-gate", "blacksmith-4vcpu-ubuntu-2404", "blacksmith-2vcpu-ubuntu-2404-arm"} {
				if !strings.Contains(strings.Join(got.Runner.Labels, "\n"), label) {
					t.Errorf("missing label %s: %s", label, data)
				}
			}
			for _, part := range []string{"# project comment", "# runner comment", "# keep this", "# paths comment", "project-runner", "DEPLOY_TARGET", "shellcheck"} {
				if strings.Contains(existing, part) && !strings.Contains(string(data), part) {
					t.Errorf("lost %q: %s", part, data)
				}
			}
			if _, err := Run(lacquer, project, false); err != nil {
				t.Fatal(err)
			}
			again, _ := os.ReadFile(path)
			if string(again) != string(data) {
				t.Fatal("second sync changed config")
			}
			rows, _, err := audit.Classify(lacquer, project)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.Dest == ".github/actionlint.yaml" {
					found = true
					if row.Status != audit.OK {
						t.Errorf("actionlint audit: %+v", row)
					}
				}
			}
			if !found {
				t.Fatal("audit does not track actionlint")
			}
		})
	}
}

func TestActionlintInvalidConfigFailsBeforeWrites(t *testing.T) {
	lacquer, project := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1.0.0\n")
	writeFile(t, filepath.Join(lacquer, "core/CLAUDE.core.md"), "core")
	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname='demo'\n")
	writeFile(t, filepath.Join(project, ".github/actionlint.yaml"), "self-hosted-runner:\n  labels: invalid\n")
	if _, err := Run(lacquer, project, false); err == nil {
		t.Fatal("accepted invalid actionlint config")
	}
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("wrote a region before refusing malformed config")
	}
}
