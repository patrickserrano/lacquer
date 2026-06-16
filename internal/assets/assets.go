// Package assets enumerates the whole-file assets (skills, commands, CI
// workflows, stack configs) that sync copies from the harness into a project,
// applying the design's placement rules.
package assets

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/patrickserrano/harness/internal/config"
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

	// distinct profiles across all components
	profiles := map[string]bool{}
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			profiles[p] = true
		}
	}

	for p := range profiles {
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

	return out, nil
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
