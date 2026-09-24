package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Same project/target xcconfig layering as #443's layeredProject fixture.
const settingsProject = `
 PRJ = {
  isa = PBXProject;
  buildConfigurationList = PLIST;
 };
 TGT = {
  isa = PBXNativeTarget;
  buildConfigurationList = TLIST;
  name = "Example App";
 };
 FILE = {isa = PBXFileReference; path = Base.xcconfig; sourceTree = SOURCE_ROOT; };
 PROJ = {
  isa = XCBuildConfiguration;
  buildSettings = {
   SWIFT_VERSION = 5;
  };
  name = Debug;
 };
 APP = {
  isa = XCBuildConfiguration;
  baseConfigurationReference = FILE;
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
 TLIST = {
  isa = XCConfigurationList;
  buildConfigurations = (
   APP,
  );
 };
`

func settingsFixture(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	project := filepath.Join(dir, "App.xcodeproj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{filepath.Join(project, "project.pbxproj"): settingsProject, filepath.Join(dir, "Base.xcconfig"): config} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return project
}

func TestSettingsStaticStatesAndProvenance(t *testing.T) {
	project := settingsFixture(t, "SWIFT_VERSION = 6\nMARKETING_VERSION = 1.2\nSWIFT_TREAT_WARNINGS_AS_ERRORS[sdk=iphoneos*] = YES\n")
	args := []string{"settings", "--project", project, "--target", "Example App", "--configuration", "Debug"}
	var out, errout bytes.Buffer
	if code := run(args, os.Getenv, &out, &errout); code != 0 {
		t.Fatalf("exit %d: %s", code, &errout)
	}
	for _, want := range []string{"static resolution by lacquer: not Xcode's evaluation; defaults and conditionals are not applied", "Example App", "SWIFT_VERSION = 6", "target xcconfig", "Base.xcconfig", "SWIFT_STRICT_CONCURRENCY = UNSET", "UNKNOWN", "conditional setting"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, &out)
		}
	}
	out.Reset()
	if code := run(append(args, "--json", "MARKETING_VERSION"), os.Getenv, &out, &errout); code != 0 {
		t.Fatal(&errout)
	}
	for _, want := range []string{`"value": "1.2"`, `"state": "set"`, `"setting": "MARKETING_VERSION"`, "static resolution by lacquer"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, &out)
		}
	}
}

func TestSettingsXcodeDerivedDataAndCompare(t *testing.T) {
	project := settingsFixture(t, "SWIFT_VERSION = 6\n")
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	t.Setenv("SETTINGS_ARGV", log)
	fake := `#!/bin/sh
printf '%s\n' "$@" > "$SETTINGS_ARGV"
while [ "$#" -gt 0 ]; do
 if [ "$1" = -derivedDataPath ]; then shift; test -d "$1" || exit 21; printf '%s' "$1" > "$SETTINGS_ARGV.tmp"; fi
 shift
done
test -f "$SETTINGS_ARGV.tmp" || { echo 'missing derived data' >&2; exit 22; }
printf '%s' '[{"target":"Example App","buildSettings":{"SWIFT_VERSION":"5","CONFIGURATION":"Debug"}}]'
`
	if err := os.WriteFile(filepath.Join(dir, "xcodebuild"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errout bytes.Buffer
	code := run([]string{"settings", "--project", project, "--target", "Example App", "--configuration", "Debug", "--xcode", "--scheme", "App Scheme", "--compare", "SWIFT_VERSION"}, os.Getenv, &out, &errout)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, &errout)
	}
	for _, want := range []string{"xcodebuild", "DIFF", "static resolution by lacquer", "6", "5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, &out)
		}
	}
	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-showBuildSettings\n-json\n-project\n" + project, "-scheme\nApp Scheme\n-configuration\nDebug", "-derivedDataPath\n", "-disableAutomaticPackageResolution\n-skipPackageUpdates"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("missing %q: %s", want, argv)
		}
	}
	temp, err := os.ReadFile(log + ".tmp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(temp)); !os.IsNotExist(err) {
		t.Errorf("temporary directory not removed: %v", err)
	}
}

