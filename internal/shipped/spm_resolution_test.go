package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// xcodebuild resolves Swift packages against the project's REQUIREMENTS, not
// against the committed Package.resolved. When a pin does not satisfy a
// requirement (a lockfile-only dependency bump, or an exactVersion pin the
// lockfile has moved past), it re-resolves, rewrites Package.resolved in the
// build's own checkout, and stays green. The build ships the requirement and
// the lockfile goes on stating a version that never ships.
//
// The managed workflows therefore resolve strictly: an explicit resolve step
// with -onlyUsePackageVersionsFromResolvedFile that fails the job, and the same
// flag on every later xcodebuild that could resolve again. These tests assert
// both, and RUN the resolve step against a fake xcodebuild, because what can be
// wrong with it is its shell, and no structural assertion reaches that.

const strictResolveFlag = "-onlyUsePackageVersionsFromResolvedFile"

// strictResolveStep is the name the resolve step carries in every job that
// builds. The tests find it by name, so renaming it is a deliberate act.
const strictResolveStep = "Resolve Swift packages from Package.resolved"

type spmStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

type spmDoc struct {
	Jobs map[string]struct {
		Steps []spmStep `yaml:"steps"`
	} `yaml:"jobs"`
}

func parseSPMDoc(t *testing.T, rendered string) spmDoc {
	t.Helper()
	var doc spmDoc
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered workflow is not valid YAML: %v", err)
	}
	return doc
}

// xcodebuildCall is one xcodebuild invocation, its continuation lines joined.
type xcodebuildCall struct {
	step string
	args []string
}

// action is what the call does: the first argument that names an action.
func (c xcodebuildCall) action() string {
	for _, a := range c.args {
		switch a {
		case "build", "test", "archive", "build-for-testing", "test-without-building",
			"-resolvePackageDependencies", "-showBuildSettings", "-exportArchive",
			"-version", "-checkFirstLaunchStatus", "-downloadPlatform", "-license", "-runFirstLaunch":
			return a
		}
	}
	return ""
}

// resolves reports whether the call can resolve Swift packages, which is every
// call that opens the project's package graph.
func (c xcodebuildCall) resolves() bool {
	switch c.action() {
	case "build", "test", "archive", "build-for-testing", "-resolvePackageDependencies", "-showBuildSettings":
		return true
	}
	return false
}

func (c xcodebuildCall) has(flag string) bool {
	for _, a := range c.args {
		if a == flag {
			return true
		}
	}
	return false
}

var xcodebuildAt = regexp.MustCompile(`(^|[\s($])xcodebuild\s`)

// xcodebuildCalls extracts every xcodebuild invocation from a step's script.
// Comment lines and echo'd text are skipped: they mention xcodebuild without
// running it.
func xcodebuildCalls(step spmStep) []xcodebuildCall {
	joined := strings.ReplaceAll(step.Run, "\\\n", " ")
	var out []xcodebuildCall
	for _, line := range strings.Split(joined, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "echo ") || strings.HasPrefix(trimmed, "printf ") {
			continue
		}
		loc := xcodebuildAt.FindStringIndex(line)
		if loc == nil {
			continue
		}
		rest := line[loc[1]:]
		// Stop at the first pipe or redirection that ends the argument list.
		for _, stop := range []string{"2>&1", " | ", " >", ")"} {
			if i := strings.Index(rest, stop); i >= 0 {
				rest = rest[:i]
			}
		}
		var args []string
		for _, f := range strings.Fields(rest) {
			args = append(args, strings.Trim(f, `"'`))
		}
		out = append(out, xcodebuildCall{step: step.Name, args: args})
	}
	return out
}

