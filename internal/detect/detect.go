// Package detect discovers a project's components by looking for stack markers
// (an Xcode project, a package.json, a Cargo.toml, a go.mod) under the project
// root, and derives the iOS project name/scheme from the .xcodeproj name.
package detect

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/harness/internal/config"
)

// skip names that should never be treated as project source.
var skipDirs = map[string]bool{
	".git": true, ".worktrees": true, "node_modules": true,
	"DerivedData": true, ".build": true, "vendor": true, ".agents": true,
}

// markerProfile maps a marker filename to the profile it implies.
var markerProfile = map[string]string{
	"package.json": "web",
	"Cargo.toml":   "rust",
	"go.mod":       "go",
}

// Components walks root (skipping vendor/control dirs) and returns the detected
// components plus a derived Project (project_name/scheme from the first
// .xcodeproj). Component Path is the marker's directory relative to root.
func Components(root string) ([]config.Component, config.Project, error) {
	byPath := map[string]string{} // component path -> profile
	var derived config.Project

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			// An *.xcodeproj directory marks an iOS component at its parent.
			if strings.HasSuffix(d.Name(), ".xcodeproj") {
				rel := componentPath(root, filepath.Dir(path))
				byPath[rel] = "ios"
				if derived.ProjectName == "" {
					name := strings.TrimSuffix(d.Name(), ".xcodeproj")
					derived.ProjectName = name
					derived.Scheme = name
				}
			}
			return nil
		}
		if profile, ok := markerProfile[d.Name()]; ok {
			rel := componentPath(root, filepath.Dir(path))
			// Don't let a marker downgrade an iOS component already found here.
			if byPath[rel] == "" {
				byPath[rel] = profile
			}
		}
		return nil
	})
	if err != nil {
		return nil, config.Project{}, err
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

// componentPath returns dir relative to root, normalized ("" at root becomes ".").
func componentPath(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "" {
		return "."
	}
	return rel
}
