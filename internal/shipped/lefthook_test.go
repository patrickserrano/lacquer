package shipped

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/config"
)

// Two profiles quietly claiming one destination is the root cause of the
// lefthook defect, and it is a defect no project-level check could see:
// `lacquer audit` compares the file on disk against what the lacquer would
// write, and the lacquer really would write the winner's copy. The bug lives in
// the SHIPPED CONTENT, one layer above any project — so this is where it is
// caught, against the real profiles/ tree, whether or not any project happens
// to declare the colliding pair.

// shippedProfiles lists every profile the lacquer ships.
func shippedProfiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root(t), "profiles"))
	if err != nil {
		t.Fatalf("read profiles/: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	if len(out) < 2 {
		t.Fatalf("found %d profile(s) in profiles/; a collision check needs at least two to compare", len(out))
	}
	return out
}

// configFor builds a manifest declaring each named profile on its own component.
// One component per profile is what config.Load enforces, so this is the widest
// legal shape.
func configFor(profiles []string) *config.Config {
	cfg := &config.Config{}
	for _, p := range profiles {
		cfg.Components = append(cfg.Components, config.Component{Path: "c-" + p, Profiles: []string{p}})
	}
	return cfg
}

// TestNoTwoProfilesSilentlyShipTheSameDestination is the guard against the whole
// class of bug, not just its one instance.
//
// It plans every profile at once and every pair on its own. assets.Plan returns
// an error for a destination two profiles claim with no merge strategy
// registered, so a future profiles/x/root/foo.yml added beside an existing
// profiles/y/root/foo.yml fails HERE — in the lacquer's own test suite, on the
// branch that adds it — rather than silently dropping one of them in whichever
// repository declares both.
//
// The pairwise pass is not redundant with the all-profiles one: plan() reports
// the first collision it finds and stops, so one bad pair could otherwise mask
// another.
func TestNoTwoProfilesSilentlyShipTheSameDestination(t *testing.T) {
	t.Parallel()
	lacquerRoot := root(t)
	all := shippedProfiles(t)

	if _, err := assets.Plan(lacquerRoot, configFor(all)); err != nil {
		t.Errorf("planning every profile together failed: %v", err)
	}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			pair := []string{all[i], all[j]}
			if _, err := assets.Plan(lacquerRoot, configFor(pair)); err != nil {
				t.Errorf("profiles %v cannot be declared by one repository: %v", pair, err)
			}
		}
	}
	t.Logf("checked %d profile(s) and %d pair(s) for destination collisions", len(all), len(all)*(len(all)-1)/2)
}

// TestEveryMergeableDestinationIsGenuinelyContested keeps the merge registry
// from becoming dead text.
//
// An entry naming a path only one profile ships reads as an active decision
// while doing nothing, and it would quietly disarm the guard above for that
// path: a second profile could then claim it and be merged by a strategy nobody
// has looked at in years. Same failure mode as a stale [project].exclude, which
// internal/exclusion exists to report.
func TestEveryMergeableDestinationIsGenuinelyContested(t *testing.T) {
	t.Parallel()
	lacquerRoot := root(t)
	profiles := shippedProfiles(t)

	// dest -> the profiles whose root/ tree ships it.
	shippers := map[string][]string{}
	for _, p := range profiles {
		base := filepath.Join(lacquerRoot, "profiles", p, "root")
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil // a profile need not ship a root tree
				}
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			dest := filepath.ToSlash(rel)
			shippers[dest] = append(shippers[dest], p)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walk profiles/%s/root: %v", p, err)
		}
	}

	for _, dest := range assets.MergeableDests() {
		got := shippers[dest]
		if len(got) < 2 {
			t.Errorf("internal/assets/merge.go registers a merge strategy for %q, but %d profile(s) ship it (%v). "+
				"A strategy for a path nobody contests is dead text that also exempts that path from the "+
				"collision guard — drop the entry, or the profile that stopped shipping it took the merge with it",
				dest, len(got), got)
		}
	}
	t.Logf("merge registry: %v", assets.MergeableDests())
}

// TestLefthookProfilesStillDisagreeAboutACommandName pins the case the merge is
// hardest to get right, at the source rather than in a rendered project.
//
// If profiles/web and profiles/supabase ever stop sharing a command name, the
// e2e assertion about `test-web` / `test-supabase` starts passing vacuously —
// it would be asserting the absence of a clash rather than the survival of both
// sides of one. This says so out loud instead.
func TestLefthookProfilesStillDisagreeAboutACommandName(t *testing.T) {
	t.Parallel()
	lacquerRoot := root(t)
	web := readFile(t, filepath.Join(lacquerRoot, "profiles", "web", "root", "lefthook.yml"))
	supa := readFile(t, filepath.Join(lacquerRoot, "profiles", "supabase", "root", "lefthook.yml"))

	for _, want := range []struct{ body, run, why string }{
		// {{WEB_RUN}} test:coverage, not a literal "pnpm run" — the package
		// manager is resolved per project (see detect.WebPackageManager), but
		// the SCRIPT name this pins on is fixed regardless of which manager
		// runs it.
		{web, "{{WEB_RUN}} test:coverage", "the web profile's pre-push test"},
		{supa, "deno test --allow-all", "the supabase profile's pre-push test"},
	} {
		if !strings.Contains(want.body, want.run) {
			t.Errorf("%s no longer runs %q; the e2e assertion about test-web/test-supabase is pinned to "+
				"these two being different commands under one name", want.why, want.run)
		}
	}
	// And both really are named `test`, or there is no clash to resolve.
	for _, body := range []string{web, supa} {
		if !strings.Contains(body, "    test:\n") {
			t.Error("a lefthook profile no longer declares a command literally named `test`. That may be " +
				"fine, but the merge's name-clash path is then untested by the fleet's real content — " +
				"point the e2e assertion at whatever clashes now, or state that nothing does")
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