// spmRenders is every rendering the strict-resolution guarantee covers: both
// workflows, for a single app, a product matrix and a watch suite.
func spmRenders(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"ci.yml (solo)":      renderIOSCI(t, soloConfig()),
		"ci.yml (products)":  renderIOSCI(t, twoIOSProducts()),
		"ci.yml (watch)":     renderIOSCI(t, watchProject()),
		"release.yml (solo)": renderRelease(t, soloProject()),
		"release.yml (products)": renderRelease(t, func() *config.Config {
			cfg := soloProject()
			cfg.Product = twoIOSProducts().Product
			return cfg
		}()),
	}
}

// Every xcodebuild call that can resolve packages carries the flag. One call
// without it re-resolves silently, and every later strict call then reads the
// lockfile that call rewrote.
func TestEveryResolvingXcodebuildCallIsStrict(t *testing.T) {
	for name, rendered := range spmRenders(t) {
		doc := parseSPMDoc(t, rendered)
		seen := map[string]int{}
		for job, j := range doc.Jobs {
			for _, st := range j.Steps {
				for _, c := range xcodebuildCalls(st) {
					if !c.resolves() {
						continue
					}
					seen[c.action()]++
					if !c.has(strictResolveFlag) {
						t.Errorf("%s: job %s, step %q runs `xcodebuild %s` without %s, so it can re-resolve Swift packages away from Package.resolved and stay green",
							name, job, st.Name, strings.Join(c.args, " "), strictResolveFlag)
					}
				}
			}
		}
		// Not vacuous: the calls that matter were found at all. A parser that
		// matched nothing would pass every assertion above.
		want := []string{"-resolvePackageDependencies"}
		if strings.HasPrefix(name, "ci.yml") {
			want = append(want, "build", "test", "-showBuildSettings")
		} else {
			want = append(want, "archive", "-showBuildSettings")
		}
		for _, a := range want {
			if seen[a] == 0 {
				t.Errorf("%s: found no `xcodebuild %s` call; the workflow changed shape or this test's parser stopped matching", name, a)
			}
		}
	}
}

