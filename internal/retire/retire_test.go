package retire

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// shippedWorkflows returns every workflow this repo actually ships, keyed by a
// repo-relative path. Globbed rather than listed, so a workflow added tomorrow is
// covered by the tests below without anyone remembering to enrol it — which is
// the entire failure mode a hardcoded filename list would reintroduce.
func shippedWorkflows(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, pat := range []string{
		"../../profiles/*/workflows/*.yml",
		"../../profiles/*/workflows-optional/*.yml",
		"../../core/root/.github/workflows/*.yml",
	} {
		files, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("glob %s: %v", pat, err)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			out[filepath.ToSlash(strings.TrimPrefix(f, "../../"))] = string(data)
		}
	}
	if len(out) == 0 {
		t.Skip("not running from a lacquer checkout: no shipped workflows found")
	}
	return out
}

// scheduledShipped is every workflow in this repo that runs on a timer.
//
// It is asserted as an exact set, not a spot check, and that is deliberate
// friction: adding a scheduled workflow to the lacquer means deciding that
// retired projects stop receiving it, and this test is where that decision gets
// made out loud. A new NON-scheduled workflow passes untouched.
var scheduledShipped = []string{
	"profiles/ios/workflows-optional/testflight-feedback.yml",
	"profiles/ios/workflows/cleanup-ci.yml",
	"profiles/ios/workflows/dependency-audit.yml",
	"profiles/ios/workflows/docs.yml",
	"profiles/ios/workflows/quality-review.yml",
	"profiles/supabase/workflows/docs.yml",
	"profiles/supabase/workflows/health.yml",
	"profiles/web/workflows/docs.yml",
}

// The detector is checked against the real shipped content, not against fixtures
// of it. Two of these files are not valid YAML until their tokens are
// substituted (profiles/ios/workflows/release.yml carries {{IOS_RELEASE_TAGS}}
// INSIDE its `on:` block), and a fixture would not have shown that.
func TestEveryShippedWorkflowClassifies(t *testing.T) {
	var got []string
	for path, body := range shippedWorkflows(t) {
		scheduled, err := Scheduled(body)
		if err != nil {
			t.Errorf("%s: cannot classify a workflow this repo ships: %v", path, err)
			continue
		}
		if scheduled {
			got = append(got, path)
		}
	}
	sort.Strings(got)
	want := append([]string(nil), scheduledShipped...)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("scheduled workflows:\n got:\n  %s\nwant:\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// PR-triggered CI is the thing a retired project must keep, so it gets its own
// assertion rather than living only inside the set comparison above.
func TestShippedPRWorkflowsAreNotScheduled(t *testing.T) {
	shipped := shippedWorkflows(t)
	for _, path := range []string{
		"profiles/ios/workflows/ci.yml",
		"profiles/web/workflows/ci.yml",
		"profiles/supabase/workflows/ci.yml",
		"profiles/web/workflows/dependency-review.yml",
		"profiles/web/workflows/env-validation.yml",
		"profiles/ios/workflows/release.yml",
	} {
		body, ok := shipped[path]
		if !ok {
			t.Fatalf("%s is not shipped any more; update this test", path)
		}
		scheduled, err := Scheduled(body)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if scheduled {
			t.Errorf("%s reads as scheduled; a retired project would lose its PR gate", path)
		}
	}
}

func TestScheduledTriggerForms(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"block mapping", "name: x\non:\n  schedule:\n    - cron: '0 3 * * *'\njobs: {}\n", true},
		{"several triggers, cron among them", "on:\n  workflow_dispatch:\n  schedule:\n    - cron: '0 7 * * *'\njobs: {}\n", true},
		{"schedule first, dispatch after", "on:\n  schedule:\n    - cron: '0 7 * * *'\n  workflow_dispatch:\njobs: {}\n", true},
		{"quoted on key", "\"on\":\n  schedule:\n    - cron: '0 3 * * *'\n", true},
		{"single-quoted on key", "'on':\n  schedule:\n    - cron: '0 3 * * *'\n", true},
		{"flow mapping", "on: { schedule: [ { cron: '0 3 * * *' } ] }\njobs: {}\n", true},
		{"sequence form", "on: [push, schedule]\njobs: {}\n", true},
		{"scalar form", "on: schedule\njobs: {}\n", true},
		{"comments inside the block", "on:\n  # nightly\n  schedule:\n    - cron: '0 3 * * *'\n", true},
		{"pull_request only", "on:\n  pull_request:\n    branches: [main]\njobs: {}\n", false},
		{"scalar push", "on: push\njobs: {}\n", false},
		{"sequence without schedule", "on: [push, pull_request]\n", false},
		// The two ways a stray "schedule" could be mistaken for a trigger. Both
		// are real shapes: workflows take dispatch inputs, and jobs have steps.
		{"schedule as a dispatch input", "on:\n  workflow_dispatch:\n    inputs:\n      schedule:\n        type: string\njobs: {}\n", false},
		{"schedule inside jobs", "on:\n  pull_request:\njobs:\n  build:\n    schedule:\n      - cron: '0 3 * * *'\n", false},
		// A placeholder owning its own indentation must not truncate the block —
		// this is profiles/ios/workflows/release.yml's shape, with the trigger
		// that matters sitting AFTER the token line.
		{"token line inside the block", "on:\n  push:\n    tags:\n{{IOS_RELEASE_TAGS}}\n  schedule:\n    - cron: '0 3 * * *'\njobs: {}\n", true},
		{"token line, no schedule", "on:\n  push:\n    tags:\n{{IOS_RELEASE_TAGS}}\n  workflow_dispatch:\njobs: {}\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Scheduled(c.body)
			if err != nil {
				t.Fatalf("Scheduled: %v", err)
			}
			if got != c.want {
				t.Errorf("Scheduled = %v, want %v for:\n%s", got, c.want, c.body)
			}
		})
	}
}

