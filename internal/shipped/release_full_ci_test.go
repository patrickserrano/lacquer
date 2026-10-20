package shipped

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// D5 (approved 2026-10-04, amended 2026-10-19): the release SHA needs a FULL,
// green CI OK from the iOS CI workflow. The gate used to accept ANY check run
// named "CI OK" on the SHA, so a web or supabase CI OK let a tag through
// (#508), and once selection ships a selected run would have too.
//
// These tests execute the rendered gate against a fake `gh` and fixture JSON,
// because every decision it makes is a comparison inside the script. They sit
// beside release_provenance_test.go, whose assertions are unchanged.
//
// When the SHA has no iOS CI run at all (the push path filter skipped a commit
// that touched no Swift component), the gate dispatches a FULL iOS CI run on
// the release ref and waits. workflow_dispatch takes a REF, not a SHA, so the
// dispatched run is identified by a nonce it carries in its run name, never by
// "the latest run", and its head_sha must equal the release SHA.

const (
	pushRunID  = 201
	dispatchID = 301
)

// provenanceStep returns the rendered gate's script and env.
func provenanceStep(t *testing.T) reuseStep {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []reuseStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(renderRelease(t, soloProject())), &doc); err != nil {
		t.Fatal(err)
	}
	for _, st := range doc.Jobs["verify-ci-provenance"].Steps {
		if st.Name == "Require a passing CI OK for this exact commit" {
			return st
		}
	}
	t.Fatal("verify-ci-provenance has no 'Require a passing CI OK for this exact commit' step")
	return reuseStep{}
}

func iosRun(id int, sha, event, status, conclusion string) map[string]any {
	return map[string]any{
		"id": id, "head_sha": sha, "event": event, "status": status, "conclusion": conclusion,
		"path": ".github/workflows/ios-ci.yml", "html_url": "https://github.com/acme/demo/actions/runs/" + itoa(id),
		"display_title": "a commit message",
	}
}

func checkRuns(sha string, suites ...int) map[string]any {
	list := []map[string]any{}
	for _, s := range suites {
		list = append(list, map[string]any{
			"name": "CI OK", "head_sha": sha, "status": "completed", "conclusion": "success",
			"check_suite": map[string]any{"id": s},
		})
	}
	return map[string]any{"total_count": len(list), "check_runs": list}
}

// recordFor routes run id's artifact listing and archive to a record.
func recordFor(t *testing.T, f *fakeAPI, run int, record map[string]any) {
	t.Helper()
	art := 600 + run
	f.on("GET /repos/acme/demo/actions/runs/"+itoa(run)+"/artifacts?name=test-selection&per_page=100", "200", artifacts(art))
	f.on("GET /repos/acme/demo/actions/artifacts/"+itoa(art)+"/zip", "200", zipped(t, "test-selection.json", record))
}

// greenRun routes run id as a completed iOS CI run with a green CI OK job.
func greenRun(f *fakeAPI, run map[string]any) {
	id := itoa(run["id"].(int))
	f.on("GET /repos/acme/demo/actions/runs/"+id, "200", run)
	f.on("GET /repos/acme/demo/actions/runs/"+id+"/jobs?filter=latest&per_page=100", "200", ciOKJobs(run["conclusion"].(string)))
}

// fullPushFixture: the SHA's iOS CI push run was FULL and green.
func fullPushFixture(t *testing.T, sha, tree string) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.on("GET /repos/acme/demo/commits/"+sha+"/check-runs?per_page=100", "200", checkRuns(sha, 900))
	run := iosRun(pushRunID, sha, "push", "completed", "success")
	f.on("GET /repos/acme/demo/actions/runs?check_suite_id=900", "200", map[string]any{"workflow_runs": []any{run}})
	greenRun(f, run)
	recordFor(t, f, pushRunID, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "full"})
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?head_sha="+sha+"&per_page=100", "200",
		map[string]any{"workflow_runs": []any{run}})
	return f
}

