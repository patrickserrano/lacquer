// Package detect discovers a project's components by looking for stack markers
// (an Xcode project, a package.json, a Cargo.toml, a go.mod) under the project
// root, and derives the iOS project name/scheme/xcodeproj path.
package detect

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/skipdirs"
)

// swiftVersionRe extracts a SWIFT_VERSION value from an XcodeGen project.yml.
var swiftVersionRe = regexp.MustCompile(`SWIFT_VERSION:\s*"?([0-9]+(?:\.[0-9]+)*)"?`)

// markerProfile maps a marker filename to the profile it implies.
var markerProfile = map[string]string{
	"package.json": "web",
	"Cargo.toml":   "rust",
	"go.mod":       "go",
}

// swiftConfig marks a directory as the iOS config/lint dir.
var swiftConfig = map[string]bool{
	".swiftlint.yml": true, ".swiftformat": true,
}

// SwiftProfile is the profile implied by a bare SwiftPM package — Swift source
// with no Xcode project to build it. It is deliberately NOT "ios": the ios
// profile's CI is app-shaped (it templates -project/-scheme into xcodebuild), so
// labelling a package "ios" would produce a manifest that cannot sync.
//
// No profile ships for it yet, which is exactly why detection must still name it:
// a stack the lacquer cannot manage has to be *reported* rather than invisible.
// See Drift.
const SwiftProfile = "swift"

// Components walks root (skipping vendor/control dirs) and returns the detected
// components plus a derived Project. The iOS component is the directory holding
// the Swift config files (.swiftlint.yml etc.) when the .xcodeproj sits within
// it; otherwise the .xcodeproj's parent. derived.Xcodeproj is the full
// repo-relative path to the first .xcodeproj.
func Components(root string) ([]config.Component, config.Project, error) {
	nonIos := map[string]string{} // component path -> web/rust/go
	var iosXcodeproj, iosXcodeprojDir string
	var iosConfigDirs []string // every dir holding a Swift config (resolved after the walk)
	var swiftPkgDirs []string  // every dir holding a Package.swift (pruned after the walk)
	var derived config.Project

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipdirs.Skip(d.Name()) {
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
		if d.Name() == "Package.swift" {
			swiftPkgDirs = append(swiftPkgDirs, componentPath(root, filepath.Dir(path)))
		}
		if d.Name() == "project.yml" && derived.SwiftVersion == "" {
			if data, rerr := os.ReadFile(path); rerr == nil {
				if m := swiftVersionRe.FindSubmatch(data); m != nil {
					derived.SwiftVersion = string(m[1])
				}
			}
		}
		if profile, ok := markerProfile[d.Name()]; ok {
			rel := componentPath(root, filepath.Dir(path))
			if nonIos[rel] == "" {
				nonIos[rel] = profile
			}
		}
		// A Supabase backend is marked by `supabase/config.toml`. The component is
		// the directory that CONTAINS `supabase/` (e.g. `server/`), not the
		// supabase dir itself — that's where deno.jsonc / the CLAUDE region land.
		if d.Name() == "config.toml" && filepath.Base(filepath.Dir(path)) == "supabase" {
			rel := componentPath(root, filepath.Dir(filepath.Dir(path)))
			if nonIos[rel] == "" {
				nonIos[rel] = "supabase"
			}
		}
		return nil
	})
	if err != nil {
		return nil, config.Project{}, err
	}

	// Multiple profiles per path, not one. A root-layout project with an Xcode
	// app AND a supabase/ backend puts both markers at ".", and assigning a
	// single profile per path meant the ios assignment below silently
	// OVERWROTE the supabase one — the component kept `profiles = ["ios"]` and
	// the whole supabase profile (deno config, lefthook, CI, RLS rules) was
	// never synced. rail is exactly that shape and had been missing it.
	byPath := map[string][]string{}
	for p, prof := range nonIos {
		byPath[p] = append(byPath[p], prof)
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
		// Append: a component can legitimately be both (see byPath above).
		byPath[iosComp] = append(byPath[iosComp], "ios")
	} else if pkg := swiftPackageRoot(swiftPkgDirs, iosConfigDirs); pkg != "" {
		// Swift with no Xcode project. Only reachable when the repo has NO
		// .xcodeproj anywhere, because a Package.swift inside an app repo is
		// almost always a local module of that app rather than a component of its
		// own: 6 of the 7 Swift repos in this fleet carry one, and every one of
		// those 6 would be a false positive. The one repo where Package.swift is
		// the whole Swift stack (needledrop/Sleevetap) has no .xcodeproj at all —
		// and went unmanaged for a month because detection had no marker for it.
		byPath[pkg] = append(byPath[pkg], SwiftProfile)
	}

	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	comps := make([]config.Component, 0, len(paths))
	for _, p := range paths {
		profs := byPath[p]
		sort.Strings(profs) // deterministic manifest output
		comps = append(comps, config.Component{Path: p, Profiles: profs})
	}
	return comps, derived, nil
}

// swiftPackageRoot picks the single component path for a repo whose Swift lives
// in SwiftPM packages rather than an Xcode project, or "" when there are none.
//
// It is one component, not one per package, because a manifest may declare a
// given profile exactly once (see config.Load) — so several packages resolve to
// their common ancestor, which is also the directory the lint/format configs
// want to sit in so they govern every package beneath. Nested packages (a
// package vendored inside another) collapse into their outermost parent.
//
// A Swift config directory that contains the result wins, mirroring the
// xcodeproj rule above: configs at ios/ with packages at ios/Foo, ios/Bar should
// yield the component "ios".
func swiftPackageRoot(pkgDirs, configDirs []string) string {
	var tops []string
	for _, d := range pkgDirs {
		nested := false
		for _, other := range pkgDirs {
			if other != d && within(other, d) {
				nested = true
				break
			}
		}
		if !nested {
			tops = append(tops, d)
		}
	}
	if len(tops) == 0 {
		return ""
	}
	root := tops[0]
	for _, d := range tops[1:] {
		root = commonDir(root, d)
	}
	best := ""
	for _, dir := range configDirs {
		if within(dir, root) && len(dir) > len(best) {
			best = dir
		}
	}
	if best != "" {
		return best
	}
	return root
}

// commonDir returns the deepest directory that contains both a and b, as a
// forward-slash component path ("." for the repo root).
func commonDir(a, b string) string {
	if a == b {
		return a
	}
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	var shared []string
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			break
		}
		shared = append(shared, as[i])
	}
	if len(shared) == 0 {
		return "."
	}
	return strings.Join(shared, "/")
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
