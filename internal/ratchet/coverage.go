package ratchet

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
)

// Coverage is the iOS app target's uncovered line count (executable minus
// covered), lower is better. Uncovered lines rather than a percentage, because
// deleting a tested file removes equal numbers from both counts: the metric does
// not move, where a percentage would drop and fail that PR for no fault.
//
// It is an EXTERNAL metric: measured by CI from an xccov report, never from the
// working tree. Measure never produces it, so local audit and sync leave it
// alone, and it is absent from a baseline until the project enrolls.
const Coverage = "ios_uncovered_lines"

// SlackMin and SlackPerMille set the band around the recorded value:
// max(20 lines, 0.5% of the app target's executable lines). 20 keeps a small app
// from tripping on a one-function change.
const (
	SlackMin      = 20
	SlackPerMille = 5
)

// Slack is the band half-width for an app target with this many executable lines.
func Slack(executable int) int {
	return max(SlackMin, executable*SlackPerMille/1000)
}

var coverageKeyRe = regexp.MustCompile(`^` + Coverage + `(_[a-z0-9]+(-[a-z0-9]+)*)?$`)

// IsCoverageKey reports whether key is the lone-product coverage metric or a
// matrix product's ios_uncovered_lines_<slug>.
func IsCoverageKey(key string) bool { return coverageKeyRe.MatchString(key) }

// CoverageKey is the ratchet key for a product: "-" is the lone product.
func CoverageKey(product string) string {
	if product == "-" {
		return Coverage
	}
	return Coverage + "_" + product
}

// AppTarget resolves --product to the xccov target name coverage is read for:
// "-" for a project with one product, otherwise a [[product]] slug.
func AppTarget(cfg *config.Config, product string) (string, error) {
	products := cfg.Products()
	if product == "-" {
		if len(products) != 1 {
			return "", fmt.Errorf("--product - names the lone product, but this project declares %d; pass a product slug", len(products))
		}
		return appTarget(products[0])
	}
	var slugs []string
	for _, p := range products {
		if p.Slug() == product && len(products) > 1 {
			return appTarget(p)
		}
		slugs = append(slugs, p.Slug())
	}
	return "", fmt.Errorf("no [[product]] has slug %q (have %s)", product, strings.Join(slugs, ", "))
}

func appTarget(p config.Product) (string, error) {
	if name := p.AppTargetName(); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("the product has no app target, scheme or name to read coverage for")
}

// CoverageReport is one app target's line counts from an xccov report.
type CoverageReport struct {
	Target     string
	Executable int
	Covered    int
}

func (r CoverageReport) Uncovered() int { return r.Executable - r.Covered }

func (r CoverageReport) Percent() float64 {
	return float64(r.Covered) * 100 / float64(r.Executable)
}

