package swiftcomponents

import (
	"reflect"
	"testing"
)

// `swift build` compiles for the host, macOS. A package whose platforms list
// no macOS cannot build that way, so the Lint job skips it, visibly. The read
// is textual because the drift audit renders the workflow on Linux, where
// there is no Swift toolchain to ask.
func TestBuildsOnMacOS(t *testing.T) {
	for name, tc := range map[string]struct {
		manifest string
		want     bool
	}{
		"no platforms: every platform at its default, macOS included": {
			"let package = Package(name: \"A\", targets: [.target(name: \"A\")])", true},
		"macOS listed": {
			"let package = Package(name: \"A\", platforms: [.iOS(.v18), .macOS(.v15)], targets: [])", true},
		"macOS listed across lines": {
			"let package = Package(\n    name: \"A\",\n    platforms: [\n        .iOS(.v18),\n        .macOS(.v15),\n    ],\n)", true},
		"iOS only": {
			"let package = Package(name: \"A\", platforms: [.iOS(.v26)], targets: [])", false},
		"iOS only, macOS commented out inside the list": {
			"let package = Package(\n    name: \"A\",\n    platforms: [\n        .iOS(.v26),\n        // .macOS(.v15), dropped: CoreNFC is iOS-only\n        /* .macOS(.v14), */\n    ],\n)", false},
		"iOS only, an old platforms line commented out above it": {
			"let package = Package(\n    name: \"A\",\n    // platforms: [.iOS(.v18), .macOS(.v15)],\n    platforms: [.iOS(.v26)],\n)", false},
		"Mac Catalyst is not macOS": {
			"let package = Package(name: \"A\", platforms: [.iOS(.v18), .macCatalyst(.v18)])", false},
		"macOS mentioned only after the platforms list": {
			"let package = Package(name: \"A\", platforms: [.iOS(.v18)], targets: [.target(name: \"A\", swiftSettings: [.define(\"X\", .when(platforms: [.macOS]))])])", false},
		"platforms from a variable: unreadable, so built and left to fail loudly": {
			"let shared: [SupportedPlatform] = [.iOS(.v18)]\nlet package = Package(name: \"A\", platforms: shared)", true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := BuildsOnMacOS(tc.manifest); got != tc.want {
				t.Fatalf("BuildsOnMacOS = %v, want %v for:\n%s", got, tc.want, tc.manifest)
			}
		})
	}
}

func TestPackagesSplitsBuiltFromSkipped(t *testing.T) {
	dir := repo(t, map[string]string{
		"tools/cli/Package.swift": "let package = Package(name: \"cli\", platforms: [.macOS(.v15)])",
		"tools/any/Package.swift": "let package = Package(name: \"any\")",
		"tools/nfc/Package.swift": "let package = Package(name: \"nfc\", platforms: [.iOS(.v26)])",
	})
	got, err := Packages(dir, manifest(appIOS, toolsPkg))
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{{Dir: "tools/any"}, {Dir: "tools/cli"}, {Dir: "tools/nfc", IOSOnly: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Packages = %+v, want %+v", got, want)
	}
}
