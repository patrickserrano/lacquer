// Package protection compares what a repository's branch protection REQUIRES
// against what its workflows can actually POST.
//
// GitHub counts a `skipped` check conclusion as SATISFYING a required status
// check. So a required context posted by a job that skips is not a gate at all:
// "verified and passing" and "never ran" are the same green tick on the merge
// button. The only context in a lacquer-managed CI that cannot be satisfied by
// a skip is the aggregate — `CI OK` is `if: always()`, so it always runs, always
// reports, and reads `needs.*.result` to decide. Every other job name in the
// workflow is skippable by construction (path filters, `needs` short-circuits,
// matrix legs that do not render).
//
// Measured across 17 non-archived repositories in the fleet this was built for:
//
//   - 14 require exactly `CI OK`. That is the intended shape.
//   - dailybread requires `Build (Release), Lint + Test`. Its project-owned
//     ios-ci.yml posts `Lint` and `Test` as SEPARATE contexts and never posts
//     `Lint + Test` at all; only merge-gate.yml does, as a conditional skip leg.
//     On PR #482 — an entitlement change — BOTH required checks were satisfied
//     purely by skips, and `CI OK` was not required at all. Nothing ran, and
//     nothing said so.
//   - Windsock and dailybread-image-proxy require NOTHING. That is the same
//     defect at zero, and it is reported as a finding rather than a pass: a
//     branch with no required context is a branch where every check is advisory.
//
// # Why this is not part of `lacquer audit`
//
// `audit` is local and offline: it reads the project's files and the lacquer's
// own, and it runs inside CI on every PR. Branch protection is in no file.
// Reading it needs the GitHub API, an authenticated `gh`, and network — and
// three properties of that make it wrong to bolt onto the audit path.
//
//   - GET /repos/{o}/{r}/branches/{b}/protection requires ADMIN permission on
//     the repository. The `GITHUB_TOKEN` a workflow gets has no admin scope, so
//     a CI-side job could not read it without a long-lived PAT held as a secret
//     in every repo — new standing credentials in seventeen places, to check a
//     setting that changes about once a year.
//   - A repository cannot usefully check its own branch protection anyway. If
//     protection is misconfigured such that nothing real is required, then the
//     job reporting so is itself not required, and the PR merges over it. The
//     check has to run somewhere its answer cannot be ignored by the thing it
//     judges — which is an operator's machine, sweeping the fleet.
//   - `audit` failing because a laptop was offline, or because `gh` was logged
//     out, would teach people that its exit codes are weather. Offline is a
//     normal state for the audit; it is a fatal one for this question.
//
// So this is its own opt-in command (`lacquer protection`), which reaches the
// network deliberately, and which reports "could not look" as its own answer
// with its own exit code. A personal account on GitHub Free cannot use branch
// protection on a private repo at all, and the API answers 403 — verified
// against patrickserrano/dailybread-image-proxy before its org transfer. A 403
// is NOT a pass and is not a finding either; collapsing it into either one would
// make this check an instance of the defect it exists to catch.
package protection

import (
	"fmt"
	"sort"
	"strings"
)

// Gate is the aggregate job name every lacquer profile's ci.yml posts, and the
// one context branch protection should require.
//
// Not configurable, because it is not a project's choice: all three shipped
// profiles (profiles/ios/workflows/ci.yml, profiles/web/workflows/ci.yml,
// profiles/supabase/workflows/ci.yml) name their `ci-ok` job exactly this, and
// each is `if: always()` with `needs` covering every real job. A project that
// renamed it would also have to teach its branch-protection settings the new
// name, so the constant is the convention rather than a default.
const Gate = "CI OK"

// Verdict is the answer for one repository.
type Verdict string

const (
	// Protected is the passing state: a merge waits for something that always
	// runs. Reached two ways — the aggregate gate is required (the managed
	// shape, 14 of 17 repos), or some required context is posted by a job
	// nothing can skip (the lacquer's own repository, which requires `test`).
	Protected Verdict = "protected"
	// AllRequiredSkippable means protection exists and requires contexts, but
	// every one of them can be satisfied by a skip: the aggregate is not among
	// them, and no required context is posted by a job that always reports. The
	// required set can therefore go green without any of the work having
	// happened. dailybread.
	AllRequiredSkippable Verdict = "all-required-skippable"
	// NothingRequired means protection exists and requires no status check at
	// all. Every check on the PR is advisory.
	NothingRequired Verdict = "nothing-required"
	// Unprotected means the branch has no protection and no ruleset covers it.
	Unprotected Verdict = "unprotected"
	// Unavailable means the check could not be performed: no `gh`, not
	// authenticated, no network, or a 403 from an account that cannot read (or
	// have) branch protection. Deliberately NOT a pass and NOT a finding —
	// "could not look" and "looked and found nothing wrong" are different
	// answers, and this package never collapses them.
	Unavailable Verdict = "unavailable"
)