// noIOSRunFixture: the SHA has no iOS CI run at all, so the gate dispatches.
// The dispatched run appears carrying the nonce and goes in_progress →
// completed, FULL and green, on the release SHA.
func noIOSRunFixture(t *testing.T, sha, tree string) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.on("GET /repos/acme/demo/commits/"+sha+"/check-runs?per_page=100", "200", checkRuns(sha))
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?head_sha="+sha+"&per_page=100", "200",
		map[string]any{"workflow_runs": []any{}})
	f.on("POST /repos/acme/demo/actions/workflows/ios-ci.yml/dispatches", "204", nil)
	dispatched := iosRun(dispatchID, sha, "workflow_dispatch", "completed", "success")
	dispatched["display_title"] = "iOS CI provenance @@NONCE@@"
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{dispatched}})
	queued := iosRun(dispatchID, sha, "workflow_dispatch", "in_progress", "")
	f.seq("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID), queued, dispatched)
	f.on("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID)+"/jobs?filter=latest&per_page=100", "200", ciOKJobs("success"))
	recordFor(t, f, dispatchID, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "full"})
	return f
}

// runProvenance executes the gate as a tag push of sha.
func runProvenance(t *testing.T, f *fakeAPI, dir, sha string, overrides map[string]string) stepResult {
	t.Helper()
	step := provenanceStep(t)
	env := map[string]string{
		"GH_TOKEN":           "fake-token",
		"EXPECTED_SHA":       sha,
		"DEFAULT_BRANCH":     "main",
		"GITHUB_REPOSITORY":  fakeRepo,
		"GITHUB_REF":         "refs/tags/v1.2.3",
		"GITHUB_RUN_ID":      "4242",
		"GITHUB_RUN_ATTEMPT": "1",
		// Bounds shrunk so a fail path proves itself in seconds; the shipped
		// defaults are asserted in TestProvenanceWaitIsBounded.
		"PROVENANCE_API_TIMEOUT":    "8",
		"PROVENANCE_POLL_INTERVAL":  "0",
		"PROVENANCE_APPEAR_TIMEOUT": "2",
		"PROVENANCE_WAIT_TIMEOUT":   "3",
	}
	for k, v := range overrides {
		env[k] = v
	}
	for _, k := range []string{"GH_TOKEN", "EXPECTED_SHA", "DEFAULT_BRANCH"} {
		if _, ok := step.Env[k]; !ok {
			t.Fatalf("the provenance step's env does not define %s", k)
		}
	}
	return f.run(step.Run, dir, env)
}

func assertReleased(t *testing.T, res stepResult) {
	t.Helper()
	if res.failed {
		t.Fatalf("the release was BLOCKED, want it through:\n%s", res.out)
	}
	if !strings.Contains(res.out, "Release provenance verified") {
		t.Errorf("the gate passed without saying it verified anything:\n%s", res.out)
	}
}

// assertBlocked asserts the release was refused, and refused for the reason
// named: a gate that blocks for some other reason (a fixture it cannot read, a
// tool that broke) would pass a bare "it failed" while the path under test
// never ran.
func assertBlocked(t *testing.T, res stepResult, reason string) {
	t.Helper()
	if !res.failed {
		t.Fatalf("the release went THROUGH, want it blocked:\n%s", res.out)
	}
	if !strings.Contains(res.out, reason) {
		t.Errorf("the release was blocked, but not because %q:\n%s", reason, res.out)
	}
	if strings.Contains(res.out, "Release provenance verified") {
		t.Errorf("a blocked release still printed the verified line:\n%s", res.out)
	}
}

func dispatched(f *fakeAPI) bool {
	for _, c := range f.calls() {
		if strings.HasPrefix(c, "POST_") && strings.Contains(c, "/dispatches") {
			return true
		}
	}
	return false
}

func TestReleaseAcceptsAFullGreenIOSPushRun(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := fullPushFixture(t, sha, tree)
	assertReleased(t, runProvenance(t, f, dir, sha, nil))
	if dispatched(f) {
		t.Error("the gate dispatched a run although the SHA already had a FULL green one")
	}
}

