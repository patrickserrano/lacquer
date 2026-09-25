// Package ciwait blocks until a pull request's checks are all terminal and says
// which of four things happened: they passed, one failed, the ceiling hit while
// some were still running, or there were no checks at all. `lacquer wait pr` is
// the command; this is the logic, kept in one tested place because every agent
// that hand-rolls it gets it wrong.
//
// The one that motivated it: a watcher branched on `(.conclusion // "PENDING")`.
// jq's `//` substitutes for null and false, not for an empty string, and a check
// IN FLIGHT reports `conclusion: ""`. It printed FINAL and exited 0 while both
// test jobs were still running. So nothing here is derived from a conclusion
// until the check's own status says it is finished, and each way a wait can end
// is its own outcome with its own exit code.
//
// Every expensive failure in this repository is a state indistinguishable from
// working (see CLAUDE.md), and each design decision below is one of those:
//
//   - An empty rollup is NoChecks, never Passed. "No item is non-terminal" is
//     trivially true of zero items, so a loop keyed on it calls a PR that was
//     never tested green. That fails in the safe-LOOKING direction.
//   - Both node shapes are handled explicitly. A CheckRun has `status` and
//     `conclusion`; a StatusContext (a legacy commit status) has `state` and no
//     `status`. A predicate on `.status != "COMPLETED"` alone reads a PENDING
//     commit status as finished.
//   - A response gh did not give us is an error, not an empty list.
//   - A conclusion nobody recognises fails closed.
package ciwait

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Outcome is how a wait ended. Its exit code is part of the interface: agents
// branch on it, so 0-3 are fixed and must never be conflated.
type Outcome int

const (
	// Passed: every check is terminal and none failed. Skipped and neutral
	// checks do not fail a PR; skipped ones are named in the output.
	Passed Outcome = iota
	// Failed: at least one check failed. Normally every check is terminal; if
	// the ceiling hit first, the failure still decides and the checks still
	// running are abandoned (they are listed in the output).
	Failed
	// TimedOut: the ceiling hit while at least one check was still running and
	// NONE had failed. Not a failure, and not a pass: the answer is unknown.
	TimedOut
	// NoChecks: the PR has no checks. Never a pass — nothing tested it.
	NoChecks
	// Error: the wait itself could not be completed (gh missing or failing,
	// the PR closed or merged, an interrupt). The PR's state is unknown.
	Error
)

// ExitCode is the process exit status for the outcome.
func (o Outcome) ExitCode() int { return int(o) }

func (o Outcome) String() string {
	switch o {
	case Passed:
		return "passed"
	case Failed:
		return "failed"
	case TimedOut:
		return "timed_out"
	case NoChecks:
		return "no_checks"
	}
	return "error"
}

// ErrGHNotFound is what a Runner returns when the gh binary is not installed.
// It is not retried: a missing binary does not appear on the next poll.
var ErrGHNotFound = errors.New("gh not found on PATH")

// Runner runs `gh` with args and returns its stdout. On failure the error
// carries gh's stderr, so the message shown to the user is gh's own.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Options configures a Wait. Run, Sleep and Now exist so tests can drive a
// twenty-minute wait in microseconds against a scripted gh; the command leaves
// them nil.
type Options struct {
	PR   int
	Repo string // owner/name; "" lets gh infer it from the checkout

	Timeout  time.Duration // ceiling on the whole wait, across head changes
	Interval time.Duration // between polls
	// EmptyGrace is how long an empty rollup is re-checked before it is
	// reported as NoChecks. Workflows register a moment after a PR opens or a
	// commit lands, so the first reading of a fresh PR is often empty.
	EmptyGrace time.Duration
	// MaxErrors is how many consecutive failed gh calls end the wait as an
	// Error. A success resets the count.
	MaxErrors int

	Run   Runner
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
}

// Kind is which of the two rollup node shapes a check came from.
type Kind string

const (
	KindCheckRun      Kind = "check_run"
	KindStatusContext Kind = "status_context"
	KindUnknown       Kind = "unknown"
)

type verdict int

const (
	vRunning verdict = iota // not terminal
	vPass
	vNeutral // terminal, passes, but is not a success
	vSkipped
	vFail
)

// Check is one entry of the rollup, normalised across both node shapes.
type Check struct {
	Name     string
	Kind     Kind
	Terminal bool
	// Workflow and RunID say which workflow run a CheckRun belongs to (RunID 0
	// when detailsUrl does not carry one). They exist to spot a superseded run.
	Workflow string
	RunID    int64
	// Label is the lower-cased conclusion (or state) of a terminal check, and
	// the lower-cased status (or state) of one still running.
	Label       string
	Duration    time.Duration
	HasDuration bool
	URL         string

	v verdict
}

