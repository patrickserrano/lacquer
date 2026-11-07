package config

import (
	"strings"
	"testing"
)

func TestCITimeoutValidation(t *testing.T) {
	for _, tc := range []struct{ name, settings, want string }{
		{"defaults", "", ""},
		{"custom", "ios_test_job_timeout_minutes = 90\nios_test_step_timeout_minutes = 60", ""},
		{"boundary", "ios_test_job_timeout_minutes = 360\nios_test_step_timeout_minutes = 360", ""},
		{"job only", "ios_test_job_timeout_minutes = 30", ""},
		{"step only", "ios_test_step_timeout_minutes = 15", ""},
		{"zero", "ios_test_step_timeout_minutes = 0", "ios_test_step_timeout_minutes"},
		{"negative", "ios_test_job_timeout_minutes = -1", "ios_test_job_timeout_minutes"},
		{"too large", "ios_test_job_timeout_minutes = 361", "ios_test_job_timeout_minutes"},
		{"step too large", "ios_test_step_timeout_minutes = 361", "ios_test_step_timeout_minutes"},
		{"fraction", "ios_test_step_timeout_minutes = 1.5", "ios_test_step_timeout_minutes"},
		{"string", "ios_test_job_timeout_minutes = \"30\"", "ios_test_job_timeout_minutes"},
		{"bool", "ios_test_job_timeout_minutes = true", "ios_test_job_timeout_minutes"},
		{"step exceeds job", "ios_test_job_timeout_minutes = 30\nios_test_step_timeout_minutes = 31", "must not exceed"},
		{"step exceeds default job", "ios_test_step_timeout_minutes = 21", "must not exceed"},
		{"default step exceeds job", "ios_test_job_timeout_minutes = 9", "must not exceed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, "[project]\nname = \"demo\"\n[ci]\n"+tc.settings+"\n")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestIOSPackageResolvedValidation(t *testing.T) {
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"ci_scripts/Package.resolved", true}, {"Config/Package.resolved", true},
		{"CI Config/Package.resolved", true},
		{"../Package.resolved", false}, {"/tmp/Package.resolved", false},
		{"$(touch bad)/Package.resolved", false}, {".", false},
		{"Demo.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, err := loadString(t, "[project]\nname = \"demo\"\nxcodeproj = \"Demo.xcodeproj\"\n[ios]\npackage_resolved = '"+tc.path+"'\n")
			if (err == nil) != tc.valid {
				t.Fatalf("error = %v, valid = %v", err, tc.valid)
			}
		})
	}
}

func TestLintRelaxationValidation(t *testing.T) {
	for _, key := range []string{"lint", "format"} {
		for _, tc := range []struct{ value, want string }{
			{`{ until = "2099-01-01", reason = "existing lint debt" }`, ""},
			{`{ until = "2000-01-01", reason = "expired debt" }`, ""},
			{`{ reason = "no expiry" }`, "until"},
			{`{ until = "2099-01-01" }`, "reason"},
			{`{ until = "not-a-date", reason = "debt" }`, "until"},
		} {
			_, err := loadString(t, "[project]\nname = 'demo'\n[baseline.relax]\n"+key+" = "+tc.value+"\n")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		}
	}
}
