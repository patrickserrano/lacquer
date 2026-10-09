package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The iOS coverage gate (#522 U1). Every test drives the real CLI, because the
// verdict is an exit code a CI step reads: a Gate that computes the right answer
// and a CLI that returns 0 anyway is the failure these exist to catch.

// covReport is an xccov `view --report --json` document with the app target and
// a test bundle. The test bundle comes FIRST, so a selector that takes
// .targets[0] instead of the named app target reads the wrong numbers.
func covReport(app string, executable, covered int) string {
	return fmt.Sprintf(`{"coveredLines":%[2]d,"executableLines":%[1]d,"lineCoverage":0.5,"targets":[
  {"name":"DemoTests.xctest","coveredLines":900,"executableLines":1000,"lineCoverage":0.9,"files":[]},
  {"name":%[3]q,"coveredLines":%[2]d,"executableLines":%[1]d,"lineCoverage":0.5,"files":[]}
]}`, executable, covered, app)
}

// covProject is a one-product iOS manifest ("Demo", so the app target is
// Demo.app), a ratchet file, and a lacquer root whose ios baseline carries the
// given floor (or none when floor is ""). relax is an optional
// [baseline.relax] coverage line.
func covProject(t *testing.T, ratchetBody, floor, relax string) (dir string, env func(string) string) {
	t.Helper()
	dir = t.TempDir()
	lq := t.TempDir()
	manifest := "[project]\nname = \"demo\"\nproject_name = \"Demo\"\nscheme = \"Demo\"\nxcodeproj = \"Demo.xcodeproj\"\n\n" +
		"[[component]]\npath = \".\"\nprofiles = [\"ios\"]\n"
	if relax != "" {
		manifest += "\n[baseline.relax]\n" + relax + "\n"
	}
	files := map[string]string{filepath.Join(dir, ".lacquer.toml"): manifest}
	if ratchetBody != "" {
		files[filepath.Join(dir, ".lacquer.ratchet.toml")] = ratchetBody
	}
	spec := "[baseline]\nswift_version = \"6\"\n"
	if floor != "" {
		spec += "coverage_floor = " + floor + "\n"
	}
	files[filepath.Join(lq, "profiles", "ios", "baseline.toml")] = spec
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	chdir(t, dir)
	return dir, envMap(map[string]string{"LACQUER_ROOT": lq})
}

const enrolled4312 = "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines = 4312\n"

// liveRelax keeps the floor out of the band tests: 8,000 executable lines with
// ~4,300 uncovered is ~46%, below any floor worth having.
const liveRelax = `coverage = { until = "2099-01-01", reason = "enrolled below the floor" }`

func writeReport(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "coverage-report.json")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func gate(t *testing.T, env func(string) string, report, event string, extra ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	args := append([]string{"ratchet", "--coverage-report", report, "--product", "-", "--ci-event", event}, extra...)
	code := run(args, env, &out, &out)
	return code, out.String()
}

// Slack for 8,000 executable lines is max(20, 0.5% of 8,000) = 40.

func TestCoverageGateFailsOnRegressionBeyondSlack(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-4400)), "pull_request")
	if code != 4 {
		t.Fatalf("exit %d, want 4:\n%s", code, out)
	}
	for _, want := range []string{"ios_uncovered_lines regressed 4312 → 4400", "lacquer ratchet --loosen ios_uncovered_lines --to 4400 --reason"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCoverageGateFailsOnUnacceptedImprovementOnPullRequest(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-4200)), "pull_request")
	if code != 4 {
		t.Fatalf("exit %d, want 4:\n%s", code, out)
	}
	if !strings.Contains(out, "::error::") || !strings.Contains(out, "lacquer ratchet --accept ios_uncovered_lines=4200") {
		t.Errorf("want an error naming the exact --accept command:\n%s", out)
	}
}

func TestCoverageGateWarnsOnUnacceptedImprovementOnPush(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-4200)), "push")
	if code != 0 {
		t.Fatalf("exit %d, want 0 (push must not redden main):\n%s", code, out)
	}
	if !strings.Contains(out, "::warning::") || !strings.Contains(out, "lacquer ratchet --accept ios_uncovered_lines=4200") {
		t.Errorf("want a warning naming the --accept command:\n%s", out)
	}
	if strings.Contains(out, "::error::") {
		t.Errorf("push printed an error:\n%s", out)
	}
}

