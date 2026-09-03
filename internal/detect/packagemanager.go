package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Package manager identifiers threaded from detection through to
// tokens.Values, and from there into the rendered web CI workflow and git
// hooks.
const (
	PackageManagerPnpm = "pnpm"
	PackageManagerNpm  = "npm"
)

// WebPackageManager reports which package manager governs the web component
// at root/prefix, read from THAT component's own package.json.
//
// This exists because the web profile shipped hardcoded to npm for years,
// then — when every real consumer turned out to use pnpm instead (#161) —
// flipped to hardcoding pnpm instead, with no lever for anything else. Both
// are the same mistake: a shared profile is not supposed to know a fleet's
// current headcount, it is supposed to read what a project actually declares.
//
// pnpm is treated as an opt-IN, not a default: a package.json carrying a
// `packageManager` field that starts with `"pnpm@"` selects pnpm; anything
// else — an npm/yarn value, a missing field, or no package.json at all —
// selects npm, which is what `setup-node`'s own `cache: npm` and `npm ci`
// already assume when nothing says otherwise. That default is deliberately
// the SAFE direction: a project this has never seen (no package.json yet, or
// one written by hand without the field) gets the workflow it would have
// gotten before pnpm entered this profile at all, rather than one that
// assumes a lockfile and a corepack pin nobody asked for.
//
// The field is read from the component's OWN package.json, not the repo
// root's, for the same reason pnpm/action-setup's `package_json_file` input
// exists: a component in a subdirectory (`admin/package.json`) has nothing to
// do with whatever sits at the repository root, and reading the wrong file
// would silently apply one component's package manager to another's CI job.
//
// A missing or unparsable package.json is not an error here — it is read as
// "no signal", which resolves to npm. sync's own preflight (missing/surviving
// token checks) is what would fail closed on a rendering bug; this function's
// job is only to answer the question, not to guard the render.
func WebPackageManager(root, prefix string) string {
	// An unknown root (a Config built in memory rather than loaded — every
	// unit test that constructs config.Config{} literally does this) must not
	// fall through to a RELATIVE package.json read, which would resolve
	// against the calling process's own working directory rather than "no
	// project at all". No root means no signal, same as a missing file.
	if root == "" {
		return PackageManagerNpm
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(prefix), "package.json"))
	if err != nil {
		return PackageManagerNpm
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return PackageManagerNpm
	}
	if strings.HasPrefix(pkg.PackageManager, "pnpm@") {
		return PackageManagerPnpm
	}
	return PackageManagerNpm
}
