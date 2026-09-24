package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseGeneratesBeforeSigningAndBumping(t *testing.T) {
	steps := releaseJobs(t, soloProject()).Jobs["build-and-deploy"].Steps
	generated, signing, bump := -1, -1, -1
	for i, st := range steps {
		if strings.Contains(st.Run, "xcodegen generate") {
			generated = i
		}
		if strings.Contains(st.Run, "xcode-project use-profiles") {
			signing = i
		}
		if strings.Contains(st.Run, "agvtool new-version") {
			bump = i
		}
	}
	if generated < 0 || signing <= generated || bump <= generated {
		t.Fatalf("generation=%d signing=%d bump=%d: generate before either project mutation", generated, signing, bump)
	}
}

func TestReleaseBuildNumberChecksResolvedValue(t *testing.T) {
	var script string
	for _, st := range releaseJobs(t, soloProject()).Jobs["build-and-deploy"].Steps {
		if strings.Contains(st.Run, "agvtool new-version") {
			script = st.Run
		}
	}
	for _, value := range []string{"42", "5", "missing", "query-failed"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{
				"app-store-connect": "echo 41",
				// Deliberately claims success without changing anything.
				"agvtool": "echo 'Updated CFBundleVersion to 42'",
				"xcodebuild": `case " $* " in *" -derivedDataPath "*) ;; *) exit 99;; esac
if [ "$RESOLVED" = query-failed ]; then exit 1; fi
if [ "$RESOLVED" = missing ]; then echo '[]'; else
printf '[{"buildSettings":{"PRODUCT_BUNDLE_IDENTIFIER":"com.test.solo","CURRENT_PROJECT_VERSION":"%s"}}]\n' "$RESOLVED"
fi`,
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-eo", "pipefail", "-c", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "RESOLVED="+value,
				"PRODUCT_ASC_APP_ID=1", "PRODUCT_SCHEME=Solo", "PRODUCT_BUNDLE_ID=com.test.solo", "PRODUCT_EXTRA_BUNDLE_IDS=[]", "GITHUB_OUTPUT="+filepath.Join(dir, "output"))
			out, err := cmd.CombinedOutput()
			if value == "42" {
				if err != nil {
					t.Fatalf("correct resolved number rejected: %v\n%s", err, out)
				}
			} else if err == nil {
				t.Fatalf("writer claimed success but resolved number %s was accepted:\n%s", value, out)
			}
		})
	}
}

func TestReleaseArchiveUsesLocalDerivedData(t *testing.T) {
	for _, st := range releaseJobs(t, soloProject()).Jobs["build-and-deploy"].Steps {
		if strings.Contains(st.Run, "xcodebuild archive") && !strings.Contains(st.Run, "-derivedDataPath DerivedData") {
			t.Fatal("release archive writes to shared DerivedData")
		}
	}
}

// Opt-in because normal Go CI need not have Xcode. This executes the rendered
// generation, bump and archive scripts with real XcodeGen/agvtool/xcodebuild;
// only the remote ASC lookup is replaced. No signing credentials are needed.
func TestReleaseRealArchive(t *testing.T) {
	if os.Getenv("LACQUER_RELEASE_INTEGRATION") != "1" {
		t.Skip("set LACQUER_RELEASE_INTEGRATION=1 on a Mac with Xcode and XcodeGen")
	}
	for _, mode := range []string{"clean-xcodegen", "stale-xcodegen", "committed-only"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			run := func(script string) string {
				t.Helper()
				cmd := exec.Command("bash", "-eo", "pipefail", "-c", script)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"),
					"PRODUCT_NAME=Demo", "PRODUCT_SCHEME=Demo", "PRODUCT_BUNDLE_ID=com.x.demo", "PRODUCT_EXTRA_BUNDLE_IDS=[]",
					"PRODUCT_ASC_APP_ID=1", "GITHUB_OUTPUT="+filepath.Join(dir, "output"), "ARCHIVE_DIR="+dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v\n%s", script, err, out)
				}
				return string(out)
			}
			write("main.c", "int main(void) { return 0; }\n")
			spec := `name: Demo
options:
  deploymentTarget:
    iOS: "17.0"
settings:
  base:
    CURRENT_PROJECT_VERSION: "5"
    MARKETING_VERSION: "1.0"
    VERSIONING_SYSTEM: apple-generic
    CODE_SIGNING_ALLOWED: NO
    GENERATE_INFOPLIST_FILE: YES
    PRODUCT_BUNDLE_IDENTIFIER: com.x.demo
targets:
  Demo:
    type: application
    platform: iOS
    sources: [main.c]
`
			write("project.yml", spec)
			if mode != "clean-xcodegen" {
				run("xcodegen generate")
			}
			if mode == "committed-only" {
				if err := os.Remove(filepath.Join(dir, "project.yml")); err != nil {
					t.Fatal(err)
				}
			} else {
				write("project.yml", strings.ReplaceAll(spec, `MARKETING_VERSION: "1.0"`, `MARKETING_VERSION: "1.0.1"`))
			}
			write("app-store-connect", "#!/bin/sh\necho 41\n")
			if err := os.Chmod(filepath.Join(dir, "app-store-connect"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, st := range releaseJobs(t, soloProject()).Jobs["build-and-deploy"].Steps {
				if strings.Contains(st.Run, "xcodegen generate") || strings.Contains(st.Run, "agvtool new-version") || strings.Contains(st.Run, "xcodebuild archive") {
					run(st.Run)
				}
			}
			plist := "Demo.xcarchive/Products/Applications/Demo.app/Info.plist"
			if got := strings.TrimSpace(run("/usr/libexec/PlistBuddy -c 'Print CFBundleVersion' " + plist)); got != "42" {
				t.Fatalf("archived build number=%s, want 42", got)
			}
			want := "1.0.1"
			if mode == "committed-only" {
				want = "1.0"
			}
			if got := strings.TrimSpace(run("/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' " + plist)); got != want {
				t.Fatalf("archived version=%s, want %s", got, want)
			}
			if _, err := os.Stat(filepath.Join(dir, "DerivedData", "Build")); err != nil {
				t.Fatalf("archive has no local build data: %v", err)
			}
			t.Logf("%s: archived CFBundleVersion=42, marketing=%s, local DerivedData/Build exists", mode, want)
		})
	}
}

func TestReleaseSettingsQueryFollowsSecrets(t *testing.T) {
	secrets, query := -1, -1
	for i, st := range releaseJobs(t, withSecrets()).Jobs["build-and-deploy"].Steps {
		if strings.Contains(st.Run, "scripts/write-release-config.sh") {
			secrets = i
		}
		if strings.Contains(st.Run, "-showBuildSettings") {
			query = i
		}
	}
	if secrets < 0 || query <= secrets {
		t.Fatalf("secrets=%d query=%d: materialize xcconfig before resolving build settings", secrets, query)
	}
}
