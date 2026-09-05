// Package gitattributes renders the managed .gitattributes region: the linguist
// overrides that stop lacquer-shipped agent tooling from being counted as the
// project's own source.
//
// The lacquer ships 20 Python files, 207KB, into every project that takes the
// ios profile — xcode-build-orchestrator/scripts/*.py, xcode-compilation-
// analyzer, swiftui-expert-skill/scripts/instruments_parser/*.py — and it ships
// them once per enabled tool directory. A project declaring claude + codex gets
// three copies (.agents/skills, .claude/skills, .codex/skills), which is ~620KB
// of byte-identical Python landing in a Swift repository.
//
// GitHub Linguist counts every byte in the working tree toward the language bar
// unless a path is marked otherwise, so that is exactly what the fleet's repo
// pages report. Measured before this existed: ShelfLife read as MORE Python than
// Swift, and rail, flare, Skein, momfriend and dailybread each carried 570-650KB
// of the identical "Python". None of it was written by any of those projects.
// It is vendored agent tooling that arrived by `lacquer sync`, and the language
// bar is the first thing anyone sees when they open the repository.
//
// linguist-vendored is the override for precisely this: code that lives in the
// tree but was not authored by the project. It suppresses the language stats and
// collapses the files in a diff, and it does NOT hide them from git — the trees
// stay tracked, so `lacquer audit` can still see drift in a synced skill.
//
// This is a REGION, not a whole-file asset, because a .gitattributes is
// genuinely co-owned and three fleet repositories prove it: dailybread's is
// twelve Git LFS filter lines (`*.png filter=lfs diff=lfs merge=lfs -text` and
// friends) — replacing that file would break LFS outright and silently commit
// pointer-less binaries; kit's carries line-ending and binary rules plus its own
// linguist overrides (`Pods/** linguist-vendored`, `*.xcodeproj/**
// linguist-generated`, `docs/** linguist-documentation`); flare's normalizes
// line endings and declares `*.swift text diff=swift linguist-language=Swift`.
// A whole-file asset would clobber all three and show as permanent conflict in
// every audit. The region merges in and leaves everything outside the markers
// alone.
package gitattributes

import (
	"strings"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/region"
)

// Name is the file the region is merged into, and Key its marker key.
const (
	Name = ".gitattributes"
	Key  = "gitattributes"
)

// Syntax is the comment form a .gitattributes uses. Same as .gitignore: `#`
// starts a line comment that runs to end of line, which is also why the
// explanatory text below sits on its OWN lines. git does not strip a trailing
// comment from an attribute line, so `.claude/skills/** linguist-vendored=true
// # tooling` would parse `#` and `tooling` as two more attribute names and the
// line would do something other than what it reads as.
//
// It is region.Hash rather than a gitattributes-specific value because the two
// files spell comments identically and a second Syntax with the same contents is
// how the two would drift apart.
var Syntax = region.Hash

// Body renders the managed region body for cfg.
//
// No error return, deliberately, unlike gitignore.Body: every input here is
// already-validated config, there is no parse step that can fail, and an
// always-nil error at three call sites reads as error handling that means
// something. If a fallible input ever appears, the signature changes with it.
func Body(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString(`# Agent skill trees, marked as vendored so GitHub Linguist stops reporting them
# as this project's source.
#
# The lacquer syncs these directories; nothing in them is project-authored, and
# the same 207KB of Python tooling ships byte-identically to every project that
# takes the ios profile — once per enabled tool directory. Before this region
# existed one fleet repo's language bar read as more Python than Swift.
#
# linguist-vendored suppresses the language statistics and collapses these paths
# in a diff. It does NOT untrack them: the files stay in git, so ` + "`lacquer audit`" + `
# can still see drift in a synced skill. That is the difference between this
# region and the .gitignore one, and it is why a skill directory belongs in both
# for different reasons.
#
# https://github.com/github-linguist/linguist/blob/main/docs/overrides.md`)

	// assets.SkillDirs returns the union sorted, and sorted is load-bearing: one
	// `lacquer sync` renders this body three times — the clobber guard's audit,
	// the write, and the lock — so an unsorted render does not merely churn
	// between runs, it writes a lock disagreeing with the file it just wrote in
	// a single run, and every audit afterwards reports drift nobody introduced.
	for _, dir := range assets.SkillDirs(cfg) {
		// A pattern containing a slash is anchored to the directory holding the
		// .gitattributes — the repository root here — so no leading `/` is
		// needed or wanted; git rejects a leading slash in a .gitattributes
		// pattern outright.
		//
		// `/**` rather than a bare directory name: Linguist matches attributes
		// against FILE paths, and an attribute set on `.claude/skills` alone
		// applies to a path that is exactly that string and to nothing inside
		// it. The bytes being counted are the files, so the files are what has
		// to match.
		//
		// `=true` spelled out rather than the bare boolean form. Both work, but
		// the bare form sits one `-` away from `-linguist-vendored`, which means
		// the opposite, and kit's own file already mixes styles.
		b.WriteString("\n" + dir + "/** linguist-vendored=true")
	}
	return b.String()
}
