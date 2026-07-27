package baseline

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture writes a project.pbxproj inside a .xcodeproj bundle and returns the
// bundle path, matching how a real project is laid out on disk.
func fixture(t *testing.T, pbxproj string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "App.xcodeproj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.pbxproj"), []byte(pbxproj), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A realistic shape, modeled on a project this checker has to get right:
//
//   - PROJ1/PROJ2 are the project-level Debug/Release configs. They declare
//     SWIFT_* settings (optimization level, compilation conditions) but no
//     language mode. Counting them as Swift-compiling configs would report a
//     fully compliant project as 4-of-6 — the false positive this fixture exists
//     to prevent.
//   - APP1/APP2 and EXT1/EXT2 are target configs that declare SWIFT_VERSION.
//   - warnings-as-errors is on the app target only, so coverage is 2 of 4.
const realisticProject = `
/* Begin PBXProject section */
		PRJOBJ /* Project object */ = {
			isa = PBXProject;
			buildConfigurationList = LISTPRJ /* Build configuration list for PBXProject "App" */;
			targets = (
				TGTAPP /* App */,
			);
		};
/* End PBXProject section */

/* Begin XCBuildConfiguration section */
		PROJ1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_ACTIVE_COMPILATION_CONDITIONS = "DEBUG $(inherited)";
				SWIFT_OPTIMIZATION_LEVEL = "-Onone";
			};
			name = Debug;
		};
		PROJ2 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_COMPILATION_MODE = wholemodule;
			};
			name = Release;
		};
		APP1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
		APP2 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
				SWIFT_VERSION = 6;
			};
			name = Release;
		};
		EXT1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
		EXT2 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_VERSION = 6;
			};
			name = Release;
		};
/* End XCBuildConfiguration section */

/* Begin XCConfigurationList section */
		LISTPRJ /* Build configuration list for PBXProject "App" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				PROJ1 /* Debug */,
				PROJ2 /* Release */,
			);
		};
		LISTAPP /* Build configuration list for PBXNativeTarget "App" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				APP1 /* Debug */,
				APP2 /* Release */,
			);
		};
		LISTEXT /* Build configuration list for PBXNativeTarget "Ext" */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				EXT1 /* Debug */,
				EXT2 /* Release */,
			);
		};
/* End XCConfigurationList section */
`

// The Swift-compiling set is the configs declaring SWIFT_VERSION. Project-level
// configs that declare other SWIFT_* settings must not inflate the denominator.
func TestSwiftConfigsExcludesProjectLevel(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, realisticProject))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	got := d.SwiftConfigs()
	if len(got) != 4 {
		t.Fatalf("SwiftConfigs = %d, want 4 (project-level Debug/Release declare SWIFT_* but no language mode)", len(got))
	}
}

func TestProjectLevelConfigsAreMarked(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, realisticProject))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	var n int
	for _, c := range d.Configs {
		if c.ProjectLevel {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d configs marked ProjectLevel, want 2", n)
	}
}

// Coverage of a key across the Swift-compiling set: 2 of 4 here.
func TestCoveragePartial(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, realisticProject))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_TREAT_WARNINGS_AS_ERRORS", "YES")
	if have != 2 || total != 4 {
		t.Errorf("Coverage = %d/%d, want 2/4", have, total)
	}
}

func TestCoverageFull(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, realisticProject))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_VERSION", "6")
	if have != 4 || total != 4 {
		t.Errorf("Coverage = %d/%d, want 4/4", have, total)
	}
}

