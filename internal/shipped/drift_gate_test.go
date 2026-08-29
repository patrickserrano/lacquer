package shipped

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// The `No lacquer drift` job clones the lacquer, sets up Go, builds the binary
// and runs `lacquer audit`. Actions bills a MINIMUM OF ONE MINUTE PER JOB, so
// its cost is a full billed minute on every PR — including the overwhelming
// majority that touch no lacquer-managed file at all. It is now gated on a paths
// filter computed by the `changes` job.
//
// That gate has two ways to be wrong, and they are not symmetric:
//
//   - Too NARROW: drift stops running on a PR that really did change a managed
//     file, and the fleet's only drift check silently reports nothing. Nothing
//     fails; the check just stops existing. TestDriftGateCoversEveryManagedPath
//     derives the managed set from assets.Plan — the same function sync and
//     audit use — so an asset kind added to the lacquer without widening the
//     filter fails here rather than going unnoticed in seventeen repos.
//   - `ci-ok` breaking on a SKIPPED dependency. `ci-ok` is the required status
//     check every repo's branch protection points at. A dependent job skips by
//     default when a job it needs skips, and a required check that never reports
//     wedges branch protection on every PR in the fleet. TestCIOKPassesWhenDriftSkips
//     runs the actual aggregation shell to prove skipped is treated as passing.

// ciProfiles are the profiles shipping a ci.yml with a drift job.
var ciProfiles = []string{"ios", "supabase", "web"}

// ciConfig is a manifest complete enough to render any profile's ci.yml.
func ciConfig() *config.Config {
	return &config.Config{
		Project: config.Project{
			Name: "demo", ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
			AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
			// Every tool, so skills fan out into .claude, .codex and .agents and
			// the AGENTS.md mirrors exist — the widest managed set a project can
			// have is the one the filter has to cover.
			Tools: []string{"claude", "codex", "antigravity"},
		},
	}
}

// renderCI renders a profile's ci.yml the way sync would.
func renderCI(t *testing.T, profile string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", profile, "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	out, missing := tokens.Substitute(string(raw), tokens.Values(ciConfig(), ""))
	if len(missing) > 0 {
		t.Fatalf("%s ci.yml has unsubstituted tokens: %v", profile, missing)
	}
	if m := lacquerToken.FindString(out); m != "" {
		t.Fatalf("%s ci.yml still contains the lacquer token %s after rendering", profile, m)
	}
	return out
}

