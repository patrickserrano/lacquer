package testtargets

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeSpec writes body as project.yml in a fresh directory and returns its path.
func writeSpec(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "project.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustSpec(t *testing.T, path string) Project {
	t.Helper()
	p, read, err := ParseSpec(path)
	if err != nil || !read {
		t.Fatalf("ParseSpec(%s) = read %v, err %v", path, read, err)
	}
	return p
}

func watchBundles(p Project) []string {
	var out []string
	for _, t := range p.Targets {
		if IsWatchBundle(t) {
			out = append(out, t.Name)
		}
	}
	return out
}

func appNames(apps []App) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.Name)
	}
	return out
}

// The committed XcodeGen-only fixture: an iOS app, a widget, a watch app, and a
// test bundle for each app.
func TestParseSpecReadsTheWatchFixture(t *testing.T) {
	p := mustSpec(t, filepath.Join("..", "shipped", "testdata", "projects", "watchapp", "project.yml"))

	if got := appNames(p.WatchApps()); !reflect.DeepEqual(got, []string{"Watchapp Watch App"}) {
		t.Errorf("WatchApps = %v, want [Watchapp Watch App]", got)
	}
	if got := watchBundles(p); !reflect.DeepEqual(got, []string{"Watchapp Watch AppTests"}) {
		t.Errorf("watch bundles = %v, want [Watchapp Watch AppTests]", got)
	}
	var names []string
	for _, tg := range p.Targets {
		names = append(names, tg.Name)
		if tg.Name == "WatchappTests" && tg.Platform != "" {
			t.Errorf("the iOS test bundle reads as platform %q", tg.Platform)
		}
	}
	if !reflect.DeepEqual(names, []string{"Watchapp Watch AppTests", "WatchappTests"}) {
		t.Errorf("test targets = %v", names)
	}
	if got := p.SchemesTesting("Watchapp Watch AppTests"); !reflect.DeepEqual(got, []string{"Watchapp Watch App"}) {
		t.Errorf("schemes testing the watch bundle = %v, want [Watchapp Watch App]", got)
	}
}

// THE CONTROL. A widget is an app-extension that runs on the iPhone simulator;
// a project with one and no watch app has no watch target of any kind, and a
// detector that said otherwise would put a watch finding on most of the fleet.
func TestParseSpecWidgetOnlySpecHasNoWatchTargets(t *testing.T) {
	p := mustSpec(t, writeSpec(t, `
name: Widgety
targets:
  Widgety:
    type: application
    platform: iOS
  WidgetyTests:
    type: bundle.unit-test
    platform: iOS
  WidgetyWidget:
    type: app-extension
    platform: iOS
  WidgetyWidgetTests:
    type: bundle.unit-test
    platform: iOS
`))
	if w := p.WatchApps(); len(w) != 0 {
		t.Errorf("WatchApps = %v, want none", w)
	}
	if w := watchBundles(p); len(w) != 0 {
		t.Errorf("watch bundles = %v, want none", w)
	}
	if got := appNames(p.Apps); !reflect.DeepEqual(got, []string{"Widgety"}) {
		t.Errorf("Apps = %v, want only the application; an extension is not one", got)
	}
}

// platform is the whole signal. An application is not a watch app because of
// its name, and a watchOS one is not an iOS app because nothing said otherwise.
func TestParseSpecReadsPlatformNotNames(t *testing.T) {
	p := mustSpec(t, writeSpec(t, `
targets:
  Wrist Watch App:
    type: application
    platform: iOS
  Wrist Watch AppTests:
    type: bundle.unit-test
    platform: iOS
  Companion:
    type: application
    platform: watchOS
  CompanionChecks:
    type: bundle.unit-test
    platform: watchOS
`))
	if got := appNames(p.WatchApps()); !reflect.DeepEqual(got, []string{"Companion"}) {
		t.Errorf("WatchApps = %v, want [Companion]", got)
	}
	if got := watchBundles(p); !reflect.DeepEqual(got, []string{"CompanionChecks"}) {
		t.Errorf("watch bundles = %v, want [CompanionChecks]", got)
	}
}

