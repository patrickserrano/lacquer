package shipped

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// A push to main runs the full suite again on a tree a pull request already
// tested in full. D4b (approved 2026-10-04, made precise 2026-10-19) lets the
// push run reuse that result, and ONLY when all three hold: the merge commit's
// TREE equals the tree the PR run recorded, that run was FULL, and its CI OK
// was green. Everything else runs the full suite, and every way the lookup can
// fail is a named test here, because a reuse that fires when it should not
// merges untested code onto main and nothing goes red.
//
// Branch protection's `strict` is off on purpose (the Test job's comment), so a
// squash-merged tree is NOT guaranteed to equal the tree that was tested. The
// tree, not the PR number or the head SHA, is what makes reuse safe without it,
// and TestMergeWhoseTreeDiffersRunsCIOnMain is the fixture that proves it.

// reuseDoc is the slice of the rendered iOS ci.yml these tests read.
type reuseDoc struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn      any               `yaml:"runs-on"`
		Needs       yaml.Node         `yaml:"needs"`
		If          string            `yaml:"if"`
		Permissions map[string]string `yaml:"permissions"`
		Outputs     map[string]string `yaml:"outputs"`
		Steps       []reuseStep       `yaml:"steps"`
	} `yaml:"jobs"`
}

type reuseStep struct {
	ID              string            `yaml:"id"`
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	Uses            string            `yaml:"uses"`
	Run             string            `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	With            map[string]any    `yaml:"with"`
	ContinueOnError any               `yaml:"continue-on-error"`
	TimeoutMinutes  int               `yaml:"timeout-minutes"`
}

func parseReuseDoc(t *testing.T, cfg *config.Config) reuseDoc {
	t.Helper()
	var doc reuseDoc
	if err := yaml.Unmarshal([]byte(renderIOSCI(t, cfg)), &doc); err != nil {
		t.Fatalf("rendered ci.yml is not valid YAML: %v", err)
	}
	return doc
}

// stepByID returns the step with id in job, failing the test when there is none.
func (d reuseDoc) stepByID(t *testing.T, job, id string) reuseStep {
	t.Helper()
	for _, st := range d.Jobs[job].Steps {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("the %s job has no step with id %q", job, id)
	return reuseStep{}
}

const (
	fakeRepo   = "acme/demo"
	prHead     = "1111111111111111111111111111111111111111"
	prRunURL   = "https://github.com/acme/demo/actions/runs/101"
	reuseGuard = "needs.changes.outputs.reuse != 'true'"
)

// reuseFixture is a PR run that tested `tree` in FULL and went green, reachable
// from the merge commit `sha` the way the reuse step looks it up. Each test
// starts here and breaks exactly one thing.
func reuseFixture(t *testing.T, sha, tree string) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t)
	f.on("GET /repos/acme/demo/commits/"+sha+"/pulls", "200", []map[string]any{
		{"number": 7, "merge_commit_sha": sha, "head": map[string]any{"sha": prHead}},
	})
	f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=pull_request&head_sha="+prHead+"&per_page=100", "200",
		map[string]any{"total_count": 1, "workflow_runs": []map[string]any{prRun(101, "completed", "success")}})
	f.on("GET /repos/acme/demo/actions/runs/101/jobs?filter=latest&per_page=100", "200", ciOKJobs("success"))
	f.on("GET /repos/acme/demo/actions/runs/101/artifacts?name=test-selection&per_page=100", "200", artifacts(555))
	f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "200", zipped(t, "test-selection.json", map[string]any{
		"pr": 7, "head": prHead, "tree": tree, "mode": "full",
	}))
	return f
}

func prRun(id int, status, conclusion string) map[string]any {
	return map[string]any{
		"id": id, "head_sha": prHead, "event": "pull_request", "status": status, "conclusion": conclusion,
		"path": ".github/workflows/ios-ci.yml", "html_url": "https://github.com/acme/demo/actions/runs/" + itoa(id),
	}
}

func ciOKJobs(conclusion string) map[string]any {
	return map[string]any{"total_count": 2, "jobs": []map[string]any{
		{"name": "Detect changed paths", "status": "completed", "conclusion": "success"},
		{"name": "CI OK", "status": "completed", "conclusion": conclusion},
	}}
}

func artifacts(ids ...int) map[string]any {
	list := []map[string]any{}
	for _, id := range ids {
		list = append(list, map[string]any{"id": id, "name": "test-selection", "expired": false})
	}
	return map[string]any{"total_count": len(ids), "artifacts": list}
}

// runReuse executes the rendered reuse step against f, as a push of sha to main.
func runReuse(t *testing.T, f *fakeAPI, dir, sha string, overrides map[string]string) stepResult {
	t.Helper()
	doc := parseReuseDoc(t, soloConfig())
	step := doc.stepByID(t, "changes", "reuse")
	env := map[string]string{
		"GH_TOKEN":          "fake-token",
		"EVENT_NAME":        "push",
		"GIT_REF":           "refs/heads/main",
		"SHA":               sha,
		"REPO":              fakeRepo,
		"API_URL":           "https://api.test",
		"REUSE_API_TIMEOUT": "8",
	}
	for k, v := range overrides {
		env[k] = v
	}
	// The step's own env block must carry every variable this test supplies, or
	// the test is feeding the script something the workflow never does.
	for k := range env {
		if k == "REUSE_API_TIMEOUT" {
			continue
		}
		if _, ok := step.Env[k]; !ok {
			t.Fatalf("the reuse step's env does not define %s (has %v)", k, keysOf(step.Env))
		}
	}
	res := f.run(step.Run, dir, env)
	if res.failed {
		t.Fatalf("the reuse step FAILED; its failure mode must be reuse=false, never a red `changes` job:\n%s", res.out)
	}
	return res
}

// assertFull asserts the push runs the full suite: reuse=false, no reused run,
// and every Mac job's guard lets it run.
//
// reason is the refusal the log must carry, so a run that went FULL for some
// other reason (a fixture the step could not read) does not pass for the path
// under test. Empty means the step leaves silently: not a push to main.
func assertFull(t *testing.T, res stepResult, reason string) {
	t.Helper()
	if reason != "" && !strings.Contains(res.out, "Running the full suite: ") {
		t.Errorf("the step went FULL without saying why:\n%s", res.out)
	}
	if !strings.Contains(res.out, reason) {
		t.Errorf("the full suite runs, but not because %q:\n%s", reason, res.out)
	}
	if res.outputs["reuse"] != "false" {
		t.Errorf("reuse=%q, want \"false\" (the full suite must run on main)\n%s", res.outputs["reuse"], res.out)
	}
	if res.outputs["reused_run"] != "" {
		t.Errorf("reused_run=%q on a run that did not reuse", res.outputs["reused_run"])
	}
	for _, job := range macJobsSkippedBy(t, res.outputs) {
		t.Errorf("Mac job %s would be SKIPPED with these outputs, so main gets no CI", job)
	}
}

// macJobsSkippedBy returns the Mac jobs whose reuse guard skips them under the
// given `changes` outputs. The guard is the only part of their `if:` these
// outputs decide; TestEveryMacJobCarriesTheReuseGuard holds every Mac job to
// carrying it.
func macJobsSkippedBy(t *testing.T, outputs map[string]string) []string {
	t.Helper()
	var skipped []string
	for name, job := range parseReuseDoc(t, watchProject()).Jobs {
		if isMacJob(job.RunsOn) && strings.Contains(job.If, reuseGuard) && outputs["reuse"] == "true" {
			skipped = append(skipped, name)
		}
	}
	return skipped
}

func isMacJob(runsOn any) bool {
	switch v := runsOn.(type) {
	case string:
		return strings.Contains(v, "macOS")
	case []any:
		for _, l := range v {
			if s, _ := l.(string); s == "macOS" {
				return true
			}
		}
	}
	return false
}

func TestPushReusesAnIdenticalTreesFullGreenRun(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	res := runReuse(t, reuseFixture(t, sha, tree), dir, sha, nil)
	if res.outputs["reuse"] != "true" {
		t.Fatalf("an identical tree with a FULL green PR run was not reused: reuse=%q\n%s", res.outputs["reuse"], res.out)
	}
	if res.outputs["reused_run"] != prRunURL {
		t.Errorf("reused_run=%q, want %q", res.outputs["reused_run"], prRunURL)
	}
	if res.outputs["reused_run_id"] != "101" {
		t.Errorf("reused_run_id=%q, want 101", res.outputs["reused_run_id"])
	}
	if res.outputs["tree"] != tree {
		t.Errorf("tree=%q, want %q", res.outputs["tree"], tree)
	}
	if len(macJobsSkippedBy(t, res.outputs)) == 0 {
		t.Error("reuse=true skips no Mac job, so the reuse saves nothing")
	}
}

// The decision's own fail path, verbatim in intent: "a merge whose tree differs
// (main moved) must run CI on main". The PR run is FULL and green and its record
// names the same PR and the same head SHA, so only a TREE comparison refuses it.
func TestMergeWhoseTreeDiffersRunsCIOnMain(t *testing.T) {
	dir, sha, _ := commitRepo(t)
	f := reuseFixture(t, sha, "2222222222222222222222222222222222222222")
	res := runReuse(t, f, dir, sha, nil)
	assertFull(t, res, "tested tree 2222222222222222222222222222222222222222, but this merge is tree")
	if !strings.Contains(res.out, "2222222222222222222222222222222222222222") {
		t.Errorf("the log does not name the tree the PR run tested:\n%s", res.out)
	}
}

// A SELECTED run is never reused: it skipped tests on purpose. Authored before
// any selected run exists, because it is what keeps Unit 3 honest.
func TestOnlyAFullRunIsReused(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	for _, mode := range []string{"selected", "none", "reused", ""} {
		t.Run("mode="+mode, func(t *testing.T) {
			f := reuseFixture(t, sha, tree)
			f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "200", zipped(t, "test-selection.json", map[string]any{
				"pr": 7, "head": prHead, "tree": tree, "mode": mode,
			}))
			assertFull(t, runReuse(t, f, dir, sha, nil), "ran mode '"+mode+"'")
		})
	}
}

func TestOnlyAGreenRunIsReused(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	t.Run("run concluded failure", func(t *testing.T) {
		f := reuseFixture(t, sha, tree)
		f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=pull_request&head_sha="+prHead+"&per_page=100", "200",
			map[string]any{"total_count": 1, "workflow_runs": []map[string]any{prRun(101, "completed", "failure")}})
		f.on("GET /repos/acme/demo/actions/runs/101/jobs?filter=latest&per_page=100", "200", ciOKJobs("failure"))
		assertFull(t, runReuse(t, f, dir, sha, nil), "run 101 is completed/failure")
	})
	t.Run("CI OK job did not succeed", func(t *testing.T) {
		f := reuseFixture(t, sha, tree)
		f.on("GET /repos/acme/demo/actions/runs/101/jobs?filter=latest&per_page=100", "200", ciOKJobs("cancelled"))
		assertFull(t, runReuse(t, f, dir, sha, nil), "CI OK is cancelled")
	})
	t.Run("run still in progress", func(t *testing.T) {
		f := reuseFixture(t, sha, tree)
		f.on("GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=pull_request&head_sha="+prHead+"&per_page=100", "200",
			map[string]any{"total_count": 1, "workflow_runs": []map[string]any{prRun(101, "in_progress", "")}})
		assertFull(t, runReuse(t, f, dir, sha, nil), "run 101 is in_progress/")
	})
}

var missingReasons = map[string]string{
	"no artifact":                 "has 0 test-selection records",
	"unparsable record":           "record is not JSON",
	"archive without the record":  "holds no test-selection.json",
	"record for a different PR":   "record is for PR 8, not #7",
	"record for a different head": "record is for head 3333333333333333333333333333333333333333",
}

// A missing record, or one that cannot be read, is not a record that agrees.
func TestMissingOrUnreadableRecordRunsFull(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	cases := map[string]func(f *fakeAPI){
		"no artifact": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/runs/101/artifacts?name=test-selection&per_page=100", "200", artifacts())
		},
		"unparsable record": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "200", zipped(t, "test-selection.json", []byte("{not json")))
		},
		"archive without the record": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "200", zipped(t, "other.json", map[string]any{"tree": tree, "mode": "full"}))
		},
		"record for a different PR": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "200", zipped(t, "test-selection.json", map[string]any{
				"pr": 8, "head": prHead, "tree": tree, "mode": "full",
			}))
		},
		"record for a different head": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "200", zipped(t, "test-selection.json", map[string]any{
				"pr": 7, "head": "3333333333333333333333333333333333333333", "tree": tree, "mode": "full",
			}))
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := reuseFixture(t, sha, tree)
			breakIt(f)
			assertFull(t, runReuse(t, f, dir, sha, nil), missingReasons[name])
		})
	}
}

var lookupReasons = map[string]string{
	"API error (500)":                              "returned HTTP 500",
	"API error on the run list":                    "returned HTTP 502",
	"API error on the artifact":                    "returned HTTP 500",
	"transport failure":                            "failed (curl exit 6)",
	"forbidden (403, no limit)":                    "was refused (HTTP 403)",
	"rate limit (403 + headers)":                   "was rate limited (HTTP 403)",
	"rate limit (429)":                             "was rate limited (HTTP 429)",
	"missing run: no PR for the commit":            "belongs to 0 pull requests",
	"missing run: no iOS CI run on its head":       "has 0 iOS CI runs",
	"missing run: PR merged as a different commit": "was merged as 4444444444444444444444444444444444444444",
	"ambiguous: two PRs for the commit":            "belongs to 2 pull requests",
	"ambiguous: two runs for the head":             "has 2 iOS CI runs",
	"ambiguous: two records":                       "has 2 test-selection records",
	"ambiguous: two CI OK jobs":                    "CI OK is 2 CI OK jobs",
	"unparsable API response":                      "is not a JSON list",
}

// Decision B (2026-10-19): "any API error, rate limit, timeout, missing run or
// ambiguous match means FULL, never a reuse."
func TestReuseLookupFailuresRunFull(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	pulls := "GET /repos/acme/demo/commits/" + sha + "/pulls"
	runs := "GET /repos/acme/demo/actions/workflows/ios-ci.yml/runs?event=pull_request&head_sha=" + prHead + "&per_page=100"
	cases := map[string]func(f *fakeAPI){
		"API error (500)":           func(f *fakeAPI) { f.on(pulls, "500", `{"message":"boom"}`) },
		"API error on the run list": func(f *fakeAPI) { f.on(runs, "502", `{"message":"bad gateway"}`) },
		"API error on the artifact": func(f *fakeAPI) { f.on("GET /repos/acme/demo/actions/artifacts/555/zip", "500", "") },
		"transport failure":         func(f *fakeAPI) { f.on(pulls, "EXIT6", nil) },
		"forbidden (403, no limit)": func(f *fakeAPI) { f.on(pulls, "403", `{"message":"Resource not accessible by integration"}`) },
		"rate limit (403 + headers)": func(f *fakeAPI) {
			f.on(pulls, "403", `{"message":"API rate limit exceeded"}`)
			f.withHeaders(pulls, "x-ratelimit-remaining: 0", "x-ratelimit-reset: 1792390238")
		},
		"rate limit (429)": func(f *fakeAPI) {
			f.on(pulls, "429", `{"message":"secondary rate limit"}`)
			f.withHeaders(pulls, "retry-after: 60")
		},
		"missing run: no PR for the commit": func(f *fakeAPI) { f.on(pulls, "200", []any{}) },
		"missing run: no iOS CI run on its head": func(f *fakeAPI) {
			f.on(runs, "200", map[string]any{"total_count": 0, "workflow_runs": []any{}})
		},
		"missing run: PR merged as a different commit": func(f *fakeAPI) {
			f.on(pulls, "200", []map[string]any{
				{"number": 7, "merge_commit_sha": "4444444444444444444444444444444444444444", "head": map[string]any{"sha": prHead}},
			})
		},
		"ambiguous: two PRs for the commit": func(f *fakeAPI) {
			f.on(pulls, "200", []map[string]any{
				{"number": 7, "merge_commit_sha": sha, "head": map[string]any{"sha": prHead}},
				{"number": 9, "merge_commit_sha": sha, "head": map[string]any{"sha": prHead}},
			})
		},
		"ambiguous: two runs for the head": func(f *fakeAPI) {
			f.on(runs, "200", map[string]any{"total_count": 2, "workflow_runs": []map[string]any{
				prRun(101, "completed", "success"), prRun(102, "completed", "success"),
			}})
		},
		"ambiguous: two records": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/runs/101/artifacts?name=test-selection&per_page=100", "200", artifacts(555, 556))
		},
		"ambiguous: two CI OK jobs": func(f *fakeAPI) {
			f.on("GET /repos/acme/demo/actions/runs/101/jobs?filter=latest&per_page=100", "200", map[string]any{"jobs": []map[string]any{
				{"name": "CI OK", "status": "completed", "conclusion": "success"},
				{"name": "CI OK", "status": "completed", "conclusion": "success"},
			}})
		},
		"unparsable API response": func(f *fakeAPI) { f.on(pulls, "200", "<html>not json</html>") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := reuseFixture(t, sha, tree)
			breakIt(f)
			assertFull(t, runReuse(t, f, dir, sha, nil), lookupReasons[name])
		})
	}
}

// A hang is not a reuse. The call is bounded, and the bound is what returns:
// the fake sleeps for a minute, so a step that waits it out fails the elapsed
// check even if it then reports the right answer. The test bound is 8s rather
// than smaller because a faked call itself can take seconds under a loaded
// full-suite run.
func TestReuseLookupTimeoutRunsFull(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	for _, key := range []string{
		"GET /repos/acme/demo/commits/" + sha + "/pulls",
		"GET /repos/acme/demo/actions/artifacts/555/zip",
	} {
		t.Run(key, func(t *testing.T) {
			f := reuseFixture(t, sha, tree)
			f.on(key, "HANG", nil)
			res := runReuse(t, f, dir, sha, nil)
			assertFull(t, res, "timed out after 8s")
			if res.elapsed > 20*time.Second {
				t.Errorf("the lookup took %s against an 8s bound; a hang must be cut off, not waited out", res.elapsed)
			}
		})
	}
}

// Reuse is for a push to main and nothing else: a pull request is the run
// being recorded, a dispatch is a request for the full suite.
func TestReuseOnlyConsidersAPushToMain(t *testing.T) {
	dir, sha, tree := commitRepo(t)
	for _, tc := range []struct{ event, ref string }{
		{"pull_request", "refs/pull/7/merge"},
		{"workflow_dispatch", "refs/heads/main"},
		{"workflow_dispatch", "refs/tags/v1.2.3"},
		{"push", "refs/heads/other"},
	} {
		t.Run(tc.event+" "+tc.ref, func(t *testing.T) {
			f := reuseFixture(t, sha, tree)
			res := runReuse(t, f, dir, sha, map[string]string{"EVENT_NAME": tc.event, "GIT_REF": tc.ref})
			assertFull(t, res, "")
			if calls := f.calls(); len(calls) != 0 {
				t.Errorf("a %s run reached the API: %v", tc.event, calls)
			}
			// The tree is recorded on every run: the PR run's record is what a
			// later push compares against.
			if res.outputs["tree"] != tree {
				t.Errorf("tree=%q on a %s run, want %q", res.outputs["tree"], tc.event, tree)
			}
		})
	}
}

// The step's failure mode is reuse=false, so it must never be able to fail the
// job, and its wall clock is bounded from outside the script too.
func TestReuseStepIsBoundedAndCannotFailTheJob(t *testing.T) {
	doc := parseReuseDoc(t, soloConfig())
	changes := doc.Jobs["changes"]
	step := doc.stepByID(t, "changes", "reuse")
	if step.ContinueOnError != true {
		t.Errorf("the reuse step has continue-on-error %v, want true: a crashed lookup must mean FULL, not a red job", step.ContinueOnError)
	}
	if step.TimeoutMinutes < 1 || step.TimeoutMinutes > 5 {
		t.Errorf("the reuse step has timeout-minutes %d, want a bound between 1 and 5", step.TimeoutMinutes)
	}
	for out, want := range map[string]string{
		"reuse":         "steps.reuse.outputs.reuse",
		"reused_run":    "steps.reuse.outputs.reused_run",
		"reused_run_id": "steps.reuse.outputs.reused_run_id",
		"tree":          "steps.reuse.outputs.tree",
	} {
		if !strings.Contains(changes.Outputs[out], want) {
			t.Errorf("the changes job's %q output is %q, want it to publish %s", out, changes.Outputs[out], want)
		}
	}
	// The API token reaches the script through the environment only.
	if strings.Contains(step.Run, "${{") {
		t.Errorf("the reuse script interpolates an Actions expression; pass it through env:\n%s", step.Run)
	}
}

// Every Mac job (a job whose runs-on carries macOS) skips on a reused tree and
// on nothing else new, so a Mac job added later without the guard fails here.
// watch-test is the one the walk is easiest to forget: it is generated, and
// only renders when a project declares watch tests.
func TestEveryMacJobCarriesTheReuseGuard(t *testing.T) {
	for name, cfg := range map[string]*config.Config{"solo": soloConfig(), "watch": watchProject()} {
		t.Run(name, func(t *testing.T) {
			doc := parseReuseDoc(t, cfg)
			var mac []string
			for job, j := range doc.Jobs {
				if !isMacJob(j.RunsOn) {
					if strings.Contains(j.If, reuseGuard) {
						t.Errorf("%s is not a Mac job but skips on reuse; only Mac work is what reuse saves", job)
					}
					continue
				}
				mac = append(mac, job)
				if !strings.Contains(j.If, reuseGuard) {
					t.Errorf("Mac job %s has no reuse guard (`%s`) in its if: %s", job, reuseGuard, j.If)
				}
				if !contains(needsList(t, j.Needs), "changes") {
					t.Errorf("Mac job %s does not need `changes`, so its reuse guard reads nothing", job)
				}
			}
			want := []string{"lint", "build-release", "test"}
			if name == "watch" {
				want = append(want, "watch-test")
			}
			for _, w := range want {
				if !contains(mac, w) {
					t.Errorf("expected Mac job %s is missing from the walk (found %v)", w, mac)
				}
			}
		})
	}
}

// ciOKScript returns the CI OK aggregator step and its env.
func ciOKAggregator(t *testing.T) reuseStep {
	t.Helper()
	doc := parseReuseDoc(t, soloConfig())
	steps := doc.Jobs["ci-ok"].Steps
	if len(steps) == 0 {
		t.Fatal("ci-ok has no steps")
	}
	return steps[0]
}

// CI OK is what branch protection requires, so it is what has to refuse a reuse
// that names no run: a skipped Mac job reads as passing, and without this a
// reuse=true with nothing behind it would merge on a gate that checked nothing.
func TestCIOKRequiresTheReusedRunItNames(t *testing.T) {
	agg := ciOKAggregator(t)
	for _, k := range []string{"REUSE", "REUSED_RUN", "EVENT_NAME", "TREE"} {
		if _, ok := agg.Env[k]; !ok {
			t.Fatalf("the CI OK aggregator's env does not define %s (has %v)", k, keysOf(agg.Env))
		}
	}
	green := map[string]string{"changes": "success", "lint": "skipped", "build-release": "skipped", "test": "skipped"}
	for _, tc := range []struct {
		name     string
		env      map[string]string
		wantFail bool
	}{
		{"reuse with a named run", map[string]string{"REUSE": "true", "REUSED_RUN": prRunURL, "EVENT_NAME": "push", "TREE": "abc"}, false},
		{"reuse naming no run", map[string]string{"REUSE": "true", "REUSED_RUN": "", "EVENT_NAME": "push", "TREE": "abc"}, true},
		{"reuse on a pull request", map[string]string{"REUSE": "true", "REUSED_RUN": prRunURL, "EVENT_NAME": "pull_request", "TREE": "abc"}, true},
		{"no reuse", map[string]string{"REUSE": "false", "REUSED_RUN": "", "EVENT_NAME": "push", "TREE": "abc"}, false},
		{"reuse output unrecognised", map[string]string{"REUSE": "maybe", "REUSED_RUN": prRunURL, "EVENT_NAME": "push", "TREE": "abc"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := needsResult.ReplaceAllStringFunc(agg.Run, func(m string) string {
				return green[needsResult.FindStringSubmatch(m)[1]]
			})
			script = changesOutput.ReplaceAllString(script, "")
			f := newFakeAPI(t)
			res := f.run(script, t.TempDir(), tc.env)
			if res.failed != tc.wantFail {
				t.Errorf("CI OK failed=%v, want %v\n%s", res.failed, tc.wantFail, res.out)
			}
			if !tc.wantFail && tc.env["REUSE"] == "true" {
				want := "Reused full green suite from " + prRunURL + " (identical tree abc)"
				if !strings.Contains(res.summary, want) {
					t.Errorf("the step summary does not say %q:\n%s", want, res.summary)
				}
			}
		})
	}
}

// The run record (Task 5.0): what this run tested, in a file a later push run
// and the release gate both read. `mode` is "full" only when the test job was
// expected to run (code changed, a project exists, and the PR is not a fork's),
// so a docs-only or fork PR's green CI OK, which ran no tests, can never be
// reused as a full suite.
func TestRunRecordSaysWhatThisRunTested(t *testing.T) {
	doc := parseReuseDoc(t, soloConfig())
	rec := doc.stepByID(t, "ci-ok", "record")
	if !strings.Contains(strings.ReplaceAll(rec.If, " ", ""), "always()") {
		t.Errorf("the record step has `if: %s`; it must run on a red CI OK too", rec.If)
	}
	if rec.ContinueOnError != true {
		t.Errorf("the record step can fail CI OK (continue-on-error %v)", rec.ContinueOnError)
	}
	var upload reuseStep
	for _, st := range doc.Jobs["ci-ok"].Steps {
		if strings.HasPrefix(st.Uses, "actions/upload-artifact@") {
			upload = st
		}
	}
	if upload.Uses == "" {
		t.Fatal("ci-ok uploads no artifact; the record never leaves the runner")
	}
	if upload.With["name"] != "test-selection" || upload.With["retention-days"] != 30 {
		t.Errorf("the record artifact is %v, want name test-selection, retention-days 30", upload.With)
	}
	if !strings.Contains(strings.ReplaceAll(upload.If, " ", ""), "always()") || upload.ContinueOnError != true {
		t.Errorf("the upload must run always() and never fail CI OK: if=%q continue-on-error=%v", upload.If, upload.ContinueOnError)
	}

	base := map[string]string{
		"PR_NUMBER": "7", "HEAD_SHA": prHead, "TREE": "abc", "CODE": "true", "XCODEPROJ": "true",
		"SAME_REPO": "true", "REUSE": "false", "REUSED_RUN": "", "REUSED_RUN_ID": "",
	}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for _, tc := range []struct {
		name string
		env  map[string]string
		want map[string]any
	}{
		{"same-repo PR with code", base, map[string]any{"pr": 7.0, "head": prHead, "tree": "abc", "mode": "full"}},
		{"docs-only PR", with("CODE", "false"), map[string]any{"mode": "none"}},
		{"fork PR", with("SAME_REPO", "false"), map[string]any{"mode": "none"}},
		{"pre-code component", with("XCODEPROJ", "false"), map[string]any{"mode": "none"}},
		{"push run", with("PR_NUMBER", ""), map[string]any{"pr": nil, "mode": "full"}},
		{"reused push run", with("PR_NUMBER", "", "REUSE", "true", "REUSED_RUN", prRunURL, "REUSED_RUN_ID", "101"),
			map[string]any{"mode": "reused", "reused_run": prRunURL, "reused_run_id": 101.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k := range tc.env {
				if _, ok := rec.Env[k]; !ok {
					t.Fatalf("the record step's env does not define %s (has %v)", k, keysOf(rec.Env))
				}
			}
			f := newFakeAPI(t)
			tmp := t.TempDir()
			env := map[string]string{"RUNNER_TEMP": tmp}
			for k, v := range tc.env {
				env[k] = v
			}
			res := f.run(rec.Run, t.TempDir(), env)
			if res.failed {
				t.Fatalf("the record step failed:\n%s", res.out)
			}
			path, _ := upload.With["path"].(string)
			path = strings.ReplaceAll(path, "${{ runner.temp }}", tmp)
			data, err := os.ReadFile(filepath.Join(path, "test-selection.json"))
			if err != nil {
				t.Fatalf("the record step wrote nothing where the upload reads (%s): %v\n%s", path, err, res.out)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("the record is not JSON: %v\n%s", err, data)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("record[%q] = %v, want %v (record: %s)", k, got[k], v, data)
				}
			}
		})
	}
}
