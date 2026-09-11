package testtargets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shape dailybread actually has: its own scheme, a watch simulator
// destination, a watch-only `-only-testing:` selector, and a pull_request
// trigger. The xcodebuild call is split over continuation lines because that is
// how every real one is written.
const watchCI = `name: Watch CI
on:
  pull_request:
    branches: [main]
  workflow_dispatch:

jobs:
  watch-tests:
    runs-on: [self-hosted, macOS]
    steps:
      - uses: actions/checkout@v4
      - name: Run watch tests
        run: |
          xcodebuild test \
            -project DailyBread.xcodeproj \
            -scheme "DailyBreadWatchApp Watch App" \
            -destination "platform=watchOS Simulator,id=$WATCH_ID" \
            -only-testing:"DailyBreadWatchApp Watch AppTests"
`

const watchTarget = "DailyBreadWatchApp Watch AppTests"

// project writes workflow files into a throwaway project root.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// decl is the declaration under test, pointing at .github/workflows/watch-ci.yml.
func decl() []Declaration {
	return []Declaration{{
		Target:   watchTarget,
		Workflow: ".github/workflows/watch-ci.yml",
		Reason:   "watchOS bundle: different scheme, watch simulator destination, neither expressible in a [[product]] leg",
	}}
}

// report runs the whole path a caller runs: compare, verify, apply.
func report(t *testing.T, dir string, selectors []string, decls []Declaration, managed map[string]bool) Report {
	t.Helper()
	targets := parseFixture(t)
	return Apply(Compare(targets, selectors), Verify(dir, decls, targets, managed))
}

func uncovered(r Report, name string) bool {
	for _, u := range r.Uncovered {
		if u.Name == name {
			return true
		}
	}
	return false
}

// The false positive this exists to remove. dailybread's watch-ci.yml runs 76
// watch tests on every pull request, and the audit called the target uncovered.
func TestVerifiedDeclarationRemovesTheFalsePositive(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})
	r := report(t, dir, []string{"DailyBreadTests"}, decl(), nil)

	if uncovered(r, watchTarget) {
		t.Errorf("a verified covered_elsewhere target is still reported as running nowhere: %+v", r.Uncovered)
	}
	if len(r.Elsewhere) != 1 || len(r.Unconfirmed) != 0 || len(r.Stale) != 0 {
		t.Fatalf("elsewhere=%d unconfirmed=%d stale=%d, want 1/0/0", len(r.Elsewhere), len(r.Unconfirmed), len(r.Stale))
	}

	out := Format(r)
	// Suppressed is not the same as invisible. An exception nobody can see is an
	// exception nobody reviews, and with no expiry to force the question the
	// printed reason is the only thing that will.
	if !strings.Contains(out, watchTarget) || !strings.Contains(out, "watch-ci.yml") {
		t.Errorf("the accepted declaration is not shown at all:\n%s", out)
	}
	if !strings.Contains(out, "different scheme") {
		t.Errorf("the report drops the reason, which is the only record of why this exists:\n%s", out)
	}
	// The check must not be readable as "these tests pass".
	for _, want := range []string{"NOT checked", "not proof it works"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not state the limits of what it verified (%q missing):\n%s", want, out)
		}
	}
}