func TestCoverageGatePassesInsideTheBand(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	for _, m := range []int{4320, 4352, 4272} { // inside, and both edges
		code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-m)), "pull_request")
		if code != 0 {
			t.Fatalf("M=%d: exit %d, want 0:\n%s", m, code, out)
		}
		if !strings.Contains(out, "band 4272–4352") {
			t.Errorf("M=%d: band not printed:\n%s", m, out)
		}
	}
}

// The floor: 1,000 executable lines, so slack is 20 and the band test values
// stay out of the way. B is set to the measured value.
func floorProject(t *testing.T, floor, relax string) (string, func(string) string) {
	return covProject(t, "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines = 300\n", floor, relax)
}

func TestCoverageFloorFailsBelow80Unrelaxed(t *testing.T) {
	dir, env := floorProject(t, "80", "")
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 1000, 700)), "pull_request")
	if code != 4 || !strings.Contains(out, "70.0%") || !strings.Contains(out, "floor 80%") {
		t.Fatalf("exit %d, want 4 naming 70.0%% and the 80%% floor:\n%s", code, out)
	}
}

func TestCoverageFloorPassesAtOrAbove80(t *testing.T) {
	dir, env := covProject(t, "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\nios_uncovered_lines = 200\n", "80", "")
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 1000, 800)), "pull_request")
	if code != 0 {
		t.Fatalf("exit %d, want 0 at exactly the floor:\n%s", code, out)
	}
}

func TestCoverageFloorWarnsWhenRelaxed(t *testing.T) {
	dir, env := floorProject(t, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 1000, 700)), "pull_request")
	if code != 0 || !strings.Contains(out, "::warning::") || !strings.Contains(out, "2099-01-01") {
		t.Fatalf("exit %d, want 0 with a warning naming the relax date:\n%s", code, out)
	}
}

func TestCoverageFloorFailsWhenRelaxExpired(t *testing.T) {
	dir, env := floorProject(t, "80", `coverage = { until = "2020-01-01", reason = "old" }`)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 1000, 700)), "pull_request")
	if code != 4 || !strings.Contains(out, "expired") {
		t.Fatalf("exit %d, want 4 naming the expired relax:\n%s", code, out)
	}
}

// A lacquer root whose ios baseline omits coverage_floor must not read as a 0%
// floor that every project passes.
func TestCoverageFloorRequiresTheKey(t *testing.T) {
	dir, env := floorProject(t, "", "")
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 1000, 700)), "pull_request")
	if code == 0 || !strings.Contains(out, "coverage_floor") {
		t.Fatalf("exit %d, want a failure naming coverage_floor:\n%s", code, out)
	}
}

func TestCoverageGateFailsWhenTheAppTargetIsAbsentFromTheReport(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Renamed.app", 8000, 8000-4312)), "pull_request")
	if code != 4 || !strings.Contains(out, `"Demo.app" is not in the coverage report`) {
		t.Fatalf("exit %d, want 4 naming the missing target:\n%s", code, out)
	}
}

func TestCoverageGateFailsWhenExecutableLinesIsZero(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 0, 0)), "pull_request")
	if code != 4 || !strings.Contains(out, "0 executable lines") {
		t.Fatalf("exit %d, want 4 naming zero executable lines:\n%s", code, out)
	}
}

func TestCoverageGateFailsWhenTheReportIsNotJSON(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, "Error: Error Domain=XCCovErrorDomain"), "pull_request")
	if code != 4 || !strings.Contains(out, "not a JSON coverage report") {
		t.Fatalf("exit %d, want 4 naming the unreadable report:\n%s", code, out)
	}
}

func TestCoverageGateFailsWhenTheTestsDidNotPass(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-4312)), "pull_request", "--test-result", "unknown")
	if code != 4 || !strings.Contains(out, "test_result=unknown") {
		t.Fatalf("exit %d, want 4 naming the test result:\n%s", code, out)
	}
}

func TestNotEnrolledWarnsAndPasses(t *testing.T) {
	for name, body := range map[string]string{
		"no ratchet file": "",
		"no coverage key": "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Below the floor, unrelaxed: un-enrolled projects must stay green on sync.
			dir, env := covProject(t, body, "80", "")
			code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 1000, 100)), "pull_request")
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			if !strings.Contains(out, "::warning::coverage gate not enrolled: run lacquer ratchet --write --coverage-report <xccov.json> and commit .lacquer.ratchet.toml") {
				t.Errorf("enrollment hint missing:\n%s", out)
			}
		})
	}
}