// ciDoc is the slice of a rendered workflow these tests assert on.
type ciDoc struct {
	Jobs map[string]struct {
		Needs   yaml.Node         `yaml:"needs"` // a scalar or a sequence
		If      string            `yaml:"if"`
		Outputs map[string]string `yaml:"outputs"`
		Steps   []struct {
			ID  string `yaml:"id"`
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func parseCI(t *testing.T, profile string) ciDoc {
	t.Helper()
	var doc ciDoc
	if err := yaml.Unmarshal([]byte(renderCI(t, profile)), &doc); err != nil {
		t.Fatalf("rendered %s ci.yml is not valid YAML: %v", profile, err)
	}
	return doc
}

// needsList normalizes `needs:` (which Actions accepts as a bare string or a
// sequence) into a slice.
func needsList(t *testing.T, n yaml.Node) []string {
	t.Helper()
	if n.IsZero() {
		return nil
	}
	var one string
	if err := n.Decode(&one); err == nil {
		return []string{one}
	}
	var many []string
	if err := n.Decode(&many); err != nil {
		t.Fatalf("cannot decode needs: %v", err)
	}
	return many
}

// TestDriftIsGatedOnTheChangesJob asserts the wiring: drift depends on changes,
// its `if` consults the changes job's `lacquer` output, and `changes` actually
// publishes that output. An `if` naming an output no job declares evaluates to
// the empty string, which is not 'true' — so the job would skip on every run,
// forever, in silence. Asserting all three together is what catches that.
func TestDriftIsGatedOnTheChangesJob(t *testing.T) {
	for _, profile := range ciProfiles {
		t.Run(profile, func(t *testing.T) {
			doc := parseCI(t, profile)

			changes, ok := doc.Jobs["changes"]
			if !ok {
				t.Fatal("no `changes` job")
			}
			if _, ok := changes.Outputs["lacquer"]; !ok {
				t.Errorf("the changes job declares no `lacquer` output (has %v)", keysOf(changes.Outputs))
			}

			drift, ok := doc.Jobs["drift"]
			if !ok {
				t.Fatal("no `drift` job")
			}
			if drift.If == "" {
				t.Fatal("the drift job is unconditional — it burns a billed minute on every PR")
			}
			if !strings.Contains(drift.If, "needs.changes.outputs.lacquer") {
				t.Errorf("drift `if` is %q — it must consult the changes job's `lacquer` output", drift.If)
			}
			if !contains(needsList(t, drift.Needs), "changes") {
				t.Errorf("drift needs %v — a job cannot read needs.changes.outputs without depending on changes",
					needsList(t, drift.Needs))
			}
		})
	}
}

// TestCIOKPassesWhenDriftSkips runs the real aggregation shell out of the
// rendered workflow with every combination of results that matters.
//
// This is the one way this change could be actively harmful: `ci-ok` is the
// single required status check, and if a skipped `drift` made it fail (or skip),
// branch protection would wedge on every PR in the fleet. Asserting on the YAML
// would not exercise it — the decision lives in the loop's comparison — so the
// script is extracted and executed.
func TestCIOKPassesWhenDriftSkips(t *testing.T) {
	for _, profile := range ciProfiles {
		t.Run(profile, func(t *testing.T) {
			doc := parseCI(t, profile)
			ciOK, ok := doc.Jobs["ci-ok"]
			if !ok {
				t.Fatal("no `ci-ok` job")
			}
			// Without always(), ci-ok inherits the default "skip if a needed job
			// skipped" behaviour and the required check never reports at all.
			if !strings.Contains(strings.ReplaceAll(ciOK.If, " ", ""), "always()") {
				t.Errorf("ci-ok `if` is %q — it must be always() so the required check still reports "+
					"when a job it needs skips", ciOK.If)
			}
			needs := needsList(t, ciOK.Needs)
			if !contains(needs, "drift") {
				t.Fatalf("ci-ok needs %v — drift must stay a gated job, not become unreported", needs)
			}
			if len(ciOK.Steps) == 0 {
				t.Fatal("ci-ok has no steps")
			}
			script := ciOK.Steps[0].Run

			// The results of every job but drift, held at success.
			others := map[string]string{}
			for _, n := range needs {
				if n != "drift" {
					others[n] = "success"
				}
			}
			allSkipped := map[string]string{}
			for _, n := range needs {
				allSkipped[n] = "skipped"
			}

			for _, tc := range []struct {
				name     string
				results  map[string]string
				wantFail bool
			}{
				{"drift skipped, everything else green", withResult(others, "drift", "skipped"), false},
				{"drift green", withResult(others, "drift", "success"), false},
				{"every job skipped (a docs-only PR)", allSkipped, false},
				{"drift failed", withResult(others, "drift", "failure"), true},
				{"drift cancelled", withResult(others, "drift", "cancelled"), true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					out, failed := runAggregator(t, script, tc.results)
					if failed != tc.wantFail {
						t.Errorf("ci-ok failed=%v, want %v\nresults: %v\noutput:\n%s",
							failed, tc.wantFail, tc.results, out)
					}
				})
			}
		})
	}
}

// needsResult matches the `${{ needs.<job>.result }}` expressions the aggregator
// is written in, so a fabricated result can be substituted for each.
var needsResult = regexp.MustCompile(`\$\{\{\s*needs\.([A-Za-z0-9_-]+)\.result\s*\}\}`)

// runAggregator substitutes job results into the extracted ci-ok script and runs
// it, reporting whether it failed.
func runAggregator(t *testing.T, script string, results map[string]string) (string, bool) {
	t.Helper()
	substituted := needsResult.ReplaceAllStringFunc(script, func(m string) string {
		job := needsResult.FindStringSubmatch(m)[1]
		r, ok := results[job]
		if !ok {
			t.Fatalf("the aggregator reads needs.%s.result, which is not in its needs list", job)
		}
		return r
	})
	if strings.Contains(substituted, "${{") {
		t.Fatalf("unsubstituted Actions expression left in the aggregator:\n%s", substituted)
	}
	cmd := exec.Command("bash", "-c", substituted)
	out, err := cmd.CombinedOutput()
	return string(out), err != nil
}

func withResult(base map[string]string, job, result string) map[string]string {
	out := map[string]string{job: result}
	for k, v := range base {
		if k != job {
			out[k] = v
		}
	}
	return out
}

// lacquerPathsRe pulls the shell variable holding the paths filter out of the
// changes job's script, so the Go assertions below test the exact regex that
// ships rather than a copy of it.
var lacquerPathsRe = regexp.MustCompile(`(?m)^\s*lacquer_paths='([^']*)'\s*$`)

// driftFilter returns the compiled paths filter from a profile's ci.yml.
func driftFilter(t *testing.T, profile string) (*regexp.Regexp, string) {
	t.Helper()
	script := changesScript(t, profile)
	m := lacquerPathsRe.FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("no `lacquer_paths=` assignment in the %s changes job", profile)
	}
	// The filter must actually be what the gate consults; an assignment nothing
	// reads would make every assertion here vacuous.
	if !strings.Contains(script, `grep -qE "$lacquer_paths"`) {
		t.Errorf("the %s changes job does not gate on $lacquer_paths", profile)
	}
	re, err := regexp.Compile(m[1])
	if err != nil {
		t.Fatalf("the %s paths filter does not compile: %v", profile, err)
	}
	return re, m[1]
}

// changesScript returns the `filter` step's shell from a rendered ci.yml.
func changesScript(t *testing.T, profile string) string {
	t.Helper()
	for _, st := range parseCI(t, profile).Jobs["changes"].Steps {
		if st.ID == "filter" {
			return st.Run
		}
	}
	t.Fatalf("no `filter` step in the %s changes job", profile)
	return ""
}

// TestDriftGateCoversEveryManagedPath is the guard against the silent failure.
//
// The managed set is derived from assets.Plan — the function sync writes from
// and audit re-derives its units from — rather than hand-listed, so adding an
// asset kind to the lacquer without widening the filter fails here. It also
// covers the inputs audit reads that are NOT assets: the manifest and lock, the
// CLAUDE.md / AGENTS.md regions, the stack markers audit re-detects from (exit
// 6) and the pbxproj the baseline check reads (exit 4). Every one of those can
// make audit fail with no managed file touched at all.
func TestDriftGateCoversEveryManagedPath(t *testing.T) {
	r := root(t)
	cfg := ciConfig()
	// Both layouts at once: a root component and a nested one. Config assets
	// land under the component directory, so a filter anchored at the repo root
	// would pass for "." and silently miss every nested project.
	cfg.Components = []config.Component{
		{Path: ".", Profiles: []string{"web"}},
		{Path: "app", Profiles: []string{"ios"}},
		{Path: "server", Profiles: []string{"supabase"}},
	}

	plan, err := assets.Plan(r, cfg)
	if err != nil {
		t.Fatalf("plan assets: %v", err)
	}
	if len(plan) == 0 {
		t.Fatal("assets.Plan returned nothing — this test would assert nothing")
	}

	managed := make([]string, 0, len(plan)+16)
	for _, a := range plan {
		managed = append(managed, filepath.ToSlash(a.Dest))
	}
	for _, c := range cfg.Components {
		for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
			managed = append(managed, filepath.ToSlash(filepath.Join(c.Path, name)))
		}
	}
	managed = append(managed,
		// The managed .gitignore region — a region like CLAUDE.md, not an asset,
		// so assets.Plan does not surface it. Editing the credential rules
		// inside the markers is drift, and the gate has to wake for it.
		".gitignore",
		// The manifest and the lock: every token, exclusion and baseline
		// relaxation audit evaluates comes out of these.
		".lacquer.toml", ".lacquer.lock",
		// Stack markers — audit re-detects components and exits 6 on one the
		// manifest never declared.
		"package.json", "app/package.json", "Cargo.toml", "go.mod",
		"Package.swift", "app/Package.swift", "server/supabase/config.toml",
		"app/.swiftlint.yml", "app/.swiftformat",
		// The baseline check (exit 4) parses the pbxproj.
		"app/Demo.xcodeproj/project.pbxproj",
	)

	for _, profile := range ciProfiles {
		t.Run(profile, func(t *testing.T) {
			re, src := driftFilter(t, profile)
			var missed []string
			for _, dest := range managed {
				if !re.MatchString(dest) {
					missed = append(missed, dest)
				}
			}
			sort.Strings(missed)
			if len(missed) > 0 {
				t.Errorf("the drift gate does not fire for %d lacquer-managed path(s), so a PR "+
					"changing them would skip the only drift check in the fleet:\n  %s\n\nfilter: %s",
					len(missed), strings.Join(missed, "\n  "), src)
			}
		})
	}
}