// Requirements is what a branch's protection demands, as read from the API.
//
// Source names where it came from, because classic branch protection and
// repository rulesets are separate systems behind separate endpoints, and a
// repo can be governed by either or both. Reporting "unprotected" off the
// classic endpoint alone would be wrong for any repo protected by a ruleset.
type Requirements struct {
	// Protected is true when classic protection exists OR a ruleset applies.
	Protected bool
	// Contexts is the union of required status-check contexts from both
	// systems, sorted and deduplicated.
	Contexts []string
	// Source is a short note of which system(s) answered.
	Source string
}

// Job is one job a local workflow declares, and therefore one context — or one
// family of contexts — the repository can post.
type Job struct {
	// Workflow is the workflow file's base name.
	Workflow string
	// Name is the job's `name:`, falling back to its key when it has none —
	// which is exactly how GitHub names the check run.
	Name string
	// Conditional is true when the job carries a top-level `if:`. This is the
	// property that makes requiring it unsafe: when the `if:` is false GitHub
	// posts the context with conclusion `skipped`, and a skipped conclusion
	// SATISFIES branch protection.
	Conditional bool
	// AlwaysReports is true when nothing in the workflow can skip this job: it
	// has no `if:` (or the literal `always()`), and neither does anything it
	// `needs`, transitively. Requiring such a context is a REAL gate even when
	// it is not the aggregate — which is why this field exists rather than the
	// verdict keying on the gate's name alone.
	//
	// Found by running the first version of this against the lacquer's own
	// repository, which requires `test` — a job with no `if:` and no `needs:`,
	// so it runs on every pull request. Reporting that as "requires the wrong
	// thing" would have been a confident false accusation against a repo doing
	// nothing wrong, in the very first real invocation.
	AlwaysReports bool
	// Matrix is true when the job carries `strategy.matrix`. GitHub then posts
	// one context per leg, named "<Name> (<leg values>)" — so a required context
	// beginning "<Name> (" is posted by this job even though it never equals its
	// name.
	Matrix bool
	// Dynamic is true when the name interpolates an expression, so what it posts
	// cannot be known from the file. Recorded rather than guessed at.
	Dynamic bool
}

// Workflows is everything the local checkout says about what it posts.
type Workflows struct {
	Jobs []Job
	// Unreadable are workflow files that could not be parsed. They are carried
	// into the report rather than dropped: a file this package failed to read is
	// not evidence that the repository posts nothing from it, and the difference
	// decides whether "that required context is posted by nothing here" is a
	// measurement or a guess.
	Unreadable []string
}

// Report is the comparison for one repository.
type Report struct {
	Repo   string
	Branch string

	Verdict Verdict
	// Required is what branch protection demands, sorted.
	Required []string
	// Source records which protection system answered (see Requirements).
	Source string
	// Posters maps each required context to the local jobs that can post it. A
	// context with no posters is one nothing in this checkout reports — either
	// it hangs the PR forever, or something outside the repository fills it in.
	Posters map[string][]Job
	// Unskippable are required contexts posted by a job that always reports, for
	// a repository that passes WITHOUT requiring the aggregate. Named in the
	// output, because "OK" then rests on a different argument than it does for
	// the fourteen repositories that require `CI OK`, and a reader should be
	// able to see which argument they got.
	Unskippable []string
	// Ambiguous are jobs whose names are expression-templated, so what they post
	// cannot be read off the file.
	//
	// They exist to QUALIFY the strongest claim this report makes. "Posted by
	// NOTHING in this checkout" is an assertion about every job in the
	// repository, and one templated name is enough to make it unprovable — so
	// where they exist, the report says what it actually knows instead.
	Ambiguous []Job
	// GatePostedBy names the workflow declaring the aggregate gate job, or "" if
	// this checkout has no such job. It splits the remedy in two: a repo that
	// renders the gate only needs its protection re-pointed, a repo that does
	// not has to re-adopt the managed workflow first.
	GatePostedBy string
	// Unreadable is carried from Workflows (see there).
	Unreadable []string
	// Reason is why the check could not be performed, for Unavailable only.
	Reason string
}

