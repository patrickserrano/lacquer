package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/swiftcomponents"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// #522 U4: the Lint job and the pre-commit hook reach every Swift component the
// manifest declares. Every test here EXECUTES the rendered step or script
// against stubs, because what matters is where each tool runs and what the step
// does with its exit code, not what the text says.

// twoSwiftComponents is an app under ios/ with a package component beside it.
func twoSwiftComponents() *config.Config {
	cfg := soloConfig()
	cfg.Components = []config.Component{
		{Path: "ios", Profiles: []string{"ios"}, Stack: "ios"},
		{Path: "tools", Stack: "ios"},
	}
	return cfg
}

// tree writes files under dir.
func tree(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("// fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runStepIn runs a rendered step body the way GitHub's default shell does
// (`bash -e {0}`), in dir, with bin first on PATH.
func runStepIn(t *testing.T, dir, body, bin string, env ...string) (string, int) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "step.sh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + bin + ":" + os.Getenv("PATH")}, env...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// recordingSwiftlint is a swiftlint that appends "<cwd> | <argv>" per run and
// exits 2 when its cwd ends in $FAIL_IN.
func recordingSwiftlint(t *testing.T) (bin, log string) {
	t.Helper()
	bin = t.TempDir()
	log = filepath.Join(bin, "runs")
	stubTool(t, bin, "swiftlint", `echo "$(pwd -P) | $*" >> "`+log+`"
if [ -n "${FAIL_IN:-}" ] && [ "$(basename "$(pwd -P)")" = "$FAIL_IN" ]; then
  echo "Sample.swift:1:1: error: Line Length Violation"
  exit 2
fi
`)
	return bin, log
}

func runs(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// TestLintStepLintsEveryComponentFromItsOwnDirectory is D16: two components,
// two runs, each from inside its own directory against its own config.
func TestLintStepLintsEveryComponentFromItsOwnDirectory(t *testing.T) {
	body := stepRun(t, parseIOSCI(t, twoSwiftComponents()), "lint", "Run SwiftLint")
	dir := t.TempDir()
	tree(t, dir, "ios/.swiftlint.yml", "ios/App/A.swift", "tools/.swiftlint.yml", "tools/lib/Sources/K.swift")
	real, _ := filepath.EvalSymlinks(dir)
	bin, log := recordingSwiftlint(t)

	out, code := runStepIn(t, dir, body, bin)
	if code != 0 {
		t.Fatalf("Run SwiftLint exited %d on a clean two-component project\n%s", code, out)
	}
	want := []string{
		real + "/ios | --strict --config .swiftlint.yml .",
		real + "/tools | --strict --config .swiftlint.yml .",
	}
	if got := runs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("swiftlint runs = %q, want %q", got, want)
	}
	for _, group := range []string{"::group::SwiftLint: ios", "::group::SwiftLint: tools"} {
		if !strings.Contains(out, group) {
			t.Errorf("the log has no %q group\n%s", group, out)
		}
	}

	// A violation in either component fails the step, names that component
	// and only that one, prints SwiftLint's own output, and does not stop the
	// other component from being linted: the app fails FIRST in one case, so
	// an early exit would leave tools unlinted.
	for _, failing := range []string{"ios", "tools"} {
		_ = os.Remove(log)
		out, code = runStepIn(t, dir, body, bin, "FAIL_IN="+failing)
		if code == 0 {
			t.Fatalf("Run SwiftLint passed with a violation in %s\n%s", failing, out)
		}
		for _, want := range []string{"SwiftLint failed in component " + failing, "Line Length Violation"} {
			if !strings.Contains(out, want) {
				t.Errorf("the failing run does not say %q\n%s", want, out)
			}
		}
		if strings.Count(out, "SwiftLint failed in component") != 1 {
			t.Errorf("a component other than %s was blamed\n%s", failing, out)
		}
		if n := len(runs(t, log)); n != 2 {
			t.Errorf("%d swiftlint runs after %s failed, want both components linted", n, failing)
		}
	}
}

// A lone component lints from its own directory too: for a root layout that is
// the repository root, exactly where the step always ran.
func TestLintStepLintsALoneRootComponentFromTheRoot(t *testing.T) {
	body := stepRun(t, parseIOSCI(t, soloConfig()), "lint", "Run SwiftLint")
	dir := t.TempDir()
	tree(t, dir, ".swiftlint.yml", "App/A.swift")
	real, _ := filepath.EvalSymlinks(dir)
	bin, log := recordingSwiftlint(t)
	if out, code := runStepIn(t, dir, body, bin); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if got, want := runs(t, log), []string{real + " | --strict --config .swiftlint.yml ."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("swiftlint runs = %q, want %q", got, want)
	}
}

// D16: a package component with no .swiftlint.yml fails Lint, by name. It is
// not skipped: skipping it is how its files were linted by nothing.
func TestLintFailsWhenAPackageComponentHasNoConfig(t *testing.T) {
	body := stepRun(t, parseIOSCI(t, twoSwiftComponents()), "lint", "Run SwiftLint")
	dir := t.TempDir()
	tree(t, dir, "ios/.swiftlint.yml", "ios/App/A.swift", "tools/lib/Sources/K.swift")
	bin, log := recordingSwiftlint(t)
	out, code := runStepIn(t, dir, body, bin)
	if code == 0 {
		t.Fatalf("Run SwiftLint passed with no tools/.swiftlint.yml\n%s", out)
	}
	if !strings.Contains(out, "component tools declares stack ios but has no .swiftlint.yml") {
		t.Errorf("the failure does not name the component and the missing config\n%s", out)
	}
	if n := len(runs(t, log)); n != 1 {
		t.Errorf("%d swiftlint runs, want the app still linted once", n)
	}
}

// The mis-scoped-config guard applies per component: Swift exists under tools
// but its config matches none of it.
func TestLintFailsWhenAComponentsConfigMatchesNothing(t *testing.T) {
	body := stepRun(t, parseIOSCI(t, twoSwiftComponents()), "lint", "Run SwiftLint")
	dir := t.TempDir()
	tree(t, dir, "ios/.swiftlint.yml", "ios/App/A.swift", "tools/.swiftlint.yml", "tools/lib/Sources/K.swift")
	bin := t.TempDir()
	stubTool(t, bin, "swiftlint", `if [ "$(basename "$(pwd -P)")" = tools ]; then echo "Error: No lintable files found at paths: ''"; fi
exit 0
`)
	out, code := runStepIn(t, dir, body, bin)
	if code == 0 || !strings.Contains(out, "Swift sources exist under tools but SwiftLint matched none") {
		t.Fatalf("a config matching nothing passed (exit %d)\n%s", code, out)
	}
}

// packageRepo is a git repository with a package under the app component and
// two under the package component, rendered into a Config with that root.
func packageRepo(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	tree(t, dir,
		"ios/Packages/Core/Package.swift", "ios/LocalLib/Package.swift",
		"tools/alpha/Package.swift", "tools/alpha/Sources/alpha/main.swift",
		"tools/beta/Package.swift", "tools/beta/Sources/Beta/B.swift",
		"tools/nfc/Sources/NFC/N.swift",
	)
	// An iOS-only package: swift build cannot compile it on the macOS runner.
	iosOnly := "let package = Package(name: \"NFC\", platforms: [.iOS(.v26)])\n"
	if err := os.WriteFile(filepath.Join(dir, "tools", "nfc", "Package.swift"), []byte(iosOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Init(t, dir, "-q")
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "f"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	cfg := twoSwiftComponents()
	cfg.Root = dir
	return cfg, dir
}

// TestBuildPackagesStepRunsOncePerPackage is D18: each package under the
// package component is built with tests, from its resolved file, with
// warnings as errors; the app component's package is not.
func TestBuildPackagesStepRunsOncePerPackage(t *testing.T) {
	cfg, dir := packageRepo(t)
	body := stepRun(t, parseIOSCI(t, cfg), "lint", "Build Swift packages")
	bin := t.TempDir()
	log := filepath.Join(bin, "runs")
	stubTool(t, bin, "swift", `echo "$*" >> "`+log+`"
case "$*" in *"${FAIL_PKG:-<none>}"*) echo "error: cannot find 'x' in scope"; exit 1 ;; esac
`)
	out, code := runStepIn(t, dir, body, bin)
	if code != 0 {
		t.Fatalf("Build Swift packages exited %d\n%s", code, out)
	}
	const flags = " --only-use-versions-from-resolved-file -Xswiftc -warnings-as-errors"
	want := []string{
		"build --build-tests --package-path tools/alpha" + flags,
		"build --build-tests --package-path tools/beta" + flags,
	}
	if got := runs(t, log); !reflect.DeepEqual(got, want) {
		t.Fatalf("swift runs = %q, want %q (the iOS-only tools/nfc must not be built)", got, want)
	}
	// The iOS-only package is not passed over silently: a notice names it.
	if !strings.Contains(out, "::notice title=Swift package not built::Not built: tools/nfc is an iOS-only package") {
		t.Errorf("no notice naming the skipped iOS-only package\n%s", out)
	}

	// A broken package fails the step, by name, and the others still build.
	_ = os.Remove(log)
	out, code = runStepIn(t, dir, body, bin, "FAIL_PKG=tools/alpha")
	if code == 0 {
		t.Fatalf("Build Swift packages passed with tools/alpha broken\n%s", out)
	}
	if !strings.Contains(out, "Swift package tools/alpha did not build") || strings.Contains(out, "tools/beta did not build") {
		t.Errorf("the failure does not name exactly the broken package\n%s", out)
	}
	if n := len(runs(t, log)); n != 2 {
		t.Errorf("%d swift runs after a failure, want every package attempted", n)
	}
}

// A package component holding only iOS-only packages builds nothing and says
// which packages it did not build; it does not claim there are none.
func TestBuildPackagesStepNamesEverySkippedPackage(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"tools/a", "tools/b"} {
		tree(t, dir, p+"/Sources/X/X.swift")
		if err := os.WriteFile(filepath.Join(dir, p, "Package.swift"), []byte("let package = Package(name: \"X\", platforms: [.iOS(.v18)])\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Init(t, dir, "-q")
	cfg := twoSwiftComponents()
	cfg.Root = dir
	body := stepRun(t, parseIOSCI(t, cfg), "lint", "Build Swift packages")
	bin := t.TempDir()
	stubTool(t, bin, "swift", "echo ran >> \""+filepath.Join(bin, "runs")+"\"\n")
	out, code := runStepIn(t, dir, body, bin)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"Not built: tools/a is an iOS-only package", "Not built: tools/b is an iOS-only package", "Built 0 Swift packages"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "No package components declared") {
		t.Errorf("a project with a package component was told it has none\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(bin, "runs")); err == nil {
		t.Error("swift ran for an iOS-only package")
	}
}

// One Swift component: nothing to build, said out loud, and swift never runs.
func TestBuildPackagesStepIsANoOpForALoneComponent(t *testing.T) {
	body := stepRun(t, parseIOSCI(t, soloConfig()), "lint", "Build Swift packages")
	bin := t.TempDir()
	stubTool(t, bin, "swift", "echo ran >> \""+filepath.Join(bin, "runs")+"\"\n")
	out, code := runStepIn(t, t.TempDir(), body, bin)
	if code != 0 || !strings.Contains(out, "No package components declared") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(bin, "runs")); err == nil {
		t.Error("swift ran for a project with no package component")
	}
}

// The stray check step runs the released binary's `swift-components --check`
// and fails exactly when it does. A missing binary is a failure, not a pass.
func TestStrayCheckStepFailsWithTheBinary(t *testing.T) {
	body := stepRun(t, parseIOSCI(t, soloConfig()), "lint", "Every Swift file belongs to a declared component")
	dir := t.TempDir()
	tree(t, dir, ".lacquer.toml")
	runnerTemp := t.TempDir()
	argv := filepath.Join(runnerTemp, "argv")
	stubTool(t, runnerTemp, "lacquer", `printf '%s\n' "$@" > "`+argv+`"; exit "${STUB_EXIT:-0}"`+"\n")

	for _, tc := range []struct {
		exit     string
		wantFail bool
	}{{"0", false}, {"1", true}} {
		out, code := runStepIn(t, dir, body, t.TempDir(), "RUNNER_TEMP="+runnerTemp, "STUB_EXIT="+tc.exit)
		if (code != 0) != tc.wantFail {
			t.Errorf("binary exit %s: step exit %d, want failure=%v\n%s", tc.exit, code, tc.wantFail, out)
		}
	}
	b, _ := os.ReadFile(argv)
	if got := strings.Fields(string(b)); !reflect.DeepEqual(got, []string{"swift-components", "--check"}) {
		t.Errorf("the step ran lacquer %q, want swift-components --check", got)
	}

	if err := os.Remove(filepath.Join(runnerTemp, "lacquer")); err != nil {
		t.Fatal(err)
	}
	out, code := runStepIn(t, dir, body, t.TempDir(), "RUNNER_TEMP="+runnerTemp)
	if code == 0 || !strings.Contains(out, "NOT checked") {
		t.Errorf("the step passed with no binary to run (exit %d)\n%s", code, out)
	}
}

// The stray check needs the binary the doctor step downloads, so it must come
// after it; the package build is the job's last step, so a broken package
// leaves every other step green.
func TestLintStepOrder(t *testing.T) {
	var names []string
	for _, st := range parseIOSCI(t, soloConfig()).Jobs["lint"].Steps {
		names = append(names, st.Name)
	}
	idx := func(n string) int {
		for i, s := range names {
			if s == n {
				return i
			}
		}
		t.Fatalf("no %q step in Lint: %q", n, names)
		return -1
	}
	if idx("Every Swift file belongs to a declared component") < idx("Prove the checks can fail") {
		t.Error("the stray check runs before the step that downloads the lacquer binary")
	}
	if last := names[len(names)-1]; last != "Build Swift packages" {
		t.Errorf("Lint's last step is %q, want Build Swift packages", last)
	}
}

// pushPaths parses on.push.paths from a rendered workflow.
func pushPaths(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	var doc struct {
		On struct {
			Push struct {
				Paths []string `yaml:"paths"`
			} `yaml:"push"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(renderIOSCI(t, cfg)), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.On.Push.Paths
}

// D19: a push to main touching only a package component still runs CI.
func TestPushPathsRenderOnePerPackageComponent(t *testing.T) {
	cfg := twoSwiftComponents()
	raw, err := os.ReadFile(iosCIPath(t))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := tokens.Substitute(string(raw), tokens.Values(cfg, "ios/"))
	var doc struct {
		On struct {
			Push struct {
				Paths []string `yaml:"paths"`
			} `yaml:"push"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ios/**", "tools/**"}; !reflect.DeepEqual(doc.On.Push.Paths, want) {
		t.Errorf("push paths = %q, want %q", doc.On.Push.Paths, want)
	}
	if got := pushPaths(t, soloConfig()); !reflect.DeepEqual(got, []string{"**"}) {
		t.Errorf("a lone root component's push paths = %q, want only **", got)
	}
}

// --- pre-commit ---

// precommitConfigHooks reads the shipped .pre-commit-config.yaml template.
func precommitConfigHooks(t *testing.T) map[string]map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "root", ".pre-commit-config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Repos []struct {
			Hooks []map[string]any `yaml:"hooks"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := map[string]map[string]any{}
	for _, r := range doc.Repos {
		for _, h := range r.Hooks {
			hooks[h["id"].(string)] = h
		}
	}
	return hooks
}

// D20: every staged .swift reaches the swiftlint hook. A files: filter on it is
// exactly how Swift beside the app went unlinted.
func TestPreCommitConfigSwiftlintHookHasNoFilesFilter(t *testing.T) {
	h, ok := precommitConfigHooks(t)["swiftlint"]
	if !ok {
		t.Fatal("no swiftlint hook")
	}
	if f, has := h["files"]; has {
		t.Errorf("the swiftlint hook still filters files (%v); Swift outside that prefix never reaches it", f)
	}
}

// swiftformat and swiftlint-docs keep the app filter: their configs exist only
// in the app component.
func TestPreCommitConfigSwiftformatHookKeepsItsFilter(t *testing.T) {
	hooks := precommitConfigHooks(t)
	for _, id := range []string{"swiftformat", "swiftlint-docs"} {
		if got := hooks[id]["files"]; got != "^{{COMPONENT_PREFIX}}" {
			t.Errorf("%s hook files = %v, want ^{{COMPONENT_PREFIX}}", id, got)
		}
	}
}

// cwdStub is a swiftlint that appends "<cwd basename>: <file args>" per run,
// and exits 2 when its cwd's basename is $FAIL_IN.
const cwdStub = `#!/bin/sh
files=""
for a in "$@"; do
  case "$a" in --*|lint|.swiftlint.yml|.swiftlint-docs.yml) ;; *) files="$files $a" ;; esac
done
echo "$(basename "$(pwd -P)"):$files" >> "$RUNS"
if [ -n "${FAIL_IN:-}" ] && [ "$(basename "$(pwd -P)")" = "$FAIL_IN" ]; then
  echo "x.swift:1:1: error: Force Unwrapping Violation"
  exit 2
fi
exit 0
`

// runGrouped runs the wrapper rendered for comps, in a project holding a
// config in each component listed in withConfig, against cwdStub.
func runGrouped(t *testing.T, comps []config.Component, gate string, withConfig []string, files []string, env ...string) (out string, code int, got []string) {
	t.Helper()
	script := renderPrecommitScript(t, &config.Config{Components: comps}, gate)
	project := filepath.Join(t.TempDir(), "proj")
	for _, c := range withConfig {
		tree(t, project, filepath.Join(c, ".swiftlint.yml"))
	}
	tree(t, project, files...)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "swiftlint"), []byte(cwdStub), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(bin, "runs")
	cmd := exec.Command("/bin/bash", append([]string{script, "swiftlint"}, files...)...)
	cmd.Dir = project
	cmd.Env = append(os.Environ(), append([]string{"PATH=" + bin + ":" + os.Getenv("PATH"), "RUNS=" + log}, env...)...)
	b, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(b), code, runs(t, log)
}

var (
	pastGate   = "2020-01-01"
	futureGate = "2099-01-01"
	appAndLib  = []config.Component{{Path: "ios", Profiles: []string{"ios"}}, {Path: "tools", Stack: "ios"}}
)

// D20: staged files are grouped by component and each group is linted from
// inside its component, with paths relative to it. Run under /bin/bash, the
// 3.2 macOS ships, because that is what `#!/usr/bin/env bash` finds in an agent
// shell without Homebrew on PATH.
func TestPrecommitGroupsStagedFilesByComponent(t *testing.T) {
	files := []string{"ios/App/A.swift", "tools/lib/K.swift", "ios/App/B.swift"}
	out, code, got := runGrouped(t, appAndLib, pastGate, []string{"ios", "tools"}, files)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if want := []string{"ios: App/A.swift App/B.swift", "tools: lib/K.swift"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("swiftlint runs = %q, want %q", got, want)
	}
	// The grouping agrees with the Go implementation the audit and CI use.
	groups, unmatched := swiftcomponents.Group(files, swiftcomponents.Components(&config.Config{Components: appAndLib}))
	if len(groups) != 2 || len(groups[0].Files) != 2 || len(groups[1].Files) != 1 || len(unmatched) != 0 {
		t.Fatalf("Go grouping disagrees: %+v unmatched %v", groups, unmatched)
	}

	// A violation in one component fails the commit, and every other
	// component is still linted, so the commit reports everything at once.
	out, code, got = runGrouped(t, appAndLib, pastGate, []string{"ios", "tools"}, files, "FAIL_IN=ios")
	if code != 2 || !strings.Contains(out, "Force Unwrapping Violation") || len(got) != 2 {
		t.Fatalf("violation in ios: exit %d, %d runs, want 2 and 2\n%s", code, len(got), out)
	}
}

// A staged file under no component blocks the commit once the gate date has
// passed, naming the file, and is never handed to swiftlint. Before the date it
// is reported with the date, and the rest of the commit is linted.
func TestPrecommitFailsClosedOnAStagedFileOutsideEveryComponent(t *testing.T) {
	files := []string{"ios/App/A.swift", "Scratch/Stray.swift"}
	out, code, got := runGrouped(t, appAndLib, pastGate, []string{"ios", "tools"}, files)
	if code == 0 {
		t.Fatalf("a stray staged file passed past the gate date\n%s", out)
	}
	for _, want := range []string{"Scratch/Stray.swift", "under no declared Swift component", "Blocking since 2020-01-01", `stack = \"ios\"`} {
		if !strings.Contains(out, strings.ReplaceAll(want, `\"`, `"`)) {
			t.Errorf("the refusal does not say %q\n%s", want, out)
		}
	}
	if len(got) != 0 {
		t.Errorf("swiftlint ran %q although the commit was refused", got)
	}

	out, code, got = runGrouped(t, appAndLib, futureGate, []string{"ios", "tools"}, files)
	if code != 0 {
		t.Fatalf("a stray staged file blocked before the gate date (exit %d)\n%s", code, out)
	}
	if !strings.Contains(out, "Not blocking yet: from 2099-01-01") {
		t.Errorf("no warning carrying the date\n%s", out)
	}
	if want := []string{"ios: App/A.swift"}; !reflect.DeepEqual(got, want) {
		t.Errorf("swiftlint runs = %q, want %q: the stray must not be linted with the app's config", got, want)
	}
}

// A component with staged files and no config fails closed by name.
func TestPrecommitFailsClosedWhenAComponentHasNoConfig(t *testing.T) {
	out, code, got := runGrouped(t, appAndLib, pastGate, []string{"ios"}, []string{"ios/A.swift", "tools/lib/K.swift"})
	if code == 0 || !strings.Contains(out, "component tools declares stack ios but has no .swiftlint.yml") {
		t.Fatalf("exit %d, want a refusal naming tools\n%s", code, out)
	}
	if len(got) > 1 {
		t.Errorf("swiftlint runs %q: tools must not be linted without its config", got)
	}
}

// A component whose path is a prefix of another's must not take its files:
// `ios` vs `ios-tools`, and a deeper component wins over the root.
func TestPrecommitCountsEveryStagedFileExactlyOnce(t *testing.T) {
	comps := []config.Component{{Path: "ios", Profiles: []string{"ios"}}, {Path: "ios-tools", Stack: "ios"}}
	out, code, got := runGrouped(t, comps, pastGate, []string{"ios", "ios-tools"}, []string{"ios-tools/T.swift", "ios/A.swift"})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if want := []string{"ios: A.swift", "ios-tools: T.swift"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("swiftlint runs = %q, want %q", got, want)
	}

	// With ios-tools NOT declared, its file is a stray, not the app's: a
	// string-prefix match would lint it under ios's config.
	appOnly := []config.Component{{Path: "ios", Profiles: []string{"ios"}}}
	out, code, got = runGrouped(t, appOnly, futureGate, []string{"ios"}, []string{"ios-tools/T.swift", "ios/A.swift"})
	if code != 0 || !strings.Contains(out, "ios-tools/T.swift") || !strings.Contains(out, "under no declared Swift component") {
		t.Fatalf("exit %d, want ios-tools/T.swift reported as a stray\n%s", code, out)
	}
	if want := []string{"ios: A.swift"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("swiftlint runs = %q, want %q", got, want)
	}
}

// The hook's gate date is the Go constant the Lint step and the audit read,
// rendered through the real token map.
func TestPrecommitGateIsTheSharedDate(t *testing.T) {
	vals := tokens.Values(soloConfig(), "")
	if got := vals[tokens.IOSSwiftGateFrom]; got != swiftcomponents.GateDate() {
		t.Fatalf("the hook's gate renders as %q, the shared gate is %q", got, swiftcomponents.GateDate())
	}
	b, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "root", "scripts", "precommit-swift.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `GATE_FROM="{{IOS_SWIFT_GATE_FROM}}"`) {
		t.Error("precommit-swift.sh does not take its gate date from the token")
	}
}