// TestDriftGateStillSkipsOrdinarySource is the other half of the coverage test:
// a filter of `.` would pass it and save nothing. These are the paths the change
// exists to skip.
func TestDriftGateStillSkipsOrdinarySource(t *testing.T) {
	unmanaged := []string{
		"app/Sources/Views/ContentView.swift",
		"app/DemoTests/ModelTests.swift",
		"src/components/Button.tsx",
		"server/supabase/functions/hello/index.ts",
		"server/supabase/migrations/20240101_init.sql",
		"app/Resources/Assets.xcassets/AppIcon.appiconset/Contents.json",
	}
	for _, profile := range ciProfiles {
		t.Run(profile, func(t *testing.T) {
			re, src := driftFilter(t, profile)
			for _, p := range unmanaged {
				if re.MatchString(p) {
					t.Errorf("the drift gate fires for %q, which the lacquer does not manage — "+
						"a filter this wide saves nothing\nfilter: %s", p, src)
				}
			}
		})
	}
}

// TestChangesFilterEmitsLacquerOutput runs the real `changes` shell against a
// real git repo, because everything above tests the regex rather than the job.
// The output name, the three-dot diff, the non-PR branch and the grep all have
// to line up for the gate to work, and only executing it proves they do.
func TestChangesFilterEmitsLacquerOutput(t *testing.T) {
	for _, profile := range ciProfiles {
		t.Run(profile, func(t *testing.T) {
			script := changesScript(t, profile)
			for _, tc := range []struct {
				name    string
				event   string
				changed []string
				want    string
			}{
				{"a Swift-only PR", "pull_request", []string{"app/Sources/Model.swift"}, "false"},
				{"a docs-only PR", "pull_request", []string{"README.md", "docs/guide.md"}, "false"},
				{"a synced workflow", "pull_request", []string{".github/workflows/ios-ci.yml"}, "true"},
				{"the manifest", "pull_request", []string{".lacquer.toml"}, "true"},
				{"a managed CLAUDE region", "pull_request", []string{"CLAUDE.md"}, "true"},
				{"a nested lint config", "pull_request", []string{"app/.swiftlint.yml"}, "true"},
				{"a new stack marker", "pull_request", []string{"web/package.json"}, "true"},
				{"a synced skill", "pull_request", []string{".claude/skills/handoff/SKILL.md"}, "true"},
				// The backstop: a push to main re-audits unconditionally, which is
				// what still catches an expiry that passed with no file changing.
				{"a push to main", "push", []string{"app/Sources/Model.swift"}, "true"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got := runChangesFilter(t, script, tc.event, tc.changed)
					if got["lacquer"] != tc.want {
						t.Errorf("lacquer=%q, want %q (changed: %v)\nall outputs: %v",
							got["lacquer"], tc.want, tc.changed, got)
					}
				})
			}
		})
	}
}

