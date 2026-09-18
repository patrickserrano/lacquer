package shipped

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A lefthook command with `root: "admin/"` runs INSIDE admin/. Two things it
// reads do not live there: scripts/docs-relaxation.sh (shipped by core/root to
// the repository root) and .lacquer.toml (always at the repository root). The
// docs commands resolved both relative to the component, so in every nested
// web or supabase component the script was "not found" — and
// `2>/dev/null || echo none` turned that into "no relaxation". A correctly
// dated [baseline.relax] documentation entry was silently ignored, TypeDoc ran,
// and momfriend could not push.
//
// Nothing asserted on it because every existing check looked at the TEXT of the
// rendered command, and the text was fine. What was wrong was the directory it
// ran in. So these tests run the rendered command, from the directory lefthook
// would run it in, inside a real git repository, and look at what it did.

// relaxedDocs is a [baseline.relax] documentation entry that stays valid for
// as long as this test suite exists.
const relaxedDocs = "\n[baseline.relax]\ndocumentation = { until = \"2999-12-31\", reason = \"test\" }\n"

// expiredDocs is the same entry, long past its date.
const expiredDocs = "\n[baseline.relax]\ndocumentation = { until = \"2000-01-01\", reason = \"test\" }\n"

// toolRan is what the stub documentation tools print, so a test can tell "the
// relaxation skipped the check" apart from "the check ran and passed".
const toolRan = "STUB-DOC-TOOL-RAN"

// hookCommand returns one rendered command from a synced project's lefthook.yml.
func hookCommand(t *testing.T, p *project, hook, cmd string) (root, run string) {
	t.Helper()
	var doc lefthookHooks
	if err := yaml.Unmarshal([]byte(p.read("lefthook.yml")), &doc); err != nil {
		t.Fatalf("lefthook.yml is not valid YAML: %v", err)
	}
	c, ok := doc[hook].Commands[cmd]
	if !ok {
		t.Fatalf("%s.%s is not in the rendered lefthook.yml", hook, cmd)
	}
	if strings.TrimSpace(c.Run) == "" {
		t.Fatalf("%s.%s has an empty run", hook, cmd)
	}
	return c.Root, c.Run
}

// runAsLefthook executes a rendered `run:` the way lefthook does on unix — `sh
// -c` — from the command's `root:` inside the project. binDir, when set, is put
// first on PATH so a stub can stand in for a toolchain binary.
func runAsLefthook(t *testing.T, p *project, root, run, binDir string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", run)
	cmd.Dir = filepath.Join(p.root, filepath.FromSlash(root))
	path := os.Getenv("PATH")
	if binDir != "" {
		path = binDir + string(os.PathListSeparator) + path
	}
	cmd.Env = append(os.Environ(), "PATH="+path)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	default:
		t.Fatalf("could not run %q: %v", run, err)
		return "", -1
	}
}

