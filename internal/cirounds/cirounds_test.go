package cirounds

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/cirounds/fakegh"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

func sha(c byte) string { return strings.Repeat(string(c), 40) }

const goodReason = "the lint check failed on an unused import in wait.go, removed it"

// rig is one PR on a fake gh. Every call builds its own Options, so nothing but
// the fake's directory (the "PR") carries state between calls: a call is a fresh
// session by construction.
type rig struct {
	t     *testing.T
	dir   string
	cap   int
	inbox string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, dir: t.TempDir(), cap: 2}
	if err := fakegh.Init(r.dir, sha('a')); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) head(h string, checks ...fakegh.Check) {
	r.t.Helper()
	if err := fakegh.SetRollup(r.dir, "OPEN", h, checks...); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) opts(local, reason string) Options {
	return Options{
		PR: 7, Repo: "o/r", Cap: r.cap, SHA: local, Reason: reason, Inbox: r.inbox,
		Run: func(_ context.Context, args ...string) ([]byte, error) { return fakegh.Run(r.dir, args...) },
		Now: func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) },
	}
}

func (r *rig) begin(local, reason string) Result {
	r.t.Helper()
	return Begin(context.Background(), r.opts(local, reason))
}

func (r *rig) comments() []fakegh.Comment {
	r.t.Helper()
	cs, err := fakegh.Comments(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return cs
}

func want(t *testing.T, got Result, code int) {
	t.Helper()
	if got.Code != code {
		t.Fatalf("exit %d, want %d:\n%s", got.Code, code, got.Text)
	}
}

// Round 1 is the push that opens the PR. It needs no reason (there is nothing
// yet to have failed) and leaves a marker on the PR: that comment IS the count.
func TestFirstRoundIsGrantedAndRecordedOnThePR(t *testing.T) {
	r := newRig(t)
	got := r.begin(sha('a'), "")
	want(t, got, CodeGranted)
	if got.Round != 1 || got.Spent != 1 || got.Cap != 2 {
		t.Errorf("round/spent/cap = %d/%d/%d, want 1/1/2", got.Round, got.Spent, got.Cap)
	}
	cs := r.comments()
	if len(cs) != 1 {
		t.Fatalf("%d comments, want exactly the one round record", len(cs))
	}
	// Readable at a glance without parsing the marker.
	for _, s := range []string{"round 1 of 2", sha('a')[:7]} {
		if !strings.Contains(cs[0].Body, s) {
			t.Errorf("visible text lacks %q:\n%s", s, cs[0].Body)
		}
	}
}

// The heart of the unit. Two failing rounds, then a third attempt is refused,
// and the refusal is on the PR.
func TestThirdAttemptIsRefusedAndReportedOnThePR(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)

	r.head(sha('a'), fakegh.Failed("lint"), fakegh.Passed("test"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)

	// The agent pushed b; it fails again, differently.
	r.head(sha('b'), fakegh.Passed("lint"), fakegh.Failed("test"))
	before := len(r.comments())
	got := r.begin(sha('c'), goodReason+" and test")
	want(t, got, CodeExhausted)

	// It says what was spent and what is still failing, in the output...
	for _, s := range []string{"2 of 2", "test"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("exhaustion report lacks %q:\n%s", s, got.Text)
		}
	}
	// ...and on the PR, where a human sees it without opening CI.
	cs := r.comments()
	if len(cs) != before+1 {
		t.Fatalf("exhaustion posted %d comments, want 1", len(cs)-before)
	}
	last := cs[len(cs)-1].Body
	for _, s := range []string{"exhausted", "2 of 2", "`test`", "will not push"} {
		if !strings.Contains(last, s) {
			t.Errorf("PR comment lacks %q:\n%s", s, last)
		}
	}
}

