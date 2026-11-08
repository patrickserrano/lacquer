package shipped

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseInstallStep struct {
	name, run string
	env       map[string]string
}

// Discover actual rendered installers, including audit and coverage callers.
// Removing a helper or leaving one copy on the old download path must fail.
func releaseInstallSteps(t *testing.T) []releaseInstallStep {
	t.Helper()
	files, err := shippedWorkflowFiles(root(t))
	if err != nil {
		t.Fatal(err)
	}
	var found []releaseInstallStep
	for _, file := range files {
		rendered, err := renderShippedWorkflow(t, file)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Jobs map[string]struct {
				Steps []struct {
					Name, Run string
					Env       map[string]string
				}
			}
		}
		if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
			t.Fatal(err)
		}
		for job, j := range doc.Jobs {
			for _, st := range j.Steps {
				if strings.Contains(st.Run, "releases/download/") && strings.Contains(st.Run, "checksums.txt") {
					found = append(found, releaseInstallStep{filepath.Base(filepath.Dir(filepath.Dir(file))) + "/" + job, st.Run, st.Env})
				}
			}
		}
	}
	if len(found) != 7 {
		t.Fatalf("found %d release installers, want 7", len(found))
	}
	return found
}

// Run the real rendered selection/download/checksum section, stopping before
// extraction or any product checks. Only network calls and sleep are stubbed.
func runReleaseInstall(t *testing.T, st releaseInstallStep, osName, arch, target, failures string, corrupt, ghFail, noGH bool) (int, string, string) {
	t.Helper()
	start := strings.Index(st.run, "case \"$(uname -m)\"")
	end := strings.Index(st.run, "tar -xzf")
	if start < 0 || end <= start {
		t.Fatal("installer selection/extraction boundaries missing")
	}
	// The audit wraps extraction in an if; retain its checksum check, not half
	// an if statement. Other installers invoke tar directly.
	script := st.run[start:end]
	script = strings.TrimSuffix(script, "if ! ")
	// The audit verifies the checkout pin before downloading.
	bin, dir, fixtures := t.TempDir(), t.TempDir(), t.TempDir()
	asset := "lacquer_1.2.3_" + osName + "_" + arch + ".tar.gz"
	payload := []byte("verified release payload\n")
	for name, data := range map[string][]byte{asset: payload, "checksums.txt": []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(payload), asset))} {
		if err := os.WriteFile(filepath.Join(fixtures, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	stubTool(t, bin, "uname", `case "$1" in -s) echo "$OS_NAME";; -m) echo "$ARCH_NAME";; *) exit 90;; esac`)
	stubTool(t, bin, "sleep", `echo "sleep $1" >> "$STATE/trace"`)
	stubTool(t, bin, "curl", releaseCurlStub)
	stubTool(t, bin, "gh", `
echo "gh $*" >> "$STATE/trace"
[ "$GH_TOKEN" = stub-auth ] || exit 91
[ "$1 $2 $3 $4 $5 $6" = "release download v1.2.3 -R patrickserrano/lacquer -p" ] || exit 92
file=$7
[ "$8" = -D ] && [ "$9" = "$RUNNER_TEMP" ] || exit 93
[ "$GH_FAIL" = false ] || exit 1
cp "$FIXTURES/$file" "$RUNNER_TEMP/$file"
if [ "$CORRUPT" = true ] && [ "$file" != checksums.txt ]; then echo corrupt > "$RUNNER_TEMP/$file"; fi
`)
	if noGH {
		script = `command() { if [ "$1" = -v ] && [ "$2" = gh ]; then return 1; fi; builtin command "$@"; }
` + script
	}
	script = "set -euo pipefail\nmkdir -p .lacquer-checkout\necho 1.2.3 > .lacquer-checkout/VERSION\n" + script + "\necho VERIFIED\n"
	unameOS, unameArch := "Linux", arch
	if osName == "darwin" {
		unameOS = "Darwin"
	}
	if arch == "amd64" {
		unameArch = "x86_64"
	} else if osName == "linux" {
		unameArch = "aarch64"
	}
	out, code := runCoverageStep(t, script, bin,
		"LACQUER_TAG=v1.2.3", "RUNNER_TEMP="+dir, "STATE="+dir, "FIXTURES="+fixtures,
		"OS_NAME="+unameOS, "ARCH_NAME="+unameArch, "TARGET="+target, "FAILURES="+failures,
		fmt.Sprintf("CORRUPT=%t", corrupt), fmt.Sprintf("GH_FAIL=%t", ghFail), "GH_TOKEN=stub-auth")
	trace, err := os.ReadFile(filepath.Join(dir, "trace"))
	if err != nil {
		t.Fatalf("installer made no network call: %v\n%s", err, out)
	}
	if strings.Contains(string(trace), "INVALID") {
		t.Fatalf("invalid download arguments:\n%s", trace)
	}
	if code == 0 && !strings.Contains(out, "VERIFIED") {
		t.Fatalf("installer silently exited without verification:\n%s", out)
	}
	return code, out, string(trace)
}

func TestReleaseInstallRetryAndChecksum(t *testing.T) {
	for _, st := range releaseInstallSteps(t) {
		t.Run(st.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, failures              string
				corrupt, ghFail, noGH, fail bool
				tries, sleeps, fallback     int
			}{
				{"first try", "", false, false, false, false, 1, 0, 0},
				{"504 then success", "504", false, false, false, false, 2, 1, 0},
				{"429 then success", "429", false, false, false, false, 2, 1, 0},
				{"network then success", "56", false, false, false, false, 2, 1, 0},
				{"timeout then success", "28", false, false, false, false, 2, 1, 0},
				{"fallback", "504 504 504 504 504", false, false, false, false, 5, 4, 1},
				{"API without gh", "504 504 504 504 504", false, false, true, false, 5, 4, 0},
				{"404 stops", "404", false, false, false, true, 1, 0, 0},
				{"all paths fail", "504 504 504 504 504", false, true, false, true, 5, 4, 1},
				{"API all paths fail", "504 504 504 504 504", false, true, true, true, 5, 4, 0},
				{"public checksum mismatch", "", true, false, false, true, 1, 0, 0},
				{"fallback checksum mismatch", "504 504 504 504 504", true, false, false, true, 5, 4, 1},
				{"API checksum mismatch", "504 504 504 504 504", true, false, true, true, 5, 4, 0},
			} {
				for _, target := range []string{"asset", "checksums.txt"} {
					t.Run(tc.name+"/"+target, func(t *testing.T) {
						code, out, trace := runReleaseInstall(t, st, "linux", "amd64", target, tc.failures, tc.corrupt, tc.ghFail, tc.noGH)
						if (code != 0) != tc.fail {
							t.Fatalf("exit %d, want fail=%t:\n%s\n%s", code, tc.fail, out, trace)
						}
						if strings.Count(trace, "public "+target+"\n") != tc.tries || strings.Count(trace, "sleep ") != tc.sleeps || strings.Count(trace, "gh release download") != tc.fallback {
							t.Fatalf("wrong attempts/backoff/fallback:\n%s", trace)
						}
						if tc.sleeps == 4 && !strings.Contains(trace, "sleep 15\npublic "+target+"\nsleep 30\npublic "+target+"\nsleep 60\npublic "+target+"\nsleep 120\n") {
							t.Fatalf("backoff must total 225s with four exponential delays:\n%s", trace)
						}
						if tc.failures != "" && !strings.Contains(out, "attempt 1/5") {
							t.Errorf("failed attempt not logged:\n%s", out)
						}
						if tc.corrupt && !strings.Contains(out, "Checksum verification failed") {
							t.Errorf("checksum failure not explicit:\n%s", out)
						}
					})
				}
			}
		})
	}
}

