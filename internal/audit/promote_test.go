package audit_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
	syncpkg "github.com/patrickserrano/lacquer/internal/sync"
)

// The shape Dependabot actually produced against darndest-api-proxy on
// 2026-09-11 (PR #45): one action, several call sites, nothing else touched.
const managedWorkflow = `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: actions/setup-node@v7
      - run: npm ci
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: npm run lint
`

func bumped(old, new string) string {
	return strings.ReplaceAll(managedWorkflow, old, new)
}

func TestActionBumpsReadsADependabotEdit(t *testing.T) {
	got := audit.ActionBumps(managedWorkflow, bumped("actions/checkout@v7", "actions/checkout@v7.0.1"))
	want := []audit.ActionBump{{Action: "actions/checkout", From: "v7", To: "v7.0.1"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("ActionBumps = %+v, want %+v (two call sites, one promotion)", got, want)
	}
}

func TestActionBumpsReportsEveryActionOnce(t *testing.T) {
	after := bumped("actions/checkout@v7", "actions/checkout@v7.0.1")
	after = strings.ReplaceAll(after, "actions/setup-node@v7", "actions/setup-node@v7.0.0")
	got := audit.ActionBumps(managedWorkflow, after)
	if len(got) != 2 {
		t.Fatalf("ActionBumps = %+v, want one entry per action", got)
	}
	if got[0].Action != "actions/checkout" || got[1].Action != "actions/setup-node" {
		t.Errorf("ActionBumps = %+v, want file order (checkout then setup-node)", got)
	}
}

// A SHA pin and its trailing `# v7.0.1` comment move together. Freezing the
// comment would refuse every SHA bump, which is the case this exists for.
func TestActionBumpsFollowsAShaPinAndItsComment(t *testing.T) {
	before := "      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n"
	after := "      - uses: actions/checkout@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa # v7.1.0\n"
	got := audit.ActionBumps(before, after)
	if len(got) != 1 || got[0].Action != "actions/checkout" || !strings.HasPrefix(got[0].To, "aaaa") {
		t.Fatalf("ActionBumps = %+v, want the SHA bump", got)
	}
}

// Everything below is the half that matters more: nil, so a real local change
// can never be waved through as "just a version bump".
func TestActionBumpsRefusesAnythingButAVersion(t *testing.T) {
	cases := []struct {
		name, after string
	}{{
		"an added line",
		strings.Replace(managedWorkflow, "      - run: npm ci\n", "      - run: npm ci\n      - run: npm audit\n", 1),
	}, {
		"a removed line",
		strings.Replace(managedWorkflow, "      - run: npm run lint\n", "", 1),
	}, {
		// A whole job appended after the rendered file's trailing blank, plus a
		// real bump. Every index the two files share now matches or is a clean
		// uses/uses version change, so the appended job sits past the end of the
		// comparison entirely and ONLY the line-count guard sees it. Calling this
		// "only action versions differ" would promote the bump while hiding the
		// project's actual local change, which is the one outcome worse than
		// reporting nothing.
		"a job appended past the end of the rendered file",
		bumped("actions/checkout@v7", "actions/checkout@v7.0.1") +
			"\n  audit:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm audit\n",
	}, {
		// The line count still matches, the line still starts with `uses:`, and
		// the version really did move — so every check EXCEPT the action-name
		// comparison passes. This is the substitution a loose matcher waves
		// through, and it is the one that matters: it is how a supply-chain
		// swap would be reported as a routine version bump worth promoting.
		"a different action at the same call site",
		strings.Replace(managedWorkflow, "- uses: actions/setup-node@v7", "- uses: evil/setup-node@v9", 1),
	}, {
		"an edited step argument",
		strings.Replace(managedWorkflow, "          fetch-depth: 0", "          fetch-depth: 1", 1),
	}, {
		// Bumped AND moved into a different block. Only the indentation
		// comparison can reject this one; the action matches and the version
		// really did advance.
		"a re-indented uses line carrying a real bump",
		strings.Replace(managedWorkflow, "      - uses: actions/setup-node@v7", "        - uses: actions/setup-node@v7.0.0", 1),
	}, {
		"a comment-only edit, which is a hand edit rather than a bump",
		strings.Replace(managedWorkflow, "- uses: actions/setup-node@v7", "- uses: actions/setup-node@v7 # pinned", 1),
	}, {
		"a local composite action, which Dependabot never bumps",
		strings.Replace(managedWorkflow, "- uses: actions/setup-node@v7", "- uses: ./.github/actions/node", 1),
	}, {
		// A digest-pinned container step. It carries an `@` and a ref that moves
		// exactly like an action's, so it is the `uses:` form that will be read
		// as an action by anything that only looks for `something@something`.
		// It is not one: Dependabot handles it under the docker ecosystem, not
		// github-actions, and the lacquer does not render it.
		"a container step pinned by digest",
		strings.Replace(managedWorkflow, "- uses: actions/setup-node@v7",
			"- uses: docker://ghcr.io/org/img@sha256:1111111111111111111111111111111111111111111111111111111111111111", 1),
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.after == managedWorkflow {
				t.Fatal("fixture did not change; the test is asserting nothing")
			}
			if got := audit.ActionBumps(managedWorkflow, c.after); got != nil {
				t.Errorf("ActionBumps = %+v, want nil — this divergence is not a version bump and must stay ordinary drift", got)
			}
		})
	}
}

