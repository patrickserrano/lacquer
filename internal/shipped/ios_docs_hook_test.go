package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// profiles/ios/root/scripts/docs-hook.sh is the iOS twin of the lefthook docs
// commands fixed in #387 (lefthook_root_paths_test.go). It read the relaxation
// with the same `scripts/docs-relaxation.sh .lacquer.toml 2>/dev/null || echo
// none`, so a missing script, a missing manifest, or a script that errored was
// indistinguishable from "read the manifest, found nothing". Here that fails
// closed — the docs gate runs — so the harm is a correctly dated relaxation
// being silently ignored rather than a gate being bypassed. It is still a state
// that cannot be told apart from working, which is the defect class.
//
// These tests sync a real iOS project, then EXECUTE the synced hook the way
// pre-commit does — the entry from the rendered .pre-commit-config.yaml, from
// the repository root — wrapping a stub that prints toolRan instead of
// SwiftLint.

// preCommitConfig is the part of a rendered .pre-commit-config.yaml these tests
// read.
type preCommitConfig struct {
	Repos []struct {
		Hooks []struct {
			ID    string `yaml:"id"`
			Entry string `yaml:"entry"`
		} `yaml:"hooks"`
	} `yaml:"repos"`
}

// iosDocsHook syncs rootapp and returns it with the repo-relative path of the
// script its swiftlint-docs hook runs, and a stub standing in for the wrapped
// SwiftLint command.
func iosDocsHook(t *testing.T) (p *project, hook, stub string) {
	t.Helper()
	p = fromFixture(t, "rootapp")
	p.sync()

	var cfg preCommitConfig
	if err := yaml.Unmarshal([]byte(p.read(".pre-commit-config.yaml")), &cfg); err != nil {
		t.Fatalf(".pre-commit-config.yaml is not valid YAML: %v", err)
	}
	for _, r := range cfg.Repos {
		for _, h := range r.Hooks {
			if h.ID == "swiftlint-docs" {
				hook = strings.Fields(h.Entry)[0]
			}
		}
	}
	// The test is only about the file pre-commit actually runs; if the wiring
	// moved, say so rather than testing a script nothing calls.
	if hook != "scripts/docs-hook.sh" {
		t.Fatalf("swiftlint-docs runs %q, want scripts/docs-hook.sh; this test exercises the wrong file", hook)
	}

	stub = filepath.Join(t.TempDir(), "swiftlint-docs-stub")
	writeExe(t, stub, "#!/bin/sh\necho "+toolRan+"\n")
	return p, hook, stub
}

// runDocsHook runs the hook from dir (repo-relative) as `<hook> <stub>`. From
// the root it is invoked exactly as pre-commit's entry names it; from anywhere
// else it has to be named by absolute path.
func runDocsHook(t *testing.T, p *project, dir, hook, stub string) (string, int) {
	t.Helper()
	if dir != "" {
		hook = filepath.Join(p.root, filepath.FromSlash(hook))
	}
	return runAsLefthook(t, p, dir, hook+" "+stub, "")
}

// TestIOSDocsHookHonoursRelaxation: the control and the case the hook exists
// for. With no relaxation SwiftLint runs; with a dated one it is skipped and the
// hook says why.
func TestIOSDocsHookHonoursRelaxation(t *testing.T) {
	t.Parallel()
	p, hook, stub := iosDocsHook(t)

	out, code := runDocsHook(t, p, "", hook, stub)
	if code != 0 || !strings.Contains(out, toolRan) {
		t.Fatalf("with no relaxation, %s did not run the documentation check (exit %d):\n%s", hook, code, out)
	}

	appendManifest(t, p, relaxedDocs)
	out, code = runDocsHook(t, p, "", hook, stub)
	if code != 0 {
		t.Fatalf("with a valid relaxation, %s failed (exit %d):\n%s", hook, code, out)
	}
	if strings.Contains(out, toolRan) {
		t.Errorf("a valid [baseline.relax] documentation entry was ignored; SwiftLint ran anyway:\n%s", out)
	}
	if !strings.Contains(out, "relaxed") {
		t.Errorf("the skip did not say why; a silent skip reads as a pass:\n%s", out)
	}
}