// A refusal repeated on the same head must not become a comment per attempt: an
// agent that retries would otherwise bury the PR in its own stop notices.
func TestRepeatedExhaustionDoesNotSpamThePR(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))
	want(t, r.begin(sha('c'), goodReason), CodeExhausted)
	n := len(r.comments())
	for i := 0; i < 3; i++ {
		want(t, r.begin(sha('c'), goodReason), CodeExhausted)
	}
	if got := len(r.comments()); got != n {
		t.Errorf("repeat attempts added %d comments", got-n)
	}
}

// A NEW failing set on the same head is news (the first look may have caught
// checks still running), so it is reported again.
func TestExhaustionIsReportedAgainWhenTheFailingSetChanges(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Running("lint"), fakegh.Running("test"))
	want(t, r.begin(sha('c'), goodReason), CodeExhausted)
	n := len(r.comments())
	r.head(sha('b'), fakegh.Failed("lint"), fakegh.Failed("test"))
	want(t, r.begin(sha('c'), goodReason), CodeExhausted)
	if len(r.comments()) != n+1 {
		t.Errorf("a changed failing set was not reported")
	}
}

// A session dies; the count must not. Nothing here shares memory with the
// earlier calls: rig.begin builds fresh Options each time, and only the PR
// (fakegh's directory) persists.
func TestFreshSessionOnAnExhaustedPRIsBlocked(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))

	fresh := Options{PR: 7, Repo: "o/r", Cap: 2, SHA: sha('c'), Reason: goodReason,
		Run: func(_ context.Context, args ...string) ([]byte, error) { return fakegh.Run(r.dir, args...) }}
	if got := Begin(context.Background(), fresh); got.Code != CodeExhausted {
		t.Fatalf("a fresh session got exit %d on an exhausted PR:\n%s", got.Code, got.Text)
	}
	if got := Status(context.Background(), fresh); got.Code != CodeExhausted {
		t.Fatalf("status of an exhausted PR is exit %d", got.Code)
	}
}

// Requirement 3: the cap is on guessing. Round 2 must name a check the previous
// round reported as failing. Every refusal here must record NOTHING.
func TestSecondRoundNeedsAReasonNamingAFailingCheck(t *testing.T) {
	cases := []struct{ name, reason string }{
		{"empty", ""},
		{"blank", "   "},
		{"names no check", "I think the flaky thing just needs another go at it"},
		{"names a check that PASSED, not the failing one", "the test check needs another look because of ordering"},
		{"a bare name is not a reason", "lint"},
		{"a substring of a word is not naming it", "the linting rules got stricter and this broke it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			want(t, r.begin(sha('a'), ""), CodeGranted)
			r.head(sha('a'), fakegh.Failed("lint"), fakegh.Passed("test"))
			n := len(r.comments())
			got := r.begin(sha('b'), tc.reason)
			want(t, got, CodeReason)
			// The refusal must hand the agent the names it may cite.
			if !strings.Contains(got.Text, "lint") {
				t.Errorf("refusal does not list the failing check to cite:\n%s", got.Text)
			}
			if len(r.comments()) != n {
				t.Errorf("a refused round wrote to the PR")
			}
		})
	}
}

// The reason is stored, on the PR, in the marker and in the visible text.
func TestSecondRoundStoresTheReasonAndWhatItAddressed(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("Build & Test (macOS)"), fakegh.Passed("lint"))
	want(t, r.begin(sha('b'), "Build & Test (macOS) crashed: the fixture reads Bundle.main, now injected"), CodeGranted)
	cs := r.comments()
	body := cs[len(cs)-1].Body
	for _, s := range []string{"round 2 of 2", "Build & Test (macOS)", "the fixture reads Bundle.main"} {
		if !strings.Contains(body, s) {
			t.Errorf("round record lacks %q:\n%s", s, body)
		}
	}
	entries, err := ParseLedger(toComments(cs))
	if err != nil {
		t.Fatal(err)
	}
	rounds := entries.Rounds
	if len(rounds) != 2 || rounds[1].SHA != sha('b') || rounds[1].Reason == "" ||
		len(rounds[1].Addressing) != 1 || rounds[1].Addressing[0] != "Build & Test (macOS)" {
		t.Errorf("stored round 2 = %+v", rounds)
	}
}

