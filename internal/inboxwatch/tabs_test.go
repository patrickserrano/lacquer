package inboxwatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

// fakeGH is a gh that answers from a script and records what it was asked. No
// test in this package reaches the real one.
type fakeGH struct {
	mu    sync.Mutex
	calls [][]string
	reply func(args []string) ([]byte, error)
}

func (g *fakeGH) run(_ context.Context, args ...string) ([]byte, error) {
	g.mu.Lock()
	g.calls = append(g.calls, args)
	g.mu.Unlock()
	if g.reply == nil {
		return []byte("[]"), nil
	}
	return g.reply(args)
}

func (g *fakeGH) called() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, c := range g.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func ago(d time.Duration) string { return t0.Add(-d).UTC().Format(time.RFC3339) }

var cfgTabs = Config{Tabs: AllTabs, CanReply: true, HasRepos: true}

// tabModel is a four-tab model of a given size with the clock at t0. Its inbox
// has been read (with what it is given) and nothing else has.
func tabModel(t *testing.T, w, h int, items ...Item) Model {
	t.Helper()
	return model(t, cfgTabs, w, h, items...)
}

func send(t *testing.T, p Program, evs ...Event) (Program, []Cmd) {
	t.Helper()
	var all []Cmd
	for _, ev := range evs {
		var c []Cmd
		p, c = p.Update(ev)
		all = append(all, c...)
	}
	return p, all
}

func tickAt(d time.Duration) TickEvent { return TickEvent{Now: t0.Add(d)} }

const laterFixture = `[
 {"repository":{"nameWithOwner":"Acme/Widgets"},"number":12,"title":"second in Widgets","createdAt":"%s","url":"https://github.com/Acme/Widgets/issues/12"},
 {"repository":{"nameWithOwner":"patrickserrano/lacquer"},"number":7,"title":"park the harvest","createdAt":"%s","url":"https://github.com/patrickserrano/lacquer/issues/7"},
 {"repository":{"nameWithOwner":"Acme/Widgets"},"number":3,"title":"first in Widgets","createdAt":"%s","url":"https://github.com/Acme/Widgets/issues/3"}
]`

