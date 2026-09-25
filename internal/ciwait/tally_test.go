package ciwait

import (
	"encoding/json"
	"testing"
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