func toComments(cs []fakegh.Comment) []Comment {
	out := make([]Comment, len(cs))
	for i, c := range cs {
		out[i] = Comment{ID: c.ID, Association: c.AuthorAssociation, Body: c.Body, URL: c.URL}
	}
	return out
}

// A wait that exits 2 (timed out), 3 (no checks) or 4 (the wait failed) reports
// no failure, so there is nothing to fix and no round to spend. Neither does a
// PASS. None of them may consume one.
func TestNothingToFixSpendsNoRound(t *testing.T) {
	cases := []struct {
		name  string
		state string
		heads []fakegh.Check
		want  int
	}{
		{"timed out: checks still running (wait exit 2)", "OPEN", []fakegh.Check{fakegh.Running("lint"), fakegh.Running("test")}, CodeNothingToFix},
		{"no checks found (wait exit 3)", "OPEN", nil, CodeNothingToFix},
		{"checks passed", "OPEN", []fakegh.Check{fakegh.Passed("lint")}, CodeNothingToFix},
		{"PR closed (wait exit 4)", "CLOSED", []fakegh.Check{fakegh.Failed("lint")}, CodeUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			want(t, r.begin(sha('a'), ""), CodeGranted)
			if err := fakegh.SetRollup(r.dir, tc.state, sha('a'), tc.heads...); err != nil {
				t.Fatal(err)
			}
			n := len(r.comments())
			want(t, r.begin(sha('b'), goodReason), tc.want)
			if len(r.comments()) != n {
				t.Fatalf("a refusal wrote to the PR")
			}
			// Not consumed: once the failure is real, the same call succeeds and
			// is round 2, not round 3.
			r.head(sha('a'), fakegh.Failed("lint"))
			got := r.begin(sha('b'), goodReason)
			want(t, got, CodeGranted)
			if got.Round != 2 {
				t.Errorf("granted round %d, want 2", got.Round)
			}
		})
	}
}

// The failure is not a round that gh reports: a cancelled job from a run a
// newer run superseded reads as a failure of a green PR (#435). Reusing ciwait
// means it does not; this pins that the counter did not re-derive it.
func TestSupersededCancelledRunIsNotAFailureToAddress(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'),
		fakegh.Check{Name: "test", Status: "COMPLETED", Conclusion: "CANCELLED", RunID: 1},
		fakegh.Check{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", RunID: 2})
	want(t, r.begin(sha('b'), "the test check failed and I fixed the fixture"), CodeNothingToFix)
}

func TestGHFailingFailsClosed(t *testing.T) {
	r := newRig(t)
	o := r.opts(sha('a'), "")
	o.Run = func(context.Context, ...string) ([]byte, error) { return nil, os.ErrPermission }
	got := Begin(context.Background(), o)
	want(t, got, CodeUnavailable)
	if !strings.Contains(got.Text, "did NOT record") {
		t.Errorf("does not say nothing was recorded:\n%s", got.Text)
	}
	if len(r.comments()) != 0 {
		t.Error("wrote a comment while gh was failing")
	}
}

// Requirement 2. A head the tool never recorded is not an agent round: a human
// pushed. Chosen behaviour: it RESETS the budget (see the package doc for the
// argument), and says so on the PR.
func TestHumanPushResetsTheBudgetAndIsSaidOnThePR(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))
	want(t, r.begin(sha('c'), goodReason), CodeExhausted)

	// A human pushes d. Nothing recorded it.
	r.head(sha('d'), fakegh.Failed("lint"))
	got := r.begin(sha('e'), "")
	want(t, got, CodeGranted)
	if got.Round != 1 || got.Spent != 1 {
		t.Errorf("after a human push: round %d, spent %d; want a fresh 1 of %d", got.Round, got.Spent, 2)
	}
	all := ""
	for _, c := range r.comments() {
		all += c.Body
	}
	if !strings.Contains(all, "budget reset") || !strings.Contains(all, sha('d')[:7]) {
		t.Errorf("the reset is not visible on the PR:\n%s", all)
	}
	// And the reset is a fresh budget, not an unlimited one.
	r.head(sha('e'), fakegh.Failed("lint"))
	want(t, r.begin(sha('f'), goodReason), CodeGranted)
	r.head(sha('f'), fakegh.Failed("lint"))
	want(t, r.begin(sha('9'), goodReason), CodeExhausted)
}