// The decision's fail path: "a non-FULL ... CI OK must not let a release
// through." The green non-FULL run is not accepted; D5's "otherwise" applies
// and a full run is dispatched, which here the API refuses.
func TestReleaseRefusesANonFullCIOK(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	for _, mode := range []string{"selected", "none", ""} {
		t.Run("mode="+mode, func(t *testing.T) {
			f := fullPushFixture(t, sha, tree)
			recordFor(t, f, pushRunID, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": mode})
			res := runProvenance(t, f, dir, sha, nil)
			assertBlocked(t, res, "dispatching one on refs/tags/v1.2.3 failed")
			if !strings.Contains(res.out, "ran mode '"+mode+"'") {
				t.Errorf("the gate does not say why the green run was not accepted:\n%s", res.out)
			}
		})
	}
}

// A green iOS push run that cannot be shown to be FULL is re-verified, not
// trusted and not a dead end: its record expired (an artifact retention limit
// can be far shorter than the 30 days requested), or it ran less than the full
// suite. The gate dispatches a FULL run of the same commit, and that run's
// verdict decides.
func TestReleaseReverifiesAGreenRunItCannotProveFull(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	for name, record := range map[string]func(f *fakeAPI){
		"record expired": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/runs/"+itoa(pushRunID)+"/artifacts?name=test-selection&per_page=100", "200",
				map[string]any{"artifacts": []map[string]any{{"id": 801, "name": "test-selection", "expired": true}}})
		},
		"record says selected": func(f *fakeAPI) {
			recordFor(t, f, pushRunID, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "selected"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := noIOSRunFixture(t, sha, tree)
			f.on("GET /repos/acme/demo/commits/"+sha+"/check-runs?per_page=100", "200", checkRuns(sha, 900))
			push := iosRun(pushRunID, sha, "push", "completed", "success")
			f.on("GET /repos/acme/demo/actions/runs?check_suite_id=900", "200", map[string]any{"workflow_runs": []any{push}})
			greenRun(f, push)
			f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?head_sha="+sha+"&per_page=100", "200",
				map[string]any{"workflow_runs": []any{push}})
			record(f)
			assertReleased(t, runProvenance(t, f, dir, sha, nil))
			if !dispatched(f) {
				t.Error("an unprovable green run was trusted without a FULL run behind it")
			}
		})
	}
}

// The decision's other fail path, and #508 itself: a green CI OK from another
// workflow, and none from iOS CI. The gate must not accept it; with no iOS run
// on the SHA it dispatches one, and here that run never appears.
func TestReleaseRefusesAWrongWorkflowCIOK(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	for _, wf := range []string{"web-ci.yml", "supabase-ci.yml"} {
		t.Run(wf, func(t *testing.T) {
			f := noIOSRunFixture(t, sha, tree)
			f.on("GET /repos/acme/demo/commits/"+sha+"/check-runs?per_page=100", "200", checkRuns(sha, 950))
			other := iosRun(250, sha, "push", "completed", "success")
			other["path"] = ".github/workflows/" + wf
			f.on("GET /repos/acme/demo/actions/runs?check_suite_id=950", "200", map[string]any{"workflow_runs": []any{other}})
			greenRun(f, other)
			recordFor(t, f, 250, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "full"})
			f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
				map[string]any{"workflow_runs": []any{}})
			res := runProvenance(t, f, dir, sha, nil)
			assertBlocked(t, res, "did not appear")
			if !dispatched(f) {
				t.Error("the gate never looked past the other workflow's CI OK for an iOS run")
			}
		})
	}
}

// A pull-request run never proves a release, even on the same SHA (a
// fast-forward or rebase merge keeps it).
func TestReleaseNeverCountsAPullRequestRun(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	f.on("GET /repos/acme/demo/commits/"+sha+"/check-runs?per_page=100", "200", checkRuns(sha, 960))
	pr := iosRun(260, sha, "pull_request", "completed", "success")
	f.on("GET /repos/acme/demo/actions/runs?check_suite_id=960", "200", map[string]any{"workflow_runs": []any{pr}})
	greenRun(f, pr)
	recordFor(t, f, 260, map[string]any{"pr": 7, "head": sha, "tree": tree, "mode": "full"})
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{}})
	assertBlocked(t, runProvenance(t, f, dir, sha, nil), "did not appear")
	if !dispatched(f) {
		t.Error("a pull-request run stood in for the iOS push run the release needs")
	}
}

func TestReleaseRefusesAMissingRecord(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := fullPushFixture(t, sha, tree)
	f.on("GET /repos/acme/demo/actions/runs/"+itoa(pushRunID)+"/artifacts?name=test-selection&per_page=100", "200", artifacts())
	assertBlocked(t, runProvenance(t, f, dir, sha, nil), "has 0 test-selection records")
}

