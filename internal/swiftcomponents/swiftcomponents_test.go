package swiftcomponents

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/testtargets"
)

// repo writes files into a fresh git repository. Paths listed in untracked are
// written but never added, so a test can prove the listing reads the working
// tree the way `git ls-files --others --exclude-standard` does.
func repo(t *testing.T, tracked map[string]string, untracked ...string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range tracked {
		write(p, body)
	}
	gittest.Init(t, dir, "-q")
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "fixture", "--allow-empty")
	for _, p := range untracked {
		write(p, "// untracked\n")
	}
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func manifest(comps ...config.Component) *config.Config {
	return &config.Config{Components: comps}
}

var (
	appIOS   = config.Component{Path: "ios", Profiles: []string{"ios"}}
	appRoot  = config.Component{Path: ".", Profiles: []string{"ios"}}
	toolsPkg = config.Component{Path: "tools", Stack: "ios"}
	webAdmin = config.Component{Path: "admin", Profiles: []string{"web"}, Stack: "web"}
)

func TestComponentsAreTheManifestsSwiftComponentsProfileFirst(t *testing.T) {
	got := Components(manifest(toolsPkg, webAdmin, appIOS))
	want := []Component{{Path: "ios", Profile: true}, {Path: "tools"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Components = %+v, want %+v (a web component is not Swift; the app comes first)", got, want)
	}
	if got := Components(manifest(webAdmin)); len(got) != 0 {
		t.Fatalf("a project with no ios component has no Swift components, got %+v", got)
	}
}

// The fail path. A Swift file under no declared component is listed, both
// committed and merely on disk: CI's checkout holds only committed files, but a
// local run must see the file someone is about to commit.
func TestStrayListsTrackedAndUntrackedSwiftOutsideEveryComponent(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/App/App.swift":             "",
		"tools/Lib/Package.swift":       "",
		"tools/Lib/Sources/Lib/K.swift": "",
		"Stray.swift":                   "",
		"README.md":                     "",
	}, "Loose/Untracked.swift")
	got, err := Stray(dir, manifest(appIOS, toolsPkg))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Loose/Untracked.swift", "Stray.swift"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Stray = %v, want %v", got, want)
	}
}

// A component path is a directory, not a string prefix: `ios` does not cover
// `ios-tools/`, and a package component must not swallow its neighbour.
func TestStrayTreatsAComponentAsADirectoryNotAPrefix(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/A.swift":       "",
		"ios-tools/B.swift": "",
	})
	got, err := Stray(dir, manifest(appIOS))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ios-tools/B.swift"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Stray = %v, want %v", got, want)
	}
}

func TestStrayIsEmptyWhenEverythingIsCovered(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		cfg   *config.Config
	}{
		"root layout covers the whole tree, local packages included": {
			map[string]string{"App/A.swift": "", "Packages/Lib/Sources/Lib/K.swift": "", "Top.swift": ""},
			manifest(appRoot),
		},
		"app plus a declared package component": {
			map[string]string{"ios/A.swift": "", "tools/Lib/Package.swift": "", "tools/Lib/Sources/Lib/K.swift": ""},
			manifest(appIOS, toolsPkg),
		},
		"a local package under the app component is the app's": {
			map[string]string{"ios/A.swift": "", "ios/Packages/Core/Sources/Core/C.swift": ""},
			manifest(appIOS),
		},
		"Swift outside every component of a project with no ios component is not this check's business": {
			map[string]string{"admin/x.swift": ""},
			manifest(webAdmin),
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Stray(repo(t, tc.files), tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("Stray = %v, want none", got)
			}
		})
	}
}

// Ignored files are not the repository's: a `.build/` checkout of a dependency
// is full of Swift nobody here wrote.
func TestStrayIgnoresGitIgnoredFiles(t *testing.T) {
	dir := repo(t, map[string]string{".gitignore": ".build/\n", "ios/A.swift": ""}, ".build/checkouts/dep/D.swift")
	got, err := Stray(dir, manifest(appIOS))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Stray = %v, want none: ignored files are not the repository's", got)
	}
}

// "Could not look" must never read as "found nothing": outside a git work
// tree the listing errors rather than returning an empty, passing answer.
func TestStrayFailsClosedOutsideAGitWorkTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Stray.swift"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Stray(dir, manifest(appIOS)); !errors.Is(err, ErrCannotList) {
		t.Fatalf("Stray outside a git work tree returned %v, want ErrCannotList; an unreadable tree would pass as clean", err)
	}
}

