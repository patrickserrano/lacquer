// Command render refreshes the eval plugin from a real iOS-profile sync.
package main

import (
	"fmt"
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
	dir, err := os.MkdirTemp(scratch, "render-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// Prevent a non-Git fixture from discovering this repository's worktree.
	if err := os.Setenv("GIT_CEILING_DIRECTORIES", scratch); err != nil {
		return err
	}
	cmd := exec.Command("git", "-c", "init.templateDir=", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize render fixture: %w: %s", err, out)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "internal/shipped/testdata/projects/rootapp/.lacquer.toml"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, ".lacquer.toml"), manifest, 0o644); err != nil {
		return err
	}
	if _, err := sync.Run(root, dir, false); err != nil {
		return err
	}
	rendered, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		return err
	}
	var context string
	for _, name := range []string{"core", "ios"} {
		body, ok := region.ExtractBody(string(rendered), name)
		if !ok {
			return fmt.Errorf("rendered CLAUDE.md lacks %s region", name)
		}
		context += body + "\n"
	}
	context = strings.TrimRight(context, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "evals/rules/rules.md"), []byte(context), 0o644); err != nil {
		return err
	}
	helper, err := os.ReadFile(filepath.Join(dir, "scripts/bump-marketing-version.sh"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "evals/rules/fixtures/bump-marketing-version.sh"), helper, 0o755)
}