func laterIssues(t *testing.T) []LaterIssue {
	t.Helper()
	out, err := parseLater([]byte(fmt.Sprintf(laterFixture, ago(50*time.Hour), ago(90*time.Minute), ago(3*time.Hour))))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func onTab(t *testing.T, m Model, key string) Program {
	t.Helper()
	p, _ := feed(t, m, key)
	return p
}

// ---- Later ----

// Later groups the parked issues under their repository, the repositories
// ordered by short name ignoring case and each one's issues by number, as
// foxy-inbox's later_issues and later_rows do.
func TestLaterGroupsByProjectSortedByShortNameThenNumber(t *testing.T) {
	p := onTab(t, tabModel(t, 100, 14), "2")
	p, _ = send(t, p, LaterEvent{Issues: laterIssues(t), At: t0})
	f := p.View()
	rows := strings.Split(plainAll(f), "\n")
	want := []string{"lacquer  (1)", "  #7      1h  park the harvest", "Widgets  (2)", "  #3      3h  first in Widgets", "  #12     2d  second in Widgets"}
	for i, w := range want {
		if got := strings.TrimRight(rows[2+i], " "); !strings.HasSuffix(got, w) && got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
	head := plain(f.Lines[0])
	if !strings.Contains(head, "3 parked · 2 projects") || !strings.Contains(head, "2 Later") {
		t.Errorf("header = %q", head)
	}
	if got := styleOf(t, f.Lines[2], "lacquer"); got != "\x1b[0;1;36;49m" {
		t.Errorf("project header style = %q, want bold cyan", got)
	}
	if got := styleOf(t, f.Lines[5], "#3"); got != sgrMagenta { // the first issue is selected, and has the selection background
		t.Errorf("issue number style = %q, want magenta", got)
	}
}

func TestLaterSearchesEveryRosterOwnerAndNeverAllOfGitHub(t *testing.T) {
	gh := &fakeGH{reply: func([]string) ([]byte, error) { return []byte("[]"), nil }}
	env := Env{Run: gh.run, Now: func() time.Time { return t0 },
		Roster:     fleet.Roster{Project: []fleet.Entry{{Name: "a", Repo: "Acme/steps"}, {Name: "b", Repo: "Acme/kit"}, {Name: "local"}}},
		ExtraRepos: []string{"patrickserrano/lacquer", "Acme/rail-web"}}
	if ev := env.Exec(Cmd{Kind: CmdLater}).(LaterEvent); ev.Err != "" {
		t.Fatalf("Err = %q", ev.Err)
	}
	want := "search issues --label later --state open --limit 300 --json repository,number,title,createdAt,url --owner Acme --owner patrickserrano"
	if got := gh.called(); len(got) != 1 || got[0] != want {
		t.Errorf("gh ran %q\nwant %q", got, want)
	}

	// With no repository anywhere there is no owner, and `gh search issues`
	// with no --owner would search every public issue on GitHub.
	gh2 := &fakeGH{}
	ev := Env{Run: gh2.run}.Exec(Cmd{Kind: CmdLater}).(LaterEvent)
	if ev.Err == "" || len(gh2.called()) != 0 {
		t.Errorf("no owners: err %q, gh ran %q; it must refuse and not run", ev.Err, gh2.called())
	}
}

// A gh that failed is not an empty tab, and an empty answer is not a failure;
// and before gh has answered, the tab says it is asking.
func TestLaterUnavailableIsNeverShownAsEmpty(t *testing.T) {
	p := onTab(t, tabModel(t, 100, 10), "2")
	if s := plainAll(p.View()); !strings.Contains(s, "loading…") || strings.Contains(s, "nothing parked") {
		t.Errorf("before the first answer:\n%s", s)
	}

	failed, _ := send(t, p, LaterEvent{Err: "gh: HTTP 403: API rate limit exceeded", At: t0})
	s := plainAll(failed.View())
	if !strings.Contains(s, "GitHub unavailable") || !strings.Contains(s, "rate limit exceeded") || strings.Contains(s, "nothing parked") || strings.Contains(s, "0 parked") {
		t.Errorf("a failed search must say unavailable, not look empty:\n%s", s)
	}
	if got := styleOf(t, failed.View().Lines[0], "GitHub unavailable"); got != "\x1b[0;1;31;49m" {
		t.Errorf("unavailable style = %q, want bold red", got)
	}

	empty, _ := send(t, p, LaterEvent{At: t0})
	s = plainAll(empty.View())
	if !strings.Contains(s, "nothing parked (label an issue `later` to park it)") || !strings.Contains(plain(empty.View().Lines[0]), "0 parked") || strings.Contains(s, "unavailable") {
		t.Errorf("an empty answer:\n%s", s)
	}

	// A failure after a success keeps the last good rows, and says they are old.
	ok, _ := send(t, p, LaterEvent{Issues: laterIssues(t), At: t0})
	stale, _ := send(t, ok, LaterEvent{Err: "network is down", At: t0.Add(time.Minute)})
	s = plainAll(stale.View())
	if !strings.Contains(s, "park the harvest") || !strings.Contains(s, "GitHub unavailable (showing the last result)") {
		t.Errorf("last good rows after a failure:\n%s", s)
	}
}

func TestLaterUnavailableWhenGHOutputIsNotJSONOrRunnerFails(t *testing.T) {
	for name, reply := range map[string]func([]string) ([]byte, error){
		"gh fails":     func([]string) ([]byte, error) { return nil, errors.New("gh: not logged in") },
		"not json":     func([]string) ([]byte, error) { return []byte("<html>"), nil },
		"empty output": func([]string) ([]byte, error) { return nil, nil },
	} {
		gh := &fakeGH{reply: reply}
		ev := Env{Run: gh.run, ExtraRepos: []string{"o/r"}}.Exec(Cmd{Kind: CmdLater}).(LaterEvent)
		if ev.Err == "" {
			t.Errorf("%s: no error, issues %v", name, ev.Issues)
		}
	}
}

// Later asks GitHub at most once every five minutes, and only while it is the
// tab being looked at; r asks now.
func TestLaterRefreshIsThrottledToFiveMinutesAndRForcesIt(t *testing.T) {
	var p Program = tabModel(t, 100, 10)
	var cmds []Cmd
	if _, cmds = send(t, p, tickAt(time.Second)); count(cmds, CmdLater) != 0 {
		t.Fatalf("Later was asked for while another tab is showing: %v", kinds(cmds))
	}
	p, cmds = feed(t, p, "2") // arriving on the tab asks at once
	if count(cmds, CmdLater) != 1 {
		t.Fatalf("switching to Later did %v, want one CmdLater", kinds(cmds))
	}
	p, _ = send(t, p, LaterEvent{Issues: laterIssues(t), At: t0})
	for _, at := range []time.Duration{time.Second, time.Minute, 4*time.Minute + 59*time.Second} {
		if _, cmds = send(t, p, tickAt(at)); count(cmds, CmdLater) != 0 {
			t.Errorf("asked again after %s", at)
		}
	}
	// The five minutes run from when the last ask began: t0 (the model's clock when the tab was opened).
	if _, cmds = send(t, p, tickAt(5*time.Minute)); count(cmds, CmdLater) != 1 {
		t.Errorf("no ask after five minutes: %v", kinds(cmds))
	}
	// r asks now, whatever the throttle says.
	var forced []Cmd
	p, _ = send(t, p, tickAt(10*time.Second))
	if _, forced = feed(t, p, "r"); count(forced, CmdLater) != 1 {
		t.Errorf("r did %v, want one CmdLater", kinds(forced))
	}
	// A second r while one is in flight does not start another; its answer is followed by one.
	p, forced = feed(t, p, "r")
	p, forced = feed(t, p, "r")
	if count(forced, CmdLater) != 0 {
		t.Errorf("r during a fetch started another")
	}
	if _, forced = send(t, p, LaterEvent{Issues: laterIssues(t), At: t0}); count(forced, CmdLater) != 1 {
		t.Errorf("the answer to a fetch that was asked to repeat did %v", kinds(forced))
	}
}

// Un-parking and coming back from the popup make the last answer out of date.
func TestLaterIsRefetchedAfterAPopupOrUnpark(t *testing.T) {
	p := onTab(t, tabModel(t, 100, 10), "2")
	p, _ = send(t, p, LaterEvent{Issues: laterIssues(t), At: t0}, tickAt(time.Second))
	for _, k := range []CmdKind{CmdPopupIssue, CmdUnpark} {
		_, cmds := send(t, p, DoneEvent{Kind: k, OK: true, Note: "x"})
		if count(cmds, CmdLater) != 1 {
			t.Errorf("after %v: %v, want a fresh CmdLater", k, kinds(cmds))
		}
		if _, cmds = send(t, p, DoneEvent{Kind: k, Note: "failed"}); count(cmds, CmdLater) != 0 {
			t.Errorf("after a failed %v the tab was refetched anyway", k)
		}
	}
}

func TestLaterKeys(t *testing.T) {
	p := onTab(t, tabModel(t, 100, 12), "2")
	p, _ = send(t, p, LaterEvent{Issues: laterIssues(t), At: t0})
	// The first issue is lacquer#7.
	if _, cmds := feed(t, p, "\r"); len(cmds) != 1 || cmds[0].Kind != CmdPopupIssue || cmds[0].ID != "patrickserrano/lacquer#7" {
		t.Errorf("Enter = %+v", cmds)
	}
	if _, cmds := feed(t, p, "o"); len(cmds) != 1 || cmds[0] != (Cmd{Kind: CmdOpen, Text: "https://github.com/patrickserrano/lacquer/issues/7", Label: "patrickserrano/lacquer#7"}) {
		t.Errorf("o = %+v", cmds)
	}
	if _, cmds := feed(t, p, "c"); len(cmds) != 1 || cmds[0].Kind != CmdCopy || cmds[0].Text != "https://github.com/patrickserrano/lacquer/issues/7" {
		t.Errorf("c = %+v", cmds)
	}
	// j moves to the next issue, skipping the project header between them.
	q, _ := feed(t, p, "j")
	if _, cmds := feed(t, q, "\r"); cmds[0].ID != "Acme/Widgets#3" {
		t.Errorf("after j, Enter opened %q", cmds[0].ID)
	}
	// A click on an issue row is Enter on it; on a header, nothing.
	if _, cmds := feed(t, p, "\x1b[<0;10;6M"); len(cmds) != 1 || cmds[0].ID != "Acme/Widgets#3" {
		t.Errorf("click on the row of Widgets#3 = %+v", cmds)
	}
	if _, cmds := feed(t, p, "\x1b[<0;10;5M"); len(cmds) != 0 {
		t.Errorf("click on a project header did %+v", cmds)
	}
}

// d takes an issue off Later, and only on the second press.
func TestLaterDNeedsTwoPressesAndAnyOtherKeyCancels(t *testing.T) {
	p := onTab(t, tabModel(t, 100, 12), "2")
	p, _ = send(t, p, LaterEvent{Issues: laterIssues(t), At: t0})
	p, cmds := feed(t, p, "d")
	if len(cmds) != 0 {
		t.Fatalf("the first d did %v", kinds(cmds))
	}
	f := p.View()
	if bar := plain(f.Lines[11]); !strings.Contains(bar, "press d again to take patrickserrano/lacquer#7 off Later (the issue stays open)") {
		t.Errorf("armed bar = %q", bar)
	}
	if got := styleOf(t, f.Lines[11], "press d again"); !strings.Contains(got, ";7;31;") {
		t.Errorf("armed bar style = %q, want reverse red", got)
	}
	_, cmds = feed(t, p, "d")
	if len(cmds) != 1 || cmds[0].Kind != CmdUnpark || cmds[0].ID != "patrickserrano/lacquer#7" {
		t.Fatalf("the second d did %+v", cmds)
	}
	// A key in between disarms it.
	q, _ := feed(t, p, "j")
	if _, cmds = feed(t, q, "d"); len(cmds) != 0 {
		t.Errorf("d after j un-parked: %+v", cmds)
	}
	// So does a refresh that reads the inbox, which must not disarm a Later row
	// on its own (the inbox reloads every few seconds).
	q, _ = send(t, p, LoadedEvent{Data: Data{}, At: t0.Add(time.Second)})
	if _, cmds = feed(t, q, "d"); len(cmds) != 1 || cmds[0].Kind != CmdUnpark {
		t.Errorf("an inbox reload disarmed the Later row: %+v", cmds)
	}
}

// The argv of the un-park is the whole write this tool makes to GitHub.
func TestUnparkRemovesOnlyTheLaterLabel(t *testing.T) {
	gh := &fakeGH{}
	env := Env{Run: gh.run}
	ev := env.Exec(Cmd{Kind: CmdUnpark, ID: "Acme/Widgets#12"}).(DoneEvent)
	if !ev.OK || ev.Note != "un-parked Acme/Widgets#12" {
		t.Errorf("event = %+v", ev)
	}
	if got := gh.called(); len(got) != 1 || got[0] != "issue edit -R Acme/Widgets 12 --remove-label later" {
		t.Errorf("gh ran %q", got)
	}
	fail := &fakeGH{reply: func([]string) ([]byte, error) { return nil, errors.New("HTTP 403") }}
	if ev := (Env{Run: fail.run}).Exec(Cmd{Kind: CmdUnpark, ID: "o/r#1"}).(DoneEvent); ev.OK || !strings.Contains(ev.Note, "HTTP 403") {
		t.Errorf("a failed un-park = %+v", ev)
	}
	// A ref that is not owner/name#number never reaches gh as arguments.
	for _, ref := range []string{"--repo=x#1", "o/r#0", "o/r#-1", "o/r#1 --add-label x", "o/r", "o/r/x#1", "#1", "o/r#01"} {
		g := &fakeGH{}
		if ev := (Env{Run: g.run}).Exec(Cmd{Kind: CmdUnpark, ID: ref}).(DoneEvent); ev.OK || len(g.called()) != 0 {
			t.Errorf("un-park %q ran gh %q, event %+v", ref, g.called(), ev)
		}
	}
}

// ---- PRs ----

const prsFixture = `[
 {"number":41,"title":"old one","author":{"login":"app/dependabot"},"isDraft":false,"createdAt":"%s","url":"https://github.com/o/r/pull/41","mergeStateStatus":"BLOCKED",
  "statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"status":"COMPLETED","conclusion":"SKIPPED"},{"status":"COMPLETED","conclusion":"NEUTRAL"},{"status":"COMPLETED","conclusion":"FAILURE"},{"status":"IN_PROGRESS","conclusion":""},{"status":"COMPLETED","conclusion":""},
   {"__typename":"StatusContext","context":"Vercel","state":"SUCCESS","targetUrl":"https://vercel.com/x"},{"__typename":"StatusContext","context":"Other","state":"PENDING"}]},
 {"number":40,"title":"fresh draft","author":{"login":"patrick"},"isDraft":true,"createdAt":"%s","url":"https://github.com/o/r/pull/40","mergeStateStatus":"","statusCheckRollup":null}
]`

func prsFor(t *testing.T, repo string, age1, age2 time.Duration) []PR {
	t.Helper()
	prs, err := parsePRs(repo, []byte(fmt.Sprintf(prsFixture, ago(age1), ago(age2))))
	if err != nil {
		t.Fatal(err)
	}
	return prs
}

func TestPRRowsShowAgeAuthorMergeStateChecksAndDraft(t *testing.T) {
	p := onTab(t, tabModel(t, 110, 12), "4")
	prs := prsFor(t, "Acme/steps", 30*time.Hour, 2*time.Hour)
	p, _ = send(t, p, PRsEvent{PRs: prs, At: t0})
	f := p.View()
	rows := strings.Split(plainAll(f), "\n")
	if rows[2] != "steps  (2)" && !strings.HasPrefix(rows[2], "steps  (2)") {
		t.Errorf("header row = %q", rows[2])
	}
	if got := strings.TrimRight(rows[3], " "); got != "  #41    30h  dependabot     BLOCKED   ✓4 ✗2 …2      old one" {
		t.Errorf("PR row 1 = %q", got)
	}
	if got := strings.TrimRight(rows[4], " "); got != "  #40     2h  patrick        UNKNOWN   ✓0 ✗0 …0      fresh draft [draft]" {
		t.Errorf("PR row 2 = %q", got)
	}
	if head := plain(f.Lines[0]); !strings.Contains(head, "2 open · 1 over 24h") || !strings.Contains(head, "4 PRs") {
		t.Errorf("header = %q", head)
	}
	if got := styleOf(t, f.Lines[4], "✓0"); got != "\x1b[0;36;49m" { // row 3 is selected
		t.Errorf("checks style = %q, want cyan", got)
	}
}

// The operator's rule is no PR open more than 24 hours: the age turns magenta
// at 24h exactly, not before.
func TestPRAgeTurnsMagentaFromTwentyFourHours(t *testing.T) {
	for _, tc := range []struct {
		age     time.Duration
		magenta bool
	}{{23*time.Hour + 59*time.Minute, false}, {24 * time.Hour, true}, {30 * time.Hour, true}, {time.Hour, false}} {
		p := onTab(t, tabModel(t, 110, 12), "4")
		prs := prsFor(t, "o/r", tc.age, tc.age)
		p, _ = send(t, p, PRsEvent{PRs: prs, At: t0})
		f := p.View()
		age := strings.TrimSpace(Age(t0.Add(-tc.age), t0))
		got := styleOf(t, f.Lines[4], age+"  ") // the second row: the first is selected
		if (got == sgrMagenta) != tc.magenta {
			t.Errorf("age %s (%s): style %q, magenta wanted %v", tc.age, age, got, tc.magenta)
		}
		wantHead := "0 over 24h"
		if tc.magenta {
			wantHead = "2 over 24h" // both fixture PRs have the age
		}
		if !strings.Contains(plain(f.Lines[0]), wantHead) {
			t.Errorf("age %s: header %q lacks %q", tc.age, plain(f.Lines[0]), wantHead)
		}
	}
}

// Checks are counted with the classifier `lacquer wait pr` uses. A legacy commit
// status (Vercel's, say) carries `state` and no `status`: read as a check run it
// is "not completed", so a SUCCESS one sat pending for ever in foxy-prs. Also
// unlike foxy-prs, a check that is COMPLETED with no conclusion fails closed, as
// it does in `wait pr`, instead of counting as pending.
func TestPRChecksSummaryClassifiesStatusContextsByState(t *testing.T) {
	pr := prsFor(t, "o/r", time.Hour, time.Hour)[0]
	// Passing: SUCCESS, SKIPPED, NEUTRAL, and Vercel's SUCCESS. Failing: FAILURE and the
	// COMPLETED-without-conclusion. Pending: the one in progress and the PENDING status.
	if pr.Passing != 4 || pr.Failing != 2 || pr.Pending != 2 {
		t.Errorf("passing/failing/pending = %d/%d/%d, want 4/2/2", pr.Passing, pr.Failing, pr.Pending)
	}
	if pr.Author != "dependabot" {
		t.Errorf("author = %q, want the app/ prefix stripped", pr.Author)
	}
	only, err := parsePRs("o/r", []byte(`[{"number":1,"title":"t","createdAt":"2026-09-24T12:00:00Z","statusCheckRollup":[
		{"__typename":"StatusContext","context":"Vercel","state":"SUCCESS"}]}]`))
	if err != nil || only[0].Passing != 1 || only[0].Pending != 0 || only[0].Failing != 0 {
		t.Errorf("a SUCCESS legacy status alone = %+v, %v", only, err)
	}
}

func TestPRsKeepGhOrderAndRosterRepoOrder(t *testing.T) {
	gh := &fakeGH{reply: func(args []string) ([]byte, error) {
		switch args[3] {
		case "Acme/zeta":
			return []byte(fmt.Sprintf(prsFixture, ago(5*time.Hour), ago(9*time.Hour))), nil // gh's order: 41 then 40
		case "Acme/alpha":
			return []byte(fmt.Sprintf(prsFixture, ago(1*time.Hour), ago(2*time.Hour))), nil
		}
		return []byte("[]"), nil
	}}
	env := Env{Run: gh.run, Now: func() time.Time { return t0 },
		Roster: fleet.Roster{Project: []fleet.Entry{{Repo: "Acme/zeta"}, {Repo: "Acme/alpha"}}}, ExtraRepos: []string{"o/extra"}}
	ev := env.Exec(Cmd{Kind: CmdPRs}).(PRsEvent)
	var got []string
	for _, p := range ev.PRs {
		got = append(got, p.Key())
	}
	want := "Acme/zeta#41 Acme/zeta#40 Acme/alpha#41 Acme/alpha#40"
	if strings.Join(got, " ") != want {
		t.Errorf("PRs = %v\nwant %s", got, want)
	}
	calls := gh.called()
	if len(calls) != 3 {
		t.Fatalf("gh ran %d times: %q", len(calls), calls)
	}
	wantArgs := "pr list -R o/extra --state open --limit 100 --json number,title,author,isDraft,createdAt,url,mergeStateStatus,statusCheckRollup"
	found := false
	for _, c := range calls {
		found = found || c == wantArgs
	}
	if !found {
		t.Errorf("no call %q in %q", wantArgs, calls)
	}
}

func TestPRsUnavailableVersusEmptyVersusPartial(t *testing.T) {
	roster := fleet.Roster{Project: []fleet.Entry{{Repo: "o/a"}, {Repo: "o/b"}}}
	run := func(reply func([]string) ([]byte, error)) PRsEvent {
		gh := &fakeGH{reply: reply}
		return Env{Run: gh.run, Roster: roster, Now: func() time.Time { return t0 }}.Exec(Cmd{Kind: CmdPRs}).(PRsEvent)
	}
	show := func(ev PRsEvent) string {
		p := onTab(t, tabModel(t, 120, 10), "4")
		p, _ = send(t, p, ev)
		return plainAll(p.View())
	}

	all := run(func([]string) ([]byte, error) { return nil, errors.New("HTTP 502") })
	if all.Err == "" {
		t.Fatalf("every repository failed and Err is empty")
	}
	if s := show(all); !strings.Contains(s, "GitHub unavailable") || !strings.Contains(s, "HTTP 502") || strings.Contains(s, "no open PRs") {
		t.Errorf("all failed:\n%s", s)
	}

	none := run(func([]string) ([]byte, error) { return []byte("[]"), nil })
	if s := show(none); !strings.Contains(s, "no open PRs across the fleet") || !strings.Contains(s, "0 open · 0 over 24h") || strings.Contains(s, "unavailable") {
		t.Errorf("none open:\n%s", s)
	}

	partial := run(func(args []string) ([]byte, error) {
		if args[3] == "o/b" {
			return nil, errors.New("HTTP 404")
		}
		return []byte(fmt.Sprintf(prsFixture, ago(time.Hour), ago(time.Hour))), nil
	})
	if partial.Err != "" || len(partial.Errors) != 1 || partial.Errors[0] != (PRError{Repo: "o/b", Err: "HTTP 404"}) {
		t.Fatalf("partial = %+v", partial)
	}
	if s := show(partial); !strings.Contains(s, "2 open · 0 over 24h · 1 repos errored") || !strings.Contains(s, "old one") {
		t.Errorf("one repository failed:\n%s", s)
	}

	// Nothing open in the repositories that answered, and one that did not: not "no open PRs".
	quiet := run(func(args []string) ([]byte, error) {
		if args[3] == "o/b" {
			return nil, errors.New("HTTP 404")
		}
		return []byte("[]"), nil
	})
	if s := show(quiet); strings.Contains(s, "no open PRs across the fleet") || !strings.Contains(s, "o/b: HTTP 404") {
		t.Errorf("empty with an error must not read as clear:\n%s", s)
	}

	// Malformed output from one repository is that repository's error.
	bad := run(func(args []string) ([]byte, error) {
		if args[3] == "o/a" {
			return []byte("nope"), nil
		}
		return []byte("[]"), nil
	})
	if len(bad.Errors) != 1 || bad.Errors[0].Repo != "o/a" {
		t.Errorf("bad json: %+v", bad)
	}

	// No repository at all is unavailable, without running gh.
	gh := &fakeGH{}
	if ev := (Env{Run: gh.run}).Exec(Cmd{Kind: CmdPRs}).(PRsEvent); ev.Err == "" || len(gh.called()) != 0 {
		t.Errorf("no repos: %+v, gh %q", ev, gh.called())
	}
}

func TestPRsRefreshIsThrottledToFiveMinutesAndRForcesIt(t *testing.T) {
	var p Program = tabModel(t, 100, 10)
	p, cmds := feed(t, p, "4")
	if count(cmds, CmdPRs) != 1 {
		t.Fatalf("arriving on PRs did %v", kinds(cmds))
	}
	p, _ = send(t, p, PRsEvent{At: t0})
	if _, cmds = send(t, p, tickAt(4*time.Minute+59*time.Second)); count(cmds, CmdPRs) != 0 {
		t.Errorf("asked before five minutes")
	}
	if _, cmds = send(t, p, tickAt(5*time.Minute)); count(cmds, CmdPRs) != 1 {
		t.Errorf("no ask at five minutes")
	}
	if _, cmds = feed(t, p, "r"); count(cmds, CmdPRs) != 1 {
		t.Errorf("r did %v", kinds(cmds))
	}
	// Other tabs do not pay for it.
	q := onTab(t, tabModel(t, 100, 10), "1")
	if _, cmds = send(t, q, tickAt(20*time.Minute)); count(cmds, CmdPRs) != 0 || count(cmds, CmdLater) != 0 {
		t.Errorf("the Inbox tab asked GitHub: %v", kinds(cmds))
	}
}

func TestPRKeys(t *testing.T) {
	p := onTab(t, tabModel(t, 110, 12), "4")
	p, _ = send(t, p, PRsEvent{PRs: prsFor(t, "o/r", 30*time.Hour, 2*time.Hour), At: t0})
	want := Cmd{Kind: CmdOpen, Text: "https://github.com/o/r/pull/41", Label: "o/r#41"}
	for name, keys := range map[string]string{"Enter": "\r", "o": "o", "click": "\x1b[<0;10;4M"} {
		if _, cmds := feed(t, p, keys); len(cmds) != 1 || cmds[0] != want {
			t.Errorf("%s = %+v, want %+v", name, cmds, want)
		}
	}
	if _, cmds := feed(t, p, "c"); len(cmds) != 1 || cmds[0].Kind != CmdCopy || cmds[0].Text != "https://github.com/o/r/pull/41" {
		t.Errorf("c = %+v", cmds)
	}
	if _, cmds := feed(t, p, "d"); len(cmds) != 0 {
		t.Errorf("there is nothing to resolve from PRs, d did %+v", cmds)
	}
	// A link that is not http(s) is never handed to open.
	bad := prsFor(t, "o/r", time.Hour, time.Hour)
	bad[0].URL = "--help"
	q, _ := send(t, p, PRsEvent{PRs: bad, At: t0})
	if _, cmds := feed(t, q, "o"); len(cmds) != 0 {
		t.Errorf("o on a non-link did %+v", cmds)
	}
}

// ---- Done ----

func writeResolved(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	var b strings.Builder
	for i := 0; i < n; i++ {
		// Raised in one order, resolved in another: the tab sorts by resolution.
		fmt.Fprintf(&b, `{"id":"r%03d","type":"action","title":"resolved %d","createdAt":%q,"resolvedAt":%q}`+"\n",
			i, i, t0.Add(-1000*time.Hour+time.Duration(i)*time.Minute).Format(time.RFC3339), t0.Add(-time.Duration(i)*time.Hour).Format(time.RFC3339))
	}
	fmt.Fprintf(&b, `{"id":"open1","type":"action","title":"still open","createdAt":%q}`+"\n", t0.Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDoneIsNewestResolvedFirstAndLimitedToThreeHundred(t *testing.T) {
	path := writeResolved(t, 305)
	if err := AppendReply(RepliesPath(path), "r001", t0, "i answered this"); err != nil {
		t.Fatal(err)
	}
	ev := Env{InboxPath: path}.load()
	got := ev.Data.Done
	if len(got) != 300 {
		t.Fatalf("Done has %d items, want the limit 300", len(got))
	}
	if got[0].ID != "r000" || got[1].ID != "r001" || got[299].ID != "r299" {
		t.Errorf("order: %s %s ... %s, want newest resolution first (r000 r001 ... r299)", got[0].ID, got[1].ID, got[299].ID)
	}
	for _, d := range got {
		if d.ID == "open1" {
			t.Fatal("an open entry is in Done")
		}
	}
	if got[0].Replied || !got[1].Replied {
		t.Errorf("replied marks: r000 %v r001 %v", got[0].Replied, got[1].Replied)
	}
	if got[0].Type != "ACTION" || got[0].Title != "resolved 0" {
		t.Errorf("item = %+v", got[0])
	}
}

// The latest record for an id decides, so an entry rewritten as resolved counts
// once, and one reopened by a later record does not.
func TestDoneUsesTheLatestRecordPerID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	lines := []string{
		fmt.Sprintf(`{"id":"a","type":"action","title":"a open then resolved","createdAt":%q}`, t0.Format(time.RFC3339)),
		fmt.Sprintf(`{"id":"a","type":"action","title":"a open then resolved","createdAt":%q,"resolvedAt":%q}`, t0.Format(time.RFC3339), t0.Format(time.RFC3339)),
		fmt.Sprintf(`{"id":"b","type":"unread","title":"b resolved then rewritten open","createdAt":%q,"resolvedAt":%q}`, t0.Format(time.RFC3339), t0.Format(time.RFC3339)),
		fmt.Sprintf(`{"id":"b","type":"unread","title":"b resolved then rewritten open","createdAt":%q}`, t0.Format(time.RFC3339)),
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Env{InboxPath: path}.load().Data.Done
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("Done = %+v, want only a", got)
	}
}

func TestDoneTabRowsMarksAndEmptyStates(t *testing.T) {
	m := tabModel(t, 100, 12)
	done := []DoneItem{
		{ID: "d1", Type: "ACTION", CreatedAt: t0.Add(-3 * time.Hour), Title: "answered one", Replied: true},
		{ID: "d2", Type: "UNREAD", CreatedAt: t0.Add(-50 * time.Hour), Title: "plain one"},
	}
	p, _ := send(t, onTab(t, m, "3"), LoadedEvent{Data: Data{Done: done}, At: t0})
	f := p.View()
	rows := strings.Split(plainAll(f), "\n")
	if !strings.Contains(rows[2], "↩") || !strings.Contains(rows[2], "d1  answered one") || !strings.Contains(rows[3], "✓") || !strings.Contains(rows[3], "2d") {
		t.Errorf("rows:\n%s\n%s", rows[2], rows[3])
	}
	if got := styleOf(t, f.Lines[2], "↩"); !strings.Contains(got, ";33;") || styleOf(t, f.Lines[3], "✓") != "\x1b[0;32;49m" {
		t.Errorf("mark colours: %q %q", got, styleOf(t, f.Lines[3], "✓"))
	}
	if head := plain(f.Lines[0]); !strings.Contains(head, "2 closed · 1 you answered") || !strings.Contains(head, "3 Done") {
		t.Errorf("header = %q", head)
	}

	e, _ := send(t, onTab(t, m, "3"), LoadedEvent{At: t0})
	if s := plainAll(e.View()); !strings.Contains(s, "nothing closed yet") {
		t.Errorf("empty:\n%s", s)
	}
	bad, _ := send(t, onTab(t, m, "3"), LoadedEvent{Err: "permission denied", At: t0})
	if s := plainAll(bad.View()); !strings.Contains(s, "the inbox could not be read: permission denied") || strings.Contains(s, "nothing closed yet") {
		t.Errorf("unreadable inbox:\n%s", s)
	}
}

func TestDoneKeys(t *testing.T) {
	m := tabModel(t, 100, 12)
	done := []DoneItem{{ID: "d1", Title: "one", Ref: "https://example.com/1", CreatedAt: t0}, {ID: "d2", Title: "two", Ref: "#12", CreatedAt: t0}}
	p, _ := send(t, onTab(t, m, "3"), LoadedEvent{Data: Data{Done: done}, At: t0})
	if _, cmds := feed(t, p, "\r"); len(cmds) != 1 || cmds[0] != (Cmd{Kind: CmdPopup, ID: "d1"}) {
		t.Errorf("Enter = %+v", cmds)
	}
	if _, cmds := feed(t, p, "c"); len(cmds) != 1 || cmds[0] != (Cmd{Kind: CmdCopy, ID: "d1", Text: "d1"}) {
		t.Errorf("c = %+v", cmds)
	}
	if _, cmds := feed(t, p, "o"); len(cmds) != 1 || cmds[0].Text != "https://example.com/1" {
		t.Errorf("o = %+v", cmds)
	}
	q, _ := feed(t, p, "j")
	if q2, cmds := feed(t, q, "o"); len(cmds) != 0 || !strings.Contains(plain(q2.View().Lines[11]), "no link on this item") {
		t.Errorf("o on a non-link = %+v", cmds)
	}
	if _, cmds := feed(t, p, "d"); len(cmds) != 0 {
		t.Errorf("d on Done did %+v: nothing to resolve", cmds)
	}
	if _, cmds := feed(t, p, "\x1b[<0;10;4M"); len(cmds) != 1 || cmds[0] != (Cmd{Kind: CmdPopup, ID: "d2"}) {
		t.Errorf("click on the second row = %+v", cmds)
	}
	if _, cmds := feed(t, p, "r"); count(cmds, CmdLoad) != 1 {
		t.Errorf("r on Done did %v, want the inbox re-read", kinds(cmds))
	}
}

// ---- the tab strip ----

func TestFourTabsInFoxyInboxOrderWithItsKeys(t *testing.T) {
	m := tabModel(t, 100, 10)
	head := plain(m.View().Lines[0])
	if !strings.Contains(head, "1 Inbox") || !strings.Contains(head, " 2 Later ") || !strings.Contains(head, " 3 Done ") || !strings.Contains(head, " 4 PRs ") {
		t.Errorf("strip = %q", head)
	}
	for key, want := range map[string]int{"1": 0, "2": 1, "3": 2, "4": 3} {
		if got := onTab(t, m, key).(Model).Active; got != want {
			t.Errorf("key %s: active = %d, want %d", key, got, want)
		}
	}
	var p Program = m
	for i := 1; i <= 4; i++ {
		p, _ = feed(t, p, "\t")
		if got := p.(Model).Active; got != i%4 {
			t.Errorf("Tab %d: active = %d", i, got)
		}
	}
	p, _ = feed(t, p, "\x1b[Z")
	if p.(Model).Active != 3 {
		t.Errorf("Shift-Tab from the first tab: active = %d, want the last", p.(Model).Active)
	}
	// Clicking a tab name: " 1 Inbox " holds columns 1-9; the next starts at 11.
	if q, _ := feed(t, m, "\x1b[<0;15;1M"); q.(Model).Active != 1 {
		t.Errorf("click on the Later tab: active = %d", q.(Model).Active)
	}
}

// The strings foxy-inbox's draw_hint draws, key bright and description dim.
// Differences from 18488d1: "q quit" ends every list hint (409a added the key),
// and the issue popup says "overseer" where foxy-inbox says "foxy".
func TestHintRowsMatchFoxyInbox(t *testing.T) {
	m := tabModel(t, 200, 10, item("a", inbox.Action, time.Hour, "x"))
	for key, want := range map[string]string{
		"1": "⏎ detail+reply · d resolve · o link · c copy id · Tab next tab · r refresh · q quit",
		"2": "⏎ issue · o open on GitHub · d un-park · c copy url · Tab next tab · r refresh · q quit",
		"3": "⏎ detail+your reply · o link · c copy id · Tab next tab · ↩ you answered · q quit",
		"4": "⏎/o open on GitHub · Tab next tab · r refresh · q quit",
	} {
		f := onTab(t, m, key).View()
		last := f.Lines[len(f.Lines)-1]
		got := strings.ReplaceAll(strings.ReplaceAll(plain(last), "  ·  ", " · "), " ", " ")
		if strings.TrimSpace(got) != want {
			t.Errorf("tab %s hint\n got %q\nwant %q", key, strings.TrimSpace(got), want)
		}
		first := strings.Fields(want)[0]
		if styleOf(t, last, first) != "\x1b[0;1;36;49m" {
			t.Errorf("tab %s: key %q is not bold cyan (%q)", key, first, styleOf(t, last, first))
		}
		if styleOf(t, last, strings.Fields(want)[1]) != sgrDim {
			t.Errorf("tab %s: description is not dim", key)
		}
	}
}

// A refresh on every tab keeps the cursor on its row and the view on the cursor.
func TestGroupedListScrollsWithItsHeaderAndKeepsTheCursorOnARefresh(t *testing.T) {
	var issues []LaterIssue
	for i := 1; i <= 8; i++ {
		issues = append(issues, LaterIssue{Repo: fmt.Sprintf("o/repo%d", (i+1)/2), Number: i, Title: fmt.Sprintf("issue %d", i), CreatedAt: t0.Add(-time.Hour), URL: "https://x/" + fmt.Sprint(i)})
	}
	p := onTab(t, tabModel(t, 60, 8), "2") // view is 5 rows: 12 rows in the list
	p, _ = send(t, p, LaterEvent{Issues: issues, At: t0})
	if mark := plain(p.View().Lines[1]); !strings.Contains(mark, "1–5 of 12") {
		t.Errorf("position marker = %q", mark)
	}
	p, _ = feed(t, p, "jjjjjj") // the 7th issue: repo4's first
	f := plainAll(p.View())
	if !strings.Contains(f, "issue 7") || !strings.Contains(f, "of 12") {
		t.Errorf("selection scrolled out of view:\n%s", f)
	}
	// The refresh brings the same issues in the same order: the cursor stays on issue 7.
	p, _ = send(t, p, LaterEvent{Issues: issues, At: t0.Add(time.Second)})
	if _, cmds := feed(t, p, "\r"); cmds[0].ID != "o/repo4#7" {
		t.Errorf("after a refresh Enter opened %q", cmds[0].ID)
	}
	// The wheel scrolls the view and drags the cursor along.
	q, _ := feed(t, p, "\x1b[<65;5;5M")
	if _, cmds := feed(t, q, "\r"); cmds[0].ID == "" {
		t.Error("no selection after the wheel")
	}
}

// gh returns at most prLimit PRs and does not say when it stopped there, so a
// repository that returns exactly that many is shown as possibly cut short.
func TestPRsSaysWhenARepositoryReturnedAFullPage(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 1; i <= prLimit; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"number":%d,"title":"t%d","createdAt":%q,"url":"https://x/%d","statusCheckRollup":[]}`, i, i, ago(time.Hour), i)
	}
	b.WriteString("]")
	full := b.String()
	gh := &fakeGH{reply: func(args []string) ([]byte, error) {
		if args[3] == "o/busy" {
			return []byte(full), nil
		}
		return []byte(fmt.Sprintf(prsFixture, ago(time.Hour), ago(time.Hour))), nil // two PRs
	}}
	ev := Env{Run: gh.run, Roster: fleet.Roster{Project: []fleet.Entry{{Repo: "o/busy"}, {Repo: "o/quiet"}}}, Now: func() time.Time { return t0 }}.Exec(Cmd{Kind: CmdPRs}).(PRsEvent)
	if len(ev.Full) != 1 || ev.Full[0] != "o/busy" {
		t.Fatalf("Full = %v, want only o/busy", ev.Full)
	}
	p := onTab(t, tabModel(t, 140, 10), "4")
	p, _ = send(t, p, ev)
	if head := plain(p.View().Lines[0]); !strings.Contains(head, "1 repos at the 100-PR limit (may be cut)") {
		t.Errorf("header = %q", head)
	}
	quiet, _ := send(t, onTab(t, tabModel(t, 140, 10), "4"), PRsEvent{PRs: prsFor(t, "o/quiet", time.Hour, time.Hour), At: t0})
	if head := plain(quiet.View().Lines[0]); strings.Contains(head, "limit") {
		t.Errorf("a short page was flagged: %q", head)
	}
}

func TestInboxEmptyMessageNamesTheDoneTabOnlyWhenThereIsOne(t *testing.T) {
	if s := plainAll(tabModel(t, 100, 8).View()); !strings.Contains(s, "inbox clear — nothing waiting on you (3 Done for what closed)") {
		t.Errorf("four tabs:\n%s", s)
	}
	if s := plainAll(model(t, cfgReply, 100, 8).View()); !strings.Contains(s, "inbox clear — nothing waiting on you") || strings.Contains(s, "Done") {
		t.Errorf("one tab:\n%s", s)
	}
}

// gh search returns at most laterLimit issues without saying it stopped there.
func TestLaterSaysWhenTheSearchReturnedItsLimit(t *testing.T) {
	mk := func(n int) []LaterIssue {
		var out []LaterIssue
		for i := 1; i <= n; i++ {
			out = append(out, LaterIssue{Repo: "o/r", Number: i, Title: "t", CreatedAt: t0})
		}
		return out
	}
	full, _ := send(t, onTab(t, tabModel(t, 140, 8), "2"), LaterEvent{Issues: mk(laterLimit), At: t0})
	if head := plain(full.View().Lines[0]); !strings.Contains(head, "at the 300-issue limit (may be cut)") {
		t.Errorf("header = %q", head)
	}
	short, _ := send(t, onTab(t, tabModel(t, 140, 8), "2"), LaterEvent{Issues: mk(laterLimit - 1), At: t0})
	if head := plain(short.View().Lines[0]); strings.Contains(head, "limit") {
		t.Errorf("a short list was flagged: %q", head)
	}
}
