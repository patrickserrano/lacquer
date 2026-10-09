package testtargets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// charliePbx is charlie's project.pbxproj, cut down to what the audit reads: one
// native unit-test bundle, and the two XCLocalSwiftPackageReference blocks
// XcodeGen writes for `packages: { CharlieCore: { path: CharlieCore }, ... }`. The
// package suites are NOT native targets — they appear only in the Charlie scheme's
// TestAction, whose BuildableReference points at `container:CharlieCore`.
const charliePbx = `// !$*UTF8*$!
{
	objects = {
		357A64A766E9A0A0C0C8619F /* Charlie */ = {
			isa = PBXNativeTarget;
			name = Charlie;
			productType = "com.apple.product-type.application";
		};
		E50ACDC4AAA70FB86B4E0579 /* CharlieTests */ = {
			isa = PBXNativeTarget;
			name = CharlieTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
/* Begin XCLocalSwiftPackageReference section */
		080E1339E3D5C75E920E3FC3 /* XCLocalSwiftPackageReference "CharlieData" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = CharlieData;
		};
		6882A1BE352ADF32E2C10B1F /* XCLocalSwiftPackageReference "CharlieCore" */ = {
			isa = XCLocalSwiftPackageReference;
			relativePath = CharlieCore;
		};
/* End XCLocalSwiftPackageReference section */

/* Begin XCRemoteSwiftPackageReference section */
		8D4C9315FD2FAB7D410ED38C /* XCRemoteSwiftPackageReference "sentry-cocoa" */ = {
			isa = XCRemoteSwiftPackageReference;
			repositoryURL = "https://github.com/getsentry/sentry-cocoa";
		};
/* End XCRemoteSwiftPackageReference section */
	};
}
`

// charlie's CharlieCore/Package.swift, verbatim.
const charlieCorePackage = `// swift-tools-version: 6.4
import PackageDescription

let package = Package(
    name: "CharlieCore",
    platforms: [.iOS(.v26), .macOS(.v27)],
    products: [
        .library(name: "CharlieCore", targets: ["CharlieCore"]),
    ],
    targets: [
        .target(
            name: "CharlieCore",
            swiftSettings: [
                .swiftLanguageMode(.v6),
            ]
        ),
        .testTarget(
            name: "CharlieCoreTests",
            dependencies: ["CharlieCore"],
            swiftSettings: [
                .swiftLanguageMode(.v6),
            ]
        ),
    ]
)
`

// charlie's CharlieData/Package.swift, verbatim — including the `../CharlieCore`
// dependency, whose path must not be mistaken for anything the audit reads.
const charlieDataPackage = `// swift-tools-version: 6.4
import PackageDescription

let package = Package(
    name: "CharlieData",
    platforms: [.iOS(.v26), .macOS(.v27)],
    products: [
        .library(name: "CharlieData", targets: ["CharlieData"]),
    ],
    dependencies: [
        .package(path: "../CharlieCore"),
    ],
    targets: [
        .target(
            name: "CharlieData",
            dependencies: ["CharlieCore"],
            swiftSettings: [
                .swiftLanguageMode(.v6),
            ]
        ),
        .testTarget(
            name: "CharlieDataTests",
            dependencies: ["CharlieData", "CharlieCore"],
            swiftSettings: [
                .swiftLanguageMode(.v6),
            ]
        ),
    ]
)
`

// writeProject lays out a project root: files maps a root-relative path to its
// contents. It returns the path of the pbxproj, which is what Parse takes.
func writeProject(t *testing.T, xcodeproj string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, filepath.FromSlash(xcodeproj), "project.pbxproj")
}

func charlieProject(t *testing.T) string {
	return writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": charliePbx,
		"CharlieCore/Package.swift":         charlieCorePackage,
		"CharlieData/Package.swift":         charlieDataPackage,
	})
}

