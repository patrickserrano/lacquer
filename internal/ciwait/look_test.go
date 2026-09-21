package ciwait

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func lookFrom(out string, err error) Runner {
	return func(context.Context, ...string) ([]byte, error) { return []byte(out), err }
}

// Look must read the way Wait does: a cancelled job from a superseded run on the
// same commit is not a failure of a green PR. This is #435's trap, and the round
// counter would otherwise charge an agent a round for a check that passed.
func TestLookDropsSupersededRuns(t *testing.T) {
	out := `{"state":"OPEN","headRefOid":"abc","statusCheckRollup":[
	 {"__typename":"CheckRun","workflowName":"CI","name":"test","status":"COMPLETED","conclusion":"CANCELLED","detailsUrl":"https://github.com/o/r/actions/runs/1/job/1"},
	 {"__typename":"CheckRun","workflowName":"CI","name":"test","status":"COMPLETED","conclusion":"SUCCESS","detailsUrl":"https://github.com/o/r/actions/runs/2/job/1"}]}`
	r, err := Look(context.Background(), lookFrom(out, nil), "o/r", 7, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Failed(); len(got) != 0 {
		t.Fatalf("a superseded cancelled run read as a failure: %v", got)
	}
	if r.Head != "abc" || r.State != "OPEN" || len(r.Checks) != 1 {
		t.Errorf("unexpected reading: %+v", r)
	}
}

func TestLookNamesFailuresAndRunning(t *testing.T) {
	out := `{"state":"OPEN","headRefOid":"abc","statusCheckRollup":[
	 {"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"FAILURE"},
	 {"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":""}]}`
	r, err := Look(context.Background(), lookFrom(out, nil), "", 7, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if f := r.Failed(); len(f) != 1 || f[0].Name != "lint" {
		t.Errorf("Failed() = %v, want [lint]", f)
	}
	if run := r.Running(); len(run) != 1 || run[0].Name != "test" {
		t.Errorf("Running() = %v, want [test]", run)
	}
}

// gh not telling us anything is an error, never an empty (or green) reading.
func TestLookErrorsAreErrors(t *testing.T) {
	if _, err := Look(context.Background(), lookFrom("", errors.New("boom")), "", 7, time.Now()); err == nil {
		t.Error("a gh failure was not an error")
	}
	_, err := Look(context.Background(), lookFrom(`{"state":"OPEN","headRefOid":"a"}`, nil), "", 7, time.Now())
	if err == nil || !strings.Contains(err.Error(), "statusCheckRollup") {
		t.Errorf("a missing rollup was not an error: %v", err)
	}
}
