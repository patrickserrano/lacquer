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
	"github.com/patrickserrano/harness/internal/tokens"
)

// MissingTokens returns "<token> (<dest>)" for every registered placeholder that
// appears in an asset's source with no project value. Used by sync's fail-closed
// preflight before any write.
func MissingTokens(plan []Asset, proj config.Project) ([]string, error) {
	var out []string
	for _, a := range plan {
		data, err := os.ReadFile(a.Src)
		if err != nil {
			return nil, fmt.Errorf("read asset %s: %w", a.Src, err)
		}
		if _, missing := tokens.Substitute(string(data), tokens.Values(proj, a.Prefix)); len(missing) > 0 {
			for _, m := range missing {
				out = append(out, fmt.Sprintf("%s (%s)", m, a.Dest))
			}
		}
	}
	return out, nil
}

// toolSkillsDir maps a configured agent tool to its project-level skills
// directory. SKILL.md is an open standard shared across Claude Code, Codex,
// Antigravity, Cursor, and Gemini CLI — only the directory differs — so the same
// skill package is copied verbatim into each enabled tool's dir. Commands are NOT
// fanned out: each tool's prompt/command mechanism differs, so commands stay
// Claude-only (.claude/commands). Custom subagent definitions (agents) are the
// same story as commands: there is no cross-tool standard for them, so they
// stay Claude-only too (.claude/agents).
var toolSkillsDir = map[string]string{
	"claude":      ".claude/skills",
	"codex":       ".codex/skills",
	"antigravity": ".agents/skills",
}

// Asset is one file to copy: an absolute source path and a project-relative
// destination path.
type Asset struct {
	Src  string
	Dest string
	// Prefix is the {{COMPONENT_PREFIX}} value for this asset's profile (the
	// owning component's path as a prefix: "" for root, "ios/" for a subdir).
	// Core assets have an empty prefix.
	Prefix string
}

