package baseline_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/baseline"
)

// pbxproj builds a minimal but REAL project file: a PBXProject pointing at its
// own configuration list, plus one target list. The shapes here are copied from
// projects in the fleet rather than invented, because every bug this file has
// caught so far was a formatting detail rather than a logic error — notably
// PBXFileReference being written on a single line, which made every xcconfig in
// every project invisible and reported a compliant project as having six
// violations.
type cfg struct {
	id, name, settings, baseRef string
}

func pbxproj(t *testing.T, projectCfgs, targetCfgs []cfg, fileRefs map[string]string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("// !$*UTF8*$!\n{\n\tobjects = {\n")

	for id, path := range fileRefs {
		// The single-line form Xcode actually writes.
		fmt.Fprintf(&b, "\t\t%s /* %s */ = {isa = PBXFileReference; lastKnownFileType = text.xcconfig; path = %s; sourceTree = \"<group>\"; };\n", id, path, path)
	}
	emit := func(c cfg) {
		fmt.Fprintf(&b, "\t\t%s /* %s */ = {\n\t\t\tisa = XCBuildConfiguration;\n", c.id, c.name)
		if c.baseRef != "" {
			fmt.Fprintf(&b, "\t\t\tbaseConfigurationReference = %s /* x.xcconfig */;\n", c.baseRef)
		}
		b.WriteString("\t\t\tbuildSettings = {\n")
		b.WriteString("\t\t\t\tSWIFT_VERSION = 6.0;\n")
		if c.settings != "" {
			fmt.Fprintf(&b, "\t\t\t\t%s\n", c.settings)
		}
		b.WriteString("\t\t\t};\n")
		fmt.Fprintf(&b, "\t\t\tname = %s;\n\t\t};\n", c.name)
	}
	for _, c := range projectCfgs {
		emit(c)
	}
	for _, c := range targetCfgs {
		emit(c)
	}

	list := func(id, comment string, cs []cfg) {
		fmt.Fprintf(&b, "\t\t%s /* %s */ = {\n\t\t\tisa = XCConfigurationList;\n\t\t\tbuildConfigurations = (\n", id, comment)
		for _, c := range cs {
			fmt.Fprintf(&b, "\t\t\t\t%s /* %s */,\n", c.id, c.name)
		}
		b.WriteString("\t\t\t);\n\t\t};\n")
	}
	list("PROJLIST0000000000000000", "Build configuration list for PBXProject", projectCfgs)
	list("TARGLIST0000000000000000", "Build configuration list for PBXNativeTarget", targetCfgs)

	b.WriteString("\t\tPROJECT00000000000000000 = {\n\t\t\tisa = PBXProject;\n\t\t\tbuildConfigurationList = PROJLIST0000000000000000 /* list */;\n\t\t};\n")
	b.WriteString("\t};\n}\n")

	dir := t.TempDir()
	proj := filepath.Join(dir, "App.xcodeproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "project.pbxproj"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const yes = "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;"

func check(t *testing.T, dir string) []baseline.Violation {
	t.Helper()
	vs, err := baseline.EnforceWarningsAsErrors(filepath.Join(dir, "App.xcodeproj"), dir)
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

// TestWarningsInheritedFromProjectLevelPasses is the layout most of the
// compliant fleet uses: set once at project level, every target inherits. A
// check that demanded the setting on each target would fail every one of them.
func TestWarningsInheritedFromProjectLevelPasses(t *testing.T) {
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", settings: yes}, {id: "P2", name: "Release", settings: yes}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, nil)
	if vs := check(t, dir); len(vs) != 0 {
		t.Errorf("inherited project-level YES reported as violations: %v", vs)
	}
}

// TestWarningsMissingEverywhereFails is the kit / skein / mindmint shape.
func TestWarningsMissingEverywhereFails(t *testing.T) {
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug"}, {id: "P2", name: "Release"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, nil)
	if vs := check(t, dir); len(vs) != 2 {
		t.Errorf("want 2 violations (one per configuration), got %d: %v", len(vs), vs)
	}
}

// TestTargetCannotOptOutOfProjectLevel is the hole a project-level-only check
// would leave: one target sets NO and silently stops treating warnings as
// errors while the project level still reads YES.
func TestTargetCannotOptOutOfProjectLevel(t *testing.T) {
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", settings: yes}, {id: "P2", name: "Release", settings: yes}},
		[]cfg{{id: "T1", name: "Debug", settings: "SWIFT_TREAT_WARNINGS_AS_ERRORS = NO;"}, {id: "T2", name: "Release"}}, nil)
	vs := check(t, dir)
	if len(vs) != 1 || vs[0].Config != "Debug" || vs[0].Found != "NO" {
		t.Errorf("a target opting out of project-level YES was not caught: %v", vs)
	}
}

// TestXcconfigUnconditionalPasses is rail and a-bible-verse-each-day: the value
// lives ONLY in an xcconfig. This is the case that was completely invisible
// until PBXFileReference's single-line form was handled — rail was reported
// with six violations while being fully compliant.
func TestXcconfigUnconditionalPasses(t *testing.T) {
	refs := map[string]string{"FR1": "Base.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "Config/Base.xcconfig", "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	if vs := check(t, dir); len(vs) != 0 {
		t.Errorf("xcconfig-supplied YES not seen: %v", vs)
	}
}

// TestXcconfigReleaseOnlyFailsDebug is the Queueify defect, and the reason this
// gate exists at all. `KEY[config=Release] = YES` is one bracket away from
// `KEY = YES` in a diff and covers half of what it appears to.
func TestXcconfigReleaseOnlyFailsDebug(t *testing.T) {
	refs := map[string]string{"FR1": "Shared.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "ios/xcconfig/Shared.xcconfig", "SWIFT_TREAT_WARNINGS_AS_ERRORS[config=Release] = YES\n")
	vs := check(t, dir)
	if len(vs) != 1 || vs[0].Config != "Debug" {
		t.Errorf("want exactly one Debug violation from a Release-only conditional, got: %v", vs)
	}
}

// TestXcconfigIncludeIsFollowed: a base xcconfig that only #includes another is
// a normal layout, and not following it would report a compliant project broken.
func TestXcconfigIncludeIsFollowed(t *testing.T) {
	refs := map[string]string{"FR1": "Debug.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "xcconfig/Debug.xcconfig", "#include \"Common.xcconfig\"\n")
	writeFile(t, dir, "xcconfig/Common.xcconfig", "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	if vs := check(t, dir); len(vs) != 0 {
		t.Errorf("#include was not followed: %v", vs)
	}
}

// TestXcconfigLastAssignmentWins mirrors Xcode: a later line overrides an
// earlier one. Reading the first match would report the value a project started
// with rather than the one it builds with.
func TestXcconfigLastAssignmentWins(t *testing.T) {
	refs := map[string]string{"FR1": "Base.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "Base.xcconfig", "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\nSWIFT_TREAT_WARNINGS_AS_ERRORS = NO\n")
	if vs := check(t, dir); len(vs) != 2 {
		t.Errorf("a later NO did not override an earlier YES: %v", vs)
	}
}

// TestCommentedOutSettingDoesNotCount — a `//`-commented line is not a setting,
// and counting it would let a project disable the policy by commenting it out.
func TestCommentedOutSettingDoesNotCount(t *testing.T) {
	refs := map[string]string{"FR1": "Base.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "Base.xcconfig", "// SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	if vs := check(t, dir); len(vs) != 2 {
		t.Errorf("a commented-out setting was treated as set: %v", vs)
	}
}

// TestDependencyXcconfigCannotSatisfyTheCheck. sentry-cocoa ships an xcconfig
// setting this very key, and it sits inside DerivedData/SourcePackages in
// several checkouts. Matching it would answer a question about this project
// with a fact about somebody else's.
func TestDependencyXcconfigCannotSatisfyTheCheck(t *testing.T) {
	refs := map[string]string{"FR1": "SDK.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "DerivedData/SourcePackages/checkouts/sentry-cocoa/Sources/Configuration/SDK.xcconfig",
		"SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	if vs := check(t, dir); len(vs) != 2 {
		t.Errorf("a dependency's xcconfig satisfied the check: %v", vs)
	}
}

// TestMissingConfigurationIsNotAPass. A project with no Debug target
// configuration must fail rather than pass vacuously — "nothing to check" and
// "nothing wrong" are different answers.
func TestMissingConfigurationIsNotAPass(t *testing.T) {
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", settings: yes}, {id: "P2", name: "Release", settings: yes}},
		[]cfg{{id: "T2", name: "Release"}}, nil)
	vs := check(t, dir)
	if len(vs) != 1 || vs[0].Config != "Debug" {
		t.Errorf("a project with no Debug target configuration passed vacuously: %v", vs)
	}
}

// TestLowercaseYesIsAccepted. Xcode writes YES, but a hand-edited xcconfig may
// carry `yes`, and the compiler does not care about case. Failing it would be a
// false refusal on a project that is genuinely strict.
func TestLowercaseYesIsAccepted(t *testing.T) {
	refs := map[string]string{"FR1": "Base.xcconfig"}
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", baseRef: "FR1"}, {id: "P2", name: "Release", baseRef: "FR1"}},
		[]cfg{{id: "T1", name: "Debug"}, {id: "T2", name: "Release"}}, refs)
	writeFile(t, dir, "Base.xcconfig", "SWIFT_TREAT_WARNINGS_AS_ERRORS = yes\n")
	if vs := check(t, dir); len(vs) != 0 {
		t.Errorf("lowercase yes was rejected: %v", vs)
	}
}

// TestStubProjectFileIsNotAViolation. `lacquer init` writes `{}` as the
// .xcodeproj purely as a detection marker, and a brand new project has to be
// able to sync. Refusing here made `init` followed by `sync` impossible.
func TestStubProjectFileIsNotAViolation(t *testing.T) {
	dir := t.TempDir()
	proj := filepath.Join(dir, "App.xcodeproj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "project.pbxproj"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vs, err := baseline.EnforceWarningsAsErrors(proj, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Errorf("a stub project file was treated as a violation, which blocks `init` then `sync`: %v", vs)
	}
}

// TestRealProjectMissingDebugStillFails is the other half of the stub rule: a
// project that HAS targets and is missing a required configuration is a
// deletion, not an empty project, and must still be caught.
func TestRealProjectMissingDebugStillFails(t *testing.T) {
	dir := pbxproj(t,
		[]cfg{{id: "P1", name: "Debug", settings: yes}, {id: "P2", name: "Release", settings: yes}},
		[]cfg{{id: "T2", name: "Release"}}, nil)
	vs := check(t, dir)
	if len(vs) != 1 || vs[0].Config != "Debug" {
		t.Errorf("a real project missing its Debug configuration was not caught: %v", vs)
	}
}
