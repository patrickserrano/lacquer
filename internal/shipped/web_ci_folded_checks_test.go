package shipped

import (
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/retire"
)

// TestWebFoldedChecksLiveInChanges: dependency review and env-schema validation
// are steps in `changes` (a job that bills nothing), not workflows of their own
// (a 4-vCPU Blacksmith job each), and CI OK reads their verdicts.
func TestWebFoldedChecksLiveInChanges(t *testing.T) {
	doc := loadShippedCI(t, "web", "ci.yml")
	changes, ok := doc.Jobs["changes"]
	if !ok {
		t.Fatal("web ci.yml has no changes job")
	}
	has := map[string]bool{}
	for _, st := range changes.Steps {
		has[st.Name] = true
	}
	for _, name := range []string{"Dependency review", "Validate .env.example against .env.schema", "Record the folded check results"} {
		if !has[name] {
			t.Errorf("changes has no %q step", name)
		}
	}

	out, err := renderShippedWorkflow(t, root(t)+"/profiles/web/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"dep_review: ${{ steps.folded.outputs.dep_review }}",
		"env_valid: ${{ steps.folded.outputs.env_valid }}",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("changes does not export %q", want)
		}
	}
	// CI OK must READ both: waiting on an output it ignores is not coverage.
	okJob := doc.Jobs["ci-ok"]
	script := ""
	for _, st := range okJob.Steps {
		script += st.Run
	}
	for _, want := range []string{"needs.changes.outputs.env_valid", "needs.changes.outputs.dep_review"} {
		if !strings.Contains(script, want) {
			t.Errorf("ci-ok never reads %s", want)
		}
	}
	// env_valid is a required check: only pass or empty may let CI OK succeed.
	if !strings.Contains(script, `"$env_valid" != "pass"`) {
		t.Error("ci-ok does not fail on an env_valid other than pass")
	}
}

// TestWebStandaloneCheckWorkflowsAreRetired: the two files are gone from the
// profile and recorded as unshipped, so a stale copy in a project is reported.
func TestWebStandaloneCheckWorkflowsAreRetired(t *testing.T) {
	for _, dest := range []string{
		".github/workflows/web-dependency-review.yml",
		".github/workflows/web-env-validation.yml",
	} {
		found := false
		for _, u := range retire.Unshipped {
			if u == dest {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not in retire.Unshipped", dest)
		}
	}
	for _, f := range []string{"dependency-review.yml", "env-validation.yml"} {
		if _, err := renderShippedWorkflow(t, root(t)+"/profiles/web/workflows/"+f); err == nil {
			t.Errorf("profiles/web/workflows/%s still exists", f)
		}
	}
}

// TestWebCIOKEnforcesFoldedChecks runs the real aggregator shell over every
// value the two folded checks can report. env_valid is a required check;
// dep_review is advisory, as the standalone workflow it replaces was (it fails
// by design where the Dependency Graph is off, which no PR can fix).
func TestWebCIOKEnforcesFoldedChecks(t *testing.T) {
	doc := loadShippedCI(t, "web", "ci.yml")
	script := ""
	for _, st := range doc.Jobs["ci-ok"].Steps {
		script += st.Run
	}
	green := map[string]string{"changes": "success", "check": "success"}
	cases := []struct {
		name     string
		env, dep string
		wantFail bool
	}{
		{"neither applied", "", "", false},
		{"both passed", "pass", "pass", false},
		{"env schema invalid", "fail", "", true},
		{"env value unrecognised", "weird", "", true},
		{"dependency review failed (advisory)", "", "fail", false},
		{"dependency review failed but env passed", "pass", "fail", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, failed := runAggregatorOutputs(t, script, green, map[string]string{
				"env_valid": tc.env, "dep_review": tc.dep,
			})
			if failed != tc.wantFail {
				t.Errorf("ci-ok failed=%v, want %v (env_valid=%q dep_review=%q)\n%s",
					failed, tc.wantFail, tc.env, tc.dep, out)
			}
		})
	}
}
