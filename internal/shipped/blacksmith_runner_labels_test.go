package shipped

// This file guards a 2026-09 defect: #312 (badd21a) deliberately right-sized
// five coordination jobs -- ones that do nothing but `git diff`, `jq` or a
// single `gh api` call -- to `blacksmith-2vcpu-ubuntu-2404-arm`, because
// Blacksmith bills vCPU-weighted minutes with a ONE-MINUTE-PER-JOB MINIMUM: a
// 4-vCPU job that finishes in 23s still bills 4 minutes, while the same job on
// 2vcpu-arm bills 1.25 (half the vCPUs, times ARM's 0.625 rate multiplier).
// Nothing else in the fleet's jobs is architecture-sensitive the way a job
// that unpacks a platform-specific binary is, so this was pure savings.
//
// #339 (829192d) replaced that AND the profile's deliberately-x64 jobs
// (Postgres/pgTAP/Supabase CLI work, and web's real build job) with a single
// `{{LINUX_RUNNER}}` template token -- one variable asked to hold two
// different intended values. #340 (2d8b50a) then removed the token entirely
// and hardcoded `blacksmith-4vcpu-ubuntu-2404` for all 14 shipped Blacksmith
// jobs, silently reverting #312's five ARM jobs back to x64 along with it.
//
// Since the single-switch change every hosted Linux job takes its runner from
// ONE Actions variable (actionlint.HostedVar) with a Blacksmith 2-vCPU x64
// fallback, so a person can move the whole fleet to GitHub-hosted runners by
// setting the variable. The table below therefore pins the SET of hosted jobs,
// and each one's rendered `runs-on:` must be exactly the switch expression;
// there are no per-job labels left, and no 4-vCPU or ARM hosted label exists.
//
// Nothing before this file checked a runner LABEL at all, which is exactly
// how a refactor could revert a deliberately-chosen cost decision and still
// pass a green suite. This test does not key on the comments that (correctly)
// argue for ARM above the `changes` job in profiles/ios/workflows/ci.yml --
// #340 proves a comment and the code beneath it can disagree indefinitely.
// It keys on the parsed `runs-on:` value alone, against an explicit
// (profile, workflow file, job id) -> expected label table below, so:
//
//   - a restored job silently re-reverted to x64 fails, naming that job;
//   - a supabase (or other deliberately-x64) job silently moved to ARM fails
//     just as loudly -- the table protects both directions, not just the
//     savings;
//   - a brand-new Blacksmith job with no entry here fails as unlisted, so
//     adding one requires a deliberate size choice rather than inheriting
//     whatever the previous job in the file happened to use;
//   - a table entry that never turns up in the rendered workflows fails too,
//     so deleting or renaming a job without updating this file is caught the
//     same way as changing its label.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/actionlint"
	"gopkg.in/yaml.v3"
)

// blacksmithLabel is an expected runner label plus why it was chosen, so a
// failure message explains the cost trade-off rather than just asserting a
// string mismatch.
type blacksmithLabel struct {
	label string
	why   string
}

const (
	hostedExpr = "switch" // every hosted job renders actionlint.HostedRunsOn

	armWhy = "coordination-only job (diff/jq/gh-api, no Xcode, no architecture-sensitive " +
		"binary), so it runs on x64 as well as it ran on ARM"
	x64BuildWhy        = "a real build/lint/test job"
	supabaseDenoArmWhy = "Deno and the lacquer download are arch-aware, so x64 is fine"
	x64DeployWhy       = "a few seconds of `supabase db push`; the Supabase CLI runs on x64"
	x64SupabaseWhy     = "drives Postgres, pgTAP or the Supabase CLI; runs on the 2-vCPU default " +
		"(no 4-vCPU hosted label exists)"
)

// blacksmithJobKey identifies one job across the fleet of shipped workflows:
// the profile it ships under, the workflow file's basename, and the job id
// (the YAML key under `jobs:`, not its display `name:`).
type blacksmithJobKey struct {
	profile string
	file    string
	jobID   string
}

// expectedBlacksmithRunnerLabels is the complete, explicit table of every
// Blacksmith job this repo ships and the label it must render with. It is
// deliberately exhaustive (see TestBlacksmithRunnerLabelsPinned's unlisted-job
// check) rather than defaulted, so a new job forces a choice here.
var expectedBlacksmithRunnerLabels = map[blacksmithJobKey]blacksmithLabel{
	// Every `changes` job, the release coordination jobs and the supabase health
	// ping moved to pi-gate (free); see pi_gate_jobs_test.go. They are absent
	// here on purpose: a Blacksmith label reappearing on one is UNLISTED.
	{"ios", "release.yml", "select-products"}:      {hostedExpr, armWhy + "; release coordination stays hosted: unproven on pi-gate for a real release"},
	{"ios", "release.yml", "notify-on-failure"}:    {hostedExpr, armWhy + "; release coordination stays hosted: unproven on pi-gate for a real release"},
	{"ios", "release.yml", "verify-ci-provenance"}: {hostedExpr, armWhy + "; stays hosted because release_provenance_test pins the release safety gate to a hosted Linux runner"},
	{"web", "ci.yml", "check"}:                     {hostedExpr, x64BuildWhy},
	{"supabase", "ci.yml", "check"}:                {hostedExpr, supabaseDenoArmWhy},
	{"supabase", "ci.yml", "database"}:             {hostedExpr, x64SupabaseWhy},
	{"supabase", "ci.yml", "deploy-database"}:      {hostedExpr, x64DeployWhy},
}