// Plan returns every asset to copy for core plus the profiles named by the
// project's components. Skills/commands/agents/workflows are root-scoped
// (deduped by destination across profiles); config is copied into each
// component that lists the owning profile.
//
// On a destination collision the first writer wins: core is walked before
// profiles, so a core skill/command takes precedence over a same-named profile
// one. Profiles are visited in sorted order and the returned slice is sorted by
// Dest, so the output (and the winning Src on any same-named profile collision)
// is deterministic.
func Plan(harnessRoot string, cfg *config.Config) ([]Asset, error) {
	var out []Asset
	seen := map[string]bool{}

	add := func(src, dest, prefix string) {
		if seen[dest] {
			return
		}
		seen[dest] = true
		// Project-declared exclusions stay project-owned: the harness neither
		// distributes nor (via audit) tracks them. Used to keep a project's
		// hand-tuned CI/config local while still adopting the rest of the harness.
		if cfg.Project.Excludes(dest) {
			return
		}
		out = append(out, Asset{Src: src, Dest: dest, Prefix: prefix})
	}

	tools := cfg.Project.EffectiveTools()

	// core assets are stack-agnostic: no component prefix. Skills fan out to each
	// enabled tool's skills dir; commands stay Claude-only.
	for _, tool := range tools {
		dir, ok := toolSkillsDir[tool]
		if !ok {
			// Defense in depth: config.Load allowlists tool names, and every known
			// tool has a dir here. Fail loud rather than write skills to the project
			// root if the two ever drift.
			return nil, fmt.Errorf("no skills directory mapped for tool %q", tool)
		}
		if err := walkInto(filepath.Join(harnessRoot, "core", "skills"),
			func(src, rel string) { add(src, filepath.Join(dir, rel), "") }); err != nil {
			return nil, err
		}
	}
	if err := walkInto(filepath.Join(harnessRoot, "core", "commands"),
		func(src, rel string) { add(src, filepath.Join(".claude", "commands", rel), "") }); err != nil {
		return nil, err
	}
	if err := walkInto(filepath.Join(harnessRoot, "core", "agents"),
		func(src, rel string) { add(src, filepath.Join(".claude", "agents", rel), "") }); err != nil {
		return nil, err
	}
	if err := walkInto(filepath.Join(harnessRoot, "core", "root"),
		func(src, rel string) { add(src, rel, "") }); err != nil {
		return nil, err
	}

	// profile -> owning component path (config guarantees one component per profile).
	profileDir := map[string]string{}
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			profileDir[p] = c.Path
		}
	}
	profiles := make([]string, 0, len(profileDir))
	for p := range profileDir {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)

	for _, p := range profiles {
		base := filepath.Join(harnessRoot, "profiles", p)
		prefix := tokens.Prefix(profileDir[p])
		for _, tool := range tools {
			dir, ok := toolSkillsDir[tool]
			if !ok {
				return nil, fmt.Errorf("no skills directory mapped for tool %q", tool)
			}
			if err := walkInto(filepath.Join(base, "skills"),
				func(src, rel string) { add(src, filepath.Join(dir, rel), prefix) }); err != nil {
				return nil, err
			}
		}
		if err := walkInto(filepath.Join(base, "commands"),
			func(src, rel string) { add(src, filepath.Join(".claude", "commands", rel), prefix) }); err != nil {
			return nil, err
		}
		if err := walkInto(filepath.Join(base, "agents"),
			func(src, rel string) { add(src, filepath.Join(".claude", "agents", rel), prefix) }); err != nil {
			return nil, err
		}
		// workflows -> .github/workflows/<p>-<file> (stack-prefixed; flat)
		if err := walkInto(filepath.Join(base, "workflows"),
			func(src, rel string) {
				add(src, filepath.Join(".github", "workflows", p+"-"+filepath.Base(rel)), prefix)
			}); err != nil {
			return nil, err
		}
		// profile root tree -> project root (verbatim relative paths)
		if err := walkInto(filepath.Join(base, "root"),
			func(src, rel string) { add(src, rel, prefix) }); err != nil {
			return nil, err
		}
	}

	// config -> each component dir that lists the owning profile
	for _, c := range cfg.Components {
		prefix := tokens.Prefix(c.Path)
		for _, p := range c.Profiles {
			if err := walkInto(filepath.Join(harnessRoot, "profiles", p, "config"),
				func(src, rel string) { add(src, filepath.Join(c.Path, rel), prefix) }); err != nil {
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
func Copy(projectRoot string, plan []Asset, proj config.Project) error {
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
		// Substitute per-project placeholders + this asset's component prefix. Any
		// missing value should already have been caught by sync's preflight;
		// substitute regardless (leaves an unresolved token rather than corrupting).
		substituted, _ := tokens.Substitute(string(data), tokens.Values(proj, a.Prefix))
		data = []byte(substituted)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// Preserve the source's executable bit so synced scripts stay runnable.
		sourceExec := false
		if info, err := os.Stat(a.Src); err == nil && info.Mode()&0o100 != 0 {
			sourceExec = true
		}
		mode := os.FileMode(0o644)
		if sourceExec {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return err
		}
		// WriteFile only applies mode on create. Only an executable source needs a
		// follow-up chmod (to restore the exec bit when overwriting an existing
		// non-exec file). Non-exec files are left alone so the user's umask is
		// respected and we avoid spurious chmod EPERM on shared mounts.
		if sourceExec {
			if fi, err := os.Stat(target); err == nil && fi.Mode()&0o100 == 0 {
				if err := os.Chmod(target, mode); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// isCruft reports whether a filename is build/tool junk that must never be
// distributed into a project (compiled bytecode, OS metadata).
func isCruft(name string) bool {
	return name == ".DS_Store" || strings.HasSuffix(name, ".pyc")
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
			// Never distribute build/tool cruft that may sit on the harness disk
			// (e.g. a stray __pycache__ from running a synced script during dev).
			// walkInto walks the filesystem, not git, so .gitignore won't stop it.
			if d.Name() == "__pycache__" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if isCruft(d.Name()) {
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
