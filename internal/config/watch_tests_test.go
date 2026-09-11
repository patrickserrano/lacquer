package config

import (
	"strings"
	"testing"
)

// watchBase is the shape the project this was built for is in: one app, no
// [[product]] block, and a watch test bundle that no [[product]] field could
// reach before this.
const watchBase = "[project]\nname = \"dailybread\"\nproject_name = \"DailyBread\"\nscheme = \"DailyBread\"\nbundle_id = \"com.x.db\"\nasc_app_id = \"1\"\n"

const goodProjectWatch = watchBase + `
[project.watch_tests]
scheme = "DailyBreadWatchApp Watch App"
test_target = "DailyBreadWatchApp Watch AppTests"
`

func TestProjectWatchTestsLoadAndFoldIntoTheSynthesisedProduct(t *testing.T) {
	cfg, err := loadString(t, goodProjectWatch)
	if err != nil {
		t.Fatal(err)
	}
	// The single-product spelling has to reach Products(), because every
	// renderer and the audit read the product. A field that decoded but never
	// folded would be a manifest key that changes nothing — silently.
	p := cfg.Products()[0]
	if p.WatchTests == nil {
		t.Fatal("[project].watch_tests did not fold into the synthesised product")
	}
	if p.WatchTests.Scheme != "DailyBreadWatchApp Watch App" {
		t.Errorf("scheme = %q; a quoted name with spaces is the normal case for a watch scheme", p.WatchTests.Scheme)
	}
	if got := p.WatchTestSelectors(); len(got) != 1 || got[0] != "DailyBreadWatchApp Watch AppTests" {
		t.Errorf("WatchTestSelectors() = %v, want the one watch selector", got)
	}
	// Kept OUT of TestSelectors: that list feeds the iOS leg's -only-testing:
	// arguments and its "Verify Test Selectors Matched" step, where a watch
	// bundle fails the job outright.
	for _, s := range p.TestSelectors() {
		if s == "DailyBreadWatchApp Watch AppTests" {
			t.Error("the watch target leaked into TestSelectors(); the iOS leg would pass it to -only-testing: and fail with \"isn't a member of the specified test plan or scheme\"")
		}
	}
}

func TestProductWatchTestsLoad(t *testing.T) {
	cfg, err := loadString(t, watchBase+`
[[product]]
name = "Paid"
scheme = "DailyBread"
bundle_id = "com.x.paid"
asc_app_id = "111"

[product.watch_tests]
scheme = "DailyBreadWatchApp Watch App"
test_target = "DailyBreadWatchApp Watch AppTests"
platform = "watchOS"
`)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Products()[0]
	if p.WatchTests == nil || p.WatchTests.TestTarget != "DailyBreadWatchApp Watch AppTests" {
		t.Fatalf("[product.watch_tests] did not decode: %+v", p.WatchTests)
	}
	sim, ok := p.WatchTests.Simulator()
	if !ok {
		t.Fatal("the default platform does not resolve to a simulator")
	}
	if sim.DestinationPrefix != "platform=watchOS Simulator" {
		t.Errorf("destination prefix = %q, want the watchOS simulator destination", sim.DestinationPrefix)
	}
}

// The platform is an ENUM, not a free-form -destination string, because the
// value is spliced into the rendered job's shell. These are the shapes a
// manifest would use to get something else in there.
func TestWatchTestsRejectsAnythingButAKnownPlatform(t *testing.T) {
	body := func(platform string) string {
		return watchBase + "\n[project.watch_tests]\nscheme = \"W App\"\ntest_target = \"W AppTests\"\nplatform = \"" + platform + "\"\n"
	}
	for _, tc := range []struct{ name, platform string }{
		{"a platform with no measured device type", "tvOS"},
		{"a whole destination string", "watchOS Simulator,id=$(id)"},
		{"command substitution", "watchOS; rm -rf /"},
		{"a runner label", "self-hosted, macOS"},
		{"wrong case", "watchos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, body(tc.platform))
			if err == nil {
				t.Fatalf("platform %q was accepted; anything not in the table reaches the runner as text nobody measured", tc.platform)
			}
			// The reader has to be told what IS legal, or the only way forward
			// is to guess.
			if !strings.Contains(err.Error(), "watchOS") {
				t.Errorf("the error does not name the legal platforms: %v", err)
			}
		})
	}
}

func TestWatchTestsRequiresBothHalvesOfTheTriple(t *testing.T) {
	for _, tc := range []struct{ name, toml, want string }{
		{
			// Blank is not "derive it from the product's scheme". Deriving would
			// name the iOS scheme, where the bundle is not a testable — the
			// exact failure this table exists to route around.
			"no scheme",
			watchBase + "\n[project.watch_tests]\ntest_target = \"W AppTests\"\n",
			"watch_tests.scheme",
		},
		{
			// And blank is not "<scheme>Tests" either: the real pair is
			// ("DailyBreadWatchApp Watch App", "DailyBreadWatchApp Watch
			// AppTests"), so a derivation would produce a selector matching
			// nothing — which xcodebuild reports as a pass.
			"no test_target",
			watchBase + "\n[project.watch_tests]\nscheme = \"W App\"\n",
			"watch_tests.test_target",
		},
		{
			"a scheme the shell would reparse",
			watchBase + "\n[project.watch_tests]\nscheme = \"W App\\\"; id #\"\ntest_target = \"W AppTests\"\n",
			"watch_tests.scheme",
		},
		{
			"a target the shell would reparse",
			watchBase + "\n[project.watch_tests]\nscheme = \"W App\"\ntest_target = \"$(id)\"\n",
			"watch_tests.test_target",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, tc.toml)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name the field %q: %v", tc.want, err)
			}
		})
	}
}

