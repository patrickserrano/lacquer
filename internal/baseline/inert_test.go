package baseline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Project-only language mode must discover the targets, not the project itself.
func TestDiscoveryInheritsProjectLanguageMode(t *testing.T) {
	d := Declared{Configs: []Config{
		{ID: "project", Name: "Debug", ProjectLevel: true, Settings: map[string]string{"SWIFT_VERSION": "6", WarningsKey: "YES"}},
		{ID: "app", Name: "Debug", Settings: map[string]string{}},
	}}
	fs := Check(std, d, nil, now)
	f := find(t, fs, "swift_version")
	if f.Total != 1 || f.Status != StatusOK {
		t.Fatalf("inherited language mode: %+v", f)
	}
	d.Configs[0].Settings[WarningsKey] = "NO"
	if f := find(t, Check(std, d, nil, now), "warnings_as_errors"); f.Status != StatusViolation {
		t.Fatalf("inherited violation: %+v", f)
	}
}

func TestDeclaredSwiftEmptyDiscoveryIsUnknown(t *testing.T) {
	for _, pbx := range []string{"{}", strings.ReplaceAll(compliantPbx, "SWIFT_VERSION = 6;", "")} {
		lr, pr := projectDirs(t, pbx)
		reps, err := Run(lr, pr, iosTarget(), nil, now)
		if err != nil {
			t.Fatal(err)
		}
		out := FormatReports(reps)
		if !strings.Contains(out, "UNKNOWN") || !strings.Contains(out, "SWIFT_VERSION") || !strings.Contains(out, "App.xcodeproj") {
			t.Fatalf("empty discovery must be visible and actionable, got %q", out)
		}
		if Blocking(reps) != 0 {
			t.Fatalf("uncertainty must not fabricate a violation: %+v", reps)
		}
	}
}

// Both xcconfig layers participate, and a target xcconfig outranks the project's
// inline settings. Includes are resolved relative to the including file.
const layeredProject = `
 PRJ = {
  isa = PBXProject;
  buildConfigurationList = PLIST;
  mainGroup = ROOT;
 };
 ROOT = {
  isa = PBXGroup;
  children = (
   CONFIG,
  );
  sourceTree = "<group>";
 };
 CONFIG = {
  isa = PBXGroup;
  children = (
   PFILE,
   TFILE,
  );
  path = Config;
  sourceTree = "<group>";
 };
 PFILE = {isa = PBXFileReference; path = Project.xcconfig; sourceTree = "<group>"; };
 TFILE = {isa = PBXFileReference; path = Target.xcconfig; sourceTree = "<group>"; };
 PROJ = {
  isa = XCBuildConfiguration;
  baseConfigurationReference = PFILE;
  buildSettings = {
   SWIFT_TREAT_WARNINGS_AS_ERRORS = NO;
  };
  name = Debug;
 };
 APP = {
  isa = XCBuildConfiguration;
  baseConfigurationReference = TFILE;
  buildSettings = {
  };
  name = Debug;
 };
 PLIST = {
  isa = XCConfigurationList;
  buildConfigurations = (
   PROJ,
  );
 };
`

