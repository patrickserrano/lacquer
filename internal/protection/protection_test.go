package protection

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dailybread's real shape, which is what this package was written for: a
// project-owned ios-ci.yml posting `Lint` and `Test` as separate contexts, and a
// merge-gate.yml whose conditional jobs post the two names branch protection
// actually requires.
func dailybreadWorkflows() Workflows {
	return Workflows{Jobs: []Job{
		// A job plainly named "CI". It is here so that a loose match — anything
		// short of exact equality with Gate — reports this checkout as already
		// carrying the aggregate, and hands back the wrong remedy.
		{Workflow: "ios-ci.yml", Name: "CI"},
		{Workflow: "ios-ci.yml", Name: "Lint"},
		{Workflow: "ios-ci.yml", Name: "Test"},
		{Workflow: "merge-gate.yml", Name: "Build (Release)", Conditional: true},
		{Workflow: "merge-gate.yml", Name: "Lint + Test", Conditional: true},
	}}
}

func managedWorkflows() Workflows {
	return Workflows{Jobs: []Job{
		{Workflow: "ci.yml", Name: "Build (Release)", Conditional: true},
		{Workflow: "ci.yml", Name: Gate, Conditional: true},
		{Workflow: "ci.yml", Name: "Lint", Conditional: true},
		{Workflow: "ci.yml", Name: "Test", Conditional: true},
	}}
}

// The only passing state. 14 of 17 repositories in the fleet look like this.
func TestRequiringTheAggregateGateIsTheOnlyPass(t *testing.T) {
	r := Compare("org/app", "main", Requirements{Protected: true, Contexts: []string{Gate}}, nil, managedWorkflows())
	if r.Verdict != Protected {
		t.Fatalf("a repo requiring %q was not reported as protected: %s", Gate, r.Verdict)
	}
	if Blocking([]Report{r}) != 0 || Unchecked([]Report{r}) != 0 {
		t.Errorf("a correctly protected repo was counted as a finding or as unchecked: %+v", r)
	}
}

// dailybread, live. Both required contexts were satisfied purely by skips on PR
// #482 and `CI OK` was not required at all, so "verified and passing" and
// "nothing ran" were the same green tick.
func TestRequiringOnlySkippableContextsIsAFinding(t *testing.T) {
	req := Requirements{Protected: true, Contexts: []string{"Build (Release)", "Lint + Test"}}
	r := Compare("PixelFoxStudio/dailybread", "main", req, nil, dailybreadWorkflows())
	if r.Verdict != AllRequiredSkippable {
		t.Fatalf("protection requiring neither %q nor anything unskippable passed: %s", Gate, r.Verdict)
	}
	if Blocking([]Report{r}) != 1 {
		t.Error("the finding was not counted as blocking, so a fleet sweep would exit 0 over it")
	}
	out := Format([]Report{r})
	// The mechanism has to be NAMED. "Requires the wrong contexts" is abstract;
	// "a skip satisfies it" is the sentence that makes somebody change the
	// setting.
	for _, want := range []string{"Lint + Test", "merge-gate.yml", "conditional", "SATISFIES"} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not explain how the requirement is vacuous (missing %q):\n%s", want, out)
		}
	}
}

// The lacquer's own repository, which is not lacquer-managed and has no `CI OK`
// job: it requires `test`, a job with no `if:` and no `needs:`, so nothing can
// skip it. That is a real gate under a different name, and the first live run of
// this command reported it as a finding — a confident false accusation against a
// repo doing nothing wrong. A detector that cries wolf on correct setups is one
// people learn to ignore, which costs more than the finding is worth.
func TestARequiredCheckThatCannotSkipIsNotAFinding(t *testing.T) {
	w := Workflows{Jobs: []Job{
		{Workflow: "ci.yml", Name: "VERSION is machine-assigned", Conditional: true},
		{Workflow: "ci.yml", Name: "test", AlwaysReports: true},
	}}
	req := Requirements{Protected: true, Contexts: []string{"VERSION is machine-assigned", "test"}}
	r := Compare("patrickserrano/lacquer", "main", req, nil, w)
	if r.Verdict != Protected {
		t.Fatalf("a repo requiring an unskippable check was reported %q", r.Verdict)
	}
	if Blocking([]Report{r}) != 0 {
		t.Error("an unskippable required check was counted as a finding")
	}
	out := Format([]Report{r})
	// It passes on a DIFFERENT argument than the fourteen repos that require the
	// aggregate, and a reader should be able to see which argument they got.
	if !strings.Contains(out, "cannot be skipped") || !strings.Contains(out, "test") {
		t.Errorf("the report does not say why this repo passes without requiring %s:\n%s", Gate, out)
	}

	// And the exemption is not a blanket one: drop the unskippable job and the
	// same required set is vacuous again.
	only := Workflows{Jobs: []Job{{Workflow: "ci.yml", Name: "VERSION is machine-assigned", Conditional: true}}}
	if v := Compare("x/y", "main", Requirements{Protected: true, Contexts: []string{"VERSION is machine-assigned"}}, nil, only).Verdict; v != AllRequiredSkippable {
		t.Errorf("a required set with no unskippable poster passed anyway: %s", v)
	}
}