func TestReleaseRefusesARecordForAnotherTree(t *testing.T) {
	dir, sha, _ := commitRepo(t)
	f := fullPushFixture(t, sha, "5555555555555555555555555555555555555555")
	assertBlocked(t, runProvenance(t, f, dir, sha, nil), "record is for tree 5555555555555555555555555555555555555555")
}

// An iOS push run exists but is red: its verdict stands. Dispatching a fresh
// run would let a flaky pass launder a failure.
func TestReleaseRefusesARedIOSPushRunWithoutDispatching(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := fullPushFixture(t, sha, tree)
	f.on("GET /repos/acme/demo/commits/"+sha+"/check-runs?per_page=100", "200", map[string]any{"check_runs": []map[string]any{{
		"name": "CI OK", "head_sha": sha, "status": "completed", "conclusion": "failure", "check_suite": map[string]any{"id": 900},
	}}})
	red := iosRun(pushRunID, sha, "push", "completed", "failure")
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?head_sha="+sha+"&per_page=100", "200",
		map[string]any{"workflow_runs": []any{red}})
	assertBlocked(t, runProvenance(t, f, dir, sha, nil), "has no FULL, green CI OK from iOS CI")
	if dispatched(f) {
		t.Error("a red iOS push run was answered by dispatching a new one")
	}
}

// A push run that reused a PR run (D4b) proves the release only through that
// PR run: FULL, green, iOS CI, the same tree.
func reusedPushFixture(t *testing.T, sha, tree string) *fakeAPI {
	t.Helper()
	f := fullPushFixture(t, sha, tree)
	recordFor(t, f, pushRunID, map[string]any{
		"pr": nil, "head": sha, "tree": tree, "mode": "reused", "reused_run": prRunURL, "reused_run_id": 101,
	})
	pr := iosRun(101, prHead, "pull_request", "completed", "success")
	greenRun(f, pr)
	recordFor(t, f, 101, map[string]any{"pr": 7, "head": prHead, "tree": tree, "mode": "full"})
	return f
}

func TestReleaseAcceptsAReusedFullPushRun(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	assertReleased(t, runProvenance(t, reusedPushFixture(t, sha, tree), dir, sha, nil))
}

var reuseReasons = map[string]string{
	"reused run was selected":        "which ran mode 'selected'",
	"reused run tested another tree": "which tested another tree",
	"reused run was red":             "run 101 is completed/failure",
	"reused run is another workflow": "not the iOS CI workflow",
	"reused run is not named":        "reused a run it does not name",
}

func TestReleaseRefusesAReuseOfANonFullRun(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	cases := map[string]func(f *fakeAPI){
		"reused run was selected": func(f *fakeAPI) {
			recordFor(t, f, 101, map[string]any{"pr": 7, "head": prHead, "tree": tree, "mode": "selected"})
		},
		"reused run tested another tree": func(f *fakeAPI) {
			recordFor(t, f, 101, map[string]any{"pr": 7, "head": prHead, "tree": "6666666666666666666666666666666666666666", "mode": "full"})
		},
		"reused run was red": func(f *fakeAPI) { greenRun(f, iosRun(101, prHead, "pull_request", "completed", "failure")) },
		"reused run is another workflow": func(f *fakeAPI) {
			r := iosRun(101, prHead, "pull_request", "completed", "success")
			r["path"] = ".github/workflows/web-ci.yml"
			greenRun(f, r)
		},
		"reused run is not named": func(f *fakeAPI) {
			recordFor(t, f, pushRunID, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "reused"})
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := reusedPushFixture(t, sha, tree)
			breakIt(f)
			assertBlocked(t, runProvenance(t, f, dir, sha, nil), reuseReasons[name])
		})
	}
}

