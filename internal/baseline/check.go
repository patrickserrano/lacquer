package baseline

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Status is the outcome of checking one baseline key.
type Status string

const (
	StatusOK        Status = "ok"        // every Swift-compiling config satisfies it
	StatusViolation Status = "violation" // it does not, and nothing excuses that
	StatusRelaxed   Status = "relaxed"   // violated, but explicitly and unexpiredly excused
	StatusExpired   Status = "expired"   // excused by a relaxation whose date has passed
	StatusImplied   Status = "implied"   // satisfied by a stronger setting, so not checked
)

// Relax is a project's time-boxed, justified opt-out from one baseline key.
type Relax struct {
	Until  string `toml:"until"`  // YYYY-MM-DD
	Reason string `toml:"reason"` // why, ideally with a tracking issue
}

// UntilDate parses Until. Exported so a manifest's relaxation can be rejected at
// load time rather than silently ignored at check time.
func (r Relax) UntilDate() (time.Time, error) {
	return time.Parse("2006-01-02", r.Until)
}

// KnownKeys is every key a relaxation may name.
//
// A manifest naming anything else is an error, not an ignored line: a typo like
// "swift_verison" would otherwise read as a permanent, invisible exemption,
// which is precisely the class of silent failure this package exists to end.
//
// "documentation" is the one key here that Check does NOT emit a finding for,
// and that is deliberate. Every other key maps to an Xcode build setting this
// package reads out of the pbxproj; "every declaration carries a doc comment and
// the docs build clean" is not a build setting, so there is nothing here to
// read. It is enforced by the stack's `Docs` CI job — which applies the same
// expiry rule, via scripts/docs-relaxation.sh — and it is listed here so that a
// project writing the relaxation the standard tells it to write does not have
// its manifest rejected at load time.
func KnownKeys() []string {
	return []string{"swift_version", "warnings_as_errors", "strict_concurrency", "documentation"}
}

// ValidKey reports whether key is one Check knows about.
func ValidKey(key string) bool {
	for _, k := range KnownKeys() {
		if k == key {
			return true
		}
	}
	return false
}

// Finding is the result of checking one baseline key against a project.
type Finding struct {
	Key         string // "swift_version", "warnings_as_errors", "strict_concurrency"
	Setting     string // the Xcode build setting behind it
	Want, Got   string // expected value, and what the project actually resolves to
	Have, Total int    // Swift-compiling configs satisfying it, out of how many
	Status      Status
	Relax       *Relax // set whenever the manifest carries one, even if unneeded
}

// Ratio renders coverage for a report line.
func (f Finding) Ratio() string { return fmt.Sprintf("%d/%d", f.Have, f.Total) }

// Blocks reports whether this finding should fail a run. A relaxation that has
// not expired does not block; one that has expired does, so time-boxed debt
// cannot quietly become permanent policy.
func (f Finding) Blocks() bool {
	return f.Status == StatusViolation || f.Status == StatusExpired
}

// Violations returns the findings that should fail a run.
func Violations(fs []Finding) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Blocks() {
			out = append(out, f)
		}
	}
	return out
}

// Check compares a project's declared settings against the asserted standard.
//
// now is a parameter rather than a call to time.Now so relaxation expiry is
// deterministic under test.
//
// A project with no Swift-compiling configurations yields no findings at all —
// there is nothing to enforce, and emitting a wall of violations for a
// non-Swift component would be noise.
func Check(spec Spec, d Declared, relax map[string]Relax, now time.Time) []Finding {
	total := len(d.SwiftConfigs())
	if total == 0 {
		return nil
	}

	var fs []Finding

	if spec.SwiftVersion != "" {
		// Minimum, not exact — see versionAtLeast.
		fs = append(fs, coverageBy(d, "swift_version", "SWIFT_VERSION", spec.SwiftVersion, total,
			func(got string) bool { return versionAtLeast(got, spec.SwiftVersion) }))
	}
	if spec.WarningsAsErrors {
		fs = append(fs, coverage(d, "warnings_as_errors", "SWIFT_TREAT_WARNINGS_AS_ERRORS", "YES", total))
	}
	if spec.StrictConcurrency != "" {
		f := coverage(d, "strict_concurrency", "SWIFT_STRICT_CONCURRENCY", spec.StrictConcurrency, total)
		// Swift 6 language mode already enforces complete strict concurrency, so
		// requiring the explicit setting there would nag a compliant project into
		// writing a no-op. Only meaningful below Swift 6.
		if allAtLeastSwift6(d) {
			f.Status, f.Got = StatusImplied, "implied by Swift 6 language mode"
		}
		fs = append(fs, f)
	}

	for i := range fs {
		applyRelax(&fs[i], relax, now)
	}
	return fs
}

