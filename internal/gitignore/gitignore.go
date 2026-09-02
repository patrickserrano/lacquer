// Package gitignore renders the managed .gitignore region: the ignore rules
// that must not be a per-project decision.
//
// The lacquer shipped 552 assets and not one .gitignore, so every project wrote
// its own and they disagreed. Measured across five repositories: two ignored
// `*.p8`, three did not; four ignored `Secrets.xcconfig`, one did not; two
// ignored `.env`, three did not. One repository was protected by nothing at
// all. A `.p8` is an App Store Connect API private key — committing one hands
// over build-upload and app-data access for every app on the account — and the
// ios profile has shipped `Secrets.xcconfig.example` the whole time, so the
// lacquer always knew the real file existed beside it and never arranged for it
// to be ignored.
//
// This is a REGION, not a whole-file asset, because a .gitignore is genuinely
// co-owned: DerivedData paths, build outputs and per-project junk are the
// project's business and nobody else's. Replacing the file would delete those
// and show up as permanent conflict in every audit. The region merges in and
// leaves everything outside the markers alone.
package gitignore

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/region"
)

// Name is the file the region is merged into, and Key its marker key.
const (
	Name = ".gitignore"
	Key  = "gitignore"
)

// Syntax is the comment form a .gitignore uses. A `#` line comment runs to end
// of line, which is also why every explanatory comment below sits on its OWN
// line: git does not strip a trailing comment from a pattern, so `*.p8 # key`
// would be a pattern matching the literal text `*.p8 # key` and would ignore
// nothing at all.
var Syntax = region.Hash

// credentials is the fixed block: files that grant access if committed. It is
// not gated on the project's profiles, deliberately. Gating would mean a
// repository that gains an iOS component is unprotected until someone re-syncs,
// and the cost of the ungated form is three inert lines in a web repo — while
// the cost of a missing line is a disclosed key. A `.p8` also does not stay in
// iOS repositories: any repo running an App Store Connect script can acquire
// one.
const credentials = `# Credentials. Every pattern here matches a file that grants real access if it
# reaches a commit — treat any that did as disclosed, and rotate it.

# App Store Connect API private key (AuthKey_XXXXXXXX.p8). Grants build upload
# and app-data access for every app on the account, and cannot be re-downloaded.
*.p8
# An exported signing identity: the certificate AND its private key.
*.p12
# A provisioning profile carries the team identity it was issued to.
*.mobileprovision

# Real service keys for the app (RevenueCat, Aptabase, Sentry). The committed
# artifact is the template beside it, Secrets.xcconfig.example, which the ios
# profile ships and CI copies verbatim to build.
#
# scripts/check-secrets.sh already refuses a commit that stages this file, but a
# pre-commit hook only fires on a machine where someone ran ` + "`lefthook install`" + `.
# This line is what protects the clone that never did.
#
# Unanchored on purpose: one project keeps a Secrets.xcconfig at two different
# depths, and an anchored pattern would cover neither of them.
Secrets.xcconfig
# The template is committed. Re-included in case a project's own rule above this
# block ignores *.xcconfig wholesale.
!Secrets.xcconfig.example

# Environment files. .env.* also covers .env.local and .env.production; the two
# committed templates are re-included below — .env.example documents the keys,
# and .env.schema is what the web profile's env-validation workflow checks it
# against. The .env.* rule would otherwise swallow both.
.env
.env.*
!.env.example
!.env.schema
# .env.op holds 1Password op:// references, never values — op run resolves
# them at run time. It has to be committed or the dev and migrate scripts have
# nothing to resolve, and the .env.* rule above would otherwise swallow it.
!.env.op`

// Body renders the managed region body for cfg.
//
// plan is what assets.Plan returns for this project — the whole-file assets the
// lacquer itself ships. It is read only to learn which skill names are
// lacquer-MANAGED, so the third-party skill rules below can be narrowed to
// exclude them. Ignoring a managed skill would be the windsock failure in a new
// place: that project ignores .agents/skills/ wholesale, which quietly untracks
// every skill the lacquer syncs alongside the third-party ones.
func Body(cfg *config.Config, plan []assets.Asset) (string, error) {
	sections := []string{credentials}

	if s := productSecrets(cfg); s != "" {
		sections = append(sections, s)
	}
	s, err := skills(cfg, ManagedSkillNames(plan))
	if err != nil {
		return "", err
	}
	sections = append(sections, s)

	return strings.Join(sections, "\n\n"), nil
}

