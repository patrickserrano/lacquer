package shipped

// Billed-minute consolidation (job count and runner class) of the shipped CI
// workflows. A job that does seconds of work bills a whole minute on a hosted
// runner (vCPU-weighted on Blacksmith, per job on a Mac), so what these tests
// pin is the SHAPE that keeps that bill small: coordination jobs on the free
// `pi-gate` role, one Mac job where there were two, one local Supabase stack per
// run. Each assertion reads the rendered, parsed workflow, never a comment.

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var piGate = []string{"self-hosted", "Linux", "pi-gate"}

// ciJobDoc is the part of a rendered workflow these tests read.
type ciJobDoc struct {
	Jobs map[string]struct {
		RunsOn yaml.Node `yaml:"runs-on"`
		Needs  yaml.Node `yaml:"needs"`
		Steps  []struct {
			Name string `yaml:"name"`
			ID   string `yaml:"id"`
			If   string `yaml:"if"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadShippedCI(t *testing.T, profile, file string) ciJobDoc {
	t.Helper()
	out, err := renderShippedWorkflow(t, filepath.Join(root(t), "profiles", profile, "workflows", file))
	if err != nil {
		t.Fatalf("%s/%s: %v", profile, file, err)
	}
	var doc ciJobDoc
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%s/%s: rendered workflow is not valid YAML: %v", profile, file, err)
	}
	if len(doc.Jobs) == 0 {
		t.Fatalf("%s/%s: parsed zero jobs", profile, file)
	}
	return doc
}

func jobRunsOnList(n yaml.Node) []string {
	if n.Kind == yaml.ScalarNode {
		return []string{n.Value}
	}
	var out []string
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

func jobNeedsList(n yaml.Node) []string {
	if n.Kind == 0 {
		return nil
	}
	return jobRunsOnList(n)
}

// TestCoordinationJobsRunOnPiGate: jobs that only read, diff or aggregate run on
// the free pi-gate role, not on a billed runner.
func TestCoordinationJobsRunOnPiGate(t *testing.T) {
	want := []struct{ profile, file, job string }{
		{"ios", "ci.yml", "changes"},
		{"ios", "ci.yml", "ci-ok"},
		{"web", "ci.yml", "changes"},
		{"web", "ci.yml", "ci-ok"},
		{"supabase", "ci.yml", "changes"},
		{"supabase", "ci.yml", "ci-ok"},
		{"supabase", "health.yml", "ping"},
		{"ios", "release.yml", "select-products"},
		{"ios", "release.yml", "notify-on-failure"},
	}
	checked := 0
	var bad []string
	for _, w := range want {
		doc := loadShippedCI(t, w.profile, w.file)
		j, ok := doc.Jobs[w.job]
		if !ok {
			bad = append(bad, w.profile+"/"+w.file+" has no job "+w.job)
			continue
		}
		checked++
		if got := jobRunsOnList(j.RunsOn); !reflect.DeepEqual(got, piGate) {
			bad = append(bad, w.profile+"/"+w.file+" job "+w.job+": runs-on "+strings.Join(got, ",")+", want pi-gate")
		}
	}
	if checked < len(want) {
		bad = append(bad, "checked fewer jobs than expected: the table and the workflows have drifted")
	}
	if len(bad) > 0 {
		t.Fatalf("coordination jobs off pi-gate:\n  %s", strings.Join(bad, "\n  "))
	}
}

// ciOKExemptJobs are jobs deliberately outside CI OK's needs, with the reason.
var ciOKExemptJobs = map[string]string{
	"supabase/deploy-database": "push-to-main deploy that itself needs the gated jobs; not a PR check",
}

// TestCIOKNeedsOnlyRealJobs: a `needs` entry naming a job that does not exist is
// a YAML error GitHub reports only at run time, and a job that CI OK does not
// need is a check nobody waits for. Both must fail here instead.
func TestCIOKNeedsOnlyRealJobs(t *testing.T) {
	profiles := []string{"ios", "web", "supabase"}
	checked := 0
	for _, profile := range profiles {
		doc := loadShippedCI(t, profile, "ci.yml")
		ok, found := doc.Jobs["ci-ok"]
		if !found {
			t.Fatalf("%s/ci.yml has no ci-ok job", profile)
		}
		checked++
		needs := map[string]bool{}
		for _, n := range jobNeedsList(ok.Needs) {
			needs[n] = true
			if _, exists := doc.Jobs[n]; !exists {
				t.Errorf("%s/ci.yml: ci-ok needs %q, which is not a job", profile, n)
			}
		}
		var ids []string
		for id := range doc.Jobs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if id == "ci-ok" || needs[id] {
				continue
			}
			if _, exempt := ciOKExemptJobs[profile+"/"+id]; exempt {
				continue
			}
			t.Errorf("%s/ci.yml: job %q is not in ci-ok's needs, so no required check waits for it", profile, id)
		}
		// Every need is also read: its result appears in the aggregator's script.
		script := ""
		for _, st := range ok.Steps {
			script += st.Run
		}
		for n := range needs {
			if !strings.Contains(script, "needs."+n+".result") {
				t.Errorf("%s/ci.yml: ci-ok waits for %q but its script never reads needs.%s.result", profile, n, n)
			}
		}
	}
	if checked < len(profiles) {
		t.Fatalf("checked %d workflows, want %d", checked, len(profiles))
	}
}

// TestIOSCIHasNoBaselineJob: the baseline assertion is steps in Lint (one Mac
// job per run, not two), and a project with no Swift yet still gets it.
func TestIOSCIHasNoBaselineJob(t *testing.T) {
	doc := loadShippedCI(t, "ios", "ci.yml")
	if _, ok := doc.Jobs["baseline"]; ok {
		t.Fatal("jobs.baseline exists: it was folded into lint to save a Mac job and a runner pickup per run")
	}
	lint, ok := doc.Jobs["lint"]
	if !ok {
		t.Fatal("no lint job")
	}
	var assert bool
	for _, st := range lint.Steps {
		switch st.Name {
		case "Assert the project baseline":
			assert = true
			if strings.Contains(st.If, "swiftsrc") {
				t.Errorf("baseline step is gated on Swift sources (%q): a Phase-0 project would skip its baseline", st.If)
			}
		case "Create Secrets.xcconfig (build-time placeholder)", "Generate Xcode project (XcodeGen)", "Detect Xcode project":
			if st.If != "" {
				t.Errorf("%q has if=%q, want unconditional like the job it came from", st.Name, st.If)
			}
		}
	}
	if !assert {
		t.Fatal(`lint has no "Assert the project baseline" step`)
	}
}

// TestSupabaseStartsOneLocalStackPerRun: booting the stack is the slow, billed
// part of the database checks. Two boots is the shape this replaced.
func TestSupabaseStartsOneLocalStackPerRun(t *testing.T) {
	doc := loadShippedCI(t, "supabase", "ci.yml")
	starts := 0
	for id, j := range doc.Jobs {
		for _, st := range j.Steps {
			// A command line, not a mention: `changes` names it in prose.
			for _, line := range strings.Split(st.Run, "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[0] == "supabase" && f[1] == "start" {
					starts++
					t.Logf("supabase start in job %s", id)
				}
			}
		}
	}
	if starts != 1 {
		t.Fatalf("%d steps run `supabase start`, want exactly 1 (one stack per run)", starts)
	}
	db, ok := doc.Jobs["database"]
	if !ok {
		t.Fatal("no `database` job")
	}
	// Both checks report even when the other fails.
	for _, name := range []string{"Read the pgTAP relaxation", "Run pgTAP tests"} {
		found := false
		for _, st := range db.Steps {
			if st.Name == name {
				found = true
				if !strings.Contains(st.If, "!cancelled()") {
					t.Errorf("%q has if=%q: a Splinter failure would skip pgTAP", name, st.If)
				}
			}
		}
		if !found {
			t.Errorf("database job has no %q step", name)
		}
	}
}