// No iOS run on the SHA: dispatch, find the run by its nonce, check its SHA,
// wait, then apply the same acceptance rules.
func TestReleaseDispatchesAFullRunWhenTheSHAHasNone(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	res := runProvenance(t, f, dir, sha, nil)
	assertReleased(t, res)
	if !dispatched(f) {
		t.Fatal("the gate released without dispatching, though the SHA had no iOS run")
	}
	fields := f.fields()
	if !strings.Contains(fields, "ref=refs/tags/v1.2.3") {
		t.Errorf("the dispatch did not target the release ref; fields:\n%s", fields)
	}
	if !strings.Contains(fields, "inputs[dispatch_id]=4242-1-") {
		t.Errorf("the dispatch carried no nonce derived from this run; fields:\n%s", fields)
	}
}

var dispatchReasons = map[string]string{
	"mode selected":     "ran mode 'selected'",
	"concluded failure": "is completed/failure",
	"record missing":    "has 0 test-selection records",
	"dispatch refused":  "dispatching one on refs/tags/v1.2.3 failed",
}

func TestReleaseRefusesADispatchedRunThatIsNotFullAndGreen(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	cases := map[string]func(f *fakeAPI){
		"mode selected": func(f *fakeAPI) {
			recordFor(t, f, dispatchID, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "selected"})
		},
		"concluded failure": func(f *fakeAPI) {
			red := iosRun(dispatchID, sha, "workflow_dispatch", "completed", "failure")
			red["display_title"] = "iOS CI provenance @@NONCE@@"
			f.seq("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID), red)
			f.on("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID)+"/jobs?filter=latest&per_page=100", "200", ciOKJobs("failure"))
		},
		"record missing": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID)+"/artifacts?name=test-selection&per_page=100", "200", artifacts())
		},
		"dispatch refused": func(f *fakeAPI) {
			f.on("POST /repos/acme/demo/actions/workflows/ios-ci.yml/dispatches", "422", `{"message":"Unexpected inputs provided"}`)
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := noIOSRunFixture(t, sha, tree)
			breakIt(f)
			assertBlocked(t, runProvenance(t, f, dir, sha, nil), dispatchReasons[name])
		})
	}
}

// workflow_dispatch takes a REF, not a SHA. If the ref moved between the
// release SHA and the dispatch, the run tests a different commit: fail closed,
// naming both.
func TestReleaseRefusesADispatchedRunOnAnotherSHA(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	moved := "7777777777777777777777777777777777777777"
	f := noIOSRunFixture(t, sha, tree)
	other := iosRun(dispatchID, moved, "workflow_dispatch", "completed", "success")
	other["display_title"] = "iOS CI provenance @@NONCE@@"
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{other}})
	f.seq("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID), other)
	recordFor(t, f, dispatchID, map[string]any{"pr": nil, "head": moved, "tree": tree, "mode": "full"})
	res := runProvenance(t, f, dir, sha, nil)
	assertBlocked(t, res, "moved between the release and the dispatch")
	for _, want := range []string{sha, moved} {
		if !strings.Contains(res.out, want) {
			t.Errorf("the refusal does not name %s:\n%s", want, res.out)
		}
	}
}

func TestReleaseRefusesWhenTheDispatchedRunNeverAppears(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{}})
	res := runProvenance(t, f, dir, sha, nil)
	assertBlocked(t, res, "did not appear within")
	if res.elapsed > 30*time.Second {
		t.Errorf("waiting for a run that never appears took %s against a 2s bound", res.elapsed)
	}
}

func TestReleaseRefusesWhenTheDispatchedRunTimesOut(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	f.seq("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID), iosRun(dispatchID, sha, "workflow_dispatch", "in_progress", ""))
	res := runProvenance(t, f, dir, sha, nil)
	assertBlocked(t, res, "did not finish within")
	if res.elapsed > 30*time.Second {
		t.Errorf("waiting on a run that never finishes took %s against a 3s bound", res.elapsed)
	}
}

func TestReleaseRefusesTwoRunsCarryingTheNonce(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	a := iosRun(dispatchID, sha, "workflow_dispatch", "completed", "success")
	a["display_title"] = "iOS CI provenance @@NONCE@@"
	b := iosRun(302, sha, "workflow_dispatch", "completed", "success")
	b["display_title"] = "iOS CI provenance @@NONCE@@"
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{b, a}})
	assertBlocked(t, runProvenance(t, f, dir, sha, nil), "carry this release's dispatch id")
}

