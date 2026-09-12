package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// precommit-swift.sh is the fail-closed wrapper every iOS project's SwiftLint
// and SwiftFormat pre-commit hooks go through (profiles/ios/root/scripts/
// precommit-swift.sh, wired up by profiles/ios/root/.pre-commit-config.yaml).
//
// SwiftLint applies a config's `excluded:` list only while walking a
// directory, never to paths named explicitly on the command line — and this
// hook always receives explicit staged paths. That made swiftlint-docs, whose
// .swiftlint-docs.yml deliberately excludes `**/Tests` and `**/*Tests`, lint
// exactly the files the config says not to on every test-only commit.
//
// These tests exercise the wrapper's branching around `--force-exclude`
// against a STUB `swiftlint` on PATH rather than the real binary, so they run
// on CI's ubuntu `test` job where swiftlint is not installed. The stub is
// configured per test case to reproduce the exact exit codes and messages
// measured against real SwiftLint 0.65.1 (see the wrapper's own comments):
//
//   - every argument excluded: exit 1, "Error: No lintable files found at
//     paths: ..." — the wrapper must turn this into exit 0.
//   - a real violation: nonzero exit, output shown — must still fail.
//   - exit 1 with an unrelated message: must still fail (proves the wrapper
//     checks the MESSAGE, not just the code).
//   - some-but-not-all arguments excluded: SwiftLint silently drops the
//     excluded ones and lints the rest, exit 0 — the wrapper must not invent
//     an "N excluded" claim it cannot back up, and a violation among the
//     surviving files must still fail.

// stubScript is a POSIX-sh `swiftlint` stand-in. It writes its own argv to
// argvFile (so a test can assert --force-exclude was actually passed) and then
// behaves exactly as SWIFTLINT_STUB_EXIT/STDOUT/STDERR direct it — a fixed
// script, varied entirely through environment, so every scenario reproduces a
// MEASURED SwiftLint exit rather than reimplementing SwiftLint's own exclusion
// semantics.
const stubScript = `#!/bin/sh
printf '%s\n' "$@" > "$SWIFTLINT_STUB_ARGV_FILE"
if [ -n "${SWIFTLINT_STUB_STDOUT:-}" ]; then
  printf '%s\n' "$SWIFTLINT_STUB_STDOUT"
fi
if [ -n "${SWIFTLINT_STUB_STDERR:-}" ]; then
  printf '%s\n' "$SWIFTLINT_STUB_STDERR" >&2
fi
exit "${SWIFTLINT_STUB_EXIT:-0}"
`

// precommitScript reads the real shipped wrapper and renders it as a
// root-layout project would (COMPONENT_PREFIX -> ""), so tests can pass plain
// relative paths without also proving the separate token-substitution step.
func precommitScript(t *testing.T) string {
	t.Helper()
	src := filepath.Join(root(t), "profiles", "ios", "root", "scripts", "precommit-swift.sh")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading precommit-swift.sh: %v", err)
	}
	rendered := strings.ReplaceAll(string(b), "{{COMPONENT_PREFIX}}", "")

	dir := t.TempDir()
	dst := filepath.Join(dir, "precommit-swift.sh")
	if err := os.WriteFile(dst, []byte(rendered), 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

// stubBin writes the stub swiftlint into its own PATH-only directory and
// returns that directory plus the file the stub will record its argv to.
func stubBin(t *testing.T) (binDir, argvFile string) {
	t.Helper()
	binDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "swiftlint"), []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	argvFile = filepath.Join(t.TempDir(), "argv")
	return binDir, argvFile
}

// precommitCase runs the wrapper with a stub swiftlint configured by env, in a
// scratch project directory, and returns its combined output and exit code.
type stubEnv struct {
	exit   string
	stdout string
	stderr string
}

func runPrecommit(t *testing.T, tool string, files []string, env stubEnv) (out string, exitCode int, argv string) {
	t.Helper()
	binDir, argvFile := stubBin(t)
	script := precommitScript(t)
	projectDir := t.TempDir()
	// The wrapper cds into COMPONENT_PREFIX when non-empty; rendered empty
	// here (root layout), so it stays in projectDir and the config names below
	// are irrelevant to the stub, which never reads them.
	for _, cfg := range []string{".swiftlint.yml", ".swiftlint-docs.yml"} {
		if err := os.WriteFile(filepath.Join(projectDir, cfg), []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	args := append([]string{tool}, files...)
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SWIFTLINT_STUB_ARGV_FILE="+argvFile,
		"SWIFTLINT_STUB_EXIT="+env.exit,
		"SWIFTLINT_STUB_STDOUT="+env.stdout,
		"SWIFTLINT_STUB_STDERR="+env.stderr,
	)
	outBytes, err := cmd.CombinedOutput()
	out = string(outBytes)
	exitCode = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("running wrapper: %v\n%s", err, out)
		}
	}
	if b, rerr := os.ReadFile(argvFile); rerr == nil {
		argv = string(b)
	}
	return out, exitCode, argv
}

