package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// The iOS CI workflow builds and tests every declared [[product]]. Two
// repositories in this fleet ship a paid and a free variant from one tree, and
// the only way to build both was to FORK this workflow — which blocks the whole
// lacquer sync for as long as the fork exists (one of them sat 71 files behind).
//
// The failure this must not create is the opposite one. Handing those two the
// single-scheme workflow would silently stop every Free-variant build and test
// while turning CI green: nothing errors, the checks pass, and half the shipped
// code stops being covered. So the tests below assert the legs EXIST and carry
// the right per-product values, not merely that the file parses.
//
// And the constraint that outranks all of it: TWELVE projects declare no
// [[product]] at all, and must receive a byte-identical file. That is the first
// test here, because a difference there is drift in twelve repositories at once.

// iosCIPath is the template every test in this file renders.
func iosCIPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(root(t), "profiles", "ios", "workflows", "ci.yml")
}

// renderIOSCI renders profiles/ios/workflows/ci.yml the way sync would.
func renderIOSCI(t *testing.T, cfg *config.Config) string {
	t.Helper()
	raw, err := os.ReadFile(iosCIPath(t))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
	if len(missing) > 0 {
		t.Fatalf("ios ci.yml has unsubstituted tokens: %v", missing)
	}
	if m := lacquerToken.FindString(out); m != "" {
		t.Fatalf("rendered ios ci.yml still contains the lacquer token %s", m)
	}
	return out
}

// soloConfig is the shape twelve projects are in: one app, no [[product]].
func soloConfig() *config.Config {
	return &config.Config{Project: config.Project{
		Name: "demo", ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
		AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
	}}
}

// twoIOSProducts models a-bible-verse-each-day's forked matrix, including the
// two things a naive implementation gets wrong: the built app target differs
// from the scheme ("A Bible Verse Daily.app" vs "A Bible Verse Each Day Free"),
// and only one variant has UI tests.
func twoIOSProducts() *config.Config {
	cfg := soloConfig()
	cfg.Product = []config.Product{
		{
			Name: "Paid", Scheme: "A Bible Verse Each Day", BundleID: "com.x.paid",
			AscAppID: "111", TagPrefix: "paid",
			TestTarget: "A Bible Verse Each DayTests", UITestTarget: "A Bible Verse Each DayUITests",
			AppTarget: "A Bible Verse Daily.app",
		},
		{
			Name: "Free", Scheme: "A Bible Verse Each Day Free", BundleID: "com.x.free",
			AscAppID: "222", TagPrefix: "free",
			TestTarget: "A Bible Verse Each Day FreeTests",
			AppTarget:  "A Bible Verse Daily Free.app",
		},
	}
	return cfg
}

// legacyIOSCITokens is the EXACT text each IOS_CI_* token replaced in ci.yml
// before the product matrix existed.
//
// This is the pin. Substituting these back into the current template reproduces,
// character for character, the file origin/main renders — so comparing that
// against the real single-product render proves the twelve projects that declare
// no [[product]] receive a byte-identical workflow.
//
// It is written as the OLD SPELLING rather than as a golden copy of the rendered
// file on purpose: a golden would have to be regenerated on every legitimate
// ci.yml edit, which turns the guard into a rubber stamp. This survives edits to
// the surrounding workflow, because both sides read the same template, and only
// fails when a renderer stops agreeing with the pre-matrix form.
var legacyIOSCITokens = map[string]string{
	"{{IOS_CI_PRODUCT_SUFFIX}}":  "",
	"{{IOS_CI_BUILD_STRATEGY}}":  "",
	"{{IOS_CI_TEST_STRATEGY}}":   "",
	"{{IOS_CI_ARTIFACT_SUFFIX}}": "",
	"{{IOS_CI_SIM_SUFFIX}}":      "",
	"{{IOS_CI_SIM_MATCH}}":       "",
	"{{IOS_CI_SCHEME}}":          "{{SCHEME}}",
	"{{IOS_CI_ONLY_TESTING}}":    `"-only-testing:{{PROJECT_NAME}}Tests"`,
	"{{IOS_CI_APP_TARGET}}":      "{{PROJECT_NAME}}.app",
	"{{IOS_CI_COVERAGE_JQ}}":     `'.targets[] | select(.name == "{{PROJECT_NAME}}.app") | .lineCoverage * 100'`,
}

