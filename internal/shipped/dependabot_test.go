package shipped

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
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

// dirsFor is every directory an ecosystem is watched at, in file order. Needed
// because ecosystemsAt collapses repeats, and "how many swift entries" is exactly
// what several of these tests are about.
func dirsFor(doc dependabotFile, eco string) []string {
	var out []string
	for _, u := range doc.Updates {
		if u.Ecosystem == eco {
			out = append(out, u.Directory)
		}
	}
	return out
}

// swiftProject builds a throwaway git repository: every path in tracked is
// written AND committed, every path in untracked is written only. It returns the
// root to put in Config.Root.
//
// Real repositories rather than plain directories, because what makes a Swift
// manifest real to Dependabot is that the REPOSITORY contains it. rail has a
// Package.resolved on disk at exactly the path Steps has one; the difference
// between the repo whose swift updates work and the repo whose Dependabot job
// aborts daily is a single `*.resolved` line in .gitignore. A fixture that only
// wrote files would call those two cases identical.
func swiftProject(t *testing.T, tracked, untracked []string) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	for _, f := range tracked {
		write(f)
	}
	if len(tracked) > 0 {
		git(append([]string{"add", "--"}, tracked...)...)
		git("commit", "-qm", "contents")
	}
	for _, f := range untracked {
		write(f)
	}
	return repo
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
//
// This is the Steps shape, and Steps' swift updates work: the entry is at "/" and
// the resolved file is committed inside the .xcodeproj at the component root.
func TestDependabotWatchesSwiftForIOS(t *testing.T) {
	repo := swiftProject(t, []string{"P.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"}, nil)
	cfg := &config.Config{
		Root:       repo,
		Project:    config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	if got := dirsFor(renderDependabot(t, cfg), "swift"); len(got) != 1 || got[0] != "/" {
		t.Errorf("swift directories = %v, want [/]", got)
	}
}

// `directory` is per-manifest and Dependabot has no glob for it, so a component
// in a subdirectory must be named. kit keeps its project at Kit/Kit.xcodeproj,
// where "/" finds nothing at all.
func TestDependabotFollowsComponentDirectories(t *testing.T) {
	repo := swiftProject(t, []string{"Kit/Kit.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"}, nil)
	cfg := &config.Config{
		Root:       repo,
		Project:    config.Project{ProjectName: "Kit", Scheme: "Kit", BundleID: "com.x.k", AscAppID: "1", Xcodeproj: "Kit/Kit.xcodeproj"},
		Components: []config.Component{{Path: "Kit", Profiles: []string{"ios"}}},
	}
	if dir := ecosystemsAt(renderDependabot(t, cfg))["swift"]; dir != "/Kit" {
		t.Errorf("swift directory = %q, want \"/Kit\"", dir)
	}
}

// A bundled Package.resolved BELOW the component's own directory keeps the entry
// at the component, because Dependabot's search for one is recursive.
//
// kit is the proof and it is the reason this rule is a rule rather than a guess:
// its component is ".", its resolved file is at Kit/Kit.xcodeproj/…, and its
// swift PRs (purchases-ios-spm, sentry-cocoa) open. Naming the deeper directory
// instead would be a change with nothing wrong to fix.
func TestDependabotKeepsTheComponentDirWhenTheBundleIsDeeper(t *testing.T) {
	repo := swiftProject(t, []string{"Kit/Kit.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"}, nil)
	cfg := &config.Config{
		Root:       repo,
		Project:    config.Project{ProjectName: "Kit", Scheme: "Kit", BundleID: "com.x.k", AscAppID: "1", Xcodeproj: "Kit/Kit.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	if got := dirsFor(renderDependabot(t, cfg), "swift"); len(got) != 1 || got[0] != "/" {
		t.Errorf("swift directories = %v, want [/]", got)
	}
}

// A component with NO Swift manifest in the repository gets no swift entry at all.
//
// Not a harmless no-op, which is what the previous unconditional entry assumed.
// Dependabot aborts the whole job — "Error during file fetching; aborting: Repo
// must contain a Package.swift configuration file or an .xcodeproj/.xcworkspace
// directory with a Package.resolved file" — so the entry also took down the
// github-actions updates in the same file. Queueify is this shape: an ios
// component at ios/ whose xcodeproj is excluded from the repo by .gitignore.
func TestDependabotEmitsNoSwiftEntryWithoutAManifest(t *testing.T) {
	repo := swiftProject(t, []string{"ios/Queueify/Queueify.xcodeproj/project.pbxproj"}, nil)
	cfg := &config.Config{
		Root:       repo,
		Project:    config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "com.x.q", AscAppID: "1", Xcodeproj: "ios/Queueify/Queueify.xcodeproj"},
		Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}},
	}
	doc := renderDependabot(t, cfg)
	if got := dirsFor(doc, "swift"); len(got) != 0 {
		t.Errorf("swift entries = %v, want none: every one of those fails the whole Dependabot job daily", got)
	}
	// The rest of the file must survive. The abort this prevents was costing the
	// repo its action updates, so dropping them here would trade one silence for
	// another.
	if ecosystemsAt(doc)["github-actions"] != "/" {
		t.Error("the repo-wide github-actions entry went missing")
	}
}

