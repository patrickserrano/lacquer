//go:build eval

package eval

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/baseline"
	"github.com/patrickserrano/lacquer/internal/config"
)

// PART 2 — CONTENT CORRECTNESS. EXTENSION POINT, NOT BUILT OUT HERE.
//
// Part 1 (the scenario_*_test.go files in this package) grades BEHAVIOUR: given
// a repo state, does the right verdict get reached. That is a separate axis
// from CONTENT correctness: given a profile lacquer actually ships, does a
// project that syncs it end up passing ITS OWN gates. The brief explicitly
// scopes Part 2 to a stub plus one worked example, so this file is that: one
// concrete case, not a framework.
//
// What a follow-up task building this out for real would add, per profile
// per gate class — none of it implemented here:
//
//   - No orphans on disk: sync every archetype initcmd.Run can produce (see
//     internal/archetype and `lacquer init --list-stacks`), then assert
//     audit.Orphans returns empty immediately after a fresh sync, for every
//     one of them — this file only proves it for one hand-picked fixture.
//   - No stale references: cross-check every path a rendered workflow
//     references (testtargets.Parse-style) against what the sync actually
//     wrote, for every archetype, not just the one component shape rootapp
//     happens to cover.
//   - Every required check resolvable: for a project with GitHub branch
//     protection requiring named status checks, assert every required check
//     NAME actually corresponds to a job the rendered workflow would run —
//     this is the class of defect CLAUDE.md's rule 3 describes
//     (blacksmith-2vcpu-ubuntu-2404-arm shipping fleet-wide, proven on zero
//     repositories, where the job does not go red, it queues forever). No
//     runner-availability check exists anywhere in this repo today; this is
//     new work, not a reuse of an existing detector the way Part 1's
//     scenarios are.
//   - Warnings-as-errors set on ALL targets, not just the primary one this
//     worked example checks — test targets, extensions, widget targets, watch
//     targets — enumerated from the .pbxproj rather than assumed.
//
// The worked example below establishes the PATTERN the above would follow:
// sync a real profile into a fresh fixture with the real, built binary, then
// assert an existing lacquer-owned check (internal/baseline, which already
// asserts warnings-as-errors — see profiles/ios/baseline.toml) reports the
// project clean. It deliberately reuses baseline.Run exactly as
// internal/shipped/e2e_test.go's validateSyncedProject does, rather than
// reimplementing warnings-as-errors detection — this suite grades whether
// lacquer's own instruments agree with themselves end to end, not whether a
// second, parallel implementation happens to concur.
func TestContentWorkedExample_WarningsAsErrors(t *testing.T) {
	defer recordScenario(t) // unmarked: tallied into the package summary as-is.
	bin := buildLacquer(t)
	root := repoRoot(t)

	// rootapp: a single-stack iOS app at the repository root, committed under
	// internal/shipped/testdata/projects — the same fixture population
	// internal/shipped/e2e_test.go syncs repeatedly. Copied rather than synced
	// in place so this test can never write into the real testdata fixture.
	project := t.TempDir()
	copyDir(t, filepath.Join(root, "internal", "shipped", "testdata", "projects", "rootapp"), project)
	initGitRepo(t, project, "rootapp fixture")

	env := map[string]string{"LACQUER_ROOT": root}
	if res := runLacquer(t, bin, project, env, "sync"); res.Code != 0 {
		t.Fatalf("setup failed: `lacquer sync` exited %d:\n%s", res.Code, res.Combined())
	}

	cfg, err := config.Load(filepath.Join(project, ".lacquer.toml"))
	if err != nil {
		t.Fatalf("setup failed: load synced project's manifest: %v", err)
	}

	reports, err := baseline.Run(root, project, cfg.BaselineTargets(), cfg.Baseline.Relax, time.Now())
	if err != nil {
		t.Fatalf("setup failed: baseline.Run: %v", err)
	}
	if len(reports) == 0 {
		t.Fatal("setup failed: baseline.Run reported nothing at all for a synced ios project — " +
			"it is not reaching this fixture's targets")
	}

	if n := baseline.Blocking(reports); n > 0 {
		t.Errorf("verdict not reached: a project freshly synced with the ios profile does not pass "+
			"its own warnings-as-errors gate (%d blocking baseline finding(s)):\n%s",
			n, baseline.FormatReports(reports))
	}

	// The vacuous-pass guard this whole file exists to avoid falling into
	// itself (see scenario_vacuous_filter_test.go): a report that looked at
	// nothing must say so explicitly (Unchecked), not pass silently.
	for _, r := range reports {
		if r.Unchecked != "" {
			continue
		}
		looked := false
		for _, f := range r.Findings {
			if f.Total > 0 {
				looked = true
				break
			}
		}
		if !looked {
			t.Errorf("baseline reported nothing for %s/%s and did not say it was Unchecked — "+
				"it found no Swift-compiling build configuration and verified nothing, which "+
				"would make the pass above vacuous", r.Profile, r.Component)
		}
	}
}