// iosCITokenRe finds every IOS_CI_* placeholder in the template.
var iosCITokenRe = regexp.MustCompile(`\{\{IOS_CI_[A-Z_]+\}\}`)

// TestIOSCISingleProductRenderIsUnchanged is the requirement this whole change
// is subordinate to. Twelve repositories declare no [[product]]; any difference
// in what they render is drift in all twelve on the same day.
func TestIOSCISingleProductRenderIsUnchanged(t *testing.T) {
	raw, err := os.ReadFile(iosCIPath(t))
	if err != nil {
		t.Fatal(err)
	}

	// Guard the guard: a token added to the template without a legacy spelling
	// here would sail through unpinned, and this test would silently stop
	// covering it.
	seen := map[string]bool{}
	for _, tok := range iosCITokenRe.FindAllString(string(raw), -1) {
		seen[tok] = true
		if _, ok := legacyIOSCITokens[tok]; !ok {
			t.Fatalf("%s appears in ci.yml with no entry in legacyIOSCITokens — its single-product "+
				"rendering is unpinned, so it could change what twelve repos receive without failing here", tok)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no IOS_CI_* tokens in ci.yml; this test is asserting nothing")
	}
	for tok := range legacyIOSCITokens {
		if !seen[tok] {
			t.Errorf("legacyIOSCITokens pins %s, which no longer appears in ci.yml", tok)
		}
	}

	legacy := string(raw)
	for tok, old := range legacyIOSCITokens {
		legacy = strings.ReplaceAll(legacy, tok, old)
	}
	want, missing := tokens.Substitute(legacy, tokens.Values(soloConfig(), ""))
	if len(missing) > 0 {
		t.Fatalf("the pre-matrix form has unsubstituted tokens: %v", missing)
	}

	got := renderIOSCI(t, soloConfig())
	if got == want {
		return
	}
	t.Errorf("a project declaring no [[product]] no longer renders the pre-matrix ci.yml.\n%s",
		firstDiff(want, got))
}

// firstDiff reports the first differing line, which is far more useful than
// dumping two 1100-line workflows.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return "line " + strconv.Itoa(i+1) + ":\n  pre-matrix: " + wl + "\n  rendered:   " + gl
		}
	}
	return "(lengths differ with no differing line)"
}

