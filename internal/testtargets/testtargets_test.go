package testtargets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pbx = `// !$*UTF8*$!
{
	objects = {
		AAA /* App */ = {
			isa = PBXNativeTarget;
			name = DailyBread;
			productType = "com.apple.product-type.application";
		};
		BBB /* Tests */ = {
			isa = PBXNativeTarget;
			name = DailyBreadTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
		CCC /* Widgets */ = {
			isa = PBXNativeTarget;
			name = DailyBreadWidgetsTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
		DDD /* Watch */ = {
			isa = PBXNativeTarget;
			name = "DailyBreadWatchApp Watch AppTests";
			productType = "com.apple.product-type.bundle.unit-test";
		};
		EEE /* WatchUI */ = {
			isa = PBXNativeTarget;
			name = "DailyBreadWatchApp Watch AppUITests";
			productType = "com.apple.product-type.bundle.ui-testing";
		};
	};
}
`

func parseFixture(t *testing.T) []Target {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "project.pbxproj")
	if err := os.WriteFile(p, []byte(pbx), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Only test bundles, and the app target is not one. A quoted name with spaces is
// not exotic — "DailyBreadWatchApp Watch AppTests" is real.
func TestParseFindsTestBundlesOnly(t *testing.T) {
	got := parseFixture(t)
	if len(got) != 4 {
		t.Fatalf("got %d targets, want 4: %+v", len(got), got)
	}
	for _, x := range got {
		if x.Name == "DailyBread" {
			t.Error("the application target was reported as a test bundle")
		}
	}
	var ui int
	for _, x := range got {
		if x.UI {
			ui++
		}
	}
	if ui != 1 {
		t.Errorf("got %d UI bundles, want 1", ui)
	}
	// Searched rather than indexed: Parse sorts, so position is not the property
	// under test. The property is that a QUOTED name with spaces survives at all.
	var quoted bool
	for _, x := range got {
		if x.Name == "DailyBreadWatchApp Watch AppTests" {
			quoted = true
		}
	}
	if !quoted {
		t.Errorf("a quoted name with spaces did not survive parsing: %+v", got)
	}
}

// dailybread's real shape: three suites the manifest never named. Each ran
// nowhere while CI stayed green, because xcodebuild says nothing about a target
// it was not asked to run.
func TestUncoveredTargetsAreReported(t *testing.T) {
	r := Compare(parseFixture(t), []string{"DailyBreadTests"})
	if len(r.Uncovered) != 3 {
		t.Fatalf("got %d uncovered, want 3: %+v", len(r.Uncovered), r.Uncovered)
	}
	out := Format(r)
	if !strings.Contains(out, "run nowhere") {
		t.Errorf("report does not say what is at stake:\n%s", out)
	}
	// Deleting an uncovered target is a correct resolution. One real uncovered
	// suite in this fleet asserted XCTAssertTrue(true) and queried UI that had
	// been removed; a report implying wiring is the only fix would send someone
	// to rehabilitate it.
	if !strings.Contains(out, "DELETE IT") {
		t.Errorf("report does not offer removal as a resolution:\n%s", out)
	}
}

// steps' real shape: `test_target` derived from the product's DISPLAY name
// ("Steps Lite") produced `Steps LiteTests`, which exists nowhere. 91 unit tests
// were selected by a name matching nothing, and xcodebuild exited 0.
func TestSelectorsNamingNothingAreReported(t *testing.T) {
	r := Compare(parseFixture(t), []string{"DailyBreadTests", "Steps LiteTests"})
	if len(r.Missing) != 1 || r.Missing[0] != "Steps LiteTests" {
		t.Fatalf("missing = %+v, want [Steps LiteTests]", r.Missing)
	}
	out := Format(r)
	if !strings.Contains(out, "EXITS 0") {
		t.Errorf("report does not explain why this is silent:\n%s", out)
	}
	if !strings.Contains(out, "display label") {
		t.Errorf("report does not name the usual cause (a product name is not a target name):\n%s", out)
	}
}

// The permissive half. A fully wired project must produce nothing, or the check
// is noise everyone learns to skip.
func TestFullyCoveredProjectIsSilent(t *testing.T) {
	all := parseFixture(t)
	var sel []string
	for _, x := range all {
		sel = append(sel, x.Name)
	}
	r := Compare(all, sel)
	if len(r.Uncovered) != 0 || len(r.Missing) != 0 {
		t.Fatalf("a fully wired project produced findings: %+v", r)
	}
	if Format(r) != "" {
		t.Errorf("Format was not empty for a clean project:\n%s", Format(r))
	}
}

// A project with no Xcode project at all — a Swift package, a web component —
// must not error. audit runs against those.
func TestMissingProjectIsNotAnError(t *testing.T) {
	got, read, err := Parse(filepath.Join(t.TempDir(), "nope.pbxproj"))
	if err != nil {
		t.Fatalf("a missing pbxproj errored: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
	if read {
		t.Error("Parse claimed it READ a project that does not exist — the caller uses that " +
			"to decide whether comparing is meaningful at all, and a false yes makes every " +
			"selector look like it names a missing target")
	}
}

// Matching is exact, because that is how -only-testing: matches. A near-miss is
// a miss, and reporting it as covered would recreate the silence.
func TestMatchingIsExact(t *testing.T) {
	r := Compare(parseFixture(t), []string{"dailybreadtests"})
	if len(r.Missing) != 1 {
		t.Error("a case-differing selector was treated as matching")
	}
	var found bool
	for _, u := range r.Uncovered {
		if u.Name == "DailyBreadTests" {
			found = true
		}
	}
	if !found {
		t.Error("the real target was treated as covered by a case-differing selector")
	}
}
