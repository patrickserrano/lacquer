package shipped

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/doctor"
	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/sync"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// The web CI's `Docs (TypeDoc)` step (#522 U8 follow-up). Until it, only
// lefthook's pre-push `docs` hook ran TypeDoc, so an undocumented export merged
// green whenever the hook did not run.

const webDocsStep = "Docs (TypeDoc)"

// typeDocNodeModulesEnv names a node_modules directory holding typedoc and
// typescript. Installing them is a network fetch, so the Go suite never does:
// lacquer's CI installs pinned versions, points this at them and requires the
// tests below to pass (a skip there is a failure). Locally they skip unless set.
const typeDocNodeModulesEnv = "LACQUER_TEST_TYPEDOC_NODE_MODULES"

func typeDocNodeModules(t *testing.T) string {
	t.Helper()
	nm := os.Getenv(typeDocNodeModulesEnv)
	if nm == "" {
		if os.Getenv("LACQUER_TEST_REQUIRE_TYPEDOC") != "" {
			t.Fatalf("LACQUER_TEST_REQUIRE_TYPEDOC is set but %s is not, so the docs gate would go unproved", typeDocNodeModulesEnv)
		}
		t.Skipf("%s is not set; CI sets it and requires this test to pass", typeDocNodeModulesEnv)
	}
	pkg, err := os.ReadFile(filepath.Join(nm, "typedoc", "package.json"))
	if err != nil {
		t.Fatalf("%s=%s holds no typedoc: %v", typeDocNodeModulesEnv, nm, err)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkg, &meta); err != nil || meta.Version == "" {
		t.Fatalf("could not read the installed typedoc's version: %v", err)
	}
	t.Logf("typedoc %s", meta.Version)
	return nm
}

// The step is the hook's invocation, not a second opinion: same relaxation
// script and manifest, the same single state that skips, and the component's
// own typedoc with no flags, so typedoc.json alone decides what is checked.
func TestWebDocsStepIsThePrePushHooksInvocation(t *testing.T) {
	step := webCheckStep(t, renderWebCI(t), webDocsStep)
	hook := readFile(t, filepath.Join(root(t), "profiles", "web", "root", "lefthook.yml"))
	for _, want := range []string{
		"for f in scripts/docs-relaxation.sh .lacquer.toml; do",
		"scripts/docs-relaxation.sh .lacquer.toml); then",
		`if [ "$state" = "relaxed" ]; then`,
	} {
		if !strings.Contains(step, want) || !strings.Contains(hook, want) {
			t.Errorf("the step and the pre-push hook do not both carry %q", want)
		}
	}
	bare := regexp.MustCompile(`(?m)\./node_modules/\.bin/typedoc(;|$)`)
	for name, body := range map[string]string{"step": step, "hook": hook} {
		if !bare.MatchString(body) {
			t.Errorf("the %s does not run ./node_modules/.bin/typedoc with no arguments", name)
		}
	}
	if regexp.MustCompile(`typedoc [-.]`).MatchString(step) {
		t.Error("the step passes TypeDoc arguments, so it would check something other than typedoc.json says")
	}
	// No escape hatch: a step that cannot fail is not a gate.
	if strings.Contains(step, "|| true") || strings.Contains(step, "continue-on-error") {
		t.Error("the docs step swallows a failure")
	}
}