// Result is everything a wait learned. Checks is the last reading of the PR's
// current head commit.
type Result struct {
	Outcome Outcome
	PR      int
	Repo    string
	Head    string
	// URL is the PR's own URL as gh reported it ("" when gh gave none).
	URL string
	// State is the PR's state (OPEN, CLOSED, MERGED) on the last successful
	// reading; "" when gh never answered. It exists so a caller can tell an
	// Error caused by a PR that is no longer open from one where the state is
	// unknown, without reading Message.
	State   string
	Checks  []Check
	Elapsed time.Duration
	// Message explains an Error (and, for NoChecks, how long it looked).
	Message string
	// Superseded is entries ignored because a newer run of the same workflow
	// reported the same check on the same commit (a rerun, or a run cancelled by
	// the concurrency group when the PR was edited). Only the latest counts, as
	// on GitHub's own checks tab; they are listed so nothing is hidden.
	Superseded []Check
	// HeadChanges records each "old -> new" head commit seen mid-wait.
	HeadChanges []string
}

func (r Result) filter(v verdict) []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.v == v {
			out = append(out, c)
		}
	}
	return out
}

// Failed is the terminal checks that failed, in rollup order.
func (r Result) Failed() []Check { return r.filter(vFail) }

// Running is the checks that were not terminal at the last reading.
func (r Result) Running() []Check { return r.filter(vRunning) }

// Skipped is the checks that concluded skipped.
func (r Result) Skipped() []Check { return r.filter(vSkipped) }

// node is one statusCheckRollup element. It carries the fields of BOTH shapes;
// which ones are populated is the shape.
type node struct {
	Typename     string `json:"__typename"`
	Name         string `json:"name"` // CheckRun
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"` // CheckRun: QUEUED, IN_PROGRESS, COMPLETED, ...
	Conclusion   string `json:"conclusion"`
	Context      string `json:"context"` // StatusContext
	State        string `json:"state"`   // StatusContext: PENDING, SUCCESS, FAILURE, ERROR
	StartedAt    string `json:"startedAt"`
	CompletedAt  string `json:"completedAt"`
	DetailsURL   string `json:"detailsUrl"`
	TargetURL    string `json:"targetUrl"`
}

// snapshot is one reading of the PR.
type snapshot struct {
	state      string
	head       string
	url        string
	checks     []Check
	superseded []Check
}

