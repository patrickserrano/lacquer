// Package cirounds caps how many rounds of CI an agent may spend on one pull
// request, and enforces it from the PR itself. `lacquer ci-round` is the command.
//
// A round is a push that starts CI. The first is the push that opens the PR; a
// second needs a named failing check or a stated review request
// (the cap is on guessing, not on rounds); a third is refused, and the refusal is
// a report, never a failure of the PR: it comments on the PR with what is still
// failing, raises an inbox ACTION, and pushes nothing. Re-running CI is the most
// expensive reflex an agent has, and this repository's answer to "an unbounded
// fan-out burned 45% of a weekly plan" is that a third attempt is a guess, and
// the harness, not persona prose, is what stops it (issue #422).
//
// # Where the count lives
//
// On the PR, as comments, one per event, each opening with an HTML-comment
// marker holding one JSON object (see Entry) beside prose saying the same thing.
// Not in a session, not on a disk: a session dies and a fresh one on an exhausted
// PR is blocked all the same; a file in a checkout is one agent's, and two
// agents in sequence would get two budgets. One comment per event (append-only)
// rather than one edited in place, because a lost update on an edited comment
// silently refunds a round, whereas two racing appends are ordered by GitHub and
// the later one is told it lost.
//
// Unknown PR heads spend an unrecorded round; authorship cannot be inferred
// from the shared account. Only an explicit reset starts a fresh budget.
// Review-requested changes spend the same budget without requiring failed CI.
package cirounds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/ciwait"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

// Exit codes of `lacquer ci-round`. They start at 10 so none can be mistaken for
// `lacquer wait pr`'s 0-4, which an agent has just been reading.
const (
	CodeGranted      = 0  // a round is recorded (or already was): push this commit
	CodeExhausted    = 10 // out of rounds: a report, not a failure. Do not push
	CodeReason       = 11 // missing reset reason or invalid failure-round reason
	CodeNothingToFix = 12 // the last round reported no failure: nothing to spend a round on
	CodeUnavailable  = 13 // the tool could not tell (gh failing, PR closed, bad input). No push granted
)

// minReasonWords is what separates a reason from a label. "lint" names a check
// and says nothing.
const minReasonWords = 4

var shaRe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// Options configures a call. Run and Now exist so tests drive a scripted gh; the
// command leaves Now nil and passes ciwait.GH.
type Options struct {
	PR   int
	Repo string // owner/name; "" lets gh infer it from the checkout
	Cap  int    // rounds allowed per budget (config: [project].ci_round_cap)
	// SHA is the commit about to be pushed, in full.
	SHA string
	// Reason is required from round 2 on: what changed, naming a failing check.
	Reason string
	// Review replaces Reason for a review-requested change, even on green CI.
	Review string
	// Inbox is the operator's inbox file; "" means none is configured.
	Inbox string

	Run ciwait.Runner
	Now func() time.Time
}

// Result is what a call decided and the text to show.
type Result struct {
	Code  int
	Text  string
	Round int // the round granted (Begin) or the last spent (Status)
	Spent int // rounds spent in the current budget
	Cap   int
}