// ghExpr matches the Actions expressions the changes script reads its inputs
// from, so they can be replaced with shell variables before it is executed.
var ghExpr = regexp.MustCompile(`\$\{\{[^}]*\}\}`)

// runChangesFilter executes the changes job's shell against a throwaway git repo
// whose HEAD commit touches exactly `changed`, returning the emitted outputs.
func runChangesFilter(t *testing.T, script, event string, changed []string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("seed.txt", "seed\n")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	for _, rel := range changed {
		write(rel, "changed\n")
	}
	git("add", "-A")
	git("commit", "-qm", "head")
	head := git("rev-parse", "HEAD")

	// Feed the job's inputs in as environment rather than hard-coding them, so a
	// future edit that reads a NEW expression trips the assertion below instead
	// of being silently substituted with something meaningless.
	sub := strings.NewReplacer(
		"${{ github.event_name }}", "$EVENT_NAME",
		"${{ github.event.pull_request.base.sha }}", "$BASE_SHA",
		"${{ github.event.pull_request.head.sha }}", "$HEAD_SHA",
	).Replace(script)
	if m := ghExpr.FindString(sub); m != "" {
		t.Fatalf("the changes script reads %s, which this test does not know how to supply", m)
	}

	outPath := filepath.Join(t.TempDir(), "gh_output")
	if err := os.WriteFile(outPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", sub)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"EVENT_NAME="+event, "BASE_SHA="+base, "HEAD_SHA="+head, "GITHUB_OUTPUT="+outPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("changes script failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

func keysOf(m map[string]string) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return fmt.Sprint(ks)
}