// charlieSelectors is what charlie's manifest renders: the derived CharlieTests, plus
// `extra_test_targets = ["CharlieCoreTests", "CharlieDataTests"]`.
var charlieSelectors = []string{"CharlieTests", "CharlieCoreTests", "CharlieDataTests"}

func parsePath(t *testing.T, pbxproj string) []Target {
	t.Helper()
	got, read, err := Parse(pbxproj)
	if err != nil {
		t.Fatal(err)
	}
	if !read {
		t.Fatal("Parse did not read a project that exists")
	}
	return got
}

// charlie's real shape, and the false alarm this file exists for. CharlieCoreTests and
// CharlieDataTests are test targets of local Swift packages the project references,
// named in the scheme, and run by `-only-testing:` — they are not native targets
// in project.pbxproj, and reading only the native targets reported both as
// "naming a target this project does not have". They exist; the audit had
// looked in one of the two places they can be.
func TestLocalPackageTestTargetsAreNotMissing(t *testing.T) {
	r := Compare(parsePath(t, charlieProject(t)), charlieSelectors)
	if len(r.Missing) != 0 {
		t.Fatalf("selectors naming a local package's test targets were reported missing: %v", r.Missing)
	}
	if len(r.Unverified) != 0 {
		t.Errorf("both packages were readable, yet selectors were reported unverified: %v", r.Unverified)
	}
	if out := Format(r); out != "" {
		t.Errorf("a fully wired rail produced a report:\n%s", out)
	}
}

// The check still exists. A selector that names a target in neither the project
// nor any package it references is the steps defect, and reading packages must
// not become a way for it to pass.
func TestSelectorNamingNothingIsStillMissingWithPackagesPresent(t *testing.T) {
	sel := append(append([]string{}, charlieSelectors...), "CharlieSyncTests")
	r := Compare(parsePath(t, charlieProject(t)), sel)
	if len(r.Missing) != 1 || r.Missing[0] != "CharlieSyncTests" {
		t.Fatalf("missing = %v, want [CharlieSyncTests]", r.Missing)
	}
	if !strings.Contains(Format(r), "CharlieSyncTests") {
		t.Errorf("the missing selector was not printed:\n%s", Format(r))
	}
}

// A package's library target is not a test target. `-only-testing:CharlieCore`
// matches no test bundle, so it must stay missing.
func TestPackageLibraryTargetIsNotATestTarget(t *testing.T) {
	r := Compare(parsePath(t, charlieProject(t)), []string{"CharlieTests", "CharlieCore"})
	if len(r.Missing) != 1 || r.Missing[0] != "CharlieCore" {
		t.Fatalf("missing = %v, want [CharlieCore] — a .target is not a .testTarget", r.Missing)
	}
}

// Only packages the PROJECT references count. A package that merely sits in the
// repository — or one reachable only as another package's dependency — has test
// targets no scheme of this project can select.
func TestUnreferencedPackageDoesNotCount(t *testing.T) {
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": strings.Replace(charliePbx,
			"relativePath = CharlieData;", "relativePath = Elsewhere;", 1),
		"CharlieCore/Package.swift": charlieCorePackage,
		"CharlieData/Package.swift": charlieDataPackage,
		"Elsewhere/Package.swift":   "// swift-tools-version: 6.0\nimport PackageDescription\nlet package = Package(name: \"Elsewhere\", targets: [])\n",
	})
	r := Compare(parsePath(t, pbx), charlieSelectors)
	if len(r.Missing) != 1 || r.Missing[0] != "CharlieDataTests" {
		t.Fatalf("missing = %v, want [CharlieDataTests] — CharlieData is not referenced by the project", r.Missing)
	}
}

