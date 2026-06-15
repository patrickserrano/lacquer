// Package sync applies harness core + profile CLAUDE.md content into a project.
package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/region"
	"github.com/patrickserrano/harness/internal/version"
)

// Run syncs core + each component's profiles into the project's CLAUDE.md files.
func Run(harnessRoot, projectRoot string) error {
	ver, err := version.Read(harnessRoot)
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	cfg, err := config.Load(filepath.Join(projectRoot, ".harness.toml"))
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	// core -> project-root CLAUDE.md
	coreBody, err := os.ReadFile(filepath.Join(harnessRoot, "core", "CLAUDE.core.md"))
	if err != nil {
		return fmt.Errorf("read core body: %w", err)
	}
	if err := mergeInto(filepath.Join(projectRoot, "CLAUDE.md"), "core", ver, string(coreBody)); err != nil {
		return err
	}

	// each profile -> <component>/CLAUDE.md
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			body, err := os.ReadFile(filepath.Join(harnessRoot, "profiles", p, "CLAUDE."+p+".md"))
			if err != nil {
				return fmt.Errorf("read profile %s body: %w", p, err)
			}
			target := filepath.Join(projectRoot, c.Path, "CLAUDE.md")
			if err := mergeInto(target, p, ver, string(body)); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeInto reads target (treating a missing file as empty), merges the managed
// region, and writes it back, creating parent directories as needed.
func mergeInto(target, key string, ver int, body string) error {
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", target, err)
	}
	merged, err := region.Merge(string(existing), key, ver, body)
	if err != nil {
		return fmt.Errorf("merge %s region in %s: %w", key, target, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(merged), 0o644)
}
