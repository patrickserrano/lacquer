package shipped

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// The existing rootapp fixture pins the default workflow renders, including
// the event-qualified dispatch title and the intended ShellCheck repairs.
// A changed timeout default or an unconditional bootstrap changes the digest.
func TestCISeamsDefaultRender(t *testing.T) {
	cfg, err := config.Load(filepath.Join(root(t), "internal/shipped/testdata/projects/rootapp/.lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"ci.yml": "2ab51f9483ed2176cdc5babe7e1ecd9e9ab4d91889f437e12feac7d0a3b8708a", "release.yml": "889565f6bb872ad0fd90936c8b3f5609f529b7996e46d25da5ca783d2ae840f9"} {
		raw, err := os.ReadFile(filepath.Join(root(t), "profiles/ios/workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
		if len(missing) > 0 {
			t.Fatal(missing)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(out))); got != want {
			t.Fatalf("%s default render digest = %s, want %s", name, got, want)
		}
	}
}

func TestCISeamsCustomTimeoutRender(t *testing.T) {
	for _, cfg := range []*config.Config{soloConfig(), twoIOSProducts()} {
		defaults := renderIOSCI(t, cfg)
		if _, err := toml.Decode("[ci]\nios_test_job_timeout_minutes = 90\nios_test_step_timeout_minutes = 60\n", cfg); err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Jobs map[string]struct {
				Timeout int `yaml:"timeout-minutes"`
				Steps   []struct {
					Name    string
					Timeout int `yaml:"timeout-minutes"`
				}
			}
		}
		custom := renderIOSCI(t, cfg)
		if err := yaml.Unmarshal([]byte(custom), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Jobs["test"].Timeout != 90 {
			t.Fatalf("test job timeout = %d", doc.Jobs["test"].Timeout)
		}
		found := false
		for _, st := range doc.Jobs["test"].Steps {
			if st.Name == "Run Tests" {
				found = true
				if st.Timeout != 60 {
					t.Fatalf("test step timeout = %d", st.Timeout)
				}
			}
		}
		if !found {
			t.Fatal("Run Tests absent")
		}
		if doc.Jobs["build-release"].Timeout != 25 {
			t.Fatal("build timeout changed")
		}
		restored := strings.Replace(custom, "timeout-minutes: 90", "timeout-minutes: 20", 1)
		restored = strings.Replace(restored, "timeout-minutes: 60", "timeout-minutes: 10", 1)
		if restored != defaults {
			t.Fatal("custom timeouts changed more than the two limits, including test selection")
		}
	}
}

func TestPackageResolvedBootstrap(t *testing.T) {
	for _, layout := range []struct{ prefix, source string }{{"", "ci_scripts/Package.resolved"}, {"ios/", "CI Config/Package.resolved"}} {
		prefix, source := layout.prefix, layout.source
		for _, linked := range []bool{false, true} {
			t.Run(fmt.Sprintf("prefix=%s/linked=%v", prefix, linked), func(t *testing.T) {
				repo := t.TempDir()
				writeSPMFile(t, repo, ".gitignore", "*.xcodeproj/\n")
				writeSPMFile(t, repo, prefix+"project.yml", "name: Demo\n")
				writeSPMFile(t, repo, source, resolvedFixture)
				initRepo(t, repo)
				if linked {
					wt := filepath.Join(t.TempDir(), "linked")
					gitIn(t, repo, "worktree", "add", "--detach", wt, "HEAD")
					repo = wt
				}
				cfg := watchProject()
				cfg.Project.Xcodeproj = prefix + "Demo.xcodeproj"
				cfg.Components = []config.Component{{Path: strings.TrimSuffix(prefix, "/"), Profiles: []string{"ios"}}}
				if prefix == "" {
					cfg.Components[0].Path = "."
				}
				if _, err := toml.Decode("[ios]\npackage_resolved = '"+source+"'\n", cfg); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(iosCIPath(t))
				if err != nil {
					t.Fatal(err)
				}
				rendered, missing := tokens.Substitute(string(raw), tokens.Values(cfg, prefix))
				if len(missing) > 0 {
					t.Fatal(missing)
				}
				renders := map[string]string{"ci.yml": rendered, "release.yml": renderReleaseAt(t, cfg, prefix)}
				for workflow, rendered := range renders {
					doc := parseSPMDoc(t, rendered)
					jobs := []string{"lint", "build-release", "test", "watch-test"}
					if workflow == "release.yml" {
						jobs = []string{"build-and-deploy"}
					}
					for _, job := range jobs {
						steps := doc.Jobs[job].Steps
						generate, copyAt, guard := -1, -1, -1
						for i, st := range steps {
							if st.Name == "Generate Xcode project (XcodeGen)" {
								generate = i
							}
							if st.Name == "Bootstrap Package.resolved" {
								copyAt = i
							}
							if st.Name == strictResolveStep {
								guard = i
							}
						}
						if generate < 0 || copyAt <= generate {
							t.Fatalf("%s/%s: bootstrap absent or before generation", workflow, job)
						}
						if (job == "test" || job == "build-release" || job == "build-and-deploy") && guard <= copyAt {
							t.Fatalf("%s/%s: bootstrap must precede guard", workflow, job)
						}
						project := cfg.Project.Xcodeproj
						dest := project + "/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"
						if steps[copyAt].WorkingDirectory != "." {
							t.Fatalf("%s/%s: bootstrap must pin repository-root working directory", workflow, job)
						}
						if err := os.Remove(filepath.Join(repo, dest)); err != nil && !os.IsNotExist(err) {
							t.Fatal(err)
						}
						run := func(script string, env ...string) (string, error) {
							cmd := exec.Command("bash", "-eo", "pipefail", "-c", script)
							cmd.Dir = repo
							cmd.Env = append(os.Environ(), env...)
							out, err := cmd.CombinedOutput()
							return string(out), err
						}
						generatorBin := t.TempDir()
						stubTool(t, generatorBin, "xcodegen", `[ -f project.yml ] || exit 1
mkdir -p Demo.xcodeproj
printf '%s' "$GENERATED_PBX" > Demo.xcodeproj/project.pbxproj
pwd > "$GENERATED_AT"
`)
						generatedAt := filepath.Join(t.TempDir(), "generated-at")
						if out, err := run(steps[generate].Run, "PATH="+generatorBin+":"+os.Getenv("PATH"), "GENERATED_PBX="+pbxRemotePackages, "GENERATED_AT="+generatedAt); err != nil {
							t.Fatalf("generation: %v\n%s", err, out)
						}
						gotDir, err := os.ReadFile(generatedAt)
						if err != nil {
							t.Fatal(err)
						}
						wantDir, err := filepath.EvalSymlinks(filepath.Join(repo, prefix))
						if err != nil {
							t.Fatal(err)
						}
						if strings.TrimSpace(string(gotDir)) != wantDir {
							t.Fatalf("generation cwd=%q, want %q", gotDir, wantDir)
						}
						if out, err := run(steps[copyAt].Run); err != nil {
							t.Fatalf("bootstrap: %v\n%s", err, out)
						}
						got, err := os.ReadFile(filepath.Join(repo, dest))
						if err != nil || string(got) != resolvedFixture {
							t.Fatalf("bootstrap bytes = %q, err = %v", got, err)
						}
						if guard >= 0 {
							bin, argsLog := fakeXcodebuild(t)
							env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "FAKE_ARGS=" + argsLog, "FAKE_RESOLVED=" + filepath.Join(repo, dest)}
							if out, err := run(steps[guard].Run, append(env, "FAKE_XCODEBUILD=ok")...); err != nil {
								t.Fatalf("matching pins rejected: %v\n%s", err, out)
							}
							if out, err := run(steps[guard].Run, append(env, "FAKE_XCODEBUILD=drift")...); err == nil || !strings.Contains(out, "::error::") {
								t.Fatalf("mismatched pins accepted: %v\n%s", err, out)
							}
						}
						if got := strings.TrimSpace(gitIn(t, repo, "ls-files", "--", "*Package.resolved")); got != source {
							t.Fatalf("committed lockfiles = %q", got)
						}
						writeSPMFile(t, repo, dest, "stale generated pins\n")
						if out, err := run(steps[copyAt].Run); err != nil {
							t.Fatalf("repeat copy: %v\n%s", err, out)
						}
						if got, err := os.ReadFile(filepath.Join(repo, dest)); err != nil || string(got) != resolvedFixture {
							t.Fatalf("stale pins survived: %q, %v", got, err)
						}
						gitIn(t, repo, "add", "-f", dest)
						if _, err := run(steps[copyAt].Run); err == nil {
							t.Fatal("second committed lockfile accepted")
						}
						gitIn(t, repo, "rm", "--cached", dest)
						gitIn(t, repo, "rm", "--cached", source)
						if _, err := run(steps[copyAt].Run); err == nil {
							t.Fatal("untracked source accepted")
						}
						gitIn(t, repo, "add", source)
						if err := os.Remove(filepath.Join(repo, source)); err != nil {
							t.Fatal(err)
						}
						if _, err := run(steps[copyAt].Run); err == nil {
							t.Fatal("missing source accepted")
						}
						writeSPMFile(t, repo, source, resolvedFixture)
					}
				}
			})
		}
	}
}

func writeSPMFile(t *testing.T, repo, name, body string) {
	t.Helper()
	p := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPackageResolvedBootstrapOff(t *testing.T) {
	for _, rendered := range []string{renderIOSCI(t, watchProject()), renderRelease(t, soloProject())} {
		if strings.Contains(rendered, "Bootstrap Package.resolved") {
			t.Fatal("bootstrap present without opt-in")
		}
	}
}
