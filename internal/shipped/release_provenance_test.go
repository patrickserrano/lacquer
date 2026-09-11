package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

type releaseDoc struct {
	Jobs map[string]struct {
		RunsOn      any               `yaml:"runs-on"`
		Needs       any               `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func releaseJobs(t *testing.T, cfg *config.Config) releaseDoc {
	t.Helper()
	var doc releaseDoc
	if err := yaml.Unmarshal([]byte(renderRelease(t, cfg)), &doc); err != nil {
		t.Fatalf("rendered release workflow is not valid YAML: %v", err)
	}
	return doc
}

// Nothing in the release pipeline looked at CI. A tag could be pushed at any
// commit — an unreviewed branch, a revert of the fix, a commit never pushed for
// review — and the pipeline would build, sign and upload it exactly as if it
// had come off a green main. The required "CI OK" check was enforced for
// MERGING and nowhere for RELEASING, which is the one place the artifact
// reaches users.
func TestReleaseRefusesACommitCINeverPassed(t *testing.T) {
	doc := releaseJobs(t, soloProject())
	job, ok := doc.Jobs["verify-ci-provenance"]
	if !ok {
		t.Fatal("the release workflow has no provenance gate")
	}
	// Linux, not the dedicated Mac: a release that must not happen should cost
	// two minutes on a hosted runner, not forty-five on the box every other
	// repository's release is queued behind.
	//
	// Asserted as a PROPERTY -- a HOSTED Linux runner -- rather than one exact
	// label, because the paragraph above cares about where the job is NOT: the
	// dedicated Mac, or a self-hosted array. Pinning the string froze more than
	// the intent twice over. First it froze the vCPU count, so right-sizing to a
	// cheaper SKU failed a test whose stated intent it satisfied. Then it froze
	// the PROVIDER: when the profiles moved off Blacksmith to `ubuntu-latest`
	// -- because the Blacksmith app is installed on the org and not on the
	// personal account, so its jobs queue forever in personally-owned
	// repositories -- this failed on a change that satisfies every word of the
	// reasoning above it.
	//
	// `ubuntu-latest` and a Blacksmith ubuntu SKU are both hosted Linux and both
	// pass. `[self-hosted, ...]` parses as a list rather than a string and fails
	// on the type assertion, and a macOS label fails the check below, so the
	// regression that matters is still caught.
	got, _ := job.RunsOn.(string)
	if !strings.Contains(got, "ubuntu") {
		t.Errorf("provenance gate runs on %v, want a hosted Linux runner (ubuntu-latest, or a hosted ubuntu SKU) -- never the dedicated Mac or a self-hosted array", job.RunsOn)
	}
	// A job-level permissions block REPLACES the workflow default, so both keys
	// have to be present: `checks: read` alone leaves checkout unable to clone.
	if job.Permissions["checks"] != "read" {
		t.Errorf("provenance gate cannot read check runs: permissions=%v", job.Permissions)
	}
	if job.Permissions["contents"] != "read" {
		t.Errorf("provenance gate cannot check out: permissions=%v", job.Permissions)
	}

	var body string
	for _, st := range job.Steps {
		body += st.Run
	}
	for _, want := range []string{
		`.name == "CI OK"`,         // the required check, by the name branch protection uses
		`.conclusion == "success"`, // completed-and-green, not merely present
		`.head_sha == $sha`,        // this commit, not one a re-run pointed elsewhere
		"merge-base --is-ancestor",
		"origin/$DEFAULT_BRANCH",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the provenance gate does not assert %q:\n%s", want, body)
		}
	}
	// The repository's OWN default branch, never a hardcoded name. `git
	// merge-base --is-ancestor` against a ref that no longer exists exits
	// NON-ZERO, which this gate reads as "not reachable" — so a stale branch
	// name here blocks every release while naming a branch nobody can find.
	if !strings.Contains(renderRelease(t, soloProject()), "github.event.repository.default_branch") {
		t.Error("the provenance gate hardcodes a branch name instead of reading the repository's default")
	}
}

// A gate nothing depends on gates nothing. This is the failure mode of every
// "we added a check" story: the job exists, it goes red, and the release ships
// beside it.
func TestEveryReleaseJobWaitsOnProvenance(t *testing.T) {
	doc := releaseJobs(t, soloProject())
	needs := func(job string) []string {
		raw := doc.Jobs[job].Needs
		switch v := raw.(type) {
		case string:
			return []string{v}
		case []any:
			var out []string
			for _, x := range v {
				out = append(out, x.(string))
			}
			return out
		}
		return nil
	}
	for _, job := range []string{"build-and-deploy"} {
		if !slices.Contains(needs(job), "verify-ci-provenance") {
			t.Errorf("%s does not wait on verify-ci-provenance (needs: %v)", job, needs(job))
		}
	}
	// notify-on-failure runs under always(), so it only reports a failed
	// provenance check if the job is in its needs list — otherwise the release
	// that was correctly blocked is the one nobody hears about.
	if !slices.Contains(needs("notify-on-failure"), "verify-ci-provenance") {
		t.Errorf("a blocked release notifies nobody (needs: %v)", needs("notify-on-failure"))
	}
}

// actions/upload-artifact rejects "/" in an artifact `name`. A tag push is safe
// ("v1.2.3"), which is why this survived; a workflow_dispatch against
// "feat/three-tab-restructure" carries the slash straight through and fails BOTH
// upload steps at the END of a forty-five-minute job, with the archive already
// built and signed. Confirmed failure, momfriend run 30157346812 — a project
// that fixed it locally and had to keep its own release workflow to do so.
func TestReleaseArtifactNamesSurviveABranchWithASlash(t *testing.T) {
	rendered := renderRelease(t, soloProject())
	if strings.Contains(rendered, "${{ github.ref_name }}.ipa") ||
		strings.Contains(rendered, "${{ github.ref_name }}.dSYM") {
		t.Error("an artifact name still interpolates github.ref_name raw; a dispatch from feat/x cannot upload")
	}

	// Run the shipped sanitiser rather than asserting on the tr expression: what
	// can be wrong is the character class, and no structural assertion reaches it.
	var doc struct {
		Jobs map[string]struct {
			Steps []struct {
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, st := range doc.Jobs["build-and-deploy"].Steps {
		if st.ID == "safe_ref" {
			script = st.Run
		}
	}
	if script == "" {
		t.Fatal("no safe_ref step in build-and-deploy")
	}
	for _, tc := range []struct{ in, want string }{
		{"v1.2.3", "v1.2.3"},
		{"feat/three-tab-restructure", "feat-three-tab-restructure"},
		{"fix/a:b", "fix-a-b"},
	} {
		out := filepath.Join(t.TempDir(), "gh_output")
		if err := os.WriteFile(out, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "REF_NAME="+tc.in, "GITHUB_OUTPUT="+out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("safe_ref failed on %q: %v\n%s", tc.in, err, b)
		}
		data, _ := os.ReadFile(out)
		got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "value="))
		if got != tc.want {
			t.Errorf("safe ref for %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}
