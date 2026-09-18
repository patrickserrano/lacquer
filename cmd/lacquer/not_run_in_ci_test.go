package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pbxCompliant plus one unit-test target nothing selects, so the fixture has a
// suite for a [[project.not_run_in_ci]] declaration to speak for.
const pbxWithCoreTests = pbxCompliant + `
		CORE /* AppCoreTests */ = {
			isa = PBXNativeTarget;
			name = AppCoreTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
`

func notRunManifest(until string) string {
	return "\n[[project.not_run_in_ci]]\ntarget = \"AppCoreTests\"\n" +
		"reason = \"needs on-device models; run on device before release\"\nuntil = \"" + until + "\"\n"
}

// The wiring test. internal/testtargets decides correctly in its own tests; this
// proves `audit` actually asks it, and that an expired declaration reaches the
// exit code — the part that can break with every unit test still green, turning
// a time-boxed exemption into a permanent one.
func TestAuditHonoursNotRunInCI(t *testing.T) {
	audit := func(t *testing.T, pbx, extra string) (int, string) {
		t.Helper()
		hr, pr := auditFixture(t, pbx, extra)
		chdir(t, pr)
		var out, errb bytes.Buffer
		code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
		return code, out.String() + errb.String()
	}

	t.Run("undeclared: uncovered, exit 0", func(t *testing.T) {
		code, out := audit(t, pbxWithCoreTests, "")
		if code != 0 || !strings.Contains(uncoveredSection(out), "AppCoreTests") {
			t.Fatalf("control: exit %d, want 0 with AppCoreTests uncovered:\n%s", code, out)
		}
	})

	t.Run("in term: its own line, exit 0", func(t *testing.T) {
		code, out := audit(t, pbxWithCoreTests, notRunManifest("2099-12-31"))
		if code != 0 {
			t.Fatalf("exit %d, want 0:\n%s", code, out)
		}
		if strings.Contains(uncoveredSection(out), "AppCoreTests") {
			t.Errorf("the declared suite is still reported as uncovered:\n%s", out)
		}
		want := "deliberately not run in CI: AppCoreTests — needs on-device models; run on device before release (until 2099-12-31)"
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain\n  %s\n%s", want, out)
		}
	})

	t.Run("expired: back in the report, exit 4", func(t *testing.T) {
		code, out := audit(t, pbxWithCoreTests, notRunManifest("2020-01-01"))
		if code != 4 {
			t.Fatalf("exit %d, want 4:\n%s", code, out)
		}
		if !strings.Contains(uncoveredSection(out), "AppCoreTests") || !strings.Contains(out, "EXPIRED 2020-01-01") {
			t.Errorf("the expired suite is not back in the report, named as expired:\n%s", out)
		}
	})

	// A manifest naming an .xcodeproj that does not exist (multimeter's state)
	// skips the whole comparison. The declaration's date must not be skipped with
	// it, or an expiry silently never fires for as long as the project stays
	// unreadable.
	t.Run("expired, project absent: still exit 4", func(t *testing.T) {
		hr, pr := auditFixture(t, pbxWithCoreTests, notRunManifest("2020-01-01"))
		if err := os.RemoveAll(filepath.Join(pr, "ios", "App.xcodeproj")); err != nil {
			t.Fatal(err)
		}
		chdir(t, pr)
		var out, errb bytes.Buffer
		code := run([]string{"audit"}, envMap(map[string]string{"LACQUER_ROOT": hr}), &out, &errb)
		if code != 4 || !strings.Contains(out.String(), "EXPIRED 2020-01-01") {
			t.Fatalf("exit %d, want 4 naming the expiry:\n%s%s", code, out.String(), errb.String())
		}
		if strings.Contains(out.String(), "not doing anything") {
			t.Errorf("an unreadable project made the declaration stale — could not look is not missing:\n%s", out.String())
		}
	})
}
