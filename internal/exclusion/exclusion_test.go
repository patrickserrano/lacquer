package exclusion

import (
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func only(t *testing.T, fs []Finding) Finding {
	t.Helper()
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(fs), fs)
	}
	return fs[0]
}

// The bare-string form is what every manifest in the fleet used, so it must keep
// working — but it must also stop being invisible.
func TestBareStringIsUnattributed(t *testing.T) {
	f := only(t, Review(
		[]config.Exclusion{{Path: "typedoc.json"}},
		[]string{"typedoc.json"},
		day("2026-08-09"),
	))
	if f.Status != StatusUnattributed {
		t.Errorf("status = %q, want unattributed", f.Status)
	}
	if f.Blocks() {
		t.Error("an unattributed exclusion must not block — gating on it would break every existing manifest for a documentation defect")
	}
	if !strings.Contains(Format([]Finding{f}), "typedoc.json") {
		t.Error("an unattributed exclusion must be reported; being unreported is the whole defect")
	}
}

// An attributed, undated exclusion is a deliberate permanent divergence
// (windsock is macOS-only and will never adopt the iOS-simulator workflow).
// Reported only if it goes stale — otherwise silence.
func TestPermanentIsHealthyAndSilent(t *testing.T) {
	f := only(t, Review(
		[]config.Exclusion{{Path: ".github/workflows/ios-ci.yml", Reason: "macOS-only app; ios-ci hardcodes simulator destinations"}},
		[]string{".github/workflows/ios-ci.yml"},
		day("2026-08-09"),
	))
	if f.Status != StatusPermanent {
		t.Errorf("status = %q, want permanent", f.Status)
	}
	if f.Blocks() {
		t.Error("a permanent divergence must not block")
	}
	if got := Format([]Finding{f}); got != "" {
		t.Errorf("a healthy exclusion should print nothing, got %q", got)
	}
}

// A dated exclusion inside its term is visible debt that does not yet gate.
func TestDatedInTermDoesNotBlock(t *testing.T) {
	f := only(t, Review(
		[]config.Exclusion{{Path: "ios/.swiftlint.yml", Reason: "local fixes pending upstream", Until: "2026-12-01"}},
		[]string{"ios/.swiftlint.yml"},
		day("2026-08-09"),
	))
	if f.Status != StatusDated {
		t.Errorf("status = %q, want dated", f.Status)
	}
	if f.Blocks() {
		t.Error("an in-term exclusion must not block")
	}
}

// The boundary must match [baseline.relax] exactly: valid THROUGH the final day.
// A one-day disagreement between the two exemption mechanisms would be the kind
// of thing nobody notices until it fails a release on the wrong afternoon.
func TestValidOnExpiryDayExpiredTheNext(t *testing.T) {
	e := []config.Exclusion{{Path: "x.yml", Reason: "r", Until: "2026-08-09"}}
	if got := only(t, Review(e, []string{"x.yml"}, day("2026-08-09"))).Status; got != StatusDated {
		t.Errorf("on the expiry date status = %q, want dated (valid through its final day)", got)
	}
	if got := only(t, Review(e, []string{"x.yml"}, day("2026-08-10"))).Status; got != StatusExpired {
		t.Errorf("the day after status = %q, want expired", got)
	}
}

// The whole point: a time-boxed exemption whose term ran out must gate.
func TestExpiredBlocks(t *testing.T) {
	f := only(t, Review(
		[]config.Exclusion{{Path: "x.yml", Reason: "pending secrets", Until: "2026-01-01"}},
		[]string{"x.yml"},
		day("2026-08-09"),
	))
	if f.Status != StatusExpired {
		t.Fatalf("status = %q, want expired", f.Status)
	}
	if !f.Blocks() || Blocking([]Finding{f}) != 1 {
		t.Error("an expired exclusion must block, or a time-boxed opt-out is just a permanent one with extra words")
	}
	if !strings.Contains(Format([]Finding{f}), "EXPIRED") {
		t.Error("the report must name the expiry")
	}
}

// An exclusion that suppresses nothing is dead text that still reads as a live
// decision. Orthogonal to attribution: this one is perfectly documented.
func TestStaleExclusionIsReported(t *testing.T) {
	f := only(t, Review(
		[]config.Exclusion{{Path: ".github/workflows/retired.yml", Reason: "well documented"}},
		[]string{".github/workflows/ios-ci.yml"}, // the lacquer ships this, not retired.yml
		day("2026-08-09"),
	))
	if !f.Stale {
		t.Fatal("an exclusion matching nothing the lacquer ships is stale")
	}
	if f.Blocks() {
		t.Error("a stale exclusion must not block; it is dead config, not a live risk")
	}
	if !strings.Contains(Format([]Finding{f}), "suppresses nothing") {
		t.Error("staleness must be reported so the entry can be deleted")
	}
}

// A directory-shaped exclusion covers everything beneath it, so it is live as
// long as the lacquer ships anything under that prefix.
func TestPrefixExclusionIsNotStale(t *testing.T) {
	f := only(t, Review(
		[]config.Exclusion{{Path: ".github/workflows/", Reason: "hand-tuned CI"}},
		[]string{".github/workflows/web-ci.yml"},
		day("2026-08-09"),
	))
	if f.Stale {
		t.Error("a prefix exclusion suppressing a file beneath it is not stale")
	}
}

// Both defects at once must both be reported: fixing the reason alone would
// leave a documented exclusion that still does nothing.
func TestUnattributedAndStaleReportBoth(t *testing.T) {
	out := Format(Review(
		[]config.Exclusion{{Path: "gone.yml"}},
		nil,
		day("2026-08-09"),
	))
	if !strings.Contains(out, "no reason recorded") || !strings.Contains(out, "suppresses nothing") {
		t.Errorf("both defects must be reported, got %q", out)
	}
}