// coverage builds the finding for one key from the project's declared settings,
// treating the wanted value as an exact match.
func coverage(d Declared, key, setting, want string, total int) Finding {
	return coverageBy(d, key, setting, want, total, func(got string) bool { return got == want })
}

// coverageBy is coverage with an explicit satisfaction test.
func coverageBy(d Declared, key, setting, want string, total int, ok func(string) bool) Finding {
	have, _ := d.CoverageBy(setting, ok)
	f := Finding{
		Key: key, Setting: setting, Want: want,
		Have: have, Total: total,
		Status: StatusViolation,
	}
	if have == total {
		f.Status = StatusOK
	}
	f.Got = observed(d, setting)
	return f
}

// observed summarizes what the project actually resolves a setting to, for the
// "got" half of a report line. Distinct values are joined so a split project
// reads as `5.0, 6` rather than an arbitrary winner.
func observed(d Declared, setting string) string {
	seen := map[string]bool{}
	var vals []string
	for _, c := range d.SwiftConfigs() {
		v, ok := d.Effective(c, setting)
		if !ok {
			v = "<unset>"
		}
		if !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	return strings.Join(vals, ", ")
}

// applyRelax folds a manifest relaxation into a finding. The relaxation is
// attached whenever one exists — including on an already-compliant key, so a
// stale relaxation can be reported and removed rather than lingering as
// permanent-looking debt.
func applyRelax(f *Finding, relax map[string]Relax, now time.Time) {
	r, ok := relax[f.Key]
	if !ok {
		return
	}
	f.Relax = &r
	if f.Status != StatusViolation {
		return // nothing to excuse
	}
	until, err := time.Parse("2006-01-02", r.Until)
	if err != nil {
		return // malformed dates are rejected at config load; fail closed here
	}
	// Valid through the end of the named day.
	if now.After(until.AddDate(0, 0, 1).Add(-time.Nanosecond)) {
		f.Status = StatusExpired
		return
	}
	f.Status = StatusRelaxed
}

// allAtLeastSwift6 reports whether every Swift-compiling configuration resolves
// to a language mode of 6 or newer.
func allAtLeastSwift6(d Declared) bool {
	swift := d.SwiftConfigs()
	if len(swift) == 0 {
		return false
	}
	for _, c := range swift {
		v, ok := d.Effective(c, "SWIFT_VERSION")
		if !ok || swiftMajor(v) < 6 {
			return false
		}
	}
	return true
}

// versionAtLeast reports whether a declared dotted version is at least the
// wanted one, comparing component-wise and treating an absent component as 0.
// So `6.0` satisfies `6`, `6.2` satisfies `6`, and `5.9` does not — which is
// what "the baseline asserts a minimum language mode" has always meant, and what
// the CI Baseline job already did.
//
// A non-numeric component makes the whole value fail closed: an unparseable
// SWIFT_VERSION is not evidence of compliance.
func versionAtLeast(got, want string) bool {
	gp, wp := strings.Split(strings.TrimSpace(got), "."), strings.Split(strings.TrimSpace(want), ".")
	for i := 0; i < len(gp) || i < len(wp); i++ {
		g, w := 0, 0
		if i < len(gp) {
			n, err := strconv.Atoi(gp[i])
			if err != nil {
				return false
			}
			g = n
		}
		if i < len(wp) {
			n, err := strconv.Atoi(wp[i])
			if err != nil {
				return false
			}
			w = n
		}
		if g != w {
			return g > w
		}
	}
	return true
}

// swiftMajor parses the leading major component of a SWIFT_VERSION value ("6",
// "5.0", "6.2"), returning 0 when it is not a number.
func swiftMajor(v string) int {
	major, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}
