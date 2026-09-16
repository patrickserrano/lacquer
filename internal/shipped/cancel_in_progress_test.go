package shipped

// This file guards a 2026-09 fleet-wide CI correctness bug: every shipped
// workflow's top-level `concurrency:` block set `cancel-in-progress: true`
// unconditionally, with a group keyed on `github.ref`
// (`ci-${{ github.workflow }}-${{ github.ref }}` or a profile-prefixed
// variant).
//
// That key is exactly why the bug is invisible on a pull request and real on
// main: a PR's ref is that PR's own, so it only ever cancels ITS OWN earlier,
// superseded run — pure churn saving, and worth keeping. A push to main
// shares ONE group with every other push to main, so an unconditional
// cancel-in-progress makes merges cancel each other's verification. `check`
// lints, typechecks, tests and builds; when a push to main has that run
// cancelled, the commit sits on main verified by nothing. GitHub records a
// cancelled run as `cancelled`, not `failure`, so no required check, branch
// protection rule, or dashboard treats it as a problem — it is an absence,
// not a failure, the same shape as a skipped required check satisfying
// branch protection (see this repo's CLAUDE.md).
//
// The fix is `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`
// wherever the bug applied, keeping cancellation on PRs and letting a push to
// main run to completion. Some workflows deliberately keep `false`
// unconditionally instead — profiles/ios/workflows/release.yml serializes a
// tag-triggered release train behind a FIXED group name (not keyed on
// github.ref at all), where cancelling mid-release would leave signing or
// upload half-done. That is not this bug and must not be "fixed" into the
// conditional form; cancelInProgressExceptions below is the one place such a
// deliberate exception is allowed, and it must carry the reason and the
// exact expected value inline so a mutation that either (a) reverts a real
// workflow back to unconditional `true`, or (b) "corrects" a deliberate
// exception into the conditional form, fails a named assertion here rather
// than passing silently.
//
// This test does not key on any of the comments above the `concurrency:`
// blocks it checks — only on the parsed `cancel-in-progress:` value itself.
// A comment that says the right thing while the value stays wrong is exactly
// the defect class this repo's CLAUDE.md exists to prevent.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// wantCancelInProgress is the event-conditional expression every workflow's
// top-level concurrency block must use unless explicitly excepted below. It
// cancels a run only when the triggering event is a pull_request, which is
// correct for every workflow this test covers: each one's `push:` trigger is
// restricted to `branches: [main]` (see each workflow's `on:` block), so the
// only alternative to a pull_request run is a push to main, and that is
// exactly the case that must NOT be cancelled.
const wantCancelInProgress = "${{ github.event_name == 'pull_request' }}"

// cancelInProgressException records one workflow file that deliberately does
// NOT use wantCancelInProgress, the exact value it must carry instead, and
// why. Keying the assertion on an explicit expected value (not just "this
// file is exempt, skip it") is what makes mutation (b) above -- silently
// "fixing" a deliberate exception into the conditional form -- fail loudly:
// the exception exists to protect files that must NOT change, not to make
// them invisible to this test.
type cancelInProgressException struct {
	wantValue string
	reason    string
}

// cancelInProgressExceptions is the complete, explicit allow-list of shipped
// workflows whose top-level concurrency block is deliberately NOT the
// event-conditional form. Paths are relative to the repo root, slash-
// separated, matching what filepath.Rel + filepath.ToSlash produce below.
var cancelInProgressExceptions = map[string]cancelInProgressException{
	"profiles/ios/workflows/release.yml": {
		wantValue: "false",
		reason: "tag-triggered release train, not PR/push CI -- its group is the FIXED " +
			"string \"ios-release\" (not keyed on github.ref at all), deliberately so every " +
			"release queues serially instead of racing. Cancelling a run mid-release leaves " +
			"signing or TestFlight upload half-done, which is worse than waiting; the fixed " +
			"group is what makes cancel-in-progress: false safe here without wedging concurrent, " +
			"unrelated CI runs the way it would if applied fleet-wide.",
	},
}

// cancelInProgressWorkflowFiles finds every workflow file this repo ships to
// projects (profiles/*/workflows/*.yml, via shippedWorkflowFiles) plus
// lacquer's OWN CI workflow (.github/workflows/ci.yml) -- the fix applies to
// both, since this repo's own main branch is exposed to exactly the same bug.
// It takes a directory rather than always reading root(t) so a test can point
// it at an empty directory and prove the "found nothing" path actually fails
// instead of passing vacuously.
func cancelInProgressWorkflowFiles(dir string) ([]string, error) {
	paths, err := shippedWorkflowFiles(dir)
	if err != nil {
		return nil, err
	}
	own := filepath.Join(dir, ".github", "workflows", "ci.yml")
	if _, statErr := os.Stat(own); statErr == nil {
		paths = append(paths, own)
	}
	sort.Strings(paths)
	return paths, nil
}