// A watch UI-testing bundle is not what [product.watch_tests] runs, so it is not
// a watch bundle here; it stays in the ordinary uncovered report.
func TestWatchUITestBundleIsNotAWatchBundle(t *testing.T) {
	p := mustSpec(t, writeSpec(t, `
targets:
  W:
    type: application
    platform: watchOS
  WUITests:
    type: bundle.ui-testing
    platform: watchOS
`))
	if w := watchBundles(p); len(w) != 0 {
		t.Errorf("watch bundles = %v, want none: a UI bundle is not run by watch_tests", w)
	}
	if len(p.Targets) != 1 || !p.Targets[0].UI || p.Targets[0].Platform != WatchOS {
		t.Errorf("Targets = %+v, want the UI bundle recorded as a watchOS UI target", p.Targets)
	}
}

// What XcodeGen generates from a multi-platform target, a template, an include,
// and supportedDestinations, each of which can be the only place a target's
// platform is written.
func TestParseSpecFollowsXcodeGenIndirection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "watch.yml"), []byte(`
targets:
  Included Watch AppTests:
    type: bundle.unit-test
    platform: watchOS
`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "project.yml")
	if err := os.WriteFile(spec, []byte(`
include:
  - path: watch.yml
targetTemplates:
  WatchTests:
    type: bundle.unit-test
    platform: watchOS
targets:
  Shared:
    type: bundle.unit-test
    platform: [iOS, watchOS]
  Templated:
    templates: [WatchTests]
  Destined:
    type: bundle.unit-test
    supportedDestinations: [watchOS]
  Everywhere:
    type: bundle.unit-test
    supportedDestinations: [iOS, watchOS]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := watchBundles(mustSpec(t, spec))
	want := []string{"Destined", "Included Watch AppTests", "Shared_watchOS", "Templated"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("watch bundles = %v, want %v", got, want)
	}
}

func TestParseSpecAbsentIsNotRead(t *testing.T) {
	_, read, err := ParseSpec(filepath.Join(t.TempDir(), "project.yml"))
	if err != nil || read {
		t.Errorf("absent spec: read %v, err %v; want not read, no error", read, err)
	}
}

// phoneSpec is watchPbxproj (below) as an XcodeGen spec.
const phoneSpec = `
name: Phone
targets:
  Phone:
    type: application
    platform: iOS
  Phone Watch App:
    type: application
    platform: watchOS
  PhoneTests:
    type: bundle.unit-test
    platform: iOS
  Phone Watch AppTests:
    type: bundle.unit-test
    platform: watchOS
  Container:
    type: application.watchapp2-container
    platform: iOS
`

// The two readers must agree on the same project, or which file the audit
// happened to read would decide its findings.
func TestSpecParseMatchesPbxprojParseOnTheSameProject(t *testing.T) {
	fromPbx := parseWatchPbxproj(t)
	fromSpec := mustSpec(t, writeSpec(t, phoneSpec))
	if len(fromPbx.Targets) == 0 || len(fromPbx.Apps) == 0 {
		t.Fatal("the pbxproj yielded no targets or apps; this comparison asserts nothing")
	}
	if !reflect.DeepEqual(fromPbx.Targets, fromSpec.Targets) {
		t.Errorf("test targets differ:\n pbxproj %+v\n spec    %+v", fromPbx.Targets, fromSpec.Targets)
	}
	if !sameApps(fromPbx.Apps, fromSpec.Apps) {
		t.Errorf("apps differ:\n pbxproj %+v\n spec    %+v", fromPbx.Apps, fromSpec.Apps)
	}
}

// sameApps compares app lists ignoring order: the pbxproj lists them in file
// order and the spec in name order.
func sameApps(a, b []App) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[App]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

// watchPbxproj is a trimmed pbxproj with an iOS app, a watch app and a test
// bundle for each, where only SDKROOT says which platform each is on, plus the
// iOS-side container stub a legacy watch app carries.
const watchPbxproj = `
/* Begin PBXNativeTarget section */
		A1 /* Phone */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L1;
			name = Phone;
			productType = "com.apple.product-type.application";
		};
		A2 /* Phone Watch App */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L2;
			name = "Phone Watch App";
			productType = "com.apple.product-type.application";
		};
		A3 /* PhoneTests */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L3;
			name = PhoneTests;
			productType = "com.apple.product-type.bundle.unit-test";
		};
		A4 /* Phone Watch AppTests */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L4;
			name = "Phone Watch AppTests";
			productType = "com.apple.product-type.bundle.unit-test";
		};
		A5 /* Container */ = {
			isa = PBXNativeTarget;
			buildConfigurationList = L5;
			name = Container;
			productType = "com.apple.product-type.application.watchapp2-container";
		};
