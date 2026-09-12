package shipped

// The action pin ledger: .github/workflows/action-pin-ledger.yml mirrors every
// action reference the profiles ship, so Dependabot's own pull request becomes
// the notification that a shipped ref went stale.
//
// The ledger is only worth having if it cannot quietly stop mirroring, so the
// tests here assert the two halves that can each fail silently:
//
//   - the ledger and the profiles name the SAME refs, checked in both
//     directions — a ref added to a profile and missing here, and a ref here
//     that no profile ships any more;
//   - the ledger is shaped so Dependabot actually reads it. That is not
//     rhetorical: dependabot-core's parser starts at the top-level `jobs` key
//     (github_actions/lib/dependabot/github_actions/workflow_file/
//     uses_collector.rb), so a file that loses that key, or that grows a ref
//     somewhere the collector does not walk, watches NOTHING while still
//     looking like a ledger. Half of this file exists to make that state fail.
//
// The refs are collected by RENDERING the shipped workflows rather than by
// grepping profiles/, because one of them is not in profiles/ at all:
// pnpm/action-setup@v6 is emitted from Go, by tokens.WebPMSetupBlock. A grep
// would have shipped a ledger that was already incomplete on the day it landed.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// ledgerPath is the ledger, relative to the lacquer checkout root.
//
// The directory and the extension are load-bearing, not cosmetic: Dependabot's
// file fetcher selects `.github/workflows` entries whose name matches
// /\.ya?ml$/ and a root action.yml, and nothing else. Moved anywhere else, the
// file parses fine, reads fine, and is watched by nothing.
const ledgerPath = ".github/workflows/action-pin-ledger.yml"

// githubRepoRef is dependabot-core's GITHUB_REPO_REFERENCE, which a `uses:`
// value must match before it is treated as an action dependency at all.
var githubRepoRef = regexp.MustCompile(`^[\w.-]+/[\w.-]+(/[^@]+)?@.+`)

// usesLine matches the textual form of a step's action reference. It is used
// ONLY to cross-check the structural walk below — see
// TestActionLedgerIsWhereDependabotWillLookForIt.
var usesLine = regexp.MustCompile(`^\s*(?:-\s+)?uses:`)

// actionRefs returns the `uses:` values Dependabot would find in a workflow,
// walking it the way uses_collector.rb does rather than by scanning lines.
//
// The walk is transcribed, not approximated, because its quirks are the point:
// a mapping that has a `uses` key is taken as a step and NOT descended into, so
// a nested `with: {source: actions/checkout@v2}` is invisible to Dependabot
// (dependabot-core's own pin_to_sha.yml fixture pins that behaviour); a mapping
// with `steps` descends there; anything else has all its values walked, which
// is how a reusable-workflow `jobs.x.uses` is reached. Collecting refs with a
// line regex would find references in both places and would therefore report a
// ledger as watched when Dependabot cannot see it.
//
// where is used only to name the file in a failure message.
func actionRefs(t *testing.T, where, content string) []string {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		// Not cosmetic. Dependabot raises DependencyFileNotParseable for an
		// unparseable workflow, which fails the repository's whole
		// github-actions update run — every other action stops being watched
		// too, and the only symptom is that the PRs stop arriving.
		t.Fatalf("%s is not valid YAML, which would abort Dependabot's entire github-actions run for this repo: %v", where, err)
	}
	var out []string
	for _, u := range usesFrom(doc) {
		// The parser skips these two before doing anything else: a local
		// composite action and a container step are not versioned dependencies.
		if strings.HasPrefix(u, ".") || strings.HasPrefix(u, "docker://") {
			continue
		}
		if !githubRepoRef.MatchString(u) {
			continue
		}
		out = append(out, u)
	}
	return out
}

// usesFrom is uses_collector.rb's workflow_root: the walk starts at the
// top-level `jobs` key, or at `runs` for a composite action, and a document
// with neither yields nothing at all.
func usesFrom(doc any) []string {
	root, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	for _, k := range []string{"jobs", "runs"} {
		if v, ok := root[k]; ok {
			return collectUses(v)
		}
	}
	return nil
}

