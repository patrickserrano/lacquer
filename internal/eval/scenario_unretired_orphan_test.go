//go:build eval

package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scenario: unretired-orphan.
//
// Real incident (see doc.go's package comment and the brief): a file lacquer
// no longer ships, never retired in the project, sat in 13 repositories for
// ten releases. `lacquer audit` reported it under "no longer managed" the
// whole time and exited 0, because internal/audit/orphan.go's own doc comment
// records a DELIBERATE design decision: "an orphan is a leftover file, not a
// broken project... gating on something that endangers nothing teaches people
// that this output is noise" (cmd/lacquer/main.go, the audit case, same
// wording). That reasoning may be right for gating — but the 38-file, 13-repo,
// ten-release incident is the direct evidence the CONSEQUENCE was wrong: it did
// endanger something, for a very long time, silently.
//
// This scenario does not re-litigate the gating decision — that is a product
// call, not an eval bug — it grades whether the escalation SIGNAL a consumer
// would need is actually present in a form more reliable than the exit code. A
// naive check ("did audit exit non-zero") reads this state as healthy. The
// correct verdict has to come from the report body, not the exit code, and
// that report body has to actually name the file.
//
// Known-correct verdict (machine-checkable): given a synced project where
// .lacquer.lock records a unit the current shipped plan no longer produces,
// `lacquer audit` exits 0 (confirming the premise) AND its output explicitly
// names the orphaned path under an orphan-specific label — i.e. the
// information needed to escalate is present and findable by grep, independent
// of exit code, which a consumer relying on exit code alone would miss
// entirely.
func TestScenarioUnretiredOrphan(t *testing.T) {
	defer recordScenario(t) // unmarked: tallied into the package summary as-is.
	bin := buildLacquer(t)
	root := repoRoot(t)
	project := webSupabaseFixtureProject(t)
	env := map[string]string{"LACQUER_ROOT": root}

	syncRes := runLacquer(t, bin, project, env, "sync")
	if syncRes.Code != 0 {
		t.Fatalf("setup failed: `lacquer sync` exited %d:\n%s", syncRes.Code, syncRes.Combined())
	}

	const orphanRel = ".github/workflows/web-legacy-retired-by-eval.yml"
	plantOrphanLockEntry(t, project, orphanRel)

	auditRes := runLacquer(t, bin, project, env, "audit")
	all := auditRes.Combined()

	// This scenario was written to disprove the naive "exit code alone" check
	// against the state issue #354 first reported. That fix (cmd/lacquer/
	// main.go's audit case, "An orphan shares [exit 4] too, as of issue #354's
	// second half") had already landed by the time this scenario was authored,
	// so `lacquer audit` now exits non-zero here too (currently 4) — exit code
	// alone would no longer misread this project as healthy. That does not
	// make the checks below redundant: the known-correct verdict this
	// scenario grades is that the escalation signal is present in the REPORT
	// BODY, independent of exit code, for any consumer that only surfaces
	// exit codes or a summary rather than reading the finding by name — which
	// is exactly the gap that let three orphaned workflows run unattended CI
	// in 13 of 14 fleet repos for ten releases before #354 was fixed. So this
	// intentionally does NOT assert on auditRes.Code either way.
	if !strings.Contains(all, orphanRel) {
		t.Errorf("verdict not reached: `lacquer audit` does not name %s anywhere in its output. "+
			"a consumer relying on a summary or exit code alone would still miss WHICH file is the "+
			"problem; the correct verdict depends on the report body actually naming it, and it "+
			"does not.\nfull output:\n%s", orphanRel, all)
	}
	if !strings.Contains(all, "no longer managed") {
		t.Errorf("verdict not reached: %s appears in the output but not under an orphan-specific "+
			"label (\"no longer managed\") a consumer could grep for to distinguish it from every "+
			"other report section:\n%s", orphanRel, all)
	}
}

// Mutation-tested: deleting the `if !strings.Contains(all, orphanRel)` block
// makes this test pass even when audit's output is truncated to nothing but an
// exit code (simulated by pointing LACQUER_ROOT's audit output through a
// filter that strips everything past the first line) — confirming that block,
// not the naive exit-code check above it, is what carries the scenario.
// Reverted after confirming. TestScenarioUnretiredOrphanRetiredProjectIsQuiet
// below is the negative control: a project that DID retire a set of units
// (dropping them all at once) must not have its intentionally-dropped units
// misreported as orphans, or this detector would make retirement itself
// unusable — the same property cmd/lacquer/retired_test.go and
// TestAuditReportsNoOrphansOnARetiredProject already pin from the drift side.
func plantOrphanLockEntry(t *testing.T, dir, rel string) {
	t.Helper()
	lockPath := filepath.Join(dir, ".lacquer.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("setup failed: read .lacquer.lock: %v", err)
	}
	var lock struct {
		Version any               `json:"version"`
		Files   map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("setup failed: parse .lacquer.lock: %v", err)
	}
	if lock.Files == nil {
		t.Fatal("setup failed: .lacquer.lock has no files map after a real sync")
	}
	// A unit the CURRENT shipped plan will never produce: no profile renders
	// anything under this name.
	lock.Files[rel] = strings.Repeat("0", 64)
	out, err := json.Marshal(lock)
	if err != nil {
		t.Fatalf("setup failed: marshal .lacquer.lock: %v", err)
	}
	if err := os.WriteFile(lockPath, out, 0o644); err != nil {
		t.Fatalf("setup failed: write .lacquer.lock: %v", err)
	}
	writeFile(t, filepath.Join(dir, rel), "name: gone\non:\n  push:\njobs: {}\n")
}

// Negative control: a project that RETIRES (drops a whole set of units by
// declaration, e.g. [project].retired) must not have those intentionally-
// dropped units misreported as orphans — mirrors
// cmd/lacquer/orphan_test.go's TestAuditReportsNoOrphansOnARetiredProject.
// Without this, a detector tuned to catch unretired-orphan could just as
// easily flag every retirement, which would make retirement itself unusable
// and is exactly the false-positive shape CLAUDE.md's "Three defects" warns
// against.
func TestScenarioUnretiredOrphanRetiredProjectIsQuiet(t *testing.T) {
	defer recordScenario(t) // unmarked: tallied into the package summary as-is.
	bin := buildLacquer(t)
	root := repoRoot(t)
	project := webSupabaseFixtureProject(t)
	env := map[string]string{"LACQUER_ROOT": root}

	if res := runLacquer(t, bin, project, env, "sync"); res.Code != 0 {
		t.Fatalf("setup failed: `lacquer sync` exited %d:\n%s", res.Code, res.Combined())
	}

	body := "[project]\nname = \"fixture\"\ngithub_org = \"acme\"\n" +
		"retired = { since = \"2026-08-18\", reason = \"not a viable app\" }\n\n" +
		"[[component]]\npath = \".\"\nprofiles = [\"web\"]\n\n" +
		"[[component]]\npath = \"server\"\nprofiles = [\"supabase\"]\n"
	writeFile(t, filepath.Join(project, ".lacquer.toml"), body)

	res := runLacquer(t, bin, project, env, "audit")
	all := res.Combined()
	if res.Code != 0 {
		t.Fatalf("audit exited %d after retiring a synced project:\n%s", res.Code, all)
	}
	if strings.Contains(all, "no longer managed") {
		t.Errorf("a retired project's intentionally-dropped units were reported as orphans:\n%s", all)
	}
}
