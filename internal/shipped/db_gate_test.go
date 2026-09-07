package shipped

import (
	"regexp"
	"strings"
	"testing"
)

// `DB Lint (Splinter)` and `DB Tests (pgTAP)` each boot a full local Supabase
// stack — Postgres plus ~8 auxiliary services — which measured ~176 seconds of
// billed runner per job, twice per PR. They were gated on the `code` output,
// which is the INVERTED deny-list: true for any change that is not a `.md`,
// `docs/`, LICENSE or `.gitignore` edit. In a repo that carries a Supabase
// component alongside an iOS or web one, that meant every pure-SwiftUI PR booted
// the whole stack twice to lint a schema nobody had touched.
//
// They are now gated on a `db` ALLOWLIST. Its two failure modes are not
// symmetric, and both are covered here:
//
//   - Too NARROW: a PR really does change the schema, the relaxation or the
//     workflow, the jobs skip, and the database silently stops being tested.
//     TestDBGateCoversEveryDatabaseInput lists what those two jobs actually
//     consume and asserts the filter fires for each.
//   - Too WIDE: the filter matches ordinary application source and saves
//     nothing, which is the whole defect being fixed.
//     TestDBGateStillSkipsOrdinarySource asserts it does not.
//
// TestChangesFilterEmitsDBOutput then runs the real shell rather than the regex,
// because the output name, the three-dot diff, the non-PR branch and the grep
// all have to line up for the gate to work.

// dbPathsRe pulls the shell variable holding the DB paths filter out of the
// changes job's script, so the assertions below test the exact regex that ships
// rather than a copy of it.
var dbPathsRe = regexp.MustCompile(`(?m)^\s*db_paths='([^']*)'\s*$`)

// dbFilter returns the compiled DB paths filter from the supabase ci.yml.
func dbFilter(t *testing.T) (*regexp.Regexp, string) {
	t.Helper()
	script := changesScript(t, "supabase")
	m := dbPathsRe.FindStringSubmatch(script)
	if m == nil {
		t.Fatal("no `db_paths=` assignment in the supabase changes job")
	}
	// The filter must actually be what the gate consults; an assignment nothing
	// reads would make every assertion here vacuous.
	if !strings.Contains(script, `grep -qE "$db_paths"`) {
		t.Error("the supabase changes job does not gate on $db_paths")
	}
	re, err := regexp.Compile(m[1])
	if err != nil {
		t.Fatalf("the DB paths filter does not compile: %v", err)
	}
	return re, m[1]
}

// TestDBJobsAreGatedOnTheDBOutput asserts the wiring. An `if` naming an output
// no job declares evaluates to the empty string, which is not 'true' — so both
// jobs would skip on every PR, forever, and the database would stop being tested
// with nothing reporting it.
func TestDBJobsAreGatedOnTheDBOutput(t *testing.T) {
	doc := parseCI(t, "supabase")

	changes, ok := doc.Jobs["changes"]
	if !ok {
		t.Fatal("no `changes` job")
	}
	if _, ok := changes.Outputs["db"]; !ok {
		t.Fatalf("the changes job declares no `db` output (has %v)", keysOf(changes.Outputs))
	}

	for _, name := range []string{"lint-database", "test-database"} {
		job, ok := doc.Jobs[name]
		if !ok {
			t.Fatalf("no `%s` job", name)
		}
		if !strings.Contains(job.If, "needs.changes.outputs.db") {
			t.Errorf("%s `if` is %q — it must consult the changes job's `db` output, not `code` "+
				"(the non-docs deny-list, which is true on every source PR)", name, job.If)
		}
		if strings.Contains(job.If, "needs.changes.outputs.code") {
			t.Errorf("%s `if` still consults `code`: %q", name, job.If)
		}
		if !contains(needsList(t, job.Needs), "changes") {
			t.Errorf("%s needs %v — a job cannot read needs.changes.outputs without depending on changes",
				name, needsList(t, job.Needs))
		}
	}

	// test-database must keep calling cancelled(): a custom `if` that names none
	// of success()/failure()/cancelled()/always() gets success() implicitly
	// ANDed back in, which would skip the pgTAP suite whenever lint-database
	// merely FAILS. lint-database is a `needs` to serialize the two stacks, not
	// a must-pass gate.
	if got := doc.Jobs["test-database"].If; !strings.Contains(got, "cancelled()") {
		t.Errorf("test-database `if` is %q — dropping cancelled() re-introduces the implicit "+
			"success(), so a lint failure would silently skip the pgTAP suite too", got)
	}
}