func TestSettingsErrorsNeverFallBack(t *testing.T) {
	project := settingsFixture(t, "SWIFT_VERSION = 6\n")
	for _, tc := range []struct {
		name         string
		args         []string
		script, want string
	}{
		{"unknown target", []string{"--target", "Missing"}, "", "no matching"},
		{"unknown configuration", []string{"--configuration", "Absent"}, "", "no matching"},
		{"compare alone", []string{"--compare"}, "", "requires --xcode"},
		{"missing tool", []string{"--xcode", "--scheme", "App Scheme"}, "", "xcodebuild"},
		{"failed tool", []string{"--xcode", "--scheme", "App Scheme"}, "#!/bin/sh\necho deliberate-failure >&2\nexit 3\n", "deliberate-failure"},
		{"bad JSON", []string{"--xcode", "--scheme", "App Scheme"}, "#!/bin/sh\necho not-json\n", "JSON"},
		{"empty result", []string{"--xcode", "--scheme", "App Scheme"}, "#!/bin/sh\necho '[]'\n", "builds targets: (none)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			if tc.script != "" {
				if err := os.WriteFile(filepath.Join(dir, "xcodebuild"), []byte(tc.script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var out, errout bytes.Buffer
			args := append([]string{"settings", "--project", project}, tc.args...)
			if code := run(args, os.Getenv, &out, &errout); code == 0 {
				t.Fatalf("success: %s", &out)
			}
			if !strings.Contains(errout.String(), tc.want) {
				t.Errorf("want %q: %s", tc.want, &errout)
			}
			if out.Len() != 0 {
				t.Errorf("fallback output: %s", &out)
			}
		})
	}
}

func TestSettingsUnreadableProjectIsUnknown(t *testing.T) {
	var out, errout bytes.Buffer
	if code := run([]string{"settings", "--project", filepath.Join(t.TempDir(), "Missing.xcodeproj")}, os.Getenv, &out, &errout); code == 0 {
		t.Fatal("missing project succeeded")
	}
	if !strings.Contains(errout.String(), "UNKNOWN") || out.Len() != 0 {
		t.Fatalf("stdout %s stderr %s", &out, &errout)
	}
}

func TestSettingsDiscoversSingleProject(t *testing.T) {
	project := settingsFixture(t, "SWIFT_VERSION = 6\n")
	chdir(t, filepath.Dir(project))
	var out, errout bytes.Buffer
	if code := run([]string{"settings"}, os.Getenv, &out, &errout); code != 0 {
		t.Fatalf("exit %d: %s", code, &errout)
	}
	if !strings.Contains(out.String(), "Example App/Debug: SWIFT_VERSION = 6") {
		t.Fatal(&out)
	}
}

func TestSettingsXcodeJSONStates(t *testing.T) {
	project := settingsFixture(t, "SWIFT_VERSION = $(MODE)\nSWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n")
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s' '[{"target":"Dependency","buildSettings":{"SWIFT_VERSION":"4"}},{"target":"Example App","buildSettings":{"SWIFT_VERSION":"6","SWIFT_TREAT_WARNINGS_AS_ERRORS":"YES","CONFIGURATION":"Debug"}}]'
`
	if err := os.WriteFile(filepath.Join(dir, "xcodebuild"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, compare := range []bool{false, true} {
		args := []string{"settings", "--project", project, "--xcode", "--scheme", "App Scheme", "--target", "Example App", "--json"}
		if compare {
			args = append(args, "--compare")
		}
		var out, errout bytes.Buffer
		if code := run(args, os.Getenv, &out, &errout); code != 0 {
			t.Fatalf("exit %d: %s", code, &errout)
		}
		var report struct {
			Label    string
			Settings []settingRow
		}
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Settings) != 3 {
			t.Fatalf("rows: %+v", report.Settings)
		}
		for _, row := range report.Settings {
			if row.Target != "Example App" {
				t.Fatalf("unselected target: %+v", row)
			}
		}
		if compare {
			if !strings.Contains(report.Label, staticSettingsLabel) {
				t.Fatal(report.Label)
			}
			for i, want := range []string{"unknown", "match", "match"} {
				if report.Settings[i].Comparison != want {
					t.Errorf("row %d: %+v", i, report.Settings[i])
				}
			}
		} else {
			if report.Label != "xcodebuild" {
				t.Fatal(report.Label)
			}
			for _, row := range report.Settings {
				if row.Static != nil {
					t.Fatalf("unexpected static output: %+v", row)
				}
			}
		}
		if got := report.Settings[0].Xcode; got.State != "set" || got.Value != "6" || got.Source != "xcodebuild" {
			t.Fatalf("wrong target value: %+v", got)
		}
		if got := report.Settings[2].Xcode; got.State != "unset" {
			t.Fatalf("missing key not unset: %+v", got)
		}
	}
}

func TestSettingsXcodeFailureCleansScratch(t *testing.T) {
	project := settingsFixture(t, "")
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "xcodebuild"), []byte("#!/bin/sh\necho failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var out, errout bytes.Buffer
	if code := run([]string{"settings", "--project", project, "--xcode", "--scheme", "App Scheme"}, os.Getenv, &out, &errout); code == 0 {
		t.Fatal("unexpected success")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("scratch left after failure: %v", entries)
	}
}

func TestSettingsXcodeRequiresScheme(t *testing.T) {
	var out, errout bytes.Buffer
	code := run([]string{"settings", "--xcode"}, os.Getenv, &out, &errout)
	if code != 2 || !strings.Contains(errout.String(), "--xcode requires --scheme") || out.Len() != 0 {
		t.Fatalf("exit %d, stdout %s, stderr %s", code, &out, &errout)
	}
}

func TestSettingsXcodeTargetNotBuiltByScheme(t *testing.T) {
	project := settingsFixture(t, "")
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s' '[{"target":"Dependency","buildSettings":{"SWIFT_VERSION":"4"}},{"target":"Other App","buildSettings":{"SWIFT_VERSION":"5"}}]'
`
	if err := os.WriteFile(filepath.Join(dir, "xcodebuild"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var out, errout bytes.Buffer
	code := run([]string{"settings", "--project", project, "--xcode", "--scheme", "App Scheme", "--target", "Example App", "--json"}, os.Getenv, &out, &errout)
	if code == 0 || out.Len() != 0 {
		t.Fatalf("exit %d, stdout %s", code, &out)
	}
	for _, want := range []string{`scheme "App Scheme" does not build target "Example App"`, "builds targets: Dependency, Other App"} {
		if !strings.Contains(errout.String(), want) {
			t.Errorf("missing %q: %s", want, &errout)
		}
	}
}
