package testtargets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Declaration is a project's claim that a test target is run by a workflow this
// lacquer does not manage. It is [[project.covered_elsewhere]], decoupled from
// the config package so this one stays a pure function of a directory.
type Declaration struct {
	// Target is the test target's exact name, as project.pbxproj spells it.
	Target string
	// Workflow is the repo-relative path of the workflow said to run it.
	Workflow string
	// Reason is why the managed workflows cannot.
	Reason string
}

// Claim is a Declaration after it has been checked against the repository.
//
// The three states are deliberately distinct, and only one of them suppresses
// anything:
//
//   - Confirmed: every check below found what it was looking for. The target is
//     moved out of the uncovered list into a section of its own, reason
//     included. It is never simply dropped — an exception nobody can see is an
//     exception nobody reviews.
//   - Not confirmed: the target STAYS in the uncovered list, with the failed
//     check printed on its line. This is the direction that matters. A
//     declaration the audit cannot verify must cost the project its finding
//     back, or "covered elsewhere" is a sentence anyone can write to make a
//     finding disappear — the defect class in lacquer#333, failing open and in
//     silence.
//   - Stale: the declaration is not doing anything (no such target, or a managed
//     selector already runs it). Reported, because a declaration that has
//     outlived its cause reads as a live exception to the next person.
type Claim struct {
	Declaration
	// Confirmed reports that every check passed. Only a confirmed, non-stale
	// claim removes its target from Report.Uncovered.
	Confirmed bool
	// Problems is what could not be confirmed, in the order checked. Non-empty
	// exactly when a non-stale claim is not confirmed.
	Problems []string
	// Stale is why this declaration is not doing anything, and "" when it is.
	Stale string
}

// autoEvents are the `on:` triggers that fire a workflow from a code change.
//
// The distinction being drawn is between a workflow that RUNS the target and one
// that merely COULD. `workflow_dispatch` and `schedule` are real triggers, but a
// suite that runs when somebody remembers, or at 4am against main, is not
// covering the pull request that broke it — which is the claim this declaration
// makes. workflow_call is here because a reusable workflow inherits its caller's
// trigger; that is weaker evidence than the rest, and it is accepted rather than
// chased because following the call graph is a YAML-parsing problem this check
// deliberately does not take on.
var autoEvents = map[string]bool{
	"pull_request":        true,
	"pull_request_target": true,
	"push":                true,
	"merge_group":         true,
	"workflow_call":       true,
}

// testAction matches an xcodebuild action that runs or builds tests, as a whole
// word — so `-resultBundlePath test.xcresult` is not mistaken for one.
var testAction = regexp.MustCompile(`(^|\s)(test|test-without-building|build-for-testing)(\s|$)`)