// The reset is recorded once. A second look at the same human head must not
// reset again, or the budget would refill on every call.
func TestResetIsNotRepeatedForTheSameHumanHead(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('d'), fakegh.Failed("lint"))
	want(t, r.begin(sha('e'), ""), CodeGranted)
	// Round 1 of epoch 2 is e; d is the human's. Head d is still the head
	// (the agent has not pushed), and asking for another new commit is round 2.
	got := r.begin(sha('f'), goodReason)
	want(t, got, CodeGranted)
	if got.Round != 2 {
		t.Errorf("round %d, want 2: the human head refilled the budget", got.Round)
	}
}

// A human push before any agent round changes nothing: there is no history to
// reset, and round 1 is still round 1.
func TestUnrecordedHeadWithNoHistoryIsJustRoundOne(t *testing.T) {
	r := newRig(t)
	r.head(sha('d'), fakegh.Failed("lint"))
	got := r.begin(sha('e'), "")
	want(t, got, CodeGranted)
	for _, c := range r.comments() {
		if strings.Contains(c.Body, "budget reset") {
			t.Errorf("a reset was recorded with nothing to reset:\n%s", c.Body)
		}
	}
}

func TestBeginIsIdempotentForARecordedSHA(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	n := len(r.comments())
	got := r.begin(sha('a'), "")
	want(t, got, CodeGranted)
	if got.Round != 1 || len(r.comments()) != n {
		t.Errorf("re-recording the same commit spent a round (round %d, +%d comments)", got.Round, len(r.comments())-n)
	}
}

func TestCapIsConfigurable(t *testing.T) {
	r := newRig(t)
	r.cap = 3
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))
	got := r.begin(sha('c'), goodReason)
	want(t, got, CodeGranted)
	if got.Cap != 3 || !strings.Contains(r.comments()[2].Body, "round 3 of 3") {
		t.Errorf("cap 3 not honoured: %+v", got)
	}
	r.head(sha('c'), fakegh.Failed("lint"))
	want(t, r.begin(sha('d'), goodReason), CodeExhausted)
}

// Only comments from someone with write access count. Otherwise a drive-by
// commenter on a public repo could forge an "exhausted" ledger and stop the
// agent, or forge a reset and refill it.
func TestCommentsFromStrangersAreNotTheLedger(t *testing.T) {
	r := newRig(t)
	// Forged rounds for the PR's REAL head (an entry for a commit the tool
	// never saw would be swallowed by the human-push reset and prove nothing).
	// If these counted, a third attempt below would be exhausted.
	forge := func(round int, s string) string {
		return marker(Entry{V: 1, Kind: KindRound, Epoch: 1, Round: round, Cap: 2, SHA: s, At: "2026-09-20T00:00:00Z"}) + "\nround"
	}
	for _, assoc := range []string{"NONE", "FIRST_TIME_CONTRIBUTOR", "CONTRIBUTOR"} {
		for round, s := range []string{sha('a'), sha('b')} {
			if _, err := fakegh.AddComment(r.dir, assoc, forge(round+1, s)); err != nil {
				t.Fatal(err)
			}
		}
	}
	got := r.begin(sha('c'), "")
	want(t, got, CodeGranted)
	if got.Round != 1 {
		t.Errorf("a stranger's marker counted: round %d", got.Round)
	}
	// A collaborator's does: the same forgery, from someone with write access.
	r2 := newRig(t)
	for round, s := range []string{sha('a'), sha('b')} {
		if _, err := fakegh.AddComment(r2.dir, "COLLABORATOR", forge(round+1, s)); err != nil {
			t.Fatal(err)
		}
	}
	want(t, r2.begin(sha('c'), goodReason), CodeExhausted)
}