// relativePath is relative to the directory holding the .xcodeproj, not to the
// repository root. flare's real shape: Delta/Delta.xcodeproj references
// `../DeltaCore`.
func TestRelativePathResolvesFromTheXcodeprojDirectory(t *testing.T) {
	pbx := writeProject(t, "App/App.xcodeproj", map[string]string{
		"App/App.xcodeproj/project.pbxproj": strings.Replace(charliePbx,
			"relativePath = CharlieCore;", "relativePath = ../CharlieCore;", 1),
		"CharlieCore/Package.swift":     charlieCorePackage,
		"App/CharlieData/Package.swift": charlieDataPackage,
	})
	r := Compare(parsePath(t, pbx), charlieSelectors)
	if len(r.Missing) != 0 || len(r.Unverified) != 0 {
		t.Fatalf("missing = %v, unverified = %v; want both empty", r.Missing, r.Unverified)
	}
}

// A path with spaces is quoted in project.pbxproj.
func TestQuotedRelativePath(t *testing.T) {
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": strings.Replace(charliePbx,
			"relativePath = CharlieCore;", `relativePath = "Packages/Charlie Core";`, 1),
		"Packages/Charlie Core/Package.swift": charlieCorePackage,
		"CharlieData/Package.swift":           charlieDataPackage,
	})
	r := Compare(parsePath(t, pbx), charlieSelectors)
	if len(r.Missing) != 0 || len(r.Unverified) != 0 {
		t.Fatalf("missing = %v, unverified = %v; want both empty", r.Missing, r.Unverified)
	}
}

// "Could not look" is not "it is not there". A referenced package whose
// Package.swift is absent — a submodule not checked out, a path that moved — is a
// place a selector's target might be, unexamined. Reporting the selector missing
// would be the audit asserting something it did not check; dropping it silently
// would be the audit passing something it did not check. It is reported as
// neither: unverified, with the reason.
func TestUnreadablePackageMakesTheSelectorUnverifiedNotMissing(t *testing.T) {
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": charliePbx,
		"CharlieCore/Package.swift":         charlieCorePackage,
		// CharlieData/Package.swift deliberately absent.
	})
	r := Compare(parsePath(t, pbx), charlieSelectors)
	if len(r.Missing) != 0 {
		t.Errorf("a selector was reported missing while a referenced package could not be read: %v", r.Missing)
	}
	if len(r.Unverified) != 1 || r.Unverified[0] != "CharlieDataTests" {
		t.Fatalf("unverified = %v, want [CharlieDataTests]", r.Unverified)
	}
	out := Format(r)
	for _, want := range []string{"CharlieDataTests", "could not check", "CharlieData/Package.swift does not exist"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not mention %q:\n%s", want, out)
		}
	}
}

// A package reference the audit cannot even locate is the same "could not look".
// Skipping it would turn every selector it could explain into "missing".
func TestPackageReferenceWithoutAPathIsUnverified(t *testing.T) {
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": strings.Replace(charliePbx, "\t\t\trelativePath = CharlieData;\n", "", 1),
		"CharlieCore/Package.swift":         charlieCorePackage,
		"CharlieData/Package.swift":         charlieDataPackage,
	})
	r := Compare(parsePath(t, pbx), charlieSelectors)
	if len(r.Missing) != 0 || strings.Join(r.Unverified, "|") != "CharlieDataTests" {
		t.Fatalf("missing = %v, unverified = %v; want no missing and [CharlieDataTests] unverified", r.Missing, r.Unverified)
	}
}

// An unreadable package only withholds judgement on selectors it could explain.
// A selector the readable places already account for is fine, and one they do
// not is unverified — never quietly covered.
func TestUnreadablePackageDoesNotCoverEverything(t *testing.T) {
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": charliePbx,
		"CharlieCore/Package.swift":         charlieCorePackage,
	})
	r := Compare(parsePath(t, pbx), []string{"CharlieTests", "CharlieCoreTests", "CharlieDataTests", "CharlieSyncTests"})
	if strings.Join(r.Unverified, "|") != "CharlieDataTests|CharlieSyncTests" {
		t.Fatalf("unverified = %v, want [CharlieDataTests CharlieSyncTests]", r.Unverified)
	}
	if Format(r) == "" {
		t.Error("unverified selectors produced an empty report")
	}
}

