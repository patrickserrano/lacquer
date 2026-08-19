package doctor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cfgOne() *config.Config {
	return &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"p"}}}}
}

// The whole point: a check that PASSES on known-bad input is broken, and doctor
// must say so. This is the shape of every serious defect found onboarding the
// fleet — a hook calling a removed flag, a build setting silently ignored, a
// gate that never ran.
func TestPassingOnBadInputIsReportedAsFailure(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"permissive check\"\nargv = [\"true\"]\nexpect = \"fail\"\n")

	var out bytes.Buffer
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a command that always succeeds must fail an expect=fail probe: %+v", res)
	}
	if !strings.Contains(out.String(), "not verifying what it claims") {
		t.Errorf("the output must explain WHY, got:\n%s", out.String())
	}
}

func TestRejectingBadInputPasses(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"strict check\"\nargv = [\"false\"]\nexpect = \"fail\"\n")

	res, err := Run(root, t.TempDir(), cfgOne(), nil, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("a command that rejects the fixture must satisfy expect=fail: %+v", res)
	}
}

// A missing tool is a finding, not a skip: the check it backs cannot be running
// anywhere this is true. That is the opposite of the rule for FIXERS, where a
// missing tool skips — a fixer leaves work undone, a check asserts compliance.
func TestMissingToolIsAFinding(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"ghost\"\nargv = [\"lacquer-no-such-binary\"]\nexpect = \"fail\"\n")

	res, err := Run(root, t.TempDir(), cfgOne(), nil, new(bytes.Buffer))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatal("a missing tool must be reported, not treated as a passing check")
	}
	if !strings.Contains(res[0].Detail, "not installed") {
		t.Errorf("detail should name the missing tool, got %q", res[0].Detail)
	}
}

// The fixture must never be written into the project — a deliberately malformed
// file landing in a working tree could be committed or picked up by a watcher.
func TestFixtureIsWrittenOutsideTheProject(t *testing.T) {
	root := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"where\"\nfile = \"Probe.swift\"\ncontent = \"bad\"\nargv = [\"false\"]\nexpect = \"fail\"\n")

	if _, err := Run(root, proj, cfgOne(), nil, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, "Probe.swift")); !os.IsNotExist(err) {
		t.Error("the fixture was written into the project directory")
	}
}

func TestRejectsAnUnknownExpectation(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"x\"\nargv = [\"true\"]\nexpect = \"maybe\"\n")
	if _, err := LoadProbes(root, "p"); err == nil {
		t.Fatal("expect must be validated at load, not silently treated as one of the two")
	}
}

// The hole `requires` exists to close, demonstrated in both directions.
//
// A probe whose argv[0] is a shell wrapper passes the argv[0] PATH check via
// /bin/sh. If the real tool inside the script is missing, the shell exits
// non-zero — and an expect="fail" probe reads any non-zero exit as "the check
// correctly rejected the bad input". The probe reports OK *because* the tool is
// absent. Every node-based check is in that shape, since its binary lives in
// node_modules/.bin rather than on PATH; the web profile shipped exactly this.
func TestMissingToolInsideAShellProbeIsNotAPass(t *testing.T) {
	root := t.TempDir()

	// Without `requires`: the false pass. Pinned so a regression is visible.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"unguarded\"\n"+
			"argv = [\"sh\", \"-c\", \"exec /nonexistent/bin/linter --check\"]\nexpect = \"fail\"\n")
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("precondition: an unguarded shell probe reports OK on a missing tool: %+v", res)
	}

	// With `requires`: reported as the missing tool it is.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"guarded\"\nrequires = [\"/nonexistent/bin/linter\"]\n"+
			"argv = [\"sh\", \"-c\", \"exec /nonexistent/bin/linter --check\"]\nexpect = \"fail\"\n")
	var out bytes.Buffer
	res, err = Run(root, t.TempDir(), cfgOne(), nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a probe whose required tool is missing must FAIL, not pass: %+v", res)
	}
	if !strings.Contains(out.String(), "does not exist") {
		t.Errorf("the output must name the missing tool, got:\n%s", out.String())
	}
}