// Windsock and dailybread-image-proxy. "Nothing required" is the same defect at
// zero, and must never read as a pass.
func TestABranchWithNoProtectionIsAFinding(t *testing.T) {
	r := Compare("org/windsock", "main", Requirements{}, nil, managedWorkflows())
	if r.Verdict != Unprotected {
		t.Fatalf("an unprotected branch was not reported: %s", r.Verdict)
	}
	if Blocking([]Report{r}) != 1 {
		t.Fatal("an unprotected branch did not count as a finding — every check on it is advisory")
	}
	if Unchecked([]Report{r}) != 0 {
		t.Error("an unprotected branch was counted as unchecked; it was checked, and the answer is bad")
	}
}

// Protection that exists but requires no status check at all.
func TestProtectionRequiringNoStatusCheckIsAFinding(t *testing.T) {
	r := Compare("org/app", "main", Requirements{Protected: true}, nil, managedWorkflows())
	if r.Verdict != NothingRequired {
		t.Fatalf("protection with zero required contexts was not reported: %s", r.Verdict)
	}
	if Blocking([]Report{r}) != 1 {
		t.Error("zero required contexts did not count as a finding")
	}
}

// The rule the whole issue turns on: "could not look" is not "looked and found
// nothing wrong". A 403 — which is what a personal account on GitHub Free
// answers for a private repo — must be neither a pass nor a finding.
func TestAFailedLookupIsNeitherAPassNorAFinding(t *testing.T) {
	r := Compare("me/private", "main", Requirements{}, errors.New("HTTP 403 reading protection"), managedWorkflows())
	if r.Verdict != Unavailable {
		t.Fatalf("a failed lookup produced verdict %q; it must be %q", r.Verdict, Unavailable)
	}
	if Blocking([]Report{r}) != 0 {
		t.Error("an unreadable repository was counted as a FINDING — that is a false accusation")
	}
	if Unchecked([]Report{r}) != 1 {
		t.Error("an unreadable repository was not counted as unchecked, so the command would exit 0 over it")
	}
	out := Format([]Report{r})
	if !strings.Contains(out, "NOT a pass") {
		t.Errorf("the report does not say that an unreadable repo is not a pass:\n%s", out)
	}
	if !strings.Contains(out, "403") {
		t.Errorf("the report drops the reason the lookup failed:\n%s", out)
	}
}

// An unprotected repo whose checkout has no aggregate job cannot be fixed by
// re-pointing protection: there is nothing to point at. The two remedies are
// different work, and a report that gave the wrong one would send somebody to
// hang every PR on a context nothing posts.
func TestTheRemedyDependsOnWhetherTheGateExistsLocally(t *testing.T) {
	withGate := Format([]Report{Compare("org/app", "main", Requirements{}, nil, managedWorkflows())})
	if !strings.Contains(withGate, "ci.yml posts "+Gate) {
		t.Errorf("a checkout that HAS the gate was not told to point protection at it:\n%s", withGate)
	}
	without := Format([]Report{Compare("org/app", "main", Requirements{}, nil, dailybreadWorkflows())})
	if !strings.Contains(without, "No job named "+Gate) {
		t.Errorf("a checkout with NO gate job was not told to re-adopt the workflow first:\n%s", without)
	}
	if strings.Contains(without, "hang") != true {
		t.Errorf("the report does not warn that requiring an unposted context hangs every PR:\n%s", without)
	}
}

