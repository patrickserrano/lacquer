// Package assets enumerates the whole-file assets (skills, commands, CI
// workflows, stack configs) that sync copies from the harness into a project,
// applying the design's placement rules.
package assets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/gitguard"
	"github.com/patrickserrano/harness/internal/safepath"
)

// Asset is one file to copy: an absolute source path and a project-relative
// destination path.
type Asset struct {
	Src  string
	Dest string
}

// Plan returns every asset to copy for core plus the profiles named by the
// project's components. Skills/commands/workflows are root-scoped (deduped by
// destination across profiles); config is copied into each component that lists
// the owning profile.
//
// On a destination collision the first writer wins: core is walked before
// profiles, so a core skill/command takes precedence over a same-named profile
// one. Profiles are visited in sorted order and the returned slice is sorted by
// Dest, so the output (and the winning Src on any same-named profile collision)
// is deterministic.
func Plan(harnessRoot string, cfg *config.Config) ([]Asset, error) {
	var out []Asset
	seen := map[string]bool{}

	add := func(src, dest string) {
		if seen[dest] {
			return
		}
		seen[dest] = true
		out = append(out, Asset{Src: src, Dest: dest})
	}

	// core: skills + commands -> root .claude/
	for _, kind := range []string{"skills", "commands"} {
		if err := walkInto(filepath.Join(harnessRoot, "core", kind),
			func(src, rel string) { add(src, filepath.Join(".claude", kind, rel)) }); err != nil {
			return nil, err
		}
	}

	// core: root tree -> project root (verbatim relative paths)
	if err := walkInto(filepath.Join(harnessRoot, "core", "root"),
		func(src, rel string) { add(src, rel) }); err != nil {
		return nil, err
	}

	// distinct profiles across all components, sorted for deterministic output
	profileSet := map[string]bool{}
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			profileSet[p] = true
		}
	}
	profiles := make([]string, 0, len(profileSet))
	for p := range profileSet {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)

	for _, p := range profiles {
		base := filepath.Join(harnessRoot, "profiles", p)
		for _, kind := range []string{"skills", "commands"} {
			if err := walkInto(filepath.Join(base, kind),
				func(src, rel string) { add(src, filepath.Join(".claude", kind, rel)) }); err != nil {
				return nil, err
			}
		}
		// workflows -> .github/workflows/<p>-<file> (stack-prefixed; flat)
		if err := walkInto(filepath.Join(base, "workflows"),
			func(src, rel string) {
				add(src, filepath.Join(".github", "workflows", p+"-"+filepath.Base(rel)))
			}); err != nil {
			return nil, err
		}
		// profile root tree -> project root (verbatim relative paths)
		if err := walkInto(filepath.Join(base, "root"),
			func(src, rel string) { add(src, rel) }); err != nil {
			return nil, err
		}
	}

	// config -> each component dir that lists the owning profile
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			if err := walkInto(filepath.Join(harnessRoot, "profiles", p, "config"),
				func(src, rel string) { add(src, filepath.Join(c.Path, rel)) }); err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Dest < out[j].Dest })
	return out, nil
}

// Copy distributes assets into projectRoot. It first requires projectRoot to be
// a git work tree (the dirty-guard is meaningless without git, so a non-git
// project is refused outright — fail closed). It then runs an all-or-nothing
// preflight over EVERY asset — path confinement within projectRoot, the
// final-element symlink guard, and the uncommitted-changes check — and aborts
// before writing anything if any asset fails. Only after the whole preflight
// passes does it copy.
//
// The copy phase itself is not atomic across files: if an I/O error (read,
// mkdir, write) occurs partway, the assets already written stay written (a
// re-run completes the rest). The deterministic safety checks are fully
// preflighted, so a confinement/symlink/dirty violation never causes a partial
// write — only a genuine mid-copy I/O fault can.
func Copy(projectRoot string, plan []Asset) error {
	inRepo, err := gitguard.InWorkTree(projectRoot)
	if err != nil {
		return fmt.Errorf("git check: %w", err)
	}
	if !inRepo {
		return fmt.Errorf("refusing asset sync: %s is not a git repository (git is required to guard against overwriting uncommitted work)", projectRoot)
	}

	// Preflight: validate every asset before writing any. Resolved targets are
	// cached for the write phase so confinement is decided exactly once.
	targets := make([]string, len(plan))
	var dirty []string
	for i, a := range plan {
		target, err := safepath.Resolve(projectRoot, a.Dest)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", a.Dest, err)
		}
		if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", a.Dest)
		}
		targets[i] = target

		isDirty, err := gitguard.Dirty(projectRoot, a.Dest)
		if err != nil {
			return fmt.Errorf("git guard %s: %w", a.Dest, err)
		}
		if isDirty {
			dirty = append(dirty, a.Dest)
		}
	}
	if len(dirty) > 0 {
		return fmt.Errorf("refusing to overwrite uncommitted changes in:\n  %s\n(commit or stash them, then re-run)",
			strings.Join(dirty, "\n  "))
	}

	for i, a := range plan {
		target := targets[i]
		data, err := os.ReadFile(a.Src)
		if err != nil {
			return fmt.Errorf("read asset %s: %w", a.Src, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// walkInto calls fn(absSrc, relPath) for every file under dir. A missing dir is
// not an error (a profile need not define every asset kind).
func walkInto(dir string, fn func(src, rel string)) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		fn(abs, rel)
		return nil
	})
}