// writeExe writes an executable file, creating its directory.
func writeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// appendManifest adds text to the project's .lacquer.toml.
func appendManifest(t *testing.T, p *project, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(p.root, ".lacquer.toml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// stubTypedoc installs a component-local typedoc that only announces itself.
func stubTypedoc(t *testing.T, p *project, component string) {
	t.Helper()
	writeExe(t, filepath.Join(p.root, filepath.FromSlash(component), "node_modules", ".bin", "typedoc"),
		"#!/bin/sh\necho "+toolRan+"\n")
}

// stubDeno returns a PATH directory holding a deno that only announces itself.
func stubDeno(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeExe(t, filepath.Join(dir, "deno"), "#!/bin/sh\necho "+toolRan+" \"$@\"\n")
	return dir
}

// docsCase is one rendered docs command and how to make its tool observable.
type docsCase struct {
	name, hook, cmd string
	// prepare installs the stub tool and returns a PATH directory (or "").
	prepare func(t *testing.T, p *project, root string) string
}

var docsCases = []docsCase{
	{
		name: "web pre-push typedoc", hook: "pre-push", cmd: "docs",
		prepare: func(t *testing.T, p *project, root string) string { stubTypedoc(t, p, root); return "" },
	},
	{
		name: "supabase pre-commit deno doc", hook: "pre-commit", cmd: "docs",
		prepare: func(t *testing.T, _ *project, _ string) string { return stubDeno(t) },
	},
}

// TestDocsRelaxationHonouredInNestedComponent is the momfriend defect: a web
// component under admin/ and a supabase one under server/, each with a dated
// relaxation at the repository root.
func TestDocsRelaxationHonouredInNestedComponent(t *testing.T) {
	t.Parallel()
	for _, tc := range docsCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := fromFixture(t, "multistack")
			p.sync()
			// The supabase docs command lives in pre-commit, the web one in
			// pre-push, so in this fixture neither name clashes and each keeps
			// its bare name. Find the one scoped to a nested component.
			root, run := hookCommand(t, p, tc.hook, tc.cmd)
			if root == "" {
				t.Fatalf("%s.%s rendered with an empty root in the multistack fixture; this test needs a nested component", tc.hook, tc.cmd)
			}
			bin := tc.prepare(t, p, root)

			// Control first: with no relaxation the tool must run, or the
			// assertion below cannot tell "skipped" from "never wired up".
			out, code := runAsLefthook(t, p, root, run, bin)
			if code != 0 || !strings.Contains(out, toolRan) {
				t.Fatalf("with no relaxation, %s.%s in %s did not run its documentation tool (exit %d):\n%s",
					tc.hook, tc.cmd, root, code, out)
			}

			appendManifest(t, p, relaxedDocs)
			out, code = runAsLefthook(t, p, root, run, bin)
			if code != 0 {
				t.Fatalf("with a valid relaxation, %s.%s in %s failed (exit %d):\n%s", tc.hook, tc.cmd, root, code, out)
			}
			if strings.Contains(out, toolRan) {
				t.Errorf("a valid [baseline.relax] documentation entry at the repository root was IGNORED by "+
					"%s.%s running in the nested component %s — the documentation tool ran anyway. The command "+
					"resolved scripts/docs-relaxation.sh or .lacquer.toml inside the component, where neither "+
					"exists:\n%s", tc.hook, tc.cmd, root, out)
			}
			if !strings.Contains(out, "relaxed") {
				t.Errorf("the skip did not say why; a silent skip reads as a pass:\n%s", out)
			}
		})
	}
}

// TestDocsRelaxationExpiredStillRunsInNestedComponent: an expired entry is not
// a skip. Resolving the manifest correctly must not have turned every state
// into "relaxed".
func TestDocsRelaxationExpiredStillRunsInNestedComponent(t *testing.T) {
	t.Parallel()
	p := fromFixture(t, "multistack")
	p.sync()
	root, run := hookCommand(t, p, "pre-push", "docs")
	stubTypedoc(t, p, root)
	appendManifest(t, p, expiredDocs)
	out, _ := runAsLefthook(t, p, root, run, "")
	if !strings.Contains(out, toolRan) {
		t.Errorf("an EXPIRED documentation relaxation skipped TypeDoc; time-boxed means the time runs out:\n%s", out)
	}
}

// TestDocsRelaxationHonouredInRootComponent: a root-layout component renders
// `root: ""`, which is the case that always worked. It must keep working.
func TestDocsRelaxationHonouredInRootComponent(t *testing.T) {
	t.Parallel()
	p := fromInit(t, "web", map[string]string{
		"package.json": "{ \"name\": \"acme\", \"packageManager\": \"pnpm@10.20.0\" }\n",
	})
	p.sync()
	root, run := hookCommand(t, p, "pre-push", "docs")
	if root != "" {
		t.Fatalf("a root-layout web project rendered pre-push.docs with root %q, want \"\"", root)
	}
	stubTypedoc(t, p, root)

	out, code := runAsLefthook(t, p, root, run, "")
	if code != 0 || !strings.Contains(out, toolRan) {
		t.Fatalf("with no relaxation, TypeDoc did not run (exit %d):\n%s", code, out)
	}
	appendManifest(t, p, relaxedDocs)
	out, code = runAsLefthook(t, p, root, run, "")
	if code != 0 || strings.Contains(out, toolRan) {
		t.Errorf("a root-layout project's valid documentation relaxation was not honoured (exit %d):\n%s", code, out)
	}
}

