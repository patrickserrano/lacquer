package config

import (
	"strings"
	"testing"
)

// A covered_elsewhere entry removes a finding, so it must say exactly where the
// suite runs: the workflow file AND the job, both in the reason a reader sees,
// and the job as a field the audit can check. Without that it is a mute button
// with a sentence attached.

func coveredEntry(job, reason string) string {
	s := coveredBase + "\n[[project.covered_elsewhere]]\ntarget = \"WatchTests\"\nworkflow = \".github/workflows/watch-ci.yml\"\n"
	if job != "" {
		s += "job = \"" + job + "\"\n"
	}
	return s + "reason = \"" + reason + "\"\n"
}

func TestCoveredElsewhereLoadsWithAJobAndANamingReason(t *testing.T) {
	cfg, err := loadString(t, coveredEntry("watch-tests", "watch-ci.yml job watch-tests runs it on a watch simulator"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Project.CoveredElsewhere[0].Job; got != "watch-tests" {
		t.Errorf("Job = %q, want watch-tests", got)
	}
}

func TestCoveredElsewhereRequiresAJob(t *testing.T) {
	_, err := loadString(t, coveredEntry("", "watch-ci.yml runs it"))
	if err == nil || !strings.Contains(err.Error(), "needs a job") {
		t.Errorf("no job: err = %v, want one saying it needs a job", err)
	}
}

func TestCoveredElsewhereRejectsAnInvalidJob(t *testing.T) {
	_, err := loadString(t, coveredEntry("watch tests; rm", "watch-ci.yml job watch tests; rm"))
	if err == nil || !strings.Contains(err.Error(), "job") {
		t.Errorf("invalid job: err = %v, want a rejection naming the job", err)
	}
}

// The reason must name both, so the line printed beside the suppressed target
// tells a reader where to go and look.
func TestCoveredElsewhereReasonMustNameTheWorkflowAndJob(t *testing.T) {
	for name, reason := range map[string]string{
		"names neither":       "a different scheme and destination",
		"names only the job":  "job watch-tests runs it",
		"names only the file": "watch-ci.yml runs it",
	} {
		_, err := loadString(t, coveredEntry("watch-tests", reason))
		if err == nil {
			t.Errorf("%s: loaded, want a rejection", name)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "watch-ci.yml") || !strings.Contains(msg, "watch-tests") {
			t.Errorf("%s: error %q does not say what the reason must name", name, msg)
		}
	}
}

// Still required at all, as before.
func TestCoveredElsewhereStillRequiresAReason(t *testing.T) {
	_, err := loadString(t, coveredEntry("watch-tests", " "))
	if err == nil || !strings.Contains(err.Error(), "needs a reason") {
		t.Errorf("blank reason: err = %v, want it rejected", err)
	}
}
