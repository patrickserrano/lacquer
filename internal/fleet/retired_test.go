package fleet

import (
	"bytes"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// A retired project audits clean by construction — retirement drops its
// scheduled work from the plan, so there is nothing left to be behind on. That
// makes it indistinguishable from a healthy project in every report that keys
// off "no findings", which is the one place the distinction matters: a sweep
// would count a dead project among the healthy ones, and the clean tally would
// rise every time something was abandoned.

func retiredReport(name string) Report {
	return Report{
		Name:    name,
		Path:    "/tmp/" + name,
		Retired: &config.Retirement{Since: "2026-08-18", Reason: "not a viable app"},
	}
}

func TestRetiredProjectIsNotCountedClean(t *testing.T) {
	var b bytes.Buffer
	Text(&b, []Report{retiredReport("atlas"), {Name: "kit", Path: "/tmp/kit"}})
	got := b.String()

	if !strings.Contains(got, "1 clean") {
		t.Errorf("retired project counted as clean — a sweep gets healthier every time one is abandoned:\n%s", got)
	}
	if !strings.Contains(got, "1 retired") {
		t.Errorf("no retired tally, so retired projects vanish into the total:\n%s", got)
	}
}

func TestRetiredProjectIsShownWithItsReason(t *testing.T) {
	var b bytes.Buffer
	Text(&b, []Report{retiredReport("atlas")})
	got := b.String()

	for _, want := range []string{"atlas", "retired", "2026-08-18", "not a viable app"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q — a retired project nobody can see is one nobody can decide to delete:\n%s", want, got)
		}
	}
	// It must not also render as ok; that is the exact confusion being removed.
	if strings.Contains(got, "ok    atlas") {
		t.Errorf("retired project still rendered as ok:\n%s", got)
	}
}

func TestRetiredProjectThatIsBrokenStillReportsAsBlocking(t *testing.T) {
	// Retirement means "stop spending on it", not "stop telling me it is
	// broken". A retired project whose manifest will not load must still be
	// visible as a failure, or retiring becomes a way to silence real errors.
	r := retiredReport("atlas")
	r.Error = "load manifest: unexpected EOF"

	if !r.Blocking() {
		t.Fatal("a retired project with a load error stopped blocking — retiring must not be a way to mute breakage")
	}

	var b bytes.Buffer
	Text(&b, []Report{r})
	got := b.String()
	if !strings.Contains(got, "FAIL") {
		t.Errorf("broken retired project not marked FAIL:\n%s", got)
	}
	if strings.Contains(got, "1 retired") {
		t.Errorf("broken retired project counted as retired instead of blocking:\n%s", got)
	}
}

func TestLiveProjectsReportUnchanged(t *testing.T) {
	// The retired tally must not appear when nothing is retired, or every
	// existing sweep's output changes for no reason.
	var b bytes.Buffer
	Text(&b, []Report{{Name: "kit", Path: "/tmp/kit"}})
	got := b.String()

	if strings.Contains(got, "retired") {
		t.Errorf("retired wording leaked into an all-live sweep:\n%s", got)
	}
	if !strings.Contains(got, "1 clean") {
		t.Errorf("live project stopped counting as clean:\n%s", got)
	}
}

// The wiring from manifest to report. Every other test here builds a Report by
// hand, so all of them keep passing if inspect() stops reading the manifest
// field at all — the reports simply arrive with Retired unset and look live.
// This one goes through Run(), from a real .lacquer.toml on disk.
func TestRetirementIsReadFromTheManifest(t *testing.T) {
	lq := lacquerRoot(t)
	p := project(t, "dead", `retired = { since = "2026-08-18", reason = "not a viable app" }`)
	r := find(t, Run(lq, rosterFor(t, map[string]string{"dead": p}), day("2026-08-09")), "dead")

	if !r.IsRetired() {
		t.Fatal("manifest declared [project].retired and the report came back live — the field is never read, so every retired project reports as healthy")
	}
	if r.Retired.Since != "2026-08-18" || r.Retired.Reason != "not a viable app" {
		t.Errorf("retirement details lost in transit: %+v", r.Retired)
	}
}