func TestCoverageGateWritesTheStepSummaryTable(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", liveRelax)
	summary := filepath.Join(dir, "summary.md")
	code, out := gate(t, env, writeReport(t, dir, covReport("Demo.app", 8000, 8000-4320)), "pull_request", "--summary", summary)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"| ios_uncovered_lines | 4312 | 4320 |", "| coverage floor |"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("summary lacks %q:\n%s", want, data)
		}
	}
}

func readRatchet(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".lacquer.ratchet.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAcceptLowersTheRecordedValue(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", "")
	var out bytes.Buffer
	if code := run([]string{"ratchet", "--accept", "ios_uncovered_lines=4200"}, env, &out, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, &out)
	}
	if got := readRatchet(t, dir); !strings.Contains(got, "ios_uncovered_lines = 4200") {
		t.Fatalf("not lowered:\n%s", got)
	}
}

func TestAcceptRefusesToRaise(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", "")
	var out bytes.Buffer
	if code := run([]string{"ratchet", "--accept", "ios_uncovered_lines=4400"}, env, &out, &out); code == 0 {
		t.Fatalf("raising via --accept exited 0:\n%s", &out)
	}
	if got := readRatchet(t, dir); !strings.Contains(got, "ios_uncovered_lines = 4312") {
		t.Fatalf("baseline changed:\n%s", got)
	}
}

func TestLoosenToRequiresAReasonAndAHigherValue(t *testing.T) {
	dir, env := covProject(t, enrolled4312, "80", "")
	var out bytes.Buffer
	for _, args := range [][]string{
		{"ratchet", "--loosen", "ios_uncovered_lines", "--to", "4400"},
		{"ratchet", "--loosen", "ios_uncovered_lines", "--to", "4400", "--reason", " "},
		{"ratchet", "--loosen", "ios_uncovered_lines", "--to", "4312", "--reason", "same"},
		{"ratchet", "--loosen", "ios_uncovered_lines", "--to", "4000", "--reason", "lower"},
		{"ratchet", "--loosen", "ios_uncovered_lines", "--reason", "no value"},
	} {
		out.Reset()
		if code := run(args, env, &out, &out); code == 0 {
			t.Errorf("%v exited 0:\n%s", args, &out)
		}
	}
	if got := readRatchet(t, dir); !strings.Contains(got, "ios_uncovered_lines = 4312") {
		t.Fatalf("a refused loosen changed the baseline:\n%s", got)
	}
	out.Reset()
	if code := run([]string{"ratchet", "--loosen", "ios_uncovered_lines", "--to", "4400", "--reason", "vendored parser"}, env, &out, &out); code != 0 {
		t.Fatalf("valid loosen exited %d:\n%s", code, &out)
	}
	got := readRatchet(t, dir)
	if !strings.Contains(got, "ios_uncovered_lines = 4400") || !strings.Contains(got, "vendored parser") {
		t.Fatalf("loosen not recorded with its reason:\n%s", got)
	}
}

func TestWriteEnrollsFromACoverageReport(t *testing.T) {
	dir, env := covProject(t, "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\n", "80", "")
	report := writeReport(t, dir, covReport("Demo.app", 8000, 8000-4312))
	var out bytes.Buffer
	if code := run([]string{"ratchet", "--write", "--coverage-report", report, "--product", "-"}, env, &out, &out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, &out)
	}
	if got := readRatchet(t, dir); !strings.Contains(got, "ios_uncovered_lines = 4312") {
		t.Fatalf("not enrolled:\n%s", got)
	}
	// An invalid report must never enroll a project at a meaningless number.
	dir2, env2 := covProject(t, "[ratchet]\nclaude_md_project_lines = 0\nunjustified_suppressions = 0\n", "80", "")
	out.Reset()
	if code := run([]string{"ratchet", "--write", "--coverage-report", writeReport(t, dir2, covReport("Other.app", 8000, 10)), "--product", "-"}, env2, &out, &out); code == 0 {
		t.Fatalf("enrolled from a report without the app target:\n%s", &out)
	}
	if got := readRatchet(t, dir2); strings.Contains(got, "ios_uncovered_lines") {
		t.Fatalf("invalid report enrolled the project:\n%s", got)
	}
}