// A bare name in `requires` is a PATH lookup; a path is a file that must exist
// and be executable. A present-but-non-executable file is the shape of a
// half-finished install, and must not read as available.
func TestRequiresDistinguishesPathLookupFromFilePath(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()

	notExec := filepath.Join(project, "tool")
	write(t, notExec, "#!/bin/sh\nexit 1\n") // 0644 — no executable bit

	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"non-executable\"\nrequires = [\"{component}/tool\"]\n"+
			"argv = [\"true\"]\nexpect = \"pass\"\n")
	var out bytes.Buffer
	res, err := Run(root, project, cfgOne(), nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a non-executable required file must fail the probe: %+v", res)
	}
	if !strings.Contains(out.String(), "not executable") {
		t.Errorf("output should say it is not executable, got:\n%s", out.String())
	}

	// A bare name that IS on PATH satisfies the requirement.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"on path\"\nrequires = [\"sh\"]\nargv = [\"true\"]\nexpect = \"pass\"\n")
	res, err = Run(root, project, cfgOne(), nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("a required tool present on PATH must satisfy the probe: %+v", res)
	}
}

// The hole `expect_output` exists to close: a non-zero exit is one bit, and a
// tool that aborted before it ever read the fixture sets that bit too.
//
// This is the same shape as TestMissingToolInsideAShellProbeIsNotAPass one level
// up. `requires` covers "the tool is not there"; nothing covered "the tool ran,
// hit an unreadable config / a removed flag / a deleted script, and gave up" —
// which is what the web profile shipped twice and what the core probes would
// have done for all five secret-scanner checks had check-secrets.sh gone
// missing.
func TestExpectOutputAssertsWhyTheCheckFailed(t *testing.T) {
	root := t.TempDir()

	// Precondition, pinned: without the field, a failure for the WRONG reason
	// reports OK. This is the bug.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"unasserted\"\nwhy = \"x\"\n"+
			"argv = [\"sh\", \"-c\", \"echo 'error: cannot read config file'; exit 1\"]\nexpect = \"fail\"\n")
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("precondition: any non-zero exit satisfies a bare expect=fail: %+v", res)
	}

	// With the field, the same wrong-reason failure is red — and says so.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"asserted\"\nwhy = \"x\"\n"+
			"argv = [\"sh\", \"-c\", \"echo 'error: cannot read config file'; exit 1\"]\n"+
			"expect = \"fail\"\nexpect_output = 'no-explicit-any'\n")
	var out bytes.Buffer
	res, err = Run(root, t.TempDir(), cfgOne(), nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a failure that does not match expect_output must be red: %+v", res)
	}
	if !strings.Contains(out.String(), "NOT for the reason this probe asserts") {
		t.Errorf("the output must distinguish a wrong-reason failure, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "cannot read config file") {
		t.Errorf("the output must show what the tool actually said, got:\n%s", out.String())
	}

	// And the INTENDED failure still passes.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"asserted\"\nwhy = \"x\"\n"+
			"argv = [\"sh\", \"-c\", \"echo 'probe.ts:1:1 lint/suspicious/no-explicit-any'; exit 1\"]\n"+
			"expect = \"fail\"\nexpect_output = 'no-explicit-any'\n")
	res, err = Run(root, t.TempDir(), cfgOne(), nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("the failure the probe asserts must satisfy it: %+v", res)
	}
}

// Exit 0 is one bit too. A pass probe can reach it without doing the thing it
// claims — the web deprecation probe did, via `! tool … | grep -q DEPRECATED`,
// where any non-deprecation failure printed no DEPRECATED, grep exited 1 and
// `!` turned that into success.
func TestExpectOutputAppliesToPassProbes(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"silent success\"\nwhy = \"x\"\n"+
			"argv = [\"true\"]\nexpect = \"pass\"\nexpect_output = 'checked 1 file'\n")
	var out bytes.Buffer
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].OK {
		t.Fatalf("a command that succeeded without saying what the probe asserts must be red: %+v", res)
	}
	if !strings.Contains(out.String(), "printed nothing at all") {
		t.Errorf("empty output should be named as such, got:\n%s", out.String())
	}
}