/* End PBXNativeTarget section */
/* Begin PBXProject section */
		P1 /* Project object */ = {
			isa = PBXProject;
			buildConfigurationList = L0;
		};
/* End PBXProject section */
/* Begin XCConfigurationList section */
		L0 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C0 /* Debug */,
			);
		};
		L1 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C1 /* Debug */,
			);
		};
		L2 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C2 /* Debug */,
			);
		};
		L3 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C3 /* Debug */,
			);
		};
		L4 = {
			isa = XCConfigurationList;
			buildConfigurations = (
				C4 /* Debug */,
			);
		};
/* End XCConfigurationList section */
/* Begin XCBuildConfiguration section */
		C0 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SDKROOT = iphoneos;
			};
			name = Debug;
		};
		C1 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				PRODUCT_NAME = Phone;
			};
			name = Debug;
		};
		C2 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SDKROOT = watchos;
			};
			name = Debug;
		};
		C3 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				PRODUCT_NAME = PhoneTests;
			};
			name = Debug;
		};
		C4 /* Debug */ = {
			isa = XCBuildConfiguration;
			buildSettings = {
				SDKROOT = watchos;
			};
			name = Debug;
		};
/* End XCBuildConfiguration section */
`

const watchScheme = `<?xml version="1.0" encoding="UTF-8"?>
<Scheme>
   <TestAction>
      <Testables>
         <TestableReference skipped = "NO">
            <BuildableReference
               BuildableName = "Phone Watch AppTests.xctest"
               BlueprintName = "Phone Watch AppTests">
            </BuildableReference>
         </TestableReference>
      </Testables>
   </TestAction>
</Scheme>
`

// A tracked pbxproj gives the same answers from SDKROOT: the watch app and its
// bundle are watchOS, the phone's are not (they inherit iphoneos from the
// project), and the iOS-side container stub is not a watch app.
func TestParseProjectReadsWatchTargetsFromSDKROOT(t *testing.T) {
	p := parseWatchPbxproj(t)
	if got := appNames(p.WatchApps()); !reflect.DeepEqual(got, []string{"Phone Watch App"}) {
		t.Errorf("WatchApps = %v, want [Phone Watch App]", got)
	}
	if got := watchBundles(p); !reflect.DeepEqual(got, []string{"Phone Watch AppTests"}) {
		t.Errorf("watch bundles = %v, want [Phone Watch AppTests]", got)
	}
	for _, a := range p.Apps {
		if strings.Contains(a.Name, "Container") {
			t.Errorf("the watchapp2-container stub was read as an app: %+v", a)
		}
	}
	if got := p.SchemesTesting("Phone Watch AppTests"); !reflect.DeepEqual(got, []string{"Phone Watch App"}) {
		t.Errorf("schemes = %v, want [Phone Watch App]", got)
	}
}

// parseWatchPbxproj writes watchPbxproj and its shared scheme to a fresh
// Phone.xcodeproj and parses it.
func parseWatchPbxproj(t *testing.T) Project {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Phone.xcodeproj")
	schemes := filepath.Join(dir, "xcshareddata", "xcschemes")
	if err := os.MkdirAll(schemes, 0o755); err != nil {
		t.Fatal(err)
	}
	pbx := filepath.Join(dir, "project.pbxproj")
	if err := os.WriteFile(pbx, []byte(watchPbxproj), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(schemes, "Phone Watch App.xcscheme"), []byte(watchScheme), 0o644); err != nil {
		t.Fatal(err)
	}
	p, read, err := ParseProject(pbx)
	if err != nil || !read {
		t.Fatalf("ParseProject: read %v, err %v", read, err)
	}
	return p
}
