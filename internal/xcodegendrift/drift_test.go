package xcodegendrift

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/gittest"
)

func TestComparisonUsesTargetAndConfigurationNotObjectIDs(t *testing.T) {
	raw := `{"objects":{"P":{"isa":"PBXProject","buildConfigurationList":"PL"},"PL":{"buildConfigurations":["PC"]},"PC":{"name":"Debug","buildSettings":{}},"A":{"isa":"PBXNativeTarget","name":"App","buildConfigurationList":"AL"},"AL":{"buildConfigurations":["AC","AR"]},"AC":{"name":"Debug","buildSettings":{"SWIFT_TREAT_WARNINGS_AS_ERRORS":"YES","FLAGS[sdk=iphoneos*]":["one","two"]}},"AR":{"name":"Release","buildSettings":{"SWIFT_TREAT_WARNINGS_AS_ERRORS":"YES"}},"T":{"isa":"PBXNativeTarget","name":"AppTests","buildConfigurationList":"TL"},"TL":{"buildConfigurations":["TC"]},"TC":{"name":"Debug","buildSettings":{"SWIFT_TREAT_WARNINGS_AS_ERRORS":"YES"}}}}`
	before, err := decodeSettings([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	after, err := decodeSettings([]byte(strings.ReplaceAll(raw, `"AC"`, `"NEW_ID"`)))
	if err != nil {
		t.Fatal(err)
	}
	if fs := compare("App.xcodeproj", before, after); len(fs) != 0 {
		t.Fatalf("IDs are not identity: %+v", fs)
	}
	delete(after[scope{"target AppTests", "Debug"}], "SWIFT_TREAT_WARNINGS_AS_ERRORS")
	delete(after[scope{"target App", "Debug"}], "FLAGS[sdk=iphoneos*]")
	after[scope{"project", "Debug"}]["NEW_SETTING"] = json.RawMessage(`"YES"`)
	fs := compare("App.xcodeproj", before, after)
	if len(fs) != 3 {
		t.Fatalf("want both directions and conditional key: %+v", fs)
	}
	out := Format(fs)
	for _, want := range []string{"target AppTests/Debug: SWIFT_TREAT_WARNINGS_AS_ERRORS present-in-committed", "target App/Debug: FLAGS[sdk=iphoneos*]", "project/Debug: NEW_SETTING absent-from-committed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
}

func TestUnreadableConfigurationGraphIsNotClean(t *testing.T) {
	for _, raw := range []string{`{}`, `garbage`, `{"objects":{"P":{"isa":"PBXProject","buildConfigurationList":"MISSING"}}}`} {
		if _, err := decodeSettings([]byte(raw)); err == nil {
			t.Fatalf("must reject %s", raw)
		}
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, raw := range map[string]string{"project.yml": "name: App\n", "App.xcodeproj/project.pbxproj": "placeholder"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Init(t, root, "-q")
	for _, args := range [][]string{{"add", "."}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	return root
}

func TestGenerationFailureIsNotCheckedAndPreservesOriginal(t *testing.T) {
	for _, behavior := range []string{"exit 9", "exit 0"} {
		t.Run(behavior, func(t *testing.T) {
			root := fixture(t)
			bin := t.TempDir()
			scripts := map[string]string{
				"xcodegen": "#!/bin/sh\nif [ \"$1\" = dump ]; then printf '{\"name\":\"App\"}'; exit; fi\n" + behavior + "\n",
				"plutil": `#!/bin/sh
for arg do path="$arg"; done
[ -f "$path" ] || exit 1
printf '%s' '{"objects":{"P":{"isa":"PBXProject","buildConfigurationList":"L"},"L":{"buildConfigurations":["C"]},"C":{"name":"Debug","buildSettings":{}}}}'
`,
			}
			for name, raw := range scripts {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(raw), 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			fs := Check(root, []baseline.Target{{Xcodeproj: "App.xcodeproj"}})
			if len(fs) != 1 || fs[0].Unchecked == "" {
				t.Fatalf("must not claim a clean comparison: %+v", fs)
			}
			raw, err := os.ReadFile(filepath.Join(root, "App.xcodeproj/project.pbxproj"))
			if err != nil || string(raw) != "placeholder" {
				t.Fatal("original changed")
			}
		})
	}
}

func TestUntrackedProjectIsOutOfScope(t *testing.T) {
	root := fixture(t)
	cmd := exec.Command("git", "rm", "--cached", "App.xcodeproj/project.pbxproj")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	if fs := Check(root, []baseline.Target{{Xcodeproj: "App.xcodeproj"}}); len(fs) != 0 {
		t.Fatalf("untracked project: %+v", fs)
	}
}

func TestCopyRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := copyInputs(root, t.TempDir(), []string{"outside"}); err == nil {
		t.Fatal("symlink could let generator write outside scratch")
	}
}

// Exercise the real OpenStep parser and XcodeGen when installed. All portable
// tests above run without either tool, including the failure and CLI wiring tests.
func TestRealXcodegenCleanAndLostSetting(t *testing.T) {
	for _, tool := range []string{"xcodegen", "plutil"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	root := fixture(t)
	spec := "name: App\ntargets:\n  App:\n    type: application\n    platform: iOS\n    settings:\n      SWIFT_VERSION: 6\n      SWIFT_TREAT_WARNINGS_AS_ERRORS: YES\n"
	path := filepath.Join(root, "project.yml")
	if err := os.WriteFile(path, []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("xcodegen", "generate")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, out)
	}
	targets := []baseline.Target{{Xcodeproj: "App.xcodeproj"}}
	if fs := Check(root, targets); len(fs) != 0 {
		t.Fatalf("clean generation: %+v", fs)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(spec, "      SWIFT_TREAT_WARNINGS_AS_ERRORS: YES\n", "")), 0644); err != nil {
		t.Fatal(err)
	}
	fs := Check(root, targets)
	if len(fs) != 2 {
		t.Fatalf("want Debug and Release losses: %+v", fs)
	}
	for _, f := range fs {
		if f.Setting != "SWIFT_TREAT_WARNINGS_AS_ERRORS" || f.Direction != "present-in-committed, absent-from-generated" {
			t.Fatalf("wrong drift: %+v", f)
		}
	}
}

func TestGenerationHooksAndEscapingOutputsAreNotRun(t *testing.T) {
	scratch := t.TempDir()
	dir := filepath.Join(scratch, "ios")
	for _, raw := range []string{
		`{"name":"App","options":{"preGenCommand":"touch /outside"}}`,
		`{"name":"App","options":{"postGenCommand":"touch /outside"}}`,
		`{"name":"App","targets":{"App":{"info":{"path":"../../outside.plist"}}}}`,
		`{"name":"App","targets":{"App":{"entitlements":{"path":"/outside.entitlements"}}}}`,
		`{"name":"../../Outside"}`,
	} {
		if err := safeSpec([]byte(raw), dir, scratch); err == nil {
			t.Fatalf("unsafe generation accepted: %s", raw)
		}
	}
	if err := safeSpec([]byte(`{"name":"App","targets":{"App":{"info":{"path":"../App/Info.plist"}}}}`), dir, scratch); err != nil {
		t.Fatalf("in-copy output rejected: %v", err)
	}
}

func TestMissingGeneratorIsNotChecked(t *testing.T) {
	root := fixture(t)
	bin := t.TempDir()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	fs := Check(root, []baseline.Target{{Xcodeproj: "App.xcodeproj"}})
	if len(fs) != 1 || !strings.Contains(fs[0].Unchecked, "xcodegen unavailable") {
		t.Fatalf("missing tool must not look clean: %+v", fs)
	}
}

func TestProjectPathCannotEscapeScratch(t *testing.T) {
	for _, path := range []string{"../Outside.xcodeproj", "/Outside.xcodeproj", "."} {
		if _, err := checkProject(t.TempDir(), path); err == nil || !strings.Contains(err.Error(), "cannot isolate project path") {
			t.Fatalf("unsafe project path %q: %v", path, err)
		}
	}
}