// The direction that matters. Every one of these is a declaration the audit
// could not bear out, and in every one the finding must come back — a claim that
// suppresses a finding by being written is the defect class in lacquer#333,
// failing open and in silence.
func TestUnverifiableDeclarationsDoNotSuppressTheFinding(t *testing.T) {
	// Named the target, runs tests, triggers on a PR — but nowhere on disk.
	missing := map[string]string{".github/workflows/other.yml": watchCI}

	// Names the target only in a comment.
	commented := strings.Replace(watchCI,
		`            -only-testing:"DailyBreadWatchApp Watch AppTests"`,
		"          # covers DailyBreadWatchApp Watch AppTests\n          echo done", 1)

	// Exists, names the target, triggers on a PR — and runs no tests. The shape
	// of a workflow that was going to run them and never did.
	noTests := "name: Watch CI\non:\n  pull_request:\n\njobs:\n  watch-tests:\n    runs-on: macos-15\n" +
		"    steps:\n      - name: DailyBreadWatchApp Watch AppTests\n        run: echo \"TODO: wire this up\"\n"

	// Runs the tests, but only when somebody remembers to press the button.
	manual := strings.Replace(watchCI, "on:\n  pull_request:\n    branches: [main]\n  workflow_dispatch:",
		"on:\n  workflow_dispatch:\n  schedule:\n    - cron: \"0 4 * * *\"", 1)

	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"workflow does not exist", missing, "does not exist"},
		{"target named only in a comment", map[string]string{".github/workflows/watch-ci.yml": commented}, "never names"},
		{"workflow runs no tests", map[string]string{".github/workflows/watch-ci.yml": noTests}, "no test invocation"},
		{"nothing triggers it on a code change", map[string]string{".github/workflows/watch-ci.yml": manual}, "not triggered by a code change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := report(t, project(t, tc.files), []string{"DailyBreadTests"}, decl(), nil)
			if !uncovered(r, watchTarget) {
				t.Fatalf("an unverifiable declaration removed the finding — %q is now reported by nothing", watchTarget)
			}
			if len(r.Elsewhere) != 0 {
				t.Errorf("an unverifiable declaration was accepted: %+v", r.Elsewhere)
			}
			if len(r.Unconfirmed) != 1 {
				t.Fatalf("unconfirmed = %d, want 1: %+v", len(r.Unconfirmed), r.Unconfirmed)
			}
			out := Format(r)
			if !strings.Contains(out, "NOT CONFIRMED") {
				t.Errorf("the report does not say the declaration failed to verify:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the report does not say WHICH check failed (want %q):\n%s", tc.want, out)
			}
		})
	}
}

// Pointing the declaration at a workflow the lacquer writes is refused. If a
// managed workflow ran the target there would be a selector naming it and no
// finding to suppress; and the next `sync` overwrites the file, so any coverage
// hand-added to it disappears without the declaration noticing.
func TestDeclarationCannotPointAtAManagedWorkflow(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/ci.yml": watchCI})
	d := decl()
	d[0].Workflow = ".github/workflows/ci.yml"

	r := report(t, dir, []string{"DailyBreadTests"}, d, map[string]bool{".github/workflows/ci.yml": true})
	if !uncovered(r, watchTarget) {
		t.Fatal("a declaration naming a lacquer-managed workflow suppressed the finding")
	}
	if !strings.Contains(Format(r), "the lacquer writes") {
		t.Errorf("the report does not explain why a managed workflow cannot be the evidence:\n%s", Format(r))
	}
}

// A path that leaves the project is refused rather than followed. The manifest
// is hand-edited, so its paths are not a trusted source — internal/audit treats
// lock keys the same way.
func TestDeclarationPathCannotEscapeTheProject(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})
	d := decl()
	d[0].Workflow = "../elsewhere/.github/workflows/watch-ci.yml"

	r := report(t, dir, []string{"DailyBreadTests"}, d, nil)
	if !uncovered(r, watchTarget) {
		t.Fatal("an escaping path was followed and suppressed the finding")
	}
	if !strings.Contains(Format(r), "not a path inside this project") {
		t.Errorf("the report does not say the path was refused:\n%s", Format(r))
	}
}

// The permissive direction has to keep working: one declared target must not
// quiet the three other suites nothing runs.
func TestOtherUncoveredTargetsAreStillReported(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})
	r := report(t, dir, []string{"DailyBreadTests"}, decl(), nil)

	for _, want := range []string{"DailyBreadWidgetsTests", "DailyBreadWatchApp Watch AppUITests"} {
		if !uncovered(r, want) {
			t.Errorf("%q stopped being reported because a DIFFERENT target was declared covered elsewhere", want)
		}
	}
	if len(r.Uncovered) != 2 {
		t.Errorf("uncovered = %+v, want exactly the two undeclared suites", r.Uncovered)
	}
}