// TestIOSDocsHookHonoursRelaxationFromASubdirectory: pre-commit always runs the
// entry from the repository root, but nothing stops the script being run by
// hand from elsewhere. The relaxation lives at the repository root either way.
func TestIOSDocsHookHonoursRelaxationFromASubdirectory(t *testing.T) {
	t.Parallel()
	p, hook, stub := iosDocsHook(t)
	appendManifest(t, p, relaxedDocs)

	out, code := runDocsHook(t, p, "Rootapp", hook, stub)
	if code != 0 || strings.Contains(out, toolRan) {
		t.Errorf("run from Rootapp/, %s did not honour the relaxation at the repository root (exit %d):\n%s",
			hook, code, out)
	}
}

// TestIOSDocsHookExpiredStillRuns: resolving the inputs strictly must not have
// changed the semantics. An expired entry fails, as it does in CI.
func TestIOSDocsHookExpiredStillRuns(t *testing.T) {
	t.Parallel()
	p, hook, stub := iosDocsHook(t)
	appendManifest(t, p, expiredDocs)

	out, code := runDocsHook(t, p, "", hook, stub)
	if code == 0 {
		t.Errorf("an EXPIRED documentation relaxation passed %s:\n%s", hook, out)
	}
	if !strings.Contains(out, "EXPIRED") {
		t.Errorf("the failure does not say the relaxation expired:\n%s", out)
	}
}

// TestIOSDocsHookMissingInputsFailLoudly: a missing script or manifest is a
// broken install, not "no relaxation", and the failure has to name the file.
func TestIOSDocsHookMissingInputsFailLoudly(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"scripts/docs-relaxation.sh", ".lacquer.toml"} {
		t.Run("without "+missing, func(t *testing.T) {
			t.Parallel()
			p, hook, stub := iosDocsHook(t)
			appendManifest(t, p, relaxedDocs)
			if err := os.Remove(filepath.Join(p.root, filepath.FromSlash(missing))); err != nil {
				t.Fatal(err)
			}

			out, code := runDocsHook(t, p, "", hook, stub)
			if code == 0 {
				t.Errorf("%s exited 0 with %s missing; a check that cannot read its inputs must fail, not "+
					"fall through to \"not relaxed\":\n%s", hook, missing, out)
			}
			if !strings.Contains(out, missing) {
				t.Errorf("the failure does not name %s, so nobody can act on it:\n%s", missing, out)
			}
			if strings.Contains(out, toolRan) {
				t.Errorf("SwiftLint ran anyway with %s missing; the failure must stop the hook:\n%s", missing, out)
			}
		})
	}
}

// TestIOSDocsHookBrokenScriptFailsLoudly: a script that is present but errors
// is the same case one step later. It prints `relaxed` before failing, so a
// hook that ignored the exit status would skip — the wrong answer twice over.
func TestIOSDocsHookBrokenScriptFailsLoudly(t *testing.T) {
	t.Parallel()
	p, hook, stub := iosDocsHook(t)
	writeExe(t, filepath.Join(p.root, "scripts", "docs-relaxation.sh"), "#!/bin/sh\necho relaxed\nexit 7\n")

	out, code := runDocsHook(t, p, "", hook, stub)
	if code == 0 {
		t.Errorf("%s exited 0 although scripts/docs-relaxation.sh failed:\n%s", hook, out)
	}
	if !strings.Contains(out, "docs-relaxation.sh failed") {
		t.Errorf("the failure does not say the relaxation script failed:\n%s", out)
	}
	if strings.Contains(out, toolRan) {
		t.Errorf("SwiftLint ran although the relaxation could not be read:\n%s", out)
	}
}