// A commented-out .testTarget is not a test target. The same defect class as
// lacquer#378, where a `#` comment counted as writing a secrets file.
func TestCommentedOutTestTargetDoesNotCount(t *testing.T) {
	core := strings.Replace(charlieCorePackage, "    ]\n)\n",
		"        // .testTarget(name: \"CharlieLegacyTests\"),\n"+
			"        /* .testTarget(\n            name: \"CharlieOldTests\"\n        ), */\n"+
			// Swift block comments nest: the inner */ does not end the outer one.
			"        /* retired: /* flaky */ .testTarget(name: \"CharlieNestedTests\"), */\n    ]\n)\n", 1)
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": charliePbx,
		"CharlieCore/Package.swift":         core,
		"CharlieData/Package.swift":         charlieDataPackage,
	})
	r := Compare(parsePath(t, pbx), []string{"CharlieTests", "CharlieCoreTests", "CharlieLegacyTests", "CharlieNestedTests", "CharlieOldTests"})
	if strings.Join(r.Missing, "|") != "CharlieLegacyTests|CharlieNestedTests|CharlieOldTests" {
		t.Fatalf("missing = %v, want the three commented-out targets", r.Missing)
	}
}

// Comment markers inside a string are not comments. `exclude: ["Fixtures/*.json"]`
// is an ordinary Package.swift line, and a scanner that took its `/*` for the
// start of a block comment would blank every test target after it — reporting
// them missing, the false alarm this file exists to remove. A URL's `//` is the
// same question on one line.
func TestCommentMarkersInStringsAreNotComments(t *testing.T) {
	core := strings.Replace(charlieCorePackage, "    targets: [",
		"    dependencies: [.package(url: \"https://example.com/a.git\", from: \"1.0.0\")],\n    targets: [", 1)
	core = strings.Replace(core, `            name: "CharlieCore",
`, `            name: "CharlieCore",
            exclude: ["Fixtures/*.json"],
`, 1)
	pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
		"Charlie.xcodeproj/project.pbxproj": charliePbx,
		"CharlieCore/Package.swift":         core,
		"CharlieData/Package.swift":         charlieDataPackage,
	})
	r := Compare(parsePath(t, pbx), charlieSelectors)
	if len(r.Missing) != 0 || len(r.Unverified) != 0 {
		t.Fatalf("missing = %v, unverified = %v; want both empty", r.Missing, r.Unverified)
	}
}

// A test target whose name is computed rather than written — a loop, a
// concatenation — cannot be read without running Swift. The package is then only
// partly read, which is "could not look" for anything it might name. A
// concatenation that STARTS with a literal is the trap: "Charlie" is not the name.
func TestComputedTestTargetNameIsUnverified(t *testing.T) {
	for _, call := range []string{
		`.testTarget(name: base + "SnapshotTests")`,
		`.testTarget(name: "Charlie" + "SnapshotTests")`,
	} {
		t.Run(call, func(t *testing.T) {
			core := strings.Replace(charlieCorePackage, "    ]\n)\n", "        "+call+",\n    ]\n)\n", 1)
			pbx := writeProject(t, "Charlie.xcodeproj", map[string]string{
				"Charlie.xcodeproj/project.pbxproj": charliePbx,
				"CharlieCore/Package.swift":         core,
				"CharlieData/Package.swift":         charlieDataPackage,
			})
			r := Compare(parsePath(t, pbx), append(append([]string{}, charlieSelectors...), "CharlieSnapshotTests"))
			if len(r.Missing) != 0 {
				t.Errorf("a selector was reported missing though a test target name could not be read: %v", r.Missing)
			}
			if strings.Join(r.Unverified, "|") != "CharlieSnapshotTests" {
				t.Fatalf("unverified = %v, want [CharlieSnapshotTests]", r.Unverified)
			}
		})
	}
}
