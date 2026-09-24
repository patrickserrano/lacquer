package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

type hostArchiveStep struct {
	Name     string `yaml:"name"`
	If       string `yaml:"if"`
	Run      string `yaml:"run"`
	Continue bool   `yaml:"continue-on-error"`
	Timeout  int    `yaml:"timeout-minutes"`
}

type hostArchiveJob struct {
	RunsOn  any               `yaml:"runs-on"`
	Timeout int               `yaml:"timeout-minutes"`
	Steps   []hostArchiveStep `yaml:"steps"`
}

func hostArchiveWorkflow(t *testing.T, file string) map[string]hostArchiveJob {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles/ios/workflows", file))
	if err != nil {
		t.Fatal(err)
	}
	rendered, missing := tokens.Substitute(string(raw), tokens.Values(watchProject(), ""))
	if len(missing) != 0 {
		t.Fatalf("missing tokens: %v", missing)
	}
	var doc struct {
		Jobs map[string]hostArchiveJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Jobs
}

func TestIOSHostDiagnostics(t *testing.T) {
	count := 0
	for _, file := range []string{"ci.yml", "release.yml", "cleanup-ci.yml"} {
		for name, job := range hostArchiveWorkflow(t, file) {
			runners, ok := job.RunsOn.([]any)
			if !ok {
				continue
			}
			mac := false
			for _, runner := range runners {
				if runner == "macOS" {
					mac = true
				}
			}
			if !mac {
				continue
			}
			count++
			t.Run(file+"/"+name, func(t *testing.T) {
				st := job.Steps[len(job.Steps)-1]
				if st.Name != "Report host load" || st.If != "always()" || !st.Continue || st.Timeout != 1 {
					t.Fatalf("missing bounded, best-effort final always() host diagnostics: %+v", st)
				}
				for _, failed := range []bool{false, true} {
					dir := t.TempDir()
					uptime := "#!/bin/sh\necho 'load averages: 490.1 420.2 300.3'\n"
					ps := "#!/bin/sh\nprintf '%s\\n' /Applications/Xcode.app/usr/bin/xcodebuild /Applications/Xcode.app/usr/bin/swift-frontend /usr/bin/bash\n"
					if failed {
						uptime = "#!/bin/sh\nexit 1\n"
						ps = uptime
					}
					writeExe(t, filepath.Join(dir, "uptime"), uptime)
					writeExe(t, filepath.Join(dir, "ps"), ps)
					writeExe(t, filepath.Join(dir, "xcrun"), "#!/bin/sh\n[ \"$*\" = 'simctl list devices booted' ] || exit 99\necho 'CI-iPhone (Booted)'\n")
					cmd := exec.Command("bash", "-e", "-c", st.Run)
					cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("diagnostics failed: %v\n%s", err, out)
					}
					if !strings.Contains(string(out), "CI-iPhone (Booted)") {
						t.Fatalf("earlier probe failure suppressed simulator evidence: %s", out)
					}
					if !failed {
						for _, want := range []string{"490.1 420.2 300.3", "xcodebuild: 1", "swift-frontend: 1"} {
							if !strings.Contains(string(out), want) {
								t.Errorf("missing %q: %s", want, out)
							}
						}
					} else if !strings.Contains(string(out), "::warning::") {
						t.Errorf("probe failures must warn: %s", out)
					}
				}
			})
		}
	}
	if count != 7 {
		t.Fatalf("tested %d Mac jobs, want 7", count)
	}
}

func TestIOSTestTimeoutLeavesDiagnosticsHeadroom(t *testing.T) {
	job := hostArchiveWorkflow(t, "ci.yml")["test"]
	for _, st := range job.Steps {
		if st.Name == "Run Tests" {
			if st.Timeout < 20 || job.Timeout < st.Timeout+20 {
				t.Fatalf("test timeout=%d, job=%d: need 20 minutes for tests plus setup/diagnostics headroom", st.Timeout, job.Timeout)
			}
			return
		}
	}
	t.Fatal("missing Run Tests")
}

func TestIOSArchiveExportedIPA(t *testing.T) {
	steps := hostArchiveWorkflow(t, "release.yml")["build-and-deploy"].Steps
	var copyStep hostArchiveStep
	export, copyIndex, upload := -1, -1, -1
	for i, st := range steps {
		switch st.Name {
		case "Export IPA":
			export = i
		case "Archive exported IPA":
			copyStep, copyIndex = st, i
		case "Upload to TestFlight":
			upload = i
		}
	}
	if copyIndex <= export || upload <= copyIndex || !copyStep.Continue || copyStep.Timeout != 1 {
		t.Fatalf("need bounded best-effort IPA copy between export and upload: export=%d copy=%d upload=%d step=%+v", export, copyIndex, upload, copyStep)
	}
	if !strings.Contains(copyStep.Run, "$ARCHIVE_DIR") || strings.Contains(copyStep.Run, "GITHUB_RUN_ID") || strings.Contains(copyStep.Run, "/Volumes/") {
		t.Fatal("IPA copy must reuse ARCHIVE_DIR, including custom archive_root")
	}
	for _, scenario := range []string{"success", "missing IPA", "missing volume", "copy failure"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "custom archive root", "repo", "123")
			if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
				t.Fatal(err)
			}
			if scenario != "missing volume" {
				if err := os.MkdirAll(dest, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "missing IPA" {
				if err := os.WriteFile(filepath.Join(dir, "build", "Display Name.ipa"), []byte("delivered binary"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-eo", "pipefail", "-c", copyStep.Run)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "ARCHIVE_DIR="+dest)
			if scenario == "copy failure" {
				writeExe(t, filepath.Join(dir, "cp"), "#!/bin/sh\nexit 1\n")
				cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"))
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("copy must not fail release: %v\n%s", err, out)
			}
			if scenario == "success" {
				got, err := os.ReadFile(filepath.Join(dest, "Display Name.ipa"))
				if err != nil || string(got) != "delivered binary" {
					t.Fatalf("stored IPA=%q, err=%v", got, err)
				}
			} else if !strings.Contains(string(out), "::warning::") {
				t.Fatalf("failure did not warn: %s", out)
			}
		})
	}
}