// A marker quoted inside a reply is not a ledger entry either.
func TestQuotedMarkerIsNotAnEntry(t *testing.T) {
	r := newRig(t)
	quoted := "> <!-- lacquer:ci-round {\"v\":1,\"kind\":\"round\",\"epoch\":1,\"round\":1,\"sha\":\"" + sha('x') + "\"} -->\nwhy did it stop?"
	if _, err := fakegh.AddComment(r.dir, "OWNER", quoted); err != nil {
		t.Fatal(err)
	}
	if got := r.begin(sha('a'), ""); got.Round != 1 {
		t.Errorf("a quoted marker counted: round %d", got.Round)
	}
}

// A ledger comment we wrote and cannot read must not be skipped: skipping it
// refunds a round. Fail closed.
func TestUnreadableLedgerEntryFailsClosed(t *testing.T) {
	r := newRig(t)
	if _, err := fakegh.AddComment(r.dir, "OWNER", `<!-- lacquer:ci-round {"v":1,"kind": -->`); err != nil {
		t.Fatal(err)
	}
	got := r.begin(sha('a'), "")
	want(t, got, CodeUnavailable)
}

// Two sessions racing for the same round: the earlier comment owns it, and the
// loser is told, rather than both proceeding on one budget.
func TestLosingARaceForARoundIsRefused(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	o := r.opts(sha('b'), goodReason)
	// Between this session reading the ledger and writing to it, another
	// session claims round 2 for a different commit.
	posted := false
	inner := o.Run
	o.Run = func(ctx context.Context, args ...string) ([]byte, error) {
		if !posted && len(args) > 1 && args[1] == "comment" {
			posted = true
			rival := Entry{V: 1, Kind: KindRound, Epoch: 1, Round: 2, Cap: 2, SHA: sha('r'), At: "2026-09-20T12:00:00Z", Addressing: []string{"lint"}, Reason: goodReason}
			if _, err := fakegh.AddComment(r.dir, "OWNER", renderRound(rival)); err != nil {
				t.Fatal(err)
			}
		}
		return inner(ctx, args...)
	}
	got := Begin(context.Background(), o)
	want(t, got, CodeUnavailable)
	if !strings.Contains(got.Text, "another session") {
		t.Errorf("does not say another session took the round:\n%s", got.Text)
	}
}

// Exhaustion is surfaced for a human: an inbox ACTION when one is configured...
func TestExhaustionWritesAnInboxAction(t *testing.T) {
	r := newRig(t)
	r.inbox = filepath.Join(t.TempDir(), "inbox.jsonl")
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))
	got := r.begin(sha('c'), goodReason)
	want(t, got, CodeExhausted)

	es, _, err := inbox.ListOpen(r.inbox)
	if err != nil || len(es) != 1 {
		t.Fatalf("inbox entries = %v, err %v; want exactly one", es, err)
	}
	if es[0].Type != inbox.Action || !strings.Contains(es[0].Title, "#7") || !strings.Contains(es[0].Title, "lint") {
		t.Errorf("inbox entry = %+v", es[0])
	}
	// Repeating the refusal must not add another action for the operator.
	want(t, r.begin(sha('c'), goodReason), CodeExhausted)
	if es, _, _ := inbox.ListOpen(r.inbox); len(es) != 1 {
		t.Errorf("a repeated refusal added inbox entries: %d", len(es))
	}
}

