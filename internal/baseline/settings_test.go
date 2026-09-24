package baseline

import (
	"strings"
	"testing"
)

func TestSettingUsesLayeredFixtures(t *testing.T) {
	for _, tc := range []struct{ name, project, target, base, inline, key, state, value, source string }{
		{"included custom setting", "", "#include \"Base.xcconfig\"\n", "MARKETING_VERSION = 1.2\n", "", "MARKETING_VERSION", "set", "1.2", "target xcconfig"},
		{"project config", "SWIFT_VERSION = 6\n", "", "", "", "SWIFT_VERSION", "set", "6", "project xcconfig"},
		{"project inline", "", "", "", "", "SWIFT_TREAT_WARNINGS_AS_ERRORS", "set", "NO", "project pbxproj"},
		{"target config", "", "SWIFT_TREAT_WARNINGS_AS_ERRORS = YES\n", "", "", "SWIFT_TREAT_WARNINGS_AS_ERRORS", "set", "YES", "target xcconfig"},
		{"target inline", "", "SWIFT_VERSION = 5\n", "", "SWIFT_VERSION = 6;", "SWIFT_VERSION", "set", "6", "target pbxproj"},
		{"missing declaration", "", "", "", "", "SWIFT_VERSION", "unset", "", ""},
		{"empty declaration", "", "SWIFT_VERSION =\n", "", "", "SWIFT_VERSION", "set", "", "target xcconfig"},
		{"missing include", "", "#include \"Missing.xcconfig\"\n", "", "", "SWIFT_VERSION", "unknown", "", "target xcconfig"},
		{"conditional", "", "SWIFT_VERSION[sdk=iphoneos*] = 6\n", "", "", "SWIFT_VERSION", "unknown", "", "target xcconfig"},
		{"expansion", "", "SWIFT_VERSION = $(MODE)\n", "", "", "SWIFT_VERSION", "unknown", "", "target xcconfig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pbx := strings.Replace(layeredProject, "baseConfigurationReference = TFILE;\n  buildSettings = {", "baseConfigurationReference = TFILE;\n  buildSettings = {\n   "+tc.inline, 1)
			path := layeredFixture(t, pbx, tc.project, tc.target, tc.base)
			d, err := ReadXcodeproj(path)
			if err != nil {
				t.Fatal(err)
			}
			var got Setting
			for _, c := range d.Configs {
				if c.ID == "APP" {
					got = d.Setting(c, tc.key)
				}
			}
			if got.State != tc.state || got.Value != tc.value || !strings.Contains(got.Source, tc.source) {
				t.Fatalf("got %+v", got)
			}
			if tc.state == "unknown" && got.Reason == "" {
				t.Fatal("UNKNOWN without a reason")
			}
		})
	}
}