// Begin asks for a round. It is run BEFORE the push it covers, with the SHA of
// the commit about to be pushed: it either records that commit as a round and
// returns CodeGranted, or refuses and records nothing (except the stop notice
// and any newly observed unrecorded push).
func Begin(ctx context.Context, o Options) Result {
	if o.Now == nil {
		o.Now = time.Now
	}
	if !shaRe.MatchString(o.SHA) {
		return unavailable(o, fmt.Sprintf("%q is not a full commit SHA; pass the commit you are about to push (default: git rev-parse HEAD)", o.SHA))
	}
	if o.Cap < 1 {
		return unavailable(o, fmt.Sprintf("the round cap must be at least 1, got %d", o.Cap))
	}
	if o.Review != "" && (oneLine(o.Review) == "" || o.Reason != "") {
		return unavailable(o, "--review must state the requested change and reviewer, and replaces --reason")
	}
	rd, ledger, res := load(ctx, o)
	if res != nil {
		return *res
	}
	now := o.Now().UTC().Format(time.RFC3339)

	if r, ok := ledger.roundFor(o.SHA); ok && r.Kind != KindUnrecorded {
		return Result{Code: CodeGranted, Round: r.Round, Spent: len(ledger.Rounds), Cap: o.Cap,
			Text: fmt.Sprintf("Round %d of %d for PR #%d is already recorded for %s; nothing new was spent.\n", r.Round, o.Cap, o.PR, short(o.SHA))}
	}

	failing := names(rd.Failed())
	spent := len(ledger.Rounds)
	if spent >= o.Cap {
		return exhausted(ctx, o, ledger, rd, failing, now)
	}

	if ledger.Any && o.SHA == rd.Head {
		return Result{Code: CodeNothingToFix, Spent: len(ledger.Rounds), Cap: o.Cap,
			Text: "REFUSED: commit is already the PR's head; no new push was granted. Unrecorded pushes spend a round.\n"}
	}

	entry := Entry{V: 1, Kind: KindRound, Epoch: ledger.Epoch, Round: spent + 1, Cap: o.Cap, SHA: o.SHA, At: now}
	if o.Review != "" {
		entry.Kind, entry.Reason = KindReview, oneLine(o.Review)
	} else if spent > 0 {
		// The last round's answer has to be a failure, or there is nothing the
		// agent knows that would justify another one. A wait that timed out
		// (2), found no checks (3) or could not run (4) reports none: it spends
		// nothing and the agent escalates instead.
		if len(failing) == 0 {
			return nothingToFix(o, ledger, rd)
		}
		if msg := checkReason(o.Reason, failing); msg != "" {
			return Result{Code: CodeReason, Spent: spent, Cap: o.Cap, Text: reasonRefusal(o, spent, failing, msg)}
		}
		entry.Addressing, entry.Reason = failing, oneLine(o.Reason)
	}

	if _, err := post(ctx, o, renderRound(entry)); err != nil {
		return unavailable(o, "could not record the round on the PR: "+err.Error())
	}
	// Two sessions can read the ledger together. The comments are ordered by
	// GitHub, so re-read: the earlier comment for a round number owns it.
	after, err := readLedger(ctx, o)
	if err != nil {
		return unavailable(o, "the round WAS written to the PR but could not be verified: "+err.Error()+"\nRun `lacquer ci-round status "+fmt.Sprint(o.PR)+"` before pushing.")
	}
	if got, ok := after.roundFor(o.SHA); !ok || got.Round != entry.Round || got.Kind == KindUnrecorded || after.Epoch != entry.Epoch {
		return unavailable(o, fmt.Sprintf("another session took round %d of PR #%d first; this call did NOT get it. Do not push. Run `lacquer ci-round status %d` to read the ledger.", entry.Round, o.PR, o.PR))
	}
	return Result{Code: CodeGranted, Round: entry.Round, Spent: entry.Round, Cap: o.Cap, Text: granted(o, entry)}
}

// Status records any unknown head and reports the ledger: exit 0 if the agent may still
// ask for a round, CodeExhausted if not.
func Status(ctx context.Context, o Options) Result {
	if o.Cap < 1 {
		return unavailable(o, fmt.Sprintf("the round cap must be at least 1, got %d", o.Cap))
	}
	rd, ledger, res := load(ctx, o)
	if res != nil {
		return *res
	}
	var b strings.Builder
	spent := len(ledger.Rounds)
	counts := map[Kind]int{}
	for _, r := range ledger.Rounds {
		counts[r.Kind]++
	}
	fmt.Fprintf(&b, "PR #%d: %d/%d used (%d failure, %d review)", o.PR, spent, o.Cap, counts[KindRound], counts[KindReview])
	if counts[KindUnrecorded] > 0 {
		fmt.Fprintf(&b, " (%d unrecorded)", counts[KindUnrecorded])
	}
	if ledger.Epoch > 1 {
		fmt.Fprintf(&b, " (budget %d)", ledger.Epoch)
	}
	b.WriteString("\n")
	for _, r := range ledger.Rounds {
		fmt.Fprintf(&b, "  round %d  %s  %s  %s", r.Round, r.Kind, short(r.SHA), r.At)
		if r.Reason != "" {
			fmt.Fprintf(&b, "  %s", oneLine(r.Reason))
		}
		b.WriteString("\n")
	}
	code := CodeGranted
	if spent >= o.Cap {
		code = CodeExhausted
		fmt.Fprintf(&b, "EXHAUSTED: no further agent push. Still failing on %s: %s\n", short(rd.Head), failingText(names(rd.Failed())))
	} else {
		fmt.Fprintf(&b, "%d round(s) left.\n", o.Cap-spent)
	}
	return Result{Code: code, Spent: spent, Cap: o.Cap, Text: b.String()}
}

