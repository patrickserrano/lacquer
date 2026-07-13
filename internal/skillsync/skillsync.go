// Package skillsync installs a project's [project].skills entries by
// shelling out to the `skills` CLI (https://github.com/vercel-labs/skills),
// project-scoped. It does not reimplement that tool's package-management
// logic (source resolution, multi-agent symlinking, its own lockfile) — it
// only drives `skills add` from lacquer's own manifest and reports drift
// against the resulting skills-lock.json.
package skillsync

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
)

// Runner executes one `skills` CLI invocation in dir and returns its combined
// output. Injectable for tests — the real implementation shells to `npx`,
// which needs a network connection and Node.js, neither available in a unit
// test sandbox.
var Runner = func(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("npx", append([]string{"--yes", "skills@latest"}, args...)...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// Result summarizes one Install call.
type Result struct {
	Installed  []string          // skill names successfully installed (or already present)
	Failed     map[string]string // skill name -> error output, for entries that failed
	Undeclared []string          // skill names present in skills-lock.json but not in [project].skills
}

// Install ensures every entry is installed at project scope (`skills add
// <source> -s <name> -p -y`, idempotent — an already-installed skill is a
// no-op). It keeps going after an individual failure so one bad entry
// doesn't block the rest, and returns every failure for the caller to report.
// After installing, it reads the project's skills-lock.json (written by the
// `skills` CLI) and flags any installed skill the manifest no longer
// declares — informational only; nothing is auto-removed.
//
// tools is the project's EffectiveTools() list. `skills add` only writes the
// canonical .agents/skills/<name> plus a Claude Code symlink — it does not
// reach every tool a project may declare. Codex is the confirmed gap:
// openai/codex's own repository dogfoods .codex/skills as its real
// project-level skill directory, but `skills add` never writes there, even
// with an explicit --agent codex flag. Install bridges that gap with a
// symlink for every declared tool `skills add` doesn't already cover.
func Install(projectRoot string, entries []config.SkillEntry, tools []string) (Result, error) {
	res := Result{Failed: map[string]string{}}
	declared := map[string]bool{}

	for _, e := range entries {
		declared[e.Name] = true
		out, err := Runner(projectRoot, "add", e.Source, "-s", e.Name, "-p", "-y")
		if err != nil {
			res.Failed[e.Name] = fmt.Sprintf("%v\n%s", err, out)
			continue
		}
		if err := bridgeToolDirs(projectRoot, e.Name, tools); err != nil {
			res.Failed[e.Name] = fmt.Sprintf("installed but failed to bridge to declared tool dirs: %v", err)
			continue
		}
		res.Installed = append(res.Installed, e.Name)
	}

	installed, err := installedSkills(projectRoot)
	if err != nil {
		return res, fmt.Errorf("read skills-lock.json: %w", err)
	}
	for name := range installed {
		if !declared[name] {
			res.Undeclared = append(res.Undeclared, name)
		}
	}
	return res, nil
}

// bridgeToolDirs makes skillName reachable from every tool dir in tools that
// `skills add` doesn't already cover natively (the canonical
// .agents/skills/<name> it always writes, and the Claude Code symlink it
// creates itself). It walks every declared tool rather than hardcoding
// "codex", so a future tool needing the same treatment is covered
// automatically. An already-present target (a prior bridge run, or a
// same-named lacquer-synced skill) is left untouched — never overwritten.
func bridgeToolDirs(projectRoot, skillName string, tools []string) error {
	canonical := filepath.Join(projectRoot, assets.ToolSkillsDir["antigravity"], skillName)
	if _, err := os.Lstat(canonical); err != nil {
		// Nothing to bridge from. `skills add` either failed silently or used
		// a layout this package doesn't recognize; Install's own error
		// handling around the Runner call is the authoritative signal for
		// that, so treat this as a no-op rather than a second failure mode.
		return nil
	}
	for _, tool := range tools {
		if tool == "antigravity" || tool == "claude" {
			continue // already covered by `skills add` itself
		}
		dir, ok := assets.ToolSkillsDir[tool]
		if !ok {
			continue
		}
		target := filepath.Join(projectRoot, dir, skillName)
		if _, err := os.Lstat(target); err == nil {
			continue // already present -- never clobber
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(target), canonical)
		if err != nil {
			return err
		}
		if err := os.Symlink(rel, target); err != nil {
			return err
		}
	}
	return nil
}

// lockFile is the subset of the `skills` CLI's project-scoped
// skills-lock.json this package reads. Its shape is that tool's, not
// lacquer's — only the top-level skill-name keys are used.
type lockFile struct {
	Skills map[string]struct {
		Source string `json:"source"`
	} `json:"skills"`
}

// installedSkills reads projectRoot/skills-lock.json and returns the set of
// currently-installed skill names. A missing lockfile (nothing installed
// yet) is not an error.
func installedSkills(projectRoot string) (map[string]bool, error) {
	data, err := os.ReadFile(filepath.Join(projectRoot, "skills-lock.json"))
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var lf lockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(lf.Skills))
	for name := range lf.Skills {
		names[name] = true
	}
	return names, nil
}