// concurrencyDoc captures just enough of a rendered workflow to read its
// top-level concurrency block. cancel-in-progress is read as a raw
// yaml.Node, never decoded into a Go bool or string field: the correct value
// is a `${{ ... }}` expression (a plain string scalar) and a deliberately
// exempted value is a literal `false` (a bool scalar) -- two different YAML
// tags for the same key, either of which must decode cleanly so this test
// can compare its literal text.
type concurrencyDoc struct {
	Concurrency struct {
		Group            string    `yaml:"group"`
		CancelInProgress yaml.Node `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
}

// TestCancelInProgressOnlyOnPullRequest parses every shipped workflow plus
// lacquer's own .github/workflows/ci.yml and asserts each workflow's
// top-level `concurrency.cancel-in-progress` is the event-conditional form
// (wantCancelInProgress), unless the file is named in
// cancelInProgressExceptions -- in which case it must carry THAT exception's
// exact expected value instead.
//
// If this parses zero workflow files, or finds zero top-level
// cancel-in-progress keys, it fails outright rather than reporting a vacuous
// pass -- a selector that silently matches nothing is its own defect class in
// this repo (see CLAUDE.md).
func TestCancelInProgressOnlyOnPullRequest(t *testing.T) {
	runCancelInProgressCheck(t, root(t))
}

// runCancelInProgressCheck is the guts of the test, factored out so it can be
// pointed at an arbitrary directory (an empty one included) to prove the
// "parsed nothing" path actually fails rather than passing vacuously -- see
// the mutation recorded in this PR's body for how that was verified.
func runCancelInProgressCheck(t *testing.T, dir string) {
	t.Helper()

	paths, err := cancelInProgressWorkflowFiles(dir)
	if err != nil {
		t.Fatalf("cancelInProgressWorkflowFiles: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("cancelInProgressWorkflowFiles found zero workflow files -- the glob, the " +
			".github/workflows/ci.yml lookup, or the directory is broken, not the fleet; this " +
			"test must not pass vacuously")
	}

	var foundKeys int
	var violations []string

	for _, path := range paths {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		// renderShippedWorkflow (internal/shipped/run_step_shell_e_test.go)
		// substitutes {{TOKEN}} placeholders via tokens.Substitute before
		// parsing. profiles/web/workflows/ci.yml contains a {{WEB_AUDIT}}
		// token that yaml.v3 cannot decode into a Go map unrendered; a file
		// with no tokens at all (lacquer's own .github/workflows/ci.yml)
		// passes through Substitute unchanged, since it only replaces tokens
		// it finds present. One code path handles both.
		out, err := renderShippedWorkflow(t, path)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}

		var doc concurrencyDoc
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%s: rendered workflow is not valid YAML: %v", rel, err)
		}

		if doc.Concurrency.CancelInProgress.IsZero() {
			// No top-level concurrency block, or one with no
			// cancel-in-progress key (e.g. profiles/ios/workflows/
			// cleanup-ci.yml, profiles/supabase/workflows/health.yml,
			// profiles/web/workflows/{dependency-review,env-validation}.yml).
			// Nothing to assert.
			continue
		}
		foundKeys++

		got := doc.Concurrency.CancelInProgress.Value
		want := wantCancelInProgress
		reason := ""
		if exc, ok := cancelInProgressExceptions[rel]; ok {
			want = exc.wantValue
			reason = " (allow-listed exception: " + exc.reason + ")"
		}

		if got != want {
			violations = append(violations, fmt.Sprintf(
				"%s: concurrency.cancel-in-progress is %q, want %q%s",
				rel, got, want, reason))
		}
	}

	if foundKeys == 0 {
		t.Fatal("found zero top-level concurrency.cancel-in-progress keys across every " +
			"checked workflow -- either the shape moved or the detector is broken; this test " +
			"must not pass vacuously")
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("%d workflow(s) have a wrong concurrency.cancel-in-progress value:\n%s\n\n"+
			"On a pull_request the concurrency group is keyed on that PR's own ref, so cancelling "+
			"only ever kills that PR's own earlier run -- pure churn saving. On a push to main "+
			"every commit shares ONE group, so an unconditional cancel-in-progress makes merges "+
			"cancel each other's verification, and GitHub records the cancelled run as "+
			"`cancelled`, not `failure` -- nothing red to show for an unverified commit on main.",
			len(violations), strings.Join(violations, "\n"))
	}
}