// ...and, with none configured, the report says exactly what to do instead.
func TestExhaustionWithoutAnInboxSaysWhatToDo(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))
	got := r.begin(sha('c'), goodReason)
	want(t, got, CodeExhausted)
	for _, s := range []string{"console --inbox", "inbox add --type action", "Do not push", "--ref 'https://github.com/o/r/pull/7#issuecomment-"} {
		if !strings.Contains(got.Text, s) {
			t.Errorf("report lacks %q:\n%s", s, got.Text)
		}
	}
}

// Status never writes.
func TestStatusIsReadOnly(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('d'), fakegh.Failed("lint")) // a human push: begin would reset
	n, calls := len(r.comments()), len(fakegh.Calls(r.dir))
	got := Status(context.Background(), r.opts("", ""))
	want(t, got, CodeGranted)
	if len(r.comments()) != n {
		t.Error("status wrote a comment")
	}
	for _, c := range fakegh.Calls(r.dir)[calls:] {
		if strings.Contains(c, "pr comment") {
			t.Errorf("status called %q", c)
		}
	}
}

func TestBadSHAIsRefused(t *testing.T) {
	r := newRig(t)
	for _, s := range []string{"", "abc", "not-a-sha", strings.Repeat("g", 40)} {
		want(t, r.begin(s, ""), CodeUnavailable)
	}
	if len(r.comments()) != 0 {
		t.Error("recorded a malformed SHA")
	}
}

// gh answering without a comments field is not "no rounds spent": that would
// refund the whole budget on a hiccup.
func TestMissingCommentsFieldFailsClosed(t *testing.T) {
	r := newRig(t)
	o := r.opts(sha('a'), "")
	inner := o.Run
	o.Run = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[len(args)-1] == "comments" {
			return []byte(`{}`), nil
		}
		return inner(ctx, args...)
	}
	want(t, Begin(context.Background(), o), CodeUnavailable)
	if len(r.comments()) != 0 {
		t.Error("recorded a round without being able to read the ledger")
	}
}

// A stop notice is per budget: after a human push refills it and the agent
// spends it and is stopped again on the same failing check, that is a new stop
// and it has to be on the PR, not swallowed as a repeat of the first.
func TestSecondExhaustionAfterAResetIsReportedAgain(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	want(t, r.begin(sha('b'), goodReason), CodeGranted)
	r.head(sha('b'), fakegh.Failed("lint"))
	want(t, r.begin(sha('c'), goodReason), CodeExhausted)
	n := len(r.comments())

	r.head(sha('d'), fakegh.Failed("lint")) // a human
	want(t, r.begin(sha('e'), ""), CodeGranted)
	r.head(sha('e'), fakegh.Failed("lint"))
	want(t, r.begin(sha('f'), goodReason), CodeGranted)
	r.head(sha('f'), fakegh.Failed("lint"))
	before := len(r.comments())
	want(t, r.begin(sha('9'), goodReason), CodeExhausted)
	if len(r.comments()) != before+1 {
		t.Errorf("the second exhaustion (n=%d before, %d after the first) was not reported", n, len(r.comments()))
	}
}

// The last round says so, on the PR and to the agent.
func TestLastRoundIsFlagged(t *testing.T) {
	r := newRig(t)
	want(t, r.begin(sha('a'), ""), CodeGranted)
	r.head(sha('a'), fakegh.Failed("lint"))
	got := r.begin(sha('b'), goodReason)
	want(t, got, CodeGranted)
	if !strings.Contains(got.Text, "LAST round") {
		t.Errorf("grant does not say it is the last round:\n%s", got.Text)
	}
	cs := r.comments()
	if !strings.Contains(cs[len(cs)-1].Body, "last round the agent gets") {
		t.Errorf("PR comment does not say it is the last round:\n%s", cs[len(cs)-1].Body)
	}
}