// The watch job builds and tests a second scheme; it must not be the one call
// left resolving freely.
func TestWatchTestCallIsStrict(t *testing.T) {
	doc := parseSPMDoc(t, renderIOSCI(t, watchProject()))
	job, ok := doc.Jobs["watch-test"]
	if !ok {
		t.Fatal("no watch-test job rendered for a project declaring watch_tests")
	}
	n := 0
	for _, st := range job.Steps {
		for _, c := range xcodebuildCalls(st) {
			if c.action() == "test" {
				n++
				if !c.has(strictResolveFlag) {
					t.Errorf("watch-test step %q runs `xcodebuild test` without %s", st.Name, strictResolveFlag)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("found no `xcodebuild test` in the watch-test job")
	}
}

// The strict resolve step runs in every job that builds, BEFORE any other call
// that opens the package graph. After one, it would be checking a lockfile
// that call may already have rewritten.
func TestStrictResolveRunsBeforeAnyOtherResolvingCall(t *testing.T) {
	cases := []struct {
		name, rendered string
		jobs           []string
	}{
		{"ci.yml", renderIOSCI(t, soloConfig()), []string{"build-release", "test"}},
		{"ci.yml (products)", renderIOSCI(t, twoIOSProducts()), []string{"build-release", "test"}},
		{"release.yml", renderRelease(t, soloProject()), []string{"build-and-deploy"}},
	}
	for _, tc := range cases {
		doc := parseSPMDoc(t, tc.rendered)
		for _, name := range tc.jobs {
			job, ok := doc.Jobs[name]
			if !ok {
				t.Errorf("%s: no %s job", tc.name, name)
				continue
			}
			resolveAt, firstOther := -1, -1
			for i, st := range job.Steps {
				for _, c := range xcodebuildCalls(st) {
					if !c.resolves() {
						continue
					}
					if st.Name == strictResolveStep && c.action() == "-resolvePackageDependencies" {
						if resolveAt < 0 {
							resolveAt = i
						}
					} else if firstOther < 0 {
						firstOther = i
					}
				}
			}
			switch {
			case resolveAt < 0:
				t.Errorf("%s: job %s has no %q step running `xcodebuild -resolvePackageDependencies`", tc.name, name, strictResolveStep)
			case firstOther < 0:
				t.Errorf("%s: job %s resolves but never builds; this test's parser stopped matching", tc.name, name)
			case firstOther < resolveAt:
				t.Errorf("%s: job %s runs %q (step %d) before %q (step %d)", tc.name, name, job.Steps[firstOther].Name, firstOther, strictResolveStep, resolveAt)
			}
		}
	}
}

// fakeXcodebuild writes an xcodebuild that records its arguments and then does
// what FAKE_XCODEBUILD says: "ok" exits 0, "drift" fails the way a strict
// resolve does when Package.resolved is out of date, "rewrite" exits 0 having
// changed Package.resolved, and "create" exits 0 having written one.
func fakeXcodebuild(t *testing.T) (bin, argsLog string) {
	t.Helper()
	bin = t.TempDir()
	argsLog = filepath.Join(t.TempDir(), "xcodebuild.args")
	script := `#!/bin/bash
printf '%s\n' "$@" >> "$FAKE_ARGS"
resolved="$FAKE_RESOLVED"
case "$FAKE_XCODEBUILD" in
  ok) echo "Resolved source packages:"; exit 0 ;;
  drift) echo "xcodebuild: error: Could not resolve package dependencies:" >&2
         echo "  the Package.resolved file is most likely severely out-of-date" >&2
         exit 74 ;;
  rewrite) echo '{"pins":[],"version":3,"rewritten":true}' > "$resolved"; exit 0 ;;
  create) mkdir -p "$(dirname "$resolved")"; echo '{"pins":[],"version":3}' > "$resolved"; exit 0 ;;
esac
echo "fake xcodebuild: unknown mode $FAKE_XCODEBUILD" >&2
exit 99
`
	if err := os.WriteFile(filepath.Join(bin, "xcodebuild"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

const (
	pbxNoPackages     = "// !$*UTF8*$!\n{ objects = { }; }\n"
	pbxRemotePackages = "// !$*UTF8*$!\n{ objects = {\n/* Begin XCRemoteSwiftPackageReference section */\n A /* XCRemoteSwiftPackageReference \"pkg\" */ = { isa = XCRemoteSwiftPackageReference; };\n/* End XCRemoteSwiftPackageReference section */\n}; }\n"
	pbxLocalPackages  = "// !$*UTF8*$!\n{ objects = {\n/* Begin XCLocalSwiftPackageReference section */\n B /* XCLocalSwiftPackageReference \"Local\" */ = { isa = XCLocalSwiftPackageReference; relativePath = Local; };\n/* End XCLocalSwiftPackageReference section */\n}; }\n"
	resolvedFixture   = "{\n  \"pins\" : [ ],\n  \"version\" : 3\n}\n"
)

// strictResolveScripts is the resolve step's script from every job that has one,
// keyed by where it came from.
func strictResolveScripts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, rendered := range map[string]string{
		"ci.yml":      renderIOSCI(t, soloConfig()),
		"release.yml": renderRelease(t, soloProject()),
	} {
		for job, j := range parseSPMDoc(t, rendered).Jobs {
			for _, st := range j.Steps {
				if st.Name == strictResolveStep {
					out[name+" "+job] = st.Run
				}
			}
		}
	}
	for _, want := range []string{"ci.yml build-release", "ci.yml test", "release.yml build-and-deploy"} {
		if _, ok := out[want]; !ok {
			t.Errorf("no %q step in %s", strictResolveStep, want)
		}
	}
	return out
}

// TestStrictResolveStepFailsClosed runs each rendered copy of the step, in a
// scratch checkout, against every state a project can be in.
func TestStrictResolveStepFailsClosed(t *testing.T) {
	scripts := strictResolveScripts(t)
	if len(scripts) == 0 {
		t.Fatal("no strict resolve step rendered anywhere")
	}
	type tc struct {
		name     string
		pbxproj  string // "" = no project.pbxproj at all
		resolved bool
		mode     string
		wantFail bool
		wantCall bool     // whether xcodebuild should have been invoked
		wantOut  []string // substrings the output must contain
	}
	cases := []tc{
		{name: "lockfile matches", pbxproj: pbxRemotePackages, resolved: true, mode: "ok", wantCall: true},
		{name: "lockfile drifted", pbxproj: pbxRemotePackages, resolved: true, mode: "drift", wantFail: true, wantCall: true,
			wantOut: []string{"::error::", "Package.resolved", "out-of-date"}},
		{name: "resolve rewrote the lockfile", pbxproj: pbxRemotePackages, resolved: true, mode: "rewrite", wantFail: true, wantCall: true,
			wantOut: []string{"::error::", "Package.resolved", "changed"}},
		{name: "remote packages, no lockfile", pbxproj: pbxRemotePackages, mode: "ok", wantFail: true,
			wantOut: []string{"::error::", "Package.resolved"}},
		{name: "local package, resolve writes a lockfile", pbxproj: pbxLocalPackages, mode: "create", wantFail: true, wantCall: true,
			wantOut: []string{"::error::", "Package.resolved"}},
		{name: "local package, nothing to pin", pbxproj: pbxLocalPackages, mode: "ok", wantCall: true},
		{name: "no packages, no lockfile", pbxproj: pbxNoPackages, mode: "drift", wantOut: []string{"nothing to resolve"}},
		{name: "no project file", pbxproj: "", mode: "ok", wantFail: true, wantOut: []string{"::error::", "project.pbxproj"}},
	}
	for where, script := range scripts {
		for _, c := range cases {
			t.Run(where+"/"+c.name, func(t *testing.T) {
				dir := t.TempDir()
				proj := filepath.Join(dir, soloConfig().Project.Xcodeproj)
				resolved := filepath.Join(proj, "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved")
				if c.pbxproj != "" {
					if err := os.MkdirAll(proj, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(proj, "project.pbxproj"), []byte(c.pbxproj), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if c.resolved {
					if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(resolved, []byte(resolvedFixture), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				bin, argsLog := fakeXcodebuild(t)
				// bash -e: what Actions runs an unannotated `run:` with.
				cmd := exec.Command("bash", "-e", "-c", script)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(),
					"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
					"FAKE_XCODEBUILD="+c.mode, "FAKE_ARGS="+argsLog, "FAKE_RESOLVED="+resolved,
					"PRODUCT_SCHEME=P")
				out, err := cmd.CombinedOutput()
				if failed := err != nil; failed != c.wantFail {
					t.Fatalf("exit error = %v, want failure = %v\n%s", err, c.wantFail, out)
				}
				for _, w := range c.wantOut {
					if !strings.Contains(string(out), w) {
						t.Errorf("output lacks %q:\n%s", w, out)
					}
				}
				args, _ := os.ReadFile(argsLog)
				if called := len(args) > 0; called != c.wantCall {
					t.Fatalf("xcodebuild called = %v, want %v\n%s", called, c.wantCall, out)
				}
				if c.wantCall {
					// The same project, scheme and derived data as the job's
					// builds, so the packages it checks are the ones they use.
					scheme, dd := "Demo", "DerivedData"
					if strings.HasPrefix(where, "release.yml") {
						scheme = "P" // $PRODUCT_SCHEME, from the matrix
					}
					for _, w := range []string{"-resolvePackageDependencies", strictResolveFlag,
						"-project\n" + soloConfig().Project.Xcodeproj, "-scheme\n" + scheme, "-derivedDataPath\n" + dd} {
						if !strings.Contains(string(args), w+"\n") {
							t.Errorf("xcodebuild was not given %s; args:\n%s", w, args)
						}
					}
				}
			})
		}
	}
}
