package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The wiring test for the OTHER half of #334. covered_elsewhere stopped the
// audit lying about a hand-written watch workflow; this is the capability that
// makes the hand-written workflow unnecessary.
//
// It is the only place the manifest field, the pbxproj parse and the report
// meet. A unit test proving WatchTestSelectors returns the target says nothing
// about whether `audit` consults it — which is exactly how a target that CI runs
// on every pull request came to be reported as running nowhere.
func TestAuditCountsAWatchTargetTheLacquerNowRuns(t *testing.T) {
	lq := realLacquer(t)
	dir := fixtureProject(t, lq)
	chdir(t, dir)
	env := envMap(map[string]string{"LACQUER_ROOT": lq})

	p := filepath.Join(dir, "fixture.xcodeproj", "project.pbxproj")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(watchPbxproj), 0o644); err != nil {
		t.Fatal(err)
	}

	audit := func() string {
		t.Helper()
		var out, errb bytes.Buffer
		run([]string{"audit"}, env, &out, &errb)
		return out.String() + errb.String()
	}

	// FIRST, with no declaration at all: the watch bundle must be reported. A
	// test that only checked the "after" state would pass against an audit that
	// had simply stopped looking.
	writeManifest(t, dir, "xcodeproj = \"fixture.xcodeproj\"")
	before := audit()
	if !strings.Contains(uncoveredSection(before), "fixture Watch AppTests") {
		t.Fatalf("the undeclared watch bundle is not reported as uncovered, so this test cannot show the declaration doing anything:\n%s", before)
	}

	// Now declare it. No covered_elsewhere, no hand-written workflow — the
	// lacquer renders the job, so the target is covered by a managed selector.
	writeManifest(t, dir, "xcodeproj = \"fixture.xcodeproj\"\n\n"+
		"[project.watch_tests]\n"+
		"scheme = \"fixture Watch App\"\n"+
		"test_target = \"fixture Watch AppTests\"")
	after := audit()
	if strings.Contains(uncoveredSection(after), "fixture Watch AppTests") {
		t.Errorf("a watch target the managed workflow now runs is still reported as covered by nothing — the capability landed and the audit did not hear about it:\n%s", after)
	}
	// And it must not have been silenced by being routed through the
	// covered-elsewhere exception path, which reports a live exception a reader
	// is meant to review.
	if strings.Contains(after, "covered by a workflow this lacquer does not manage") {
		t.Errorf("the watch target is being excused as covered elsewhere rather than counted as covered here:\n%s", after)
	}
}
