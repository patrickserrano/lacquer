package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/ratchet"
)

// A PR that changes the product count (#578). The coverage step measures the
// lone key for one product and a per-slug key per leg for several, so a 2→1 or
// 1→2 PR measures a key the base never recorded. Before this, the gate saw it
// as un-enrolled, warned and passed: coverage un-gated, no reason, no inbox
// item. Now, when the base gates coverage at all, the measured key must be
// enrolled on the PR, with a new reason, and the enrollment is reported.

// countPR checks out a PR from baseManifest/baseRatchet to headManifest/
// headRatchet and returns the env to run the CLI with.
func countPR(t *testing.T, baseManifest, baseRatchet, headManifest, headRatchet string) func(string) string {
	t.Helper()
	pr := gittest.NewPR(t, map[string]string{".lacquer.toml": baseManifest, ".lacquer.ratchet.toml": baseRatchet}, func(work string) {
		gittest.Write(t, work, map[string]string{".lacquer.toml": headManifest, ".lacquer.ratchet.toml": headRatchet})
	})
	lq := t.TempDir()
	gittest.Write(t, lq, map[string]string{"profiles/ios/baseline.toml": "[baseline]\ncoverage_floor = 80\n"})
	chdir(t, pr.Checkout)
	return envMap(map[string]string{"LACQUER_ROOT": lq})
}

const (
	twoKeys = locals + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_widget = 7\n"
	loneKey = locals + "ios_uncovered_lines = 100\n"
)

func TestProductCount2To1FailsWithTheLoneKeyUnenrolled(t *testing.T) {
	env := countPR(t, productManifest("Demo", "Widget"), twoKeys, productManifest("Demo"), twoKeys)
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 4 || !strings.Contains(out, "::error::ratchet: this leg measures ios_uncovered_lines, which this PR does not enroll, while the PR's base gates coverage") ||
		!strings.Contains(out, `lacquer ratchet --enroll ios_uncovered_lines --to 100 --reason "<why the product count changed>"`) {
		t.Fatalf("exit %d; want 4 with the enroll command and this run's value:\n%s", code, out)
	}
}

func TestProductCount1To2FailsWithTheNewKeyUnenrolled(t *testing.T) {
	env := countPR(t, productManifest("Demo"), loneKey, productManifest("Demo", "Widget"), loneKey)
	code, out := baseGate(t, env, "pull_request", 100, "--product", "demo")
	if code != 4 || !strings.Contains(out, "this leg measures ios_uncovered_lines_demo, which this PR does not enroll") ||
		!strings.Contains(out, `lacquer ratchet --enroll ios_uncovered_lines_demo --to 100 --reason`) {
		t.Fatalf("exit %d; want 4 naming the per-slug key:\n%s", code, out)
	}
}

// When the report itself is unreadable there is no value to print, and the
// command says where to get one rather than inventing it.
func TestProductCountFailureWithoutAReportPointsAtAPassingRun(t *testing.T) {
	env := countPR(t, productManifest("Demo", "Widget"), twoKeys, productManifest("Demo"), twoKeys)
	report := writeReport(t, t.TempDir(), "not json")
	code, out := gate(t, env, report, "pull_request", "--compare-base")
	if code != 4 || !strings.Contains(out, "--to <the uncovered lines a passing run reports>") {
		t.Fatalf("exit %d; want 4 without a made-up value:\n%s", code, out)
	}
}

