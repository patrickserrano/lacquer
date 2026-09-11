package protection

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubGH replaces the `gh` transport for the duration of one test. Keyed on the
// API path so a test can answer the protection endpoint and the rulesets
// endpoint differently — which is the whole of the interesting behaviour here.
func stubGH(t *testing.T, answers map[string]struct {
	stdout string
	fail   bool
}) {
	t.Helper()
	prev := runGH
	t.Cleanup(func() { runGH = prev })
	runGH = func(args ...string) ([]byte, []byte, error) {
		if len(args) != 2 || args[0] != "api" {
			t.Fatalf("unexpected gh invocation: %v", args)
		}
		a, ok := answers[args[1]]
		if !ok {
			t.Fatalf("no stubbed answer for %q", args[1])
		}
		if a.fail {
			return []byte(a.stdout), []byte("gh: failed"), &exec.ExitError{}
		}
		return []byte(a.stdout), nil, nil
	}
}

type answer = struct {
	stdout string
	fail   bool
}

const notProtected = `{"message":"Branch not protected","documentation_url":"https://docs.github.com/rest","status":"404"}`
const forbidden = `{"message":"Upgrade to GitHub Pro or make this repository public to enable this feature.","status":"403"}`

// The dailybread response, copied from the live API.
const dailybreadProtection = `{"required_status_checks":{"checks":[{"app_id":15368,"context":"Build (Release)"},{"app_id":15368,"context":"Lint + Test"}],"contexts":["Build (Release)","Lint + Test"],"strict":true}}`

func TestFetchReadsRequiredContexts(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/app/branches/main/protection": {stdout: dailybreadProtection},
		"repos/org/app/rules/branches/main":      {stdout: `[]`},
	})
	req, err := Fetch("org/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !req.Protected {
		t.Error("a protected branch was reported as unprotected")
	}
	// Deduped across the deprecated `contexts` and the current `checks`, which
	// both carry the same two names.
	if strings.Join(req.Contexts, "|") != "Build (Release)|Lint + Test" {
		t.Errorf("required contexts wrong: %q", req.Contexts)
	}
}

// A repository whose protection is expressed ONLY through `checks` (the field
// that replaced the deprecated `contexts`) must not read as requiring nothing.
func TestFetchReadsTheChecksArrayAlone(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/app/branches/main/protection": {stdout: `{"required_status_checks":{"checks":[{"context":"CI OK"}]}}`},
		"repos/org/app/rules/branches/main":      {stdout: `[]`},
	})
	req, err := Fetch("org/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(req.Contexts, Gate) {
		t.Errorf("a repo requiring %q via `checks` read as requiring %v", Gate, req.Contexts)
	}
}

// 404 "Branch not protected" is an ANSWER. Returning it as an error would turn
// the Windsock/image-proxy finding into "could not check" and hide it.
func TestBranchNotProtectedIsAnAnswerNotAFailure(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/windsock/branches/main/protection": {stdout: notProtected, fail: true},
		"repos/org/windsock/rules/branches/main":      {stdout: `[]`},
	})
	req, err := Fetch("org/windsock", "main")
	if err != nil {
		t.Fatalf("an unprotected branch was reported as an error, hiding the finding: %v", err)
	}
	if req.Protected || len(req.Contexts) != 0 {
		t.Errorf("an unprotected branch produced protection: %+v", req)
	}
}

// The 404 that means "you are not allowed to look", not "there is nothing
// here". Measured live: cli/cli answers 404 {"message":"Not Found"} to an
// account without admin, while a genuinely unprotected branch answers 404
// {"message":"Branch not protected"}. Reading the first as the second would
// report every repository an operator lacks admin on as UNPROTECTED — a
// confident false accusation produced by not looking.
func TestA404WithoutAdminIsNotReportedAsUnprotected(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/cli/cli/branches/trunk/protection": {stdout: `{"message":"Not Found","status":"404"}`, fail: true},
	})
	req, err := Fetch("cli/cli", "trunk")
	if err == nil {
		t.Fatalf("a 404 meaning \"no admin\" was read as a real answer: %+v", req)
	}
	if !strings.Contains(err.Error(), "ADMIN") {
		t.Errorf("the error does not explain that 404 here can mean \"not allowed to look\": %v", err)
	}
	r := Compare("cli/cli", "trunk", Requirements{}, err, Workflows{})
	if r.Verdict != Unavailable {
		t.Errorf("verdict %q; a repository nobody was allowed to read must be %q", r.Verdict, Unavailable)
	}
}

// 403 is the personal-account-on-Free case, verified against
// patrickserrano/dailybread-image-proxy before its org transfer. It must reach
// the caller as an error, so the verdict becomes Unavailable rather than a pass.
func TestForbiddenProtectionIsAnError(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/me/private/branches/main/protection": {stdout: forbidden, fail: true},
	})
	_, err := Fetch("me/private", "main")
	if err == nil {
		t.Fatal("a 403 was reported as a successful read — that is a repository nobody looked at, reported as fine")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the 403 is not named in the error: %v", err)
	}
	r := Compare("me/private", "main", Requirements{}, err, Workflows{})
	if r.Verdict != Unavailable {
		t.Errorf("a 403 produced verdict %q", r.Verdict)
	}
}

