package assets

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"gopkg.in/yaml.v3"
)

// lefthookDoc is the parsed shape of a merged file. Parsed rather than grepped
// throughout this file: a command that appears as text but not as YAML is a
// command lefthook never runs, and a `root:` that landed one level too deep is
// invisible to a substring match.
type lefthookDoc map[string]struct {
	Parallel bool `yaml:"parallel"`
	Commands map[string]struct {
		Root string `yaml:"root"`
		Run  string `yaml:"run"`
		Glob string `yaml:"glob"`
	} `yaml:"commands"`
}

func parseLefthook(t *testing.T, body []byte) lefthookDoc {
	t.Helper()
	var doc lefthookDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("merged lefthook.yml is not valid YAML: %v\n%s", err, body)
	}
	return doc
}

// The two shapes the real profiles have, reduced to what the merge has to
// decide: a command only one profile ships, a repo-root command both ship
// identically, and a component-scoped command both ship under the same name.
const (
	fragAlpha = `# alpha's own header.
pre-commit:
  parallel: false
  commands:
    # alpha only.
    lint:
      root: "front/"
      run: alpha lint
    secrets:
      run: scripts/check-secrets.sh
pre-push:
  parallel: true
  commands:
    test:
      root: "front/"
      run: alpha test --coverage
`
	fragBeta = `# beta's own header.
pre-commit:
  parallel: false
  commands:
    fmt:
      root: "back/"
      run: beta fmt --check
    # The same repo-root scan, described in different words.
    secrets:
      run: scripts/check-secrets.sh
pre-push:
  parallel: true
  commands:
    test:
      root: "back/"
      run: beta test
`
)

func fragments(bodies ...string) []Fragment {
	names := []string{"alpha", "beta", "gamma"}
	out := make([]Fragment, 0, len(bodies))
	for i, b := range bodies {
		out = append(out, Fragment{Profile: names[i], Src: "/lacquer/profiles/" + names[i] + "/root/lefthook.yml", Content: b})
	}
	return out
}

// TestMergeLefthookKeepsEveryCommandScopedToItsOwnComponent is the assertion the
// whole change exists for. A merged file where the front-end hooks run against
// the back-end directory is worse than the collision it replaces: the collision
// merely skipped a check, a mis-scoped command runs the wrong toolchain in the
// wrong place and fails every commit.
func TestMergeLefthookKeepsEveryCommandScopedToItsOwnComponent(t *testing.T) {
	body, err := mergeLefthook("lefthook.yml", fragments(fragAlpha, fragBeta))
	if err != nil {
		t.Fatalf("mergeLefthook: %v", err)
	}
	doc := parseLefthook(t, body)

	for _, tc := range []struct{ hook, cmd, root, run string }{
		{"pre-commit", "lint", "front/", "alpha lint"},
		{"pre-commit", "fmt", "back/", "beta fmt --check"},
		{"pre-push", "test-alpha", "front/", "alpha test --coverage"},
		{"pre-push", "test-beta", "back/", "beta test"},
	} {
		c, ok := doc[tc.hook].Commands[tc.cmd]
		if !ok {
			t.Errorf("%s.%s is missing from the merged file; a profile's hook was dropped", tc.hook, tc.cmd)
			continue
		}
		if c.Root != tc.root {
			t.Errorf("%s.%s has root %q, want %q — it would run the wrong toolchain in the wrong directory",
				tc.hook, tc.cmd, c.Root, tc.root)
		}
		if strings.TrimSpace(c.Run) != tc.run {
			t.Errorf("%s.%s runs %q, want %q", tc.hook, tc.cmd, c.Run, tc.run)
		}
	}
}

// TestMergeLefthookKeepsBothSidesOfANameClash: `test` means something different
// in each profile and neither may win.
func TestMergeLefthookKeepsBothSidesOfANameClash(t *testing.T) {
	body, err := mergeLefthook("lefthook.yml", fragments(fragAlpha, fragBeta))
	if err != nil {
		t.Fatalf("mergeLefthook: %v", err)
	}
	doc := parseLefthook(t, body)
	if _, ok := doc["pre-push"].Commands["test"]; ok {
		t.Error("the merged file still has a bare `pre-push.test`; with two different commands by that " +
			"name, one of them must have been discarded")
	}
	var got []string
	for name := range doc["pre-push"].Commands {
		got = append(got, name)
	}
	for _, want := range []string{"test-alpha", "test-beta"} {
		if _, ok := doc["pre-push"].Commands[want]; !ok {
			t.Errorf("no %q in the merged pre-push commands %v", want, got)
		}
	}
}

