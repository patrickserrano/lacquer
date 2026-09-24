// Command render refreshes the eval plugin from real per-profile syncs.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/region"
	"github.com/patrickserrano/lacquer/internal/sync"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	// Keep all generated files below this worktree, including when TMPDIR is unset.
	scratch := filepath.Join(root, "testdata/.rule-eval-work")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return err
	}
	// Prevent a non-Git fixture from discovering this repository's worktree.
	if err := os.Setenv("GIT_CEILING_DIRECTORIES", scratch); err != nil {
		return err
	}
	for _, tc := range []struct{ profile, fixture, component string }{
		{"core", "spmpackage", "."},
		{"ios", "rootapp", "."},
		{"web", "multistack", "admin"},
		{"supabase", "multistack", "server"},
		{"marketing", "marketing", "."},
	} {
		if err := render(root, scratch, tc.profile, tc.fixture, tc.component); err != nil {
			return err
		}
	}
	return nil
}

func render(root, scratch, profile, fixture, component string) error {
	dir, err := os.MkdirTemp(scratch, "render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command("git", "-c", "init.templateDir=", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize render fixture: %w: %s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "internal/shipped/testdata/projects", fixture, ".lacquer.toml"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), manifest, 0o644); err != nil {
		return err
	}
	if _, err := sync.Run(root, dir, false); err != nil {
		return err
	}
	destination := filepath.Join(root, "evals/rules/contexts", profile)
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	for _, file := range []string{"CLAUDE.md", "AGENTS.md"} {
		context, err := regionBody(dir, file, "core")
		if err != nil {
			return err
		}
		if profile != "core" {
			body, err := regionBody(dir, filepath.Join(component, file), profile)
			if err != nil {
				return err
			}
			context += body
		}
		context = strings.TrimRight(context, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(destination, file), []byte(context), 0o644); err != nil {
			return err
		}
	}
	if profile == "ios" {
		helper, err := os.ReadFile(filepath.Join(dir, "scripts/bump-marketing-version.sh"))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "evals/rules/fixtures/bump-marketing-version.sh"), helper, 0o755); err != nil {
			return err
		}
	}
	// Skills are root-scoped even for nested components. Copy the selected
	// procedures and marketing distractors byte-for-byte from this same sync.
	selected := map[string][]string{
		"core":      {"engineering-workflow", "project-documentation", "working-with-lacquer", "github-ci-fix"},
		"ios":       {"ios-project-development", "ios-ci-configuration", "ios-build-verification", "ios-release-guide", "ios-secrets-setup", "ios-ui-verification"},
		"web":       {"web-development-guide"},
		"supabase":  {"supabase-development-guide"},
		"marketing": {"product-marketing", "marketing-ideas", "marketing-council", "copywriting"},
	}
	if profile == "marketing" {
		name := "LICENSE-upstream-marketingskills"
		data, err := os.ReadFile(filepath.Join(dir, ".claude/skills", name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "evals/rules/skills", name), data, 0o644); err != nil {
			return err
		}
	}
	for _, name := range selected[profile] {
		source := filepath.Join(dir, ".claude/skills", name)
		dest := filepath.Join(root, "evals/rules/skills", name)
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return os.MkdirAll(filepath.Join(dest, rel), 0o755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dest, rel), data, info.Mode().Perm())
		}); err != nil {
			return err
		}
	}
	return nil
}

func regionBody(dir, file, name string) (string, error) {
	rendered, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "", err
	}
	body, ok := region.ExtractBody(string(rendered), name)
	if !ok {
		return "", fmt.Errorf("rendered %s lacks %s region", file, name)
	}
	return body + "\n", nil
}
