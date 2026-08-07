// Package skipdirs centralizes the directory-name skip rules shared by
// internal/detect (component detection) and internal/skillsuggest (import
// scanning) — both must never treat a vendored, build, or dependency-cache
// directory as project source.
package skipdirs

import "strings"

var exact = map[string]bool{
	".git": true, ".worktrees": true, "node_modules": true,
	".build": true, "vendor": true, ".agents": true,
	"Pods": true, "Carthage": true,

	// Agent tool directories. `.agents` was already here; `.claude` and
	// `.codex` are the same kind of thing and were not, which mattered as soon
	// as detection gained teeth: `.claude/worktrees/agent-*/` is a full,
	// gitignored checkout of the project, so walking into it yields a phantom
	// copy of every component in the repo.
	//
	// throughline had one, and it surfaced as four undeclared stacks instead of
	// two — `admin` and `server` plus
	// `.claude/worktrees/agent-a9b4.../admin` and `.../server`. Harmless while
	// drift was only a report; not harmless now that an undeclared stack makes
	// `sync` refuse and `audit` exit 6, and that `lacquer adopt` would have
	// written those paths into the manifest as real components.
	//
	// Note the name here is `.claude`, not `worktrees`: skipping a bare
	// `worktrees` anywhere would be a wider net than intended, and the thing
	// that is genuinely never project source is the tool directory itself.
	".claude": true, ".codex": true,

	// The lacquer's OWN source, cloned into the project workspace by the
	// `No lacquer drift` CI job so it has a binary to audit with. It is not
	// project code, and walking into it makes every project on the lacquer
	// appear to contain the lacquer's stacks.
	//
	// This is not theoretical: once stack detection began running on every
	// audit, the drift job started reporting `.lacquer-checkout/site -> web`
	// as an undeclared stack and `.lacquer-checkout -> go` as one no profile
	// covers, and failed with exit 6 — on projects whose only mistake was
	// having the job that checks them out. The job that verifies a project
	// matches the lacquer cannot itself be what makes the project stop
	// matching.
	".lacquer-checkout": true,

	// Framework build output. These contain a generated package.json (and
	// sometimes a tsconfig), which detection reads as a web component — so
	// `lacquer init` on a Next.js site reported FIVE components: ".", ".next",
	// ".next/build", ".next/dev", and ".next/dev/build". Syncing that manifest
	// would have rendered CLAUDE.md regions, lefthook config and CI workflows
	// into build artifacts.
	//
	// Observed on pixelfoxstudio.com; the rest are the same shape for the other
	// frameworks this fleet is likely to meet.
	".next": true, ".nuxt": true, ".svelte-kit": true, ".astro": true,
	".output": true, ".vercel": true, ".netlify": true, ".turbo": true,
	".parcel-cache": true, ".angular": true, ".docusaurus": true,
	"dist": true, "out": true, ".cache": true,
}

// Skip reports whether a directory named name should never be walked into.
//
// DerivedData is matched by prefix, not exact name: this fleet's own
// convention (see profiles/ios's "Working in worktrees" guidance) is a
// unique per-worktree derived-data path like DerivedData-<feature>, so
// parallel worktrees don't collide on one directory. An exact-name-only
// check misses every one of those, letting Xcode's cached SPM dependency
// checkouts (under DerivedData-*/SourcePackages/checkouts/) leak into
// component detection or import scanning as if they were project source.
func Skip(name string) bool {
	if exact[name] {
		return true
	}
	return strings.HasPrefix(name, "DerivedData")
}
