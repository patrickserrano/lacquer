package detect

import (
	"path"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/gitguard"
)

// SwiftManifestIndex is where a repository's Swift dependency manifests actually
// live, as Dependabot's swift ecosystem is able to find them.
//
// It exists because every other Dependabot entry the lacquer renders is safe to
// derive from the manifest alone: a `web` component was detected BY its
// package.json, a `rust` one BY its Cargo.toml, so pointing an entry at that
// component's directory cannot miss. An `ios` component is detected by an
// .xcodeproj, which is not a dependency manifest — so "this component is iOS"
// says nothing about whether, or where, a Swift manifest exists. The two
// questions had been treated as one, on the theory that a project with no SPM
// dependencies has nothing for Dependabot to find and the entry is therefore
// harmless. It is not harmless. Dependabot ERRORS:
//
//	Error during file fetching; aborting: Repo must contain a Package.swift
//	configuration file or an .xcodeproj/.xcworkspace directory with a
//	Package.resolved file.
//
// That aborted the whole Dependabot job — github-actions updates included — daily
// in three repositories (Queueify, rail, windsock), while the file it was
// rendered from looked completely correct.
//
// The rules below are not read off Dependabot's documentation; they are what this
// fleet's runs empirically do and do not find:
//
//   - Steps: swift PRs open, entry at "/", Package.resolved committed inside
//     Steps.xcodeproj at the root.
//   - kit: swift PRs open, entry at "/", Package.resolved committed inside
//     Kit/Kit.xcodeproj — a directory LEVEL BELOW the entry. So the search for a
//     bundled Package.resolved is recursive beneath `directory`.
//   - windsock: no swift PRs, entry at "/", with WindsockKit/Package.swift and
//     WindsockKit/Package.resolved committed one level below. So a bare SwiftPM
//     package is NOT found recursively — it has to be named exactly.
//   - Queueify, rail: no swift PRs; nothing at all is committed for it to read
//     (both keep the xcodeproj's Package.resolved out of the repo via
//     .gitignore), and both fail with the error above.
type SwiftManifestIndex struct {
	// pkgDirs are directories holding a bare SwiftPM package Dependabot can read:
	// Package.swift AND Package.resolved, both tracked, directly inside.
	//
	// Package.resolved is required, not merely welcome. The error text implies a
	// Package.swift alone satisfies file fetching, but no entry in this fleet has
	// ever been OBSERVED to work without a resolved file, and the cost of being
	// wrong is asymmetric: a missing entry stops watching a dependency quietly,
	// while a wrong one fails a job every single day until somebody looks. rail
	// is the case that decides it — RailCore/Package.swift and
	// RailData/Package.swift are committed, their resolved files are not — and it
	// gets no entry rather than two guesses.
	pkgDirs map[string]bool
	// bundleDirs are the directories that CONTAIN an .xcodeproj/.xcworkspace with
	// a tracked Package.resolved inside it. Any ancestor of one of these
	// qualifies, because Dependabot's search for a bundled resolved file is
	// recursive (kit, above).
	bundleDirs []string
}

// IndexSwiftManifests reads root's git index and records where Dependabot's swift
// ecosystem can find something to read.
//
// git rather than a filesystem walk, and deliberately not a second walker beside
// Components: what matters is what the REPOSITORY contains, not what the working
// tree contains. Both repos whose entries fail today have the file on disk and
// excluded by .gitignore, so a walk would have confirmed the broken entry as
// correct. git also excludes DerivedData/, .build/ and worktree copies for free —
// which matters more here than in Components, since a checkout of Steps carries
// nine Package.resolved files belonging to RevenueCat's own test fixtures.
//
// A root that is not a git repository (or a machine with no git) yields an empty
// index, which renders no swift entry at all. That is the fail-closed direction:
// sync already refuses to write assets outside a git repository, so nothing this
// affects ever reaches a project.
func IndexSwiftManifests(root string) (SwiftManifestIndex, error) {
	ix := SwiftManifestIndex{pkgDirs: map[string]bool{}}
	if root == "" {
		return ix, nil
	}
	files, err := gitguard.Tracked(root, "*Package.swift", "*Package.resolved")
	if err != nil {
		return ix, err
	}
	hasSwift, hasResolved, bundles := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range files {
		dir := dirOf(f)
		switch path.Base(f) {
		case "Package.swift":
			hasSwift[dir] = true
		case "Package.resolved":
			if bundle, ok := bundleParent(f); ok {
				bundles[bundle] = true
				continue
			}
			hasResolved[dir] = true
		}
	}
	for dir := range hasSwift {
		if hasResolved[dir] {
			ix.pkgDirs[dir] = true
		}
	}
	for dir := range bundles {
		ix.bundleDirs = append(ix.bundleDirs, dir)
	}
	sort.Strings(ix.bundleDirs) // deterministic, so a render never depends on map order
	return ix, nil
}

// DependabotDirs returns the directories a swift Dependabot entry for the
// component at comp should name, as component paths ("." for the repo root).
//
// Empty means NOTHING there is readable, and the caller must emit no entry: an
// absent entry is a dependency nobody watches, which is bad, and a present one
// pointing at nothing fails the job daily, which is worse.
//
// At most one entry is returned for a component that itself qualifies. A repo
// whose component root has an app-level Package.resolved is already covered by
// it — the resolution of every local package it depends on is inside that file —
// so naming the local packages as well would add PRs that duplicate the app's.
func (ix SwiftManifestIndex) DependabotDirs(comp string) []string {
	if comp == "" {
		comp = "."
	}
	// A bundled Package.resolved anywhere beneath comp is found from comp itself.
	// This is the shape of every working entry in the fleet, and keeping it as the
	// first rule is what makes steps and kit render exactly what they render now.
	for _, d := range ix.bundleDirs {
		if within(comp, d) {
			return []string{comp}
		}
	}
	if ix.pkgDirs[comp] {
		return []string{comp}
	}
	// Nothing at comp: name the bare packages beneath it instead, since Dependabot
	// will not find them from above (windsock). Outermost only — a package
	// vendored inside another is resolved by its parent.
	var out []string
	for d := range ix.pkgDirs {
		if d == comp || !within(comp, d) {
			continue
		}
		nested := false
		for other := range ix.pkgDirs {
			if other != d && other != comp && within(comp, other) && within(other, d) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// bundleParent reports the directory containing the outermost .xcodeproj /
// .xcworkspace bundle on p's path, when p is inside one.
//
// Outermost matters for what gets RECORDED, not for any answer: a Package.resolved
// lives at Foo.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved
// — two nested bundle-suffixed directories — and only ancestors of the recorded
// directory are ever consulted, so both spellings resolve the same today. The
// outer one is the directory a person would name when asked where the project is,
// which is what makes it the right thing to keep.
func bundleParent(p string) (string, bool) {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if ext := path.Ext(s); ext == ".xcodeproj" || ext == ".xcworkspace" {
			if i == 0 {
				return ".", true
			}
			return path.Join(segs[:i]...), true
		}
	}
	return "", false
}

// dirOf is path.Dir with the repo root spelled "." — the same spelling component
// paths use in .lacquer.toml, so the two can be compared directly.
func dirOf(p string) string {
	d := path.Dir(p)
	if d == "" || d == "/" {
		return "."
	}
	return d
}