// The step sits in the job every web component's CI runs, after Build, for any
// component path: it is in the template, so every rendered web CI carries it.
func TestWebDocsStepRendersInTheCheckJobAfterBuild(t *testing.T) {
	rendered := renderWebCI(t)
	var doc struct {
		Jobs map[string]struct {
			If       string           `yaml:"if"`
			Defaults map[string]any   `yaml:"defaults"`
			Steps    []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, st := range doc.Jobs["check"].Steps {
		n, _ := st["name"].(string)
		names = append(names, n)
		if n == webDocsStep {
			if _, ok := st["if"]; ok {
				t.Error("the docs step carries its own `if:`, so it can skip where the job runs")
			}
			// The root, for the manifest and the relaxation script; the run
			// then enters the component (TestWebDocsStepRunsInTheComponent).
			if wd, _ := st["working-directory"].(string); wd != "." {
				t.Errorf("the docs step's working-directory is %q, want the repository root", wd)
			}
		}
	}
	build, docs := -1, -1
	for i, n := range names {
		switch n {
		case "Build":
			build = i
		case webDocsStep:
			docs = i
		}
	}
	if docs < 0 || build < 0 || docs < build {
		t.Errorf("want %q after Build in the check job, got steps %q", webDocsStep, names)
	}
	if n := strings.Count(rendered, "name: "+webDocsStep); n != 1 {
		t.Errorf("rendered web CI carries %d docs steps, want 1", n)
	}
}

// docsProject syncs a git repository whose root is one web component, with
// webKeys appended to its manifest, writes a tsconfig and a documented
// src/index.ts, and links node_modules to nm. It returns the project path.
func docsProject(t *testing.T, nm, webKeys string) string {
	t.Helper()
	return docsProjectAt(t, nm, ".", webKeys)
}

// docsProjectAt is docsProject with the web component at path ("." or "admin").
func docsProjectAt(t *testing.T, nm, path, webKeys string) string {
	t.Helper()
	project := t.TempDir()
	gittest.Init(t, project, "-q")
	writeIn(t, project, ".lacquer.toml", `[project]
name = "demo"
project_name = "Demo"
scheme = "Demo"
bundle_id = "com.x.demo"
asc_app_id = "1"
xcodeproj = "Demo.xcodeproj"
swift_version = "6"
github_org = "acme"

[[component]]
path = "`+path+`"
profiles = ["web"]
`+webKeys)
	c := filepath.Join(project, filepath.FromSlash(path))
	writeIn(t, c, "package.json", "{\n  \"name\": \"demo\",\n  \"private\": true\n}\n")
	writeIn(t, c, "tsconfig.json", `{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true},"include":["src"]}`)
	writeIn(t, c, "src/index.ts", "/** Documented. */\nexport const top = 1\n")
	if _, err := sync.Run(root(t), project, false); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := os.Symlink(nm, filepath.Join(c, "node_modules")); err != nil {
		t.Fatal(err)
	}
	return project
}

func writeIn(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runDocsStep runs the rendered step's body the way GitHub's default shell
// does (`bash -e`), in the component, and returns its output and exit code.
func runDocsStep(t *testing.T, project string) (string, int) {
	t.Helper()
	return runDocsStepAt(t, project, "")
}

// runDocsStepAt renders the step for a component at prefix ("" or "admin/")
// and runs it from the repository root, as its working-directory says.
func runDocsStepAt(t *testing.T, project, prefix string) (string, int) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "web", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, missing := tokens.Substitute(string(raw), tokens.Values(&config.Config{Root: project}, prefix))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	body := webCheckStep(t, rendered, webDocsStep)
	if !strings.Contains(body, `cd "`+prefix+`."`) {
		t.Fatalf("the step does not enter the component %q:\n%s", prefix, body)
	}
	script := filepath.Join(t.TempDir(), "step.sh")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", script)
	cmd.Dir = project
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

// green and red assert the step's verdict, and that TypeDoc really ran to it.
func green(t *testing.T, project, why string) string {
	t.Helper()
	out, code := runDocsStep(t, project)
	if code != 0 || !strings.Contains(out, "html generated at ./docs-site") {
		t.Fatalf("%s: want the docs step green with a site written, got exit %d:\n%s", why, code, out)
	}
	return out
}

func red(t *testing.T, project, why string, names ...string) {
	t.Helper()
	out, code := runDocsStep(t, project)
	if code == 0 {
		t.Fatalf("%s: want the docs step red, got exit 0:\n%s", why, out)
	}
	for _, n := range names {
		if !strings.Contains(out, n+" (Variable)") || !strings.Contains(out, "does not have any documentation") {
			t.Errorf("%s: the failure does not name %s as undocumented:\n%s", why, n, out)
		}
	}
	if !strings.Contains(out, "::error::TypeDoc failed") {
		t.Errorf("%s: no ::error:: annotation:\n%s", why, out)
	}
}

func TestWebDocsStepAgainstRealTypeDoc(t *testing.T) {
	nm := typeDocNodeModules(t)

	t.Run("default config", func(t *testing.T) {
		p := docsProject(t, nm, "")
		green(t, p, "a documented barrel")
		writeIn(t, p, "src/index.ts", "/** Documented. */\nexport const top = 1\nexport const bare = 2\n")
		red(t, p, "an undocumented export in the barrel", "bare")
	})

	t.Run("expand covers a file nobody listed, and not test files", func(t *testing.T) {
		p := docsProject(t, nm, "\n[web]\ntypedoc_entry_points = [\"src\"]\ntypedoc_entry_point_strategy = \"expand\"\n")
		green(t, p, "a documented tree")
		writeIn(t, p, "src/feature/deep/added.ts", "export const added = 1\n")
		red(t, p, "a new undocumented file under the expanded directory", "added")

		writeIn(t, p, "src/feature/deep/added.ts", "/** Now documented. */\nexport const added = 1\n")
		writeIn(t, p, "src/feature/added.test.ts", "export const testHelper = 1\n")
		writeIn(t, p, "src/feature/added.spec.tsx", "export const specHelper = 1\n")
		writeIn(t, p, "src/__tests__/fixture.ts", "export const fixture = 1\n")
		out := green(t, p, "undocumented exports only in test files")
		for _, n := range []string{"testHelper", "specHelper", "fixture"} {
			if strings.Contains(out, n) {
				t.Errorf("TypeDoc read the test-only export %s:\n%s", n, out)
			}
		}
	})

	t.Run("a glob covers a new file without a strategy", func(t *testing.T) {
		p := docsProject(t, nm, "\n[web]\ntypedoc_entry_points = [\"src/**/*.ts\"]\n")
		writeIn(t, p, "src/a.test.ts", "export const testHelper = 1\n")
		green(t, p, "a documented tree beside an undocumented test file")
		writeIn(t, p, "src/nested/new.ts", "export const fresh = 1\n")
		red(t, p, "a new undocumented file the glob matches", "fresh")
	})

	t.Run("only an active relaxation skips", func(t *testing.T) {
		p := docsProject(t, nm, "")
		writeIn(t, p, "src/index.ts", "export const bare = 2\n")
		manifest := readFile(t, filepath.Join(p, ".lacquer.toml"))
		writeIn(t, p, ".lacquer.toml", manifest+"\n[baseline.relax]\ndocumentation = { until = \"2999-01-01\", reason = \"test, #1\" }\n")
		out, code := runDocsStep(t, p)
		if code != 0 || !strings.Contains(out, "::notice::Documentation is relaxed") || strings.Contains(out, "html generated") {
			t.Errorf("an active relaxation: want a skip with a notice, got exit %d:\n%s", code, out)
		}
		writeIn(t, p, ".lacquer.toml", manifest+"\n[baseline.relax]\ndocumentation = { until = \"2000-01-01\", reason = \"test, #1\" }\n")
		red(t, p, "an expired relaxation", "bare")
	})

	t.Run("a nested component, from the repository root", func(t *testing.T) {
		p := docsProjectAt(t, nm, "admin", "\n[web]\ntypedoc_entry_points = [\"src\"]\ntypedoc_entry_point_strategy = \"expand\"\n")
		if out, code := runDocsStepAt(t, p, "admin/"); code != 0 || !strings.Contains(out, "html generated at ./docs-site") {
			t.Fatalf("a documented nested component: exit %d:\n%s", code, out)
		}
		writeIn(t, p, "admin/src/more/added.ts", "export const nested = 1\n")
		out, code := runDocsStepAt(t, p, "admin/")
		if code == 0 || !strings.Contains(out, "nested (Variable)") {
			t.Fatalf("an undocumented file in a nested component: want red naming it, got exit %d:\n%s", code, out)
		}
	})

	t.Run("a missing relaxation script fails rather than skipping", func(t *testing.T) {
		p := docsProject(t, nm, "")
		if err := os.Remove(filepath.Join(p, "scripts", "docs-relaxation.sh")); err != nil {
			t.Fatal(err)
		}
		out, code := runDocsStep(t, p)
		if code == 0 || !strings.Contains(out, "::error::scripts/docs-relaxation.sh is missing") {
			t.Errorf("want a failure naming the missing script, got exit %d:\n%s", code, out)
		}
	})
}

// Every shipped TypeDoc probe, through doctor, against real TypeDoc, in a
// project that declares nothing and in one that declares "expand": each must
// report OK, which for the expect="fail" probes means TypeDoc rejected its
// fixture for the reason the probe names.
func TestDoctorWebTypeDocProbesAgainstRealTypeDoc(t *testing.T) {
	nm := typeDocNodeModules(t)
	probes, err := doctor.LoadProbes(root(t), "web")
	if err != nil {
		t.Fatal(err)
	}
	typedoc := map[string]bool{}
	for _, p := range probes {
		if p.Check == "typedoc-entry-points" || strings.Contains(strings.Join(p.Argv, " "), "node_modules/.bin/typedoc") {
			typedoc[p.Name] = true
		}
	}
	if len(typedoc) < 5 {
		t.Fatalf("found %d TypeDoc probes, want at least 5 (four tool probes and the entry-point check)", len(typedoc))
	}
	for name, keys := range map[string]string{
		"undeclared": "",
		"expand":     "\n[web]\ntypedoc_entry_points = [\"src\"]\ntypedoc_entry_point_strategy = \"expand\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			project := docsProject(t, nm, keys)
			cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			results, err := doctor.Run(root(t), project, cfg, []string{"web"}, &out)
			if err != nil {
				t.Fatal(err)
			}
			ran := 0
			for _, r := range results {
				if !typedoc[r.Name] {
					continue
				}
				ran++
				if !r.OK {
					t.Errorf("probe %q failed: %s", r.Name, r.Detail)
				}
			}
			if ran != len(typedoc) {
				t.Fatalf("ran %d of the %d TypeDoc probes:\n%s", ran, len(typedoc), out.String())
			}
		})
	}
}