// Verify checks every declaration against the repository and returns what it
// found.
//
// WHAT THE EVIDENCE PROVES. All of it together proves that a file exists at the
// declared path, that this lacquer does not write that file, that it names the
// target on a line that is not a comment, that it contains something that runs
// tests, and that it is triggered by a code change. That is enough to
// distinguish a real project-owned test job from a sentence in a manifest, and
// it is enough to notice the ways the arrangement actually decays: the workflow
// is renamed, deleted, or stops naming the target.
//
// WHAT IT DOES NOT PROVE, and no wording in the report should imply otherwise:
// that the tests RAN, that they passed, that the mention of the target is the
// `-only-testing:` selector rather than a job name or an `env:` value, that the
// test invocation and the mention are in the same job, or that the workflow's
// own result is required to merge. A workflow whose test step is `|| true`, or
// whose job is skipped by an `if:`, satisfies every check here. This is
// EVIDENCE THAT THE CLAIM IS PLAUSIBLE AND CURRENT, not proof that the tests
// run — the lacquer cannot see a CI run from a working tree, and a check that
// implied it could would be the thing it exists to catch.
//
// The honest handling of the gap is that nothing here can turn a finding green
// on its own: an unconfirmed claim leaves the target reported, and a confirmed
// one moves it to a section that prints the reason and the evidence's limits.
//
// managed is the set of repo-relative paths this lacquer ships to this project,
// used to reject a declaration pointing at a managed workflow. Two reasons: if a
// managed workflow really ran the target, a selector would name it and there
// would be no finding to suppress; and the next `lacquer sync` overwrites that
// file, so any coverage it did provide can vanish without the declaration
// noticing. An empty set skips the check rather than guessing — see Parse on why
// "could not look" is not "it is not there".
func Verify(projectRoot string, decls []Declaration, project []Target, managed map[string]bool) []Claim {
	have := make(map[string]bool, len(project))
	for _, t := range project {
		have[t.Name] = true
	}

	out := make([]Claim, 0, len(decls))
	for _, d := range decls {
		c := Claim{Declaration: d}
		if !have[d.Target] {
			// Nothing to suppress and nothing to check: the target this speaks
			// for is not in the project. Usually it was renamed or deleted, which
			// is exactly when a declaration quietly stops matching anything.
			c.Stale = "this project has no test target with that name (renamed, or deleted)"
		} else {
			c.Problems = problems(projectRoot, d, managed)
			c.Confirmed = len(c.Problems) == 0
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}

// problems returns every check this declaration failed, in the order checked,
// and nil when it passed all of them.
func problems(projectRoot string, d Declaration, managed map[string]bool) []string {
	rel := filepath.FromSlash(d.Workflow)
	if !filepath.IsLocal(rel) {
		// The manifest is hand-edited and committed, so its paths are not a
		// trusted source. Anything that would read outside the project is
		// refused rather than followed, as internal/audit does with lock keys.
		return []string{fmt.Sprintf("%s is not a path inside this project", d.Workflow)}
	}
	body, err := os.ReadFile(filepath.Join(projectRoot, rel))
	if err != nil {
		if os.IsNotExist(err) {
			return []string{fmt.Sprintf("%s does not exist in this project", d.Workflow)}
		}
		return []string{fmt.Sprintf("%s could not be read: %v", d.Workflow, err)}
	}
	text := string(body)

	var probs []string
	if managed[d.Workflow] {
		probs = append(probs, fmt.Sprintf("%s is a file the lacquer writes — if it ran this target a "+
			"selector would name it, and the next sync overwrites whatever was added by hand", d.Workflow))
	}
	if !mentions(text, d.Target) {
		probs = append(probs, fmt.Sprintf("%s never names %q outside a comment", d.Workflow, d.Target))
	}
	if !runsTests(text) {
		probs = append(probs, fmt.Sprintf("%s contains no test invocation (an xcodebuild test / "+
			"build-for-testing / -only-testing: command, or `swift test`)", d.Workflow))
	}
	if events := triggers(text); !anyAuto(events) {
		probs = append(probs, fmt.Sprintf("%s is not triggered by a code change: its `on:` block is %s, "+
			"so nothing runs this target on a pull request", d.Workflow, describe(events)))
	}
	return probs
}

// mentions reports whether the target is named on a line that is not a comment.
//
// Substring rather than structure, and the comment filter is the whole of the
// rigour: it proves the name appears somewhere a runner could read, not that it
// is the `-only-testing:` selector. What it does buy is that a declaration
// cannot be satisfied by writing the target's name in a `#` comment in an
// otherwise unrelated workflow — which is the one way somebody would satisfy it
// without meaning to lie.
func mentions(body, target string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, target) {
			return true
		}
	}
	return false
}

// runsTests reports whether the workflow contains a command that runs tests.
//
// Continuation lines are joined first, because the real shape is
//
//	xcodebuild test \
//	  -scheme "…" \
//	  -only-testing:"…"
//
// and a line-at-a-time search for "xcodebuild test" finds nothing in a file that
// is unambiguously running tests. Getting this wrong fails in the safe
// direction — the claim is not confirmed and the target stays reported — but a
// check that is wrong about the normal case is a check people route around.
func runsTests(body string) bool {
	for _, cmd := range commands(body) {
		if strings.Contains(cmd, "-only-testing:") ||
			strings.Contains(cmd, "swift test") ||
			strings.Contains(cmd, "fastlane scan") {
			return true
		}
		if strings.Contains(cmd, "xcodebuild") && testAction.MatchString(cmd) {
			return true
		}
	}
	return false
}