// ParseCoverage reads `xcrun xccov view --report --json` and selects the named
// app target. Each way the report can fail to describe that target is its own
// error, so "never ran" can never read as "0 lines, passed".
func ParseCoverage(data []byte, target string) (CoverageReport, error) {
	var doc struct {
		Targets []struct {
			Name       string `json:"name"`
			Executable *int   `json:"executableLines"`
			Covered    *int   `json:"coveredLines"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return CoverageReport{}, fmt.Errorf("not a JSON coverage report (%v); xccov probably failed, see the step above", err)
	}
	var names []string
	for _, t := range doc.Targets {
		names = append(names, t.Name)
		if t.Name != target {
			continue
		}
		if t.Executable == nil || t.Covered == nil {
			return CoverageReport{}, fmt.Errorf("%q has no executableLines/coveredLines in the coverage report", target)
		}
		r := CoverageReport{Target: target, Executable: *t.Executable, Covered: *t.Covered}
		if r.Executable <= 0 {
			return CoverageReport{}, fmt.Errorf("%q has 0 executable lines in the coverage report; the tests did not exercise the app target, so there is nothing to measure", target)
		}
		if r.Covered < 0 || r.Covered > r.Executable {
			return CoverageReport{}, fmt.Errorf("%q reports %d covered of %d executable lines", target, r.Covered, r.Executable)
		}
		return r, nil
	}
	return CoverageReport{}, fmt.Errorf("%q is not in the coverage report (targets: %s); a renamed scheme or app target reads as 0%% otherwise", target, strings.Join(names, ", "))
}

// GateInput is everything one coverage verdict depends on.
type GateInput struct {
	Key       string
	Baseline  *Baseline // nil when the project has no .lacquer.ratchet.toml
	Report    CoverageReport
	ReportErr error
	// TestResult is the Test job's own verdict; "" when not supplied.
	TestResult string
	Floor      int
	Relax      *baseline.Relax
	Now        time.Time
	// Event is GITHUB_EVENT_NAME; "" for a local run.
	Event string
}

// Verdict is the gate's output. Lines go to stdout; Summary is a Markdown table
// for the step summary.
type Verdict struct {
	Lines   []string
	Summary string
	Fail    bool
}

const notEnrolled = "coverage gate not enrolled: run lacquer ratchet --write --coverage-report <xccov.json> and commit " + Name

// Gate decides one product's coverage. Order matters: validity before verdict,
// so a report that describes nothing can never be compared and pass.
func Gate(in GateInput) Verdict {
	var v Verdict
	ci := in.Event != ""
	say := func(level, msg string) {
		if ci && level != "" {
			msg = "::" + level + "::" + msg
		}
		v.Lines = append(v.Lines, msg)
	}
	fail := func(msg string) { v.Fail = true; say("error", msg) }

	recorded, enrolled := 0, false
	if in.Baseline != nil {
		recorded, enrolled = in.Baseline.Ratchet[in.Key]
	}
	if !enrolled {
		// Un-enrolled projects stay green on sync, floor included; they go red
		// only after they enroll, which is deliberate and local.
		say("warning", notEnrolled)
		if in.ReportErr != nil {
			say("warning", "coverage: "+in.ReportErr.Error())
		}
		return v
	}
	if in.TestResult != "" && in.TestResult != "passed" {
		fail(fmt.Sprintf("coverage: the tests did not pass (test_result=%s), so coverage was not evaluated", in.TestResult))
		return v
	}
	if in.ReportErr != nil {
		fail("coverage: " + in.ReportErr.Error())
		return v
	}

	m, s := in.Report.Uncovered(), Slack(in.Report.Executable)
	lo, hi := max(0, recorded-s), recorded+s
	band := fmt.Sprintf("band %d–%d (slack %d)", lo, hi, s)
	verdict := "pass"
	switch {
	case m > hi:
		verdict = "regressed"
		fail(fmt.Sprintf("ratchet: %s regressed %d → %d, beyond the %s. Add tests, or run: lacquer ratchet --loosen %s --to %d --reason \"<why>\"", in.Key, recorded, m, band, in.Key, m))
	case m < lo:
		verdict = "improved"
		msg := fmt.Sprintf("ratchet: %s improved %d → %d, beyond the %s. Run this and commit %s: lacquer ratchet --accept %s=%d", in.Key, recorded, m, band, Name, in.Key, m)
		if in.Event == "pull_request" {
			// Advisory tightening is never done; forcing it on the PR that earned it is.
			fail(msg)
		} else {
			say("warning", msg)
		}
	default:
		say("", fmt.Sprintf("ratchet: %s = %d, recorded %d, %s", in.Key, m, recorded, band))
	}

	pct := in.Report.Percent()
	floorState := "met"
	if pct < float64(in.Floor) {
		msg := fmt.Sprintf("coverage: %s is %.1f%%, below the floor %d%%", in.Report.Target, pct, in.Floor)
		switch {
		case in.Relax == nil:
			floorState = "below"
			fail(msg + ". Add tests, or add a dated [baseline.relax] coverage entry to .lacquer.toml")
		case relaxExpired(*in.Relax, in.Now):
			floorState = "relax expired"
			fail(fmt.Sprintf("%s, and the [baseline.relax] coverage entry expired on %s (%s). Meet the floor or extend it deliberately", msg, in.Relax.Until, in.Relax.Reason))
		default:
			floorState = "relaxed until " + in.Relax.Until
			say("warning", fmt.Sprintf("%s (relaxed until %s: %s)", msg, in.Relax.Until, in.Relax.Reason))
		}
	} else {
		say("", fmt.Sprintf("coverage: %s is %.1f%%, floor %d%%", in.Report.Target, pct, in.Floor))
	}

	v.Summary = fmt.Sprintf("| Check | Recorded | Measured | Allowed | Verdict |\n|---|---|---|---|---|\n"+
		"| %s | %d | %d | %d–%d | %s |\n| coverage floor | %d%% | %.1f%% | ≥ %d%% | %s |\n",
		in.Key, recorded, m, lo, hi, verdict, in.Floor, pct, in.Floor, floorState)
	return v
}

// relaxExpired matches the CI relax step: the until date itself is still valid.
func relaxExpired(r baseline.Relax, now time.Time) bool {
	until, err := r.UntilDate()
	return err != nil || now.UTC().Format("2006-01-02") > until.Format("2006-01-02")
}

// CoverageNotes is what audit and sync say about the coverage metric: it is
// measured in CI, or the project has not enrolled. Silent for non-iOS projects.
func CoverageNotes(b *Baseline, cfg *config.Config) string {
	var keys []string
	if b != nil {
		for key := range b.Ratchet {
			if IsCoverageKey(key) {
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		if !hasIOS(cfg) {
			return ""
		}
		return "ratchet: " + notEnrolled + "\n"
	}
	slices.Sort(keys)
	var out strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&out, "ratchet: %s is measured in CI (not checked here)\n", key)
	}
	return out.String()
}

// Enrolled reports whether a project records any coverage metric.
func Enrolled(b *Baseline) bool {
	if b == nil {
		return false
	}
	for key := range b.Ratchet {
		if IsCoverageKey(key) {
			return true
		}
	}
	return false
}

func hasIOS(cfg *config.Config) bool {
	for _, c := range cfg.Components {
		if slices.Contains(c.Profiles, "ios") {
			return true
		}
	}
	return false
}

// WriteCoverage enrolls or tightens one coverage key from a measured report.
// An existing baseline gets only that key; a project with no baseline is
// initialized with the local metrics too, since Read requires them. A value
// above the recorded one is a regression: reported, never written.
func WriteCoverage(root string, cfg *config.Config, key string, value int) (*Finding, error) {
	b, err := Read(root)
	if err != nil {
		return nil, err
	}
	if b == nil {
		values, err := Measure(root, cfg)
		if err != nil {
			return nil, err
		}
		values[key] = value
		return nil, write(root, &Baseline{Ratchet: values})
	}
	before, ok := b.Ratchet[key]
	if ok && value == before {
		return nil, nil
	}
	if ok && value > before {
		return &Finding{key, before, value}, nil
	}
	b.Ratchet[key] = value
	if !ok {
		return nil, write(root, b)
	}
	return &Finding{key, before, value}, write(root, b)
}

// Accept writes N only if it is lower than the recorded value. Tightening
// unmeasured is safe: if N is too low, the next CI run fails as a regression.
func Accept(root, key string, value int) error {
	if !IsCoverageKey(key) {
		return fmt.Errorf("--accept takes a coverage metric (%s or %s_<product>), not %q", Coverage, Coverage, key)
	}
	if value < 0 {
		return fmt.Errorf("--accept %s=%d: the value must be nonnegative", key, value)
	}
	b, err := Read(root)
	if err != nil {
		return err
	}
	before, ok := 0, false
	if b != nil {
		before, ok = b.Ratchet[key]
	}
	if !ok {
		return fmt.Errorf("%s is not enrolled; %s", key, notEnrolled)
	}
	if value >= before {
		return fmt.Errorf("--accept only lowers %s (recorded %d, given %d); raising it takes --loosen %s --to %d --reason", key, before, value, key, value)
	}
	b.Ratchet[key] = value
	return write(root, b)
}

// LoosenTo raises a coverage metric to N with a recorded reason. The value comes
// from the CI failure message, since there is no local measurement.
func LoosenTo(root, key string, value int, reason string) error {
	if !IsCoverageKey(key) {
		return fmt.Errorf("--to applies to a coverage metric only; %q is measured locally, so loosen it without --to", key)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("loosening requires a nonempty --reason")
	}
	b, err := Read(root)
	if err != nil {
		return err
	}
	before, ok := 0, false
	if b != nil {
		before, ok = b.Ratchet[key]
	}
	if !ok {
		return fmt.Errorf("%s is not enrolled; %s", key, notEnrolled)
	}
	if value <= before {
		return fmt.Errorf("--loosen %s --to %d does not raise it (recorded %d); use --accept to lower", key, value, before)
	}
	b.Ratchet[key] = value
	if b.Reasons == nil {
		b.Reasons = map[string]string{}
	}
	b.Reasons[key] = strings.TrimSpace(reason)
	return write(root, b)
}

// ReadReport reads a report file for ParseCoverage.
func ReadReport(path, target string) (CoverageReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return CoverageReport{}, fmt.Errorf("read coverage report: %w", err)
	}
	return ParseCoverage(data, target)
}