// Compare produces the report for one repository.
//
// err is the fetch error, threaded in rather than handled by the caller, so
// there is exactly ONE place where a failed lookup becomes a verdict — and it is
// the same place that knows a failed lookup is not a pass.
func Compare(repo, branch string, req Requirements, err error, w Workflows) Report {
	r := Report{
		Repo:       repo,
		Branch:     branch,
		Required:   append([]string(nil), req.Contexts...),
		Source:     req.Source,
		Posters:    map[string][]Job{},
		Unreadable: w.Unreadable,
	}
	sort.Strings(r.Required)

	for _, j := range w.Jobs {
		if j.Dynamic {
			r.Ambiguous = append(r.Ambiguous, j)
		}
		if j.Name == Gate && r.GatePostedBy == "" {
			r.GatePostedBy = j.Workflow
		}
	}
	for _, c := range r.Required {
		for _, j := range w.Jobs {
			if posts(j, c) {
				r.Posters[c] = append(r.Posters[c], j)
			}
		}
	}

	switch {
	case err != nil:
		r.Verdict = Unavailable
		r.Reason = err.Error()
	case !req.Protected:
		r.Verdict = Unprotected
	case len(req.Contexts) == 0:
		r.Verdict = NothingRequired
	// The aggregate is required. This is decided on the NAME alone, without
	// consulting the checkout, because `CI OK` is the lacquer's own `if:
	// always()` job by construction — and a stale, absent, or not-yet-synced
	// local checkout must not be able to turn a correctly-protected repository
	// into a finding.
	case contains(req.Contexts, Gate):
		r.Verdict = Protected
	default:
		// Not the managed shape, but not necessarily vacuous: a required context
		// posted by a job nothing can skip is a real gate under a different name.
		// The lacquer's own repository is this case (it requires `test`), and
		// calling it a finding would have been noise in the first real run.
		for _, c := range r.Required {
			for _, j := range r.Posters[c] {
				if j.AlwaysReports {
					r.Verdict = Protected
					r.Unskippable = append(r.Unskippable, c)
					// One unskippable poster settles this context; two workflows
					// declaring the same job name would otherwise list it twice.
					break
				}
			}
		}
		if r.Verdict != Protected {
			r.Verdict = AllRequiredSkippable
		}
	}
	return r
}

// Unreachable is the report for a repository that could not even be identified
// — no `repo` in the roster and no origin remote in the checkout.
//
// It exists so such an entry still occupies a LINE in the output. Dropping it
// would let a roster of seventeen print sixteen verdicts under a heading that
// says seventeen were checked, which is precisely the shape of defect this
// command was written to find.
func Unreachable(name string, err error) Report {
	return Report{Repo: name, Branch: "?", Verdict: Unavailable, Reason: err.Error(), Posters: map[string][]Job{}}
}

// posts reports whether job j can post the status context named ctx.
//
// Exact match, plus the matrix spelling: GitHub names a matrixed job's check
// runs "<job name> (<leg values>)", so `Test (Free)` and `Test (Pro)` are both
// posted by a job named `Test`. A job whose NAME is dynamic posts nothing as far
// as this is concerned — it might post ctx and it might not, and the one thing
// this package must not do is report a guess as a measurement. Keeping such a
// job out of Posters is what lets the report QUALIFY its "posted by nothing"
// sentence (see Report.Ambiguous and unposted) rather than assert one it cannot
// support.
func posts(j Job, ctx string) bool {
	if j.Dynamic {
		return false
	}
	if j.Name == ctx {
		return true
	}
	return j.Matrix && strings.HasPrefix(ctx, j.Name+" (") && strings.HasSuffix(ctx, ")")
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}

// Blocking counts reports that found a real defect. Unavailable is excluded on
// purpose: it is counted by Unchecked, and conflating the two would let a
// logged-out `gh` read as a clean fleet.
func Blocking(rs []Report) int {
	n := 0
	for _, r := range rs {
		switch r.Verdict {
		case AllRequiredSkippable, NothingRequired, Unprotected:
			n++
		}
	}
	return n
}

// Unchecked counts repositories no verdict could be reached for.
func Unchecked(rs []Report) int {
	n := 0
	for _, r := range rs {
		if r.Verdict == Unavailable {
			n++
		}
	}
	return n
}

