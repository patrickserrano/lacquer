package config

import (
	"slices"
	"strings"
	"testing"
)

const notRunBase = "[project]\nname = \"MomFriend\"\nproject_name = \"MomFriend\"\nscheme = \"MomFriend\"\n" +
	"xcodeproj = \"ios/MomFriend.xcodeproj\"\n"

// momfriend's real case: a local-package suite CI builds and never runs, because
// it needs on-device models and is written to fail, not skip, without them.
const goodNotRun = notRunBase + `
[[project.not_run_in_ci]]
target = "MomFriendCoreTests"
reason = "needs on-device models; built in CI, run on device before release"
until  = "2026-12-31"
`

func TestNotRunInCILoads(t *testing.T) {
	cfg, err := loadString(t, goodNotRun)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Project.NotRunInCI
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	want := NotRunInCI{
		Target: "MomFriendCoreTests",
		Reason: "needs on-device models; built in CI, run on device before release",
		Until:  "2026-12-31",
	}
	if got[0] != want {
		t.Errorf("entry did not decode:\n got %+v\nwant %+v", got[0], want)
	}
	if d, err := got[0].UntilDate(); err != nil || d.Format("2006-01-02") != "2026-12-31" {
		t.Errorf("UntilDate() = %v, %v", d, err)
	}
}

// The key is found by the manifestTables reflection, so the strict unknown-key
// check covers the table's interior rather than going blind inside it.
func TestNotRunInCIIsAReflectedTable(t *testing.T) {
	if !slices.Contains(manifestTables["project"].keys, "not_run_in_ci") {
		t.Fatalf("[project] does not list not_run_in_ci: %v", manifestTables["project"].keys)
	}
	tbl, ok := manifestTables["project.not_run_in_ci"]
	if !ok {
		t.Fatal("project.not_run_in_ci is not a recorded table")
	}
	if got := strings.Join(tbl.keys, ","); got != "reason,target,until" {
		t.Errorf("keys = %q, want reason,target,until", got)
	}
	if tbl.label != "[[project.not_run_in_ci]]" {
		t.Errorf("label = %q", tbl.label)
	}
}

// Every field is required. A declaration with no reason cannot be reviewed, and
// one with no date never comes back for review — which is the whole of the
// difference between this and deleting the finding.
func TestNotRunInCIRejectsMalformedEntries(t *testing.T) {
	entry := func(body string) string { return notRunBase + "\n[[project.not_run_in_ci]]\n" + body }

	for _, tc := range []struct{ name, toml, want string }{
		{
			"no target",
			entry("reason = \"device\"\nuntil = \"2026-12-31\"\n"),
			"needs a target",
		},
		{
			"target with shell metacharacters",
			entry("target = \"Core$(whoami)Tests\"\nreason = \"device\"\nuntil = \"2026-12-31\"\n"),
			"invalid target",
		},
		{
			"no reason",
			entry("target = \"CoreTests\"\nuntil = \"2026-12-31\"\n"),
			"needs a reason",
		},
		{
			"blank reason",
			entry("target = \"CoreTests\"\nreason = \"  \"\nuntil = \"2026-12-31\"\n"),
			"needs a reason",
		},
		{
			"no until",
			entry("target = \"CoreTests\"\nreason = \"device\"\n"),
			"needs an until date",
		},
		{
			"malformed until",
			entry("target = \"CoreTests\"\nreason = \"device\"\nuntil = \"31/12/2026\"\n"),
			"invalid until",
		},
		{
			"impossible until",
			entry("target = \"CoreTests\"\nreason = \"device\"\nuntil = \"2026-02-30\"\n"),
			"invalid until",
		},
		{
			// An unquoted TOML date is a datetime, not a string. It must not be
			// accepted and silently dropped into an empty until.
			"unquoted TOML date",
			entry("target = \"CoreTests\"\nreason = \"device\"\nuntil = 2026-12-31\n"),
			"not_run_in_ci.until",
		},
		{
			"unknown key",
			entry("target = \"CoreTests\"\nreason = \"device\"\nuntil = \"2026-12-31\"\nworkflow = \".github/workflows/x.yml\"\n"),
			"project.not_run_in_ci.workflow",
		},
		{
			"the same target twice",
			entry("target = \"CoreTests\"\nreason = \"one\"\nuntil = \"2026-12-31\"\n") +
				"\n[[project.not_run_in_ci]]\ntarget = \"CoreTests\"\nreason = \"two\"\nuntil = \"2027-01-31\"\n",
			"twice",
		},
		{
			// Two answers that cannot both be true: one says CI runs it, the
			// other says CI deliberately does not.
			"also declared covered_elsewhere",
			entry("target = \"CoreTests\"\nreason = \"device\"\nuntil = \"2026-12-31\"\n") +
				"\n[[project.covered_elsewhere]]\ntarget = \"CoreTests\"\nworkflow = \".github/workflows/core.yml\"\nreason = \"core\"\n",
			"also declared in [[project.covered_elsewhere]]",
		},
		{
			// With no Xcode project the audit reads no test targets, so the
			// declaration — and its expiry — would never be evaluated at all.
			"no xcodeproj",
			"[project]\nname = \"x\"\n\n[[project.not_run_in_ci]]\ntarget = \"CoreTests\"\nreason = \"device\"\nuntil = \"2026-12-31\"\n",
			"needs [project].xcodeproj",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, tc.toml)
			if err == nil {
				t.Fatalf("%s loaded clean", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not name the problem (want %q):\n%s", tc.want, err)
			}
		})
	}
}

// Load checks shape only. An entry already past its date must still load, or
// the project is locked out of `sync` and `fix` on the day it expires; expiry is
// the audit's to report, as it is for [baseline.relax] and dependabot_ignore.
func TestNotRunInCIPastUntilStillLoads(t *testing.T) {
	_, err := loadString(t, notRunBase+"\n[[project.not_run_in_ci]]\ntarget = \"CoreTests\"\nreason = \"device\"\nuntil = \"2020-01-01\"\n")
	if err != nil {
		t.Fatalf("an expired declaration must load (audit reports it): %v", err)
	}
}