// TestDBGateCoversEveryDatabaseInput is the guard against the silent failure:
// every path either job reads must wake it.
func TestDBGateCoversEveryDatabaseInput(t *testing.T) {
	re, src := dbFilter(t)
	for _, p := range []string{
		// The stack `supabase start` boots, at both layouts — root and under a
		// component prefix, which is why the filter matches at any depth.
		"supabase/config.toml",
		"server/supabase/config.toml",
		// The schema being linted and deployed.
		"supabase/migrations/20240101_init.sql",
		"server/supabase/migrations/20260902_revoke_dml.sql",
		// The pgTAP suite `supabase test db` runs.
		"supabase/tests/rls.sql",
		"server/supabase/tests/rls.sql",
		// Seed data applied by `supabase start`.
		"supabase/seed.sql",
		// Edge functions are served by the same stack.
		"server/supabase/functions/health/index.ts",
		// Schema kept outside supabase/ entirely.
		"db/migrations/0001_init.sql",
		"database/seed/users.sql",
		// The [baseline.relax] pgtap entry lives here: removing or expiring it
		// flips test-database's verdict with no .sql file touched at all.
		".lacquer.toml",
		// Executed by the "Read the pgTAP relaxation" step.
		"scripts/docs-relaxation.sh",
		// The workflow that DEFINES both jobs. sync writes each profile's
		// workflows to `.github/workflows/<profile>-`.
		".github/workflows/supabase-ci.yml",
	} {
		if !re.MatchString(p) {
			t.Errorf("the DB gate does not fire for %q, so a PR changing it would skip both "+
				"database jobs and the schema would go untested\nfilter: %s", p, src)
		}
	}
}

// TestDBGateStillSkipsOrdinarySource is the other half: a filter of `.` would
// pass the test above and save nothing. These are the paths the change exists to
// skip — every one of them measured on a real PR in the fleet.
func TestDBGateStillSkipsOrdinarySource(t *testing.T) {
	re, src := dbFilter(t)
	for _, p := range []string{
		// The three real PRs that motivated this: pure SwiftUI, in a repo that
		// happens to carry a Supabase component.
		"Rail/Features/Memories/OnThisDayWidget.swift",
		"Rail/DesignSystem/Components/Badge.swift",
		"ios/MomFriend/Core/Routing/Router.swift",
		"RailTests/Memories/MemoryStoreTests.swift",
		// Web and Deno source, which the `check` job covers.
		"admin/src/app/page.tsx",
		"admin/package.json",
		"server/deno.jsonc",
		// Other profiles' workflows cannot define or affect these jobs — this is
		// the narrowing that keeps grouped Dependabot action bumps from booting
		// Postgres twice.
		".github/workflows/ios-release.yml",
		".github/workflows/web-ci.yml",
		// The drift baseline is read by `audit`, not by either DB job.
		".lacquer.lock",
		"docs/schema-notes.md",
	} {
		if re.MatchString(p) {
			t.Errorf("the DB gate fires for %q, which neither database job reads — a filter this "+
				"wide boots a full Postgres stack twice for nothing\nfilter: %s", p, src)
		}
	}
}

// TestChangesFilterEmitsDBOutput executes the real `changes` shell against a
// real repo, because everything above tests the regex rather than the job.
func TestChangesFilterEmitsDBOutput(t *testing.T) {
	script := changesScript(t, "supabase")
	for _, tc := range []struct {
		name    string
		event   string
		changed []string
		want    map[string]string
	}{
		{
			"a docs-only PR",
			"pull_request", []string{"README.md", "docs/guide.md"},
			map[string]string{"code": "false", "db": "false", "lacquer": "false"},
		},
		{
			"a pure-SwiftUI PR in a repo that also has a Supabase component",
			"pull_request", []string{"Rail/Features/Memories/OnThisDayWidget.swift"},
			map[string]string{"code": "true", "db": "false", "lacquer": "false"},
		},
		{
			"a migration",
			"pull_request", []string{"server/supabase/migrations/20260902_revoke_dml.sql"},
			map[string]string{"code": "true", "db": "true", "lacquer": "false"},
		},
		{
			"the manifest, which carries the pgTAP relaxation",
			"pull_request", []string{".lacquer.toml"},
			map[string]string{"code": "true", "db": "true", "lacquer": "true"},
		},
		{
			"a grouped Dependabot bump touching only iOS workflows",
			"pull_request", []string{".github/workflows/ios-release.yml"},
			map[string]string{"code": "true", "db": "false", "lacquer": "true"},
		},
		{
			"this workflow itself",
			"pull_request", []string{".github/workflows/supabase-ci.yml"},
			map[string]string{"code": "true", "db": "true", "lacquer": "true"},
		},
		{
			// `deploy-database` needs [check, lint-database, test-database], and
			// a skipped need skips it — so a push must never gate `db` on paths,
			// or it could skip deploying the very migration it carries.
			"a push to main",
			"push", []string{"server/supabase/migrations/20260902_revoke_dml.sql"},
			map[string]string{"code": "true", "db": "true", "lacquer": "true"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runChangesFilter(t, script, tc.event, tc.changed)
			for k, want := range tc.want {
				if got[k] != want {
					t.Errorf("%s=%q, want %q (changed: %v)\nall outputs: %v",
						k, got[k], want, tc.changed, got)
				}
			}
		})
	}
}
