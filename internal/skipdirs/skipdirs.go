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
