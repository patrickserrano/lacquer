package assets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

func TestIOSReaderCallerConditionalInclude(t *testing.T) {
	for _, tc := range []struct {
		name, debt             string
		profiles               []string
		docs, ciExcluded, want bool
	}{
		{"default", "", []string{"ios"}, false, false, false},
		{"docs opt-in", "", []string{"ios"}, true, false, true},
		{"lint", "lint", []string{"ios"}, false, false, true},
		{"format", "format", []string{"ios"}, false, false, true},
		{"docs debt only", "documentation", []string{"ios"}, false, false, false},
		{"owned style caller", "lint", []string{"ios"}, false, true, true},
		{"web", "", []string{"web"}, false, false, true},
		{"database", "", []string{"supabase"}, false, false, true},
		{"marketing", "", []string{"marketing"}, false, false, true},
		{"mixed web", "", []string{"ios", "web"}, false, false, true},
		{"mixed marketing", "", []string{"ios", "marketing"}, false, false, true},
		{"profileless", "", nil, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "core/root/scripts/docs-relaxation.sh"), "reader\n")
			cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: tc.profiles}}, IOS: config.IOS{DocumentationHook: tc.docs}}
			if tc.debt != "" {
				cfg.Baseline.Relax = map[string]baseline.Relax{tc.debt: {Until: "2999-12-31", Reason: "existing debt"}}
			}
			if tc.ciExcluded {
				cfg.Project.Exclude = []config.Exclusion{{Path: ".github/workflows/ios-ci.yml", Reason: "project-owned workflow"}}
			}
			plan, err := Plan(root, cfg)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, a := range plan {
				found = found || a.Dest == "scripts/docs-relaxation.sh"
			}
			if found != tc.want {
				t.Fatalf("reader planned=%t, want %t", found, tc.want)
			}
		})
	}
}

func TestDocumentationHookRegion(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := &config.Config{IOS: config.IOS{DocumentationHook: enabled}}
		src := filepath.Join(t.TempDir(), ".pre-commit-config.yaml")
		body := "repos:\n  - repo: local\n    hooks:\n      # lacquer:documentation-hook\n"
		if err := os.WriteFile(src, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		got, _, err := Render(Asset{Src: src, Dest: ".pre-commit-config.yaml", Prefix: "ios/"}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !enabled && string(got) != body {
			t.Fatalf("default bytes changed: %q", got)
		}
		var doc struct {
			Repos []struct {
				Hooks []struct{ ID, Entry, Files string }
			}
		}
		if err := yaml.Unmarshal(got, &doc); err != nil {
			t.Fatal(err)
		}
		want := 0
		if enabled {
			want = 1
		}
		if len(doc.Repos) != 1 || len(doc.Repos[0].Hooks) != want {
			t.Fatalf("wrong hook count: %+v", doc)
		}
		if enabled {
			h := doc.Repos[0].Hooks[0]
			if h.ID != "swiftlint-docs" || h.Entry != "scripts/docs-hook.sh scripts/precommit-swift.sh swiftlint-docs" || h.Files != "^ios/" {
				t.Fatalf("wrong opt-in hook: %+v", h)
			}
		}
	}
}
func TestDocumentationHookMalformedRegion(t *testing.T) {
	src := filepath.Join(t.TempDir(), ".pre-commit-config.yaml")
	if err := os.WriteFile(src, []byte("      # lacquer:documentation-hook\n      # lacquer:documentation-hook\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Render(Asset{Src: src, Dest: ".pre-commit-config.yaml"}, &config.Config{IOS: config.IOS{DocumentationHook: true}}); err == nil {
		t.Fatal("duplicate insertion marker accepted")
	}
}
