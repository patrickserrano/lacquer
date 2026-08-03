package baseline

import (
	"testing"
	"time"
)

var std = Spec{SwiftVersion: "6", WarningsAsErrors: true, StrictConcurrency: "complete"}

// now is fixed so expiry behavior is deterministic — Check takes the clock as an
// argument rather than reading it, so a relaxation's boundary is testable.
var now = time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)

// find returns the finding for a key, failing the test if absent.
func find(t *testing.T, fs []Finding, key string) Finding {
	t.Helper()
	for _, f := range fs {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("no finding for %q in %+v", key, fs)
	return Finding{}
}

// declared builds a Declared with n target configs, each carrying the given
// settings, so the tests can state coverage directly.
func declared(n int, settings map[string]string) Declared {
	var cs []Config
	for i := 0; i < n; i++ {
		s := map[string]string{}
		for k, v := range settings {
			s[k] = v
		}
		cs = append(cs, Config{ID: string(rune('a' + i)), Name: "Debug", Settings: s})
	}
	return Declared{Configs: cs}
}

func TestCheckFullyCompliant(t *testing.T) {
	d := declared(4, map[string]string{
		"SWIFT_VERSION":                  "6",
		"SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES",
	})
	fs := Check(std, d, nil, now)
	if v := Violations(fs); len(v) != 0 {
		t.Errorf("Violations = %+v, want none", v)
	}
	if got := find(t, fs, "swift_version").Status; got != StatusOK {
		t.Errorf("swift_version status = %q, want ok", got)
	}
}

// Strict concurrency is already enforced by Swift 6 language mode. Demanding the
// explicit setting on a compliant project would nag every project into writing a
// no-op, which is how a check earns a reputation for noise and gets deleted.
func TestCheckStrictConcurrencyImpliedBySwift6(t *testing.T) {
	d := declared(2, map[string]string{
		"SWIFT_VERSION":                  "6",
		"SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES",
	})
	fs := Check(std, d, nil, now)
	f := find(t, fs, "strict_concurrency")
	if f.Status != StatusImplied {
		t.Errorf("strict_concurrency status = %q, want implied", f.Status)
	}
	if len(Violations(fs)) != 0 {
		t.Error("an implied setting must not count as a violation")
	}
}

// Below Swift 6 the setting is load-bearing, so it is checked for real.
func TestCheckStrictConcurrencyRequiredBelowSwift6(t *testing.T) {
	d := declared(2, map[string]string{
		"SWIFT_VERSION":                  "5.0",
		"SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES",
	})
	fs := Check(std, d, nil, now)
	if got := find(t, fs, "strict_concurrency").Status; got != StatusViolation {
		t.Errorf("strict_concurrency status = %q, want violation on a Swift 5 project", got)
	}
}

// Partial coverage is a violation and must report the ratio — "4 of 12" is the
// actionable part of the message.
func TestCheckPartialCoverageReportsRatio(t *testing.T) {
	d := Declared{Configs: []Config{
		{ID: "a", Name: "Debug", Settings: map[string]string{"SWIFT_VERSION": "6", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"}},
		{ID: "b", Name: "Debug", Settings: map[string]string{"SWIFT_VERSION": "6"}},
		{ID: "c", Name: "Debug", Settings: map[string]string{"SWIFT_VERSION": "6"}},
	}}
	f := find(t, Check(std, d, nil, now), "warnings_as_errors")
	if f.Status != StatusViolation {
		t.Errorf("status = %q, want violation", f.Status)
	}
	if f.Have != 1 || f.Total != 3 {
		t.Errorf("coverage = %d/%d, want 1/3", f.Have, f.Total)
	}
}

func TestCheckWrongLanguageMode(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "5.0", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	f := find(t, Check(std, d, nil, now), "swift_version")
	if f.Status != StatusViolation {
		t.Errorf("status = %q, want violation", f.Status)
	}
	if f.Want != "6" || f.Got != "5.0" {
		t.Errorf("want/got = %q/%q, want 6/5.0", f.Want, f.Got)
	}
}

// A project declaring no Swift at all has nothing to enforce: no findings, not a
// pile of spurious violations.
func TestCheckNoSwiftConfigsYieldsNoFindings(t *testing.T) {
	d := Declared{Configs: []Config{
		{ID: "a", Name: "Debug", Settings: map[string]string{"ENABLE_TESTABILITY": "YES"}},
	}}
	if fs := Check(std, d, nil, now); len(fs) != 0 {
		t.Errorf("Check = %+v, want no findings", fs)
	}
}

// An unexpired relaxation converts a violation into visible, non-blocking debt.
func TestCheckRelaxationSuppressesViolation(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "5.0", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	relax := map[string]Relax{
		"swift_version": {Until: "2026-09-01", Reason: "pre-Swift-6 audio engine, tracked in #142"},
	}
	fs := Check(std, d, relax, now)
	f := find(t, fs, "swift_version")
	if f.Status != StatusRelaxed {
		t.Errorf("status = %q, want relaxed", f.Status)
	}
	if f.Relax == nil || f.Relax.Reason == "" {
		t.Error("a relaxed finding must carry its reason so the debt stays visible")
	}
	for _, v := range Violations(fs) {
		if v.Key == "swift_version" {
			t.Error("an unexpired relaxation must not block")
		}
	}
}

// An expired relaxation is a hard violation. Otherwise time-boxed debt silently
// becomes permanent policy, which is the failure this whole package exists to
// prevent.
func TestCheckExpiredRelaxationIsAViolation(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "5.0", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	relax := map[string]Relax{
		"swift_version": {Until: "2026-07-25", Reason: "tracked in #142"},
	}
	fs := Check(std, d, relax, now)
	if got := find(t, fs, "swift_version").Status; got != StatusExpired {
		t.Errorf("status = %q, want expired", got)
	}
	var blocked bool
	for _, v := range Violations(fs) {
		if v.Key == "swift_version" {
			blocked = true
		}
	}
	if !blocked {
		t.Error("an expired relaxation must block")
	}
}

// The expiry boundary: a relaxation is valid through its final day.
func TestCheckRelaxationValidOnExpiryDay(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "5.0", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	relax := map[string]Relax{"swift_version": {Until: "2026-07-26", Reason: "r"}}
	if got := find(t, Check(std, d, relax, now), "swift_version").Status; got != StatusRelaxed {
		t.Errorf("status = %q, want relaxed on the expiry date itself", got)
	}
}

// A relaxation for something already compliant is stale debt worth surfacing, but
// it is not a failure.
func TestCheckStaleRelaxationOnCompliantKey(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "6", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	relax := map[string]Relax{"swift_version": {Until: "2026-09-01", Reason: "r"}}
	fs := Check(std, d, relax, now)
	f := find(t, fs, "swift_version")
	if f.Status != StatusOK {
		t.Errorf("status = %q, want ok", f.Status)
	}
	if f.Relax == nil {
		t.Error("a stale relaxation should still be reported so it can be removed")
	}
	if len(Violations(fs)) != 0 {
		t.Error("a stale relaxation must not block")
	}
}

// The documentation baseline is relaxable like every other key, but unlike them
// it maps to no Xcode build setting — it is enforced by the stack's Docs CI job.
// So the manifest must ACCEPT the key (otherwise a project writing the
// relaxation the standard documents would fail to load) while Check stays silent
// about it (there is nothing in the pbxproj to read).
func TestDocumentationRelaxationIsAcceptedButNotChecked(t *testing.T) {
	if !ValidKey("documentation") {
		t.Fatal(`ValidKey("documentation") = false; a project could not write the relaxation the docs standard tells it to`)
	}

	d := declared(2, map[string]string{
		"SWIFT_VERSION":                  "6",
		"SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES",
	})
	relax := map[string]Relax{
		// Expired on purpose: if Check ever grew an opinion about this key, an
		// expired relaxation would surface here as a blocking finding.
		"documentation": {Until: "2020-01-01", Reason: "legacy module"},
	}
	for _, f := range Check(std, d, relax, now) {
		if f.Key == "documentation" {
			t.Errorf("Check emitted a finding for documentation (%+v); it has no build setting to read, and CI owns it", f)
		}
	}
}