// commands splits a workflow into logical shell commands: comment lines dropped,
// backslash continuations joined onto the line they continue.
func commands(body string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasSuffix(t, "\\") {
			cur.WriteString(strings.TrimSuffix(t, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(t)
		out = append(out, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// triggers returns the event names in a workflow's top-level `on:` block.
//
// A shape read, not a YAML parse: find the unindented `on:` key, then take the
// keys or list items indented under it, stopping at the next unindented line.
// Both spellings the fleet uses are handled — the inline `on: [push,
// pull_request]` and the block form. Reading the block rather than grepping the
// file matters: `github.event_name == 'pull_request'` inside an `if:` is not a
// trigger, and a grep would count it as one.
func triggers(body string) []string {
	lines := strings.Split(body, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		key, rest, ok := strings.Cut(line, ":")
		// YAML 1.1 reads a bare `on` as the boolean true, so a workflow may
		// quote it. All three spellings mean the same block.
		if !ok || (key != "on" && key != `"on"` && key != "'on'" && key != "true") {
			continue
		}
		if inline := strings.TrimSpace(rest); inline != "" && !strings.HasPrefix(inline, "#") {
			for _, e := range strings.Split(strings.Trim(inline, "[]"), ",") {
				if e = strings.TrimSpace(e); e != "" {
					out = append(out, e)
				}
			}
			return out
		}
		// The level the events themselves sit at. Anything deeper is a
		// `branches:` / `paths:` filter nested under one of them.
		level := minIndent(lines[i+1:])
		for _, sub := range lines[i+1:] {
			t := strings.TrimSpace(sub)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if indentOf(sub) == 0 {
				return out // an unindented key ends the block
			}
			if indentOf(sub) > level {
				continue
			}
			t = strings.TrimPrefix(t, "- ")
			out = append(out, strings.TrimSpace(strings.Split(t, ":")[0]))
		}
		return out
	}
	return out
}

// indentOf counts the leading spaces (a tab counts as one) on a line.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// minIndent is the shallowest indent among the lines of a block — the level its
// own keys sit at.
func minIndent(lines []string) int {
	best := -1
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		n := indentOf(line)
		if n == 0 {
			break // out of the block
		}
		if best < 0 || n < best {
			best = n
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// anyAuto reports whether any event fires on a code change.
func anyAuto(events []string) bool {
	for _, e := range events {
		if autoEvents[e] {
			return true
		}
	}
	return false
}

// describe renders the trigger list for the report.
func describe(events []string) string {
	if len(events) == 0 {
		return "unreadable"
	}
	return "[" + strings.Join(events, ", ") + "]"
}

// Apply folds verified claims into a report: a confirmed claim's target leaves
// the uncovered list, an unconfirmed one's stays there, and every claim is
// recorded so Format can print it.
//
// A confirmed claim for a target NO selector was going to report is marked stale
// rather than accepted. That is the case where the managed workflow already runs
// the target and the declaration is a second answer to a question that was not
// asked — harmless today, misleading the day somebody reads it as the reason the
// target is covered.
func Apply(r Report, claims []Claim) Report {
	confirmed := map[string]bool{}
	uncovered := map[string]bool{}
	for _, t := range r.Uncovered {
		uncovered[t.Name] = true
	}
	for i := range claims {
		c := &claims[i]
		if c.Stale == "" && c.Confirmed && !uncovered[c.Target] {
			c.Stale = "a managed test selector already covers this target"
		}
		if c.Stale == "" && c.Confirmed {
			confirmed[c.Target] = true
		}
		switch {
		case c.Stale != "":
			r.Stale = append(r.Stale, *c)
		case c.Confirmed:
			r.Elsewhere = append(r.Elsewhere, *c)
		default:
			r.Unconfirmed = append(r.Unconfirmed, *c)
		}
	}
	var kept []Target
	for _, t := range r.Uncovered {
		if !confirmed[t.Name] {
			kept = append(kept, t)
		}
	}
	r.Uncovered = kept
	return r
}
