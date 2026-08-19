package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// soloProject is the shape twelve repositories in this fleet actually have: one
// component at the root, no [[product]] entries.
func soloProject() *config.Config {
	return &config.Config{
		Project: config.Project{
			ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
			AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
		},
	}
}

// renderIOSWorkflow substitutes a profile workflow the way sync does.
func renderIOSWorkflow(t *testing.T, name string, cfg *config.Config, prefix string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, prefix))
	if len(missing) > 0 {
		t.Fatalf("%s: unsubstituted tokens: %v", name, missing)
	}
	return out
}

// namedStep returns the `run:` body of one step of one job, by step name.
//
// By NAME rather than by index: the point of these tests is the behaviour of a
// particular step, and an index silently follows whichever step happens to sit
// there after an edit.
func namedStep(t *testing.T, rendered, job, step string) string {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered workflow is not valid YAML: %v", err)
	}
	for _, st := range doc.Jobs[job].Steps {
		if n, _ := st["name"].(string); n == step {
			run, _ := st["run"].(string)
			if run == "" {
				t.Fatalf("step %q in job %q has no run: block", step, job)
			}
			return run
		}
	}
	t.Fatalf("no step named %q in job %q", step, job)
	return ""
}

// stubBin writes an executable shell script into its own directory and returns
// that directory, for prepending to PATH.
func stubBin(t *testing.T, name, script string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDeadCodeScanFailsLoudlyWhenPeripheryDies runs the SHIPPED steps against a
// periphery that fails, and requires the job to go red.
//
// This is the defect core/CLAUDE.core.md names outright — "No `|| true`, no
// dropping `--strict`, no `2>/dev/null` ... those turn a gate into a log line" —
// shipped on the job that pays for itself least: ~30 minutes of the fleet's ONE
// self-hosted Mac, per repository, every Monday.
//
// The old step was `periphery scan … > periphery-report.json || true`. The
// redirect creates the report BEFORE periphery runs, so a dead scan still leaves
// a file, the `[ ! -f … ]` guard never fires, and `jq … 2>/dev/null || echo 0`
// turns the wreckage into zero. Measured on the pre-fix content, all three
// shapes of a failed scan reported success: an empty stdout printed
// "Found **** unused declarations", and partial JSON or a prose error printed
// "No dead code found!".
//
// So this asserts the OUTCOME rather than the absence of a string. A future edit
// could reintroduce the bug with different spelling; it cannot reintroduce it
// while a dying scan still has to turn this job red.
func TestDeadCodeScanFailsLoudlyWhenPeripheryDies(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed; the shipped step requires it")
	}
	rendered := renderIOSWorkflow(t, "dead-code.yml", soloProject(), "")
	scan := namedStep(t, rendered, "periphery", "Run Periphery")
	analyze := namedStep(t, rendered, "periphery", "Analyze Results")

	for _, tc := range []struct {
		name string
		// stdout the stub periphery emits, and the code it exits with.
		stdout string
		exit   string
		// wantRed is whether the two steps together must fail the job.
		wantRed bool
		// wantCount is the count reported to GITHUB_OUTPUT on a green run.
		wantCount string
	}{
		// The three ways a real scan dies. Each of these reported GREEN before.
		{"dies with empty stdout", "", "1", true, ""},
		{"dies mid-write, partial JSON", `[{"name":"a","kind":`, "1", true, ""},
		{"dies with prose on stdout", "Indexing... aborted", "1", true, ""},
		// Exit 0 is necessary and not sufficient: a scan that indexed nothing can
		// still exit 0 with output that is not a findings array.
		{"exits 0 with a non-array body", "Nothing to see", "0", true, ""},
		// And the two real results must still work. A gate that fails on
		// everything is no more use than one that passes on everything.
		{"clean scan, no findings", "[]", "0", false, "0"},
		{
			"clean scan, two findings",
			`[{"name":"a","kind":"class","location":{"file":"/x/A.swift","line":3}},` +
				`{"name":"b","kind":"function","location":{"file":"/x/B.swift","line":9}}]`,
			"0", false, "2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(payload, []byte(tc.stdout), 0o600); err != nil {
				t.Fatal(err)
			}
			// stderr as well as stdout: periphery reports its real problem there,
			// and a step that hides it is the other half of this defect.
			bin := stubBin(t, "periphery", "#!/bin/bash\ncat "+payload+
				"\necho 'periphery: diagnostic on stderr' >&2\nexit "+tc.exit+"\n")

			work := t.TempDir()
			env := append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GITHUB_OUTPUT="+filepath.Join(work, "gh_output"),
				"GITHUB_STEP_SUMMARY="+filepath.Join(work, "gh_summary"),
				"DEAD_CODE_THRESHOLD=5",
			)

			run := func(script string) (string, bool) {
				cmd := exec.Command("bash", "-c", "cd "+work+"\n"+script)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				return string(out), err == nil
			}

			scanOut, scanOK := run(scan)
			red := !scanOK
			var analyzeOut string
			if scanOK {
				// GitHub only runs the next step when this one passed.
				var ok bool
				analyzeOut, ok = run(analyze)
				red = !ok
			}
			combined := scanOut + analyzeOut

			if red != tc.wantRed {
				t.Fatalf("job red = %v, want %v\noutput:\n%s", red, tc.wantRed, combined)
			}

			summary, _ := os.ReadFile(filepath.Join(work, "gh_summary"))
			if tc.wantRed {
				// The whole point: a failure has to be legible, not just non-zero.
				if !strings.Contains(combined, "::error::") {
					t.Errorf("the job failed without an ::error:: annotation:\n%s", combined)
				}
				if strings.Contains(string(summary), "No dead code found") {
					t.Errorf("a FAILED scan still reported \"No dead code found!\" — "+
						"this is the exact silent green the fix exists to remove:\n%s", summary)
				}
				return
			}

			got, _ := os.ReadFile(filepath.Join(work, "gh_output"))
			if !strings.Contains(string(got), "count="+tc.wantCount+"\n") {
				t.Errorf("GITHUB_OUTPUT = %q, want count=%s", got, tc.wantCount)
			}
		})
	}
}

