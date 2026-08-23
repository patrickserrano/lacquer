// Package assets enumerates the whole-file assets (skills, commands, CI
// workflows, stack configs) that sync copies from the lacquer into a project,
// applying the design's placement rules.
package assets

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gitguard"
	"github.com/patrickserrano/lacquer/internal/retire"
	"github.com/patrickserrano/lacquer/internal/safepath"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

// MissingTokens returns "<token> (<dest>)" for every registered placeholder that
// appears in an asset's source with no project value. Used by sync's fail-closed
// preflight before any write.
func MissingTokens(plan []Asset, cfg *config.Config) ([]string, error) {
	var out []string
	for _, a := range plan {
		_, missing, err := Render(a, cfg)
		if err != nil {
			return nil, err
		}
		for _, m := range missing {
			out = append(out, fmt.Sprintf("%s (%s)", m, a.Dest))
		}
	}
	return out, nil
}

// SurvivingTokens returns every lacquer-shaped placeholder still present in a
// plan's RENDERED output, keyed by destination.
//
// MissingTokens above answers "which registered token had no value". This
// answers the question that one structurally cannot: "what is still a raw
// {{TOKEN}} in the bytes we are about to write". Those differ whenever a token
// is absent from the registry, because Substitute iterates the registry rather
// than the content — so an unregistered token is never looked up, never
// reported, and ships verbatim into the file.
//
// Checked against the rendered bytes, not the source, so it also covers merged
// destinations and any future render path that skips substitution entirely.
func SurvivingTokens(plan []Asset, cfg *config.Config) ([]string, error) {
	var out []string
	for _, a := range plan {
		body, _, err := Render(a, cfg)
		if err != nil {
			return nil, err
		}
		for _, t := range tokens.Surviving(string(body)) {
			out = append(out, fmt.Sprintf("%s (%s)", t, a.Dest))
		}
	}
	return out, nil
}

// Render returns the exact bytes an asset's destination receives, plus any
// registered placeholder that had no project value.
//
// Every consumer goes through here — sync writes it, audit hashes it — so a
// merged destination cannot be written as one thing and audited as another. That
// symmetry is what keeps `lacquer audit` meaningful: before the merge existed,
// audit reported OK for a lefthook.yml holding one profile's hooks because that
// is genuinely the file the lacquer would have written.
func Render(a Asset, cfg *config.Config) ([]byte, []string, error) {
	if len(a.Merged) == 0 {
		data, err := os.ReadFile(a.Src)
		if err != nil {
			return nil, nil, fmt.Errorf("read asset %s: %w", a.Src, err)
		}
		body, missing := tokens.Substitute(string(data), tokens.Values(cfg, a.Prefix))
		return []byte(body), missing, nil
	}

	merge, ok := mergerFor(a.Dest)
	if !ok {
		// Unreachable via plan(), which only ever sets Merged for a destination
		// that has one. Fail loud rather than silently write the first fragment.
		return nil, nil, fmt.Errorf("no merge strategy for %s, but %d profiles claim it", a.Dest, len(a.Merged))
	}
	var missing []string
	frags := make([]Fragment, 0, len(a.Merged))
	for _, m := range a.Merged {
		data, err := os.ReadFile(m.src)
		if err != nil {
			return nil, nil, fmt.Errorf("read asset %s: %w", m.src, err)
		}
		body, miss := tokens.Substitute(string(data), tokens.Values(cfg, m.prefix))
		missing = append(missing, miss...)
		frags = append(frags, Fragment{Profile: m.profile, Src: m.src, Content: body})
	}
	body, err := merge(a.Dest, frags)
	if err != nil {
		return nil, nil, err
	}
	return body, missing, nil
}

// ToolSkillsDir maps a configured agent tool to its project-level skills
// directory. SKILL.md is an open standard shared across Claude Code, Codex,
// Antigravity, Cursor, and Gemini CLI — only the directory differs — so the same
// skill package is copied verbatim into each enabled tool's dir. Commands are NOT
// fanned out: each tool's prompt/command mechanism differs, so commands stay
// Claude-only (.claude/commands). Custom subagent definitions (agents) are the
// same story as commands: there is no cross-tool standard for them, so they
// stay Claude-only too (.claude/agents).
//
// Exported so internal/skillsync can bridge third-party skill installs (from
// the `skills` CLI, which writes only to the canonical .agents/skills/<name>
// plus a Claude Code symlink) into every other tool a project declares —
// notably .codex/skills, which openai/codex's own repository dogfoods as its
// real project-level skill directory but which `skills add` does not write to
// even with an explicit --agent codex flag.
var ToolSkillsDir = map[string]string{
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
	// Merged is set only when several profiles claim this destination and a
	// merger is registered for it (see merge.go). Src/Prefix then describe the
	// first claimant and are not enough to render the file — call Render, which
	// every consumer does.
	Merged []claim
}

