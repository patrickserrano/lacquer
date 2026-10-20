package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// The base compare (#571) through the real CLI, in a depth-1 checkout of a PR
// merge commit. The rendered step's own tests are in internal/shipped; these
// cover each verdict and each pass path by name.

const (
	baseManifest = "[project]\nname = \"demo\"\nproject_name = \"Demo\"\nscheme = \"Demo\"\nxcodeproj = \"Demo.xcodeproj\"\n\n" +
		"[[component]]\npath = \".\"\nprofiles = [\"ios\"]\n"
	locals = "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\n"
)

// basePR checks out a PR whose base holds baseRatchet ("" for no file) and
// whose PR writes headRatchet ("" deletes the file), and returns the env for
// run. Reports use 1,000 executable lines: slack 20, and 100 uncovered is 90%.
func basePR(t *testing.T, baseRatchet, headRatchet string) (dir string, env func(string) string) {
	t.Helper()
	files := map[string]string{".lacquer.toml": baseManifest}
	if baseRatchet != "" {
		files[".lacquer.ratchet.toml"] = baseRatchet
	}
	pr := gittest.NewPR(t, files, func(work string) {
		if headRatchet == "" && baseRatchet == "" {
			return
		}
		gittest.Write(t, work, map[string]string{".lacquer.ratchet.toml": headRatchet})
	})
	lq := t.TempDir()
	gittest.Write(t, lq, map[string]string{"profiles/ios/baseline.toml": "[baseline]\ncoverage_floor = 80\n"})
	chdir(t, pr.Checkout)
	return pr.Checkout, envMap(map[string]string{"LACQUER_ROOT": lq})
}

// baseGate runs the gate on a report of `uncovered` of 1,000 lines. The report
// lives outside the checkout, so it is not an untracked file in the PR.
func baseGate(t *testing.T, env func(string) string, event string, uncovered int, extra ...string) (int, string) {
	t.Helper()
	report := writeReport(t, t.TempDir(), covReport("Demo.app", 1000, 1000-uncovered))
	return gate(t, env, report, event, append([]string{"--compare-base"}, extra...)...)
}

func TestBaseCompareFirstEnrollmentPasses(t *testing.T) {
	_, env := basePR(t, "", locals+"ios_uncovered_lines = 100\n")
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 0 || strings.Contains(out, "::error::") {
		t.Fatalf("exit %d; a first enrollment must pass:\n%s", code, out)
	}
}

func TestBaseCompareTighteningPassesSilently(t *testing.T) {
	_, env := basePR(t, locals+"ios_uncovered_lines = 100\n", locals+"ios_uncovered_lines = 90\n")
	code, out := baseGate(t, env, "pull_request", 90)
	if code != 0 || strings.Contains(out, "PR's base") || strings.Contains(out, "loosened") {
		t.Fatalf("exit %d; tightening must pass and say nothing about the base:\n%s", code, out)
	}
}

func TestBaseCompareFailsADeletedRatchetFile(t *testing.T) {
	_, env := basePR(t, locals+"ios_uncovered_lines = 100\n", "")
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 4 || !strings.Contains(out, "::error::ratchet: ios_uncovered_lines is enrolled on the PR's base (100) and this PR deletes .lacquer.ratchet.toml") {
		t.Fatalf("exit %d; want 4 naming the key:\n%s", code, out)
	}
}

// The compare covers every key, not only the product this run measures.
func TestBaseCompareFailsAnotherProductsRemovedKey(t *testing.T) {
	_, env := basePR(t, locals+"ios_uncovered_lines = 100\nios_uncovered_lines_widget = 7\n", locals+"ios_uncovered_lines = 100\n")
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 4 || !strings.Contains(out, "ios_uncovered_lines_widget is enrolled on the PR's base (7) and this PR removes it") {
		t.Fatalf("exit %d; want 4 naming the other product's key:\n%s", code, out)
	}
}

func TestBaseCompareFailsAHandRaiseWithNoReason(t *testing.T) {
	_, env := basePR(t, locals+"ios_uncovered_lines = 100\n", locals+"ios_uncovered_lines = 150\n")
	code, out := baseGate(t, env, "pull_request", 150)
	if code != 4 || !strings.Contains(out, "ios_uncovered_lines is raised 100 → 150 on this PR without a new reason") ||
		!strings.Contains(out, "lacquer ratchet --loosen ios_uncovered_lines --to 150 --reason") {
		t.Fatalf("exit %d; want 4 naming the raise and the command:\n%s", code, out)
	}
}