func TestGroupUsesTheLongestMatchingComponent(t *testing.T) {
	comps := []Component{{Path: ".", Profile: true}, {Path: "ios"}, {Path: "ios-tools"}, {Path: "ios/Lib"}}
	groups, unmatched := Group([]string{"ios/A.swift", "ios-tools/B.swift", "ios/Lib/C.swift", "Top.swift"}, comps)
	want := []Grouped{
		{Component: Component{Path: ".", Profile: true}, Files: []string{"Top.swift"}},
		{Component: Component{Path: "ios"}, Files: []string{"ios/A.swift"}},
		{Component: Component{Path: "ios-tools"}, Files: []string{"ios-tools/B.swift"}},
		{Component: Component{Path: "ios/Lib"}, Files: []string{"ios/Lib/C.swift"}},
	}
	if !reflect.DeepEqual(groups, want) || len(unmatched) != 0 {
		t.Fatalf("Group = %+v unmatched %v, want %+v", groups, unmatched, want)
	}
}

// D18: only packages under a PACKAGE component are built by the Lint job. A
// package inside the app component is built by the Xcode project in Test.
func TestPackageDirsListsPackagesUnderPackageComponentsOnly(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/Packages/Core/Package.swift": "",
		"ios/LocalLib/Package.swift":      "",
		"tools/Package.swift":             "",
		"tools/a/Package.swift":           "",
		"tools/b/Package.swift":           "",
		"tools/b/deep/x/Package.swift":    "",
		"tools/c/Sources/c/main.swift":    "",
	})
	got, err := PackageDirs(dir, manifest(appIOS, toolsPkg))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tools", "tools/a", "tools/b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PackageDirs = %v, want %v (depth 0 or 1 under a package component, never the app's)", got, want)
	}
}

func TestCheckBlocksOnlyFromTheSharedGateDate(t *testing.T) {
	dir := repo(t, map[string]string{"ios/A.swift": "", "Stray.swift": ""})
	cfg := manifest(appIOS)
	if !GateFrom().Equal(testtargets.WatchGateFrom) {
		t.Fatalf("the stray-Swift gate opens %s but the watch gate %s: the ruling is one date for both", GateFrom(), testtargets.WatchGateFrom)
	}
	before, err := Check(dir, cfg, GateFrom().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if before.Blocks() {
		t.Error("strays block before the gate date; they must only warn")
	}
	on, err := Check(dir, cfg, GateFrom())
	if err != nil {
		t.Fatal(err)
	}
	if !on.Blocks() {
		t.Error("strays do not block on the gate date itself")
	}
	clean, err := Check(repo(t, map[string]string{"ios/A.swift": ""}), cfg, GateFrom().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if clean.Blocks() {
		t.Error("a project with no strays blocks after the gate date")
	}
}

// The finding says what to do: the [[component]] block for each directory the
// strays live in, the date, and every component that WAS discovered.
func TestFormatPrintsTheRemedyTheDateAndTheComponents(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/A.swift":                     "",
		"tools/Lib/Package.swift":         "",
		"FlatLib/Sources/FlatLib/F.swift": "",
		"FlatLib/Tests/FT/T.swift":        "",
		"Stray.swift":                     "",
	})
	r, err := Check(dir, manifest(appIOS, toolsPkg), GateFrom().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	out := Format(r)
	for _, want := range []string{
		"FlatLib/Sources/FlatLib/F.swift",
		"Stray.swift",
		"ios (app)", "tools",
		`path = "FlatLib"`, `stack = "ios"`, "FlatLib/.swiftlint.yml",
		"Stray.swift: move it under a component",
		"from " + GateDate(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Format does not say %q:\n%s", want, out)
		}
	}
}

// A directory that would CONTAIN a declared component is not a remedy: the
// new component would lint the existing one a second time.
func TestRemedyNeverSuggestsADirectoryThatContainsAComponent(t *testing.T) {
	dir := repo(t, map[string]string{
		"ios/App/A.swift":                   "",
		"ios/NFCLib/Sources/NFCLib/N.swift": "",
		"ios/Loose.swift":                   "",
	})
	r, err := Check(dir, manifest(config.Component{Path: "ios/App", Profiles: []string{"ios"}}), GateFrom())
	if err != nil {
		t.Fatal(err)
	}
	got := Remedies(r)
	want := []Remedy{{Dir: "ios/NFCLib", Files: 1}, {Files: 1, Move: []string{"ios/Loose.swift"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Remedies = %+v, want %+v", got, want)
	}
}