// A container step pinned by digest is the one `uses:` form that reads as an
// action to anything matching `something@something`: same key, same shape, and a
// ref that moves the same way. It is not an action — Dependabot updates it under
// the docker ecosystem — and a digest change is not something the lacquer's
// profiles/ can be edited to accept, so calling it promotable would send someone
// to change a file that does not contain it.
//
// Both sides carry the same container reference here, deliberately. Anything
// less and the action-name comparison rejects the pair first and this assertion
// proves nothing about the reference shape.
func TestActionBumpsIgnoresAContainerDigest(t *testing.T) {
	const line = "      - uses: docker://ghcr.io/org/img@sha256:%s\n"
	before := fmt.Sprintf(line, strings.Repeat("1", 64))
	after := fmt.Sprintf(line, strings.Repeat("2", 64))
	if got := audit.ActionBumps(before, after); got != nil {
		t.Errorf("ActionBumps = %+v, want nil — a container digest is not a GitHub action", got)
	}
}

func TestActionBumpsIsEmptyForIdenticalContent(t *testing.T) {
	if got := audit.ActionBumps(managedWorkflow, managedWorkflow); got != nil {
		t.Errorf("ActionBumps = %+v, want nil", got)
	}
}

// The end-to-end case: a Dependabot commit lands on a managed workflow, and the
// audit report names the action, both refs, and where the fix belongs.
func TestAuditReportsAnActionBumpAsPromotable(t *testing.T) {
	lacquer, project := setupWithWorkflow(t)

	wf := filepath.Join(project, ".github", "workflows", "ci.yml")
	writeFile(t, wf, bumped("actions/checkout@v7", "actions/checkout@v7.0.1"))
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "chore(deps): bump actions/checkout")

	rows, ver, err := audit.Classify(lacquer, project)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := statusOf(rows, ".github/workflows/ci.yml"); got != audit.Modified {
		t.Fatalf("status = %s, want locally-modified", got)
	}

	promo := audit.Promotable(rows)
	if len(promo) != 1 || promo[0].Dest != ".github/workflows/ci.yml" {
		t.Fatalf("Promotable = %+v, want the managed workflow", promo)
	}
	if b := promo[0].Bumps; len(b) != 1 || b[0].Action != "actions/checkout" || b[0].From != "v7" || b[0].To != "v7.0.1" {
		t.Fatalf("Bumps = %+v, want actions/checkout v7 → v7.0.1", b)
	}

	// Still blocking. The project's edit really will be lost to the next sync,
	// and a promotable row that stopped clobbering would let the churn merge
	// silently and be reverted later — the failure mode this whole issue is about.
	if clob := audit.Clobbered(rows); len(clob) != 1 || clob[0] != ".github/workflows/ci.yml" {
		t.Errorf("Clobbered = %v, want the workflow to still block sync", clob)
	}

	out := audit.Format(rows, ver)
	for _, want := range []string{
		"promotable (only action versions differ",
		".github/workflows/ci.yml",
		"actions/checkout  v7 → v7.0.1",
		"profiles/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

// The counterweight: a project that genuinely hand-edited a managed workflow
// must NOT be told its change is a promotable version bump. Getting this wrong
// is worse than reporting nothing — it invites a --force reset over real work.
func TestAuditDoesNotCallAHandEditPromotable(t *testing.T) {
	lacquer, project := setupWithWorkflow(t)

	wf := filepath.Join(project, ".github", "workflows", "ci.yml")
	writeFile(t, wf, strings.Replace(managedWorkflow, "      - run: npm ci\n", "      - run: npm ci --ignore-scripts\n", 1))
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "local edit")

	rows, ver, err := audit.Classify(lacquer, project)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := statusOf(rows, ".github/workflows/ci.yml"); got != audit.Modified {
		t.Fatalf("status = %s, want locally-modified", got)
	}
	if promo := audit.Promotable(rows); len(promo) != 0 {
		t.Fatalf("Promotable = %+v, want none — a run: step changed, not a version", promo)
	}
	if out := audit.Format(rows, ver); strings.Contains(out, "promotable") {
		t.Errorf("report offers a promotion for a hand edit:\n%s", out)
	}
}

// setupWithWorkflow is setup() plus a managed workflow, shipped from core/root
// so the plan writes it to .github/workflows/ci.yml verbatim.
func setupWithWorkflow(t *testing.T) (lacquer, project string) {
	t.Helper()
	lacquer, project = setup(t)
	writeFile(t, filepath.Join(lacquer, "core", "root", ".github", "workflows", "ci.yml"), managedWorkflow)
	if _, err := syncpkg.Run(lacquer, project, false); err != nil {
		t.Fatalf("sync the workflow in: %v", err)
	}
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "sync workflow")
	// Guard the fixture: if the plan ever stops shipping this, every assertion
	// below would pass against a file nobody manages.
	if _, err := os.Stat(filepath.Join(project, ".github", "workflows", "ci.yml")); err != nil {
		t.Fatalf("fixture workflow was not synced into the project: %v", err)
	}
	return lacquer, project
}
