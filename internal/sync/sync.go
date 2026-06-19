// Package sync applies harness core + profile CLAUDE.md content into a project.
package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/harness/internal/assets"
	"github.com/patrickserrano/harness/internal/audit"
	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/lock"
	"github.com/patrickserrano/harness/internal/region"
	"github.com/patrickserrano/harness/internal/safepath"
	"github.com/patrickserrano/harness/internal/tokens"
	"github.com/patrickserrano/harness/internal/version"
)

// Result summarizes what a sync wrote: the number of CLAUDE.md regions merged
// and the number of whole-file assets copied.
type Result struct {
	Regions, Assets int
}

// region is a CLAUDE.md region to write: destination rel path, marker key, body,
// and the {{COMPONENT_PREFIX}} value for that region's component ("" for core).
type regionWrite struct {
	rel, key, body, prefix string
}

// Run syncs core + each component's profiles into the project's CLAUDE.md files,
// then distributes whole-file assets, substituting per-project placeholders.
//
// A token preflight runs first: if any registered {{KEY}} appears in a region
// body or asset with no [project] value, Run aborts before writing anything
// (fail closed), so nothing ever lands half-tokenized.
//
// The asset copy phase is not atomic across files (a mid-copy I/O fault may leave
// some assets written); region/asset writes are otherwise guarded and idempotent,
// so a corrected re-run heals a partial sync.
func Run(harnessRoot, projectRoot string, force bool) (Result, error) {
	ver, err := version.Read(harnessRoot)
	if err != nil {
		return Result{}, fmt.Errorf("read version: %w", err)
	}
	cfg, err := config.Load(filepath.Join(projectRoot, ".harness.toml"))
	if err != nil {
		return Result{}, fmt.Errorf("load manifest: %w", err)
	}

	// Gather region bodies (core + each component profile) without writing yet.
	var regions []regionWrite
	coreBody, err := os.ReadFile(filepath.Join(harnessRoot, "core", "CLAUDE.core.md"))
	if err != nil {
		return Result{}, fmt.Errorf("read core body: %w", err)
	}
	regions = append(regions, regionWrite{"CLAUDE.md", "core", string(coreBody), ""})
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			body, err := os.ReadFile(filepath.Join(harnessRoot, "profiles", p, "CLAUDE."+p+".md"))
			if err != nil {
				return Result{}, fmt.Errorf("read profile %s body: %w", p, err)
			}
			regions = append(regions, regionWrite{filepath.Join(c.Path, "CLAUDE.md"), p, string(body), tokens.Prefix(c.Path)})
		}
	}

	// Mirror every CLAUDE.md region into a sibling AGENTS.md when a tool that reads
	// it (Codex, Google Antigravity) is enabled in [project].tools. AGENTS.md is
	// the cross-tool rules file; gating it on the same `tools` switch as skill
	// provisioning keeps the model coherent and leaves claude-only projects with
	// just CLAUDE.md. Identical key/body/prefix — only the destination filename
	// differs, so the token preflight and merge below handle it transparently.
	if cfg.Project.WantsAgentsMd() {
		mirror := make([]regionWrite, 0, len(regions))
		for _, r := range regions {
			m := r
			m.rel = filepath.Join(filepath.Dir(r.rel), "AGENTS.md")
			mirror = append(mirror, m)
		}
		regions = append(regions, mirror...)
	}

	plan, err := assets.Plan(harnessRoot, cfg)
	if err != nil {
		return Result{}, fmt.Errorf("plan assets: %w", err)
	}

	// Token preflight — fail closed before any write.
	var missing []string
	for _, r := range regions {
		if _, m := tokens.Substitute(r.body, tokens.Values(cfg.Project, r.prefix)); len(m) > 0 {
			for _, t := range m {
				missing = append(missing, fmt.Sprintf("%s (%s)", t, r.rel))
			}
		}
	}
	assetMissing, err := assets.MissingTokens(plan, cfg.Project)
	if err != nil {
		return Result{}, err
	}
	missing = append(missing, assetMissing...)
	if len(missing) > 0 {
		return Result{}, fmt.Errorf("missing [project] values for placeholders (add them to .harness.toml [project], then re-run):\n  %s",
			strings.Join(missing, "\n  "))
	}

	// Clobber guard: refuse to overwrite a managed unit the project has locally
	// changed (and the harness has not), unless forced. This is detectable only
	// with a .harness.lock baseline, so a project syncing for the first time is
	// never blocked — the lock bootstraps below. See internal/audit.
	if !force {
		rows, _, err := audit.Classify(harnessRoot, projectRoot)
		if err != nil {
			return Result{}, fmt.Errorf("audit before sync: %w", err)
		}
		if clob := audit.Clobbered(rows); len(clob) > 0 {
			return Result{}, fmt.Errorf("refusing to overwrite local changes the harness did not make — run `harness audit` to review, then either promote the change into the harness or re-sync with --force to take the harness version:\n  %s",
				strings.Join(clob, "\n  "))
		}
	}

	// Writes: substitute + merge region bodies.
	for _, r := range regions {
		body, _ := tokens.Substitute(r.body, tokens.Values(cfg.Project, r.prefix))
		if err := mergeInto(projectRoot, r.rel, r.key, ver, body); err != nil {
			return Result{}, err
		}
	}

	// Whole-file assets. Only run when the harness has assets, so a region-only
	// sync into a non-git directory still works (assets.Copy requires git).
	if len(plan) > 0 {
		if err := assets.Copy(projectRoot, plan, cfg.Project); err != nil {
			return Result{}, err
		}
	}

	// Record the baseline so the next audit/sync can tell a project's edits apart
	// from harness updates. Fail loud if it can't be written — a missing or stale
	// lock would silently disable the clobber guard.
	lk, err := audit.LockFor(harnessRoot, projectRoot)
	if err != nil {
		return Result{}, fmt.Errorf("compute lock: %w", err)
	}
	if err := lock.Write(projectRoot, lk); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", lock.Name, err)
	}

	return Result{Regions: len(regions), Assets: len(plan)}, nil
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