// Naming the watch target on BOTH legs is rejected rather than merged. The iOS
// leg fails outright on it, so a manifest that says both is a reader who
// believes one of the two runs it.
func TestWatchTargetCannotAlsoBeAnIOSSelector(t *testing.T) {
	_, err := loadString(t, watchBase+`
extra_test_targets = ["DailyBreadWatchApp Watch AppTests"]

[project.watch_tests]
scheme = "DailyBreadWatchApp Watch App"
test_target = "DailyBreadWatchApp Watch AppTests"
`)
	if err == nil {
		t.Fatal("the watch target was accepted as an iOS -only-testing: selector as well; that selector fails the Test job with \"isn't a member of the specified test plan or scheme\"")
	}
	if !strings.Contains(err.Error(), "iOS test leg") {
		t.Errorf("the error does not explain which leg cannot run it: %v", err)
	}
}

// Same rule as [project].extra_test_targets: the single-product spelling
// alongside declared products would have to be GUESSED at.
func TestProjectWatchTestsRejectedAlongsideProducts(t *testing.T) {
	_, err := loadString(t, watchBase+`
[project.watch_tests]
scheme = "W App"
test_target = "W AppTests"

[[product]]
name = "Paid"
scheme = "DailyBread"
bundle_id = "com.x.paid"
asc_app_id = "111"
tag_prefix = "paid"

[[product]]
name = "Free"
scheme = "DailyBread Free"
bundle_id = "com.x.free"
asc_app_id = "222"
tag_prefix = "free"
`)
	if err == nil {
		t.Fatal("[project].watch_tests was accepted alongside [[product]] blocks; which product the watch bundle belongs to would have to be guessed")
	}
	if !strings.Contains(err.Error(), "single-product spelling") {
		t.Errorf("the error does not point at the [[product]] spelling: %v", err)
	}
}

// The unknown-key guard is derived by reflection from the Go types, so a nested
// table has to be reachable by it too — otherwise a typo inside watch_tests is
// a silently ignored key, and the project believes it declared a watch job it
// did not.
func TestWatchTestsRejectsAnUnknownKey(t *testing.T) {
	_, err := loadString(t, watchBase+"\n[project.watch_tests]\nscheme = \"W App\"\ntest_target = \"W AppTests\"\ndestination = \"platform=watchOS Simulator\"\n")
	if err == nil {
		t.Fatal("an unknown key inside [project.watch_tests] was accepted")
	}
	if !strings.Contains(err.Error(), "destination") {
		t.Errorf("the error does not name the unknown key: %v", err)
	}
}

// A Config with no watch_tests must behave exactly as it did: nil everywhere,
// no selectors, nothing to render.
func TestNoWatchTestsIsNil(t *testing.T) {
	cfg, err := loadString(t, watchBase)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Products()[0].WatchTests != nil {
		t.Error("a manifest with no watch_tests produced a non-nil WatchTests")
	}
	if got := cfg.Products()[0].WatchTestSelectors(); got != nil {
		t.Errorf("WatchTestSelectors() = %v, want nil", got)
	}
}

// Every platform in the table has to carry all six facts. An entry missing one
// renders a job with a blank device type, a blank destination or no readiness
// signal — and xcodebuild treats a partial -destination as a destination it may
// choose for you, which is the silent-wrong-target failure this whole feature is
// trying to stop producing.
func TestEverySimulatorPlatformIsComplete(t *testing.T) {
	if len(SimulatorPlatforms) == 0 {
		t.Fatal("the platform table is empty; this test asserts nothing")
	}
	for name, s := range SimulatorPlatforms {
		for _, f := range []struct{ field, val string }{
			{"DestinationPrefix", s.DestinationPrefix},
			{"Runtime", s.Runtime},
			{"DownloadPlatform", s.DownloadPlatform},
			{"DeviceType", s.DeviceType},
			{"ReadyService", s.ReadyService},
			{"SimPrefix", s.SimPrefix},
		} {
			if f.val == "" {
				t.Errorf("platform %q has an empty %s", name, f.field)
			}
		}
		if !strings.HasPrefix(s.DestinationPrefix, "platform=") {
			t.Errorf("platform %q destination prefix %q is not an xcodebuild destination", name, s.DestinationPrefix)
		}
		// The device id is appended by the job, so the prefix must not already
		// carry one: "id=" twice is not an error to xcodebuild.
		if strings.Contains(s.DestinationPrefix, "id=") {
			t.Errorf("platform %q destination prefix %q already names a device; the job appends ,id=$DEVICE_ID", name, s.DestinationPrefix)
		}
	}
}