// An unreadable trigger block is an error, never a guess. Guessing "not
// scheduled" keeps paying for a nightly job on a dead project; guessing
// "scheduled" withholds a gate from a live one. Both are silent.
func TestScheduledFailsLoudly(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"no on block", "name: x\njobs:\n  build:\n    runs-on: ubuntu-latest\n"},
		{"empty on block", "name: x\non:\njobs: {}\n"},
		{"on is not a trigger", "on: 42\n"},
		{"unparseable block", "on:\n  schedule:\n   - cron: '0 3 * * *'\n  - stray\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Scheduled(c.body); err == nil {
				t.Errorf("Scheduled returned no error for:\n%s", c.body)
			}
		})
	}
}

func TestDrops(t *testing.T) {
	dir := t.TempDir()
	scheduled := filepath.Join(dir, "sched.yml")
	pr := filepath.Join(dir, "pr.yml")
	for path, body := range map[string]string{
		scheduled: "on:\n  schedule:\n    - cron: '0 3 * * *'\njobs: {}\n",
		pr:        "on:\n  pull_request:\njobs: {}\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name, src, dest string
		want            bool
	}{
		{"dependabot", pr, ".github/dependabot.yml", true},
		{"scheduled workflow", scheduled, ".github/workflows/ios-docs.yml", true},
		{"pr workflow", pr, ".github/workflows/web-ci.yml", false},
		// Same content, somewhere that is not a workflow directory. `schedule:`
		// in a lint config or a skill is not a cron job.
		{"scheduled content outside .github/workflows", scheduled, "ios/.swiftlint.yml", false},
		{"nested under workflows", scheduled, ".github/workflows/nested/x.yml", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Drops(c.src, c.dest)
			if err != nil {
				t.Fatalf("Drops: %v", err)
			}
			if got != c.want {
				t.Errorf("Drops(%q) = %v, want %v", c.dest, got, c.want)
			}
		})
	}

	if _, err := Drops(filepath.Join(dir, "gone.yml"), ".github/workflows/x.yml"); err == nil {
		t.Error("an unreadable workflow must be an error, not a silent keep")
	}
}

func TestNotice(t *testing.T) {
	if got := Notice(&config.Config{}); got != "" {
		t.Errorf("a live project must print nothing, got %q", got)
	}
	cfg := &config.Config{Project: config.Project{
		Retired: &config.Retirement{Since: "2026-08-18", Reason: "not a viable app"},
	}}
	got := Notice(cfg)
	for _, want := range []string{"RETIRED", "2026-08-18", "not a viable app"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice must contain %q:\n%s", want, got)
		}
	}
}
