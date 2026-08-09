// Package config parses and validates a project's .lacquer.toml manifest.
package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/baseline"
)

// profileNameRe restricts profile names to a strict allowlist. Profile names are
// used unescaped in filesystem paths (profiles/<p>/CLAUDE.<p>.md) and as managed-
// region marker keys, so anything outside this set is rejected to prevent path
// traversal and marker injection.
var profileNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Project struct {
	Name         string `toml:"name"`
	ProjectName  string `toml:"project_name"`
	Scheme       string `toml:"scheme"`
	BundleID     string `toml:"bundle_id"`
	AscAppID     string `toml:"asc_app_id"`
	Xcodeproj    string `toml:"xcodeproj"`
	SwiftVersion string `toml:"swift_version"`
	GithubOrg    string `toml:"github_org"`
	// Stack is the archetype this project was initialised from (see
	// lacquer's archetypes/). Provenance only — the [[component]] blocks are
	// what sync acts on. It records the answer the brief/PCD gave so a later
	// reader can tell "iOS app with a Supabase backend, deliberately" apart
	// from "whatever happened to exist the day someone ran init".
	Stack   string      `toml:"stack"`
	Tools   []string    `toml:"tools"`
	Exclude []Exclusion `toml:"exclude"`
	Skills  []string    `toml:"skills"`
	// BuildEnv names repository secrets the project's build needs at BUILD time,
	// rendered into the synced web CI job's env block as
	// `NAME: ${{ secrets.NAME }}`.
	//
	// This exists because its absence cost a project the entire workflow.
	// pixelfoxstudio.com's `npm run build` statically collects page data, and
	// src/sanity/env.ts throws when NEXT_PUBLIC_SANITY_* are unset — so the
	// shared job could never build it. With no slot for five secret names, the
	// only way out was [project].exclude on web-ci.yml, which opted the repo out
	// of the whole shared workflow to add five lines. It has been hand-carrying
	// a full copy since, annotated "tracks the lacquer's web profile" — a copy
	// that drifts the moment the shared one changes.
	//
	// Names only, never values: the manifest is committed. The rendered form
	// reads each from `secrets`, so an unset secret is empty rather than
	// baked in.
	BuildEnv []string `toml:"build_env"`
}

// Exclusion is one [project].exclude entry: a path the lacquer neither
// distributes nor tracks.
//
// Two spellings are accepted, and the difference is the point:
//
//	exclude = ["typedoc.json"]                                  # bare
//	exclude = [{ path = "x", reason = "...", until = "..." }]   # attributed
//
// The bare form is what every manifest used first, and it is an unattributed,
// unbounded, invisible opt-out — the one exemption in this tool with no reason,
// no expiry, and no report, sitting directly beside [baseline.relax], which
// requires all three. It stays loadable so upgrading the lacquer never breaks a
// project, and is reported on every run until it is migrated.
//
// The attributed form requires a reason. `until` stays OPTIONAL, which is the
// one place this deliberately diverges from [baseline.relax], because the fleet
// showed two genuinely different things wearing the same spelling:
//
//   - Temporary debt. throughline excludes five iOS workflows carrying local
//     fixes "until that upstreaming happens deliberately" — a sentence with no
//     date attached, in a comment no tool can read. That should expire.
//   - Permanent divergence. windsock is a macOS-only app and excludes the
//     iOS-simulator CI workflow. No date will ever make that exclusion wrong.
//
// Forcing `until` onto the second kind would mean inventing dates for decisions
// that are simply correct, and renewing them forever. A rubber-stamped expiry
// is worse than an honest permanent one: it teaches the reviewer that dates in
// this file are noise. So presence of `until` IS the declaration of which kind
// this is — dated entries expire and then block, undated ones are reported as
// permanent divergences and never gate.
type Exclusion struct {
	Path   string `toml:"path"`
	Until  string `toml:"until"`  // optional, YYYY-MM-DD
	Reason string `toml:"reason"` // required in the attributed form
}

