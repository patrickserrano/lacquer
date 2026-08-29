package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// renderWebCI substitutes profiles/web/workflows/ci.yml the way sync does.
func renderWebCI(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "web", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
		AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
	}}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	return out
}

// webCheckStep returns the `run:` body of one step in the `check` job, by step name.
func webCheckStep(t *testing.T, rendered, step string) string {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered workflow is not valid YAML: %v", err)
	}
	for _, st := range doc.Jobs["check"].Steps {
		if n, _ := st["name"].(string); n == step {
			run, _ := st["run"].(string)
			if run == "" {
				t.Fatalf("step %q has no run: block", step)
			}
			return run
		}
	}
	t.Fatalf("no step named %q in job check", step)
	return ""
}

// TestWebCIUsesLocalTurboBinaryWhenTurboJSONPresent is the one property this
// milestone adds: a component with turbo.json gets each task run through
// turbo, fanning out across every package in the workspace; a component
// without one keeps today's single-package pnpm invocation untouched.
//
// The local binary only — ./node_modules/.bin/turbo, never `pnpm dlx turbo` —
// for the same reason the Biome step below it uses ./node_modules/.bin/biome:
// see internal/shipped/shipped_test.go's resolver-dispatch ban.
func TestWebCIUsesLocalTurboBinaryWhenTurboJSONPresent(t *testing.T) {
	rendered := renderWebCI(t)
	cases := []struct {
		step     string
		turbo    string
		fallback string
	}{
		{"Lint", "./node_modules/.bin/turbo run lint", "./node_modules/.bin/biome ci --error-on-warnings ."},
		{"Type check", "./node_modules/.bin/turbo run typecheck", "pnpm run typecheck"},
		{"Test (coverage)", "./node_modules/.bin/turbo run test", "pnpm run test:coverage"},
		{"Build", "./node_modules/.bin/turbo run build", "pnpm run build"},
	}
	for _, c := range cases {
		t.Run(c.step, func(t *testing.T) {
			run := webCheckStep(t, rendered, c.step)
			if !strings.Contains(run, "if [ -f turbo.json ]") {
				t.Errorf("step %q does not branch on turbo.json:\n%s", c.step, run)
			}
			if !strings.Contains(run, c.turbo) {
				t.Errorf("step %q does not call %q on the turbo branch:\n%s", c.step, c.turbo, run)
			}
			if !strings.Contains(run, c.fallback) {
				t.Errorf("step %q does not keep %q on the fallback branch:\n%s", c.step, c.fallback, run)
			}
			if strings.Contains(run, "pnpm dlx turbo") || strings.Contains(run, "npx turbo") {
				t.Errorf("step %q dispatches turbo through a resolver instead of the local binary:\n%s", c.step, run)
			}
		})
	}
}