// Reset explicitly starts a fresh budget, preserving the audit trail.
func Reset(ctx context.Context, o Options) Result {
	if oneLine(o.Reason) == "" {
		return Result{Code: CodeReason, Cap: o.Cap, Text: "REFUSED: reset requires --reason explaining why a fresh budget is authorized.\n"}
	}
	if o.Cap < 1 {
		return unavailable(o, "the round cap must be at least 1")
	}
	rd, ledger, res := load(ctx, o)
	if res != nil {
		return *res
	}
	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}
	e := Entry{V: 1, Kind: KindReset, Epoch: ledger.Epoch + 1, Cap: o.Cap, SHA: rd.Head, At: now.UTC().Format(time.RFC3339), Reason: oneLine(o.Reason)}
	if _, err := post(ctx, o, renderReset(e)); err != nil {
		return unavailable(o, "could not record reset: "+err.Error())
	}
	return Result{Code: CodeGranted, Cap: o.Cap, Text: fmt.Sprintf("PR #%d: budget reset; 0/%d used. Reason: %s\n", o.PR, o.Cap, e.Reason)}
}

// load reads the PR and its ledger. A failure is CodeUnavailable: a gate that
// cannot see must not say "go ahead".
func load(ctx context.Context, o Options) (ciwait.Reading, Ledger, *Result) {
	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}
	rd, err := ciwait.Look(ctx, o.Run, o.Repo, o.PR, now)
	if err != nil {
		r := unavailable(o, "could not read the PR: "+err.Error())
		return ciwait.Reading{}, Ledger{}, &r
	}
	if rd.State != "OPEN" {
		r := unavailable(o, fmt.Sprintf("PR #%d is %s, not open", o.PR, rd.State))
		return ciwait.Reading{}, Ledger{}, &r
	}
	ledger, err := readLedger(ctx, o)
	if err != nil {
		r := unavailable(o, "could not read the round ledger on the PR: "+err.Error())
		return ciwait.Reading{}, Ledger{}, &r
	}
	// The first begin may register the push that opened the PR. Every other
	// unknown head must be charged, including a first observation by status.
	if !ledger.Known[rd.Head] && (ledger.Any || o.SHA != rd.Head) {
		e := Entry{V: 1, Kind: KindUnrecorded, Epoch: ledger.Epoch, Round: len(ledger.Rounds) + 1, Cap: o.Cap, SHA: rd.Head, At: now.UTC().Format(time.RFC3339)}
		if _, err := post(ctx, o, renderRound(e)); err != nil {
			r := unavailable(o, "could not record unrecorded push: "+err.Error())
			return rd, ledger, &r
		}
		ledger, err = readLedger(ctx, o)
		if err != nil || !ledger.Known[rd.Head] {
			r := unavailable(o, fmt.Sprintf("unrecorded push WAS written but could not be verified: %v", err))
			return rd, ledger, &r
		}
	}
	return rd, ledger, nil
}