func TestReleaseInstallAllPlatforms(t *testing.T) {
	for _, st := range releaseInstallSteps(t) {
		if st.env["GH_TOKEN"] != "${{ github.token }}" {
			t.Errorf("%s: missing workflow token environment", st.name)
		}
		for _, osName := range []string{"linux", "darwin"} {
			for _, arch := range []string{"amd64", "arm64"} {
				t.Run(st.name+"/"+osName+"/"+arch, func(t *testing.T) {
					code, out, _ := runReleaseInstall(t, st, osName, arch, "asset", "", false, false, false)
					if code != 0 {
						t.Fatalf("platform install failed: %d\n%s", code, out)
					}
				})
			}
		}
	}
}

const releaseCurlStub = `
out=""; url=""; writeout=false; connect=false; bound=false; auth=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift;;
    -w) writeout=true; shift;;
    --connect-timeout) [ "$2" -gt 0 ] && [ "$2" -le 5 ] && connect=true; shift;;
    --max-time) [ "$2" -gt 0 ] && [ "$2" -le 15 ] && bound=true; shift;;
    -H) case "$2" in 'Authorization: Bearer stub-auth') auth=true;; esac; shift;;
    https://*) url=$1;;
  esac
  shift
done
file=${url##*/}
case "$url" in
  https://github.com/patrickserrano/lacquer/releases/download/v1.2.3/*)
    name=$file; [ "$file" = checksums.txt ] || name=asset
    echo "public $name" >> "$STATE/trace"
    if ! $writeout || ! $connect || ! $bound; then echo INVALID >> "$STATE/trace"; exit 97; fi
    n=0; [ ! -f "$STATE/$name.count" ] || n=$(cat "$STATE/$name.count")
    n=$((n+1)); echo "$n" > "$STATE/$name.count"
    status=200
    if [ "$name" = "$TARGET" ]; then
      i=0; for s in $FAILURES; do i=$((i+1)); if [ "$i" = "$n" ]; then status=$s; fi; done
    fi
    case "$status" in
      200) ;;
      56|28) printf 000; exit "$status";;
      *) printf '%s' "$status"; echo partial > "$out"; exit 22;;
    esac
    ;;
  https://api.github.com/repos/patrickserrano/lacquer/releases/tags/v1.2.3)
    $auth || exit 91
    [ "$GH_FAIL" = false ] || exit 22
    printf '{"assets":[' > "$out"
    sep=''; for f in "$FIXTURES"/*; do
      printf '%s{"name":"%s","url":"https://api.github.com/repos/patrickserrano/lacquer/releases/assets/%s"}' "$sep" "${f##*/}" "${f##*/}" >> "$out"; sep=,
    done
    echo ']}' >> "$out"; exit 0;;
  https://api.github.com/repos/patrickserrano/lacquer/releases/assets/*)
    $auth || exit 91; echo "api $file" >> "$STATE/trace";;
  *) echo INVALID >> "$STATE/trace"; exit 98;;
esac
cp "$FIXTURES/$file" "$out"
if [ "$CORRUPT" = true ] && [ "$file" != checksums.txt ]; then echo corrupt > "$out"; fi
if $writeout; then printf 200; fi
`
