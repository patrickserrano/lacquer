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

type dependabotFile struct {
	Version int `yaml:"version"`
	Updates []struct {
		Ecosystem string `yaml:"package-ecosystem"`
		Directory string `yaml:"directory"`
		Schedule  struct {
			Interval string `yaml:"interval"`
		} `yaml:"schedule"`
		Limit  int      `yaml:"open-pull-requests-limit"`
		Ignore []any    `yaml:"ignore"`
		Allow  []any    `yaml:"allow"`
		Labels []string `yaml:"labels"`
	} `yaml:"updates"`
}

func renderDependabot(t *testing.T, cfg *config.Config) dependabotFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "core", "root", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	var doc dependabotFile
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered dependabot.yml is not valid YAML: %v\n%s", err, out)
	}
	if doc.Version != 2 {
		t.Errorf("version = %d, want 2", doc.Version)
	}
	return doc
}

func ecosystemsAt(doc dependabotFile) map[string]string {
	m := map[string]string{}
	for _, u := range doc.Updates {
		m[u.Ecosystem] = u.Directory
	}
	return m
}

// Every repo has workflows, and a pinned action SHA is the dependency most
// likely to rot unnoticed: nothing fails when one goes stale.
func TestDependabotAlwaysWatchesActions(t *testing.T) {
	cfg := &config.Config{
		Project:    config.Project{ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.s", AscAppID: "1", Xcodeproj: "Solo.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	got := ecosystemsAt(renderDependabot(t, cfg))
	if dir, ok := got["github-actions"]; !ok || dir != "/" {
		t.Errorf("github-actions entry = %q (present=%v), want \"/\"", dir, ok)
	}
}

// Dependabot's swift ecosystem needed a top-level Package.swift until
// 2026-03-31; it now discovers Package.resolved inside .xcodeproj bundles, which
// is the layout every iOS project here uses. Dropping this entry would leave
// twelve repos' SPM dependencies unwatched.
func TestDependabotWatchesSwiftForIOS(t *testing.T) {
	cfg := &config.Config{
		Project:    config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	if _, ok := ecosystemsAt(renderDependabot(t, cfg))["swift"]; !ok {
		t.Error("an ios component got no swift entry")
	}
}

// `directory` is per-manifest and Dependabot has no glob for it, so a component
// in a subdirectory must be named. kit keeps its project at Kit/Kit.xcodeproj,
// where "/" finds nothing at all.
func TestDependabotFollowsComponentDirectories(t *testing.T) {
	cfg := &config.Config{
		Project:    config.Project{ProjectName: "Kit", Scheme: "Kit", BundleID: "com.x.k", AscAppID: "1", Xcodeproj: "Kit/Kit.xcodeproj"},
		Components: []config.Component{{Path: "Kit", Profiles: []string{"ios"}}},
	}
	if dir := ecosystemsAt(renderDependabot(t, cfg))["swift"]; dir != "/Kit" {
		t.Errorf("swift directory = %q, want \"/Kit\"", dir)
	}
}

// A repo shipping both stacks must get both, each at its own path — this is the
// shape that a single hand-written file gets wrong.
func TestDependabotCoversEveryComponent(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "com.x.r", AscAppID: "1", Xcodeproj: "ios/Rail.xcodeproj"},
		Components: []config.Component{
			{Path: "ios", Profiles: []string{"ios"}},
			{Path: "web", Profiles: []string{"web"}},
		},
	}
	got := ecosystemsAt(renderDependabot(t, cfg))
	for eco, want := range map[string]string{"github-actions": "/", "swift": "/ios", "npm": "/web"} {
		if got[eco] != want {
			t.Errorf("%s directory = %q, want %q", eco, got[eco], want)
		}
	}
}

// "Auto-open PRs for everything": nothing filtered, and not capped at the
// default 5 — at five, a repo that falls behind stops opening PRs for the rest
// and says nothing, so the queue looks empty because it is full.
func TestDependabotOpensPRsForEverythingDaily(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{
			{Path: ".", Profiles: []string{"ios"}},
			{Path: "web", Profiles: []string{"web"}},
		},
	}
	doc := renderDependabot(t, cfg)
	if len(doc.Updates) == 0 {
		t.Fatal("no updates rendered")
	}
	for _, u := range doc.Updates {
		if u.Schedule.Interval != "daily" {
			t.Errorf("%s: interval = %q, want daily", u.Ecosystem, u.Schedule.Interval)
		}
		if u.Limit <= 5 {
			t.Errorf("%s: open-pull-requests-limit = %d, want more than the default 5", u.Ecosystem, u.Limit)
		}
		if len(u.Ignore) > 0 {
			t.Errorf("%s: has ignore rules, which would exclude updates", u.Ecosystem)
		}
		if len(u.Allow) > 0 {
			t.Errorf("%s: has an allow list, which narrows what gets opened", u.Ecosystem)
		}
	}
}

// Docs publishing is scheduled, not merge-triggered. The DocC build takes ~38
// minutes on the fleet's one self-hosted Mac, so a per-merge publish put a
// 38-minute job at the head of the queue that CI and releases then sat behind.
func TestDocsWorkflowsAreScheduledNotPushed(t *testing.T) {
	for _, profile := range []string{"ios", "web", "supabase"} {
		raw, err := os.ReadFile(filepath.Join(root(t), "profiles", profile, "workflows", "docs.yml"))
		if err != nil {
			t.Fatal(err)
		}
		// `on` is a YAML 1.1 boolean, so it parses as the key `true`.
		var doc struct {
			On struct {
				Push     any `yaml:"push"`
				Schedule []struct {
					Cron string `yaml:"cron"`
				} `yaml:"schedule"`
			} `yaml:"on"`
			Jobs map[string]struct {
				Needs any    `yaml:"needs"`
				If    string `yaml:"if"`
			} `yaml:"jobs"`
		}
		body := lacquerToken.ReplaceAllString(strings.ReplaceAll(string(raw), "{{WEB_BUILD_ENV}}\n", ""), "x")
		if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("%s docs.yml: %v", profile, err)
		}

		if doc.On.Push != nil {
			t.Errorf("%s docs.yml still publishes on push", profile)
		}
		if len(doc.On.Schedule) == 0 {
			t.Errorf("%s docs.yml has no schedule", profile)
			continue
		}
		// Not 07:00/08:00/09:00 — those are cleanup-ci, supabase health and
		// testflight-feedback, and this exists to stop competing for the runner.
		for _, sc := range doc.On.Schedule {
			for _, taken := range []string{"0 7 ", "0 8 ", "0 9 "} {
				if strings.HasPrefix(sc.Cron, taken) {
					t.Errorf("%s docs.yml cron %q collides with an existing daily", profile, sc.Cron)
				}
			}
		}

		// The expensive job must be GATED, not merely conditional per step: a
		// skipped job never occupies a runner, a skipped step still does.
		gated := false
		for name, j := range doc.Jobs {
			if name == "check" {
				continue
			}
			if j.Needs != nil && strings.Contains(j.If, "needs.check.outputs.needed") {
				gated = true
			}
		}
		if !gated {
			t.Errorf("%s docs.yml: the publish job is not gated on the staleness check", profile)
		}
	}
}
