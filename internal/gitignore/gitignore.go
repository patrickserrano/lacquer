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
// co-owned: build outputs and per-project junk in general are the project's
// business and nobody else's. Replacing the file would delete those and show
// up as permanent conflict in every audit. The region merges in and leaves
// everything outside the markers alone.
//
// The DerivedData family is the one carve-out from that "project's business"
// line, and deliberately so: profiles/ios/CLAUDE.ios.md tells agents to build
// with `-d DerivedData-<feature>`, and the iOS CI workflow creates DerivedData
// and WatchDerivedData directories of its own. Those are not incidental build
// output a project happens to produce — they are paths the lacquer's OWN rules
// and CI commands into existence, which is the same argument buildOutputs below
// makes for itself. See buildOutputs for the block.
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
# PEM. The extension does not say what is inside: a .pem holds a certificate, a
# private key, or both concatenated. Six projects had already hand-written this
# rule independently (kit, flare, pixelfoxstudio.com, rail-web, patrickserrano,
# white-whales) and the repository actually holding PEM keys on disk is not one
# of them, which is the same signal the *.p8 block was built on.
#
# The whole extension rather than a guess at which half is secret: ignoring a
# dev certificate costs nothing, since the script that made it remakes it.
#
# Unanchored, and safe to be: no repository in the fleet tracks a *.pem.
*.pem

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

// agentArtifacts covers what an AGENT leaves behind in the working tree. It is
// the same fleet finding as the credentials block, one class down: measured
// across 38 repositories, seven carry a .playwright-mcp/ directory, SIX
// hand-wrote the identical rule for it -- at lines 2, 6, 11, 94 and 112 of five
// different .gitignore files, none of them in a managed region -- and the
// seventh (journalcast) wrote nothing and now has 22 files and 208K of it
// sitting unignored, one `git add -A` from a commit.
//
// The three repositories with a real playwright.config disagree three further
// ways on where Playwright's own output goes: one ignores playwright-report/
// only, one ignores test-results/ only, one ignores both. A convention that
// every project reinvents is not a convention, and this is exactly the shape
// this package exists to end.
//
// NOT in the credentials block above, deliberately. None of this is a key, and
// putting it there would dilute what that block means -- every line in it is a
// file that grants access, and that has to stay true for it to be read the way
// it needs to be. The argument here is smaller and still real: .playwright-mcp/
// holds a console dump and an accessibility snapshot of whatever page the
// browser was pointed at. Sampled across the fleet, that already includes a
// real user's email address in two repositories and Supabase references in a
// third. No token turned up in the sample, but nothing BOUNDS it to that --
// the content is whatever was on screen, which for an agent debugging an
// authenticated app is the authenticated app.
//
// Ungated on profile, for the credentials block's reason. The MCP server writes
// into the working directory of whatever repository an agent happens to be in,
// and four of the seven repositories carrying the directory are iOS or mixed,
// not web. Gating on the web profile would leave exactly those four unprotected.
//
// Unanchored: an agent's working directory is often a component subdirectory in
// a nested-layout repository, so an anchored rule would miss the case that
// actually produces these. Safe to leave unanchored because no repository in
// the fleet tracks a single file under any of these three paths -- checked, not
// assumed, because an ignore rule that lands on a tracked file is how you get a
// file that is committed, invisible to `git status`, and stale forever.
const agentArtifacts = `# Agent and browser-automation artifacts. Regenerated on demand and never an
# input to anything, so committing them only puts someone's debugging session
# in everyone else's diff.

# Written by the Playwright MCP server into the working directory: a console
# dump and an accessibility snapshot of whatever page was open. Treat it as a
# recording of a browsing session, not as test output.
.playwright-mcp/
# Playwright's own run output: the HTML report and the per-test artifacts
# (traces, screenshots, videos) its outputDir collects.
playwright-report/
test-results/`

// buildOutputs covers directories and one file pattern that are not "the
// project's business" in the sense the package comment draws that line at —
// they are paths the LACQUER'S OWN ios rules and CI command into existence,
// which is the same argument agentArtifacts makes one class up: nobody wrote
// these by hand, an instruction this repository ships did.
//
// profiles/ios/CLAUDE.ios.md tells agents to build with `-d DerivedData-
// <feature>` so a stale build of one feature branch cannot mask another's
// warnings; the ios CI workflow creates DerivedData and WatchDerivedData of
// its own for the same reason on a shared runner. default.profraw is the LLVM
// coverage counter file the same builds emit when coverage is enabled.
//
// Measured before adding this: two of the DerivedData-family paths were
// already covered, but only by luck — a project's OWN hand-written
// `DerivedData/` line, sitting outside any managed region, so a project that
// never wrote one (or wrote it without the `-*` sibling this pattern needs)
// had `ios/DerivedData-menubar/x`, `DerivedData-menubar/x`,
// `ios/WatchDerivedData/x`, `default.profraw` and `ios/default.profraw` all
// genuinely untracked-but-unignored: one `git add -A` from landing a build
// artifact in a commit and, worse, in a diff nobody would think to check.
//
// Both SwiftLint configs (.swiftlint.yml, .swiftlint-docs.yml) already
// exclude `**/DerivedData-*` from linting — this block is the same judgment
// applied to git, which had no equivalent rule at all.
//
// Ungated on profile, deliberately, for the same reason as agentArtifacts and
// credentials above: the cost of shipping this to a repository with no ios
// component is a few inert lines that never match anything real, while
// gating it would mean a repository that GAINS an ios component stays
// unprotected until its next sync — and these are Xcode-specific directory
// names (DerivedData, WatchDerivedData) and an LLVM coverage extension
// (.profraw) that nothing else plausibly creates, so there is no over-match
// risk an ungated rule would introduce in a non-ios repository.
//
// Unanchored, for the fleet finding above: the paths this block exists to
// cover were seen BOTH at the repository root and nested under a component
// directory (ios/DerivedData-menubar vs. DerivedData-menubar), so an anchored
// pattern would miss whichever layout it wasn't written for.
//
// Checked, not assumed, before shipping: no repository in the fleet roster
// tracks a file matching any of these four patterns (see the PR description
// for the sweep) — the same safety bar credentials and agentArtifacts were
// held to, because an ignore rule that lands on a tracked file hides it from
// `git status` forever.
const buildOutputs = `# Build output the lacquer's own ios rules and CI create, not incidental
# project build output in general (see this package's own doc comment) — an
# agent following profiles/ios/CLAUDE.ios.md's build instructions or a run of
# the ios CI workflow makes these, on every machine that follows either.

# profiles/ios/CLAUDE.ios.md tells agents to build with
# "-d DerivedData-<feature>"; the ios CI workflow creates DerivedData and
# WatchDerivedData of its own. Unanchored: seen both at the repository root
# and nested under a component directory.
DerivedData/
DerivedData-*/
WatchDerivedData/
# LLVM's coverage counter file, emitted by the same builds when coverage is
# enabled.
*.profraw`

// Body renders the managed region body for cfg.
//
// plan is what assets.Plan returns for this project — the whole-file assets the
// lacquer itself ships. It is read only to learn which skill names are
// lacquer-MANAGED, so the third-party skill rules below can be narrowed to
// exclude them. Ignoring a managed skill would be the windsock failure in a new
// place: that project ignores .agents/skills/ wholesale, which quietly untracks
// every skill the lacquer syncs alongside the third-party ones.
func Body(cfg *config.Config, plan []assets.Asset) (string, error) {
	sections := []string{credentials, agentArtifacts, buildOutputs}

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

	for _, dir := range assets.SkillDirs(cfg) {
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