// What replaces an expiry date. The declaration has no `until`; it comes back
// for review when it stops being true — the target is renamed or deleted, or a
// managed selector starts covering it — and a stale one is reported rather than
// left reading as a live exception.
func TestDeclarationsThatStoppedMeaningAnythingAreReported(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})

	t.Run("target no longer exists", func(t *testing.T) {
		d := decl()
		d[0].Target = "DailyBreadWatchApp Watch AppTests-renamed"
		r := report(t, dir, []string{"DailyBreadTests"}, d, nil)
		if len(r.Stale) != 1 {
			t.Fatalf("stale = %+v, want the renamed declaration", r.Stale)
		}
		if !strings.Contains(Format(r), "no test target with that name") {
			t.Errorf("the report does not say why the declaration is dead:\n%s", Format(r))
		}
	})

	t.Run("a managed selector covers it now", func(t *testing.T) {
		// The state after the lacquer grows a way to run the target: the
		// selector covers it, and the declaration is a second answer to a
		// question nobody is asking any more.
		r := report(t, dir, []string{"DailyBreadTests", watchTarget}, decl(), nil)
		if len(r.Stale) != 1 || len(r.Elsewhere) != 0 {
			t.Fatalf("stale=%+v elsewhere=%+v, want the declaration reported as redundant", r.Stale, r.Elsewhere)
		}
		if !strings.Contains(Format(r), "already covers this target") {
			t.Errorf("the report does not say the declaration is redundant:\n%s", Format(r))
		}
	})
}

// A project that declares nothing must produce exactly what it produced before
// this field existed. The check is already reported on every audit of every iOS
// project in the fleet; a new empty section in all of them is noise.
func TestNoDeclarationsChangesNothing(t *testing.T) {
	dir := project(t, map[string]string{".github/workflows/watch-ci.yml": watchCI})
	targets := parseFixture(t)
	var sel []string
	for _, x := range targets {
		sel = append(sel, x.Name)
	}
	r := Apply(Compare(targets, sel), Verify(dir, nil, targets, nil))
	if Format(r) != "" {
		t.Errorf("a fully wired project with no declarations produced output:\n%s", Format(r))
	}
}

// The `on:` read is a shape read, not a grep: `github.event_name ==
// 'pull_request'` inside an `if:` is not a trigger, and counting it as one would
// confirm a manual workflow as pull-request coverage.
func TestTriggersReadsTheOnBlockRatherThanTheFile(t *testing.T) {
	body := "name: X\non:\n  workflow_dispatch:\n\njobs:\n  t:\n    if: github.event_name == 'pull_request'\n" +
		"    steps:\n      - run: xcodebuild test -scheme X\n"
	if got := triggers(body); len(got) != 1 || got[0] != "workflow_dispatch" {
		t.Fatalf("triggers = %+v, want [workflow_dispatch]", got)
	}
	if anyAuto(triggers(body)) {
		t.Error("an `if:` expression mentioning pull_request was counted as a trigger")
	}
	// The two spellings in the fleet, plus the quoted form YAML 1.1 forces on
	// anyone who noticed that bare `on` parses as the boolean true.
	for _, tc := range []struct{ name, body string }{
		{"inline list", "on: [push, workflow_dispatch]\n"},
		{"inline scalar", "on: pull_request\n"},
		{"quoted key", "\"on\":\n  push:\n    branches: [main]\n"},
		{"list items", "on:\n  - pull_request\n  - workflow_dispatch\n"},
	} {
		if !anyAuto(triggers(tc.body)) {
			t.Errorf("%s: triggers = %+v, none read as a code-change trigger", tc.name, triggers(tc.body))
		}
	}
}

// The join is not cosmetic. A real xcodebuild call is split over continuation
// lines, and a line-at-a-time search finds no "xcodebuild test" in a file that
// is unambiguously running tests — which would report every watch project's
// declaration as unverifiable.
func TestTestInvocationIsFoundAcrossContinuationLines(t *testing.T) {
	split := "        run: |\n          xcodebuild \\\n            test \\\n            -scheme \"Watch App\" \\\n" +
		"            -destination \"platform=watchOS Simulator,id=$ID\"\n"
	if !runsTests(split) {
		t.Error("a continuation-split `xcodebuild test` was not recognised as running tests")
	}
	// And the word has to be the action, not any appearance of "test".
	if runsTests("        run: xcodebuild archive -resultBundlePath test.xcresult\n") {
		t.Error("`test.xcresult` in an archive command was read as a test run")
	}
	if runsTests("        run: echo \"no tests here\"\n") {
		t.Error("a workflow that runs nothing was read as running tests")
	}
}