// claim is one profile's stake in a destination.
type claim struct {
	src     string
	prefix  string
	profile string // "" for core assets, which never merge
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
func Plan(lacquerRoot string, cfg *config.Config) ([]Asset, error) {
	out, _, err := plan(lacquerRoot, cfg)
	return out, err
}

// Suppressed returns the destinations [project].exclude kept out of the plan —
// the paths the lacquer WOULD ship if the manifest did not opt out of them.
//
// This is what makes a stale exclusion detectable. An exclusion naming a path
// the lacquer no longer ships (renamed workflow, retired config) suppresses
// nothing, so it reads as a deliberate ongoing decision while being dead text.
// Nothing could see that before, because Plan drops excluded destinations on the
// floor and every consumer only ever saw the survivors.
func Suppressed(lacquerRoot string, cfg *config.Config) ([]string, error) {
	_, sup, err := plan(lacquerRoot, cfg)
	return sup, err
}

// Shipped returns every destination the lacquer would write into this project if
// the manifest opted out of nothing — the plan with [project].exclude and
// retirement lifted.
//
// This is what makes an ORPHAN detectable, and the "lifted" part is the whole
// point. An orphan is a file the LACQUER stopped shipping: it is recorded in
// .lacquer.lock, sync will never delete it (a deliberate contract), and nothing
// reported it, so one retired workflow lived on in thirteen repositories and had
// to be removed by hand, one repository at a time.
//
// An excluded path and a retired project's dropped workflows are NOT that. Both
// leave the plan, but the lacquer still ships them — this project has merely
// stopped receiving them, and the day the exclusion or the retirement is lifted
// they come back. Diffing the lock against Plan would report every one of them
// as an orphan and advise deleting a file the project is about to be sent again,
// which would make retirement unusable. Diffing against Shipped cannot: a
// destination only leaves this set when no source in the lacquer produces it any
// more, or when the project stopped asking for the profile or tool that did.
func Shipped(lacquerRoot string, cfg *config.Config) ([]string, error) {
	// A copy, so the caller's config is untouched: Project is a struct field, so
	// assigning through the copy cannot reach the original's Exclude/Retired.
	// Components is shared, and plan only reads it.
	open := *cfg
	open.Project.Exclude = nil
	open.Project.Retired = nil
	out, _, err := plan(lacquerRoot, &open)
	if err != nil {
		return nil, err
	}
	dests := make([]string, 0, len(out))
	for _, a := range out {
		dests = append(dests, a.Dest)
	}
	return dests, nil
}

// plan builds the asset list, returning both the assets to copy and the
// destinations [project].exclude suppressed.
func plan(lacquerRoot string, cfg *config.Config) ([]Asset, []string, error) {
	var out []Asset
	var suppressed []string
	// seen records, per destination, which profile claimed it and where the
	// resulting Asset sits in `out` (-1 when the destination was dropped by an
	// exclusion or by retirement, so a later claimant has nothing to merge into).
	type placed struct {
		idx     int
		profile string
	}
	seen := map[string]placed{}
	// A workflow this cannot classify must not be silently kept or silently
	// dropped. add() has no error return (it is called from inside four walks),
	// so the first failure is held here and returned before the plan is used.
	var retireErr error
	// Likewise for two profiles claiming one destination with nothing registered
	// to merge them — see merge.go for why that must not be survivable.
	var collideErr error

	add := func(src, dest, prefix, profile string) {
		if prev, taken := seen[dest]; taken {
			switch {
			case profile == "" || prev.profile == "" || prev.profile == profile:
				// Core-vs-profile (either order) or the same profile twice. First
				// writer wins, exactly as before: core taking precedence over a
				// same-named profile skill is a deliberate rule, not a collision.
				return
			case prev.idx < 0:
				// The destination was excluded or retired away. A second claimant
				// does not resurrect it.
				return
			}
			if _, ok := mergerFor(dest); !ok {
				if collideErr == nil {
					collideErr = fmt.Errorf(
						"the %s and %s profiles both ship %s and nothing merges them. One of the two would "+
							"silently win and the other's contents would never reach the project — with `lacquer "+
							"audit` reporting OK throughout, because the lacquer really would write the winner. "+
							"Give the two profiles different destinations, or register a merge strategy for this "+
							"one in internal/assets/merge.go",
						prev.profile, profile, dest)
				}
				return
			}
			a := &out[prev.idx]
			if len(a.Merged) == 0 {
				a.Merged = []claim{{src: a.Src, prefix: a.Prefix, profile: prev.profile}}
			}
			a.Merged = append(a.Merged, claim{src: src, prefix: prefix, profile: profile})
			return
		}
		seen[dest] = placed{idx: -1, profile: profile}
		// Project-declared exclusions stay project-owned: the lacquer neither
		// distributes nor (via audit) tracks them. Used to keep a project's
		// hand-tuned CI/config local while still adopting the rest of the lacquer.
		//
		// This is checked BEFORE retirement on purpose. Both drop the asset, but
		// only an exclusion is recorded in `suppressed`, and that record is what
		// tells a live exclusion from dead text. Retiring a project must not turn
		// its exclusion of a scheduled workflow into "excludes a path the lacquer
		// no longer ships — it can be deleted": the lacquer still ships it, this
		// project has simply stopped receiving it, and deleting the exclusion on
		// that advice would quietly re-adopt the file the day it is un-retired.
		if cfg.Project.Excludes(dest) {
			suppressed = append(suppressed, dest)
			return
		}
		// Retirement is an implicit exclusion set covering scheduled work only —
		// see internal/retire. Deliberately NOT added to `suppressed`: nothing in
		// the manifest names these paths, so there is no declaration to review.
		if cfg.Project.IsRetired() {
			drop, err := retire.Drops(src, dest)
			if err != nil {
				if retireErr == nil {
					retireErr = err
				}
				return
			}
			if drop {
				return
			}
		}
		out = append(out, Asset{Src: src, Dest: dest, Prefix: prefix})
		seen[dest] = placed{idx: len(out) - 1, profile: profile}
	}

	tools := cfg.Project.EffectiveTools()

	// core assets are stack-agnostic: no component prefix. Skills fan out to each
	// enabled tool's skills dir; commands stay Claude-only.
	for _, tool := range tools {
		dir, ok := ToolSkillsDir[tool]
		if !ok {
			// Defense in depth: config.Load allowlists tool names, and every known
			// tool has a dir here. Fail loud rather than write skills to the project
			// root if the two ever drift.
			return nil, nil, fmt.Errorf("no skills directory mapped for tool %q", tool)
		}
		if err := walkInto(filepath.Join(lacquerRoot, "core", "skills"),
			func(src, rel string) { add(src, filepath.Join(dir, rel), "", "") }); err != nil {
			return nil, nil, err
		}
	}
	if err := walkInto(filepath.Join(lacquerRoot, "core", "commands"),
		func(src, rel string) { add(src, filepath.Join(".claude", "commands", rel), "", "") }); err != nil {
		return nil, nil, err
	}
	if err := walkInto(filepath.Join(lacquerRoot, "core", "agents"),
		func(src, rel string) { add(src, filepath.Join(".claude", "agents", rel), "", "") }); err != nil {
		return nil, nil, err
	}
	if err := walkInto(filepath.Join(lacquerRoot, "core", "root"),
		func(src, rel string) { add(src, rel, "", "") }); err != nil {
		return nil, nil, err
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
		base := filepath.Join(lacquerRoot, "profiles", p)
		prefix := tokens.Prefix(profileDir[p])
		for _, tool := range tools {
			dir, ok := ToolSkillsDir[tool]
			if !ok {
				return nil, nil, fmt.Errorf("no skills directory mapped for tool %q", tool)
			}
			if err := walkInto(filepath.Join(base, "skills"),
				func(src, rel string) { add(src, filepath.Join(dir, rel), prefix, p) }); err != nil {
				return nil, nil, err
			}
		}
		if err := walkInto(filepath.Join(base, "commands"),
			func(src, rel string) { add(src, filepath.Join(".claude", "commands", rel), prefix, p) }); err != nil {
			return nil, nil, err
		}
		if err := walkInto(filepath.Join(base, "agents"),
			func(src, rel string) { add(src, filepath.Join(".claude", "agents", rel), prefix, p) }); err != nil {
			return nil, nil, err
		}
		// workflows -> .github/workflows/<p>-<file> (stack-prefixed; flat)
		if err := walkInto(filepath.Join(base, "workflows"),
			func(src, rel string) {
				add(src, filepath.Join(".github", "workflows", p+"-"+filepath.Base(rel)), prefix, p)
			}); err != nil {
			return nil, nil, err
		}
		// workflows the lacquer ships but does NOT install unless asked, same
		// destination shape as above. Opt in with [project].optional_workflows.
		//
		// Gated by NAME rather than by directory-per-project so the file stays a
		// single shared asset: a project that opts in gets the same one everybody
		// else would, and it keeps being maintained rather than becoming a copy
		// somebody forked years ago.
		for _, want := range cfg.Project.OptionalWorkflows {
			src := filepath.Join(base, "workflows-optional", want+".yml")
			if _, err := os.Stat(src); err != nil {
				// A typo here would silently install nothing, which is the
				// failure this whole mechanism is meant to avoid.
				return nil, nil, fmt.Errorf("[project].optional_workflows: %s has no optional workflow %q", p, want)
			}
			add(src, filepath.Join(".github", "workflows", p+"-"+want+".yml"), prefix, p)
		}

		// profile root tree -> project root (verbatim relative paths)
		if err := walkInto(filepath.Join(base, "root"),
			func(src, rel string) { add(src, rel, prefix, p) }); err != nil {
			return nil, nil, err
		}
	}

	// config -> each component dir that lists the owning profile
	for _, c := range cfg.Components {
		prefix := tokens.Prefix(c.Path)
		for _, p := range c.Profiles {
			if err := walkInto(filepath.Join(lacquerRoot, "profiles", p, "config"),
				func(src, rel string) { add(src, filepath.Join(c.Path, rel), prefix, p) }); err != nil {
				return nil, nil, err
			}
		}
	}

	if retireErr != nil {
		return nil, nil, retireErr
	}
	if collideErr != nil {
		return nil, nil, collideErr
	}
	// A merged destination's fragment order decides which profile's file header
	// and hook comments lead the composed file, so it is pinned to sorted profile
	// order rather than to whichever walk happened to reach the destination
	// first. Src/Prefix are realigned to the leading fragment so the two
	// descriptions of "first claimant" cannot disagree.
	for i := range out {
		if len(out[i].Merged) == 0 {
			continue
		}
		sort.Slice(out[i].Merged, func(a, b int) bool { return out[i].Merged[a].profile < out[i].Merged[b].profile })
		out[i].Src = out[i].Merged[0].src
		out[i].Prefix = out[i].Merged[0].prefix
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dest < out[j].Dest })
	return out, suppressed, nil
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
// Preflight validates every asset without writing anything, returning the
// resolved target paths for Write to reuse so confinement is decided once.
//
// Split out of Copy so a CALLER can preflight before it starts writing anything
// of its own. sync writes managed regions (CLAUDE.md, AGENTS.md) and then calls
// Copy, so an asset-preflight failure used to abort AFTER the regions were on
// disk — leaving the project half-synced. Queueify landed in exactly that state:
// one uncommitted workflow file made Copy refuse, and it was left with rewritten
// CLAUDE.md and AGENTS.md from a sync that reported failure.
func Preflight(projectRoot string, plan []Asset) ([]string, error) {
	inRepo, err := gitguard.InWorkTree(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("git check: %w", err)
	}
	if !inRepo {
		return nil, fmt.Errorf("refusing asset sync: %s is not a git repository (git is required to guard against overwriting uncommitted work)", projectRoot)
	}

	targets := make([]string, len(plan))
	var dirty []string
	for i, a := range plan {
		target, err := safepath.Resolve(projectRoot, a.Dest)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", a.Dest, err)
		}
		if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to write through symlink: %s", a.Dest)
		}
		targets[i] = target

		isDirty, err := gitguard.Dirty(projectRoot, a.Dest)
		if err != nil {
			return nil, fmt.Errorf("git guard %s: %w", a.Dest, err)
		}
		if isDirty {
			dirty = append(dirty, a.Dest)
		}
	}
	if len(dirty) > 0 {
		return nil, fmt.Errorf("refusing to overwrite uncommitted changes in:\n  %s\n(commit or stash them, then re-run)",
			strings.Join(dirty, "\n  "))
	}
	return targets, nil
}

// Copy preflights and then writes. Kept for callers that do no writing of their
// own; sync uses Preflight + Write so its region writes sit behind this check.
func Copy(projectRoot string, plan []Asset, cfg *config.Config) error {
	targets, err := Preflight(projectRoot, plan)
	if err != nil {
		return err
	}
	return Write(projectRoot, plan, cfg, targets)
}

// Write copies the planned assets using targets from a prior Preflight.
func Write(projectRoot string, plan []Asset, cfg *config.Config, targets []string) error {
	for i, a := range plan {
		target := targets[i]
		// Render substitutes per-project placeholders + each fragment's component
		// prefix, and composes the fragments of a merged destination. Any missing
		// token value should already have been caught by sync's preflight; render
		// regardless (leaves an unresolved token rather than corrupting).
		data, _, err := Render(a, cfg)
		if err != nil {
			return err
		}
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
			// Never distribute build/tool cruft that may sit on the lacquer disk
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