// "Never latest run." A newer, green dispatch of someone else's sits first in
// the listing; this release's own run (by nonce) is red. Taking the latest run
// would ship.
func TestReleaseIdentifiesItsRunByNonceNotLatest(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	newer := iosRun(399, sha, "workflow_dispatch", "completed", "success")
	newer["display_title"] = "iOS CI provenance someone-elses-nonce"
	mine := iosRun(dispatchID, sha, "workflow_dispatch", "completed", "failure")
	mine["display_title"] = "iOS CI provenance @@NONCE@@"
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{newer, mine}})
	f.seq("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID), mine)
	f.on("GET /repos/acme/demo/actions/runs/"+itoa(dispatchID)+"/jobs?filter=latest&per_page=100", "200", ciOKJobs("failure"))
	greenRun(f, newer)
	recordFor(t, f, 399, map[string]any{"pr": nil, "head": sha, "tree": tree, "mode": "full"})
	assertBlocked(t, runProvenance(t, f, dir, sha, nil), "run 301 is completed/failure")
}

func TestReleaseRefusesOnAnAPIFailure(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	checks := "GET /repos/acme/demo/commits/" + sha + "/check-runs?per_page=100"
	for name, breakIt := range map[string]func(f *fakeAPI){
		"check runs 500":  func(f *fakeAPI) { f.on(checks, "500", `{"message":"boom"}`) },
		"check runs hang": func(f *fakeAPI) { f.on(checks, "HANG", nil) },
		"run lookup 502":  func(f *fakeAPI) { f.on("GET /repos/acme/demo/actions/runs?check_suite_id=900", "502", "") },
		"record download hangs": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/artifacts/"+itoa(600+pushRunID)+"/zip", "HANG", nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := fullPushFixture(t, sha, tree)
			breakIt(f)
			res := runProvenance(t, f, dir, sha, nil)
			assertBlocked(t, res, "could not")
			if res.elapsed > 30*time.Second {
				t.Errorf("the gate took %s against an 8s per-call bound", res.elapsed)
			}
		})
	}
}

