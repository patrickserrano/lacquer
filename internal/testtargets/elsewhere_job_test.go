package testtargets

import (
	"strings"
	"testing"
)

// The declared job must exist in the workflow. A workflow that runs the suite in
// a job other than the one named is a declaration nobody can follow; one with
// no such job at all is not true.
func TestCoveredElsewhereNamingAJobTheWorkflowLacksIsUnconfirmed(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})

	right := decl()
	right[0].Job = "watch-tests"
	if r := report(t, dir, []string{"AlphaAppTests"}, right, nil); uncovered(r, watchTarget) {
		t.Fatalf("the declaration naming the real job did not verify: %+v", r.Unconfirmed)
	}

	wrong := decl()
	wrong[0].Job = "lint"
	r := report(t, dir, []string{"AlphaAppTests"}, wrong, nil)
	if !uncovered(r, watchTarget) {
		t.Fatal("a declaration naming a job the workflow does not have still suppressed the finding")
	}
	if len(r.Unconfirmed) != 1 || !strings.Contains(strings.Join(r.Unconfirmed[0].Problems, "\n"), `has no job "lint"`) {
		t.Errorf("the failed job check is not named: %+v", r.Unconfirmed)
	}
}

func TestMissingWorkflowsListsOnlyAbsentFiles(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})
	got := MissingWorkflows(dir, []Declaration{
		{Target: "A", Workflow: ".github/workflows/watch-ci.yml"},
		{Target: "B", Workflow: ".github/workflows/gone.yml"},
	})
	if len(got) != 1 || got[0].Target != "B" {
		t.Errorf("MissingWorkflows = %+v, want only B", got)
	}
}
