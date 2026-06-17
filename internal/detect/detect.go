// Package detect discovers a project's components by looking for stack markers
// (an Xcode project, a package.json, a Cargo.toml, a go.mod) under the project
// root, and derives the iOS project name/scheme/xcodeproj path.
package detect

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/harness/internal/config"
)

// skip names that should never be treated as project source. Pods/Carthage hold
// dependency .xcodeproj files that would otherwise be mis-detected as components.
var skipDirs = map[string]bool{
	".git": true, ".worktrees": true, "node_modules": true,
	"DerivedData": true, ".build": true, "vendor": true, ".agents": true,
	"Pods": true, "Carthage": true,
}

// markerProfile maps a marker filename to the profile it implies.
var markerProfile = map[string]string{
	"package.json": "web",
	"Cargo.toml":   "rust",
	"go.mod":       "go",
}

// swiftConfig marks a directory as the iOS config/lint dir.
var swiftConfig = map[string]bool{
	".swiftlint.yml": true, ".swiftformat": true, ".periphery.yml": true,
}

// Components walks root (skipping vendor/control dirs) and returns the detected
// components plus a derived Project. The iOS component is the directory holding
// the Swift config files (.swiftlint.yml etc.) when the .xcodeproj sits within
// it; otherwise the .xcodeproj's parent. derived.Xcodeproj is the full
// repo-relative path to the first .xcodeproj.
func Components(root string) ([]config.Component, config.Project, error) {
	nonIos := map[string]string{} // component path -> web/rust/go
	var iosXcodeproj, iosXcodeprojDir string
	var iosConfigDirs []string // every dir holding a Swift config (resolved after the walk)
	var derived config.Project

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if strings.HasSuffix(d.Name(), ".xcodeproj") {
				if iosXcodeproj == "" {
					iosXcodeproj = relSlash(root, path)
					iosXcodeprojDir = componentPath(root, filepath.Dir(path))
					name := strings.TrimSuffix(d.Name(), ".xcodeproj")
					derived.ProjectName = name
					derived.Scheme = name
				}
				return filepath.SkipDir // don't descend into the project bundle
			}
			return nil
		}
		if swiftConfig[d.Name()] {
			iosConfigDirs = append(iosConfigDirs, componentPath(root, filepath.Dir(path)))
		}
		if profile, ok := markerProfile[d.Name()]; ok {
			rel := componentPath(root, filepath.Dir(path))
			if nonIos[rel] == "" {
				nonIos[rel] = profile
			}
		}
		return nil
	})
	if err != nil {
		return nil, config.Project{}, err
	}

	byPath := map[string]string{}
	for p, prof := range nonIos {
		byPath[p] = prof
	}
	if iosXcodeproj != "" {
		derived.Xcodeproj = iosXcodeproj
		iosComp := iosXcodeprojDir
		// Prefer the config dir when the xcodeproj lives within it (e.g. configs
		// at ios/, xcodeproj at ios/Queueify/Queueify.xcodeproj). Among all config
		// dirs that are ancestors of the xcodeproj, pick the deepest (most
		// specific); unrelated config dirs elsewhere are ignored. Order-independent.
		best := ""
		for _, dir := range iosConfigDirs {
			if within(dir, iosXcodeprojDir) && len(dir) > len(best) {
				best = dir
			}
		}
		if best != "" {
			iosComp = best
		}
		byPath[iosComp] = "ios"
	}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	comps := make([]config.Component, 0, len(paths))
	for _, p := range paths {
		comps = append(comps, config.Component{Path: p, Profiles: []string{byPath[p]}})
	}
	return comps, derived, nil
}

// within reports whether child is parent or a descendant of parent.
func within(parent, child string) bool {
	if parent == child {
		return true
	}
	if parent == "." {
		return true // everything is within the repo root
	}
	return strings.HasPrefix(child, parent+"/")
}

// relSlash returns path relative to root as a forward-slash path.
func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// componentPath returns dir relative to root as a forward-slash path ("" at root
// becomes "."), so the manifest is canonical and cross-platform.
func componentPath(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "" {
		return "."
	}
	return filepath.ToSlash(rel)
}