// TestDeadCodeAnalyzeFailsClosedOnAMissingReport pins the branch that used to
// answer "count=0" to a report that is not there.
//
// The scan step only leaves the file behind when it succeeded AND emitted a JSON
// array, so its absence cannot mean "no dead code". Reporting zero to that is a
// conclusion nothing supports.
func TestDeadCodeAnalyzeFailsClosedOnAMissingReport(t *testing.T) {
	rendered := renderIOSWorkflow(t, "dead-code.yml", soloProject(), "")
	analyze := namedStep(t, rendered, "periphery", "Analyze Results")

	work := t.TempDir()
	cmd := exec.Command("bash", "-c", "cd "+work+"\n"+analyze)
	cmd.Env = append(os.Environ(),
		"GITHUB_OUTPUT="+filepath.Join(work, "gh_output"),
		"GITHUB_STEP_SUMMARY="+filepath.Join(work, "gh_summary"),
		"DEAD_CODE_THRESHOLD=5",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a missing periphery-report.json passed the analyze step:\n%s", out)
	}
	got, _ := os.ReadFile(filepath.Join(work, "gh_output"))
	if strings.Contains(string(got), "has_dead_code=false") {
		t.Errorf("a missing report was reported as \"no dead code\": %q", got)
	}
}

// peripheryKeys is every top-level key periphery 3 accepts that this config uses
// or plausibly would. It is an allowlist rather than a denylist because periphery
// does NOT fail on a key it does not know — it prints
// "warning: .periphery.yml: invalid key '…'" and carries on scanning without it,
// which is indistinguishable from the setting having had no effect.
//
// That is not hypothetical. The shipped config carried `targets:` and `exclude:`,
// both removed in periphery 3: the target list selected nothing (schemes select
// targets now) and the exclusion filtered nothing, so generated code and previews
// were reported as dead the whole time. Measured on periphery 3.8.0.
var peripheryKeys = map[string]bool{
	"project": true, "schemes": true, "report_exclude": true, "report_include": true,
	"index_exclude": true, "exclude_targets": true, "exclude_tests": true,
	"retain_files": true, "retain_public": true, "retain_objc_accessible": true,
	"retain_objc_annotated": true, "retain_unused_protocol_func_params": true,
	"retain_assign_only_properties": true, "format": true, "strict": true,
	"disable_update_check": true, "quiet": true, "verbose": true,
}