// TestMergeLefthookRunsARepoRootCommandOnce: `secrets` scans the whole staged
// set itself, so one per profile means the same scan N times per commit and
// every hit reported N times.
func TestMergeLefthookRunsARepoRootCommandOnce(t *testing.T) {
	body, err := mergeLefthook("lefthook.yml", fragments(fragAlpha, fragBeta))
	if err != nil {
		t.Fatalf("mergeLefthook: %v", err)
	}
	doc := parseLefthook(t, body)
	var names []string
	for name := range doc["pre-commit"].Commands {
		if strings.HasPrefix(name, "secrets") {
			names = append(names, name)
		}
	}
	if len(names) != 1 || names[0] != "secrets" {
		t.Errorf("the merged pre-commit has %v; the repo-root secret scan must appear exactly once, "+
			"under its own name", names)
	}
	// And it is genuinely unscoped — a `root:` here would confine a scan that
	// reads `git diff --cached` for itself to one component.
	if root := doc["pre-commit"].Commands["secrets"].Root; root != "" {
		t.Errorf("the merged `secrets` command gained root %q; it is repo-wide on purpose", root)
	}
}

// TestMergeLefthookRefusesToChooseBetweenHookSettings. `parallel` is one value
// for the whole hook, so two profiles disagreeing about it is not something a
// merge can resolve — and quietly taking the first is the class of bug being
// fixed, not a workaround for it.
func TestMergeLefthookRefusesToChooseBetweenHookSettings(t *testing.T) {
	beta := strings.Replace(fragBeta, "pre-push:\n  parallel: true", "pre-push:\n  parallel: false", 1)
	if beta == fragBeta {
		t.Fatal("the fixture edit did not apply; this test would prove nothing")
	}
	_, err := mergeLefthook("lefthook.yml", fragments(fragAlpha, beta))
	if err == nil {
		t.Fatal("two profiles set pre-push.parallel to different values and the merge silently picked one")
	}
	for _, want := range []string{"alpha", "beta", "pre-push.parallel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q, so it does not say what to change: %v", want, err)
		}
	}
}

// TestMergeLefthookRefusesToChooseBetweenTopLevelSettings — the same rule one
// level up, for keys that are not hooks at all.
func TestMergeLefthookRefusesToChooseBetweenTopLevelSettings(t *testing.T) {
	_, err := mergeLefthook("lefthook.yml", fragments(
		"min_version: 1.5.0\n"+fragAlpha,
		"min_version: 1.9.0\n"+fragBeta,
	))
	if err == nil {
		t.Fatal("two profiles declared different min_version values and the merge silently picked one")
	}
	if !strings.Contains(err.Error(), "min_version") {
		t.Errorf("the error does not name the setting: %v", err)
	}
}