// TestDocsRelaxationMissingInputsFailLoudly: a missing script or manifest is a
// broken install, not "no relaxation". Reporting it as "none" is exactly how
// the nested-component defect stayed invisible — the command could not tell
// "I read the manifest and found nothing" from "I never read it".
func TestDocsRelaxationMissingInputsFailLoudly(t *testing.T) {
	t.Parallel()
	for _, missing := range []string{"scripts/docs-relaxation.sh", ".lacquer.toml"} {
		for _, tc := range docsCases {
			t.Run(tc.name+" without "+missing, func(t *testing.T) {
				t.Parallel()
				p := fromFixture(t, "multistack")
				p.sync()
				root, run := hookCommand(t, p, tc.hook, tc.cmd)
				bin := tc.prepare(t, p, root)
				appendManifest(t, p, relaxedDocs)
				if err := os.Remove(filepath.Join(p.root, filepath.FromSlash(missing))); err != nil {
					t.Fatal(err)
				}

				out, code := runAsLefthook(t, p, root, run, bin)
				if code == 0 {
					t.Errorf("%s.%s exited 0 with %s missing from the repository root; a check that cannot "+
						"read its inputs must fail, not fall through to \"not relaxed\":\n%s", tc.hook, tc.cmd, missing, out)
				}
				if !strings.Contains(out, missing) {
					t.Errorf("the failure does not name %s, so nobody can act on it:\n%s", missing, out)
				}
				if strings.Contains(out, toolRan) {
					t.Errorf("the documentation tool ran anyway with %s missing; the failure must stop the command:\n%s", missing, out)
				}
			})
		}
	}
}

// TestDocsRelaxationBrokenScriptFailsLoudly: a script that is present but
// errors is the same case one step later. Swallowing its failure back into
// "none" survived every other test here, so it gets its own.
func TestDocsRelaxationBrokenScriptFailsLoudly(t *testing.T) {
	t.Parallel()
	for _, tc := range docsCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := fromFixture(t, "multistack")
			p.sync()
			root, run := hookCommand(t, p, tc.hook, tc.cmd)
			bin := tc.prepare(t, p, root)
			writeExe(t, filepath.Join(p.root, "scripts", "docs-relaxation.sh"), "#!/bin/sh\necho relaxed\nexit 7\n")

			out, code := runAsLefthook(t, p, root, run, bin)
			if code == 0 {
				t.Errorf("%s.%s exited 0 although scripts/docs-relaxation.sh failed:\n%s", tc.hook, tc.cmd, out)
			}
			if !strings.Contains(out, "docs-relaxation.sh failed") {
				t.Errorf("the failure does not say the relaxation script failed:\n%s", out)
			}
			if strings.Contains(out, toolRan) {
				t.Errorf("the documentation tool ran although the relaxation could not be read:\n%s", out)
			}
		})
	}
}

// TestDenoTestPermitsNoFiles: a supabase component with edge functions and no
// tests could never push, because `deno test` exits non-zero when it finds
// nothing. CI already permits the empty set and says so; the hook now does the
// same, as a printed warning since there are no annotations in a hook.
func TestDenoTestPermitsNoFiles(t *testing.T) {
	t.Parallel()
	p := fromFixture(t, "multistack")
	p.sync()
	root, run := hookCommand(t, p, "pre-push", "test-supabase")
	if !strings.Contains(run, "--permit-no-files") {
		t.Errorf("pre-push.test-supabase runs %q without --permit-no-files; a component with no tests can never push", run)
	}

	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno is not installed; the rendered flag is asserted above, the behaviour needs deno")
	}
	fn := filepath.Join(p.root, filepath.FromSlash(root), "supabase", "functions", "hello", "index.ts")
	if err := os.MkdirAll(filepath.Dir(fn), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fn, []byte("export const hello = (): string => \"hi\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runAsLefthook(t, p, root, run, "")
	if code != 0 {
		t.Fatalf("with edge functions and no tests, pre-push.test-supabase failed (exit %d):\n%s", code, out)
	}
	if !strings.Contains(out, "untested") {
		t.Errorf("zero tests passed without saying so; green must not read as tested:\n%s", out)
	}

	// Control: a failing test still fails. --permit-no-files must not have
	// turned the command into one that cannot fail.
	test := filepath.Join(filepath.Dir(fn), "index_test.ts")
	if err := os.WriteFile(test, []byte("Deno.test(\"fails\", () => { throw new Error(\"boom\"); });\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runAsLefthook(t, p, root, run, "")
	if code == 0 {
		t.Errorf("a failing Deno test passed pre-push.test-supabase:\n%s", out)
	}
	if strings.Contains(out, "untested") {
		t.Errorf("the no-tests warning printed when a test exists:\n%s", out)
	}
}
