package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const publicNamesScript = ".github/scripts/public-names-check.sh"

// runPublicNamesScript runs the CI step's script with a stub `go` first on PATH,
// so the decision logic is exercised without GitHub and without the real scan.
// The stub records that it was called and prints goOutput, exiting goExit.
// It returns the combined output, the exit code, and whether the stub ran.
func runPublicNamesScript(t *testing.T, actor, denylist, goOutput string, goExit int) (out string, code int, goRan bool, tmp string) {
	t.Helper()
	bin := t.TempDir()
	tmp = t.TempDir()
	marker := filepath.Join(bin, "go-ran")
	stub := "#!/usr/bin/env bash\ntouch " + marker + "\ncat <<'EOF_STUB'\n" + goOutput + "\nEOF_STUB\nexit " + string(rune('0'+goExit)) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(root(t), filepath.FromSlash(publicNamesScript)))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ACTOR="+actor, "DENYLIST="+denylist, "RUNNER_TEMP="+tmp)
	b, err := cmd.CombinedOutput()
	out = string(b)
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v\n%s", publicNamesScript, err, out)
	}
	_, statErr := os.Stat(marker)
	return out, code, statErr == nil, tmp
}

const goPassWithCount = "    public_names_test.go:1: public-names: scanned 412 tracked files\n--- PASS: TestShippedContentNamesNoPrivateConsumer (0.1s)"

func TestPublicNamesScript(t *testing.T) {
	t.Run("dependabot with no secret skips with a notice", func(t *testing.T) {
		out, code, ran, _ := runPublicNamesScript(t, "dependabot[bot]", "", goPassWithCount, 0)
		if code != 0 || ran || !strings.Contains(out, "::notice::skipped: no denylist secret") {
			t.Fatalf("want exit 0, no scan, a skip notice; got code=%d ran=%v out=%q", code, ran, out)
		}
	})
	t.Run("a human with no secret fails with a named error", func(t *testing.T) {
		for _, actor := range []string{"someone", "", "dependabot-preview[bot]", "Dependabot[bot]", "dependabot[bot]-x"} {
			out, code, ran, _ := runPublicNamesScript(t, actor, "", goPassWithCount, 0)
			if code != 1 || ran || !strings.Contains(out, "::error::") || !strings.Contains(out, "LACQUER_PUBLIC_DENYLIST") {
				t.Fatalf("actor %q: want exit 1, no scan, an ::error:: naming the secret; got code=%d ran=%v out=%q", actor, code, ran, out)
			}
		}
	})
	t.Run("a human with the list runs the scan", func(t *testing.T) {
		out, code, ran, tmp := runPublicNamesScript(t, "someone", "secretpattern\n", goPassWithCount, 0)
		if code != 0 || !ran {
			t.Fatalf("want the scan to run and pass; got code=%d ran=%v out=%q", code, ran, out)
		}
		if strings.Contains(out, "secretpattern") {
			t.Fatalf("the list was echoed: %q", out)
		}
		info, err := os.Stat(filepath.Join(tmp, "denylist.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("list mode = %v, want 0600", info.Mode().Perm())
		}
	})
	t.Run("dependabot that does hold the list still scans", func(t *testing.T) {
		_, code, ran, _ := runPublicNamesScript(t, "dependabot[bot]", "x\n", goPassWithCount, 0)
		if code != 0 || !ran {
			t.Fatalf("want a scan; got code=%d ran=%v", code, ran)
		}
	})
	t.Run("a pass without the scanned-file count fails", func(t *testing.T) {
		out, code, _, _ := runPublicNamesScript(t, "someone", "x\n", "--- PASS: TestShippedContentNamesNoPrivateConsumer (0.1s)", 0)
		if code != 1 || !strings.Contains(out, "::error::") {
			t.Fatalf("want exit 1 with an error; got code=%d out=%q", code, out)
		}
	})
	t.Run("a count of zero fails", func(t *testing.T) {
		zero := "    x_test.go:1: public-names: scanned 0 tracked files\n--- PASS: TestShippedContentNamesNoPrivateConsumer (0.1s)"
		_, code, _, _ := runPublicNamesScript(t, "someone", "x\n", zero, 0)
		if code != 1 {
			t.Fatalf("want exit 1 on 0 scanned; got %d", code)
		}
	})
	t.Run("a skipped test fails", func(t *testing.T) {
		skipped := "    x_test.go:1: public-names: scanned 9 tracked files\n--- SKIP: TestShippedContentNamesNoPrivateConsumer (0.0s)"
		_, code, _, _ := runPublicNamesScript(t, "someone", "x\n", skipped, 0)
		if code != 1 {
			t.Fatalf("want exit 1 when the test did not PASS; got %d", code)
		}
	})
	t.Run("a failing scan fails", func(t *testing.T) {
		_, code, _, _ := runPublicNamesScript(t, "someone", "x\n", "--- FAIL: TestShippedContentNamesNoPrivateConsumer", 1)
		if code == 0 {
			t.Fatal("a failing go test exited 0")
		}
	})
}

// The workflow must call the script and pass the actor and the secret only
// through env, and the temp files must still be removed unconditionally.
func TestPublicNamesWorkflowWiring(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ci := string(b)
	i := strings.Index(ci, "  public-names:")
	if i < 0 {
		t.Fatal("no public-names job in ci.yml")
	}
	job := ci[i:]
	if j := strings.Index(job[1:], "\n  # Behaviour eval suite"); j >= 0 {
		job = job[:j+1]
	}
	for _, want := range []string{
		"DENYLIST: ${{ secrets.LACQUER_PUBLIC_DENYLIST }}",
		"ACTOR: ${{ github.actor }}",
		"run: bash .github/scripts/public-names-check.sh",
		"if: always()",
		"rm -f \"$RUNNER_TEMP/denylist.txt\"",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("public-names job lacks %q", want)
		}
	}
	if strings.Contains(job, "github.triggering_actor") {
		t.Error("public-names must key on github.actor: a re-run keeps the original actor's (Dependabot's) secret access")
	}
	if strings.Contains(job, "${{ secrets.") && strings.Count(job, "${{ secrets.") != 1 {
		t.Error("the secret must be referenced exactly once, in step env")
	}
}