// A setting declared once at the project level is inherited by every target
// config of the same name. Reporting that as uncovered would be a false positive
// on a legitimate and common way to configure a project.
func TestCoverageInheritsFromProjectLevel(t *testing.T) {
	inherited := `
/* Begin PBXProject section */
		PRJOBJ /* Project object */ = {
			isa = PBXProject;
			buildConfigurationList = LISTPRJ;
		};
/* End PBXProject section */

/* Begin XCBuildConfiguration section */
		PROJ1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
			};
			name = Debug;
		};
		PROJ2 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
			};
			name = Release;
		};
		APP1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
		APP2 /* Release */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_VERSION = 6;
			};
			name = Release;
		};
/* End XCBuildConfiguration section */

/* Begin XCConfigurationList section */
		LISTPRJ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				PROJ1 /* Debug */,
				PROJ2 /* Release */,
			);
		};
/* End XCConfigurationList section */
`
	d, err := ReadXcodeproj(fixture(t, inherited))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_TREAT_WARNINGS_AS_ERRORS", "YES")
	if have != 2 || total != 2 {
		t.Errorf("Coverage = %d/%d, want 2/2 — the project-level setting is inherited", have, total)
	}
}

// A target config that overrides an inherited value with the wrong value is not
// covered: the override wins, and it is non-compliant.
func TestCoverageTargetOverrideWins(t *testing.T) {
	overridden := `
/* Begin PBXProject section */
		PRJOBJ /* Project object */ = {
			isa = PBXProject;
			buildConfigurationList = LISTPRJ /* Build configuration list */;
		};
/* End PBXProject section */

/* Begin XCBuildConfiguration section */
		PROJ1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;
			};
			name = Debug;
		};
		APP1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_TREAT_WARNINGS_AS_ERRORS = NO;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */

/* Begin XCConfigurationList section */
		LISTPRJ /* Build configuration list */ = {
			isa = XCConfigurationList;
			buildConfigurations = (
				PROJ1 /* Debug */,
			);
		};
/* End XCConfigurationList section */
`
	d, err := ReadXcodeproj(fixture(t, overridden))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_TREAT_WARNINGS_AS_ERRORS", "YES")
	if have != 0 || total != 1 {
		t.Errorf("Coverage = %d/%d, want 0/1 — the target's NO overrides the project's YES", have, total)
	}
}

// A key nothing declares is uncovered across the whole Swift-compiling set.
func TestCoverageUnsetKey(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, realisticProject))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_STRICT_CONCURRENCY", "complete")
	if have != 0 || total != 4 {
		t.Errorf("Coverage = %d/%d, want 0/4", have, total)
	}
}

// Xcode quotes values that need it; the quotes are syntax, not part of the value.
func TestReadXcodeprojStripsQuotes(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, `
/* Begin XCBuildConfiguration section */
		AAA1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SWIFT_STRICT_CONCURRENCY = "complete";
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_STRICT_CONCURRENCY", "complete")
	if have != 1 || total != 1 {
		t.Errorf("Coverage = %d/%d, want 1/1", have, total)
	}
}

// A conditional variant (SETTING[sdk=...]) is a different setting from the plain
// key. Treating it as the plain key would let an iphoneos-only override read as
// unconditional coverage.
func TestReadXcodeprojIgnoresConditionalVariants(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, `
/* Begin XCBuildConfiguration section */
		AAA1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				"SWIFT_TREAT_WARNINGS_AS_ERRORS[sdk=iphoneos*]" = YES;
				SWIFT_VERSION = 6;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_TREAT_WARNINGS_AS_ERRORS", "YES")
	if have != 0 || total != 1 {
		t.Errorf("Coverage = %d/%d, want 0/1 (only an [sdk=] variant is present)", have, total)
	}
}

// No Swift anywhere: nothing to check, and total must be 0 rather than a
// division-by-zero trap or a spurious violation.
func TestCoverageNoSwiftConfigs(t *testing.T) {
	d, err := ReadXcodeproj(fixture(t, `
/* Begin XCBuildConfiguration section */
		AAA1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				ENABLE_TESTABILITY = YES;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`))
	if err != nil {
		t.Fatalf("ReadXcodeproj: %v", err)
	}
	have, total := d.Coverage("SWIFT_VERSION", "6")
	if have != 0 || total != 0 {
		t.Errorf("Coverage = %d/%d, want 0/0", have, total)
	}
}

func TestReadXcodeprojMissingFile(t *testing.T) {
	if _, err := ReadXcodeproj(filepath.Join(t.TempDir(), "Nope.xcodeproj")); err == nil {
		t.Fatal("ReadXcodeproj on a missing project: want error, got nil")
	}
}