// GitHub names a matrixed job's check runs "<name> (<leg>)". Treating those as
// unposted would report every multi-product repo as requiring something nothing
// posts — a false finding on exactly the projects that did the right thing.
func TestMatrixLegsCountAsPostedByTheirJob(t *testing.T) {
	w := Workflows{Jobs: []Job{{Workflow: "ci.yml", Name: "Test", Matrix: true}}}
	req := Requirements{Protected: true, Contexts: []string{"Test (Free)"}}
	r := Compare("org/app", "main", req, nil, w)
	if len(r.Posters["Test (Free)"]) != 1 {
		t.Fatalf("a matrix leg was not attributed to its job: %+v", r.Posters)
	}
	if len(r.Posters["Test"]) != 0 {
		t.Error("a context nobody requires was given posters")
	}
	// And the containment must not be loose: an unrelated name that merely
	// starts with the job name is a different context.
	other := Compare("org/app", "main", Requirements{Protected: true, Contexts: []string{"Testing"}}, nil, w)
	if len(other.Posters["Testing"]) != 0 {
		t.Error("a different context sharing a prefix was claimed as posted by this job")
	}
}

// A job whose name interpolates an expression might post the required context
// and might not. Claiming either way would be reporting a guess as a
// measurement — and the claim at risk here is the report's strongest sentence,
// "posted by NOTHING in this checkout", which is what sends somebody deleting a
// branch-protection rule.
func TestADynamicJobNameQualifiesThePostedByNothingClaim(t *testing.T) {
	w := Workflows{Jobs: []Job{{Workflow: "ci.yml", Name: "${{ matrix.name }}", Dynamic: true}}}
	r := Compare("org/app", "main", Requirements{Protected: true, Contexts: []string{"Lint"}}, nil, w)
	if len(r.Posters["Lint"]) != 0 {
		t.Errorf("a dynamically-named job was claimed to post a context it may not: %+v", r.Posters)
	}
	out := Format([]Report{r})
	if strings.Contains(out, "posted by NOTHING") {
		t.Errorf("the report asserts nothing posts the context although a job name could not be resolved:\n%s", out)
	}
	if !strings.Contains(out, "templated") {
		t.Errorf("the report does not say WHY it cannot name a poster:\n%s", out)
	}

	// The unqualified sentence is still available, and is still said when it was
	// actually established — otherwise this softening would have destroyed the
	// finding rather than made it honest.
	plain := Format([]Report{Compare("org/app", "main",
		Requirements{Protected: true, Contexts: []string{"Lint"}}, nil,
		Workflows{Jobs: []Job{{Workflow: "ci.yml", Name: Gate}}})})
	if !strings.Contains(plain, "posted by NOTHING") {
		t.Errorf("a context genuinely posted by nothing was not reported as such:\n%s", plain)
	}
}

// Same rule for a workflow that would not parse: some of the evidence was never
// read, so the claim is qualified rather than rounded down to a confident one.
func TestAnUnreadableWorkflowQualifiesThePostedByNothingClaim(t *testing.T) {
	w := Workflows{Jobs: []Job{{Workflow: "ci.yml", Name: Gate}}, Unreadable: []string{"broken.yml"}}
	out := Format([]Report{Compare("org/app", "main",
		Requirements{Protected: true, Contexts: []string{"Lint"}}, nil, w)})
	if strings.Contains(out, "posted by NOTHING") {
		t.Errorf("an unreadable workflow was treated as evidence that nothing posts the context:\n%s", out)
	}
	if !strings.Contains(out, "broken.yml would not parse") {
		t.Errorf("the report does not name the file it could not read:\n%s", out)
	}
}

// Passing repositories get a line too. A report that printed only findings
// would leave an operator unable to tell "17 checked, all fine" from "17 never
// looked at" — which is the defect this package is about, wearing the report's
// clothes.
func TestPassingRepositoriesStillAppearInTheReport(t *testing.T) {
	rs := []Report{
		Compare("org/a", "main", Requirements{Protected: true, Contexts: []string{Gate}}, nil, managedWorkflows()),
		Compare("org/b", "main", Requirements{}, nil, managedWorkflows()),
	}
	out := Format(rs)
	if !strings.Contains(out, "org/a@main") || !strings.Contains(out, "org/b@main") {
		t.Errorf("not every repository appears in the report:\n%s", out)
	}
	if !strings.Contains(out, "2 checked, 1 finding(s), 0 could not be checked.") {
		t.Errorf("the tally does not separate checked / findings / unchecked:\n%s", out)
	}
}

// A roster entry whose repository cannot even be identified still occupies a
// line, and still counts as unchecked.
func TestAnUnidentifiableRepositoryIsReportedNotDropped(t *testing.T) {
	r := Unreachable("some-project", errors.New("no origin remote"))
	if r.Verdict != Unavailable || Unchecked([]Report{r}) != 1 {
		t.Fatalf("an unidentifiable project was not counted as unchecked: %+v", r)
	}
	if out := Format([]Report{r}); !strings.Contains(out, "some-project") {
		t.Errorf("the project is missing from the report entirely:\n%s", out)
	}
}

