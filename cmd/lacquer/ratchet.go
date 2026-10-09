package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/ratchet"
)

func runRatchet(args []string, root, lacquerRoot string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ratchet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	write := fs.Bool("write", false, "initialize or tighten the committed baseline")
	loosen := fs.String("loosen", "", "raise this metric to its current measurement")
	reason := fs.String("reason", "", "reason for the explicit increase")
	to := fs.String("to", "", "with --loosen on a coverage metric: the value to raise it to")
	accept := fs.String("accept", "", "METRIC=N: lower a coverage metric to N (never raises)")
	report := fs.String("coverage-report", "", "xccov --report --json output to measure the iOS coverage metric from")
	product := fs.String("product", "", "with --coverage-report: the product slug, or - for the lone product")
	event := fs.String("ci-event", "", "GITHUB_EVENT_NAME; an improvement beyond the slack fails only on pull_request")
	testResult := fs.String("test-result", "", "the Test job's test_result; anything but passed fails the gate")
	summary := fs.String("summary", "", "append the coverage verdict table to this file (GITHUB_STEP_SUMMARY)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	coverageOnly := *product != "" || *event != "" || *testResult != "" || *summary != ""
	if fs.NArg() != 0 || (*write && *loosen != "") || (*reason != "" && *loosen == "") ||
		(*to != "" && *loosen == "") || (*accept != "" && (*write || *loosen != "" || *report != "" || coverageOnly)) ||
		(coverageOnly && *report == "") || (*report != "" && *product == "") || (*report != "" && *loosen != "") {
		fmt.Fprintln(stderr, "use ratchet [--write | --loosen METRIC --reason TEXT [--to N] | --accept METRIC=N]\n"+
			"          [--coverage-report XCCOV.json --product SLUG|- [--write] [--ci-event E] [--test-result R] [--summary FILE]]")
		return 2
	}
	if *accept != "" {
		key, raw, ok := strings.Cut(*accept, "=")
		n, err := strconv.Atoi(raw)
		if !ok || err != nil {
			return fail(stderr, fmt.Errorf("--accept takes METRIC=N, got %q", *accept))
		}
		if err := ratchet.Accept(root, key, n); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "ratchet: %s lowered to %d in %s\n", key, n, ratchet.Name)
		return 0
	}
	cfg, err := config.Load(filepath.Join(root, ".lacquer.toml"))
	if err != nil {
		return fail(stderr, err)
	}
	if *loosen != "" {
		if *to != "" {
			n, err := strconv.Atoi(*to)
			if err != nil {
				return fail(stderr, fmt.Errorf("--to takes an integer, got %q", *to))
			}
			if err := ratchet.LoosenTo(root, *loosen, n, *reason); err != nil {
				return fail(stderr, err)
			}
		} else if err := ratchet.Loosen(root, cfg, *loosen, *reason); err != nil {
			return fail(stderr, err)
		}
		fmt.Fprintf(stdout, "ratchet: recorded %s increase with reason in %s\n", *loosen, ratchet.Name)
		return 0
	}
	if *report != "" {
		return runCoverage(root, lacquerRoot, cfg, *report, *product, *event, *testResult, *summary, *write, stdout, stderr)
	}
	if *write {
		findings, err := ratchet.Tighten(root, cfg, true)
		if err != nil {
			return fail(stderr, err)
		}
		fmt.Fprint(stdout, ratchet.Format(findings))
		if ratchet.Blocking(findings) > 0 {
			return 4
		}
		fmt.Fprintf(stdout, "ratchet: baseline saved in %s\n", ratchet.Name)
		return 0
	}
	values, err := ratchet.Measure(root, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "ratchet: claude_md_project_lines = %d\nratchet: unjustified_suppressions = %d\n", values[ratchet.ClaudeProjectLines], values[ratchet.Suppressions])
	findings, err := ratchet.Check(root, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprint(stdout, ratchet.Format(findings))
	b, err := ratchet.Read(root)
	if err != nil {
		return fail(stderr, err)
	}
	if b == nil {
		fmt.Fprintln(stdout, "ratchet: no baseline; run lacquer ratchet --write and commit the file")
	}
	fmt.Fprint(stdout, ratchet.CoverageNotes(b, cfg))
	if ratchet.Blocking(findings) > 0 {
		return 4
	}
	return 0
}

// runCoverage is the iOS coverage gate the Test job's Check Coverage step runs,
// and, with --write, the enrollment and tighten path. Every message is owned
// here; the workflow step is plumbing and reads only the exit code.
func runCoverage(root, lacquerRoot string, cfg *config.Config, report, product, event, testResult, summary string, write bool, stdout, stderr io.Writer) int {
	target, err := ratchet.AppTarget(cfg, product)
	if err != nil {
		return fail(stderr, err)
	}
	key := ratchet.CoverageKey(product)
	measured, reportErr := ratchet.ReadReport(report, target)
	if write {
		if reportErr != nil {
			return fail(stderr, fmt.Errorf("not enrolling or tightening %s: %w", key, reportErr))
		}
		f, err := ratchet.WriteCoverage(root, cfg, key, measured.Uncovered())
		if err != nil {
			return fail(stderr, err)
		}
		if f != nil && f.After > f.Before+ratchet.Slack(measured.Executable) {
			fmt.Fprint(stdout, ratchet.Format([]ratchet.Finding{*f}))
			return 4
		}
		fmt.Fprintf(stdout, "ratchet: %s = %d (%.1f%% of %s); baseline saved in %s\n", key, measured.Uncovered(), measured.Percent(), target, ratchet.Name)
		return 0
	}
	b, err := ratchet.Read(root)
	if err != nil {
		return fail(stderr, err)
	}
	in := ratchet.GateInput{Key: key, Baseline: b, Report: measured, ReportErr: reportErr, TestResult: testResult, Now: time.Now(), Event: event}
	if b != nil {
		if _, enrolled := b.Ratchet[key]; enrolled {
			if in.Floor, err = baseline.CoverageFloor(lacquerRoot, "ios"); err != nil {
				return fail(stderr, err)
			}
			if r, ok := cfg.Baseline.Relax["coverage"]; ok {
				in.Relax = &r
			}
		}
	}
	v := ratchet.Gate(in)
	for _, line := range v.Lines {
		fmt.Fprintln(stdout, line)
	}
	if summary != "" && v.Summary != "" {
		f, err := os.OpenFile(summary, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fail(stderr, err)
		}
		_, werr := fmt.Fprintf(f, "\n### Coverage ratchet\n\n%s", v.Summary)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return fail(stderr, werr)
		}
	}
	if v.Fail {
		return 4
	}
	return 0
}

