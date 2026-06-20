// Package config parses and validates a project's .harness.toml manifest.
package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// profileNameRe restricts profile names to a strict allowlist. Profile names are
// used unescaped in filesystem paths (profiles/<p>/CLAUDE.<p>.md) and as managed-
// region marker keys, so anything outside this set is rejected to prevent path
// traversal and marker injection.
var profileNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Project struct {
	Name         string   `toml:"name"`
	ProjectName  string   `toml:"project_name"`
	Scheme       string   `toml:"scheme"`
	BundleID     string   `toml:"bundle_id"`
	AscAppID     string   `toml:"asc_app_id"`
	Xcodeproj    string   `toml:"xcodeproj"`
	SwiftVersion string   `toml:"swift_version"`
	GithubOrg    string   `toml:"github_org"`
	Tools        []string `toml:"tools"`
	Exclude      []string `toml:"exclude"`
}

// orgVal matches a GitHub org/user login (alphanumeric, single internal hyphens,
// no leading hyphen). github_org is substituted into synced docs via
// {{GITHUB_ORG}}, so it is charset-restricted like every other [project] value.
var orgVal = regexp.MustCompile(`^[A-Za-z0-9](-?[A-Za-z0-9])*$`)

// ValidGithubOrg reports whether s is a safe GitHub org/user login. Exported so
// the onboard command validates the same way before passing --org to `gh`.
func ValidGithubOrg(s string) bool { return orgVal.MatchString(s) }

// Excludes reports whether dest (a project-relative asset path) falls under any
// configured exclusion prefix, so sync/audit leave that path project-owned.
// A pattern matches the path itself or anything beneath it: "x/y" excludes
// "x/y" and "x/y/z", but not "x/yz".
//
// An excluded path is opted out of harness oversight entirely — it is neither
// distributed by sync nor reported by audit (audit derives its unit set from the
// same filtered plan). That is the intended tradeoff for keeping a path local.
func (p Project) Excludes(dest string) bool {
	for _, pat := range p.Exclude {
		pat = strings.TrimSuffix(pat, "/")
		if dest == pat || strings.HasPrefix(dest, pat+"/") {
			return true
		}
	}
	return false
}

// knownTools is the set of agent tools the harness can provision skills for.
// A tool name maps (in the assets package) to that tool's project-level skills
// directory. Restricted to a strict allowlist because it would otherwise route
// file writes to an attacker-named directory.
var knownTools = map[string]bool{
	"claude":      true, // .claude/skills
	"codex":       true, // .codex/skills
	"antigravity": true, // .agents/skills
}

// EffectiveTools returns the configured tools, defaulting to just "claude" when
// the manifest omits the field (backward-compatible: existing projects keep
// their Claude-only skill layout until they opt other tools in).
func (p Project) EffectiveTools() []string {
	if len(p.Tools) == 0 {
		return []string{"claude"}
	}
	return p.Tools
}

// WantsAgentsMd reports whether any enabled tool reads a project-root AGENTS.md
// (Codex, Antigravity). Claude Code uses CLAUDE.md, so a claude-only project
// gets no AGENTS.md. Shared by sync (what it writes) and audit (what it expects).
func (p Project) WantsAgentsMd() bool {
	for _, t := range p.EffectiveTools() {
		if t == "codex" || t == "antigravity" {
			return true
		}
	}
	return false
}

// Validators for [project] values. These values are substituted into synced CI
// YAML and pre-commit shell, so they are charset-restricted to prevent a crafted
// manifest from injecting structure or commands. A blank value is allowed (init
// stubs them); sync fails closed if a blank value's placeholder is actually used.
var (
	projNameVal    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)
	projBundleVal  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	projAscVal     = regexp.MustCompile(`^[0-9]+$`)
	projVersionVal = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// ValidProjectName reports whether s is a safe project/repo name (the same
// charset used for [project].name / project_name). Exported so the onboard
// command can defensively validate a name before passing it to `gh`.
func ValidProjectName(s string) bool {
	return projNameVal.MatchString(s)
}