// productSecrets renders a rule for every [[product]] that redirects its
// release-time keys somewhere other than the default Secrets.xcconfig — one
// project writes Config/Monetization.xcconfig, which the block above does not
// cover. The release workflow creates this file on a runner, but a developer
// reproducing a release locally creates it in their working tree, which is
// where it gets committed from.
func productSecrets(cfg *config.Config) string {
	seen := map[string]bool{}
	var lines []string
	for _, p := range cfg.Product {
		rel := filepath.ToSlash(p.SecretsPath())
		// The default is already covered by the unanchored Secrets.xcconfig rule.
		if rel == "Secrets.xcconfig" || seen[rel] {
			continue
		}
		seen[rel] = true
		// A pattern containing a slash is anchored to the .gitignore's own
		// directory, so a bare `Config/x.xcconfig` would miss `ios/Config/
		// x.xcconfig` in a nested-component project. A leading `**/` matches at
		// any depth, root included.
		lines = append(lines, "**/"+rel)
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	return "# Release-time keys redirected by [[product]].secrets_file. Written by CI on a\n" +
		"# runner, and by hand in a working tree whenever someone reproduces a release.\n" +
		strings.Join(lines, "\n")
}

// skills renders the third-party skill rules.
//
// The fleet had no convention here either: one project ignores .agents/skills/
// wholesale (swallowing the lacquer's own skills with it), another commits the
// installed trees and leaves skills-lock.json untracked-but-unignored, and a
// third did a third thing.
//
// The rule derives from [project].skills rather than from the directory, so
// only the trees the `skills` CLI installs are ignored and everything the
// lacquer syncs stays tracked and auditable. A skill name that the lacquer also
// ships is skipped outright — if both want the same name, tracking wins.
func skills(cfg *config.Config, managed map[string]bool) (string, error) {
	var b strings.Builder
	b.WriteString(`# Third-party skills, installed by the ` + "`skills`" + ` CLI from [project].skills.
# Vendored dependency trees: reproducible from the manifest, so committing them
# would put someone else's source in every diff and every review.
#
# Named one by one rather than ignoring the skills directories, because those
# directories ALSO hold the skills the lacquer syncs — and those must stay
# tracked, or ` + "`lacquer audit`" + ` goes blind to them and a drifted skill stops
# being detectable. Re-sync after editing [project].skills to refresh this list.
#
# skills-lock.json is deliberately NOT ignored. It pins the resolved source of
# every installed skill; committing it is what makes an install reproducible and
# what lets a reviewer see a skill's source change. It is a lockfile, and
# lockfiles are tracked.`)

	entries, err := cfg.Project.ParsedSkills()
	if err != nil {
		return "", fmt.Errorf("[project].skills: %w", err)
	}

	// Sorted and deduped so the rendered body is stable: an unstable render
	// would rewrite .gitignore on every sync and show as permanent drift.
	names := make([]string, 0, len(entries))
	var kept []string
	seenName := map[string]bool{}
	for _, e := range entries {
		if seenName[e.Name] {
			continue
		}
		seenName[e.Name] = true
		if managed[e.Name] {
			kept = append(kept, e.Name)
			continue
		}
		names = append(names, e.Name)
	}
	sort.Strings(names)
	sort.Strings(kept)

	// Say so rather than dropping it silently. A name the lacquer also ships is
	// the one case where a declared skill gets no rule, and someone will
	// otherwise read that as a bug in this renderer.
	for _, name := range kept {
		fmt.Fprintf(&b, "\n#\n# %q is declared here AND shipped by the lacquer, so it stays TRACKED:\n"+
			"# the synced copy is a managed unit and ignoring it would hide drift in it.", name)
	}

	if len(names) == 0 {
		if len(kept) == 0 {
			b.WriteString("\n#\n# [project].skills declares none, so there is nothing to ignore here yet.")
		}
		return b.String(), nil
	}

	for _, dir := range skillDirs(cfg) {
		for _, name := range names {
			// Leading slash: skills install project-scoped at the repo root, and
			// an unanchored `.claude/skills/x` would also ignore a nested
			// component's same-named directory.
			//
			// No TRAILING slash: `skills add` writes the canonical tree under
			// .agents/skills and makes the other tools' copies SYMLINKS, and a
			// directory-only pattern does not match a symlink.
			b.WriteString("\n/" + path.Join(dir, name))
		}
	}
	return b.String(), nil
}

// skillDirs is every directory a third-party skill can land in for this
// project, sorted.
//
// It is the union of the project's declared tools and the two directories the
// `skills` CLI writes REGARDLESS of what the manifest declares: the canonical
// .agents/skills tree and the Claude Code symlink beside it. A claude-only
// project still ends up with .agents/skills/<name> on disk, so deriving these
// from [project].tools alone would leave the canonical copy untracked-but-
// unignored — which is exactly the state one project in the fleet is in.
func skillDirs(cfg *config.Config) []string {
	dirs := map[string]bool{
		assets.ToolSkillsDir["antigravity"]: true, // canonical tree
		assets.ToolSkillsDir["claude"]:      true, // symlink `skills add` always writes
	}
	for _, tool := range cfg.Project.EffectiveTools() {
		if dir, ok := assets.ToolSkillsDir[tool]; ok {
			dirs[dir] = true
		}
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// ManagedSkillNames returns the skill names the lacquer itself ships, read off
// the asset plan's destinations. Exported so a test can assert the two sets are
// actually disjoint rather than trusting that they are.
func ManagedSkillNames(plan []assets.Asset) map[string]bool {
	names := map[string]bool{}
	for _, a := range plan {
		dest := filepath.ToSlash(a.Dest)
		for _, dir := range assets.ToolSkillsDir {
			prefix := dir + "/"
			if !strings.HasPrefix(dest, prefix) {
				continue
			}
			rest := strings.TrimPrefix(dest, prefix)
			if i := strings.Index(rest, "/"); i > 0 {
				names[rest[:i]] = true
			}
		}
	}
	return names
}