// The shipped bounds, and the job timeout that has to sit above them. A wait
// that outlives its job is killed by the runner with no message naming why.
func TestProvenanceWaitIsBounded(t *testing.T) {
	step := provenanceStep(t)
	for _, want := range []string{
		`PROVENANCE_API_TIMEOUT:-30`,
		`PROVENANCE_POLL_INTERVAL:-60`,
		`PROVENANCE_APPEAR_TIMEOUT:-300`,
		`PROVENANCE_WAIT_TIMEOUT:-7200`,
	} {
		if !strings.Contains(step.Run, want) {
			t.Errorf("the provenance gate does not default %s", want)
		}
	}
	var doc struct {
		Jobs map[string]struct {
			TimeoutMinutes int `yaml:"timeout-minutes"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(renderRelease(t, soloProject())), &doc); err != nil {
		t.Fatal(err)
	}
	got := doc.Jobs["verify-ci-provenance"].TimeoutMinutes
	// appear (5) + wait (120) + the rest, with headroom.
	if got < 130 || got > 150 {
		t.Errorf("verify-ci-provenance timeout-minutes is %d, want 130-150: above the 125-minute script bound, not far above it", got)
	}
}

// The dispatched run is found by the title iOS CI gives a dispatch carrying a
// nonce. If the two files disagree on that title, every dispatched release
// waits out its bound and fails.
func TestDispatchTitleMatchesTheCIRunName(t *testing.T) {
	ci := renderIOSCI(t, soloConfig())
	if !strings.Contains(ci, "run-name: ${{ inputs.dispatch_id && format('iOS CI provenance {0}', inputs.dispatch_id) || '' }}") {
		t.Error("ios ci.yml does not name a nonce-carrying dispatch 'iOS CI provenance <nonce>'")
	}
	if !strings.Contains(ci, "dispatch_id:") {
		t.Error("ios ci.yml's workflow_dispatch declares no dispatch_id input")
	}
	if !strings.Contains(provenanceStep(t).Run, `title="iOS CI provenance $nonce"`) {
		t.Error("the release gate does not look for the title ci.yml gives its dispatched run")
	}
}

// The dispatch POST is retried on a transient failure (#586). Backoff is shrunk
// to nothing so the tests run in seconds; the shipped 10 s / 30 s are pinned in
// TestReleaseDispatchRetryIsBounded.
var noBackoff = map[string]string{
	"PROVENANCE_DISPATCH_BACKOFF_1": "0",
	"PROVENANCE_DISPATCH_BACKOFF_2": "0",
}

const dispatchRoute = "POST /repos/acme/demo/actions/workflows/ios-ci.yml/dispatches"

// dispatchFixture is noIOSRunFixture with the dispatch route answering status.
func dispatchFixture(t *testing.T, status string) (*fakeAPI, string, string) {
	t.Helper()
	dir, sha, tree := commitRepo(t)
	f := noIOSRunFixture(t, sha, tree)
	f.on(dispatchRoute, status, nil)
	return f, dir, sha
}

func postCount(f *fakeAPI) int {
	n := 0
	for _, c := range f.calls() {
		if c == "POST_/repos/acme/demo/actions/workflows/ios-ci.yml/dispatches" {
			n++
		}
	}
	return n
}

// dispatchIDs is the dispatch_id each POST carried, in order.
func dispatchIDs(f *fakeAPI) []string {
	var ids []string
	for _, l := range strings.Split(f.fields(), "\n") {
		if id, ok := strings.CutPrefix(l, "inputs[dispatch_id]="); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func assertPosts(t *testing.T, f *fakeAPI, want int) {
	t.Helper()
	if got := postCount(f); got != want {
		t.Errorf("the gate POSTed the dispatch %d time(s), want %d", got, want)
	}
}

func TestReleaseRetriesTheDispatchOnA5xxWithOneNonce(t *testing.T) {
	f, dir, sha := dispatchFixture(t, "502|502|204")
	res := runProvenance(t, f, dir, sha, noBackoff)
	assertReleased(t, res)
	assertPosts(t, f, 3)
	ids := dispatchIDs(f)
	if len(ids) != 3 || ids[0] != ids[1] || ids[1] != ids[2] || ids[0] == "" {
		t.Errorf("every attempt must carry the same dispatch id; got %q", ids)
	}
	for _, want := range []string{"attempt 1 of 3 failed (HTTP 502)", "attempt 2 of 3 failed (HTTP 502)"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("no retry line %q:\n%s", want, res.out)
		}
	}
}

func TestReleaseFailsAfterThreeDispatchAttemptsOnA5xx(t *testing.T) {
	f, dir, sha := dispatchFixture(t, "503")
	res := runProvenance(t, f, dir, sha, noBackoff)
	assertBlocked(t, res, "dispatching one on refs/tags/v1.2.3 failed after 3 attempts (last: HTTP 503)")
	assertPosts(t, f, 3)
}

func TestReleaseDoesNotRetryA4xxDispatch(t *testing.T) {
	for _, status := range []string{"422", "403", "404", "429"} {
		t.Run(status, func(t *testing.T) {
			f, dir, sha := dispatchFixture(t, status)
			res := runProvenance(t, f, dir, sha, noBackoff)
			assertBlocked(t, res, "failed after 1 attempt (last: HTTP "+status+")")
			assertPosts(t, f, 1)
		})
	}
}

func TestReleaseDoesNotRetryAnUnclassifiableDispatchError(t *testing.T) {
	for name, text := range map[string]string{
		"no status":   "gh: something nobody planned for",
		"empty":       "",
		"token":       "gh: To use GitHub CLI in a GitHub Actions workflow, set the GH_TOKEN environment variable.",
		"exit status": "gh: exit status 1",
	} {
		t.Run(name, func(t *testing.T) {
			f, dir, sha := dispatchFixture(t, "ERR:"+text)
			res := runProvenance(t, f, dir, sha, noBackoff)
			assertBlocked(t, res, "failed after 1 attempt (last: unclassified error, exit 1)")
			assertPosts(t, f, 1)
		})
	}
}

func TestReleaseRetriesTheDispatchOnATransportError(t *testing.T) {
	cases := map[string]struct {
		status string
		env    map[string]string
		last   string
	}{
		"connection refused": {
			status: `ERR:Post "https://api.github.com/repos/acme/demo/actions/workflows/ios-ci.yml/dispatches": dial tcp 140.82.112.5:443: connect: connection refused|204`,
			last:   "transport error",
		},
		"no such host": {status: `ERR:Post "https://api.github.com/x": dial tcp: lookup api.github.com: no such host|204`, last: "transport error"},
		"alarm timeout": {
			// The fake sleeps past api_timeout; the perl alarm kills it (exit 142).
			status: "HANG|204",
			env:    map[string]string{"PROVENANCE_API_TIMEOUT": "1"},
			last:   "timed out after 1s",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f, dir, sha := dispatchFixture(t, c.status)
			env := map[string]string{}
			for k, v := range noBackoff {
				env[k] = v
			}
			for k, v := range c.env {
				env[k] = v
			}
			res := runProvenance(t, f, dir, sha, env)
			assertReleased(t, res)
			assertPosts(t, f, 2)
			if !strings.Contains(res.out, "attempt 1 of 3 failed ("+c.last+")") {
				t.Errorf("no retry line naming %q:\n%s", c.last, res.out)
			}
		})
	}
}

// The status is read from where gh puts it, in either of its two message
// shapes, and from nowhere else: a message that merely mentions a code must not
// turn a refusal into a retry, or a transient error into a refusal.
func TestReleaseClassifiesDispatchErrorsFromGhStderr(t *testing.T) {
	cases := []struct {
		name, stderr string
		posts        int
		last         string
	}{
		{"5xx, message first", "gh: Server Error (HTTP 502)", 3, "HTTP 502"},
		{"5xx, status first", "HTTP 504: Gateway Timeout (https://api.github.com/repos/acme/demo/actions/workflows/ios-ci.yml/dispatches)", 3, "HTTP 504"},
		{"4xx, message first", "gh: Validation Failed (HTTP 422)", 1, "HTTP 422"},
		{"4xx, status first", "HTTP 403: Resource not accessible by integration (https://api.github.com/x)", 1, "HTTP 403"},
		{"4xx naming a 5xx", "gh: upstream answered HTTP 502 earlier (HTTP 422)", 1, "HTTP 422"},
		{"5xx naming a 4xx", "gh: not HTTP 404 really (HTTP 500)", 3, "HTTP 500"},
		{"a code in a URL only", "gh: could not parse https://example.com/HTTP 502/x", 1, "unclassified error, exit 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, dir, sha := dispatchFixture(t, "ERR:"+c.stderr)
			res := runProvenance(t, f, dir, sha, noBackoff)
			assertBlocked(t, res, "(last: "+c.last+")")
			assertPosts(t, f, c.posts)
		})
	}
}

// Requirement (4): a 5xx POST that GitHub accepted anyway leaves a run behind,
// and the retry makes a second with the same dispatch id. The "N runs carry
// this release's dispatch id" refusal must still fire after a retry.
func TestReleaseRefusesTwoRunsCreatedByARetriedDispatch(t *testing.T) {
	f, dir, sha := dispatchFixture(t, "502|204")
	a := iosRun(dispatchID, sha, "workflow_dispatch", "completed", "success")
	a["display_title"] = "iOS CI provenance @@NONCE@@"
	b := iosRun(302, sha, "workflow_dispatch", "completed", "success")
	b["display_title"] = "iOS CI provenance @@NONCE@@"
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=workflow_dispatch&created=*", "200",
		map[string]any{"workflow_runs": []any{b, a}})
	res := runProvenance(t, f, dir, sha, noBackoff)
	assertBlocked(t, res, "2 iOS CI runs carry this release's dispatch id")
	assertPosts(t, f, 2)
	if !strings.Contains(res.out, "attempt 1 of 3 failed (HTTP 502)") {
		t.Errorf("the dispatch was not retried before the refusal:\n%s", res.out)
	}
}

// The shipped bound: 3 attempts, 10 s then 30 s apart, stated in a comment
// beside the code with its worst case.
func TestReleaseDispatchRetryIsBounded(t *testing.T) {
	run := provenanceStep(t).Run
	for _, want := range []string{
		"max_attempts=3",
		`backoff_first="${PROVENANCE_DISPATCH_BACKOFF_1:-10}"`,
		`backoff_second="${PROVENANCE_DISPATCH_BACKOFF_2:-30}"`,
		"= 100 s",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("the provenance step does not contain %q", want)
		}
	}
}
