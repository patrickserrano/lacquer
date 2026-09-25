package ciwait

import (
	"encoding/json"
	"testing"
	"time"
)

// The counts come from the classifier Wait uses, so the two cannot disagree
// about a check. Legacy commit statuses (Vercel's) are read by state: SUCCESS
// passes, FAILURE and ERROR fail, PENDING and EXPECTED are pending.
func TestTallyClassifiesEveryNodeShape(t *testing.T) {
	roll := `[
	 {"__typename":"CheckRun","name":"build","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"https://x/runs/2"},
	 {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SKIPPED"},
	 {"__typename":"CheckRun","name":"docs","status":"COMPLETED","conclusion":"NEUTRAL"},
	 {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"},
	 {"__typename":"CheckRun","name":"slow","status":"IN_PROGRESS","conclusion":""},
	 {"__typename":"StatusContext","context":"Vercel","state":"SUCCESS"},
	 {"__typename":"StatusContext","context":"a","state":"FAILURE"},
	 {"__typename":"StatusContext","context":"b","state":"ERROR"},
	 {"__typename":"StatusContext","context":"c","state":"PENDING"},
	 {"__typename":"StatusContext","context":"d","state":"EXPECTED"}]`
	got, err := Tally(json.RawMessage(roll))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Counts{Passing: 4, Failing: 3, Pending: 3}); got != want {
		t.Errorf("Tally = %+v, want %+v", got, want)
	}
}

func TestTallyOfNoRollupIsZeroAndOfGarbageIsAnError(t *testing.T) {
	for _, r := range []string{"", "null", "[]", "  null "} {
		if got, err := Tally(json.RawMessage(r)); err != nil || got != (Counts{}) {
			t.Errorf("Tally(%q) = %+v, %v", r, got, err)
		}
	}
	if _, err := Tally(json.RawMessage(`{"not":"an array"}`)); err == nil {
		t.Error("garbage was counted as no checks")
	}
}

// A run superseded by a newer one of the same workflow does not count.
func TestTallyIgnoresASupersededRun(t *testing.T) {
	roll := `[
	 {"__typename":"CheckRun","name":"test","workflowName":"CI","status":"COMPLETED","conclusion":"CANCELLED","detailsUrl":"https://github.com/o/r/actions/runs/1/job/9"},
	 {"__typename":"CheckRun","name":"test","workflowName":"CI","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"https://github.com/o/r/actions/runs/2/job/9"}]`
	if got, _ := Tally(json.RawMessage(roll)); got != (Counts{Passing: 1}) {
		t.Errorf("Tally = %+v, want only the newest run", got)
	}
}

// Failing checks come back with when they finished, which is what "failing
// since" is measured from. A commit status has no completion time, so it
// carries the time it was set.
func TestTallyFailuresReportsWhenEachFailingCheckFinished(t *testing.T) {
	roll := `[
	 {"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","startedAt":"2026-09-25T08:00:00Z","completedAt":"2026-09-25T08:10:00Z"},
	 {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-25T08:11:00Z"},
	 {"__typename":"StatusContext","context":"vercel","state":"FAILURE","startedAt":"2026-09-25T07:30:00Z"},
	 {"__typename":"CheckRun","name":"slow","status":"IN_PROGRESS","conclusion":""}]`
	c, failed, err := TallyFailures(json.RawMessage(roll))
	if err != nil {
		t.Fatal(err)
	}
	if c != (Counts{Passing: 1, Failing: 2, Pending: 1}) {
		t.Errorf("counts = %+v", c)
	}
	if len(failed) != 2 || failed[0].Name != "test" || failed[1].Name != "vercel" {
		t.Fatalf("failed = %+v", failed)
	}
	if got := failed[0].CompletedAt.UTC().Format(time.RFC3339); got != "2026-09-25T08:10:00Z" {
		t.Errorf("a check run's time = %s, want its completedAt, not its startedAt", got)
	}
	if got := failed[1].CompletedAt.UTC().Format(time.RFC3339); got != "2026-09-25T07:30:00Z" {
		t.Errorf("a commit status's time = %s", got)
	}
	// A failure with no time says so with the zero time. It is never invented.
	_, failed, _ = TallyFailures(json.RawMessage(`[{"__typename":"CheckRun","name":"x","status":"COMPLETED","conclusion":"FAILURE"}]`))
	if len(failed) != 1 || !failed[0].CompletedAt.IsZero() {
		t.Errorf("failed = %+v, want one with no time", failed)
	}
}