// A top-level setting the profiles agree on is not a conflict, and must survive.
func TestMergeLefthookKeepsAnAgreedTopLevelSetting(t *testing.T) {
	body, err := mergeLefthook("lefthook.yml", fragments(
		"min_version: 1.9.0\n"+fragAlpha,
		"min_version: 1.9.0\n"+fragBeta,
	))
	if err != nil {
		t.Fatalf("mergeLefthook: %v", err)
	}
	var doc struct {
		MinVersion string `yaml:"min_version"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("merged file is not valid YAML: %v", err)
	}
	if doc.MinVersion != "1.9.0" {
		t.Errorf("min_version = %q after the merge, want %q — a setting both profiles agree on was dropped",
			doc.MinVersion, "1.9.0")
	}
}

// TestMergeLefthookIsOrderIndependentForTheCommandSet. The fragment order fixes
// which profile's prose leads the file, and it must not change which commands
// end up in it.
func TestMergeLefthookIsOrderIndependentForTheCommandSet(t *testing.T) {
	forward, err := mergeLefthook("lefthook.yml", fragments(fragAlpha, fragBeta))
	if err != nil {
		t.Fatalf("mergeLefthook: %v", err)
	}
	reverse, err := mergeLefthook("lefthook.yml", []Fragment{
		{Profile: "beta", Src: "b", Content: fragBeta},
		{Profile: "alpha", Src: "a", Content: fragAlpha},
	})
	if err != nil {
		t.Fatalf("mergeLefthook reversed: %v", err)
	}
	for _, hook := range []string{"pre-commit", "pre-push"} {
		a := commandNames(parseLefthook(t, forward), hook)
		b := commandNames(parseLefthook(t, reverse), hook)
		if strings.Join(a, ",") != strings.Join(b, ",") {
			t.Errorf("%s holds %v one way round and %v the other; the merge loses a command depending "+
				"on which profile sorts first", hook, a, b)
		}
	}
}

func commandNames(doc lefthookDoc, hook string) []string {
	var out []string
	for name := range doc[hook].Commands {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestMergeLefthookKeepsTheFragmentsRationale. The profile files are mostly
// comment, and the comments are the reason a reader does not "simplify" a hook
// back into the defect it was written to prevent.
func TestMergeLefthookKeepsTheFragmentsRationale(t *testing.T) {
	body, err := mergeLefthook("lefthook.yml", fragments(fragAlpha, fragBeta))
	if err != nil {
		t.Fatalf("mergeLefthook: %v", err)
	}
	for _, want := range []string{
		"# alpha's own header.",
		"# beta's own header.",
		"# alpha only.",
		"Composed by `lacquer sync`",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the merged file lost %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// The guard: a destination two profiles claim with nothing to merge them
// ---------------------------------------------------------------------------

// stubProfile writes a minimal profile tree shipping one root-tree file.
func stubProfile(t *testing.T, lacquerRoot, profile, rel, body string) {
	t.Helper()
	write(t, filepath.Join(lacquerRoot, "profiles", profile, "root", filepath.FromSlash(rel)), body)
}

func twoProfileConfig() *config.Config {
	return &config.Config{
		Components: []config.Component{
			{Path: "front", Profiles: []string{"alpha"}},
			{Path: "back", Profiles: []string{"beta"}},
		},
	}
}

// TestPlanRefusesADestinationTwoProfilesClaim is the guard that makes the
// original bug impossible to reintroduce quietly. Before it, adding
// profiles/x/root/foo.yml beside an existing profiles/y/root/foo.yml produced a
// project that synced, audited OK, and silently ran only one of them.
func TestPlanRefusesADestinationTwoProfilesClaim(t *testing.T) {
	h := t.TempDir()
	stubProfile(t, h, "alpha", "shared-config.yml", "alpha: 1\n")
	stubProfile(t, h, "beta", "shared-config.yml", "beta: 1\n")

	_, err := Plan(h, twoProfileConfig())
	if err == nil {
		t.Fatal("Plan accepted two profiles shipping the same destination. One of them silently wins " +
			"and the other's contents never reach the project, with audit reporting OK throughout")
	}
	for _, want := range []string{"alpha", "beta", "shared-config.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q, so it does not say what collided: %v", want, err)
		}
	}
}

// A registered destination is merged rather than refused, and the plan carries
// one asset holding both claimants.
func TestPlanMergesARegisteredDestination(t *testing.T) {
	h := t.TempDir()
	stubProfile(t, h, "alpha", "lefthook.yml", fragAlpha)
	stubProfile(t, h, "beta", "lefthook.yml", fragBeta)

	plan, err := Plan(h, twoProfileConfig())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var found int
	for _, a := range plan {
		if a.Dest != "lefthook.yml" {
			continue
		}
		found++
		if len(a.Merged) != 2 {
			t.Fatalf("lefthook.yml carries %d fragment(s), want 2 — a profile was dropped", len(a.Merged))
		}
		if a.Merged[0].profile != "alpha" || a.Merged[1].profile != "beta" {
			t.Errorf("fragments are %q then %q; sorted profile order is what makes the output "+
				"deterministic", a.Merged[0].profile, a.Merged[1].profile)
		}
		body, missing, err := Render(a, twoProfileConfig())
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if len(missing) > 0 {
			t.Errorf("unresolved tokens in the merged file: %v", missing)
		}
		doc := parseLefthook(t, body)
		for _, want := range []string{"test-alpha", "test-beta"} {
			if _, ok := doc["pre-push"].Commands[want]; !ok {
				t.Errorf("%s missing from the rendered merge", want)
			}
		}
	}
	if found != 1 {
		t.Fatalf("the plan holds %d lefthook.yml entries, want exactly 1", found)
	}
}

// Core still beats a same-named profile asset, silently and on purpose. The new
// guard is about profile-vs-profile only, and turning the core precedence rule
// into an error would break every project.
func TestPlanKeepsCorePrecedenceOverAProfile(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "root", "shared-config.yml"), "core: 1\n")
	stubProfile(t, h, "alpha", "shared-config.yml", "alpha: 1\n")

	plan, err := Plan(h, twoProfileConfig())
	if err != nil {
		t.Fatalf("Plan refused a core/profile collision, which is a deliberate precedence rule: %v", err)
	}
	for _, a := range plan {
		if a.Dest != "shared-config.yml" {
			continue
		}
		if len(a.Merged) != 0 {
			t.Error("a core asset was merged with a profile one; core takes precedence, it does not compose")
		}
		if !strings.Contains(a.Src, filepath.Join("core", "root")) {
			t.Errorf("shared-config.yml resolved to %q, want the core copy", a.Src)
		}
	}
}

// An excluded destination stays excluded no matter how many profiles claim it —
// a second claimant must not resurrect it, and must not trip the guard either.
func TestPlanExclusionSurvivesASecondClaimant(t *testing.T) {
	h := t.TempDir()
	stubProfile(t, h, "alpha", "lefthook.yml", fragAlpha)
	stubProfile(t, h, "beta", "lefthook.yml", fragBeta)

	cfg := twoProfileConfig()
	cfg.Project.Exclude = []config.Exclusion{{Path: "lefthook.yml", Reason: "hand-tuned", Until: "2999-01-01"}}

	plan, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, a := range plan {
		if a.Dest == "lefthook.yml" {
			t.Fatal("an excluded destination came back because a second profile claimed it")
		}
	}
	sup, err := Suppressed(h, cfg)
	if err != nil {
		t.Fatalf("Suppressed: %v", err)
	}
	var seen bool
	for _, s := range sup {
		if s == "lefthook.yml" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("lefthook.yml is not reported as suppressed (%v); an exclusion nothing records reads as "+
			"dead text the next time somebody reviews it", sup)
	}
}