func TestProductCountChangePassesWithAReasonedEnrollmentAndReportsIt(t *testing.T) {
	head := loneKey + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_widget = 7\n[reasons]\n" +
		"ios_uncovered_lines_demo = \"split into an app and a widget\"\nios_uncovered_lines_widget = \"split into an app and a widget\"\n"
	env := countPR(t, productManifest("Demo"), loneKey, productManifest("Demo", "Widget"), head)
	summary := filepath.Join(t.TempDir(), "summary.md")
	code, out := baseGate(t, env, "pull_request", 100, "--product", "demo", "--summary", summary)
	if code != 0 {
		t.Fatalf("exit %d; an enrollment with a new reason passes:\n%s", code, out)
	}
	for _, want := range []string{
		"ratchet: enrolled ios_uncovered_lines_demo at 100: split into an app and a widget",
		`::notice title=lacquer ratchet enrolled::lacquer-ratchet-enrolled/v1 {"key":"ios_uncovered_lines_demo","to":100,"reason":"split into an app and a widget"}`,
		`lacquer-ratchet-enrolled/v1 {"key":"ios_uncovered_lines_widget","to":7,"reason":"split into an app and a widget"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	data, err := os.ReadFile(summary)
	if err != nil || !strings.Contains(string(data), "- enrolled ios_uncovered_lines_demo at 100: split into an app and a widget") {
		t.Errorf("the job summary lacks the enrollment (%v):\n%s", err, data)
	}
}

func TestProductCountEnrollmentWithoutANewReasonFails(t *testing.T) {
	head := loneKey + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_widget = 7\n"
	env := countPR(t, productManifest("Demo"), loneKey, productManifest("Demo", "Widget"), head)
	code, out := baseGate(t, env, "pull_request", 100, "--product", "demo")
	if code != 4 || !strings.Contains(out, "ios_uncovered_lines_demo is enrolled on this PR while the PR's base already gates coverage, without a new reason in [reasons]") {
		t.Fatalf("exit %d; want 4 asking for a reason:\n%s", code, out)
	}
}

// A first enrollment, of any number of products, needs no reason: the base
// gated nothing, so nothing is being moved.
func TestProductCountFirstEnrollmentStillPasses(t *testing.T) {
	env := countPR(t, productManifest("Demo", "Widget"), locals, productManifest("Demo", "Widget"), twoKeys)
	if code, out := baseGate(t, env, "pull_request", 100, "--product", "demo"); code != 0 || strings.Contains(out, "::error::") {
		t.Fatalf("exit %d; a first enrollment passes:\n%s", code, out)
	}
	// Measuring a key while the base gates nothing stays the old warning.
	env = countPR(t, productManifest("Demo"), locals, productManifest("Demo"), locals)
	if code, out := baseGate(t, env, "pull_request", 100); code != 0 || !strings.Contains(out, "coverage gate not enrolled") {
		t.Fatalf("exit %d; want the not-enrolled warning:\n%s", code, out)
	}
}

// Once no single-product render exists, the lone key measures nothing: it may
// be removed under the removal-with-reason rule (#571), and is reported.
func TestTheStaleLoneKeyIsRemovableOnceNoSingleProductRenderExists(t *testing.T) {
	head := locals + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_widget = 7\n[reasons]\n" +
		"ios_uncovered_lines = \"no single-product render after the split\"\n" +
		"ios_uncovered_lines_demo = \"split into an app and a widget\"\nios_uncovered_lines_widget = \"split into an app and a widget\"\n"
	env := countPR(t, productManifest("Demo"), loneKey, productManifest("Demo", "Widget"), head)
	code, out := baseGate(t, env, "pull_request", 100, "--product", "demo")
	if code != 0 || !strings.Contains(out, `lacquer-ratchet-loosened/v1 {"key":"ios_uncovered_lines","from":100,"removed":true,"reason":"no single-product render after the split"}`) {
		t.Fatalf("exit %d; want the lone key's removal passed and reported:\n%s", code, out)
	}
	// Without a reason it is the #571 failure.
	head = locals + "ios_uncovered_lines_demo = 100\nios_uncovered_lines_widget = 7\n[reasons]\n" +
		"ios_uncovered_lines_demo = \"split\"\nios_uncovered_lines_widget = \"split\"\n"
	env = countPR(t, productManifest("Demo"), loneKey, productManifest("Demo", "Widget"), head)
	if code, out := baseGate(t, env, "pull_request", 100, "--product", "demo"); code != 4 || !strings.Contains(out, "ios_uncovered_lines is enrolled on the PR's base (100) and this PR removes it with its product, without a new reason") {
		t.Fatalf("exit %d; want 4 asking for a reason:\n%s", code, out)
	}
}

func TestTheLoneKeyStaysLiveWhileTheProjectRendersOneProduct(t *testing.T) {
	env := countPR(t, productManifest("Demo"), loneKey+"ios_uncovered_lines_demo = 100\n", productManifest("Demo"),
		locals+"ios_uncovered_lines_demo = 100\n[reasons]\nios_uncovered_lines = \"tidy\"\n")
	code, out := baseGate(t, env, "pull_request", 100)
	if code != 4 || !strings.Contains(out, "ios_uncovered_lines is enrolled on the PR's base (100) and this PR removes it") || !strings.Contains(out, "its product is still in .lacquer.toml") {
		t.Fatalf("exit %d; the lone key of a one-product project is live:\n%s", code, out)
	}
}

// --enroll is the reasoned path's write: the CI-reported value, a reason, and
// nothing else.
func TestRatchetEnrollWritesTheKeyAndItsReason(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	var out strings.Builder
	if code := run([]string{"ratchet", "--enroll", "ios_uncovered_lines_widget", "--to", "7", "--reason", "widget added"}, env, &out, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	b, err := ratchet.Read(dir)
	if err != nil || b.Ratchet["ios_uncovered_lines_widget"] != 7 || b.Reasons["ios_uncovered_lines_widget"] != "widget added" || b.Ratchet[ratchet.Coverage] != 4312 {
		t.Fatalf("baseline %+v (%v)", b, err)
	}
	for name, args := range map[string][]string{
		"already enrolled":   {"--enroll", "ios_uncovered_lines", "--to", "9000", "--reason", "x"},
		"not a coverage key": {"--enroll", "unjustified_suppressions", "--to", "1", "--reason", "x"},
		"no reason":          {"--enroll", "ios_uncovered_lines_extra", "--to", "1"},
		"blank reason":       {"--enroll", "ios_uncovered_lines_extra", "--to", "1", "--reason", "  "},
		"no value":           {"--enroll", "ios_uncovered_lines_extra", "--reason", "x"},
		"negative":           {"--enroll", "ios_uncovered_lines_extra", "--to", "-1", "--reason", "x"},
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			if code := run(append([]string{"ratchet"}, args...), env, &out, &out); code == 0 {
				t.Fatalf("exit 0; want a refusal:\n%s", out.String())
			}
			after, err := ratchet.Read(dir)
			if err != nil || len(after.Ratchet) != len(b.Ratchet) || after.Ratchet[ratchet.Coverage] != 4312 {
				t.Fatalf("a refused enroll changed the baseline: %+v (%v)", after, err)
			}
		})
	}
}