func parseSnapshot(out []byte, now time.Time) (snapshot, error) {
	var raw struct {
		State      string  `json:"state"`
		HeadRefOid string  `json:"headRefOid"`
		URL        string  `json:"url"`
		Rollup     *[]node `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return snapshot{}, fmt.Errorf("unreadable gh output: %w", err)
	}
	// An absent or null rollup is not an empty one. Reading "gh told us nothing"
	// as "the PR has no checks" would report a network hiccup as NoChecks — and
	// reading it as "no failures" would report it as green.
	if raw.Rollup == nil {
		return snapshot{}, errors.New("gh output has no statusCheckRollup")
	}
	s := snapshot{state: raw.State, head: raw.HeadRefOid, url: raw.URL}
	all := make([]Check, 0, len(*raw.Rollup))
	for _, n := range *raw.Rollup {
		all = append(all, classify(n, now))
	}
	s.checks, s.superseded = dropSuperseded(all)
	return s, nil
}

var runIDRe = regexp.MustCompile(`/runs/(\d+)`)

func runID(url string) int64 {
	if m := runIDRe.FindStringSubmatch(url); m != nil {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return id
		}
	}
	return 0
}

// dropSuperseded keeps, for each (workflow, check name), only the entries of the
// newest run. The rollup lists every run on the commit, so a PR edited while CI
// ran carries a cancelled `test` from the run the concurrency group killed
// beside the latest run's real one, and reading both calls a PR whose latest
// `test` passed FAILED. Deliberately narrow: only CheckRuns whose runs are
// known and DIFFER are collapsed. Two same-named jobs in one run, or entries
// with no run id, are all kept, so this can hide nothing it cannot account for.
func dropSuperseded(all []Check) (kept, superseded []Check) {
	newest := map[string]int64{}
	key := func(c Check) string { return c.Workflow + "\x00" + c.Name }
	for _, c := range all {
		if c.Kind == KindCheckRun && c.RunID != 0 && c.RunID > newest[key(c)] {
			newest[key(c)] = c.RunID
		}
	}
	for _, c := range all {
		if c.Kind == KindCheckRun && c.RunID != 0 && c.RunID < newest[key(c)] {
			superseded = append(superseded, c)
			continue
		}
		kept = append(kept, c)
	}
	return kept, superseded
}

// classify is the predicate the whole package exists to get right.
func classify(n node, now time.Time) Check {
	switch {
	case n.Typename == "CheckRun" || (n.Typename == "" && (n.Status != "" || n.Name != "")):
		return classifyRun(n, now)
	case n.Typename == "StatusContext" || (n.Typename == "" && n.State != ""):
		return classifyContext(n, now)
	}
	// A shape we cannot read is never terminal: waiting on it ends in a
	// timeout, which is loud. Counting it done could end in a false green.
	return Check{Name: "(unrecognised node " + n.Typename + ")", Kind: KindUnknown, Label: "unrecognised", v: vRunning}
}

func classifyRun(n node, now time.Time) Check {
	c := Check{Name: n.Name, Kind: KindCheckRun, URL: n.DetailsURL, Workflow: n.WorkflowName, RunID: runID(n.DetailsURL)}
	c.Duration, c.HasDuration = span(n.StartedAt, n.CompletedAt, now)
	// Terminal is the status alone. The conclusion of a check in flight is ""
	// (not null), which is exactly what a `// default` misses.
	if !strings.EqualFold(n.Status, "COMPLETED") {
		c.Label = strings.ToLower(n.Status)
		if c.Label == "" {
			c.Label = "unknown"
		}
		c.v = vRunning
		return c
	}
	c.Terminal = true
	c.Label = strings.ToLower(n.Conclusion)
	switch strings.ToUpper(n.Conclusion) {
	case "SUCCESS":
		c.v = vPass
	case "NEUTRAL":
		c.v = vNeutral
	case "SKIPPED":
		c.v = vSkipped
	case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE", "STALE":
		c.v = vFail
	default:
		// Completed with a conclusion we do not know, or none at all. Fail
		// closed: an unknown is not a success.
		c.v = vFail
		if c.Label == "" {
			c.Label = "no-conclusion"
		}
	}
	return c
}

func classifyContext(n node, now time.Time) Check {
	c := Check{Name: n.Context, Kind: KindStatusContext, URL: n.TargetURL, Label: strings.ToLower(n.State)}
	// A commit status carries `state` and no `status`: keying on `.status`
	// would see "" and call a PENDING one finished.
	switch strings.ToUpper(n.State) {
	case "SUCCESS":
		c.Terminal, c.v = true, vPass
	case "FAILURE", "ERROR":
		c.Terminal, c.v = true, vFail
	default: // PENDING, EXPECTED, or anything new
		c.v = vRunning
		if c.Label == "" {
			c.Label = "unknown"
		}
	}
	if !c.Terminal {
		c.Duration, c.HasDuration = span(n.StartedAt, "", now)
	}
	return c
}

// span is how long a check took, or has been running. The zero time GitHub
// reports for a check that has not started ("0001-01-01T00:00:00Z") is no time.
func span(started, completed string, now time.Time) (time.Duration, bool) {
	s, ok := parseTime(started)
	if !ok {
		return 0, false
	}
	end, ok := parseTime(completed)
	if !ok {
		end = now
	}
	if d := end.Sub(s); d >= 0 {
		return d.Round(time.Second), true
	}
	return 0, false
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.Year() < 2000 {
		return time.Time{}, false
	}
	return t, true
}

func allTerminal(cs []Check) bool {
	for _, c := range cs {
		if !c.Terminal {
			return false
		}
	}
	return true
}

// signature identifies a reading well enough to tell whether two consecutive
// ones agree.
func signature(cs []Check) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = string(c.Kind) + "|" + c.Name + "|" + c.Label
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// Wait polls the PR until it reaches one of the outcomes. It never returns
// Passed for an empty or unread rollup, and never returns before every check
// present in two consecutive readings is terminal.
func Wait(ctx context.Context, o Options) Result {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleep
	}
	if o.MaxErrors < 1 {
		o.MaxErrors = 1
	}
	start := o.Now()
	deadline := start.Add(o.Timeout)
	res := Result{PR: o.PR, Repo: o.Repo}
	finish := func(out Outcome, msg string) Result {
		res.Outcome, res.Message, res.Elapsed = out, msg, o.Now().Sub(start)
		return res
	}

	args := []string{"pr", "view"}
	if o.Repo != "" {
		args = append(args, "-R", o.Repo)
	}
	args = append(args, fmt.Sprint(o.PR), "--json", "state,headRefOid,statusCheckRollup,url")

	var (
		haveHead   bool
		emptySince time.Time // zero unless the current head's rollup is empty
		prevSig    string    // signature of the previous all-terminal reading
		consecErrs int
		lastErr    error
		lastOK     bool // did the most recent poll succeed
	)
	for {
		if err := ctx.Err(); err != nil {
			return finish(Error, "interrupted: "+err.Error())
		}
		out, err := o.Run(ctx, args...)
		var snap snapshot
		if err == nil {
			snap, err = parseSnapshot(out, o.Now())
		}
		now := o.Now()

		if err != nil {
			lastOK = false
			if errors.Is(err, ErrGHNotFound) {
				return finish(Error, "gh not found on PATH: install the GitHub CLI (https://cli.github.com) and run `gh auth login`")
			}
			if ctx.Err() != nil {
				return finish(Error, "interrupted: "+ctx.Err().Error())
			}
			lastErr = err
			consecErrs++
			if consecErrs >= o.MaxErrors {
				return finish(Error, fmt.Sprintf("gh failed %d times in a row, giving up (the PR's state is UNKNOWN, not passing): %v", consecErrs, lastErr))
			}
		} else {
			lastOK, consecErrs = true, 0
			res.State = snap.state
			if snap.url != "" {
				res.URL = snap.url
			}
			if snap.state != "OPEN" {
				st := snap.state
				if st == "" {
					st = "in an unknown state"
				}
				res.Head, res.Checks = snap.head, snap.checks
				return finish(Error, fmt.Sprintf("PR #%d is %s; not waiting on the checks of a PR that is no longer open", o.PR, st))
			}
			if haveHead && snap.head != res.Head {
				// A push mid-wait means new checks. What was true of the old
				// commit is not the PR's answer, so start over on the new one.
				// The ceiling is NOT reset: it bounds the whole wait.
				res.HeadChanges = append(res.HeadChanges, short(res.Head)+" -> "+short(snap.head))
				emptySince, prevSig = time.Time{}, ""
			}
			haveHead = true
			res.Head, res.Checks, res.Superseded = snap.head, snap.checks, snap.superseded

			switch {
			case len(snap.checks) == 0:
				prevSig = ""
				if emptySince.IsZero() {
					emptySince = now
				}
				if now.Sub(emptySince) >= o.EmptyGrace || !now.Before(deadline) {
					return finish(NoChecks, fmt.Sprintf("the rollup was empty for %s", now.Sub(emptySince).Round(time.Second)))
				}
			case allTerminal(snap.checks):
				emptySince = time.Time{}
				// The first all-terminal reading can be one taken before a
				// slower workflow registered. It has to hold for a second poll.
				sig := signature(snap.checks)
				if sig == prevSig {
					return finish(outcomeOf(snap.checks), "")
				}
				prevSig = sig
			default:
				emptySince, prevSig = time.Time{}, ""
			}
		}

		if !now.Before(deadline) {
			if !lastOK {
				return finish(Error, fmt.Sprintf("the ceiling hit while gh was failing (the PR's state is UNKNOWN): %v", lastErr))
			}
			if oc := outcomeOf(res.Checks); allTerminal(res.Checks) || oc == Failed {
				// Terminal but not yet confirmed by a second poll: the data is
				// complete, so report it rather than call it a timeout. And a
				// known failure is decisive even with checks still running: CI
				// cannot become green from there, so it is Failed, not "we do
				// not know yet". Whoever branches on the exit code must not have
				// to re-derive that from the text.
				return finish(oc, "")
			}
			return finish(TimedOut, "")
		}
		d := o.Interval
		if left := deadline.Sub(now); left < d {
			d = left
		}
		if err := o.Sleep(ctx, d); err != nil {
			return finish(Error, "interrupted: "+err.Error())
		}
	}
}

