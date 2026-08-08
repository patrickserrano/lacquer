package doctor

import (
	"bytes"
	"os"
	"path/filepath"
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
	res, err := Run(root, t.TempDir(), cfgOne(), &out)
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

	res, err := Run(root, t.TempDir(), cfgOne(), new(bytes.Buffer))
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

	res, err := Run(root, t.TempDir(), cfgOne(), new(bytes.Buffer))
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

	if _, err := Run(root, proj, cfgOne(), new(bytes.Buffer)); err != nil {
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
	res, err := Run(root, t.TempDir(), cfgOne(), &bytes.Buffer{})
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
	res, err = Run(root, t.TempDir(), cfgOne(), &out)
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
	res, err := Run(root, project, cfgOne(), &out)
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
	res, err = Run(root, project, cfgOne(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("a required tool present on PATH must satisfy the probe: %+v", res)
	}
}

// Every probe this lacquer ships must parse, and any probe whose argv[0] is a
// shell must declare `requires` — that combination is unsound without it, and
// the web profile shipped it once already.
func TestShippedProbesAreSound(t *testing.T) {
	root := "../.."
	for _, profile := range []string{"ios", "web", "supabase"} {
		probes, err := LoadProbes(root, profile)
		if err != nil {
			t.Errorf("%s: %v", profile, err)
			continue
		}
		for _, p := range probes {
			isShell := p.Argv[0] == "sh" || p.Argv[0] == "bash" || p.Argv[0] == "/bin/sh"
			if isShell && len(p.Requires) == 0 {
				t.Errorf("%s: probe %q runs through a shell but declares no `requires`; a missing tool would satisfy it silently",
					profile, p.Name)
			}
			if p.Why == "" {
				t.Errorf("%s: probe %q has no `why`; the failure output is what teaches the reader", profile, p.Name)
			}
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
	res, err := Run(root, t.TempDir(), cfgOne(), &out)
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
	res, err := Run(root, t.TempDir(), cfgOne(), &bytes.Buffer{})
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
		if p.Why == "" {
			t.Errorf("core probe %q has no `why`", p.Name)
		}
		if len(p.Requires) == 0 && (p.Argv[0] == "sh" || p.Argv[0] == "bash") {
			t.Errorf("core probe %q runs through a shell but declares no `requires`", p.Name)
		}
	}
}
