// Package swiftformatpin is the one place the lacquer decides which SwiftFormat
// a project runs, and what that release must hash to.
//
// A routine `brew upgrade` moved a shared runner to a SwiftFormat release with
// new default rules, and Lint failed on unchanged main branches in every iOS
// project, with Build and Test skipped behind it. Homebrew has no versioned
// swiftformat formula, so the version cannot be held there. CI and the local
// hooks instead install the release asset directly and verify it against the
// table below.
//
// The table lives in Go, beside the default, rather than in the iOS baseline
// file: a version is only useful with its checksum, and the tokens that render
// both into templates have no lacquer root to read a file from. Bumping the pin
// is a lacquer release that adds a row and moves Default.
package swiftformatpin

import (
	"fmt"
	"regexp"
	"sort"
)

// Default is the SwiftFormat version a project runs when its .lacquer.toml does
// not set swiftformat_version.
const Default = "0.64.0"

// checksums maps a release to the sha256 of its macOS `swiftformat.zip` asset on
// github.com/nicklockwood/SwiftFormat/releases. Each asset holds a universal
// (arm64 + x86_64) binary. Every value was computed from the downloaded asset:
//
//	curl -fsSL -o swiftformat.zip \
//	  https://github.com/nicklockwood/SwiftFormat/releases/download/<version>/swiftformat.zip
//	shasum -a 256 swiftformat.zip
//
// Never copy a hash from a release page or a package index; a number that was
// not computed from the bytes this table will verify proves nothing.
var checksums = map[string]string{
	"0.64.0": "95ea62625d0b898a7af870293311ae6f09e4a3147ecd900593687e4b9ab2fd98",
	"0.62.1": "7cb1cb1fae04932047c7015441c543848e8e60e1572d808d080e0a1f1661114a",
}

// versionFormat is MAJOR.MINOR.PATCH and nothing else: the value is substituted
// into shell and a URL, so it is held to the one shape a release tag has.
var versionFormat = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ValidFormat reports whether v is shaped like a release version.
func ValidFormat(v string) bool { return versionFormat.MatchString(v) }

// Resolve returns the version a project runs: its own setting, or Default when
// it sets none.
func Resolve(configured string) string {
	if configured == "" {
		return Default
	}
	return configured
}

// Checksum returns the sha256 a release's swiftformat.zip must have. A version
// with no row is an error naming the version and the remedy; it is never
// answered with the default's sum, which would make a mistyped pin install
// something other than what the project asked for.
func Checksum(version string) (string, error) {
	if sum, ok := checksums[version]; ok {
		return sum, nil
	}
	return "", fmt.Errorf("swiftformat_version %s has no checksum in this lacquer; bump lacquer", version)
}

// Versions lists every release the table can install, ascending.
func Versions() []string {
	out := make([]string, 0, len(checksums))
	for v := range checksums {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
