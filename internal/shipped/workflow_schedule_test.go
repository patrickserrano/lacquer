package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// soloProject is the shape twelve repositories in this fleet actually have: one
// component at the root, no [[product]] entries.
func soloProject() *config.Config {
	return &config.Config{
		Project: config.Project{
			ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
			AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
		},
	}
}

// renderIOSWorkflow substitutes a profile workflow the way sync does.
func renderIOSWorkflow(t *testing.T, name string, cfg *config.Config, prefix string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, prefix))
	if len(missing) > 0 {
		t.Fatalf("%s: unsubstituted tokens: %v", name, missing)
	}
	return out
}

// macSchedule is one shipped workflow that both runs on a schedule and wants the
// self-hosted Mac.
type macSchedule struct {
	file string
	cron string
}

// TestScheduledMacJobsDoNotShareASlot keeps the fleet's heaviest scheduled work
// off a single instant.
//
// There is ONE self-hosted Mac for roughly twelve iOS repositories. Two workflows
// sharing a cron means twenty-four heavy jobs asking for that runner at the same
// moment, which it can only answer by serialising them — and CI and releases
// queue behind the whole pile. dead-code.yml and quality-review.yml were both
// "0 9 * * 1"; they are now Monday and Wednesday.
//
// This checks the SHIPPED literals, which is all a template can control: the same
// minute still renders into every repository, and spreading those apart needs a
// per-project token that internal/tokens does not have.
func TestScheduledMacJobsDoNotShareASlot(t *testing.T) {
	dir := filepath.Join(root(t), "profiles", "ios", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var found []macSchedule
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		// Rendered, not token-stripped. Several of these files hold tokens that
		// expand to whole YAML blocks at column 0 (release.yml's product choices),
		// so a regex substitution leaves something that does not parse.
		body := renderIOSWorkflow(t, e.Name(), soloProject(), "")
		var doc struct {
			On struct {
				Schedule []struct {
					Cron string `yaml:"cron"`
				} `yaml:"schedule"`
			} `yaml:"on"`
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("rendered %s: %v", e.Name(), err)
		}
		if len(doc.On.Schedule) == 0 {
			continue
		}
		var wantsMac bool
		for _, j := range doc.Jobs {
			labels, ok := j.RunsOn.([]any)
			if !ok {
				continue
			}
			for _, l := range labels {
				if s, _ := l.(string); s == "self-hosted" {
					wantsMac = true
				}
			}
		}
		if !wantsMac {
			continue
		}
		for _, sc := range doc.On.Schedule {
			found = append(found, macSchedule{file: e.Name(), cron: sc.Cron})
		}
	}

	// Guard the guard: a walk that finds nothing would pass forever.
	if len(found) < 3 {
		t.Fatalf("only %d scheduled self-hosted workflows found; the walk is not reaching them", len(found))
	}

	seen := map[string]string{}
	for _, s := range found {
		if prev, dup := seen[s.cron]; dup {
			t.Errorf("%s and %s both run at %q. There is ONE self-hosted Mac for ~12 iOS "+
				"repositories, so a shared slot is ~24 heavy jobs queueing on one runner at once, "+
				"with CI and releases behind them. Move one to another day.", prev, s.file, s.cron)
			continue
		}
		seen[s.cron] = s.file
	}
}

// TestDocsCommentsDoNotDescribeTheRevertedTwoJobDesign keeps prose and
// implementation from disagreeing again.
//
// All three docs.yml files carried a comment arguing for a cheap `check` job on a
// hosted runner, justified by "a skipped job never occupies a runner at all" and
// reasoning about iOS's Mac — including in web and supabase, which have no Mac.
// The design it described lasted one day: e958179 reverted it after measuring
// that GitHub bills a minimum of one minute per JOB, so the gate cost a billed
// minute per repository per day to save ~20 seconds of an idle runner.
//
// The comment outlived the code by many commits. Nothing catches that, because
// nothing reads comments — so this does, for this one claim, which is now
// contradicted by the file it sits in and by
// TestDocsWorkflowsAreScheduledNotPushed.
func TestDocsCommentsDoNotDescribeTheRevertedTwoJobDesign(t *testing.T) {
	// The load-bearing sentence of the reverted argument.
	const claim = "a skipped job never occupies a runner"
	var checked int
	for _, profile := range []string{"ios", "web", "supabase"} {
		p := filepath.Join(root(t), "profiles", profile, "workflows", "docs.yml")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(strings.ToLower(line), claim) {
				t.Errorf("profiles/%s/workflows/docs.yml:%d still argues for the two-job design "+
					"that e958179 reverted: %q\nThese files have ONE job by measurement — a second "+
					"one costs a billed minute per repository per day.", profile, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked != 3 {
		t.Fatalf("read %d docs.yml files, want 3", checked)
	}
}
