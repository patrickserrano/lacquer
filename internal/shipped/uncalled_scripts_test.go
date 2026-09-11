package shipped

import (
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/assets"
	"github.com/patrickserrano/lacquer/internal/audit"
)

// These run audit.UncalledScripts against the REAL core/ and profiles/ trees,
// through the committed fixtures under testdata/projects. The unit tests in
// internal/audit pin the mechanism on a stub lacquer; these pin what it says
// about the content this repository actually ships, which is the half that
// changes under people's feet.

// uncalled runs the check on a fixture and returns the reported paths.
func uncalled(t *testing.T, name string) ([]string, []audit.UncalledScript) {
	t.Helper()
	p := fromFixture(t, name)
	found, err := audit.UncalledScripts(p.lacquerRoot, p.root)
	if err != nil {
		t.Fatalf("UncalledScripts on %s: %v", name, err)
	}
	dests := make([]string, 0, len(found))
	for _, f := range found {
		if !f.Confirmed() {
			t.Errorf("%s: the sweep could not finish for %s (%s) — a committed fixture is a git "+
				"repository with every file readable, so this means the check lost its way, not that "+
				"the project is unusual", name, f.Dest, f.Unconfirmable)
		}
		dests = append(dests, f.Dest)
	}
	sort.Strings(dests)
	return dests, found
}

// THE LIVE FINDING, and the reason this detector exists. rootapp is an iOS
// project with no [[product]] block, so tokens.ProductSecrets renders nothing at
// {{IOS_PRODUCT_SECRETS}} and the release workflow never names the script. The
// file still ships, still has its executable bit, and profiles/ios/CLAUDE.ios.md
// still tells the reader that `release.yml` runs it.
//
// If somebody wires a caller for the no-secrets case, this test fails and should
// simply be deleted. Until then it is the assertion that the fleet is in the
// state lacquer#333 described.
func TestWriteReleaseConfigHasNoCallerWithoutDeclaredSecrets(t *testing.T) {
	dests, found := uncalled(t, "rootapp")
	const script = "scripts/write-release-config.sh"
	if !containsString(dests, script) {
		t.Fatalf("%s has a caller on a project declaring no [[product]].secrets; reported: %v", script, dests)
	}
	for _, f := range found {
		if f.Dest != script {
			continue
		}
		if len(f.Documented) == 0 {
			t.Errorf("the shipped CLAUDE.md region describes this script running and the finding does "+
				"not say so: %+v", f)
		}
		out := audit.FormatUncalledScripts(found)
		if !strings.Contains(out, "DOCUMENTED AS RUNNING") {
			t.Errorf("the report does not surface the documentation claim:\n%s", out)
		}
	}
}

// The same script, the same profile, a manifest that declares secrets — and the
// caller now exists, because it is built by tokens.ProductSecrets and
// substituted in. A check that grepped profiles/ for the filename would find it
// in no workflow at all and report this correct project as broken, which is
// exactly the failure #319 shipped one layer over.
//
// The rendered bytes are asserted directly as well as through the check, so a
// future refactor cannot make both agree on the wrong answer.
func TestWriteReleaseConfigIsCalledWhenAProductDeclaresSecrets(t *testing.T) {
	p := fromFixture(t, "duoapp")
	cfg := p.config()
	plan, err := assets.Plan(p.lacquerRoot, cfg)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var release string
	for _, a := range plan {
		if a.Dest != ".github/workflows/ios-release.yml" {
			continue
		}
		body, _, err := assets.Render(a, cfg)
		if err != nil {
			t.Fatalf("render release: %v", err)
		}
		release = string(body)
	}
	if release == "" {
		t.Fatal("duoapp's plan has no rendered ios-release.yml")
	}
	if !strings.Contains(release, "scripts/write-release-config.sh") {
		t.Fatal("the rendered release workflow does not invoke write-release-config.sh — the fixture no " +
			"longer exercises the token-rendered caller this test exists for")
	}

	dests, _ := uncalled(t, "duoapp")
	if containsString(dests, "scripts/write-release-config.sh") {
		t.Fatalf("a script the rendered release workflow demonstrably runs was reported as uncalled: %v", dests)
	}
}

// The guard. Every other script core/ and profiles/ ship has a caller on the two
// fully-profiled fixtures, and this pins that so a new script arriving without
// one is a test failure rather than a discovery months later. It fails in both
// directions: an over-reporting change to the check lands here too.
func TestTheOnlyUncalledScriptOnAProfiledProjectIsTheKnownOne(t *testing.T) {
	for _, name := range []string{"rootapp", "multistack"} {
		dests, _ := uncalled(t, name)
		want := []string{"scripts/write-release-config.sh"}
		if !equalStrings(dests, want) {
			t.Errorf("%s: uncalled scripts = %v, want %v.\nA new entry means a script was shipped with "+
				"no caller; a missing entry means either it was wired up (delete it from want) or the "+
				"check stopped finding it.", name, dests, want)
		}
	}
}

// A project whose components declare no profiles gets core's three scripts and
// nothing that runs them: core/root ships scripts/, but lefthook.yml and
// .pre-commit-config.yaml — the only things that call them — come from the web,
// supabase and ios profiles.
//
// Pinned rather than fixed here, because the remedy is a decision about what
// core ships to a profile-less project and not a change to this check. What the
// test buys is that the state is written down and visible instead of being
// rediscovered.
func TestAProfilelessProjectGetsCoreScriptsWithNothingToRunThem(t *testing.T) {
	dests, _ := uncalled(t, "spmpackage")
	want := []string{
		"scripts/check-commit-msg.sh",
		"scripts/check-secrets.sh",
		"scripts/docs-relaxation.sh",
	}
	if !equalStrings(dests, want) {
		t.Errorf("spmpackage: uncalled scripts = %v, want %v", dests, want)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
