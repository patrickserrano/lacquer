package main

import (
	"bytes"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditReportsXcodegenSettingsLostOnRegeneration(t *testing.T) {
	hr, pr := auditFixture(t, pbxCompliant, "")
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(pr, "ios", "project.yml"), "name: App\n")
	gittest.Init(t, pr, "-q")
	for _, args := range [][]string{{"add", "."}} {
		c := exec.Command("git", args...)
		c.Dir = pr
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	bin := t.TempDir()
	// Distinct snapshots: plutil normalizes the serialized project, while the
	// generator drops the warning policy only from its isolated output.
	script := "#!/bin/sh\nif [ \"$1\" = dump ]; then printf '{\"name\":\"App\"}'; exit; fi\nmkdir -p App.xcodeproj\nprintf generated > App.xcodeproj/project.pbxproj\n"
	if err := os.WriteFile(filepath.Join(bin, "xcodegen"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	plist := `#!/bin/sh
for arg do path="$arg"; done
setting='"SWIFT_TREAT_WARNINGS_AS_ERRORS":"YES",'
if [ "$(cat "$path")" = generated ]; then setting=''; fi
printf '{"objects":{"P":{"isa":"PBXProject","buildConfigurationList":"L"},"L":{"buildConfigurations":["C"]},"C":{"name":"Debug","buildSettings":{%s"SWIFT_VERSION":"6"}}}}' "$setting"
`
	if err := os.WriteFile(filepath.Join(bin, "plutil"), []byte(plist), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	chdir(t, pr)
	var out, errb bytes.Buffer
	code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
	if code != 0 {
		t.Fatalf("report-only check exited %d: %s", code, errb.String())
	}
	for _, want := range []string{"XcodeGen", "project/Debug", "SWIFT_TREAT_WARNINGS_AS_ERRORS", "present-in-committed, absent-from-generated"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q: %s", want, out.String())
		}
	}
	raw, err := os.ReadFile(filepath.Join(pr, "ios", "App.xcodeproj", "project.pbxproj"))
	if err != nil || string(raw) != pbxCompliant {
		t.Fatal("audit changed the original project")
	}
}

func TestXcodegenOnlyDoesNotRunOtherAuditGates(t *testing.T) {
	hr, pr := auditFixture(t, pbxPartialWerror, "")
	chdir(t, pr)
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--xcodegen-only"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb); code != 0 {
		t.Fatalf("report-only command exited %d: %s %s", code, out.String(), errb.String())
	}
	if strings.Contains(out.String(), "baseline") {
		t.Fatalf("unrelated baseline leaked into report-only CI command: %s", out.String())
	}
}