// (a) every staged path excluded: the wrapper must turn SwiftLint's exit 1 +
// "No lintable files found" into a successful hook run.
func TestPrecommitSwiftAllExcludedSucceeds(t *testing.T) {
	out, code, _ := runPrecommit(t, "swiftlint-docs", []string{"MultimeterTests/FooTests.swift"}, stubEnv{
		exit:   "1",
		stderr: "Error: No lintable files found at paths: 'MultimeterTests/FooTests.swift'",
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 when every staged path is excluded by the config:\n%s", code, out)
	}
	if !strings.Contains(out, "excluded") {
		t.Errorf("output does not say the staged files were excluded, so this reads as a silent no-op rather than an explained skip:\n%s", out)
	}
}

// (b) a real violation must still fail, with SwiftLint's own output visible.
func TestPrecommitSwiftViolationFails(t *testing.T) {
	out, code, _ := runPrecommit(t, "swiftlint-docs", []string{"App/Config.swift"}, stubEnv{
		exit:   "2",
		stderr: "App/Config.swift:3:8: error: Missing Docs Violation: public declarations should be documented (missing_docs)",
	})
	if code == 0 {
		t.Fatalf("exit = 0, want nonzero for a real violation:\n%s", out)
	}
	if !strings.Contains(out, "Missing Docs Violation") {
		t.Errorf("SwiftLint's own violation output was not shown:\n%s", out)
	}
}

// (c) exit 1 with an UNRELATED message must still fail. This is the case that
// distinguishes "checks the message" from "checks the code": a wrapper that
// treats any exit 1 as an exclusion would pass this silently.
func TestPrecommitSwiftOtherExit1MessageStillFails(t *testing.T) {
	out, code, _ := runPrecommit(t, "swiftlint-docs", []string{"App/Config.swift"}, stubEnv{
		exit:   "1",
		stderr: "Error: Invalid configuration: unknown rule 'made_up_rule'",
	})
	if code == 0 {
		t.Fatalf("exit = 0, want nonzero — exit 1 here is a real error, not the no-lintable-files case:\n%s", out)
	}
	if !strings.Contains(out, "unknown rule") {
		t.Errorf("SwiftLint's own error output was not shown:\n%s", out)
	}
}

// (d) empty argv is still refused before SwiftLint is ever invoked.
func TestPrecommitSwiftEmptyArgvRefused(t *testing.T) {
	out, code, argv := runPrecommit(t, "swiftlint-docs", nil, stubEnv{exit: "0"})
	if code == 0 {
		t.Fatalf("exit = 0, want nonzero for an empty staged-file list:\n%s", out)
	}
	if argv != "" {
		t.Errorf("swiftlint was invoked at all for an empty file list (argv file = %q); the empty-argv guard must fire before that", argv)
	}
	if !strings.Contains(out, "no staged Swift files") {
		t.Errorf("refusal message missing or reworded:\n%s", out)
	}
}

// (e) some but not all staged paths excluded: SwiftLint silently drops the
// excluded ones and lints the rest, exit 0 — measured live against SwiftLint
// 0.65.1 (an excluded test file + one app file -> "0 violations, 0 serious in
// 1 file", exit 0, with no indication anything was dropped). The wrapper must
// succeed here without claims about how many files were excluded.
func TestPrecommitSwiftMixedExcludeSucceeds(t *testing.T) {
	out, code, _ := runPrecommit(t, "swiftlint-docs",
		[]string{"MultimeterTests/FooTests.swift", "App/Config.swift"},
		stubEnv{exit: "0", stdout: "Done linting! Found 0 violations, 0 serious in 1 file."})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 — SwiftLint silently excluded the test file and passed on the rest:\n%s", code, out)
	}
	if strings.Contains(out, "excluded") {
		t.Errorf("output claims something about exclusion in the mixed case, where SwiftLint gives no such signal — this is an invented claim:\n%s", out)
	}
	if strings.Contains(out, "2 staged Swift file(s)") == false {
		t.Errorf("closing line should still report what was HANDED to swiftlint (2 files), just not how many were checked/excluded:\n%s", out)
	}
}

// (e), variant: the same mixed case, but the surviving (non-excluded) file has
// a real violation. It must still fail — success in the mixed case must not
// become a blanket pass.
func TestPrecommitSwiftMixedExcludeWithViolationFails(t *testing.T) {
	out, code, _ := runPrecommit(t, "swiftlint-docs",
		[]string{"MultimeterTests/FooTests.swift", "App/Config.swift"},
		stubEnv{exit: "2", stderr: "App/Config.swift:3:8: error: Missing Docs Violation: public declarations should be documented (missing_docs)"})
	if code == 0 {
		t.Fatalf("exit = 0, want nonzero — the surviving file has a real violation:\n%s", out)
	}
	if !strings.Contains(out, "Missing Docs Violation") {
		t.Errorf("SwiftLint's own violation output was not shown:\n%s", out)
	}
}

// --force-exclude must actually be passed. Removing it is exactly the
// regression this fix closes (SwiftLint stops respecting excluded: for
// explicit paths without it), and mutation-testing that by hand is how it was
// found: dropping the flag from the wrapper makes this test fail because the
// stub's recorded argv no longer contains it.
func TestPrecommitSwiftPassesForceExclude(t *testing.T) {
	_, code, argv := runPrecommit(t, "swiftlint-docs", []string{"App/Config.swift"}, stubEnv{exit: "0"})
	if code != 0 {
		t.Fatalf("wrapper failed unexpectedly: exit %d", code)
	}
	fields := strings.Fields(argv)
	var found bool
	for _, f := range fields {
		if f == "--force-exclude" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("swiftlint was invoked without --force-exclude; argv was:\n%s", argv)
	}
}

// Sanity check that the stub-and-render harness itself works: an arbitrary
// nonzero exit from the stub propagates through the wrapper unchanged.
func TestPrecommitSwiftStubHarnessSanity(t *testing.T) {
	_, code, _ := runPrecommit(t, "swiftlint-docs", []string{"App/Config.swift"}, stubEnv{exit: "7"})
	if want := 7; code != want {
		t.Fatalf("exit = %d, want %d (exit code plumbing)", code, want)
	}
}