// Format renders the sweep. Every repository gets a line, including the passing
// ones: this command is run deliberately and rarely, and a report that printed
// only findings would give an operator no way to tell "14 checked, all correct"
// apart from "14 silently skipped" — which is the defect this whole package is
// about, wearing the report's clothes.
func Format(rs []Report) string {
	var b strings.Builder
	b.WriteString("branch protection vs the contexts CI posts:\n")
	for _, r := range rs {
		fmt.Fprintf(&b, "  %s@%s  %s\n", r.Repo, r.Branch, headline(r))
		b.WriteString(detail(r))
	}
	fmt.Fprintf(&b, "\n%d checked, %d finding(s), %d could not be checked.\n",
		len(rs), Blocking(rs), Unchecked(rs))
	return b.String()
}

func headline(r Report) string {
	switch r.Verdict {
	case Protected:
		if len(r.Unskippable) > 0 {
			return "OK — requires " + strings.Join(r.Required, ", ") +
				" (not " + Gate + ", but " + strings.Join(r.Unskippable, ", ") + " cannot be skipped)"
		}
		return "OK — requires " + strings.Join(r.Required, ", ")
	case AllRequiredSkippable:
		return "EVERY REQUIRED CHECK IS SKIPPABLE — " + strings.Join(r.Required, ", ")
	case NothingRequired:
		return "NO REQUIRED CHECK — protection exists and requires nothing"
	case Unprotected:
		return "UNPROTECTED — no branch protection, no ruleset"
	default:
		return "COULD NOT CHECK — " + r.Reason
	}
}

func detail(r Report) string {
	var b strings.Builder
	switch r.Verdict {
	case Unavailable:
		// The remedy is the whole point of keeping this out of both other
		// buckets: an operator who reads "could not check" as "fine" has been
		// told the opposite of what was measured.
		b.WriteString("      This is NOT a pass. Nothing was learned about this repository.\n")
		b.WriteString("      A personal account on GitHub Free cannot use branch protection on a private\n")
		b.WriteString("      repo and the API answers 403; reading protection otherwise needs ADMIN on\n")
		b.WriteString("      the repo. Check `gh auth status`, or read the setting by hand.\n")
	case AllRequiredSkippable:
		for _, c := range r.Required {
			ps := r.Posters[c]
			if len(ps) == 0 {
				fmt.Fprintf(&b, "      %s — %s\n", c, unposted(r))
				continue
			}
			for _, p := range ps {
				note := p.Workflow
				if p.Conditional {
					// The mechanism, named. A conditional job is not a weaker
					// gate than an unconditional one; it is not a gate.
					note += " (conditional — a skip SATISFIES it)"
				}
				fmt.Fprintf(&b, "      %s — posted by %s\n", c, note)
			}
		}
		b.WriteString(remedy(r))
	case NothingRequired, Unprotected:
		b.WriteString(remedy(r))
	}
	for _, u := range r.Unreadable {
		fmt.Fprintf(&b, "      note: %s could not be parsed, so what it posts is unknown\n", u)
	}
	return b.String()
}

// unposted is how the report describes a required context no local job posts.
//
// "Posted by NOTHING in this checkout" is the strongest sentence in this output
// and the one most likely to send somebody deleting a branch-protection rule, so
// it is only said when it was actually established. A templated job name or a
// workflow that would not parse means some of the evidence was never read, and
// the report says so rather than rounding it down to a confident claim — the
// same distinction the verdicts themselves keep between "could not look" and
// "looked and found nothing".
func unposted(r Report) string {
	var why []string
	for _, j := range r.Ambiguous {
		why = append(why, j.Workflow+" job name is templated ("+j.Name+")")
	}
	for _, u := range r.Unreadable {
		why = append(why, u+" would not parse")
	}
	if len(why) == 0 {
		return "posted by NOTHING in this checkout"
	}
	return "posted by nothing this could identify — " + strings.Join(why, "; ")
}

func remedy(r Report) string {
	if r.GatePostedBy == "" {
		return "      No job named " + Gate + " exists in this checkout, so pointing protection at it\n" +
			"      would only hang every PR. Re-adopt the managed ci.yml (`lacquer sync`) first,\n" +
			"      then require " + Gate + " and nothing else.\n"
	}
	return "      " + r.GatePostedBy + " posts " + Gate + ", which is `if: always()` and reads every\n" +
		"      job's result. Require exactly that and drop the rest: every other name is\n" +
		"      skippable, and GitHub counts a skipped check as SATISFYING a requirement.\n"
}