func outcomeOf(cs []Check) Outcome {
	for _, c := range cs {
		if c.v == vFail {
			return Failed
		}
	}
	return Passed
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Reading is one look at a pull request's current head commit: what Wait sees on
// each poll, without the waiting. `lacquer ci-round` uses it to learn what the
// last round reported, so that the definition of "a check failed" is the one
// place this package already gets right and is not derived a second time.
type Reading struct {
	State      string // OPEN, CLOSED, MERGED
	Head       string
	Checks     []Check
	Superseded []Check
}

func (r Reading) result() Result { return Result{Checks: r.Checks} }

// Failed is the terminal checks that failed. Like Wait, a failure is decisive
// even while other checks are still running.
func (r Reading) Failed() []Check { return r.result().Failed() }

// Running is the checks that are not terminal yet.
func (r Reading) Running() []Check { return r.result().Running() }

// Look reads the PR once. A response gh did not give, or one without a rollup,
// is an error, never an empty reading.
func Look(ctx context.Context, run Runner, repo string, pr int, now time.Time) (Reading, error) {
	args := []string{"pr", "view"}
	if repo != "" {
		args = append(args, "-R", repo)
	}
	args = append(args, fmt.Sprint(pr), "--json", "state,headRefOid,statusCheckRollup")
	out, err := run(ctx, args...)
	if err != nil {
		return Reading{}, err
	}
	snap, err := parseSnapshot(out, now)
	if err != nil {
		return Reading{}, err
	}
	return Reading{State: snap.state, Head: snap.head, Checks: snap.checks, Superseded: snap.superseded}, nil
}
