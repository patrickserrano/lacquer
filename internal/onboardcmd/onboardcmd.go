// Package onboardcmd implements `harness onboard`: init + (when no git remote
// exists) create a private GitHub repo. This is the one harness command with an
// outward side effect; init/sync stay filesystem-only.
package onboardcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/initcmd"
)

// ghCreate creates a private repo and wires it as origin. Injectable for tests.
var ghCreate = func(dir, org, name string) error {
	cmd := exec.Command("gh", "repo", "create", org+"/"+name,
		"--private", "--source=.", "--remote=origin")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh repo create: %v\n%s", err, out)
	}
	return nil
}

// Run ensures a .harness.toml exists, then (if createRepo and no origin remote)
// creates a private repo under org. It does not sync.
func Run(projectRoot, org string, createRepo bool) (string, error) {
	var out strings.Builder

	manifest := filepath.Join(projectRoot, ".harness.toml")
	if _, err := os.Stat(manifest); os.IsNotExist(err) {
		summary, err := initcmd.Run(projectRoot)
		if err != nil {
			return "", err
		}
		out.WriteString(summary)
		out.WriteString("\n")
	} else if err != nil {
		return "", err
	} else {
		out.WriteString("Using existing .harness.toml\n")
	}

	if createRepo {
		if org == "" {
			// Fall back to the manifest's github_org so the org is declared once
			// (in .harness.toml) and reused by repo creation and the {{GITHUB_ORG}}
			// token. A freshly init-stubbed manifest leaves it blank → still errors.
			if cfg, err := config.Load(manifest); err == nil {
				org = cfg.Project.GithubOrg
			}
		}
		if org == "" {
			return "", fmt.Errorf("no org to create the repo under (the harness has no default); pass --org <login>, set github_org in .harness.toml, or use --no-repo")
		}
		if !config.ValidGithubOrg(org) {
			return "", fmt.Errorf("invalid org %q (expected a GitHub org/user login)", org)
		}
		if hasOriginRemote(projectRoot) {
			out.WriteString("Remote 'origin' already exists; skipping repo creation.\n")
		} else {
			name, err := repoName(projectRoot, manifest)
			if err != nil {
				return "", err
			}
			if err := ghCreate(projectRoot, org, name); err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "Created private repo %s/%s and set origin.\n", org, name)
		}
	}

	out.WriteString("Next: fill any blank [project] values in .harness.toml, then run `harness sync`.")
	return out.String(), nil
}

func hasOriginRemote(dir string) bool {
	cmd := exec.Command("git", "remote")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, r := range strings.Fields(string(out)) {
		if r == "origin" {
			return true
		}
	}
	return false
}

// repoName uses [project].name when present, else the project dir's basename.
// A malformed manifest is surfaced, not silently ignored.
func repoName(projectRoot, manifest string) (string, error) {
	cfg, err := config.Load(manifest)
	if err != nil {
		return "", fmt.Errorf("load manifest for repo name: %w", err)
	}
	if cfg.Project.Name != "" {
		return cfg.Project.Name, nil
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	name := filepath.Base(abs)
	// Defense in depth: the dir basename isn't manifest-validated, and it is
	// passed to `gh`. Refuse anything outside the safe name charset.
	if !config.ValidProjectName(name) {
		return "", fmt.Errorf("cannot derive a safe repo name from dir %q; set [project].name in .harness.toml", name)
	}
	return name, nil
}