// Fail closed at load: a pattern that cannot be compiled, or one loose enough to
// match a command that printed nothing, is refused rather than quietly ignored.
// A probe whose assertion is never evaluated is a probe that proves nothing.
func TestExpectOutputIsValidatedAtLoad(t *testing.T) {
	for _, tc := range []struct{ name, pattern, want string }{
		{"uncompilable", `no-explicit-any(`, "not a valid regexp"},
		{"matches everything", `.*`, "matches empty output"},
		{"optional group only", `(DEPRECATED)?`, "matches empty output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
				"[[probe]]\nname = \"x\"\nwhy = \"x\"\nargv = [\"false\"]\nexpect = \"fail\"\n"+
					"expect_output = '"+tc.pattern+"'\n")
			_, err := LoadProbes(root, "p")
			if err == nil {
				t.Fatalf("expect_output %q must be rejected at load", tc.pattern)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should say %q, got %v", tc.want, err)
			}
		})
	}
}

// A doctor.toml written before this field existed must load and behave exactly
// as it did. Backward compatibility is not a nicety here: every project in the
// fleet carries a synced copy.
func TestProbesWithoutExpectOutputAreUnchanged(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"legacy\"\nwhy = \"x\"\n"+
			"argv = [\"sh\", \"-c\", \"echo 'anything at all'; exit 1\"]\nrequires = [\"sh\"]\nexpect = \"fail\"\n")
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("a probe declaring no expect_output must behave as before: %+v", res)
	}
}

// Outputs that real breakage produces, none of which any shipped probe may
// accept. A regex broad enough to match one of these has rebuilt the hole
// `expect_output` closes: it would report green while the tool was failing for a
// reason that has nothing to do with the check being proved.
//
// The empty string is in the list because a command that printed nothing is the
// purest form of "never ran"; LoadProbes rejects patterns matching it, and this
// keeps that visible next to its siblings.
var wrongReasonOutputs = []string{
	"",
	"bash: /project/scripts/check-secrets.sh: No such file or directory",
	"sh: line 1: swiftlint: command not found",
	"/project/scripts/check-secrets.sh: line 12: syntax error near unexpected token `('",
	"error: Unknown option '--config'",
	"error: unexpected argument '--nope' found",
	"Error: Could not find a configuration file",
	"error: Found argument '--strict' which wasn't expected",
	"Segmentation fault: 11",
	"error: Uncaught (in promise) TypeError: undefined is not a function",
	"npm error could not determine executable to run",
	"error: internal error: entered unreachable code",
}

// Every probe this lacquer ships must parse; any probe whose argv[0] is a shell
// must declare `requires`; and any probe expecting a FAILURE must say which
// failure, because "exited non-zero" is satisfied by a tool that never reached
// the fixture. Each of those three rules exists because the shipped set violated
// it and a broken check reported green.
func TestShippedProbesAreSound(t *testing.T) {
	root := "../.."
	for _, profile := range []string{"ios", "web", "supabase"} {
		probes, err := LoadProbes(root, profile)
		if err != nil {
			t.Errorf("%s: %v", profile, err)
			continue
		}
		for _, p := range probes {
			assertShippedProbeIsSound(t, profile, p)
		}
	}
}

func assertShippedProbeIsSound(t *testing.T, layer string, p Probe) {
	t.Helper()
	isShell := p.Argv[0] == "sh" || p.Argv[0] == "bash" || p.Argv[0] == "/bin/sh"
	if isShell && len(p.Requires) == 0 {
		t.Errorf("%s: probe %q runs through a shell but declares no `requires`; a missing tool would satisfy it silently",
			layer, p.Name)
	}
	if p.Why == "" {
		t.Errorf("%s: probe %q has no `why`; the failure output is what teaches the reader", layer, p.Name)
	}
	if p.Expect == "fail" && p.ExpectOutput == "" {
		t.Errorf("%s: probe %q expects a failure but does not say WHICH failure; add `expect_output` naming the"+
			" diagnostic only the intended rejection produces, or a config error, a deleted script or exit 127"+
			" will report this check as working", layer, p.Name)
	}
	if p.ExpectOutput == "" {
		return
	}
	re, err := compileExpectOutput(p.ExpectOutput)
	if err != nil {
		t.Errorf("%s: probe %q: %v", layer, p.Name, err)
		return
	}
	for _, bad := range wrongReasonOutputs {
		if re.MatchString(bad) {
			t.Errorf("%s: probe %q has expect_output %s, which matches %q — output produced by the check"+
				" BREAKING rather than by it working. Assert the rule id or message the intended rejection prints.",
				layer, p.Name, strconv.Quote(p.ExpectOutput), bad)
		}
	}
}