// A manifest that exists on disk but NOT in the repository does not count.
//
// This is rail, exactly: Rail.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/
// Package.resolved is right there in the working tree, and `*.resolved` in
// .gitignore keeps it out of the repo — so Dependabot fetches nothing and aborts.
// RailCore/Package.swift and RailData/Package.swift ARE committed and still do not
// qualify: no entry in this fleet has been observed to work without a resolved
// file, and a wrong entry costs a failed job every day where a missing one costs
// visibility. Prefer nothing.
func TestDependabotIgnoresManifestsTheRepoDoesNotContain(t *testing.T) {
	repo := swiftProject(t,
		[]string{"RailCore/Package.swift", "RailData/Package.swift"},
		[]string{"Rail.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved"})
	cfg := &config.Config{
		Root:       repo,
		Project:    config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "com.x.r", AscAppID: "1", Xcodeproj: "Rail.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios", "supabase"}}},
	}
	if got := dirsFor(renderDependabot(t, cfg), "swift"); len(got) != 0 {
		t.Errorf("swift entries = %v, want none: nothing there is in the repository Dependabot reads", got)
	}
}

// A nested SwiftPM package is named exactly, because Dependabot does not find one
// from above.
//
// windsock is the case: WindsockKit/Package.swift and WindsockKit/Package.resolved
// are committed one level below the root, its entry pointed at "/", and it has
// never opened a swift PR — only the actions ones. The asymmetry with the test
// above (bundles found recursively, bare packages not) is empirical, not
// documented, which is why both directions have a test.
func TestDependabotPointsSwiftAtANestedPackage(t *testing.T) {
	repo := swiftProject(t, []string{
		"Windsock.xcodeproj/project.pbxproj", // an app project with no resolved file of its own
		"WindsockKit/Package.swift",
		"WindsockKit/Package.resolved",
	}, nil)
	cfg := &config.Config{
		Root:       repo,
		Project:    config.Project{ProjectName: "Windsock", Scheme: "Windsock", BundleID: "com.x.w", AscAppID: "1", Xcodeproj: "Windsock.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	if got := dirsFor(renderDependabot(t, cfg), "swift"); len(got) != 1 || got[0] != "/WindsockKit" {
		t.Errorf("swift directories = %v, want [/WindsockKit]", got)
	}
}

// A repo shipping both stacks must get both, each at its own path — this is the
// shape that a single hand-written file gets wrong.
func TestDependabotCoversEveryComponent(t *testing.T) {
	repo := swiftProject(t, []string{
		"ios/Rail.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved",
		"web/package.json",
	}, nil)
	cfg := &config.Config{
		Root:    repo,
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

// npm and github-actions are derived from the manifest alone and must stay that
// way: a web component was detected BY its package.json, so its entry cannot
// point at nothing, and requiring a git lookup for it would make an in-memory
// project lose coverage for no reason. Only swift consults the repository.
func TestDependabotNeedsNoRepoForNpmOrActions(t *testing.T) {
	cfg := &config.Config{ // no Root: nothing on disk to consult
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{
			{Path: "site", Stack: "web"},
			{Path: "ios", Profiles: []string{"ios"}},
		},
	}
	got := ecosystemsAt(renderDependabot(t, cfg))
	if got["npm"] != "/site" {
		t.Errorf("npm directory = %q, want /site", got["npm"])
	}
	if got["github-actions"] != "/" {
		t.Errorf("github-actions directory = %q, want /", got["github-actions"])
	}
	// And the swift entry is absent rather than guessed, because an unknown root
	// is not evidence of a manifest.
	if dir, ok := got["swift"]; ok {
		t.Errorf("swift entry at %q from a project with no known root; that is the guess that fails jobs", dir)
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

		// ONE job, with the work gated on a staleness step inside it.
		//
		// This was briefly two jobs — a cheap ubuntu gate plus the publish job —
		// so a skip would not occupy the Mac. That trade was wrong on cost:
		// GitHub bills a MINIMUM OF ONE MINUTE PER JOB, so the gate cost a full
		// billed minute per repository per day (~420/month across the fleet) to
		// save about twenty seconds of a runner that is not otherwise busy at
		// 03:00. One job pays one minute whether it skips or publishes.
		if len(doc.Jobs) != 1 {
			t.Errorf("%s docs.yml has %d jobs; each one costs a billed minute even when it skips", profile, len(doc.Jobs))
		}
		for name, j := range doc.Jobs {
			if j.Needs != nil {
				t.Errorf("%s docs.yml job %q depends on another job — that is a second billed minute", profile, name)
			}
		}
	}
}

// Grouping is the lever for PR volume; `ignore` is not. Grouping changes how
// many PRs carry the updates, not which updates are offered.
//
// Majors must stay OUT of the group: this fleet lost Dead Code Analysis in seven
// repositories to a download-artifact v7.0.1 that never existed, and a major
// buried in a batch of twenty is a major nobody read.
func TestDependabotGroupsRoutineUpdatesButNotMajors(t *testing.T) {
	cfg := &config.Config{
		Project:    config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	raw, err := os.ReadFile(filepath.Join(root(t), "core", "root", ".github", "dependabot.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := tokens.Substitute(string(raw), tokens.Values(cfg, ""))

	var doc struct {
		Updates []struct {
			Ecosystem string `yaml:"package-ecosystem"`
			Ignore    []any  `yaml:"ignore"`
			Groups    map[string]struct {
				Patterns    []string `yaml:"patterns"`
				UpdateTypes []string `yaml:"update-types"`
			} `yaml:"groups"`
		} `yaml:"updates"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered dependabot.yml is not valid YAML: %v\n%s", err, out)
	}

	for _, u := range doc.Updates {
		if len(u.Ignore) > 0 {
			t.Errorf("%s: has ignore rules — volume is managed by grouping, not by silencing updates", u.Ecosystem)
		}
		if len(u.Groups) == 0 {
			t.Errorf("%s: no groups, so every update opens its own PR", u.Ecosystem)
			continue
		}
		for name, g := range u.Groups {
			for _, ut := range g.UpdateTypes {
				if ut == "major" {
					t.Errorf("%s group %q includes majors; they must stay individually reviewable", u.Ecosystem, name)
				}
			}
			if len(g.UpdateTypes) == 0 {
				t.Errorf("%s group %q has no update-types, which would swallow majors too", u.Ecosystem, name)
			}
		}
	}
}

// A component the lacquer does NOT manage still gets its dependencies watched.
//
// This is the gap that motivated `stack`. Entries used to render from `profiles`
// alone, so a component with hand-written CI and no profile got nothing — one
// project had a Next.js admin app with 36 npm dependencies watched by nothing,
// and it was invisible because the file that should have listed it looked
// complete.
//
// Watching dependencies and adopting shared CI are different decisions. A
// project may keep its own workflows and still want security updates.
func TestDependabotWatchesUnmanagedComponents(t *testing.T) {
	repo := swiftProject(t, []string{
		"ios/P.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved",
		"admin/package.json",
	}, nil)
	cfg := &config.Config{
		Root:    repo,
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "ios/P.xcodeproj"},
		Components: []config.Component{
			{Path: "ios", Profiles: []string{"ios"}},
			// Declared, detected as web, deliberately unmanaged.
			{Path: "admin", Stack: "web"},
		},
	}
	got := ecosystemsAt(renderDependabot(t, cfg))
	if dir, ok := got["npm"]; !ok || dir != "/admin" {
		t.Errorf("an unmanaged web component got npm=%q (present=%v); want /admin", dir, ok)
	}
	if got["swift"] != "/ios" {
		t.Errorf("the managed ios component lost its entry: %q", got["swift"])
	}
}

// A stack with no dependency manifest Dependabot supports must emit NOTHING
// rather than an entry pointing at configuration. supabase/config.toml is
// config, not a lockfile — an entry there is a promise the tool cannot keep.
func TestDependabotSkipsStacksWithNoManifest(t *testing.T) {
	cfg := &config.Config{
		Project:    config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{{Path: "server", Stack: "supabase"}},
	}
	doc := renderDependabot(t, cfg)
	for _, u := range doc.Updates {
		if u.Directory == "/server" {
			t.Errorf("supabase component got a %q entry at /server; it has no manifest Dependabot reads", u.Ecosystem)
		}
	}
	// github-actions must still be there — that is repo-wide, not per-stack.
	if ecosystemsAt(doc)["github-actions"] != "/" {
		t.Error("the repo-wide github-actions entry went missing")
	}
}

// Stack and profile naming the same ecosystem must not double up.
func TestDependabotDoesNotDuplicateEcosystems(t *testing.T) {
	cfg := &config.Config{
		Project:    config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Components: []config.Component{{Path: "web", Stack: "web", Profiles: []string{"web"}}},
	}
	n := 0
	for _, u := range renderDependabot(t, cfg).Updates {
		if u.Ecosystem == "npm" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d npm entries for one component; stack and profile agreeing must not double up", n)
	}
}

// A default-off workflow must not ship unless the project asks for it.
//
// testflight-feedback is the reason this exists: it needs
// APP_STORE_CONNECT_FEEDBACK_ISSUER_ID and two siblings, no project in the fleet
// had them, and it had therefore been red on EVERY scheduled run since it was
// added, in every repository that received it. A scheduled job nobody can
// satisfy is worse than a missing feature — it trains people to ignore red.
func TestOptionalWorkflowsShipOnlyWhenRequested(t *testing.T) {
	r := root(t)
	// It must still exist in the lacquer: dropped as a default, kept as a choice.
	if _, err := os.Stat(filepath.Join(r, "profiles", "ios", "workflows-optional", "testflight-feedback.yml")); err != nil {
		t.Fatalf("the optional workflow is gone entirely, not just defaulted off: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r, "profiles", "ios", "workflows", "testflight-feedback.yml")); err == nil {
		t.Error("testflight-feedback is still in workflows/, so it would ship by default")
	}

	base := &config.Config{
		Project:    config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj", SwiftVersion: "6.0"},
		Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}},
	}
	has := func(cfg *config.Config) bool {
		plan, err := assets.Plan(r, cfg)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		for _, a := range plan {
			if strings.HasSuffix(a.Dest, "ios-testflight-feedback.yml") {
				return true
			}
		}
		return false
	}
	if has(base) {
		t.Error("shipped without being requested")
	}

	opted := *base
	opted.Project.OptionalWorkflows = []string{"testflight-feedback"}
	if !has(&opted) {
		t.Error("did not ship when explicitly requested, so opting in does nothing")
	}

	// A typo must fail loudly. Silently installing nothing is the exact failure
	// this mechanism exists to prevent.
	typo := *base
	typo.Project.OptionalWorkflows = []string{"testflght-feedback"}
	if _, err := assets.Plan(r, &typo); err == nil {
		t.Error("a misspelled optional workflow was accepted; it would silently install nothing")
	}
}

// The dead-code job must CREATE the labels it files issues with.
//
// `gh issue create --label` fails outright on a label the repository does not
// have — "could not add label: 'dead-code' not found" — and that killed this job
// in all nine iOS repositories, none of which had either label. The scan worked
// the whole time; nothing ever filed the issue it produced, which is the quiet
// kind of broken: green scan, no output, no error anyone reads.
func TestDeadCodeCreatesItsOwnLabels(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "ios", "workflows", "dead-code.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := lacquerToken.ReplaceAllString(string(raw), "x")

	var doc struct {
		Jobs map[string]struct {
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	job, ok := doc.Jobs["create-issue"]
	if !ok {
		t.Fatal("no create-issue job")
	}

	var create, label int
	for _, st := range job.Steps {
		if strings.Contains(st.Run, "gh issue create") {
			create++
			for _, l := range []string{"dead-code", "maintenance"} {
				if !strings.Contains(st.Run, "gh label create "+l) {
					t.Errorf("the step running `gh issue create` does not create the %q label first; the call fails outright if it is missing", l)
				}
			}
			// Idempotence matters: this runs on every scan, and "already exists"
			// must not fail the job.
			if !strings.Contains(st.Run, "|| true") {
				t.Error("label creation is not tolerant of an existing label, so the second scan would fail")
			}
			label++
		}
	}
	if create == 0 {
		t.Error("no step creates an issue")
	}
	// Creating a label needs issues:write, same as creating the issue.
	if job.Permissions["issues"] != "write" {
		t.Errorf("create-issue has issues=%q; label creation needs write", job.Permissions["issues"])
	}
}
