package main

import (
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
