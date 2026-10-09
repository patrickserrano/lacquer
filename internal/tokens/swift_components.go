package tokens

import (
	"fmt"
	"strings"
	"sync"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/swiftcomponents"
)

// The Swift-component family (#522 U4) makes the iOS Lint job and pre-commit
// hook reach every Swift component the manifest declares, not only the app's.
// A project with one Swift component (the whole fleet bar a few) renders each
// to its old spelling, pinned in legacyIOSCITokens.
const (
	// IOSCIPushPaths is the `on.push.paths` list: the app's line, then one per
	// package component, so a push to main touching only tools/ still runs CI.
	IOSCIPushPaths = "{{IOS_CI_PUSH_PATHS}}"
	// IOSCILintComponents is the space-separated directory list the Run
	// SwiftLint loop and Detect Swift sources walk: `ios/. tools/.`, or the
	// app's `{{COMPONENT_PREFIX}}.` alone.
	IOSCILintComponents = "{{IOS_CI_LINT_COMPONENTS}}"
	// IOSCIPackageDirs is the bash array body of packages the Lint job builds
	// (swiftcomponents.Packages that build on macOS): `'tools/a' 'tools/b'`, or
	// empty.
	IOSCIPackageDirs = "{{IOS_CI_PACKAGE_DIRS}}"
	// IOSCIPackageSkips is the same for iOS-only packages, which `swift build`
	// cannot compile on the macOS runner. The step names each one rather than
	// passing over it.
	IOSCIPackageSkips = "{{IOS_CI_PACKAGE_SKIPS}}"
	// IOSSwiftComponents is the pre-commit wrapper's component list, one path
	// per line, the app first.
	IOSSwiftComponents = "{{IOS_SWIFT_COMPONENTS}}"
	// IOSSwiftGateFrom is the day stray Swift starts blocking the pre-commit
	// hook, rendered from the same Go constant the Lint step and the audit read
	// (swiftcomponents.GateFrom), so the three cannot switch on different days.
	IOSSwiftGateFrom = "{{IOS_SWIFT_GATE_FROM}}"
)

// swiftComponents is the manifest's Swift components, or, for a Config built
// without any (the single-product tests, a manifest predating components), the
// app at the prefix being rendered.
func swiftComponents(cfg *config.Config, prefix string) []swiftcomponents.Component {
	if comps := swiftcomponents.Components(cfg); len(comps) > 0 {
		return comps
	}
	app := strings.TrimSuffix(prefix, "/")
	if app == "" {
		app = "."
	}
	return []swiftcomponents.Component{{Path: app, Profile: true}}
}

// CIPushPaths renders the whole `on.push.paths` list body, one line per Swift
// component. It owns its line at column 0 (like {{IOS_RELEASE_TAGS}}) because a
// token trailing a quoted list item would leave the template's `on:` block
// unparseable before substitution, and internal/retire parses that block.
// For a lone component it is exactly the line the template carried before.
func CIPushPaths(cfg *config.Config, prefix string) string {
	lines := []string{fmt.Sprintf("      - '%s**'", prefix)}
	for _, c := range swiftComponents(cfg, prefix) {
		if !c.Profile {
			lines = append(lines, fmt.Sprintf("      - '%s/**'", c.Path))
		}
	}
	return strings.Join(lines, "\n")
}

// CILintComponents renders the Lint loop's directory list. Component paths are
// validated to plain names, so no quoting is needed and the lone-component
// spelling is exactly the text the step carried before.
func CILintComponents(cfg *config.Config, prefix string) string {
	var dirs []string
	for _, c := range swiftComponents(cfg, prefix) {
		dirs = append(dirs, Prefix(c.Path)+".")
	}
	return strings.Join(dirs, " ")
}

// CIPackageDirs renders the package build list: the packages under package
// components that build on macOS, or, with iosOnly, the ones that do not. It
// reads the repository's tracked Package.swift files; with no root to read (a
// Config built in memory) or a git failure it renders nothing, and
// `lacquer audit` then reports the rendered file as drift against a sync that
// could read it.
func CIPackageDirs(cfg *config.Config, iosOnly bool) string {
	if cfg.Root == "" {
		return ""
	}
	var quoted []string
	for _, p := range packages(cfg) {
		if p.IOSOnly == iosOnly {
			quoted = append(quoted, "'"+p.Dir+"'")
		}
	}
	return strings.Join(quoted, " ")
}

// SwiftComponentList renders the pre-commit wrapper's component list.
func SwiftComponentList(cfg *config.Config, prefix string) string {
	var paths []string
	for _, c := range swiftComponents(cfg, prefix) {
		paths = append(paths, c.Path)
	}
	return strings.Join(paths, "\n")
}

// packages memoises swiftcomponents.Packages per root and component set:
// Values runs once per rendered file, and each call would otherwise list the
// repository again. Same shape as swiftManifests.
func packages(cfg *config.Config) []swiftcomponents.Package {
	key := cfg.Root + "\x00" + SwiftComponentList(cfg, "")
	pkgMu.Lock()
	defer pkgMu.Unlock()
	if pkgs, ok := pkgCache[key]; ok {
		return pkgs
	}
	pkgs, _ := swiftcomponents.Packages(cfg.Root, cfg)
	pkgCache[key] = pkgs
	return pkgs
}

var (
	pkgMu    sync.Mutex
	pkgCache = map[string][]swiftcomponents.Package{}
)