// iosCIJobs is the slice of the rendered workflow the matrix tests assert on.
type iosCIJobs struct {
	Jobs map[string]struct {
		Name     string    `yaml:"name"`
		If       string    `yaml:"if"`
		Needs    yaml.Node `yaml:"needs"`
		Strategy struct {
			FailFast *bool `yaml:"fail-fast"`
			Matrix   struct {
				Product []map[string]string `yaml:"product"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Env   map[string]string `yaml:"env"`
		Steps []struct {
			ID   string            `yaml:"id"`
			If   string            `yaml:"if"`
			Name string            `yaml:"name"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func parseIOSCI(t *testing.T, cfg *config.Config) iosCIJobs {
	t.Helper()
	var doc iosCIJobs
	if err := yaml.Unmarshal([]byte(renderIOSCI(t, cfg)), &doc); err != nil {
		t.Fatalf("rendered ios ci.yml is not valid YAML: %v", err)
	}
	return doc
}

// TestIOSCIMatrixCoversEveryProduct is the point of the change: both variants
// get built and both get tested, with the values that actually differ between
// them.
func TestIOSCIMatrixCoversEveryProduct(t *testing.T) {
	doc := parseIOSCI(t, twoIOSProducts())

	build, ok := doc.Jobs["build-release"]
	if !ok {
		t.Fatal("no build-release job")
	}
	if n := len(build.Strategy.Matrix.Product); n != 2 {
		t.Fatalf("build-release has %d matrix leg(s), want 2 — a missing leg means that "+
			"product silently stops being built while CI stays green", n)
	}
	wantSchemes := map[string]string{
		"Paid": "A Bible Verse Each Day",
		"Free": "A Bible Verse Each Day Free",
	}
	for _, leg := range build.Strategy.Matrix.Product {
		if want := wantSchemes[leg["name"]]; leg["scheme"] != want {
			t.Errorf("build leg %q builds scheme %q, want %q", leg["name"], leg["scheme"], want)
		}
	}

	test, ok := doc.Jobs["test"]
	if !ok {
		t.Fatal("no test job")
	}
	if n := len(test.Strategy.Matrix.Product); n != 2 {
		t.Fatalf("test has %d matrix leg(s), want 2", n)
	}
	wantLegs := map[string]map[string]string{
		"Paid": {
			"scheme":         "A Bible Verse Each Day",
			"test_target":    "A Bible Verse Each DayTests",
			"ui_test_target": "A Bible Verse Each DayUITests",
			"app_target":     "A Bible Verse Daily.app",
			"artifact":       "paid",
		},
		"Free": {
			"scheme":      "A Bible Verse Each Day Free",
			"test_target": "A Bible Verse Each Day FreeTests",
			// Blank, and that is a value: this variant has no UI tests, and an
			// empty `-only-testing:` selector matches nothing while still
			// exiting 0.
			"ui_test_target": "",
			// Deliberately NOT the scheme. The real project's schemes and built
			// products have different names, and a derived app target would
			// select no coverage row at all — which reports as 0.0%, not as an
			// error.
			"app_target": "A Bible Verse Daily Free.app",
			"artifact":   "free",
		},
	}
	for _, leg := range test.Strategy.Matrix.Product {
		want, ok := wantLegs[leg["name"]]
		if !ok {
			t.Errorf("unexpected test leg %q", leg["name"])
			continue
		}
		for k, v := range want {
			if leg[k] != v {
				t.Errorf("test leg %q: %s = %q, want %q", leg["name"], k, leg[k], v)
			}
		}
	}

	// The legs must actually be USED. A matrix nothing reads builds the same
	// scheme twice and looks perfectly healthy in the checks list.
	if got := build.Name; !strings.Contains(got, "matrix.product.name") {
		t.Errorf("build-release name is %q — with a matrix the legs need distinguishing", got)
	}
	if got := test.Name; !strings.Contains(got, "matrix.product.name") {
		t.Errorf("test name is %q — with a matrix the legs need distinguishing", got)
	}
	assertUsesMatrix(t, "build-release", build.Env, "PRODUCT_SCHEME", "matrix.product.scheme")
	assertUsesMatrix(t, "test", test.Env, "PRODUCT_SCHEME", "matrix.product.scheme")
	assertUsesMatrix(t, "test", test.Env, "TEST_TARGET", "matrix.product.test_target")
	assertUsesMatrix(t, "test", test.Env, "UI_TEST_TARGET", "matrix.product.ui_test_target")
	assertUsesMatrix(t, "test", test.Env, "APP_TARGET", "matrix.product.app_target")
	assertUsesMatrix(t, "test", test.Env, "PRODUCT_SLUG", "matrix.product.artifact")

	buildScript := stepRun(t, doc, "build-release", "Build Release")
	if !strings.Contains(buildScript, `-scheme "$PRODUCT_SCHEME"`) {
		t.Errorf("the Release build does not read the matrix scheme:\n%s", buildScript)
	}
	testScript := stepRun(t, doc, "test", "Run Tests")
	if !strings.Contains(testScript, `-scheme "$PRODUCT_SCHEME"`) {
		t.Errorf("the test run does not read the matrix scheme:\n%s", testScript)
	}
	if !strings.Contains(testScript, `"-only-testing:$TEST_TARGET"`) {
		t.Errorf("the test run does not read the matrix test target:\n%s", testScript)
	}
	if !strings.Contains(testScript, `${UI_TEST_TARGET:+"-only-testing:$UI_TEST_TARGET"}`) {
		t.Errorf("the UI test target is not conditional — a product without UI tests would get an "+
			"empty -only-testing selector, which matches nothing and exits 0:\n%s", testScript)
	}
	coverage := stepRun(t, doc, "test", "Check Coverage")
	if !strings.Contains(coverage, `--arg app "$APP_TARGET"`) {
		t.Errorf("coverage does not select the matrix app target. A jq program is SINGLE-quoted, so a "+
			"shell variable inside it does not expand and the query silently selects nothing:\n%s", coverage)
	}

	// Two legs writing one artifact name: the second upload fails outright, so
	// the free variant's results are the ones nobody ever gets.
	upload := stepWith(t, doc, "test", "Upload Test Results")
	if !strings.Contains(upload["name"], "matrix.product.artifact") {
		t.Errorf("test results upload to %q — every leg would use one artifact name", upload["name"])
	}
}

func assertUsesMatrix(t *testing.T, job string, env map[string]string, key, want string) {
	t.Helper()
	got, ok := env[key]
	if !ok {
		t.Errorf("%s job declares no %s (env: %v)", job, key, env)
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s job's %s = %q, want it read from %s", job, key, got, want)
	}
}

func stepRun(t *testing.T, doc iosCIJobs, job, step string) string {
	t.Helper()
	for _, st := range doc.Jobs[job].Steps {
		if st.Name == step {
			return st.Run
		}
	}
	t.Fatalf("no %q step in the %s job", step, job)
	return ""
}

func stepWith(t *testing.T, doc iosCIJobs, job, step string) map[string]string {
	t.Helper()
	for _, st := range doc.Jobs[job].Steps {
		if st.Name == step {
			return st.With
		}
	}
	t.Fatalf("no %q step in the %s job", step, job)
	return nil
}

// TestIOSCIMatrixDoesNotFailFast pins the one strategy setting that changes what
// a failure MEANS.
//
// With fail-fast on, a failing paid build cancels the free leg mid-run. The
// cancelled leg reports `cancelled`, which the CI OK aggregator treats exactly
// like a failure — so one real defect in one app presents as both apps being
// broken, and the other app's genuine result was never computed at all.
func TestIOSCIMatrixDoesNotFailFast(t *testing.T) {
	doc := parseIOSCI(t, twoIOSProducts())
	for _, job := range []string{"build-release", "test"} {
		ff := doc.Jobs[job].Strategy.FailFast
		if ff == nil {
			t.Errorf("%s does not set fail-fast; it defaults to TRUE, so one product failing "+
				"cancels the other's run", job)
			continue
		}
		if *ff {
			t.Errorf("%s sets fail-fast: true — one product failing must not cancel the other", job)
		}
	}
}

// TestIOSCIForkGuardSurvivesTheMatrix. Every job on the dedicated self-hosted
// Mac carries a same-repo guard, because a fork PR running there executes
// untrusted code on a machine holding this fleet's signing identity. A matrix
// multiplies the jobs; it must not drop the guard from any of them.
func TestIOSCIForkGuardSurvivesTheMatrix(t *testing.T) {
	const guard = "github.event.pull_request.head.repo.full_name == github.repository"
	for _, tc := range []struct {
		name string
		cfg  *config.Config
	}{
		{"no products", soloConfig()},
		{"two products", twoIOSProducts()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := parseIOSCI(t, tc.cfg)
			for _, job := range []string{"lint", "build-release", "test", "baseline", "docs"} {
				j, ok := doc.Jobs[job]
				if !ok {
					t.Errorf("no %s job", job)
					continue
				}
				if !strings.Contains(j.If, guard) {
					t.Errorf("the %s job's `if` lost the same-repo guard — fork-PR code would run on "+
						"the self-hosted runner.\n  if: %s", job, j.If)
				}
			}
		})
	}
}

// TestIOSCISimulatorsDoNotCollide executes the two shipped lines that decide
// which simulators a leg destroys.
//
// This is the sharp edge of matrixing the test job. The simulator is named for
// the repository and the job DELETES everything matching that name before
// creating its own — correct only while one job per repository runs at a time.
// Two legs would share the name, and the second would delete the simulator the
// first is mid-test on. That surfaces as "the test runner crashed before
// establishing connection", which reads like an application bug and is not one.
//
// The cleanup greps a SUBSTRING, so scoping the name is not sufficient on its
// own: with products named Steps and StepsFree — a real pair — the steps leg's
// pattern occurs inside the stepsfree leg's name. Hence the trailing " (", which
// `simctl list devices` always prints after a device name.
func TestIOSCISimulatorsDoNotCollide(t *testing.T) {
	cfg := soloConfig()
	cfg.Product = []config.Product{
		{Name: "Steps", Scheme: "Steps", BundleID: "com.x.steps", AscAppID: "1", TagPrefix: "steps"},
		{Name: "StepsFree", Scheme: "StepsFree", BundleID: "com.x.free", AscAppID: "2", TagPrefix: "stepsfree"},
	}
	script := stepRun(t, parseIOSCI(t, cfg), "test", "Setup Simulator")

	simLine := mustFind(t, regexp.MustCompile(`(?m)^\s*(SIM_NAME=.*)$`), script, "the SIM_NAME assignment")
	staleLine := mustFind(t, regexp.MustCompile(`(?m)^\s*(STALE_IDS=.*)$`), script, "the stale-simulator cleanup")

	// The other leg's simulator, booted and mid-test.
	const victim = "    CI-iPhone-99887766-stepsfree (AAAAAAAA-1111-2222-3333-444444444444) (Booted)"

	run := func(slug string) string {
		t.Helper()
		// Stub the one command the extracted lines call, so the real shipped
		// pattern runs against a real `simctl` listing.
		prog := "xcrun() { printf '%s\\n' \"$LISTING\"; }\n" + simLine + "\n" + staleLine + "\nprintf '%s' \"$STALE_IDS\"\n"
		cmd := exec.Command("bash", "-c", prog)
		cmd.Env = append(os.Environ(),
			"GITHUB_RUN_ID=99887766", "PRODUCT_SLUG="+slug, "LISTING="+victim)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("extracted simulator shell failed: %v\n%s", err, out)
		}
		return string(out)
	}

	if got := run("steps"); got != "" {
		t.Errorf("the `steps` leg would delete the `stepsfree` leg's live simulator (%q). "+
			"The cleanup greps a substring, so scoping the NAME per product is not enough — the "+
			"match has to be anchored past the end of the name.", got)
	}
	if got := run("stepsfree"); !strings.Contains(got, "AAAAAAAA-1111-2222-3333-444444444444") {
		t.Errorf("the `stepsfree` leg no longer finds its OWN stale simulator (got %q). "+
			"Leaving strays behind is what wedges the next run.", got)
	}
}

func mustFind(t *testing.T, re *regexp.Regexp, hay, what string) string {
	t.Helper()
	m := re.FindStringSubmatch(hay)
	if m == nil {
		t.Fatalf("could not find %s in the Setup Simulator step", what)
	}
	return strings.TrimSpace(m[1])
}

// TestCIOKAggregatesMatrixJobs. `ci-ok` is the single required status check
// every repo's branch protection points at. Expanding a job into N legs must not
// remove it from that aggregation — GitHub reports one result per JOB (a failure
// in any leg makes the job `failure`), so the aggregator keeps working only for
// as long as it keeps naming these jobs.
func TestCIOKAggregatesMatrixJobs(t *testing.T) {
	doc := parseIOSCI(t, twoIOSProducts())
	ciOK, ok := doc.Jobs["ci-ok"]
	if !ok {
		t.Fatal("no ci-ok job")
	}
	needs := needsList(t, ciOK.Needs)
	for _, job := range []string{"build-release", "test"} {
		if !contains(needs, job) {
			t.Fatalf("ci-ok needs %v — %s must stay aggregated, or a failing product leg would "+
				"leave the required check green", needs, job)
		}
	}
	if len(ciOK.Steps) == 0 {
		t.Fatal("ci-ok has no steps")
	}
	script := ciOK.Steps[0].Run

	green := map[string]string{}
	for _, n := range needs {
		green[n] = "success"
	}
	for _, tc := range []struct {
		name     string
		results  map[string]string
		wantFail bool
	}{
		{"every job green", green, false},
		{"a product's test leg failed", withResult(green, "test", "failure"), true},
		{"a product's build leg failed", withResult(green, "build-release", "failure"), true},
		{"a leg was cancelled", withResult(green, "test", "cancelled"), true},
		{"the heavy jobs skipped (a docs-only PR)", withResult(withResult(green, "test", "skipped"), "build-release", "skipped"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, failed := runAggregator(t, script, tc.results)
			if failed != tc.wantFail {
				t.Errorf("ci-ok failed=%v, want %v\nresults: %v\noutput:\n%s",
					failed, tc.wantFail, tc.results, out)
			}
		})
	}
}

// Two runs of the SAME repository must not share a simulator name.
//
// Scoping per repository was not enough, and the gap was not theoretical: a push
// landing while a pull request is still testing, or two PRs updated together,
// both build a device of the same name and delete each other's mid-test. One
// project hit exactly that, fixed it locally with a per-run name, and this is
// that fix promoted.
func TestIOSCISimulatorNameIsUniquePerRun(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.s", AscAppID: "1",
		Xcodeproj: "Solo.xcodeproj", SwiftVersion: "6.0",
	}}
	script := stepRun(t, parseIOSCI(t, cfg), "test", "Setup Simulator")
	simLine := mustFind(t, regexp.MustCompile(`(?m)^\s*(SIM_NAME=.*)$`), script, "the SIM_NAME assignment")

	if !strings.Contains(simLine, "GITHUB_RUN_ID") {
		t.Errorf("SIM_NAME is not keyed on the run: %q", simLine)
	}
	// GITHUB_REPOSITORY_ID is stable across runs of one repo, so keying on it
	// lets two concurrent runs of that repo collide — the exact bug this fixes.
	if strings.Contains(simLine, "GITHUB_REPOSITORY_ID") {
		t.Errorf("SIM_NAME still keyed on the repository, which is shared across concurrent runs: %q", simLine)
	}

	// Per-run names give up the implicit cleanup the old fixed name had, where
	// the NEXT run reclaimed the previous device by recreating it. Without a
	// teardown every run leaks a simulator until the nightly cleanup.
	steps := parseIOSCI(t, cfg).Jobs["test"].Steps
	var teardown, idx = -1, -1
	for i, st := range steps {
		if strings.Contains(st.Run, "simctl delete") && strings.Contains(st.Run, "GITHUB_RUN_ID") {
			teardown = i
		}
		if strings.Contains(st.Name, "Collect Simulator Diagnostics") {
			idx = i
		}
	}
	if teardown < 0 {
		t.Fatal("no teardown deletes this run's simulators; per-run names leak without one")
	}
	if !strings.Contains(steps[teardown].If, "always()") {
		t.Errorf("teardown is not always(): cancelled and failed runs are exactly the ones that leak (if=%q)", steps[teardown].If)
	}
	// Ordering is load-bearing: deleting before diagnostics destroys the device
	// that step exists to inspect.
	if idx >= 0 && teardown < idx {
		t.Errorf("teardown at step %d runs BEFORE Collect Simulator Diagnostics at %d", teardown, idx)
	}
}