func validateProject(p Project) error {
	check := func(field, val string, re *regexp.Regexp) error {
		if val == "" {
			return nil
		}
		if !re.MatchString(val) {
			return fmt.Errorf("invalid [project].%s value %q", field, val)
		}
		return nil
	}
	if err := check("name", p.Name, projNameVal); err != nil {
		return err
	}
	if err := check("project_name", p.ProjectName, projNameVal); err != nil {
		return err
	}
	if err := check("scheme", p.Scheme, projNameVal); err != nil {
		return err
	}
	if err := check("bundle_id", p.BundleID, projBundleVal); err != nil {
		return err
	}
	if err := check("asc_app_id", p.AscAppID, projAscVal); err != nil {
		return err
	}
	if err := check("swift_version", p.SwiftVersion, projVersionVal); err != nil {
		return err
	}
	if err := check("github_org", p.GithubOrg, orgVal); err != nil {
		return err
	}
	for _, t := range p.Tools {
		if !knownTools[t] {
			return fmt.Errorf("invalid [project].tools entry %q (known tools: antigravity, claude, codex)", t)
		}
	}
	for _, e := range p.Exclude {
		if err := validateComponentPath(e); err != nil {
			return fmt.Errorf("invalid [project].exclude entry: %w", err)
		}
	}
	return validateXcodeproj(p.Xcodeproj)
}

// validateXcodeproj accepts a blank value, or a relative, non-escaping,
// charset-safe path ending in ".xcodeproj" (it is substituted into CI -project
// args via {{XCODEPROJ}}).
func validateXcodeproj(p string) error {
	if p == "" {
		return nil
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("[project].xcodeproj %q must be relative", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("[project].xcodeproj %q escapes the project root", p)
	}
	if !componentPathVal.MatchString(filepath.ToSlash(clean)) || !strings.HasSuffix(clean, ".xcodeproj") {
		return fmt.Errorf("[project].xcodeproj %q is not a valid .xcodeproj path", p)
	}
	return nil
}

type Component struct {
	Path     string   `toml:"path"`
	Profiles []string `toml:"profiles"`
}

type Config struct {
	Project    Project     `toml:"project"`
	Components []Component `toml:"component"`
}

// Load reads, parses, and validates the .harness.toml at path. It rejects any
// component path that is absolute or escapes the project root, and any profile
// name that is not a simple lowercase identifier — both are used to build
// filesystem paths, so untrusted manifests must not be able to reach outside the
// intended directories.
func Load(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, err
	}
	if err := validateProject(cfg.Project); err != nil {
		return nil, err
	}
	seenProfile := map[string]string{} // profile -> first component path that declared it
	for _, c := range cfg.Components {
		if err := validateComponentPath(c.Path); err != nil {
			return nil, err
		}
		for _, p := range c.Profiles {
			if !profileNameRe.MatchString(p) {
				return nil, fmt.Errorf("invalid profile name %q (must match %s)", p, profileNameRe.String())
			}
			if prev, ok := seenProfile[p]; ok {
				return nil, fmt.Errorf("profile %q is declared by two components (%q and %q); one component per profile is supported", p, prev, c.Path)
			}
			seenProfile[p] = c.Path
		}
	}
	return &cfg, nil
}

// componentPathVal allows "." or slash-separated segments of safe characters
// only. component.path is substituted into CI YAML / shell via the derived
// {{COMPONENT_PREFIX}}, so it must not carry spaces, shell metacharacters, or
// path separators beyond simple nesting.
// Each segment must START with an alphanumeric / "." / "_" (never "-"), so a
// path can't become a shell flag once glued into {{COMPONENT_PREFIX}} (e.g.
// "-rf" -> `cd -rf/.`). Subsequent chars may include "-".
var componentPathVal = regexp.MustCompile(`^(\.|[A-Za-z0-9._][A-Za-z0-9._-]*(/[A-Za-z0-9._][A-Za-z0-9._-]*)*)$`)

// validateComponentPath rejects empty, absolute, and root-escaping component
// paths. The path must stay within the project root once joined.
func validateComponentPath(p string) error {
	if p == "" {
		return fmt.Errorf("component path must not be empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("component path %q must be relative, not absolute", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("component path %q escapes the project root", p)
	}
	// ToSlash so nested paths validate on Windows (filepath.Clean yields "\" there).
	if !componentPathVal.MatchString(filepath.ToSlash(clean)) {
		return fmt.Errorf("component path %q contains unsafe characters", p)
	}
	return nil
}
