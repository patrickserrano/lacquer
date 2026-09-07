package shipped

import (
	"errors"
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
	// Both empty unless a product declares extra_test_targets. A project that
	// declares none gets neither the array setup nor the verification step —
	// which is what makes the extra selectors an opt-in rather than an edit to
	// twelve workflows.
	"{{IOS_CI_EXTRA_TEST_SETUP}}": "",
	"{{IOS_CI_VERIFY_SELECTORS}}": "",
	"{{IOS_CI_SCHEME}}":           "{{SCHEME}}",
	"{{IOS_CI_ONLY_TESTING}}":     `"-only-testing:{{PROJECT_NAME}}Tests"`,
	"{{IOS_CI_APP_TARGET}}":       "{{PROJECT_NAME}}.app",
	"{{IOS_CI_COVERAGE_JQ}}":      `'.targets[] | select(.name == "{{PROJECT_NAME}}.app") | .lineCoverage * 100'`,
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

// withExtras is a single-product project whose repository also holds two local
// Swift packages with their own maintained suites.
//
// The shape this feature exists for: `-only-testing:` is a whitelist, so a
// package test target the app's selector does not name is run by NOTHING, and
// xcodebuild reports that as a pass. One of these deliberately contains a space,
// because Xcode target names do and a joined-and-word-split argument list would
// turn it into two selectors that each match nothing.
func withExtras() *config.Config {
	cfg := soloConfig()
	cfg.Product = []config.Product{{
		Name: "Demo", Scheme: "Demo", BundleID: "com.x.demo", AscAppID: "1",
		ExtraTestTargets: []string{"CoreKitTests", "Feature KitTests"},
	}}
	return cfg
}

// TestIOSCIRunsEveryDeclaredTestTarget: a declared extra target that does not
// reach the command line is the whole defect, restated. It looks like a config
// that took effect and is a suite that still never runs.
func TestIOSCIRunsEveryDeclaredTestTarget(t *testing.T) {
	script := stepRun(t, parseIOSCI(t, withExtras()), "test", "Run Tests")
	for _, want := range []string{
		`"-only-testing:DemoTests"`,
		`"-only-testing:CoreKitTests"`,
		`"-only-testing:Feature KitTests"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the test run does not pass %s, so that suite does not execute and the job "+
				"still goes green:\n%s", want, script)
		}
	}
	// Quoted as ONE argument. Unquoted, "Feature KitTests" splits into
	// `-only-testing:Feature` and `KitTests` — the first matches nothing (exit 0)
	// and the second is read as a build setting.
	if strings.Contains(script, `-only-testing:Feature KitTests `) &&
		!strings.Contains(script, `"-only-testing:Feature KitTests"`) {
		t.Errorf("a target name containing a space is not quoted as a single argument:\n%s", script)
	}
}

// TestIOSCIMatrixCarriesPerProductExtras. The extras are per-product for the
// same reason test_target is: a package linked into the paid scheme and not the
// free one must not be selected on the leg that cannot build it, where it would
// match nothing and pass.
func TestIOSCIMatrixCarriesPerProductExtras(t *testing.T) {
	cfg := twoIOSProducts()
	cfg.Product[0].ExtraTestTargets = []string{"CoreKitTests", "Paid FeatureTests"}
	doc := parseIOSCI(t, cfg)
	test := doc.Jobs["test"]

	wantExtras := map[string]string{
		"Paid": "CoreKitTests\nPaid FeatureTests",
		// Declared nothing, and that is a value: the key must be present on
		// every leg or the expression is undefined where it is missing.
		"Free": "",
	}
	for _, leg := range test.Strategy.Matrix.Product {
		got, ok := leg["extra_test_targets"]
		if !ok {
			t.Errorf("test leg %q has no extra_test_targets key; a key on one leg and not another "+
				"makes matrix.product.extra_test_targets undefined there", leg["name"])
			continue
		}
		if want := wantExtras[leg["name"]]; got != want {
			t.Errorf("test leg %q: extra_test_targets = %q, want %q", leg["name"], got, want)
		}
	}
	assertUsesMatrix(t, "test", test.Env, "EXTRA_TEST_TARGETS", "matrix.product.extra_test_targets")

	script := stepRun(t, doc, "test", "Run Tests")
	// An ARRAY, expanded quoted. A joined string would word-split "Paid
	// FeatureTests" into two selectors that each match nothing and exit 0 —
	// which is the failure this whole feature has to avoid manufacturing.
	if !strings.Contains(script, `EXTRA_ONLY_TESTING+=("-only-testing:$extra_target")`) {
		t.Errorf("the leg's extra selectors are not built one argument at a time:\n%s", script)
	}
	if !strings.Contains(script, `"${EXTRA_ONLY_TESTING[@]}"`) {
		t.Errorf("the extra selectors are never passed to xcodebuild:\n%s", script)
	}
}

// TestIOSCIExtraTestTargetsAreOptIn is the companion to
// TestIOSCISingleProductRenderIsUnchanged, for the projects that DO declare
// products. Neither the array setup nor the verification step may appear until
// somebody asks for extras, or this is an edit to every workflow in the fleet.
func TestIOSCIExtraTestTargetsAreOptIn(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *config.Config
	}{
		{"no products", soloConfig()},
		{"two products, no extras", twoIOSProducts()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderIOSCI(t, tc.cfg)
			for _, unwanted := range []string{
				"EXTRA_TEST_TARGETS", "EXTRA_ONLY_TESTING", "extra_test_targets",
				"Verify Test Selectors Matched",
			} {
				if strings.Contains(out, unwanted) {
					t.Errorf("a project declaring no extra_test_targets renders %q — every repo "+
						"with this workflow would see a diff it did not ask for", unwanted)
				}
			}
		})
	}
	// And the argument list itself is the pre-change text, character for
	// character. Asserting on the absence of new strings alone would pass if the
	// old spelling had merely been rewritten.
	got := stepRun(t, parseIOSCI(t, twoIOSProducts()), "test", "Run Tests")
	const legacy = `"-only-testing:$TEST_TARGET" ${UI_TEST_TARGET:+"-only-testing:$UI_TEST_TARGET"} \`
	if !strings.Contains(got, legacy) {
		t.Errorf("the matrix -only-testing arguments are no longer the pre-change text %q", legacy)
	}
}

// TestIOSCIVerifiesEverySelectorMatched runs the SHIPPED guard.
//
// This is the defence the extra selectors are not safe without, so it is
// executed rather than pattern-matched: `xcodebuild` exits 0 for an
// `-only-testing:` selector that matches no tests, which makes every additional
// selector an additional way to convert a maintained suite into a silent no-op.
//
// Both directions are asserted. A one-directional test here would be unsound in
// the usual way: if `jq` were missing, or the extraction broke, the executed-
// bundle list would come back empty, EVERY selector would be reported missing,
// and a test that only checked "the bad case fails" would pass for exactly the
// wrong reason.
func TestIOSCIVerifiesEverySelectorMatched(t *testing.T) {
	// A result bundle in Apple's `get test-results tests` shape.
	bundle := func(names ...string) string {
		var b strings.Builder
		b.WriteString(`{"testNodes":[{"nodeType":"Test Plan","name":"Demo","children":[`)
		for i, n := range names {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"nodeType":"Unit test bundle","name":"` + n + `","children":[` +
				`{"nodeType":"Test Case","name":"aTest","result":"Passed"}]}`)
		}
		b.WriteString(`]}]}`)
		return b.String()
	}

	// The two renderings, run identically. The matrix form reads its selectors
	// from the job env the strategy block hoists, so both are exercised here —
	// without the matrix case, dropping the extras from the checked list in that
	// form alone would go unnoticed, which is the same silent-pass shape one
	// level up.
	twoWithExtras := twoIOSProducts()
	twoWithExtras.Product[0].ExtraTestTargets = []string{"CoreKitTests", "Feature KitTests"}
	for _, mode := range []struct {
		name string
		cfg  *config.Config
		env  []string
		// selectors is every name the guard must require, and the fixtures below
		// are built from it — so a selector the shipped step stops checking makes
		// its own case fail.
		selectors []string
	}{
		{
			name: "single product", cfg: withExtras(),
			selectors: []string{"DemoTests", "CoreKitTests", "Feature KitTests"},
		},
		{
			name: "matrix leg", cfg: twoWithExtras,
			env: []string{
				"TEST_TARGET=A Bible Verse Each DayTests",
				"UI_TEST_TARGET=A Bible Verse Each DayUITests",
				"EXTRA_TEST_TARGETS=CoreKitTests\nFeature KitTests",
			},
			selectors: []string{
				"A Bible Verse Each DayTests", "A Bible Verse Each DayUITests",
				"CoreKitTests", "Feature KitTests",
			},
		},
	} {
		t.Run(mode.name, func(t *testing.T) {
			script := stepRun(t, parseIOSCI(t, mode.cfg), "test", "Verify Test Selectors Matched")

			run := func(t *testing.T, results string) (string, int) {
				t.Helper()
				// Stub the one command that needs a Mac; everything the guard
				// decides with — jq, the loop, the comparison — is shipped code.
				prog := "xcrun() { printf '%s' \"$RESULTS\"; }\n" + script
				cmd := exec.Command("bash", "-c", prog)
				cmd.Env = append(append(os.Environ(), mode.env...), "RESULTS="+results)
				out, err := cmd.CombinedOutput()
				code := 0
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					code = ee.ExitCode()
				} else if err != nil {
					t.Fatalf("could not run the extracted guard: %v\n%s", err, out)
				}
				return string(out), code
			}

			t.Run("every selector ran", func(t *testing.T) {
				out, code := run(t, bundle(mode.selectors...))
				if code != 0 {
					t.Fatalf("the guard failed a run in which every selector DID match (exit %d). A "+
						"guard that cannot pass gets deleted — and it would also mean the failing "+
						"cases below prove nothing, since they would fail for this reason instead.\n%s",
						code, out)
				}
			})

			// Each selector removed in turn. This is what makes the guard's
			// COVERAGE testable rather than just its verdict: a selector the step
			// quietly stopped checking fails its own case here.
			for i, missing := range mode.selectors {
				t.Run("nothing ran for "+missing, func(t *testing.T) {
					ran := append(append([]string{}, mode.selectors[:i]...), mode.selectors[i+1:]...)
					out, code := run(t, bundle(ran...))
					if code == 0 {
						t.Fatalf("the guard passed a run where -only-testing:%s matched nothing. "+
							"xcodebuild exits 0 on that, so this is a green check over a suite "+
							"that did not execute.\n%s", missing, out)
					}
					if !strings.Contains(out, missing) {
						t.Errorf("the guard failed without naming %q as the selector that matched "+
							"nothing:\n%s", missing, out)
					}
				})
			}

			t.Run("a target name with a space is compared whole", func(t *testing.T) {
				// "Feature KitTests" absent, "Feature" and "KitTests" present. A
				// comparison that word-split the selector would find both halves
				// and report success over a suite that never ran.
				var ran []string
				for _, s := range mode.selectors {
					if s == "Feature KitTests" {
						ran = append(ran, "Feature", "KitTests")
						continue
					}
					ran = append(ran, s)
				}
				out, code := run(t, bundle(ran...))
				if code == 0 {
					t.Fatalf("the guard accepted the two HALVES of a target name as the target:\n%s", out)
				}
			})

			t.Run("a longer bundle name does not stand in for the selector", func(t *testing.T) {
				// The shape a rename leaves behind: CoreKitTests became
				// CoreKitTestsSupport, the manifest still names the old one, and a
				// substring comparison would find the old name inside the new one
				// and report the old suite as having run.
				var ran []string
				for _, s := range mode.selectors {
					if s == "CoreKitTests" {
						s = "CoreKitTestsSupport"
					}
					ran = append(ran, s)
				}
				out, code := run(t, bundle(ran...))
				if code == 0 {
					t.Fatalf("the guard accepted CoreKitTestsSupport as CoreKitTests; the comparison "+
						"has to be whole-line, or a renamed target keeps reporting as run:\n%s", out)
				}
			})

			t.Run("an unreadable result bundle is not a pass", func(t *testing.T) {
				// Fail closed. Tool missing, schema changed, bundle corrupt — every
				// path that cannot reach a verdict has to be red, because "I could
				// not tell" reported as green is the original defect in this
				// workflow's own history.
				out, code := run(t, "not json at all")
				if code == 0 {
					t.Fatalf("an unparseable result bundle passed the guard:\n%s", out)
				}
			})
		})
	}
}

// TestIOSCITestJobShellParses puts every rendered `run:` block in the test job
// through `bash -n`.
//
// The selector work adds SHELL to a template — an array built in a loop, a
// heredoc whose body is generated — and a shell syntax error there does not
// surface until a self-hosted runner reaches the step, minutes into a job, in
// whichever repository synced first. `bash -n` costs nothing and answers it
// here. It runs against all three shapes because the generated shell differs in
// each: absent, literal, and hoisted-from-env.
func TestIOSCITestJobShellParses(t *testing.T) {
	twoWithExtras := twoIOSProducts()
	twoWithExtras.Product[0].ExtraTestTargets = []string{"CoreKitTests", "Feature KitTests"}
	for _, tc := range []struct {
		name string
		cfg  *config.Config
	}{
		{"no extras", soloConfig()},
		{"single product with extras", withExtras()},
		{"matrix with extras", twoWithExtras},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := parseIOSCI(t, tc.cfg).Jobs["test"].Steps
			var checked int
			for _, st := range steps {
				if st.Run == "" {
					continue
				}
				checked++
				f, err := os.CreateTemp(t.TempDir(), "step-*.sh")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteString(st.Run); err != nil {
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command("bash", "-n", f.Name()).CombinedOutput()
				// Output as well as exit status. `bash -n` exits 0 on a heredoc
				// delimited by end-of-file and only WARNS — and that is the worst
				// case here, not a benign one: a misspelled terminator makes the
				// heredoc swallow the rest of the step, including the `exit 1`
				// that fails the job. A check that reads only the exit status
				// would call that valid.
				if err != nil || len(out) > 0 {
					t.Errorf("the %q step is not clean shell: %v\n%s\n--- script ---\n%s",
						st.Name, err, out, st.Run)
				}
			}
			if checked == 0 {
				t.Fatal("no run: blocks in the test job; this test is asserting nothing")
			}
		})
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
			for _, job := range []string{"lint", "build-release", "test", "baseline"} {
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
			// "pass" for the folded-in drift audit: this test is about the
			// matrix legs, and a drift verdict of its own would confound it.
			out, failed := runAggregator(t, script, tc.results, "pass")
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

// A widget or app-extension target runs on the same iOS simulator as the host
// app; only an actual watchOS companion app needs its own simulator platform,
// which is not preinstalled on a fresh runner. This must stay opt-in: a
// project with no watch target must not pay for a platform download it never
// asked for, and must render the same workflow it already had.
func TestWatchSimulatorSetupIsOptIn(t *testing.T) {
	rendered := renderIOSCI(t, soloConfig())
	if strings.Contains(rendered, "Install watchOS Simulator Runtime") {
		t.Error("a project with no watch_target must not get the watchOS simulator step")
	}
}

func TestWatchSimulatorSetupRendersForWatchTarget(t *testing.T) {
	cfg := soloConfig()
	cfg.Project.WatchTarget = true
	doc := parseIOSCI(t, cfg)
	found := map[string]bool{}
	for job, jobName := range map[string]string{"test": "Test", "build-release": "Build (Release)"} {
		for _, st := range doc.Jobs[job].Steps {
			if strings.Contains(st.Name, "Install watchOS Simulator Runtime") {
				found[jobName] = true
			}
		}
	}
	if !found["Test"] {
		t.Error("watch_target = true must add the watchOS simulator step to Test")
	}
	if !found["Build (Release)"] {
		t.Error("watch_target = true must add the watchOS simulator step to Build (Release)")
	}
}

// A component declared per the archetype rule before any code exists (e.g. a
// Phase-0 backend-first project: xcodeproj configured, no Swift on disk yet)
// has nothing for Lint's SwiftLint/SwiftFormat steps to check. Without a gate,
// `swiftlint` itself exits 1 with "No lintable files found" for a path with
// zero Swift files anywhere under it, so Lint always failed outright for a
// pre-code component -- distinct from (and in addition to) the mis-scoped-config
// case the job already guards, where Swift exists elsewhere but none matched.
func TestLintSkipsWhenNoSwiftSources(t *testing.T) {
	doc := parseIOSCI(t, soloConfig())
	lint := doc.Jobs["lint"]

	var detectID string
	for _, st := range lint.Steps {
		if st.Name == "Detect Swift sources" {
			detectID = st.ID
		}
	}
	if detectID == "" {
		t.Fatal(`Lint job has no "Detect Swift sources" step`)
	}

	wantIf := "steps." + detectID + ".outputs.present == 'true'"
	for _, name := range []string{"Run SwiftLint", "Check SwiftFormat"} {
		found := false
		for _, st := range lint.Steps {
			if st.Name != name {
				continue
			}
			found = true
			if st.If != wantIf {
				t.Errorf("%q step has if=%q, want %q -- it must not run for a pre-code component", name, st.If, wantIf)
			}
		}
		if !found {
			t.Errorf("Lint job has no %q step", name)
		}
	}
}

// The Baseline job's `xcodebuild -showBuildSettings` call fails outright when
// {{XCODEPROJ}} doesn't exist on disk -- expected for a component declared
// before its xcodeproj is committed (`lacquer audit`'s own baseline check
// already tolerates this as Unchecked when the component has no Swift sources
// either). The job must skip its baseline assertion in that same case rather
// than fail with an xcodebuild error that points at the wrong cause.
func TestBaselineSkipsWhenXcodeprojMissing(t *testing.T) {
	doc := parseIOSCI(t, soloConfig())
	baseline := doc.Jobs["baseline"]

	var detectID string
	for _, st := range baseline.Steps {
		if st.Name == "Detect Xcode project" {
			detectID = st.ID
		}
	}
	if detectID == "" {
		t.Fatal(`Baseline job has no "Detect Xcode project" step`)
	}

	wantIf := "steps." + detectID + ".outputs.present == 'true'"
	for _, name := range []string{"Read the baseline relaxations", "Assert the project baseline"} {
		found := false
		for _, st := range baseline.Steps {
			if st.Name != name {
				continue
			}
			found = true
			if st.If != wantIf {
				t.Errorf("%q step has if=%q, want %q -- it must not run when {{XCODEPROJ}} doesn't exist", name, st.If, wantIf)
			}
		}
		if !found {
			t.Errorf("Baseline job has no %q step", name)
		}
	}
}

// TestXcodegenGenerateCdSurvivesRootLayout guards a real regression: the
// "Generate Xcode project" step's `cd {{COMPONENT_PREFIX}}` broke on a
// root-layout project (`[[component]] path = "."`), where COMPONENT_PREFIX
// substitutes to the empty string -- not "." -- so the rendered line was a
// bare `cd`, which bash resolves to $HOME rather than staying in the
// checkout. Discovered live on OutOfTheMouths: `xcodegen generate` then
// failed with "No project spec found at /Users/<runner-user>/project.yml".
// A subdirectory component (COMPONENT_PREFIX = "ios/App/") never exercised
// this, which is exactly how it shipped unnoticed.
func TestXcodegenGenerateCdSurvivesRootLayout(t *testing.T) {
	raw := renderIOSCI(t, soloConfig())
	if strings.Contains(raw, "cd  &&") || strings.Contains(raw, "cd &&") {
		t.Fatal("rendered ios-ci.yml contains a bare `cd` with no argument -- " +
			"bash resolves that to $HOME instead of the checkout root")
	}
	if !strings.Contains(raw, "(cd . && xcodegen generate)") {
		t.Fatal(`root-layout project must render "(cd . && xcodegen generate)"; ` +
			"got something else for the xcodegen generate step")
	}
}

// Test and Build (Release) both run `xcodebuild` directly against {{XCODEPROJ}}
// with no equivalent to Lint/Baseline's pre-code exemption (TestLintSkipsWhenNoSwiftSources,
// TestBaselineSkipsWhenXcodeprojMissing) -- discovered live on multimeter (a
// Phase-0, zero-Swift component): Test's `xcodebuild test` failed outright with
// exit code 66 rather than skipping. Unlike Lint/Baseline, gating every one of
// Test's ~14 downstream steps individually would be a lot of churn, so this is
// a job-level skip instead, driven by a cheap check in the `changes` job (which
// already does a full checkout) rather than per-job step detection.
func TestChangesJobDetectsXcodeproj(t *testing.T) {
	raw := renderIOSCI(t, soloConfig())
	if !strings.Contains(raw, "xcodeproj: ${{ steps.filter.outputs.xcodeproj }}") {
		t.Fatal(`the "changes" job must expose an "xcodeproj" output`)
	}
	if !strings.Contains(raw, `echo "xcodeproj=$xcodeproj_present" >> "$GITHUB_OUTPUT"`) {
		t.Fatal(`the "changes" job's filter step must compute and emit xcodeproj_present`)
	}
}

func TestTestAndBuildReleaseSkipWithoutXcodeproj(t *testing.T) {
	doc := parseIOSCI(t, soloConfig())
	for _, job := range []string{"test", "build-release"} {
		cond := doc.Jobs[job].If
		if !strings.Contains(cond, "needs.changes.outputs.xcodeproj == 'true'") {
			t.Errorf("%q job's if=%q does not gate on needs.changes.outputs.xcodeproj -- "+
				"it will run xcodebuild against a {{XCODEPROJ}} that may not exist yet", job, cond)
		}
	}
}