// UnmarshalTOML accepts either a bare string or a table, so the form every
// existing manifest uses keeps loading unchanged.
func (e *Exclusion) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		e.Path = t
		return nil
	case map[string]any:
		str := func(key string) (string, error) {
			raw, ok := t[key]
			if !ok {
				return "", nil
			}
			s, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("[project].exclude %s must be a string, got %T", key, raw)
			}
			return s, nil
		}
		for key := range t {
			switch key {
			case "path", "until", "reason":
			default:
				// A typo'd key would otherwise be silently dropped, turning an
				// attributed exclusion back into an unattributed one — exactly the
				// failure this type exists to end.
				return fmt.Errorf("unknown [project].exclude key %q (known keys: path, reason, until)", key)
			}
		}
		var err error
		if e.Path, err = str("path"); err != nil {
			return err
		}
		if e.Until, err = str("until"); err != nil {
			return err
		}
		if e.Reason, err = str("reason"); err != nil {
			return err
		}
		if e.Path == "" {
			return fmt.Errorf("[project].exclude entry needs a path")
		}
		return nil
	default:
		return fmt.Errorf("[project].exclude entry must be a string or a table, got %T", v)
	}
}

// Attributed reports whether this exclusion carries a reason — i.e. whether a
// later reader can tell why the path is unmanaged without guessing.
func (e Exclusion) Attributed() bool { return strings.TrimSpace(e.Reason) != "" }

// UntilDate parses Until. Only meaningful when Until is non-empty.
func (e Exclusion) UntilDate() (time.Time, error) { return time.Parse("2006-01-02", e.Until) }

// SkillEntry is a parsed "<owner>/<repo>@<skill-name>" entry from
// [project].skills — a third-party (or this lacquer's own) skill package to
// install via `lacquer skills` / the `skills` CLI (vercel-labs/skills).
type SkillEntry struct {
	Source string // "<owner>/<repo>", e.g. "dpearson2699/swift-ios-skills"
	Name   string // "<skill-name>", e.g. "healthkit"
}

// String renders back the "<source>@<name>" manifest form.
func (s SkillEntry) String() string { return s.Source + "@" + s.Name }