// core ships checks every project gets — the secrets scanner above all — and
// they had nowhere to put a probe until the core layer existed. Probes are
// loaded from core/doctor.toml and run once against the project root, not per
// component, because those scripts are repo-wide.
func TestCoreLayerProbesRun(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "core", "doctor.toml"),
		"[[probe]]\nname = \"core check rejects bad input\"\nwhy = \"x\"\n"+
			"argv = [\"false\"]\nexpect = \"fail\"\n")
	// A profile probe too, to prove core runs ALONGSIDE profiles rather than
	// replacing them.
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"profile check\"\nwhy = \"x\"\nargv = [\"false\"]\nexpect = \"fail\"\n")

	var out bytes.Buffer
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want core + profile: %+v", len(res), res)
	}
	if res[0].Profile != CoreLayer {
		t.Errorf("core probe should run first and be attributed to %q, got %q", CoreLayer, res[0].Profile)
	}
	if !strings.Contains(out.String(), "core check rejects bad input") ||
		!strings.Contains(out.String(), "profile check") {
		t.Errorf("both layers should be reported:\n%s", out.String())
	}
}

// A lacquer with no core/doctor.toml must behave exactly as before.
func TestCoreLayerIsOptional(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "profiles", "p", "doctor.toml"),
		"[[probe]]\nname = \"only profile\"\nwhy = \"x\"\nargv = [\"false\"]\nexpect = \"fail\"\n")
	res, err := Run(root, t.TempDir(), cfgOne(), nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want just the profile probe: %+v", len(res), res)
	}
}

// Every probe core ships must parse and carry a `why` — the failure output is
// where a reader learns what broke.
func TestShippedCoreProbesAreValid(t *testing.T) {
	probes, err := LoadProbes("../..", CoreLayer)
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) == 0 {
		t.Fatal("core ships no probes; the secrets scanner should have one")
	}
	for _, p := range probes {
		// core's probes are the highest-stakes in the harness and every one of
		// them runs the secrets scanner through `bash`, so they are held to the
		// same contract as the profiles': say which failure, not just that one
		// happened. Delete check-secrets.sh and a bare expect="fail" reports all
		// five green.
		assertShippedProbeIsSound(t, CoreLayer, p)
	}
}

// A project can declare several profiles on one component, and its CI jobs run
// on different runners. rail declares ["ios", "supabase"] and its supabase job
// runs on ubuntu, where doctor ran the iOS probes, found no swiftlint, and
// failed six checks — correctly (a missing tool IS a finding) but uselessly,
// because that runner was never going to have Xcode.
func TestRunScopesToNamedProfiles(t *testing.T) {
	lq := t.TempDir()
	writeFile(t, filepath.Join(lq, "profiles", "alpha", "doctor.toml"),
		"[[probe]]\nname = \"alpha probe\"\nargv = [\"sh\", \"-c\", \"exit 1\"]\nexpect = \"fail\"\n")
	writeFile(t, filepath.Join(lq, "profiles", "beta", "doctor.toml"),
		"[[probe]]\nname = \"beta probe\"\nargv = [\"sh\", \"-c\", \"exit 1\"]\nexpect = \"fail\"\n")
	cfg := &config.Config{Components: []config.Component{
		{Path: ".", Profiles: []string{"alpha", "beta"}},
	}}
	proj := t.TempDir()

	all, err := Run(lq, proj, cfg, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("with no scope every declared profile runs: got %d, want 2", len(all))
	}

	scoped, err := Run(lq, proj, cfg, []string{"alpha"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Profile != "alpha" {
		t.Fatalf("scoping must run only the named profile, got %+v", scoped)
	}
}

// Scoping must never become a way to make a failing probe disappear: anything
// the caller NAMES still has to work.
func TestScopingDoesNotSuppressTheNamedProfilesFailures(t *testing.T) {
	lq := t.TempDir()
	// expect="fail" but the command SUCCEEDS — a broken check.
	writeFile(t, filepath.Join(lq, "profiles", "alpha", "doctor.toml"),
		"[[probe]]\nname = \"broken\"\nargv = [\"sh\", \"-c\", \"exit 0\"]\nexpect = \"fail\"\n")
	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"alpha"}}}}
	res, err := Run(lq, t.TempDir(), cfg, []string{"alpha"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(Failures(res)) != 1 {
		t.Errorf("a named profile's broken probe must still fail, got %+v", res)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
