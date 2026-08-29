package assets

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// realLacquer is this checkout. These tests plan against the content the repo
// actually ships, because "which files does a retired project stop getting" is a
// question about the real profiles — a fixture would answer a different one.
func realLacquer(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "VERSION")); err != nil {
		t.Skipf("not running from a lacquer checkout: %v", err)
	}
	return root
}

// retiredProject is the manifest under test, optionally retired.
func retiredProject(retired bool) *config.Config {
	var p config.Project
	if retired {
		p.Retired = &config.Retirement{Since: "2026-08-18", Reason: "not a viable app"}
	}
	return &config.Config{
		Project: p,
		Components: []config.Component{
			{Path: ".", Profiles: []string{"ios", "web"}},
			{Path: "server", Profiles: []string{"supabase"}},
		},
	}
}

func planDests(t *testing.T, cfg *config.Config) map[string]bool {
	t.Helper()
	plan, err := Plan(realLacquer(t), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	out := map[string]bool{}
	for _, a := range plan {
		out[filepath.ToSlash(a.Dest)] = true
	}
	return out
}

// droppedByRetirement is exactly what leaves the plan: every scheduled workflow
// across all three profiles, plus dependabot.
//
// Listed per-profile and in full rather than spot-checked, because the interesting
// claim is not "some files leave" but "these and no others".
var droppedByRetirement = []string{
	".github/dependabot.yml",
	// ios
	".github/workflows/ios-cleanup-ci.yml",
	".github/workflows/ios-dependency-audit.yml",
	".github/workflows/ios-docs.yml",
	".github/workflows/ios-quality-review.yml",
	// supabase
	".github/workflows/supabase-docs.yml",
	".github/workflows/supabase-health.yml",
	// web
	".github/workflows/web-docs.yml",
}

// keptWhenRetired is the "stay consistent" half: the repo must not rot.
var keptWhenRetired = []string{
	// PR-triggered CI, in every profile.
	".github/workflows/ios-ci.yml",
	".github/workflows/web-ci.yml",
	".github/workflows/supabase-ci.yml",
	".github/workflows/web-dependency-review.yml",
	".github/workflows/web-env-validation.yml",
	// Event-driven, not timed.
	".github/workflows/ios-claude.yml",
	".github/workflows/ios-issue-deduplication.yml",
	".github/workflows/ios-release.yml",
	// Lint / format / build configs.
	".swiftlint.yml",
	".swiftformat",
	"biome.json",
	"tsconfig.base.json",
	"server/deno.jsonc",
	// Hooks and scripts.
	".pre-commit-config.yaml",
	"lefthook.yml",
	"scripts/check-secrets.sh",
}

func TestRetiredPlanDropsScheduledWorkAndKeepsTheRest(t *testing.T) {
	live := planDests(t, retiredProject(false))
	retired := planDests(t, retiredProject(true))

	for _, dest := range droppedByRetirement {
		if !live[dest] {
			t.Fatalf("%s is not shipped to a live project at all; this test is out of date", dest)
		}
		if retired[dest] {
			t.Errorf("%s is still planned for a retired project — it costs money on a timer", dest)
		}
	}
	for _, dest := range keptWhenRetired {
		if !live[dest] {
			t.Fatalf("%s is not shipped to a live project at all; this test is out of date", dest)
		}
		if !retired[dest] {
			t.Errorf("%s left the plan; retirement stops the spend, it does not stop the sync", dest)
		}
	}

	// Nothing beyond the declared set may leave. Retirement is a narrow rule, and
	// a broad one would quietly let a repo rot out of the fleet.
	var extra []string
	for dest := range live {
		if !retired[dest] && !contains(droppedByRetirement, dest) {
			extra = append(extra, dest)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("retirement dropped assets it should not have:\n  %s", strings.Join(extra, "\n  "))
	}
	// And nothing may APPEAR: retiring a project can only ever subtract.
	for dest := range retired {
		if !live[dest] {
			t.Errorf("%s appeared only for a retired project", dest)
		}
	}
}

// An opted-in optional workflow is scheduled work like any other. It is not
// special-cased anywhere — it goes through the same content check — but it is
// the one asset a project asked for BY NAME, so the drop is worth pinning.
func TestRetiredPlanDropsOptedInScheduledWorkflow(t *testing.T) {
	iosOnly := func(retired bool) *config.Config {
		p := config.Project{OptionalWorkflows: []string{"testflight-feedback"}}
		if retired {
			p.Retired = &config.Retirement{Since: "2026-08-18", Reason: "not a viable app"}
		}
		return &config.Config{
			Project:    p,
			Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
		}
	}
	const dest = ".github/workflows/ios-testflight-feedback.yml"
	if !planDests(t, iosOnly(false))[dest] {
		t.Fatalf("%s is not installed by opting in; this test is out of date", dest)
	}
	if planDests(t, iosOnly(true))[dest] {
		t.Errorf("%s survived retirement — it is a daily cron", dest)
	}
}

// Only .github/workflows/*.yml and dependabot are candidates. A retired project
// keeps every skill, command, agent and CLAUDE region it had.
func TestRetiredPlanTouchesOnlyGithubAssets(t *testing.T) {
	live := planDests(t, retiredProject(false))
	retired := planDests(t, retiredProject(true))
	for dest := range live {
		if retired[dest] {
			continue
		}
		if !strings.HasPrefix(dest, ".github/") {
			t.Errorf("%s was dropped, but retirement only ever drops scheduled .github assets", dest)
		}
	}
}

// The retirement drop must not be recorded as an exclusion suppression: nothing
// in the manifest names these paths, so there is no declaration to review, and
// counting them would corrupt the stale-exclusion report.
func TestRetirementDoesNotForgeExclusionSuppressions(t *testing.T) {
	sup, err := Suppressed(realLacquer(t), retiredProject(true))
	if err != nil {
		t.Fatalf("Suppressed: %v", err)
	}
	if len(sup) != 0 {
		t.Errorf("retirement recorded %v as exclusion-suppressed; it excludes nothing", sup)
	}
}

// An exclusion naming a scheduled workflow must still register as suppressing
// something. Otherwise retiring a project would report the exclusion as dead
// text ("suppresses nothing and can be deleted") and the advice to delete it
// would silently re-adopt the file the day the project is un-retired.
func TestExclusionOfAScheduledWorkflowStaysLiveWhenRetired(t *testing.T) {
	cfg := retiredProject(true)
	cfg.Project.Exclude = []config.Exclusion{
		{Path: ".github/workflows/ios-docs.yml", Reason: "hand-tuned publish step"},
	}
	sup, err := Suppressed(realLacquer(t), cfg)
	if err != nil {
		t.Fatalf("Suppressed: %v", err)
	}
	if len(sup) != 1 || filepath.ToSlash(sup[0]) != ".github/workflows/ios-docs.yml" {
		t.Errorf("suppressed = %v, want the excluded scheduled workflow — an exclusion "+
			"shadowed by retirement must not read as stale", sup)
	}
}

// A workflow the detector cannot read must abort the plan, not be quietly kept
// (a bill on a dead project) or quietly dropped (a gate missing from a live one).
// Only a retired project pays this cost: a live one never reads workflow content.
func TestRetiredPlanFailsOnAnUnclassifiableWorkflow(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "profiles", "web", "workflows", "mystery.yml"),
		"name: mystery\njobs:\n  build:\n    runs-on: ubuntu-latest\n")

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"web"}}}}
	if _, err := Plan(h, cfg); err != nil {
		t.Fatalf("a live project must not read workflow content at all: %v", err)
	}

	cfg.Project.Retired = &config.Retirement{Since: "2026-08-18", Reason: "not a viable app"}
	_, err := Plan(h, cfg)
	if err == nil {
		t.Fatal("Plan succeeded on a workflow with no `on:` block")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Errorf("the error must name the workflow, got %q", err)
	}
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}
