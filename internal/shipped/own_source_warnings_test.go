package shipped

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// The opt-in gate for Swift warnings in a repository's OWN sources.
//
// On current Xcode some Swift 6 diagnostics (the SendableMetatypes group is one)
// stay warnings even under SWIFT_TREAT_WARNINGS_AS_ERRORS, -warnings-as-errors
// and -Werror <group>, while ordinary warnings escalate under the same flags. A
// CI that leans on warnings-as-errors alone therefore goes green with the
// warning printed in its log. This gate reads the log instead of the flags.
//
// What these tests protect is the question the repository's CLAUDE.md ends on:
// can the gate tell "verified and passing" from "never ran"? So there is a case
// for each way it could silently pass (a missing log, a blank allowlist line, a
// broken regex, a path form the runner prints differently) as well as for the
// three outcomes the decision names.

const (
	gateStepName = "Fail on Swift warnings in the repository's own sources"
	// The default allowlist path, repeated here on purpose: a rename in the
	// implementation must be a conscious edit of this test.
	gateDefaultAllowlist = ".lacquer-warning-allowlist"
)

type gateJobDoc struct {
	Jobs map[string]struct {
		Steps []struct {
			Name             string `yaml:"name"`
			If               string `yaml:"if"`
			Run              string `yaml:"run"`
			WorkingDirectory string `yaml:"working-directory"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func gateConfigOn() *config.Config {
	cfg := soloConfig()
	cfg.CI.IOSFailOnOwnSourceWarnings = true
	return cfg
}

func parseGateDoc(t *testing.T, cfg *config.Config) gateJobDoc {
	t.Helper()
	var doc gateJobDoc
	if err := yaml.Unmarshal([]byte(renderIOSCI(t, cfg)), &doc); err != nil {
		t.Fatalf("rendered ci.yml is not valid YAML: %v", err)
	}
	return doc
}

// gateStep returns the rendered gate step of one job and the index of the step
// before it, or fails the test if the job has no such step.
func gateStep(t *testing.T, cfg *config.Config, job string) (run string, prev string, wd string, ifCond string) {
	t.Helper()
	doc := parseGateDoc(t, cfg)
	j, ok := doc.Jobs[job]
	if !ok {
		t.Fatalf("rendered ci.yml has no %q job", job)
	}
	for i, s := range j.Steps {
		if s.Name == gateStepName {
			if i > 0 {
				prev = j.Steps[i-1].Name
			}
			return s.Run, prev, s.WorkingDirectory, s.If
		}
	}
	t.Fatalf("job %q renders no %q step", job, gateStepName)
	return "", "", "", ""
}

// ---- render tests -----------------------------------------------------------

func TestOwnSourceWarningsGateOffRendersNoGate(t *testing.T) {
	for name, cfg := range map[string]*config.Config{"solo": soloConfig(), "two products": twoIOSProducts()} {
		t.Run(name, func(t *testing.T) {
			out := renderIOSCI(t, cfg)
			for _, banned := range []string{gateStepName, "own sources", ".lacquer-warning-allowlist", "OWN_SOURCE"} {
				if strings.Contains(out, banned) {
					t.Errorf("with the gate off the rendered ci.yml still mentions %q", banned)
				}
			}
		})
	}
}

func TestOwnSourceWarningsGateExplicitlyOffEqualsAbsent(t *testing.T) {
	absent := renderIOSCI(t, soloConfig())
	cfg := soloConfig()
	cfg.CI.IOSFailOnOwnSourceWarnings = false
	if renderIOSCI(t, cfg) != absent {
		t.Error("ios_fail_on_own_source_warnings = false renders differently from the key being absent")
	}
}

// Everything the gate adds is one step per job. Removing exactly those two
// steps from an on-render must give back the off-render, so turning the gate on
// changes nothing else in the workflow.
func TestOwnSourceWarningsGateOnOnlyAddsTheTwoSteps(t *testing.T) {
	for name, mk := range map[string]func() *config.Config{"solo": soloConfig, "two products": twoIOSProducts} {
		t.Run(name, func(t *testing.T) {
			off := renderIOSCI(t, mk())
			cfg := mk()
			cfg.CI.IOSFailOnOwnSourceWarnings = true
			on := renderIOSCI(t, cfg)
			if on == off {
				t.Fatal("turning the gate on changed nothing")
			}
			// The two inserted blocks, exactly as the tokens produce them.
			vals := tokens.Values(cfg, "")
			stripped := on
			for _, tok := range []string{tokens.IOSCIOwnSourceWarningsBuild, tokens.IOSCIOwnSourceWarningsTest} {
				block := vals[tok]
				if block == "" {
					t.Fatalf("%s renders empty with the gate on", tok)
				}
				if strings.Count(stripped, block) != 1 {
					t.Fatalf("%s appears %d times in the rendered workflow, want exactly once", tok, strings.Count(stripped, block))
				}
				stripped = strings.Replace(stripped, block, "", 1)
			}
			if stripped != off {
				t.Errorf("the on-render differs from the off-render by more than the two gate steps (without them %d bytes, off %d)", len(stripped), len(off))
			}
			if got := strings.Count(on, "- name: "+gateStepName); got != 2 {
				t.Errorf("the on-render has %d gate steps, want 2 (one per job)", got)
			}
		})
	}
}

func TestOwnSourceWarningsGateOnRendersAfterBuildAndTests(t *testing.T) {
	for name, mk := range map[string]func() *config.Config{
		"solo": gateConfigOn,
		"two products": func() *config.Config {
			cfg := twoIOSProducts()
			cfg.CI.IOSFailOnOwnSourceWarnings = true
			return cfg
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := mk()
			for job, want := range map[string]struct{ prev, log string }{
				"build-release": {"Build Release", "xcodebuild-release.log"},
				"test":          {"Run Tests", "xcodebuild.log"},
			} {
				run, prev, wd, ifCond := gateStep(t, cfg, job)
				if !strings.HasPrefix(prev, want.prev) && !strings.Contains(prev, "secrets keys") {
					t.Errorf("%s: gate follows %q, want it right after %q (or the bundle-secrets check that follows it)", job, prev, want.prev)
				}
				if !strings.Contains(run, want.log) {
					t.Errorf("%s: the gate does not read %s:\n%s", job, want.log, run)
				}
				if !strings.Contains(run, gateDefaultAllowlist) {
					t.Errorf("%s: the gate does not read the default allowlist %s", job, gateDefaultAllowlist)
				}
				if wd != "." {
					t.Errorf("%s: gate working-directory = %q, want \".\" (the repo root, where the logs are written)", job, wd)
				}
				// No `if:` — it must run exactly when the step before it passed, never
				// be skippable on its own.
				if ifCond != "" {
					t.Errorf("%s: gate carries if: %q; it must run on the default success() condition", job, ifCond)
				}
			}
		})
	}
}

func TestOwnSourceWarningsGateHonoursACustomAllowlistPath(t *testing.T) {
	cfg := gateConfigOn()
	custom := "ci/warning-allowlist.txt"
	cfg.CI.IOSOwnSourceWarningsAllowlistSetting = &custom
	for _, job := range []string{"build-release", "test"} {
		run, _, _, _ := gateStep(t, cfg, job)
		if !strings.Contains(run, custom) {
			t.Errorf("%s: custom allowlist %s is not in the gate", job, custom)
		}
		if strings.Contains(run, gateDefaultAllowlist) {
			t.Errorf("%s: the default allowlist is still in the gate beside the custom one", job)
		}
	}
}

// ---- scanner behaviour ------------------------------------------------------

type gateRun struct {
	exit int
	out  string
}

// runGate renders the gate for one job, executes its `run:` body with bash in a
// scratch repository, and returns the exit code and combined output. The log is
// written under logName; allowlist, when non-nil, is written to the allowlist
// path. A nil logContent writes no log at all.
func runGate(t *testing.T, cfg *config.Config, job string, logContent *string, allowlist *string) (gateRun, string) {
	t.Helper()
	run, _, _, _ := gateStep(t, cfg, job)
	repo := t.TempDir()
	logName := "xcodebuild.log"
	if job == "build-release" {
		logName = "xcodebuild-release.log"
	}
	if logContent != nil {
		if err := os.WriteFile(filepath.Join(repo, logName), []byte(strings.ReplaceAll(*logContent, "@REPO@", repo)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if allowlist != nil {
		path := filepath.Join(repo, cfg.CI.IOSOwnSourceWarningsAllowlist())
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(*allowlist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(t.TempDir(), "gate.sh")
	// GitHub runs a step with `bash -e {0}`; do the same so a script that relies
	// on not being under -e cannot pass here and fail there.
	if err := os.WriteFile(script, []byte(run), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", script)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GITHUB_WORKSPACE="+repo)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	res := gateRun{out: buf.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		res.exit = ee.ExitCode()
	default:
		t.Fatalf("could not run the gate: %v", err)
	}
	return res, repo
}

func strp(s string) *string { return &s }

const (
	ownWarn   = "@REPO@/App/Sources/Feature.swift:41:17: warning: capture of non-Sendable type 'Self.Type' in an isolated closure; this is an error in the Swift 6 language mode [#SendableMetatypes]\n"
	ownWarn2  = "@REPO@/App/Sources/Other.swift:9:3: warning: variable 'x' was never mutated; consider changing to 'let' constant\n"
	spmWarn   = "@REPO@/DerivedData/SourcePackages/checkouts/SomePackage/Sources/SomePackage/Thing.swift:88:5: warning: 'foo' is deprecated\n"
	ddWarn    = "@REPO@/DerivedData/Build/Intermediates.noindex/App.build/Debug-iphonesimulator/App.build/DerivedSources/GeneratedAssetSymbols.swift:12:1: warning: generated\n"
	watchDD   = "@REPO@/WatchDerivedData/SourcePackages/checkouts/P/Sources/P/Y.swift:2:2: warning: 'bar' is deprecated\n"
	outside   = "/Applications/Xcode.app/Contents/Developer/Platforms/iPhoneOS.platform/Developer/Library/Frameworks/Testing/Sources/Foo.swift:1:1: warning: toolchain warning\n"
	elsewhere = "/Users/someone-else/Packages/Dep/Sources/Dep/Z.swift:5:5: warning: a package outside the workspace\n"
	noise     = "CompileSwift normal arm64 /x/y.swift\nnote: something\n/x/y.swift:1:1: error: boom\n** BUILD SUCCEEDED **\n"
)

func TestOwnSourceWarningGateOutcomes(t *testing.T) {
	type tc struct {
		name      string
		log       string
		allowlist *string
		wantExit  int
		mustHave  []string
		mustNot   []string
	}
	for _, c := range []tc{
		{name: "an own-source warning fails", log: noise + ownWarn, wantExit: 1,
			mustHave: []string{"Feature.swift:41:17: warning:", "[#SendableMetatypes]", "1 "}},
		{name: "an SPM checkout warning passes", log: noise + spmWarn, wantExit: 0, mustNot: []string{"Thing.swift:88"}},
		{name: "a DerivedData warning passes", log: noise + ddWarn, wantExit: 0},
		{name: "a Watch DerivedData warning passes", log: noise + watchDD, wantExit: 0},
		{name: "a toolchain-path warning outside the workspace passes", log: noise + outside, wantExit: 0},
		{name: "a package elsewhere on the machine passes", log: noise + elsewhere, wantExit: 0},
		{name: "no warnings passes", log: noise, wantExit: 0},
		{name: "own and dependency together still fail, naming only the own one",
			log: noise + spmWarn + ownWarn + ddWarn, wantExit: 1,
			mustHave: []string{"Feature.swift:41:17"}, mustNot: []string{"Thing.swift:88", "GeneratedAssetSymbols"}},
		{name: "an allowlisted own-source warning passes", log: noise + ownWarn,
			allowlist: strp("# accepted until the framework ships the fix\nSendableMetatypes\\]\n"), wantExit: 0},
		{name: "an allowlisted warning does not excuse a different one", log: noise + ownWarn + ownWarn2,
			allowlist: strp("SendableMetatypes\n"), wantExit: 1,
			mustHave: []string{"Other.swift:9:3"}, mustNot: []string{"Feature.swift:41:17: warning: capture"}},
		{name: "the allowlist is matched against the whole line, path included", log: noise + ownWarn + ownWarn2,
			allowlist: strp("/App/Sources/Feature\\.swift:41:17: warning:\n"), wantExit: 1,
			mustHave: []string{"Other.swift:9:3"}},
		{name: "a missing allowlist file is an empty allowlist", log: noise + ownWarn, allowlist: nil, wantExit: 1},
		{name: "an allowlist of only blanks and comments allows nothing", log: noise + ownWarn,
			allowlist: strp("\n   \n# just a comment\n\t\n"), wantExit: 1, mustHave: []string{"Feature.swift:41:17"}},
		{name: "a blank line among real patterns does not allow everything", log: noise + ownWarn2,
			allowlist: strp("SendableMetatypes\n\nFeature\\.swift\n"), wantExit: 1, mustHave: []string{"Other.swift:9:3"}},
		{name: "a repeated warning is reported once", log: noise + ownWarn + ownWarn + ownWarn, wantExit: 1,
			mustHave: []string{"1 Swift warning"}},
		{name: "two distinct warnings are counted as two", log: noise + ownWarn + ownWarn2, wantExit: 1,
			mustHave: []string{"2 Swift warning"}},
		{name: "a relative path in the log is the repository's own", log: noise + "App/Sources/Rel.swift:3:4: warning: relative\n", wantExit: 1,
			mustHave: []string{"Rel.swift:3:4"}},
		{name: "an own path containing a space is still own", log: noise + "@REPO@/My App/Sources/Spacey.swift:3:4: warning: spaced\n", wantExit: 1,
			mustHave: []string{"Spacey.swift:3:4"}},
		{name: "an Objective-C warning is not a Swift warning", log: noise + "@REPO@/App/Legacy.m:3:4: warning: unused variable\n", wantExit: 0},
		{name: "a linker warning with no file is not an own-source warning", log: noise + "ld: warning: ignoring duplicate libraries: '-lc++'\n", wantExit: 0},
		{name: "an error line is not a warning", log: noise + "@REPO@/App/Sources/Feature.swift:41:17: error: nope\n", wantExit: 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, job := range []string{"build-release", "test"} {
				log := c.log
				res, _ := runGate(t, gateConfigOn(), job, &log, c.allowlist)
				if res.exit != c.wantExit {
					t.Fatalf("%s: exit = %d, want %d\n%s", job, res.exit, c.wantExit, res.out)
				}
				for _, s := range c.mustHave {
					if !strings.Contains(res.out, s) {
						t.Errorf("%s: output lacks %q:\n%s", job, s, res.out)
					}
				}
				for _, s := range c.mustNot {
					if strings.Contains(res.out, s) {
						t.Errorf("%s: output unexpectedly contains %q:\n%s", job, s, res.out)
					}
				}
			}
		})
	}
}

// The runner prints the path it was given, and on this Mac a temp directory is a
// symlink (/var -> /private/var). The gate must treat both spellings of the
// workspace as the repository, or a warning in a file the repo owns is called
// "outside the workspace" and passes.
func TestOwnSourceWarningGateFollowsASymlinkedWorkspace(t *testing.T) {
	// t.TempDir() is itself under the /var symlink on macOS, so resolve it: the
	// "real" spelling here has to be the physical one `pwd -P` prints.
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	// GITHUB_WORKSPACE is the spelling the runner hands xcodebuild; the shell's
	// own cwd may be the other one.
	for _, spelling := range []struct{ name, logged, cwd, workspace string }{
		{"logged by the real path, workspace and cwd the link", real, link, link},
		{"logged by the link, cwd the real path, workspace the link", link, real, link},
		{"logged by the link, workspace the real path, cwd the link", link, link, real},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			run, _, _, _ := gateStep(t, gateConfigOn(), "test")
			if err := os.WriteFile(filepath.Join(spelling.cwd, "xcodebuild.log"),
				[]byte(spelling.logged+"/App/Sources/Feature.swift:1:1: warning: spelled differently\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(t.TempDir(), "gate.sh")
			if err := os.WriteFile(script, []byte(run), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "-e", script)
			cmd.Dir = spelling.cwd
			cmd.Env = append(os.Environ(), "GITHUB_WORKSPACE="+spelling.workspace, "PWD="+spelling.cwd)
			out, err := cmd.CombinedOutput()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 1 {
				t.Fatalf("a warning in the repository's own file passed when the workspace was spelled two ways (err=%v)\n%s", err, out)
			}
		})
	}
}

// The failure modes that would let the gate pass without having looked.
func TestOwnSourceWarningGateFailsClosed(t *testing.T) {
	t.Run("a missing log fails", func(t *testing.T) {
		for _, job := range []string{"build-release", "test"} {
			res, _ := runGate(t, gateConfigOn(), job, nil, nil)
			if res.exit == 0 {
				t.Fatalf("%s: no log was written and the gate passed:\n%s", job, res.out)
			}
			if !strings.Contains(res.out, "::error::") {
				t.Errorf("%s: a missing log fails without an ::error:: annotation:\n%s", job, res.out)
			}
		}
	})
	t.Run("an empty log fails", func(t *testing.T) {
		empty := ""
		res, _ := runGate(t, gateConfigOn(), "test", &empty, nil)
		if res.exit == 0 {
			t.Fatalf("an empty log passed:\n%s", res.out)
		}
	})
	t.Run("an invalid allowlist regex fails even when no warning needs it", func(t *testing.T) {
		clean := "** BUILD SUCCEEDED **\n"
		res, _ := runGate(t, gateConfigOn(), "test", &clean, strp("([unclosed\n"))
		if res.exit == 0 {
			t.Fatalf("an allowlist that is not a valid regex passed:\n%s", res.out)
		}
		if !strings.Contains(res.out, "::error::") || !strings.Contains(res.out, gateDefaultAllowlist) {
			t.Errorf("the failure does not name the allowlist:\n%s", res.out)
		}
	})
	t.Run("an invalid allowlist regex fails when a warning would have used it", func(t *testing.T) {
		log := ownWarn
		res, _ := runGate(t, gateConfigOn(), "test", &log, strp("([unclosed\n"))
		if res.exit == 0 {
			t.Fatalf("an invalid allowlist let an own-source warning through:\n%s", res.out)
		}
	})
}

// A pass must say what it looked at, so a log that holds nothing recognisable
// cannot read as "verified": the summary line is the proof the scan ran.
func TestOwnSourceWarningGateReportsWhatItScanned(t *testing.T) {
	log := "CompileSwift normal arm64 /x.swift\n" + spmWarn + ownWarn
	res, _ := runGate(t, gateConfigOn(), "test", &log, strp("SendableMetatypes\n"))
	if res.exit != 0 {
		t.Fatalf("exit = %d:\n%s", res.exit, res.out)
	}
	for _, want := range []string{"Swift warnings", "own", "allowlisted"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("a passing run does not report %q:\n%s", want, res.out)
		}
	}
}