// collectUses is uses_collector.rb's collect_uses/collect_uses_from_hash.
func collectUses(v any) []string {
	switch t := v.(type) {
	case map[string]any:
		if u, ok := t["uses"]; ok {
			// A non-string `uses` is dropped rather than descended into, which
			// is what the Ruby does.
			if s, ok := u.(string); ok {
				return []string{s}
			}
			return nil
		}
		if s, ok := t["steps"]; ok {
			return collectUses(s)
		}
		// Ruby hashes preserve insertion order and Go maps do not, so the keys
		// are sorted here. Only the ORDER differs; the set does not, and every
		// caller compares sets.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			out = append(out, collectUses(t[k])...)
		}
		return out
	case []any:
		var out []string
		for _, c := range t {
			out = append(out, collectUses(c)...)
		}
		return out
	}
	return nil
}

// ledgerConfigs is the set of project shapes the shipped workflows are rendered
// under to discover every ref the lacquer can ship.
//
// It is a matrix rather than one config because a `uses:` line can be decided in
// Go: WebPMSetupBlock emits a pnpm/action-setup step for a pnpm component and
// nothing for an npm one, so a single npm config would collect nine refs and
// miss the tenth. The iOS shapes are here for the same reason — the product
// matrix rewrites large parts of profiles/ios/workflows/ci.yml — and the
// optional workflow because it ships only when a project asks for it.
//
// The matrix being incomplete is itself a silent failure, so it is not trusted:
// TestLedgerConfigsRenderEveryShippedWorkflow asserts these configs between them
// render every workflow the repo ships.
func ledgerConfigs(t *testing.T) []*config.Config {
	t.Helper()
	ios := func() *config.Config {
		return &config.Config{Project: config.Project{
			Name: "demo", ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
			AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
		}, Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}}}
	}
	// No opted-in leg: the profiles ship no optional workflow. If one is added,
	// add a config here too — TestLedgerConfigsRenderEveryShippedWorkflow fails
	// when the matrix stops covering something shipped, which is the point.
	solo := ios()

	matrix := ios()
	matrix.Product = []config.Product{
		{Name: "Paid", Scheme: "S", BundleID: "com.x.p", AscAppID: "1", TagPrefix: "paid"},
		{Name: "Free", Scheme: "F", BundleID: "com.x.f", AscAppID: "2", TagPrefix: "free"},
	}

	web := func(root string) *config.Config {
		return &config.Config{Root: root, Project: config.Project{Name: "w", ProjectName: "W", GithubOrg: "acme"},
			Components: []config.Component{{Path: ".", Profiles: []string{"web"}}}}
	}
	// detect.WebPackageManager reads the component's package.json off disk, so
	// the pnpm shape needs a real one; an in-memory Config with no Root is the
	// npm shape by definition.
	pnpmRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(pnpmRoot, "package.json"), []byte(`{"packageManager":"pnpm@9.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	supabase := &config.Config{Project: config.Project{Name: "s", ProjectName: "S", GithubOrg: "acme"},
		Components: []config.Component{{Path: ".", Profiles: []string{"supabase"}}}}

	return []*config.Config{solo, matrix, web(""), web(pnpmRoot), supabase}
}

// renderedWorkflows returns every `.github/workflows/` file the lacquer would
// write across ledgerConfigs, keyed by destination, plus the set of source
// templates that produced them (repo-relative).
//
// Rendered, not read: two of the shipped workflows are not valid YAML until
// their tokens are substituted (profiles/ios/workflows/release.yml carries
// {{IOS_RELEASE_TAGS}} inside its own `on:` block), and the refs added by
// WebPMSetupBlock exist only after substitution.
func renderedWorkflows(t *testing.T) (byDest map[string]string, srcs map[string]bool) {
	t.Helper()
	r := root(t)
	byDest = map[string]string{}
	srcs = map[string]bool{}
	for _, cfg := range ledgerConfigs(t) {
		plan, err := assets.Plan(r, cfg)
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		for _, a := range plan {
			if !strings.HasPrefix(a.Dest, ".github/workflows/") {
				continue
			}
			body, missing, err := assets.Render(a, cfg)
			if err != nil {
				t.Fatalf("Render %s: %v", a.Dest, err)
			}
			if len(missing) > 0 {
				t.Fatalf("Render %s left tokens unsubstituted: %v", a.Dest, missing)
			}
			rel := filepath.ToSlash(strings.TrimPrefix(a.Src, r+string(filepath.Separator)))
			// Keyed by destination AND source so two renders of one template
			// (npm and pnpm) are both kept; the value is only ever scanned.
			byDest[a.Dest+" <- "+rel] = string(body)
			srcs[rel] = true
		}
	}
	if len(byDest) == 0 {
		t.Fatal("no workflows rendered; the ledger would be compared against an empty set and every test below would pass vacuously")
	}
	return byDest, srcs
}

// shippedActionRefs is every distinct `owner/repo@ref` the lacquer ships, mapped
// to the source templates it came from, sorted.
//
// Deduped and sorted for the failure message's sake, not for correctness: one
// template can name the same action in fifteen steps and be rendered under
// several configs, and a raw list prints the same path twenty-four times in one
// error — which is how a report stops being read.
func shippedActionRefs(t *testing.T) map[string][]string {
	t.Helper()
	files, _ := renderedWorkflows(t)
	seen := map[string]map[string]bool{}
	for where, body := range files {
		// "<dest> <- <source template>"; the template is what someone edits.
		_, src, _ := strings.Cut(where, " <- ")
		for _, ref := range actionRefs(t, where, body) {
			if seen[ref] == nil {
				seen[ref] = map[string]bool{}
			}
			seen[ref][src] = true
		}
	}
	out := map[string][]string{}
	for ref, srcs := range seen {
		out[ref] = sortedKeys(srcs)
	}
	if len(out) == 0 {
		t.Fatal("no action refs found in any shipped workflow; the walk is broken, and an empty set would make the ledger trivially correct")
	}
	return out
}

// ledgerRefs is every `owner/repo@ref` in the ledger, and the raw file.
func ledgerRefs(t *testing.T) ([]string, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root(t), filepath.FromSlash(ledgerPath)))
	if err != nil {
		t.Fatalf("the pin ledger is gone: %v\n"+
			"It is the only thing upstream watching the action refs the profiles ship. "+
			"If it was moved, move it back: Dependabot reads %s and nowhere else.", err, ledgerPath)
	}
	return actionRefs(t, ledgerPath, string(data)), string(data)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The ledger and the profiles must name the same refs, both directions.
//
// This is the test the whole mechanism rests on. Without it the ledger goes
// stale the first time a profile changes and becomes a second unwatched copy of
// the thing it was built to watch — which is the defect this repo keeps finding
// and the reason a "just add a ledger" version of this change would have been
// worse than none.
//
// Both directions matter and they fail for different reasons. A ref in a profile
// and not here is an action nothing upstream watches — the original hole. A ref
// here and not in any profile is a Dependabot PR that will keep arriving about a
// dependency this repo does not ship, and a reviewer learning to wave the ledger
// through.
func TestActionLedgerMirrorsEveryShippedActionRef(t *testing.T) {
	shipped := shippedActionRefs(t)
	inLedger := map[string]bool{}
	ledger, _ := ledgerRefs(t)
	for _, ref := range ledger {
		inLedger[ref] = true
	}

	for _, ref := range sortedKeys(shipped) {
		if !inLedger[ref] {
			t.Errorf("%s is shipped by %s but is not in %s, so nothing upstream watches it.\n"+
				"Add `      - uses: %s` to the ledger's steps.",
				ref, strings.Join(shipped[ref], ", "), ledgerPath, ref)
		}
	}
	for _, ref := range sortedKeys(inLedger) {
		if _, ok := shipped[ref]; !ok {
			t.Errorf("%s is in %s but no profile ships it any more.\n"+
				"If Dependabot just bumped it here, make the same bump in profiles/ (or internal/tokens for "+
				"pnpm/action-setup) in this same pull request — that is what the ledger is for. "+
				"If the action was retired, drop the line.", ref, ledgerPath)
		}
	}

	// The whole correct steps list, printed ready to paste. Reconstructing it by
	// hand from the errors above is the step where someone fixes four of five
	// refs and leaves the ledger wrong in a way that still compiles.
	if t.Failed() {
		var b strings.Builder
		for _, ref := range sortedKeys(shipped) {
			fmt.Fprintf(&b, "      - uses: %s\n", ref)
		}
		t.Logf("the `steps:` block in %s should be exactly:\n%s", ledgerPath, b.String())
	}
}

// The ledger has to be shaped so Dependabot reads it, which is a different claim
// from "it is valid YAML" and from "it contains the right text".
//
// Every assertion here is a way the file can look complete and watch nothing:
// the wrong directory (the fetcher globs one), a missing top-level `jobs` key
// (the collector's entry point — with no `jobs` it returns an empty list and
// this file becomes decoration), or a ref parked somewhere the collector does
// not walk. That last one is not hypothetical: a reference under a step's
// `with:` is skipped, because a mapping carrying `uses` is never descended into.
func TestActionLedgerIsWhereDependabotWillLookForIt(t *testing.T) {
	dir, name := path.Split(ledgerPath)
	if dir != ".github/workflows/" {
		t.Errorf("the ledger is at %s; Dependabot's github-actions fetcher reads .github/workflows/ and a root action.yml, and nothing else", ledgerPath)
	}
	if ext := filepath.Ext(name); ext != ".yml" && ext != ".yaml" {
		t.Errorf("the ledger is %s; the fetcher selects workflow files by /\\.ya?ml$/", name)
	}

	refs, raw := ledgerRefs(t)

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%s is not valid YAML: %v", ledgerPath, err)
	}
	if _, ok := doc["jobs"]; !ok {
		t.Fatalf("%s has no top-level `jobs` key. uses_collector.rb starts there: without it Dependabot "+
			"collects nothing from this file and every ref in it is unwatched again, silently.", ledgerPath)
	}

	// Every `uses:` written in the file must be one the walk actually reached.
	// The two counts diverging means a ref is somewhere Dependabot will not
	// look, which reads as a watched pin and is not one.
	var lines int
	for _, l := range strings.Split(raw, "\n") {
		if usesLine.MatchString(l) {
			lines++
		}
	}
	if lines != len(refs) {
		t.Errorf("%s writes %d `uses:` lines but Dependabot's walk reaches %d of them (%v).\n"+
			"A reference outside jobs -> steps -> uses is not watched. Put every ref in a step of a job.",
			ledgerPath, lines, len(refs), refs)
	}
	if len(refs) == 0 {
		t.Errorf("%s declares no action refs at all", ledgerPath)
	}
}

// The ledger must cost nothing to keep.
//
// A workflow that fires on pull_request would add a check to every PR in the
// repo for a file whose entire purpose is to be read and never executed, and one
// that ran its steps would check out this repo nine times to no end. Neither is
// needed: Dependabot's fetcher and parser never read `on:`, and never ask
// whether a job could run — dependabot-core's composite_action.yml fixture has
// no `on:` key at all and its multiple_sources.yml fixture has jobs with no
// `runs-on:`, and both are parsed for dependencies.
func TestActionLedgerNeverRuns(t *testing.T) {
	_, raw := ledgerRefs(t)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("%s is not valid YAML: %v", ledgerPath, err)
	}

	// The key is read, not assumed. yaml.v3 resolves the bare key `on` to the
	// string "on" (YAML 1.2 core schema); Ruby's Psych, which is YAML 1.1,
	// resolves the same key to the boolean true, which is why so much advice
	// says to quote it. A missing key is a failure here rather than a silent
	// pass, because "no triggers found" and "the only trigger is
	// workflow_dispatch" are the same answer to a loop that finds nothing.
	triggers, ok := doc["on"]
	if !ok {
		t.Fatalf("%s declares no triggers; a workflow with no `on:` is an error in the repo's Actions tab", ledgerPath)
	}
	switch on := triggers.(type) {
	case map[string]any:
		for _, k := range sortedKeys(on) {
			if k != "workflow_dispatch" {
				t.Errorf("%s fires on %q. The ledger must never run: workflow_dispatch is the only trigger that "+
					"costs nothing and adds no check to anyone's pull request.", ledgerPath, k)
			}
		}
	default:
		t.Errorf("%s: `on:` is %T, want a mapping containing only workflow_dispatch", ledgerPath, triggers)
	}

	jobs, _ := doc["jobs"].(map[string]any)
	for _, name := range sortedKeys(jobs) {
		job, ok := jobs[name].(map[string]any)
		if !ok {
			t.Errorf("%s: job %q is %T, want a mapping", ledgerPath, name, jobs[name])
			continue
		}
		if cond, ok := job["if"]; !ok || cond != false {
			t.Errorf("%s: job %q has `if: %v`, want `if: false`. A manual dispatch would otherwise run every step, "+
				"and the steps exist to be read, not executed.", ledgerPath, name, cond)
		}
	}
}

// One action, one ref, across everything the lacquer ships.
//
// Mixed precision is what turns a routine bump into churn against a managed
// file. darndest-api-proxy #45 is the measured case: two project-owned workflows
// stale at actions/checkout@v6.0.2 and one lacquer-managed workflow at v7, and
// Dependabot normalised ALL THREE to v7.0.1 — the managed file was rewritten
// because its neighbours disagreed with it, not because it was out of date.
//
// The ledger makes this the lacquer's own problem too: two refs for one action
// here is an invitation for Dependabot to collapse them into one, which would
// leave the ledger unable to mirror the profiles at all.
//
// The shipped content carried exactly one such pair when this test was written:
// the optional testflight-feedback workflow pinned actions/checkout@v7.0.1 while
// every other shipped workflow floated at v7. That workflow has since been
// removed outright, so the pair went with it — but the test stays, because the
// next disagreeing pair will be written by hand and nothing else would catch it.
func TestShippedWorkflowsUseOneRefPerAction(t *testing.T) {
	refs := shippedActionRefs(t)
	byAction := map[string][]string{}
	for _, ref := range sortedKeys(refs) {
		action, _, _ := strings.Cut(ref, "@")
		byAction[action] = append(byAction[action], ref)
	}
	for _, action := range sortedKeys(byAction) {
		if len(byAction[action]) > 1 {
			t.Errorf("the profiles ship %s at %d different refs: %s.\n"+
				"A project receiving both gets the mixed-precision state that made Dependabot rewrite a managed "+
				"workflow in darndest-api-proxy #45. Pick one ref and use it everywhere.",
				action, len(byAction[action]), strings.Join(byAction[action], ", "))
		}
	}
}

// The render matrix must cover every workflow the repo ships.
//
// Without this, a new profile — or a new workflow under an existing one that no
// config in ledgerConfigs enables — contributes no refs, the mirror test still
// passes, and the actions in it are unwatched exactly as before. The failure
// that matters for a ledger is the one where nothing is found, so "found
// nothing" has to be what fails.
func TestLedgerConfigsRenderEveryShippedWorkflow(t *testing.T) {
	_, rendered := renderedWorkflows(t)
	r := root(t)
	var found int
	for _, pat := range []string{
		filepath.Join(r, "profiles", "*", "workflows", "*.yml"),
		filepath.Join(r, "profiles", "*", "workflows-optional", "*.yml"),
		filepath.Join(r, "core", "root", ".github", "workflows", "*.yml"),
	} {
		files, err := filepath.Glob(pat)
		if err != nil {
			t.Fatalf("glob %s: %v", pat, err)
		}
		for _, f := range files {
			found++
			rel := filepath.ToSlash(strings.TrimPrefix(f, r+string(filepath.Separator)))
			if !rendered[rel] {
				t.Errorf("%s ships, but no config in ledgerConfigs renders it, so none of its action refs reach "+
					"the ledger and nothing upstream watches them. Add a project shape that enables it.", rel)
			}
		}
	}
	if found == 0 {
		t.Fatal("found no shipped workflows to check; the globs are wrong and every ledger test above is vacuous")
	}
}