func readLedger(ctx context.Context, o Options) (Ledger, error) {
	args := []string{"pr", "view"}
	if o.Repo != "" {
		args = append(args, "-R", o.Repo)
	}
	args = append(args, fmt.Sprint(o.PR), "--json", "comments")
	out, err := o.Run(ctx, args...)
	if err != nil {
		return Ledger{}, err
	}
	var raw struct {
		Comments *[]struct {
			ID                string `json:"id"`
			AuthorAssociation string `json:"authorAssociation"`
			Body              string `json:"body"`
			URL               string `json:"url"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Ledger{}, fmt.Errorf("unreadable gh output: %w", err)
	}
	// Absent is not empty: reading "gh told us nothing" as "no rounds spent"
	// would refund the whole budget.
	if raw.Comments == nil {
		return Ledger{}, errors.New("gh output has no comments field")
	}
	cs := make([]Comment, len(*raw.Comments))
	for i, c := range *raw.Comments {
		cs[i] = Comment{ID: c.ID, Association: c.AuthorAssociation, Body: c.Body, URL: c.URL}
	}
	return ParseLedger(cs)
}

func post(ctx context.Context, o Options, body string) (string, error) {
	args := []string{"pr", "comment"}
	if o.Repo != "" {
		args = append(args, "-R", o.Repo)
	}
	args = append(args, fmt.Sprint(o.PR), "--body", body)
	out, err := o.Run(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

func names(cs []ciwait.Check) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func failingText(n []string) string {
	if len(n) == 0 {
		return "(nothing reported failing yet: checks may still be running)"
	}
	return strings.Join(n, ", ")
}

// checkReason is "" if reason is acceptable, else why not. It has to name at
// least one check the last round reported failing, at a word boundary: "lint"
// is not named by "linting", and "test" would otherwise match any sentence that
// mentions a test. It is a forcing function for saying what you are fixing, not
// a proof that you fixed it.
func checkReason(reason string, failing []string) string {
	reason = oneLine(reason)
	if reason == "" {
		return "no --reason given"
	}
	if len(strings.Fields(reason)) < minReasonWords {
		return fmt.Sprintf("the reason is fewer than %d words; say what you changed and why it fixes the failure", minReasonWords)
	}
	for _, n := range failing {
		re := regexp.MustCompile(`(?i)(^|[^\pL\pN])` + regexp.QuoteMeta(n) + `($|[^\pL\pN])`)
		if re.MatchString(reason) {
			return ""
		}
	}
	return "the reason does not name a check the previous round reported failing"
}

func reasonRefusal(o Options, spent int, failing []string, why string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "REFUSED (%s). Round %d of %d for PR #%d needs a reason that names a check the previous round reported failing.\n", why, spent+1, o.Cap, o.PR)
	fmt.Fprintf(&b, "Failing on the last round: %s\n", strings.Join(failing, ", "))
	b.WriteString("No new push was granted; any observed unrecorded head still spends a round. A round is not for trying again; say which failure you understand and what you changed for it, quoting the check's name:\n")
	fmt.Fprintf(&b, "  lacquer ci-round begin %d --reason \"<%s failed because ...; I changed ...>\"\n", o.PR, failing[0])
	return b.String()
}

func nothingToFix(o Options, l Ledger, rd ciwait.Reading) Result {
	var b strings.Builder
	fmt.Fprintf(&b, "REFUSED: no new push granted. The last round (%s) has reported no failing check on PR #%d, so there is nothing yet to fix.\n", short(rd.Head), o.PR)
	switch {
	case len(rd.Checks) == 0:
		b.WriteString("It has NO checks: the PR was never tested, which is not green (`lacquer wait pr` exits 3). Escalate; do not push to find out.\n")
	case len(rd.Running()) > 0:
		fmt.Fprintf(&b, "Still running: %s. Wait for it (`lacquer wait pr %d`); a timeout (exit 2) is not a failure, so escalate rather than push.\n", strings.Join(names(rd.Running()), ", "), o.PR)
	default:
		b.WriteString("Every check passed or was skipped. There is no failure to spend a round on.\n")
	}
	return Result{Code: CodeNothingToFix, Spent: len(l.Rounds), Cap: o.Cap, Text: b.String()}
}

func granted(o Options, e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GRANTED: round %d of %d for PR #%d, recorded on the PR for commit %s.\n", e.Round, o.Cap, o.PR, short(e.SHA))
	fmt.Fprintf(&b, "Push exactly %s: an unrecorded head spends another round.\n", short(e.SHA))
	if e.Round >= o.Cap {
		fmt.Fprintf(&b, "This is the LAST round. If checks fail after it, do not push a third time: `lacquer ci-round begin` will refuse, and the useful thing to do then is say what you do not understand.\n")
	}
	return b.String()
}

func unavailable(o Options, why string) Result {
	return Result{Code: CodeUnavailable, Cap: o.Cap, Text: "COULD NOT CHECK: " + why + "\nThe tool did NOT grant a push and cannot say one is allowed. Ledger observations may already have been recorded. Do not push; escalate.\n"}
}

// exhausted is the third attempt: refuse, and report. It never fails the PR,
// closes it or pushes anything.
func exhausted(ctx context.Context, o Options, l Ledger, rd ciwait.Reading, failing []string, now string) Result {
	var b strings.Builder
	fmt.Fprintf(&b, "EXHAUSTED: PR #%d has used %d of %d agent CI rounds. This is a report, not a failure: the PR is untouched and nothing was pushed.\n", o.PR, len(l.Rounds), o.Cap)
	for _, r := range l.Rounds {
		fmt.Fprintf(&b, "  round %d  %s  %s  %s", r.Round, r.Kind, short(r.SHA), r.At)
		if r.Reason != "" {
			fmt.Fprintf(&b, "  %s", oneLine(r.Reason))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Still failing on %s: %s\n", short(rd.Head), failingText(failing))

	fresh := !l.alreadyReported(rd.Head, failing)
	if !fresh {
		b.WriteString("The stop notice for this state is already on the PR.\n")
	} else {
		entry := Entry{V: 1, Kind: KindExhausted, Epoch: l.Epoch, Cap: o.Cap, SHA: rd.Head, At: now, Failing: failing}
		url, err := post(ctx, o, renderExhausted(entry, l.Rounds))
		if err != nil {
			fmt.Fprintf(&b, "Could not comment on the PR (%v); the refusal stands.\n", err)
		} else {
			fmt.Fprintf(&b, "Commented on the PR with what is still failing: %s\n", url)
		}
		b.WriteString(surface(o, failing, url))
	}
	b.WriteString("Do not push again. Stop, and say what you do not understand: a third attempt is a guess.\n")
	return Result{Code: CodeExhausted, Spent: len(l.Rounds), Cap: o.Cap, Text: b.String()}
}

// surface puts the stop where a human will see it: an inbox ACTION when one is
// configured, otherwise the exact command to run.
func surface(o Options, failing []string, url string) string {
	title := fmt.Sprintf("PR #%d: agent CI budget exhausted (%d of %d rounds); still failing: %s", o.PR, o.Cap, o.Cap, strings.Join(failing, ", "))
	if len(failing) == 0 {
		title = fmt.Sprintf("PR #%d: agent CI budget exhausted (%d of %d rounds)", o.PR, o.Cap, o.Cap)
	}
	ref := fmt.Sprintf("#%d", o.PR)
	if url != "" {
		ref = url
	}
	cmd := fmt.Sprintf("lacquer console --inbox <inbox file> inbox add --type action --title %s --ref %s", shellQuote(title), shellQuote(ref))
	if o.Inbox == "" {
		return "No inbox is configured (no --inbox, $LACQUER_INBOX unset). Tell your PM or the operator, and put it in their inbox with:\n  " + cmd + "\n"
	}
	e, err := inbox.Add(o.Inbox, inbox.Entry{Type: inbox.Action, Title: title, Ref: ref, Project: o.Repo,
		Body: "The agent used its whole CI-round budget on this PR and checks still fail. It stopped instead of pushing again. Read the PR's stop comment for what is failing; either fix it, or authorize a fresh budget with lacquer ci-round reset --reason."})
	if err != nil {
		return fmt.Sprintf("Could not write the inbox entry (%v). Put it there yourself:\n  %s\n", err, cmd)
	}
	return fmt.Sprintf("Inbox ACTION %s written to %s.\n", e.ID, o.Inbox)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
