package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
)

// Blacksmith is installed PER ACCOUNT. The app is on the PixelFoxStudio org and
// not on the personal account, so a Blacksmith job in a personally-owned repo
// queues with no runner assigned, forever — measured on
// patrickserrano/darndest-api-proxy, where both the arm64 and x64 jobs sat
// `queued` while the same jobs completed in seconds in org-owned repos.
//
// The failure mode is why this is a manifest field rather than a fleet-wide
// choice: the job does not go red, it HANGS, and a required check that never
// resolves reads as "CI is slow today" rather than as breakage.

func TestLinuxRunnerDefaultsToBlacksmith(t *testing.T) {
	if got := tokens.LinuxRunnerFor(""); got != tokens.DefaultLinuxRunner {
		t.Errorf("default = %q, want %q — 13 of 19 managed projects live where Blacksmith is installed", got, tokens.DefaultLinuxRunner)
	}
}

func TestLinuxRunnerOverrideIsUsed(t *testing.T) {
	if got := tokens.LinuxRunnerFor("ubuntu-latest"); got != "ubuntu-latest" {
		t.Errorf("override = %q, want it honoured — a personally-owned repo cannot reach Blacksmith", got)
	}
}

// The token must never render empty: an empty `runs-on` is a workflow that
// parses and never runs, which is the same silent-hang failure in a new place.
func TestLinuxRunnerNeverRendersEmpty(t *testing.T) {
	for _, in := range []string{"", "ubuntu-latest", tokens.DefaultLinuxRunner} {
		if got := tokens.LinuxRunnerFor(in); strings.TrimSpace(got) == "" {
			t.Errorf("LinuxRunnerFor(%q) rendered empty", in)
		}
	}
}

// A manifest must not be able to move Linux work onto the dedicated Mac or a
// self-hosted array. The release provenance gate exists to keep it off there.
func TestLinuxRunnerRejectsASelfHostedArray(t *testing.T) {
	for _, bad := range []string{
		"[self-hosted, macOS, ARM64, dedicated]",
		"self hosted",
		"ubuntu latest",
		strings.Repeat("a", 70),
	} {
		dir := t.TempDir()
		manifest := filepath.Join(dir, ".lacquer.toml")
		body := "[project]\nname = \"X\"\nproject_name = \"X\"\nscheme = \"X\"\n" +
			"bundle_id = \"com.x.x\"\nasc_app_id = \"1\"\nxcodeproj = \"X.xcodeproj\"\n" +
			"linux_runner = \"" + bad + "\"\n\n[[component]]\npath = \".\"\nprofiles = [\"ios\"]\n"
		if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(manifest); err == nil {
			t.Errorf("linux_runner %q was accepted", bad)
		}
	}
}

// Every Linux job in every profile renders through the token. A literal label
// left behind is a job that silently ignores the project's override.
func TestNoProfileHardcodesALinuxRunner(t *testing.T) {
	for _, f := range profileWorkflows(t) {
		body := readFile(t, f)
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "runs-on:") {
				continue
			}
			v := strings.TrimSpace(strings.TrimPrefix(line, "runs-on:"))
			// Self-hosted arrays are written literally on purpose: the macOS box
			// and the pi gate are not substitutable for a hosted Linux runner.
			if strings.HasPrefix(v, "[") || v == tokens.LinuxRunner {
				continue
			}
			t.Errorf("%s hardcodes %q; Linux jobs must render {{LINUX_RUNNER}} so a project can override a runner its account cannot reach", f, v)
		}
	}
}

// profileWorkflows is every workflow the profiles ship, optional ones included:
// an optional workflow still renders into a repository and still needs a runner
// that repository can reach.
func profileWorkflows(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{
		"profiles/*/workflows/*.yml",
		"profiles/*/workflows-optional/*.yml",
	} {
		m, err := filepath.Glob(filepath.Join(root(t), pat))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m...)
	}
	if len(out) == 0 {
		t.Fatal("no profile workflows found — the glob is wrong, and a test that checks nothing passes")
	}
	return out
}