func TestPeripheryConfigUsesOnlyKeysPeripheryAccepts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "config", ".periphery.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf(".periphery.yml is not valid YAML: %v", err)
	}
	if len(doc) == 0 {
		t.Fatal("no keys parsed out of .periphery.yml; this test is checking nothing")
	}
	for k := range doc {
		if !peripheryKeys[k] {
			t.Errorf("%q is not a key periphery accepts. It will draw "+
				"\"warning: .periphery.yml: invalid key '%s'\" and be ignored — a setting that "+
				"silently does nothing, which is how `targets:` and `exclude:` survived here.", k, k)
		}
	}
}

// TestPeripheryConfigNamesTheProjectItWasGiven rejects a return to deriving the
// project path from [project].name.
//
// profiles/ios/CLAUDE.ios.md records why derivation is wrong for this fleet: one
// app builds a differently-named product from its scheme, which is why
// `app_target` is declared rather than computed. The same reasoning applies to
// the project file — a derived path that misses makes periphery exit with "The
// project cannot be found at …" and scan nothing.
func TestPeripheryConfigNamesTheProjectItWasGiven(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "config", ".periphery.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Project string   `yaml:"project"`
		Schemes []string `yaml:"schemes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Project, tokens.Xcodeproj) {
		t.Errorf("project = %q, want the declared %s rather than a name-derived path",
			doc.Project, tokens.Xcodeproj)
	}
	// The workflow runs periphery FROM the component directory, and {{XCODEPROJ}}
	// is repo-relative, so the two only agree through {{COMPONENT_TO_ROOT}}.
	// Without it, every project whose component is not the repo root scans a path
	// that does not exist.
	if !strings.Contains(doc.Project, tokens.ComponentToRoot) {
		t.Errorf("project = %q: %s is repo-relative but periphery resolves this against the "+
			"component directory it is run from, so it must be joined through %s",
			doc.Project, tokens.Xcodeproj, tokens.ComponentToRoot)
	}
	if len(doc.Schemes) == 0 {
		t.Error("no schemes declared; periphery 3 selects targets from the schemes, so an " +
			"empty list scans nothing")
	}
}

// TestPeripheryAcceptsTheRenderedConfig is the live cross-check on the allowlist
// above: it renders the config for a nested layout and hands it to the real
// periphery, requiring no invalid-key warning.
//
// Skipped where periphery is not installed, which is every Linux CI runner —
// the allowlist test above is the one that always runs. This one exists because
// an allowlist can go stale against a tool that keeps moving, and the tool is
// the authority on its own keys.
func TestPeripheryAcceptsTheRenderedConfig(t *testing.T) {
	bin, err := exec.LookPath("periphery")
	if err != nil {
		t.Skip("periphery is not installed")
	}
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "config", ".periphery.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := soloProject()
	cfg.Project.Xcodeproj = "ios/Deep/Demo.xcodeproj"
	rendered, missing := tokens.Substitute(string(raw), tokens.Values(cfg, "ios/"))
	if len(missing) > 0 {
		t.Fatalf(".periphery.yml has unsubstituted tokens: %v", missing)
	}

	// A component directory, exactly as the workflow's `cd {{COMPONENT_PREFIX}}.`
	// leaves it.
	comp := filepath.Join(t.TempDir(), "ios")
	if err := os.MkdirAll(comp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(comp, ".periphery.yml"), []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "scan", "--config", ".periphery.yml", "--format", "json")
	cmd.Dir = comp
	out, _ := cmd.CombinedOutput()
	// The scan itself fails: there is no real .xcodeproj here, and that is the
	// point of the second assertion.
	if strings.Contains(string(out), "invalid key") {
		t.Errorf("periphery rejected a key in the shipped config:\n%s", out)
	}
	// It must fail having looked in the right place. The path is the thing this
	// change fixed, and the error names it.
	want := filepath.Join(comp, "Deep", "Demo.xcodeproj")
	if !strings.Contains(string(out), want) {
		t.Errorf("periphery resolved the project somewhere other than %s:\n%s", want, out)
	}
}

// macSchedule is one shipped workflow that both runs on a schedule and wants the
// self-hosted Mac.
type macSchedule struct {
	file string
	cron string
}

// TestScheduledMacJobsDoNotShareASlot keeps the fleet's heaviest scheduled work
// off a single instant.
//
// There is ONE self-hosted Mac for roughly twelve iOS repositories. Two workflows
// sharing a cron means twenty-four heavy jobs asking for that runner at the same
// moment, which it can only answer by serialising them — and CI and releases
// queue behind the whole pile. dead-code.yml and quality-review.yml were both
// "0 9 * * 1"; they are now Monday and Wednesday.
//
// This checks the SHIPPED literals, which is all a template can control: the same
// minute still renders into every repository, and spreading those apart needs a
// per-project token that internal/tokens does not have.
func TestScheduledMacJobsDoNotShareASlot(t *testing.T) {
	dir := filepath.Join(root(t), "profiles", "ios", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var found []macSchedule
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		// Rendered, not token-stripped. Several of these files hold tokens that
		// expand to whole YAML blocks at column 0 (release.yml's product choices),
		// so a regex substitution leaves something that does not parse.
		body := renderIOSWorkflow(t, e.Name(), soloProject(), "")
		var doc struct {
			On struct {
				Schedule []struct {
					Cron string `yaml:"cron"`
				} `yaml:"schedule"`
			} `yaml:"on"`
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("rendered %s: %v", e.Name(), err)
		}
		if len(doc.On.Schedule) == 0 {
			continue
		}
		var wantsMac bool
		for _, j := range doc.Jobs {
			labels, ok := j.RunsOn.([]any)
			if !ok {
				continue
			}
			for _, l := range labels {
				if s, _ := l.(string); s == "self-hosted" {
					wantsMac = true
				}
			}
		}
		if !wantsMac {
			continue
		}
		for _, sc := range doc.On.Schedule {
			found = append(found, macSchedule{file: e.Name(), cron: sc.Cron})
		}
	}

	// Guard the guard: a walk that finds nothing would pass forever.
	if len(found) < 3 {
		t.Fatalf("only %d scheduled self-hosted workflows found; the walk is not reaching them", len(found))
	}

	seen := map[string]string{}
	for _, s := range found {
		if prev, dup := seen[s.cron]; dup {
			t.Errorf("%s and %s both run at %q. There is ONE self-hosted Mac for ~12 iOS "+
				"repositories, so a shared slot is ~24 heavy jobs queueing on one runner at once, "+
				"with CI and releases behind them. Move one to another day.", prev, s.file, s.cron)
			continue
		}
		seen[s.cron] = s.file
	}
}

// TestDocsCommentsDoNotDescribeTheRevertedTwoJobDesign keeps prose and
// implementation from disagreeing again.
//
// All three docs.yml files carried a comment arguing for a cheap `check` job on a
// hosted runner, justified by "a skipped job never occupies a runner at all" and
// reasoning about iOS's Mac — including in web and supabase, which have no Mac.
// The design it described lasted one day: e958179 reverted it after measuring
// that GitHub bills a minimum of one minute per JOB, so the gate cost a billed
// minute per repository per day to save ~20 seconds of an idle runner.
//
// The comment outlived the code by many commits. Nothing catches that, because
// nothing reads comments — so this does, for this one claim, which is now
// contradicted by the file it sits in and by
// TestDocsWorkflowsAreScheduledNotPushed.
func TestDocsCommentsDoNotDescribeTheRevertedTwoJobDesign(t *testing.T) {
	// The load-bearing sentence of the reverted argument.
	const claim = "a skipped job never occupies a runner"
	var checked int
	for _, profile := range []string{"ios", "web", "supabase"} {
		p := filepath.Join(root(t), "profiles", profile, "workflows", "docs.yml")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(strings.ToLower(line), claim) {
				t.Errorf("profiles/%s/workflows/docs.yml:%d still argues for the two-job design "+
					"that e958179 reverted: %q\nThese files have ONE job by measurement — a second "+
					"one costs a billed minute per repository per day.", profile, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked != 3 {
		t.Fatalf("read %d docs.yml files, want 3", checked)
	}
}
