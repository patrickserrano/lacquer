package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The seeding step exists because a gitignored Secrets.xcconfig is an Xcode
// project's base configuration, and a clean checkout has none. These tests RUN
// the shipped shell rather than grepping it, because every failure this step has
// ever had looked identical from the outside: the step ran, exited 0, seeded
// something, and the build died later on a path the step never touched.
//
// #109 was the first. kit keeps an example at both the component root and a
// nested source directory, the step copied only the root one, and the Release
// build failed on `Kit/Kit/Secrets.xcconfig`. The fix — seed every example
// beside itself — then broke the OTHER shape: a project whose only example is at
// the component root while its project reads {{COMPONENT_PREFIX}}<Scheme>/. That
// one ran red every night for a week on a scheduled Docs job that never runs on
// a PR, so no check in this repo and no PR anywhere reported it.
//
// A grep would have passed at every point in that history. Only running it
// distinguishes "seeded a file" from "seeded the file the project opens".

// seedShell is the seeding shell extracted from a shipped file, with tokens
// substituted the way a sync would.
type seedShell struct {
	file string
	body string
}

// ciSeedSteps returns the body of every "Create Secrets.xcconfig" run: block in
// the iOS profile's workflows. scripts/build-docs.sh carried a fourth copy of
// the same prologue until the DocC job and the script itself were removed.
//
// Sourced from the shipped files themselves so a fourth copy, or a fifth, is
// covered the moment it is added — the duplication is deliberate (see the
// comments in ci.yml) and a test that hardcoded three would silently stop
// covering a new one.
func ciSeedSteps(t *testing.T, prefix, scheme string) []seedShell {
	t.Helper()
	sub := strings.NewReplacer(
		"{{COMPONENT_PREFIX}}", prefix,
		"{{IOS_CI_SCHEME}}", scheme,
		"{{SCHEME}}", scheme,
	)

	var out []seedShell
	wf, err := filepath.Glob(filepath.Join(root(t), "profiles", "ios", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range wf {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, body := range extractRunBlocks(string(raw), "Create Secrets.xcconfig") {
			out = append(out, seedShell{file: filepath.Base(f), body: sub.Replace(body)})
		}
	}
	if len(out) == 0 {
		t.Fatal("no \"Create Secrets.xcconfig\" run: block found in profiles/ios/workflows; " +
			"this test would pass having run nothing")
	}
	return out
}

// extractRunBlocks returns the dedented body of each `run: |` block belonging to
// a step whose name contains want.
func extractRunBlocks(src, want string) []string {
	var out []string
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], "- name:") || !strings.Contains(lines[i], want) {
			continue
		}
		j := i
		for j < len(lines) && !strings.HasSuffix(strings.TrimRight(lines[j], " "), "run: |") {
			j++
		}
		if j == len(lines) {
			continue
		}
		indent := len(lines[j]) - len(strings.TrimLeft(lines[j], " ")) + 2
		var body []string
		for k := j + 1; k < len(lines); k++ {
			l := lines[k]
			if strings.TrimSpace(l) == "" {
				body = append(body, "")
				continue
			}
			if len(l)-len(strings.TrimLeft(l, " ")) < indent {
				break
			}
			body = append(body, l[indent:])
		}
		out = append(out, strings.Join(body, "\n"))
	}
	return out
}

