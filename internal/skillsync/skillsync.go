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
func Install(projectRoot string, entries []config.SkillEntry) (Result, error) {
	res := Result{Failed: map[string]string{}}
	declared := map[string]bool{}

	for _, e := range entries {
		declared[e.Name] = true
		out, err := Runner(projectRoot, "add", e.Source, "-s", e.Name, "-p", "-y")
		if err != nil {
			res.Failed[e.Name] = fmt.Sprintf("%v\n%s", err, out)
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