func TestBaseCompareFailsARaiseWithTheBasesUnchangedReason(t *testing.T) {
	reasons := "[reasons]\nios_uncovered_lines = \"legacy module\"\n"
	_, env := basePR(t, locals+"ios_uncovered_lines = 100\n"+reasons, locals+"ios_uncovered_lines = 150\n"+reasons)
	code, out := baseGate(t, env, "pull_request", 150)
	if code != 4 || !strings.Contains(out, "raised 100 → 150 on this PR without a new reason") {
		t.Fatalf("exit %d; an old reason must not cover a new raise:\n%s", code, out)
	}
}

func TestBaseComparePassesARaiseWithANewReasonAndReportsIt(t *testing.T) {
	base := locals + "ios_uncovered_lines = 100\n[reasons]\nios_uncovered_lines = \"legacy module\"\n"
	head := locals + "ios_uncovered_lines = 150\n[reasons]\nios_uncovered_lines = \"vendored parser\"\n"
	_, env := basePR(t, base, head)
	summary := filepath.Join(t.TempDir(), "summary.md")
	code, out := baseGate(t, env, "pull_request", 150, "--summary", summary)
	if code != 0 {
		t.Fatalf("exit %d; a raise with a new reason passes:\n%s", code, out)
	}
	for _, want := range []string{
		"ratchet: loosened ios_uncovered_lines 100→150: vendored parser",
		`::notice title=lacquer ratchet loosened::lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines","from":100,"to":150,"reason":"vendored parser"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(summary)
	if err != nil || !strings.Contains(string(data), "- loosened ios_uncovered_lines 100→150: vendored parser") {
		t.Errorf("the job summary lacks the loosening (%v):\n%s", err, data)
	}
}

func TestBaseCompareFailsAnUnparseableBase(t *testing.T) {
	_, env := basePR(t, "[ratchet\n", locals+"ios_uncovered_lines = 100\n")
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 4 || !strings.Contains(out, "the PR's base could not be compared") || !strings.Contains(out, "does not parse") {
		t.Fatalf("exit %d; want 4 naming the parse failure:\n%s", code, out)
	}
}

// No base ref: off a pull_request the flag changes nothing, even in a checkout
// that deleted the file (today's behaviour: not enrolled, warns, passes), and
// even outside a git work tree.
func TestBaseCompareDoesNothingOffAPullRequest(t *testing.T) {
	for _, event := range []string{"push", "workflow_dispatch", "schedule"} {
		t.Run(event, func(t *testing.T) {
			_, env := basePR(t, locals+"ios_uncovered_lines = 100\n", "")
			code, out := baseGate(t, env, event, 100)
			if code != 0 || strings.Contains(out, "PR's base") || !strings.Contains(out, "coverage gate not enrolled") {
				t.Fatalf("exit %d; want today's not-enrolled pass:\n%s", code, out)
			}
		})
	}
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	if code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-4312)), "push", "--compare-base"); code != 0 {
		t.Fatalf("exit %d outside a git work tree on push:\n%s", code, out)
	}
}

func TestCompareBaseIsRejectedWithWrite(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	var out strings.Builder
	report := writeReport(t, dir, covReport("Demo.app", 8000, 8000-4312))
	if code := run([]string{"ratchet", "--write", "--coverage-report", report, "--product", "-", "--compare-base"}, env, &out, &out); code != 2 {
		t.Fatalf("exit %d; --compare-base is a gate flag, not a write flag:\n%s", code, out.String())
	}
}

// Removing a coverage key (#571, round 2): allowed only together with its
// product AND with a new reason; reported as a loosening to "removed".

func productManifest(names ...string) string {
	m := "[project]\nname = \"demo\"\nproject_name = \"Demo\"\nxcodeproj = \"Demo.xcodeproj\"\n\n[[component]]\npath = \".\"\nprofiles = [\"ios\"]\n"
	for i, n := range names {
		m += fmt.Sprintf("\n[[product]]\nname = %q\nscheme = %q\nbundle_id = \"com.x.%s\"\nasc_app_id = \"%d\"\ntag_prefix = %q\n", n, n, strings.ToLower(n), i+1, strings.ToLower(n))
	}
	return m
}

const threeKeys = locals + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_widget = 7\nios_uncovered_lines_extra = 50\n"

// removalPR is a three-product base; the PR writes headManifest and drops the
// widget key, with headReasons appended to the ratchet file.
func removalPR(t *testing.T, headManifest, headReasons string) func(string) string {
	t.Helper()
	base := map[string]string{".lacquer.toml": productManifest("Demo", "Widget", "Extra"), ".lacquer.ratchet.toml": threeKeys}
	pr := gittest.NewPR(t, base, func(work string) {
		gittest.Write(t, work, map[string]string{
			".lacquer.toml":         headManifest,
			".lacquer.ratchet.toml": locals + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_extra = 50\n" + headReasons,
		})
	})
	lq := t.TempDir()
	gittest.Write(t, lq, map[string]string{"profiles/ios/baseline.toml": "[baseline]\ncoverage_floor = 80\n"})
	chdir(t, pr.Checkout)
	return envMap(map[string]string{"LACQUER_ROOT": lq})
}

func TestBaseComparePassesAKeyRemovedWithItsProductAndANewReason(t *testing.T) {
	env := removalPR(t, productManifest("Demo", "Extra"), "[reasons]\nios_uncovered_lines_widget = \"widget app retired\"\n")
	code, out := baseGate(t, env, "pull_request", 100, "--product", "demo")
	if code != 0 {
		t.Fatalf("exit %d; a key removed with its product and a new reason passes:\n%s", code, out)
	}
	for _, want := range []string{
		"ratchet: loosened ios_uncovered_lines_widget 7→removed: widget app retired",
		`lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines_widget","from":7,"removed":true,"reason":"widget app retired"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestBaseCompareFailsAKeyRemovedWhileItsProductStillExists(t *testing.T) {
	env := removalPR(t, productManifest("Demo", "Widget", "Extra"), "[reasons]\nios_uncovered_lines_widget = \"widget app retired\"\n")
	code, out := baseGate(t, env, "pull_request", 100, "--product", "demo")
	if code != 4 || !strings.Contains(out, "ios_uncovered_lines_widget is enrolled on the PR's base (7) and this PR removes it") ||
		!strings.Contains(out, "its product is still in .lacquer.toml") {
		t.Fatalf("exit %d; removing a live product's key is a disguised loosening:\n%s", code, out)
	}
}

func TestBaseCompareFailsAKeyRemovedWithItsProductButNoNewReason(t *testing.T) {
	for name, reasons := range map[string]string{
		"no reason": "",
		// A reason for another key does not cover this one. The reused-base-
		// reason case is in internal/ratchet's TestCompareBase.
		"a reason for another key only": "[reasons]\nios_uncovered_lines_extra = \"unrelated\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			env := removalPR(t, productManifest("Demo", "Extra"), reasons)
			code, out := baseGate(t, env, "pull_request", 100, "--product", "demo")
			if code != 4 || !strings.Contains(out, "ios_uncovered_lines_widget is enrolled on the PR's base (7) and this PR removes it with its product, without a new reason") {
				t.Fatalf("exit %d; want 4 asking for a new reason:\n%s", code, out)
			}
		})
	}
}

