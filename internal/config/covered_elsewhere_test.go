package config

import (
	"strings"
	"testing"
)

const coveredBase = "[project]\nname = \"DailyBread\"\nproject_name = \"DailyBread\"\nscheme = \"DailyBread\"\n"

const goodCovered = coveredBase + `
[[project.covered_elsewhere]]
target = "DailyBreadWatchApp Watch AppTests"
workflow = ".github/workflows/watch-ci.yml"
reason = "watchOS bundle: a different scheme and a watch simulator destination, neither expressible in a [[product]] leg"
`

func TestCoveredElsewhereLoads(t *testing.T) {
	cfg, err := loadString(t, goodCovered)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Project.CoveredElsewhere
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Target != "DailyBreadWatchApp Watch AppTests" {
		t.Errorf("target = %q — a quoted name with spaces is the normal case here, not an exotic one", got[0].Target)
	}
	if got[0].Workflow != ".github/workflows/watch-ci.yml" || got[0].Reason == "" {
		t.Errorf("entry did not decode: %+v", got[0])
	}
}

// Every field is required, because a half-written declaration cannot be
// verified — and an unverifiable declaration that still suppressed a finding is
// exactly what this must never become.
func TestCoveredElsewhereRejectsMalformedEntries(t *testing.T) {
	entry := func(body string) string { return coveredBase + "\n[[project.covered_elsewhere]]\n" + body }

	for _, tc := range []struct{ name, toml, want string }{
		{
			"no target",
			entry("workflow = \".github/workflows/watch-ci.yml\"\nreason = \"watch\"\n"),
			"needs a target",
		},
		{
			"no workflow",
			entry("target = \"WatchTests\"\nreason = \"watch\"\n"),
			"needs a workflow",
		},
		{
			"no reason",
			entry("target = \"WatchTests\"\nworkflow = \".github/workflows/watch-ci.yml\"\n"),
			"needs a reason",
		},
		{
			"blank reason",
			entry("target = \"WatchTests\"\nworkflow = \".github/workflows/watch-ci.yml\"\nreason = \"   \"\n"),
			"needs a reason",
		},
		{
			// Not a workflow, so there is nothing that could run anything.
			"workflow outside .github/workflows",
			entry("target = \"WatchTests\"\nworkflow = \"docs/watch-testing.md\"\nreason = \"watch\"\n"),
			"invalid workflow",
		},
		{
			"workflow escaping the repository",
			entry("target = \"WatchTests\"\nworkflow = \".github/workflows/../../etc/passwd\"\nreason = \"watch\"\n"),
			"invalid workflow",
		},
		{
			"target with shell metacharacters",
			entry("target = \"Watch$(whoami)Tests\"\nworkflow = \".github/workflows/watch-ci.yml\"\nreason = \"watch\"\n"),
			"invalid target",
		},
		{
			"unknown key",
			entry("target = \"WatchTests\"\nworkflow = \".github/workflows/watch-ci.yml\"\nreason = \"w\"\nworkfow = \"x\"\n"),
			"unknown [[project.covered_elsewhere]] key",
		},
		{
			// Nothing has ever written a bare-string form, so a typo is an error
			// rather than a field silently dropped.
			"bare string instead of a table",
			coveredBase + "covered_elsewhere = [\"WatchTests\"]\n",
			"must be a table",
		},
		{
			"the same target twice",
			entry("target = \"WatchTests\"\nworkflow = \".github/workflows/a.yml\"\nreason = \"one\"\n") +
				"\n[[project.covered_elsewhere]]\ntarget = \"WatchTests\"\nworkflow = \".github/workflows/b.yml\"\nreason = \"two\"\n",
			"twice",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, tc.toml)
			if err == nil {
				t.Fatalf("%s loaded clean; a declaration nothing can check must not be accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name the problem (want %q):\n%s", tc.want, err)
			}
		})
	}
}

// The expiry decision, pinned. `until` is what a reader who knows
// dependabot_ignore and [baseline.relax] will reach for; accepting and dropping
// it would leave them believing this comes back for review on a date. It does
// not — it comes back when it stops verifying — and the error has to say so
// rather than reading as an oversight.
func TestCoveredElsewhereRejectsAnExpiryAndExplainsWhy(t *testing.T) {
	for _, key := range []string{"until", "expires"} {
		_, err := loadString(t, coveredBase+
			"\n[[project.covered_elsewhere]]\ntarget = \"WatchTests\"\n"+
			"workflow = \".github/workflows/watch-ci.yml\"\nreason = \"watch\"\n"+
			key+" = \"2026-12-31\"\n")
		if err == nil {
			t.Fatalf("%q was accepted; a date this mechanism does not act on reads as a review that will happen", key)
		}
		msg := err.Error()
		if !strings.Contains(msg, key) {
			t.Errorf("error does not name the rejected key:\n%s", msg)
		}
		if !strings.Contains(msg, "re-verified") {
			t.Errorf("error does not say what replaces the date, so it reads as an oversight:\n%s", msg)
		}
	}
}
