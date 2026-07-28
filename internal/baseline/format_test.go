package baseline

import (
	"strings"
	"testing"
)

// A clean project says so in one line rather than printing a table of OKs.
func TestFormatAllClean(t *testing.T) {
	d := declared(4, map[string]string{"SWIFT_VERSION": "6", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	out := Format("ios", Check(std, d, nil, now))
	if !strings.Contains(out, "baseline: ok") {
		t.Errorf("want a one-line ok summary, got:\n%s", out)
	}
}

// A violation must state the setting, the ratio, and what to do — a report that
// says only "non-compliant" gets ignored.
func TestFormatViolationIsActionable(t *testing.T) {
	d := Declared{Configs: []Config{
		{ID: "a", Name: "Debug", Settings: map[string]string{"SWIFT_VERSION": "6", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"}},
		{ID: "b", Name: "Debug", Settings: map[string]string{"SWIFT_VERSION": "6"}},
		{ID: "c", Name: "Debug", Settings: map[string]string{"SWIFT_VERSION": "6"}},
	}}
	out := Format("ios", Check(std, d, nil, now))
	for _, want := range []string{
		"warnings_as_errors",             // the key
		"SWIFT_TREAT_WARNINGS_AS_ERRORS", // the actual build setting to change
		"1/3",                            // the coverage ratio
		"1 violation",                    // the count
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q, got:\n%s", want, out)
		}
	}
}

// A relaxation must show its reason and expiry, so debt is legible in the report
// rather than merely absent from the failure list.
func TestFormatShowsRelaxationReasonAndExpiry(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "5.0", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	relax := map[string]Relax{"swift_version": {Until: "2026-09-01", Reason: "audio engine, #142"}}
	out := Format("ios", Check(std, d, relax, now))
	for _, want := range []string{"relaxed", "2026-09-01", "audio engine, #142"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q, got:\n%s", want, out)
		}
	}
}

func TestFormatExpiredRelaxationReadsAsExpired(t *testing.T) {
	d := declared(2, map[string]string{"SWIFT_VERSION": "5.0", "SWIFT_TREAT_WARNINGS_AS_ERRORS": "YES"})
	relax := map[string]Relax{"swift_version": {Until: "2026-07-01", Reason: "r"}}
	out := Format("ios", Check(std, d, relax, now))
	if !strings.Contains(out, "expired") {
		t.Errorf("want an expired marker, got:\n%s", out)
	}
}

// No findings at all (a component with no Swift) prints nothing, so a web or
// supabase component does not emit an empty baseline section.
func TestFormatNoFindingsIsEmpty(t *testing.T) {
	if out := Format("web", nil); out != "" {
		t.Errorf("want empty output for no findings, got:\n%s", out)
	}
}
