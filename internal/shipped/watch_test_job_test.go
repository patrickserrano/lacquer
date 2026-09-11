package shipped

import (
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// A watchOS test bundle could not run through the managed workflow at all: it is
// a testable of a DIFFERENT scheme, so naming it in extra_test_targets fails with
// "isn't a member of the specified test plan or scheme", and the Test job carries
// exactly one destination, platform=iOS Simulator. Projects solved it with a
// hand-written workflow — which the uncovered-target audit then reported as a
// violation.
//
// These tests assert the rendered job actually stands the suite up and actually
// reads a verdict back, not merely that a job with the right name appears. The
// failure mode being designed against is a watch job that runs zero tests and
// reports a pass, which is what xcodebuild does for a selector that matches
// nothing.

// watchProject is the shape the project this was built for is in: one app, no
// [[product]], and a watch bundle declared through the single-product spelling.
func watchProject() *config.Config {
	cfg := soloConfig()
	cfg.Project.WatchTests = &config.WatchTests{
		Scheme:     "DailyBreadWatchApp Watch App",
		TestTarget: "DailyBreadWatchApp Watch AppTests",
	}
	return cfg
}

// watchStep, watchJobT and watchDoc are the rendered workflow, typed just far
// enough to make assertions about the watch job specifically.
type watchStep struct {
	Name string         `yaml:"name"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
}

type watchJobT struct {
	Name     string `yaml:"name"`
	RunsOn   any    `yaml:"runs-on"`
	Needs    any    `yaml:"needs"`
	If       string `yaml:"if"`
	Strategy struct {
		FailFast bool                        `yaml:"fail-fast"`
		Matrix   map[string][]map[string]any `yaml:"matrix"`
	} `yaml:"strategy"`
	Env   map[string]string `yaml:"env"`
	Steps []watchStep       `yaml:"steps"`
}

type watchDoc struct {
	Jobs map[string]watchJobT `yaml:"jobs"`
}

func renderWatchDoc(t *testing.T, cfg *config.Config) watchDoc {
	t.Helper()
	var doc watchDoc
	if err := yaml.Unmarshal([]byte(renderIOSCI(t, cfg)), &doc); err != nil {
		t.Fatalf("rendered ci.yml is not valid YAML: %v", err)
	}
	return doc
}

func TestWatchProductRendersAWatchOSDestination(t *testing.T) {
	doc := renderWatchDoc(t, watchProject())
	job, ok := doc.Jobs["watch-test"]
	if !ok {
		t.Fatal("a project declaring watch_tests renders no watch-test job, so the bundle still has nowhere to run")
	}

	legs := job.Strategy.Matrix["watch"]
	if len(legs) != 1 {
		t.Fatalf("got %d watch legs, want 1: %v", len(legs), legs)
	}
	leg := legs[0]
	for _, tc := range []struct{ key, want string }{
		// The two halves of the gap, both per-leg.
		{"scheme", "DailyBreadWatchApp Watch App"},
		{"test_target", "DailyBreadWatchApp Watch AppTests"},
		// THE destination. This is the field that did not exist: without it a
		// leg inherits platform=iOS Simulator no matter which scheme it names.
		{"destination_prefix", "platform=watchOS Simulator"},
		// And the simulator facts, all from the platform table rather than the
		// manifest.
		{"device_type", "Apple Watch Series 12 (46mm)"},
		{"runtime", "com.apple.CoreSimulator.SimRuntime.watchOS-27-0"},
		{"ready_service", "com.apple.Carousel"},
		{"sim_prefix", "CI-Watch"},
	} {
		if got, _ := leg[tc.key].(string); got != tc.want {
			t.Errorf("matrix leg %s = %q, want %q", tc.key, got, tc.want)
		}
	}

	body := watchJobBody(t, job.Steps)
	// The destination as xcodebuild actually receives it: the prefix, plus the
	// device the setup step created. A prefix carried through the environment
	// with the id baked in would arrive as four literal characters.
	if !strings.Contains(body, `-destination "${WATCH_DESTINATION_PREFIX},id=$DEVICE_ID"`) {
		t.Error("the test invocation does not point at the created watch device")
	}
	if !strings.Contains(body, `"-only-testing:$WATCH_TEST_TARGET"`) {
		t.Error("the watch test target is not passed as an -only-testing: selector")
	}
	if !strings.Contains(body, `-scheme "$WATCH_SCHEME"`) {
		t.Error("the watch job does not build the watch scheme")
	}
	// It must not reach for the iOS destination, which is the bug in miniature.
	if strings.Contains(body, "platform=iOS Simulator") {
		t.Error("the watch job carries an iOS simulator destination")
	}
}

// The pairing knowledge is the part worth not rediscovering per project: a
// PAIRED watch activates a real WCSession, so a suite written against the
// unpaired state exercises different behaviour and reports it as a pass.
func TestWatchJobCreatesAndAssertsAnUnpairedDevice(t *testing.T) {
	job := watchJob(t, watchProject())
	body := watchJobBody(t, job.Steps)

	// Run-scoped, and per leg. GITHUB_RUN_ID alone is shared by the legs of one
	// run, so a cleanup keyed on it would delete a sibling's live device.
	if !strings.Contains(body, `SIM_NAME="${WATCH_SIM_PREFIX}-${GITHUB_RUN_ID}-${WATCH_SLUG}"`) {
		t.Error("the watch simulator name is not scoped to this run AND this leg")
	}
	if !strings.Contains(body, `xcrun simctl create "$SIM_NAME" "$WATCH_DEVICE_TYPE"`) {
		t.Error("the job does not CREATE its own device; hunting for an existing unpaired watch is not stable — three of five stock watchOS 27.0 simulators on this fleet's runner were already paired")
	}
	// Asserted, not assumed.
	if !strings.Contains(body, "xcrun simctl list pairs") {
		t.Error("the job never checks whether the device it created is paired")
	}
	if !strings.Contains(body, "::error::") || !strings.Contains(body, "WCSession") {
		t.Error("a paired device does not fail the job with an explanation of why it matters")
	}
	// The readiness poll must be the watch shell. Measured on a booted watchOS
	// 27.0 simulator: launchctl carries com.apple.Carousel and no SpringBoard,
	// so the iOS job's poll copied across always times out.
	//
	// Checked against the EXECUTABLE lines only. Keying on the whole body would
	// match this job's own comment explaining why SpringBoard is wrong here,
	// which is a detector reading prose rather than the thing it guards.
	for _, line := range shellLines(body) {
		if strings.Contains(strings.ToLower(line), "springboard") {
			t.Errorf("the watch job polls for SpringBoard, which a watchOS simulator does not run: %s", line)
		}
	}
	if !strings.Contains(body, `grep -q "$WATCH_READY_SERVICE"`) {
		t.Error("the watch job does not poll for the platform's readiness service")
	}
	// And it cleans up only what it made.
	if !strings.Contains(body, `grep "${WATCH_SIM_PREFIX}-${GITHUB_RUN_ID}-${WATCH_SLUG} ("`) {
		t.Error("the cleanup is not anchored to this leg's exact device name; an unanchored prefix match reaches a sibling leg's live device")
	}
}

// The #333 defect class, inverted: a check whose passing state is reachable
// without the checked thing having happened. A watch suite that runs zero tests
// exits 0 and reports "0 failed".
func TestWatchJobFailsWhenTheSuiteDidNotActuallyRun(t *testing.T) {
	job := watchJob(t, watchProject())

	var assert string
	for _, st := range job.Steps {
		if strings.Contains(st.Name, "Assert") {
			assert = st.Run
		}
	}
	if assert == "" {
		t.Fatal("the watch job has no assertion step; the xcodebuild exit code alone cannot express \"ran nothing\"")
	}
	for _, want := range []struct{ needle, why string }{
		{`if [ ! -d "WatchTestResults.xcresult" ]`, "an absent result bundle is not a pass"},
		{`.totalTestCount`, "a suite that executed zero tests must fail"},
		{`[ "$TOTAL" = "0" ]`, "zero executed tests must be named as the failure"},
		{`[ "$RESULT" != "Passed" ]`, "only Passed is a pass — \"unknown\" is xcresulttool saying it could not tell"},
	} {
		if !strings.Contains(assert, want.needle) {
			t.Errorf("the assertion does not check %s (missing %q)", want.why, want.needle)
		}
	}
	// NOT ONE `|| true` in the whole step. One anywhere in here turns every
	// branch above into decoration: the read succeeds by definition, the
	// verdict is whatever the swallowed failure left behind, and the job is
	// green. Checked on executable lines so the step's own comment about not
	// having any does not satisfy it.
	for _, line := range shellLines(assert) {
		if strings.Contains(line, "|| true") {
			t.Errorf("the assertion step swallows a failure, so a read it could not perform reports a pass: %s", line)
		}
	}
	// The exit code is recorded by the test step, not acted on — otherwise a
	// non-zero exit would skip the step that catches the zero-test case.
	for _, st := range job.Steps {
		if st.Name == "Run Watch Tests" && !strings.Contains(st.Run, `echo "exit_code=$EXIT_CODE" >> "$GITHUB_OUTPUT"`) {
			t.Error("the test step does not hand its exit code to the assertion step")
		}
	}
}

// A job nothing requires is a job that can fail without blocking anything. "CI
// OK" is the check branch protection points at, and it is the only name worth
// requiring — so the watch job has to be in its needs[] AND in its result loop.
func TestWatchJobIsRequiredByTheCIOKGate(t *testing.T) {
	doc := renderWatchDoc(t, watchProject())
	gate, ok := doc.Jobs["ci-ok"]
	if !ok {
		t.Fatal("no ci-ok job")
	}
	needs, _ := gate.Needs.([]any)
	var found bool
	for _, n := range needs {
		if s, _ := n.(string); s == "watch-test" {
			found = true
		}
	}
	if !found {
		t.Errorf("CI OK does not need watch-test: %v", gate.Needs)
	}
	var body string
	for _, st := range gate.Steps {
		body += st.Run
	}
	// needs[] alone would make the gate WAIT for the job and then ignore its
	// result, which looks like coverage and is not.
	if !strings.Contains(body, `"${{ needs.watch-test.result }}"`) {
		t.Errorf("CI OK waits for watch-test but never reads its result:\n%s", body)
	}
}

// The same-repo guard is what keeps fork-PR code off the self-hosted Mac. A new
// job that compiles this repository's code with a weaker condition is how that
// guard stops meaning anything.
func TestWatchJobCarriesTheSameForkGuardAsTheTestJob(t *testing.T) {
	doc := renderWatchDoc(t, watchProject())
	watch := doc.Jobs["watch-test"]
	test := doc.Jobs["test"]
	if watch.If == "" || watch.If != test.If {
		t.Errorf("watch-test's condition differs from the Test job's.\n  test:  %s\n  watch: %s", test.If, watch.If)
	}
	if watch.Strategy.FailFast {
		t.Error("fail-fast is on: one product's watch suite failing would cancel the others, and a cancelled sibling reports no result at all")
	}
}

// The whole feature is opt-in, and this is the constraint it is subordinate to:
// twelve repositories declare no [[product]] and must receive the file they
// already have. TestIOSCISingleProductRenderIsUnchanged pins the bytes; this
// pins the SHAPE, so a future change that renders an empty job stanza — valid
// YAML, byte-different — fails with a message about the job rather than about
// line 1943.
func TestProjectWithoutWatchTestsRendersNoWatchJob(t *testing.T) {
	doc := renderWatchDoc(t, soloConfig())
	if _, ok := doc.Jobs["watch-test"]; ok {
		t.Error("a project with no watch_tests rendered a watch-test job")
	}
	gate := doc.Jobs["ci-ok"]
	needs, _ := gate.Needs.([]any)
	for _, n := range needs {
		if s, _ := n.(string); s == "watch-test" {
			t.Error("CI OK needs a watch-test job that is not rendered; the whole workflow would never start")
		}
	}
	var body string
	for _, st := range gate.Steps {
		body += st.Run
	}
	if strings.Contains(body, "watch-test") {
		t.Error("the CI OK gate mentions watch-test in a project that renders no such job")
	}
	// And the multi-product matrix path must be just as unaffected.
	multi := renderWatchDoc(t, twoIOSProducts())
	if _, ok := multi.Jobs["watch-test"]; ok {
		t.Error("a two-product project with no watch_tests rendered a watch-test job")
	}
}

// Two products, one with a watch bundle. The watch job must carry exactly the
// leg that declared one — not a leg per product, which would run the same
// watch suite twice, and not one merged leg, which would run it under the wrong
// scheme.
func TestOnlyProductsDeclaringWatchTestsGetALeg(t *testing.T) {
	cfg := twoIOSProducts()
	cfg.Product[1].WatchTests = &config.WatchTests{
		Scheme:     "Free Watch App",
		TestTarget: "Free Watch AppTests",
	}
	job := watchJob(t, cfg)
	legs := job.Strategy.Matrix["watch"]
	if len(legs) != 1 {
		t.Fatalf("got %d watch legs, want 1 (only the Free product declares a watch bundle): %v", len(legs), legs)
	}
	if got, _ := legs[0]["product"].(string); got != "Free" {
		t.Errorf("the watch leg belongs to %q, want Free", got)
	}
	if got, _ := legs[0]["slug"].(string); got != "free" {
		t.Errorf("the watch leg's slug is %q, want free — the slug scopes the simulator name and the uploaded artifact", got)
	}
}

// watchJob renders cfg and returns the watch-test job, failing if there is none.
func watchJob(t *testing.T, cfg *config.Config) watchJobT {
	t.Helper()
	doc := renderWatchDoc(t, cfg)
	job, ok := doc.Jobs["watch-test"]
	if !ok {
		t.Fatal("no watch-test job was rendered")
	}
	return job
}

// shellLines is the EXECUTABLE lines of a shell body: comments and blanks
// dropped.
//
// Every assertion about what this job does reads these rather than the raw text,
// because the job is heavily commented — by design, since the knowledge in those
// comments is the reason it exists in the lacquer instead of in fifteen
// project-owned workflows. A check that matched the prose would pass on a job
// that DESCRIBES doing the right thing and fail on one that explains why the
// wrong thing is wrong.
func shellLines(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// watchJobBody is every step's shell, concatenated.
func watchJobBody(t *testing.T, steps []watchStep) string {
	t.Helper()
	var b strings.Builder
	for _, st := range steps {
		b.WriteString(st.Run)
	}
	if b.Len() == 0 {
		t.Fatal("the watch job has no shell at all")
	}
	return b.String()
}