// Classic protection and rulesets are separate systems. Reading only the
// classic endpoint would report a ruleset-protected repo as unprotected — the
// mirror image of the defect being hunted, and just as false.
func TestRulesetOnlyProtectionIsNotReportedAsUnprotected(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/app/branches/main/protection": {stdout: notProtected, fail: true},
		"repos/org/app/rules/branches/main": {stdout: `[{"type":"required_status_checks","parameters":` +
			`{"required_status_checks":[{"context":"CI OK"}]}}]`},
	})
	req, err := Fetch("org/app", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !req.Protected {
		t.Fatal("a branch protected by a ruleset was reported as unprotected")
	}
	if !contains(req.Contexts, Gate) {
		t.Errorf("the ruleset's required contexts were dropped: %v", req.Contexts)
	}
	if !strings.Contains(req.Source, "ruleset") {
		t.Errorf("the report does not say where the requirement came from: %q", req.Source)
	}
}

// Unreadable rulesets cannot change a PASSING answer (a ruleset only ever adds
// requirements), so the sweep continues with a note.
func TestUnreadableRulesetsCannotOverturnAPass(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/app/branches/main/protection": {stdout: `{"required_status_checks":{"contexts":["CI OK"]}}`},
		"repos/org/app/rules/branches/main":      {stdout: forbidden, fail: true},
	})
	req, err := Fetch("org/app", "main")
	if err != nil {
		t.Fatalf("a passing repo was reported unchecked over an unreadable ruleset: %v", err)
	}
	if !contains(req.Contexts, Gate) {
		t.Errorf("contexts lost: %v", req.Contexts)
	}
	if !strings.Contains(req.Source, "unreadable") {
		t.Errorf("the report hides that rulesets could not be read: %q", req.Source)
	}
}

// But an unreadable ruleset CAN overturn a finding: the ruleset might be the
// thing requiring the gate. Reporting "requires the wrong thing" off evidence
// that was never read is exactly the defect this package exists to catch.
func TestUnreadableRulesetsTurnAFindingIntoUnchecked(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/app/branches/main/protection": {stdout: dailybreadProtection},
		"repos/org/app/rules/branches/main":      {stdout: forbidden, fail: true},
	})
	_, err := Fetch("org/app", "main")
	if err == nil {
		t.Fatal("a finding was reported although the ruleset that might contradict it was never read")
	}
	r := Compare("org/app", "main", Requirements{}, err, Workflows{})
	if r.Verdict != Unavailable {
		t.Errorf("verdict %q; an unread ruleset must produce %q", r.Verdict, Unavailable)
	}
}

// The default branch is asked for, not assumed. Guessing "main" would report a
// repository still on `master` as UNPROTECTED — a false finding
// indistinguishable from a true one.
func TestDefaultBranchIsAskedFor(t *testing.T) {
	stubGH(t, map[string]answer{"repos/org/legacy": {stdout: `{"default_branch":"master"}`}})
	b, err := DefaultBranch("org/legacy")
	if err != nil {
		t.Fatal(err)
	}
	if b != "master" {
		t.Errorf("default branch %q, want master", b)
	}
}

// A `gh` that is missing, logged out, or offline writes no parseable body. That
// is not a 404 and must not be read as one.
func TestAnUnparseableFailureIsNotMistakenForNoProtection(t *testing.T) {
	stubGH(t, map[string]answer{
		"repos/org/app/branches/main/protection": {stdout: "", fail: true},
	})
	if _, err := Fetch("org/app", "main"); err == nil {
		t.Fatal("a transport failure was read as a successful answer")
	}
}

// Defense in depth: the slug and branch reach `exec.Command`.
func TestFetchRefusesAnUnsafeRepoOrBranch(t *testing.T) {
	prev := runGH
	t.Cleanup(func() { runGH = prev })
	runGH = func(args ...string) ([]byte, []byte, error) {
		t.Fatalf("gh was invoked with an unvalidated argument: %v", args)
		return nil, nil, nil
	}
	if _, err := Fetch("org/app; rm -rf /", "main"); err == nil {
		t.Error("an unsafe repository slug was accepted")
	}
	if _, err := Fetch("org/app", "main;id"); err == nil {
		t.Error("an unsafe branch name was accepted")
	}
	if _, err := DefaultBranch("not-a-slug"); err == nil {
		t.Error("a malformed slug was accepted")
	}
}

func gitInit(t *testing.T, dir, origin string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// Three of seventeen checkouts in this fleet sit in a directory named
// differently from their repository, so the slug comes from the remote.
func TestSlugComesFromTheOriginRemote(t *testing.T) {
	for _, tc := range []struct{ origin, want string }{
		{"git@github.com:PixelFoxStudio/dailybread.git", "PixelFoxStudio/dailybread"},
		{"https://github.com/PixelFoxStudio/Windsock.git", "PixelFoxStudio/Windsock"},
		{"https://github.com/PixelFoxStudio/Windsock", "PixelFoxStudio/Windsock"},
	} {
		dir := filepath.Join(t.TempDir(), "checkout")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInit(t, dir, tc.origin)
		got, err := Slug(dir)
		if err != nil {
			t.Fatalf("%s: %v", tc.origin, err)
		}
		if got != tc.want {
			t.Errorf("origin %s -> %q, want %q", tc.origin, got, tc.want)
		}
	}
}

func TestSlugRefusesACheckoutWithNoOrigin(t *testing.T) {
	dir := t.TempDir()
	if _, err := Slug(dir); err == nil {
		t.Error("a checkout with no origin yielded a slug, which would point the API at some other repository")
	}
}