func writeWorkflow(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, ".github", "workflows", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalReadsNamesConditionalsAndMatrices(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "ci.yml", `
name: CI
on:
  pull_request:
jobs:
  lint:
    name: Lint
    if: needs.changes.outputs.swift == 'true'
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
  test:
    name: Test
    strategy:
      matrix:
        product: [Free, Pro]
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
  ci-ok:
    name: CI OK
    if: always()
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
  bare:
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
  gated:
    name: Gated
    needs: [lint]
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
  templated:
    name: ${{ matrix.product }} Build
    strategy:
      matrix:
        product: [Free, Pro]
    runs-on: ubuntu-latest
    steps: [{run: "true"}]
`)
	w, err := Local(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Job{}
	for _, j := range w.Jobs {
		got[j.Name] = j
	}
	if len(got) != 6 {
		t.Fatalf("expected six jobs, got %d: %+v", len(got), w.Jobs)
	}
	// Skippability, which is what decides whether requiring a name buys a gate.
	if !got["bare"].AlwaysReports {
		t.Error("a job with no `if:` and no `needs:` was not recognised as always reporting")
	}
	if got["Lint"].AlwaysReports {
		t.Error("a job with an `if:` was treated as always reporting — requiring it would be a gate a skip satisfies")
	}
	if !got[Gate].AlwaysReports {
		t.Errorf("`if: always()` was not recognised as always reporting, which would flag every correctly-protected repo")
	}
	// Transitive: `needs` a job that can skip, so it can skip too.
	if got["Gated"].AlwaysReports {
		t.Error("a job needing a skippable job was treated as unskippable; GitHub skips a job whose dependency skipped")
	}
	// An expression in the name means the posted context cannot be read off the
	// file. Failing to record that is how a report ends up asserting "posted by
	// NOTHING" about a job whose name it never resolved.
	if !got["${{ matrix.product }} Build"].Dynamic {
		t.Error("a templated job name was not recorded as dynamic")
	}
	if got["Lint"].Dynamic {
		t.Error("a literal job name was recorded as dynamic, which would suppress a real finding")
	}
	if !got["Lint"].Conditional {
		t.Error("a job with an `if:` was not recorded as conditional — that is the property that makes requiring it unsafe")
	}
	if !got["Test"].Matrix {
		t.Error("a matrixed job was not recorded as such, so its legs would read as unposted")
	}
	if got[Gate].Workflow != "ci.yml" {
		t.Errorf("the aggregate gate was not attributed to its workflow: %+v", got[Gate])
	}
	// A job with no `name:` still posts a context, named after its key.
	if _, ok := got["bare"]; !ok {
		t.Errorf("a job without a `name:` was dropped; GitHub posts it under its key: %+v", w.Jobs)
	}
}

// A file that could not be parsed is not evidence that the repository posts
// nothing from it. If this collapsed into "no jobs found", an unreadable
// workflow would manufacture "required context posted by NOTHING" findings.
func TestAnUnparseableWorkflowIsRecordedNotSilentlyEmpty(t *testing.T) {
	dir := t.TempDir()
	writeWorkflow(t, dir, "broken.yml", "jobs:\n  - this is not: [a mapping\n")
	w, err := Local(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Unreadable) != 1 || w.Unreadable[0] != "broken.yml" {
		t.Fatalf("an unparseable workflow was swallowed: %+v", w)
	}
	r := Compare("org/app", "main", Requirements{Protected: true, Contexts: []string{Gate}}, nil, w)
	if out := Format([]Report{r}); !strings.Contains(out, "broken.yml could not be parsed") {
		t.Errorf("the report does not admit that a workflow was unreadable:\n%s", out)
	}
}

// Plenty of repositories in a roster have no workflows at all. That is a fact
// to compare against their protection, not a reason to abort the sweep.
func TestNoWorkflowsDirectoryIsNotAnError(t *testing.T) {
	w, err := Local(t.TempDir())
	if err != nil {
		t.Fatalf("a checkout with no .github/workflows errored: %v", err)
	}
	if len(w.Jobs) != 0 || len(w.Unreadable) != 0 {
		t.Errorf("invented workflow content out of an empty checkout: %+v", w)
	}
}