// blacksmithRunsOnDoc captures just enough of a rendered workflow to read
// every job's `runs-on:` value as a raw yaml.Node -- not a string field --
// because a self-hosted job's runs-on is a YAML sequence
// (`[self-hosted, macOS, ARM64, dedicated]`), and decoding that into a string
// field would be a hard decode error, not a job this test can skip cleanly.
type blacksmithRunsOnDoc struct {
	Jobs map[string]struct {
		RunsOn yaml.Node `yaml:"runs-on"`
	} `yaml:"jobs"`
}

// runBlacksmithRunnerLabelsCheck is the guts of the test, factored out so it
// can be pointed at an arbitrary directory (an empty one included) to prove
// the "parsed nothing" paths actually fail rather than pass vacuously -- see
// the mutation recorded in this PR's body for how that was verified.
func runBlacksmithRunnerLabelsCheck(t *testing.T, dir string) {
	t.Helper()

	paths, err := shippedWorkflowFiles(dir)
	if err != nil {
		t.Fatalf("shippedWorkflowFiles: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("shippedWorkflowFiles found zero workflow files under profiles/*/workflows -- " +
			"the glob or the directory is broken, not the fleet; this test must not pass vacuously")
	}

	seenExpected := map[blacksmithJobKey]bool{}
	var violations []string
	var unlisted []string
	blacksmithJobsFound := 0

	for _, path := range paths {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		// rel looks like "profiles/<profile>/workflows/<file>.yml".
		parts := strings.Split(rel, "/")
		if len(parts) != 4 || parts[0] != "profiles" || parts[2] != "workflows" {
			t.Fatalf("%s: does not match profiles/<profile>/workflows/<file>.yml -- "+
				"shippedWorkflowFiles' glob and this test's path parsing have drifted apart", rel)
		}
		profile, file := parts[1], parts[3]

		out, err := renderShippedWorkflow(t, path)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}

		var doc blacksmithRunsOnDoc
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: rendered workflow is not valid YAML: %v", rel, err)
		}
		if len(doc.Jobs) == 0 {
			t.Fatalf("%s: parsed zero jobs -- the renderer or the parser is broken, not the fleet", rel)
		}

		jobNames := make([]string, 0, len(doc.Jobs))
		for name := range doc.Jobs {
			jobNames = append(jobNames, name)
		}
		sort.Strings(jobNames)

		for _, jobID := range jobNames {
			node := doc.Jobs[jobID].RunsOn
			// A self-hosted runner is a YAML sequence
			// (`[self-hosted, ...]`), never a Blacksmith job; skip anything
			// that isn't a plain scalar string.
			if node.Kind != yaml.ScalarNode {
				continue
			}
			label := node.Value
			blacksmithJobsFound++

			key := blacksmithJobKey{profile: profile, file: file, jobID: jobID}
			want, ok := expectedBlacksmithRunnerLabels[key]
			if !ok {
				unlisted = append(unlisted, fmt.Sprintf(
					"%s job %q: runs-on %q is a hosted job with no entry in "+
						"expectedBlacksmithRunnerLabels -- a new hosted job must be listed and use the switch, not a "+
						"hardcoded label",
					rel, jobID, label))
				continue
			}
			seenExpected[key] = true
			if err := actionlint.CheckHostedRunsOn(label); err != nil {
				violations = append(violations, fmt.Sprintf("%s job %q: %v -- %s", rel, jobID, err, want.why))
			} else if label != actionlint.HostedRunsOn {
				violations = append(violations, fmt.Sprintf(
					"%s job %q: runs-on is %q, want exactly %q -- %s",
					rel, jobID, label, actionlint.HostedRunsOn, want.why))
			}
		}
	}

	if blacksmithJobsFound == 0 {
		t.Fatal("found zero hosted jobs across every shipped workflow -- either every job " +
			"moved off Blacksmith or the detector is broken; this test must not pass vacuously")
	}

	var missing []string
	keys := make([]blacksmithJobKey, 0, len(expectedBlacksmithRunnerLabels))
	for k := range expectedBlacksmithRunnerLabels {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].profile != keys[j].profile {
			return keys[i].profile < keys[j].profile
		}
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].jobID < keys[j].jobID
	})
	for _, k := range keys {
		if !seenExpected[k] {
			missing = append(missing, fmt.Sprintf(
				"profiles/%s/workflows/%s job %q: expected in expectedBlacksmithRunnerLabels "+
					"(want the switch expression) but not found in any rendered shipped workflow -- the job was "+
					"renamed, removed, or moved off Blacksmith without updating this table",
				k.profile, k.file, k.jobID))
		}
	}

	if len(violations) > 0 || len(unlisted) > 0 || len(missing) > 0 {
		var msg strings.Builder
		fmt.Fprintf(&msg, "blacksmith runner label mismatch(es):\n")
		for _, v := range violations {
			fmt.Fprintf(&msg, "  MISMATCH: %s\n", v)
		}
		for _, u := range unlisted {
			fmt.Fprintf(&msg, "  UNLISTED: %s\n", u)
		}
		for _, m := range missing {
			fmt.Fprintf(&msg, "  MISSING:  %s\n", m)
		}
		t.Fatal(msg.String())
	}
}

// TestBlacksmithRunnerLabelsPinned parses every shipped workflow and asserts
// each Blacksmith job's `runs-on:` matches an explicit, deliberately-chosen
// expectation. See the package comment above for the incident (#312 -> #339
// -> #340) this exists to make impossible to repeat unnoticed.
func TestBlacksmithRunnerLabelsPinned(t *testing.T) {
	runBlacksmithRunnerLabelsCheck(t, root(t))
}