// Exercise the real CLI gate on a scratch project, not just Compare. The
// positive control rejects a gate that fails for unrelated setup errors.
func ratchetProbe() doctor.Result {
	r := doctor.Result{Component: ".", Profile: doctor.CoreLayer, Name: "ratchet audit rejects a regression after tightening"}
	dir, err := os.MkdirTemp("", "lacquer-ratchet-probe-")
	if err != nil {
		r.Detail = err.Error()
		return r
	}
	defer os.RemoveAll(dir)
	err = proveRatchet(dir)
	r.OK = err == nil
	if err != nil {
		r.Detail = err.Error()
	}
	return r
}

func proveRatchet(dir string) error {
	content := filepath.Join(dir, "content")
	project := filepath.Join(dir, "project")
	for name, body := range map[string]string{
		"content/VERSION": "1.0.0\n", "content/core/CLAUDE.core.md": "rules\n",
		"project/.lacquer.toml":              "[project]\nname = 'probe'\nproject_name = 'Probe'\n[[component]]\npath = '.'\nprofiles = []\n",
		"content/profiles/ios/baseline.toml": "[baseline]\ncoverage_floor = 80\n",
		"project/CLAUDE.md":                  strings.Repeat("line\n", 100), "project/probe.ts": "// eslint-disable no-console\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(content, "profiles"), 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "probe.ts"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git: %w: %s", err, out)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(project); err != nil {
		return err
	}
	defer os.Chdir(old)
	env := func(key string) string {
		switch key {
		case "LACQUER_ROOT":
			return content
		case "LACQUER_ALLOW_UNVERIFIED_ROOT", "LACQUER_ALLOW_STALE_BINARY":
			return "1"
		}
		return ""
	}
	var out bytes.Buffer
	invoke := func(args ...string) int { out.Reset(); return run(args, env, &out, &out) }
	if code := invoke("sync"); code != 0 {
		return fmt.Errorf("initial sync = %d: %s", code, &out)
	}
	if code := invoke("ratchet", "--write"); code != 0 {
		return fmt.Errorf("initialize = %d: %s", code, &out)
	}
	claude := filepath.Join(project, "CLAUDE.md")
	data, err := os.ReadFile(claude)
	if err != nil {
		return err
	}
	if err := os.WriteFile(claude, []byte(strings.Replace(string(data), strings.Repeat("line\n", 100), "line\n", 1)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(project, "probe.ts"), []byte("const ok = 1;\n"), 0o644); err != nil {
		return err
	}
	if code := invoke("sync"); code != 0 {
		return fmt.Errorf("tighten = %d: %s", code, &out)
	}
	if code := invoke("audit"); code != 0 {
		return fmt.Errorf("positive control = %d: %s", code, &out)
	}
	if err := os.WriteFile(filepath.Join(project, "probe.ts"), []byte("// eslint-disable no-console\n"), 0o644); err != nil {
		return err
	}
	if code := invoke("audit"); code != 4 || !strings.Contains(out.String(), "ratchet: unjustified_suppressions regressed 0 → 1") {
		return fmt.Errorf("known-bad audit = %d, expected 4 and 0 → 1 diagnostic: %s", code, &out)
	}
	if err := os.WriteFile(filepath.Join(project, "probe.ts"), []byte("const ok = 1;\n"), 0o644); err != nil {
		return err
	}
	data, err = os.ReadFile(claude)
	if err != nil {
		return err
	}
	if err := os.WriteFile(claude, append(data, []byte("extra prose\n")...), 0o644); err != nil {
		return err
	}
	if code := invoke("audit"); code != 4 || !strings.Contains(out.String(), "ratchet: claude_md_project_lines regressed") {
		return fmt.Errorf("CLAUDE regression = %d: %s", code, &out)
	}
	return proveCoverage(project, invoke, &out)
}

// proveCoverage is the iOS coverage gate's leg: enroll from a report, pass the
// same report (positive control), then fail a report that regressed beyond the
// slack. A gate that cannot reach exit 4 here cannot reach it in CI either.
func proveCoverage(project string, invoke func(...string) int, out *bytes.Buffer) error {
	report := filepath.Join(project, "coverage-report.json")
	put := func(covered int) error {
		body := fmt.Sprintf(`{"targets":[{"name":"Probe.app","executableLines":1000,"coveredLines":%d}]}`, covered)
		return os.WriteFile(report, []byte(body), 0o644)
	}
	if err := put(900); err != nil {
		return err
	}
	if code := invoke("ratchet", "--write", "--coverage-report", report, "--product", "-"); code != 0 {
		return fmt.Errorf("coverage enroll = %d: %s", code, out)
	}
	if code := invoke("ratchet", "--coverage-report", report, "--product", "-", "--ci-event", "pull_request"); code != 0 {
		return fmt.Errorf("coverage positive control = %d: %s", code, out)
	}
	if err := put(800); err != nil {
		return err
	}
	if code := invoke("ratchet", "--coverage-report", report, "--product", "-", "--ci-event", "pull_request"); code != 4 ||
		!strings.Contains(out.String(), "ios_uncovered_lines regressed 100 → 200") {
		return fmt.Errorf("known-bad coverage = %d, expected 4 and 100 → 200: %s", code, out)
	}
	return nil
}