// TestSecretsSeedReachesTheFileTheProjectReads runs every shipped copy of the
// seeding shell against the real layouts in this fleet.
func TestSecretsSeedReachesTheFileTheProjectReads(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("no bash: %v", err)
	}

	cases := []struct {
		name string
		// prefix and scheme as a sync would render them.
		prefix, scheme string
		// examples committed in the project, relative to the repo root.
		examples []string
		// dirs that must exist for the layout to be itself.
		dirs []string
		// want and notWant are repo-relative Secrets.xcconfig paths.
		want, notWant []string
	}{
		{
			// The shape that ran red nightly: the lacquer writes the example at
			// the component root (internal/shipped's assertComponentLayout
			// asserts that path), the Xcode project resolves its
			// baseConfigurationReference against the app target's own folder.
			name:     "example at the component root, project reads the scheme directory",
			prefix:   "ios/",
			scheme:   "App",
			examples: []string{"ios/Secrets.xcconfig.example"},
			dirs:     []string{"ios/App"},
			want:     []string{"ios/App/Secrets.xcconfig"},
		},
		{
			// #109's shape. Both examples are seeded beside themselves, and the
			// scheme directory gets nothing: this project has already said it
			// keeps the two together, so a placeholder there would be a file
			// nothing reads.
			name:     "examples at two depths are each seeded beside themselves",
			prefix:   "",
			scheme:   "Kit",
			examples: []string{"Secrets.xcconfig.example", "Kit/Kit/Secrets.xcconfig.example"},
			dirs:     []string{"Kit/Kit"},
			want:     []string{"Secrets.xcconfig", "Kit/Kit/Secrets.xcconfig"},
			notWant:  []string{"Kit/Secrets.xcconfig"},
		},
		{
			// A project with no runtime keys must stay a no-op, not an error.
			name:    "no example at all is a silent no-op",
			prefix:  "ios/",
			scheme:  "App",
			dirs:    []string{"ios/App"},
			notWant: []string{"ios/Secrets.xcconfig", "ios/App/Secrets.xcconfig"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, step := range ciSeedSteps(t, tc.prefix, tc.scheme) {
				dir := t.TempDir()
				for _, d := range tc.dirs {
					if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				for _, e := range tc.examples {
					p := filepath.Join(dir, filepath.FromSlash(e))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte("KEY = placeholder\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}

				cmd := exec.Command("bash", "-euo", "pipefail", "-c", step.body)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: seeding shell failed: %v\n%s", step.file, err, out)
				}

				for _, w := range tc.want {
					if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(w))); err != nil {
						t.Errorf("%s: %s was not seeded. The step exited 0 having written nothing the "+
							"build can open, which is the failure this test exists for: xcodebuild then "+
							"fails with \"Unable to open base configuration reference file\" on a path no "+
							"step mentioned.", step.file, w)
					}
				}
				for _, n := range tc.notWant {
					if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(n))); err == nil {
						t.Errorf("%s: seeded %s, which nothing reads in this layout.", step.file, n)
					}
				}
			}
		})
	}
}

// TestSecretsSeedNeverOverwritesRealKeys guards the other direction. On a
// self-hosted runner the workspace outlives the job, and a release writes REAL
// credentials into the same filename; a seed that clobbered them would ship an
// app wired to `appl_xxxxxxxx` and nothing would look wrong until the revenue
// did not arrive.
func TestSecretsSeedNeverOverwritesRealKeys(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("no bash: %v", err)
	}
	const real = "KEY = the-real-one\n"
	for _, step := range ciSeedSteps(t, "ios/", "App") {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "ios", "App"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"ios/Secrets.xcconfig.example", "ios/Secrets.xcconfig", "ios/App/Secrets.xcconfig"} {
			body := "KEY = placeholder\n"
			if !strings.HasSuffix(f, ".example") {
				body = real
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(f)), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("bash", "-euo", "pipefail", "-c", step.body)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: seeding shell failed: %v\n%s", step.file, err, out)
		}
		for _, f := range []string{"ios/Secrets.xcconfig", "ios/App/Secrets.xcconfig"} {
			got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != real {
				t.Errorf("%s: overwrote %s with the placeholder. A release writes real credentials to "+
					"this path and the runner's workspace outlives the job.", step.file, f)
			}
		}
	}
}

// TestEveryIOSJobThatBuildsTheProjectSeedsSecrets is the coverage half. The
// Docs publisher went red nightly for a week because the gap was in a workflow
// nobody looked at; a new workflow that builds the project and forgets this step
// would repeat that exactly, and only on a schedule where no PR would show it.
func TestEveryIOSJobThatBuildsTheProjectSeedsSecrets(t *testing.T) {
	// Workflows that drive a build of {{XCODEPROJ}}, and how they do it.
	// dead-code.yml (Periphery) used to be a third entry here -- removed along
	// with the workflow itself once Periphery's local cache grew to 48GB and
	// the tool went unmaintained upstream. docs.yml was removed along with docs
	// publishing entirely, and scripts/build-docs.sh, which it delegated to,
	// followed once nothing was left to call it.
	builders := map[string]string{
		"ci.yml": "xcodebuild",
	}
	for file, how := range builders {
		raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", file))
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if !strings.Contains(body, "Secrets.xcconfig.example") {
			t.Errorf("profiles/ios/workflows/%s builds the Xcode project (%s) but never seeds "+
				"Secrets.xcconfig. A clean checkout has none — it is gitignored — so the build fails "+
				"at project load with an error naming a file no step in this workflow mentions.", file, how)
		}
	}
}