// skillSourceVal restricts the "<owner>/<repo>" half to GitHub's safe charset.
// skillNameVal restricts the skill name to the lowercase-kebab convention
// every skill in this fleet uses. Both entries are passed to `npx skills add`
// as separate argv elements (never shell-interpolated), but are still
// charset-validated so a malformed entry fails at `lacquer init`/`skills`
// time with a clear error instead of a confusing third-party CLI failure —
// and so a value can never be mistaken for a flag (no leading "-").
var (
	skillSourceVal = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	skillNameVal   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// ParseSkillEntry validates and splits a "<owner>/<repo>@<skill-name>" string.
func ParseSkillEntry(raw string) (SkillEntry, error) {
	source, name, ok := strings.Cut(raw, "@")
	if !ok || !skillSourceVal.MatchString(source) || !skillNameVal.MatchString(name) {
		return SkillEntry{}, fmt.Errorf("invalid [project].skills entry %q (expected \"<owner>/<repo>@<skill-name>\")", raw)
	}
	return SkillEntry{Source: source, Name: name}, nil
}

// ParsedSkills validates and parses every [project].skills entry.
func (p Project) ParsedSkills() ([]SkillEntry, error) {
	entries := make([]SkillEntry, 0, len(p.Skills))
	for _, raw := range p.Skills {
		e, err := ParseSkillEntry(raw)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
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
// An excluded path is opted out of lacquer oversight entirely — it is neither
// distributed by sync nor reported by audit (audit derives its unit set from the
// same filtered plan). That is the intended tradeoff for keeping a path local.
func (p Project) Excludes(dest string) bool {
	for _, e := range p.Exclude {
		if e.Matches(dest) {
			return true
		}
	}
	return false
}

// Matches reports whether dest falls under this exclusion's path.
func (e Exclusion) Matches(dest string) bool {
	pat := strings.TrimSuffix(e.Path, "/")
	return dest == pat || strings.HasPrefix(dest, pat+"/")
}

// knownTools is the set of agent tools the lacquer can provision skills for.
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
	// stackVal mirrors the archetype name charset. `stack` is never substituted
	// into synced content, but it is echoed in CLI output and read back to name a
	// file under archetypes/, so it stays on the same lowercase-kebab allowlist.
	stackVal = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// envNameVal is the POSIX environment-variable name charset. See
	// [project].build_env — these are rendered into synced workflow YAML.
	envNameVal = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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
	if err := check("stack", p.Stack, stackVal); err != nil {
		return err
	}
	for _, t := range p.Tools {
		if !knownTools[t] {
			return fmt.Errorf("invalid [project].tools entry %q (known tools: antigravity, claude, codex)", t)
		}
	}
	// build_env names are interpolated into synced YAML twice — as a mapping key
	// and inside ${{ secrets.NAME }} — so they are held to the POSIX environment
	// name charset. Anything else could inject structure into the workflow.
	seenEnv := map[string]bool{}
	for _, e := range p.BuildEnv {
		if !envNameVal.MatchString(e) {
			return fmt.Errorf("invalid [project].build_env entry %q (must match %s)", e, envNameVal.String())
		}
		if seenEnv[e] {
			return fmt.Errorf("[project].build_env lists %q twice (it would render a duplicate YAML key)", e)
		}
		seenEnv[e] = true
	}
	for _, e := range p.Exclude {
		if err := validateComponentPath(strings.TrimSuffix(e.Path, "/")); err != nil {
			return fmt.Errorf("invalid [project].exclude entry: %w", err)
		}
		// Shape only. Whether a dated exclusion has EXPIRED is evaluated at audit
		// time, not here, for the same reason [baseline.relax] is: load runs inside
		// `sync` and `fix`, and a manifest that cannot load is a manifest whose
		// own repair tooling is unavailable. An expired exemption must block CI,
		// not lock the project out of the command that fixes it.
		if e.Until != "" {
			if _, err := e.UntilDate(); err != nil {
				return fmt.Errorf("[project].exclude %q has an invalid until %q (want YYYY-MM-DD)", e.Path, e.Until)
			}
			if !e.Attributed() {
				return fmt.Errorf("[project].exclude %q has an until but no reason (an expiry no one can interpret cannot be reviewed)", e.Path)
			}
		}
	}
	if _, err := p.ParsedSkills(); err != nil {
		return err
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
	if !xcodeprojVal.MatchString(filepath.ToSlash(clean)) || !strings.HasSuffix(clean, ".xcodeproj") {
		return fmt.Errorf("[project].xcodeproj %q is not a valid .xcodeproj path", p)
	}
	return nil
}

type Component struct {
	Path     string   `toml:"path"`
	Profiles []string `toml:"profiles"`
}

// Baseline is a project's stance on the lacquer-owned project baseline. A project
// may only RELAX the standard, never restate or redefine it — the standard itself
// lives in the lacquer (profiles/<profile>/baseline.toml), so every project
// inherits it by default and a fresh init cannot scaffold a stale one.
type Baseline struct {
	Relax map[string]baseline.Relax `toml:"relax"`
}

type Config struct {
	Project    Project     `toml:"project"`
	Components []Component `toml:"component"`
	Baseline   Baseline    `toml:"baseline"`
}

// Load reads, parses, and validates the .lacquer.toml at path. It rejects any
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
	if err := validateBaseline(cfg.Baseline); err != nil {
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

// BaselineTargets returns one baseline target per (component, profile) pair, with
// the project's xcodeproj attached to the component that actually contains it.
//
// Attaching it to the owning component rather than to all of them matters for a
// multi-component project: handing a Supabase component the iOS project would
// have it checked — and reported — against the wrong standard.
func (c *Config) BaselineTargets() []baseline.Target {
	var out []baseline.Target
	xc := filepath.ToSlash(c.Project.Xcodeproj)
	for _, comp := range c.Components {
		for _, p := range comp.Profiles {
			t := baseline.Target{Profile: p, Component: comp.Path}
			if xc != "" && componentOwns(comp.Path, xc) {
				t.Xcodeproj = xc
			}
			out = append(out, t)
		}
	}
	return out
}

// componentOwns reports whether a component path contains the given
// project-relative path. "." (a root layout) contains everything.
func componentOwns(component, path string) bool {
	if component == "." || component == "" {
		return true
	}
	return strings.HasPrefix(path, component+"/")
}

// validateBaseline checks every [baseline.relax] entry.
//
// All three failures below are hard errors rather than warnings, because each one
// silently produces a permanent exemption if tolerated: an unknown key never
// matches a finding, a missing reason makes the debt unattributable, and a
// missing or unparseable expiry makes it unbounded. A relaxation that cannot
// expire is not a relaxation, it is a redefinition of the standard.
func validateBaseline(b Baseline) error {
	keys := make([]string, 0, len(b.Relax))
	for k := range b.Relax {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic error for a manifest with several problems
	for _, k := range keys {
		r := b.Relax[k]
		if !baseline.ValidKey(k) {
			return fmt.Errorf("unknown [baseline.relax] key %q (known keys: %s)", k, strings.Join(baseline.KnownKeys(), ", "))
		}
		if strings.TrimSpace(r.Reason) == "" {
			return fmt.Errorf("[baseline.relax].%s needs a non-empty reason (the debt must be attributable)", k)
		}
		if r.Until == "" {
			return fmt.Errorf("[baseline.relax].%s needs an until date (YYYY-MM-DD); a relaxation without an expiry is a redefinition of the standard", k)
		}
		if _, err := r.UntilDate(); err != nil {
			return fmt.Errorf("[baseline.relax].%s has an invalid until %q (want YYYY-MM-DD)", k, r.Until)
		}
	}
	return nil
}

// componentPathVal allows "." or slash-separated segments of safe characters
// only. component.path is substituted into CI YAML / shell via the derived
// {{COMPONENT_PREFIX}}, so it must not carry spaces, shell metacharacters, or
// path separators beyond simple nesting.
// Each segment must START with an alphanumeric / "." / "_" (never "-"), so a
// path can't become a shell flag once glued into {{COMPONENT_PREFIX}} (e.g.
// "-rf" -> `cd -rf/.`). Subsequent chars may include "-".
var componentPathVal = regexp.MustCompile(`^(\.|[A-Za-z0-9._][A-Za-z0-9._-]*(/[A-Za-z0-9._][A-Za-z0-9._-]*)*)$`)

// xcodeprojVal is componentPathVal plus spaces. An Xcode project named after a
// human-readable app title — "A Bible Verse Each Day.xcodeproj" — is completely
// ordinary, and rejecting it locked the oldest app in the fleet out of lacquer
// entirely. Spaces are safe here and NOT in a component path because every
// {{XCODEPROJ}} substitution site is quoted (`-project "{{XCODEPROJ}}"`),
// whereas {{COMPONENT_PREFIX}} is glued directly into paths like
// `cd {{COMPONENT_PREFIX}}.` where a space would split the argument.
//
// Everything genuinely dangerous is still rejected: quotes, $, backticks,
// backslashes, ;, |, &, newlines, and any other shell metacharacter. Each
// segment must still START with an alphanumeric / "." / "_" so the value can
// never be read as a flag.
//
// If you ever un-quote a {{XCODEPROJ}} substitution, this must go back to
// componentPathVal — the two are one decision.
var xcodeprojVal = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9 ._-]*(/[A-Za-z0-9._][A-Za-z0-9 ._-]*)*$`)

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
