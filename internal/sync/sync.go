// Package sync applies harness core + profile CLAUDE.md content into a project.
package sync

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/patrickserrano/harness/internal/assets"
	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/region"
	"github.com/patrickserrano/harness/internal/safepath"
	"github.com/patrickserrano/harness/internal/version"
)

// Run syncs core + each component's profiles into the project's CLAUDE.md files.
//
// Run is not atomic across files: if it fails partway (e.g. a component names a
// profile that has no CLAUDE.<profile>.md in the harness — a deliberate fail-loud
// choice that surfaces manifest typos), files written before the failure stay
// written. This is recoverable, not corrupting: each per-file write is itself
// safe (region.Merge preserves project-owned text and is idempotent), so a
// corrected re-run heals a partial sync. The uncommitted-changes git guard in a
// later plan will tighten this.
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
	if err := mergeInto(projectRoot, "CLAUDE.md", "core", ver, string(coreBody)); err != nil {
		return err
	}

	// each profile -> <component>/CLAUDE.md
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			body, err := os.ReadFile(filepath.Join(harnessRoot, "profiles", p, "CLAUDE."+p+".md"))
			if err != nil {
				return fmt.Errorf("read profile %s body: %w", p, err)
			}
			rel := filepath.Join(c.Path, "CLAUDE.md")
			if err := mergeInto(projectRoot, rel, p, ver, string(body)); err != nil {
				return err
			}
		}
	}

	// Phase 2: whole-file assets (skills, commands, workflows, configs).
	// Only run when the harness actually has assets to distribute, so a
	// region-only sync into a non-git directory still works (assets.Copy
	// requires a git work tree to guard against clobbering uncommitted work).
	plan, err := assets.Plan(harnessRoot, cfg)
	if err != nil {
		return fmt.Errorf("plan assets: %w", err)
	}
	if len(plan) > 0 {
		if err := assets.Copy(projectRoot, plan); err != nil {
			return err
		}
	}

	return nil
}

// mergeInto resolves rel under projectRoot (confining it within the root even
// against symlinked directories), reads the target (a missing file is treated as
// empty), merges the managed region, and writes it back, creating parent
// directories as needed.
func mergeInto(projectRoot, rel, key string, ver int, body string) error {
	target, err := safepath.Resolve(projectRoot, rel)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", rel, err)
	}
	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", target, err)
	}
	merged, err := region.Merge(string(existing), key, ver, body)
	if err != nil {
		return fmt.Errorf("merge %s region in %s: %w", key, target, err)
	}
	// Refuse to write through a symlink: os.WriteFile would follow it and clobber
	// whatever it points at, potentially outside the project root.
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through symlink: %s", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(merged), 0o644)
}