// The base compare says when it made none (#586): an absent file at the base is
// a pass, and the output must say so, or it reads like a compare that found
// nothing to report.
func TestBaseCompareSaysWhenTheBaseIsNotEnrolled(t *testing.T) {
	_, env := basePR(t, "", locals+"ios_uncovered_lines = 100\n")
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 0 {
		t.Fatalf("exit %d; a first enrollment must still pass:\n%s", code, out)
	}
	if n := strings.Count(out, "base not enrolled: no base compare (first enrollment)"); n != 1 {
		t.Fatalf("the not-enrolled line appears %d times, want exactly 1:\n%s", n, out)
	}
}

func TestBaseCompareDoesNotSayNotEnrolledForAnEnrolledBase(t *testing.T) {
	for name, head := range map[string]string{
		"tightened": locals + "ios_uncovered_lines = 90\n",
		"unchanged": locals + "ios_uncovered_lines = 100\n",
		"deleted":   "",
	} {
		t.Run(name, func(t *testing.T) {
			_, env := basePR(t, locals+"ios_uncovered_lines = 100\n", head)
			_, out := baseGate(t, env, "pull_request", 100)
			if strings.Contains(out, "base not enrolled") {
				t.Fatalf("an enrolled base was reported as not enrolled:\n%s", out)
			}
		})
	}
}

// A base that cannot be read is an error, never the not-enrolled line. The
// base is enrolled here but its commit cannot be fetched, so the only way to
// print the line is to mistake the failure for an absent file.
func TestBaseCompareFetchFailureIsNotTheNotEnrolledLine(t *testing.T) {
	dir, env := basePR(t, locals+"ios_uncovered_lines = 100\n", locals+"ios_uncovered_lines = 100\n")
	gittest.Git(t, dir, "remote", "set-url", "origin", "file://"+filepath.Join(t.TempDir(), "gone.git"))
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 4 || !strings.Contains(out, "the PR's base could not be compared") || !strings.Contains(out, "could not fetch the base commit") {
		t.Fatalf("exit %d; want 4 naming the fetch failure:\n%s", code, out)
	}
	if strings.Contains(out, "base not enrolled") {
		t.Fatalf("a base that could not be fetched printed the not-enrolled line:\n%s", out)
	}
}
