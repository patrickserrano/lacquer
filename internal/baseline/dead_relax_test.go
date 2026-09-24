package baseline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeadRelaxationIsVisibleOnOtherwiseCleanBaseline(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "6", WarningsKey: "YES"})
	for _, key := range []string{"warnings_as_errors", "strict_concurrency"} {
		t.Run(key, func(t *testing.T) {
			fs := Check(std, d, map[string]Relax{key: {Until: "2099-01-01", Reason: "migration"}}, now)
			out := Format("ios", fs)
			if !strings.Contains(out, "dead relaxation") || !strings.Contains(out, key) || !strings.Contains(out, "remove") {
				t.Fatalf("unneeded relaxation hidden: %s", out)
			}
			if len(Violations(fs)) != 0 {
				t.Fatal("dead relaxation must be report-only, like stale exclusions")
			}
		})
	}
}

func TestEveryRelaxationGetsAnEvaluation(t *testing.T) {
	lr, pr := projectDirs(t, compliantPbx)
	relax := map[string]Relax{
		"documentation":      {Until: "2099-01-01", Reason: "docs backlog"},
		"warnings_as_errors": {Until: "2099-01-01", Reason: "migration"},
	}
	reps, err := Run(lr, pr, []Target{{Profile: "ios", Component: "ios"}}, relax, now)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatReports(reps)
	for key := range relax {
		if !strings.Contains(out, key) || !strings.Contains(out, "relaxation NOT CHECKED") {
			t.Fatalf("cannot silently treat unevaluated %s as live: %s", key, out)
		}
	}
}

func TestPassingComponentDoesNotDeclareSharedRelaxationDead(t *testing.T) {
	lr, pr := projectDirs(t, compliantPbx)
	dir := filepath.Join(pr, "other", "Other.xcodeproj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project.pbxproj"), []byte(partialPbx), 0644); err != nil {
		t.Fatal(err)
	}
	targets := append(iosTarget(), Target{Profile: "ios", Component: "other", Xcodeproj: "other/Other.xcodeproj"})
	reps, err := Run(lr, pr, targets, map[string]Relax{"warnings_as_errors": {Until: "2099-01-01", Reason: "other target migration"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	out := FormatReports(reps)
	if strings.Contains(out, "dead relaxation") || !strings.Contains(out, "RELAXED") {
		t.Fatalf("shared relaxation is still needed: %s", out)
	}
}

func TestUnknownRelaxationIsNotReportedAsLive(t *testing.T) {
	d := declared(1, map[string]string{"SWIFT_VERSION": "6", WarningsKey: "$(CUSTOM_WARNING_POLICY)"})
	out := Format("ios", Check(std, d, map[string]Relax{"warnings_as_errors": {Until: "2099-01-01", Reason: "migration"}}, now))
	if !strings.Contains(out, "relaxation NOT CHECKED") || strings.Contains(out, "RELAXED") {
		t.Fatalf("unknown must stay unknown: %s", out)
	}
}