func layeredFixture(t *testing.T, pbx, project, target, base string) string {
	t.Helper()
	path := fixture(t, pbx)
	dir := filepath.Join(filepath.Dir(path), "Config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"Project.xcconfig": project, "Target.xcconfig": target, "Base.xcconfig": base} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestDiscoveryResolvesBothXcconfigLayers(t *testing.T) {
	path := layeredFixture(t, layeredProject, "SWIFT_VERSION = 6.0\n", "#include \"Base.xcconfig\"\n", "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	d, err := ReadXcodeproj(path)
	if err != nil {
		t.Fatal(err)
	}
	fs := Check(std, d, nil, now)
	for _, key := range []string{"swift_version", "warnings_as_errors"} {
		if f := find(t, fs, key); f.Status != StatusOK || f.Total != 1 {
			t.Fatalf("%s: %+v", key, f)
		}
	}
	// Report a target-layer violation, with its actual included source file.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "Config", "Base.xcconfig"), []byte("SWIFT_TREAT_WARNINGS_AS_ERRORS = NO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err = ReadXcodeproj(path)
	if err != nil {
		t.Fatal(err)
	}
	out := Format("ios", Check(std, d, nil, now))
	for _, want := range []string{"APP/Debug", "target xcconfig", "Base.xcconfig", "project-level pbxproj", "outranked"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestDiscoveryTargetXcconfigLanguageMode(t *testing.T) {
	path := layeredFixture(t, layeredProject, "", "#include \"Base.xcconfig\"\n", "SWIFT_VERSION = 6\nSWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	d, err := ReadXcodeproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if f := find(t, Check(std, d, nil, now), "swift_version"); f.Status != StatusOK {
		t.Fatal(f)
	}
}

func TestUncertainXcconfigIsUnknown(t *testing.T) {
	for _, content := range []string{
		"#include \"Missing.xcconfig\"\n",
		"#include \"Target.xcconfig\"\n",
		"SWIFT_VERSION = $(CUSTOM_SWIFT)\n",
		"SWIFT_VERSION[sdk=iphoneos*] = 6\n",
	} {
		t.Run(content, func(t *testing.T) {
			path := layeredFixture(t, layeredProject, "SWIFT_VERSION = 6\n", content, "")
			d, err := ReadXcodeproj(path)
			if err != nil {
				t.Fatal(err)
			}
			out := Format("ios", Check(std, d, nil, now))
			if !strings.Contains(out, "UNKNOWN") || strings.Contains(out, "baseline: ok") {
				t.Fatalf("uncertain resolution reported as certainty: %s", out)
			}
		})
	}
}

func TestKnownViolationSurvivesUncertainConfiguration(t *testing.T) {
	pbx := layeredProject + strings.ReplaceAll(compliantPbx, "APP1", "OTHER")
	// The extra target is Swift 6 but explicitly violates warnings-as-errors.
	pbx = strings.Replace(pbx, "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES;", "SWIFT_TREAT_WARNINGS_AS_ERRORS = NO;", 1)
	path := layeredFixture(t, pbx, "SWIFT_VERSION = 6\n", "#include \"Missing.xcconfig\"\n", "")
	d, err := ReadXcodeproj(path)
	if err != nil {
		t.Fatal(err)
	}
	f := find(t, Check(std, d, nil, now), "warnings_as_errors")
	if !f.Blocks() {
		t.Fatalf("uncertain target hid a known violation: %+v", f)
	}
}

func TestXcconfigPrecedenceAndIncludes(t *testing.T) {
	for _, tt := range []struct{ name, project, target, base, inline, want, source string }{
		{"project xcconfig", "SWIFT_VERSION = 6\n", "", "", "", "6", "project xcconfig"},
		{"project inline", "SWIFT_VERSION = 5\n", "", "", "", "6", "project pbxproj"},
		{"target xcconfig", "SWIFT_VERSION = 5\n", "SWIFT_VERSION = 6\n", "", "", "6", "target xcconfig"},
		{"target inline", "SWIFT_VERSION = 5\n", "SWIFT_VERSION = 5\n", "", "SWIFT_VERSION = 6;", "6", "target pbxproj"},
		{"include then override", "", "#include \"Base.xcconfig\"\nSWIFT_VERSION = 6\n", "SWIFT_VERSION = 5\n", "", "6", "Target.xcconfig"},
		{"override then include", "", "SWIFT_VERSION = 5\n#include \"Base.xcconfig\"\n", "SWIFT_VERSION = 6\n", "", "6", "Base.xcconfig"},
		{"optional include", "SWIFT_VERSION = 6\n", "#include? \"Missing.xcconfig\"\n", "", "", "6", "project xcconfig"},
		{"inherited", "SWIFT_VERSION = 6\n", "SWIFT_VERSION = $(inherited)\n", "", "SWIFT_VERSION = $(inherited);", "6", "project xcconfig"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pbx := layeredProject
			if tt.name == "project inline" {
				pbx = strings.Replace(pbx, "SWIFT_TREAT_WARNINGS_AS_ERRORS = NO;", "SWIFT_VERSION = 6;", 1)
			}
			pbx = strings.Replace(pbx, "  buildSettings = {\n  };", "  buildSettings = {\n   "+tt.inline+"\n  };", 1)
			path := layeredFixture(t, pbx, tt.project, tt.target, tt.base)
			d, err := ReadXcodeproj(path)
			if err != nil {
				t.Fatal(err)
			}
			configs := d.SwiftConfigs()
			if len(configs) != 1 {
				t.Fatalf("configs: %+v", configs)
			}
			r := d.resolve(configs[0], "SWIFT_VERSION")
			if r.unknown != "" || r.value != tt.want || !strings.Contains(r.source, tt.source) {
				t.Fatalf("resolved: %+v", r)
			}
		})
	}
}

func TestXcconfigDuplicateBasenameUsesReferencedGroup(t *testing.T) {
	path := layeredFixture(t, layeredProject, "SWIFT_VERSION = 6\n", "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n", "")
	stale := filepath.Join(filepath.Dir(path), "Other")
	if err := os.Mkdir(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "Target.xcconfig"), []byte("SWIFT_TREAT_WARNINGS_AS_ERRORS = NO\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := ReadXcodeproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if f := find(t, Check(std, d, nil, now), "warnings_as_errors"); f.Status != StatusOK {
		t.Fatal(f)
	}
}

func TestConditionalPbxprojOverrideCannotPass(t *testing.T) {
	pbx := strings.Replace(compliantPbx, "SWIFT_VERSION = 6;", "SWIFT_VERSION = 6;\n\t\t\t\t\"SWIFT_VERSION[sdk=iphoneos*]\" = 5;", 1)
	d, err := ReadXcodeproj(fixture(t, pbx))
	if err != nil {
		t.Fatal(err)
	}
	if f := find(t, Check(std, d, nil, now), "swift_version"); f.Status != StatusUnknown {
		t.Fatal(f)
	}
}
