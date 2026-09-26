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

// ---- fixtures ----

// prAt is one open PR whose only failing check finished failedAgo before t0 and
// which was opened openedAgo before it. The two are separate arguments because
// the Stuck tab must measure from the first.
func prAt(t *testing.T, repo string, n int, openedAgo, failedAgo time.Duration) PR {
	t.Helper()
	roll := fmt.Sprintf(`[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","completedAt":%q},
		{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":%q}]`, ago(failedAgo), ago(failedAgo))
	body := fmt.Sprintf(`[{"number":%d,"title":"PR %d","author":{"login":"a"},"isDraft":false,"createdAt":%q,"url":"https://github.com/%s/pull/%d","mergeStateStatus":"BLOCKED","statusCheckRollup":%s}]`,
		n, n, ago(openedAgo), repo, n, roll)
	prs, err := parsePRs(repo, []byte(body))
	if err != nil || len(prs) != 1 {
		t.Fatalf("parsePRs: %v %v", prs, err)
	}
	return prs[0]
}

// laterAt is one parked issue last touched idle before t0.
func laterAt(t *testing.T, repo string, n int, idle time.Duration) LaterIssue {
	t.Helper()
	body := fmt.Sprintf(`[{"repository":{"nameWithOwner":%q},"number":%d,"title":"Issue %d","createdAt":%q,"updatedAt":%q,"url":"https://github.com/%s/issues/%d"}]`,
		repo, n, n, ago(idle+24*time.Hour), ago(idle), repo, n)
	is, err := parseLater([]byte(body))
	if err != nil || len(is) != 1 {
		t.Fatalf("parseLater: %v %v", is, err)
	}
	return is[0]
}

// stuckModel is a Model on the Stuck tab with every source answered (the App
// Store snapshot healthy, listing one app with nothing stuck), the clock at t0
// plus adv.
func stuckModel(t *testing.T, adv time.Duration, prs []PR, issues []LaterIssue) Model {
	t.Helper()
	p := Program(tabModel(t, 140, 24))
	p = onTab(t, p.(Model), "5")
	p, _ = send(t, p, tickAt(adv),
		PRsEvent{PRs: prs, At: t0.Add(adv)}, LaterEvent{Issues: issues, At: t0.Add(adv)},
		LoadedEvent{Data: Data{ASC: healthyASC(adv)}, At: t0.Add(adv)})
	return p.(Model)
}

func stuckRefs(m Model) []string {
	var out []string
	for _, r := range m.stuckReports() {
		for _, it := range r.Items {
			out = append(out, it.Key)
		}
	}
	return out
}

func screen(m Model) string { return plainAll(m.View()) }

// ---- the operator's thresholds ----

// The thresholds are the operator's and are raised one at a time with a recorded
// reason (#424), so a change to either one has to show up here.
func TestThresholdsAreTheOperatorsOfSeptember20(t *testing.T) {
	if StuckPRFailingAfter != 3*time.Hour {
		t.Errorf("failing checks: %s, want 3h", StuckPRFailingAfter)
	}
	if StuckLaterIdleAfter != 14*24*time.Hour {
		t.Errorf("idle later issue: %s, want 14d", StuckLaterIdleAfter)
	}
}

func TestFailingChecksAreStuckAtThreeHoursAndNotBefore(t *testing.T) {
	for _, tc := range []struct {
		failedAgo time.Duration
		stuck     bool
	}{
		{2*time.Hour + 59*time.Minute, false},
		{3*time.Hour - time.Second, false},
		{3 * time.Hour, true},
		{3*time.Hour + time.Minute, true},
	} {
		m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 40*time.Hour, tc.failedAgo)}, nil)
		got := len(stuckRefs(m)) == 1
		if got != tc.stuck {
			t.Errorf("failing %s: stuck = %v, want %v", tc.failedAgo, got, tc.stuck)
		}
	}
}

// The clock is a fake one: the same PR crosses the line as time passes, with no
// new data.
func TestARowAppearsWhenItsThresholdPassesWithNoNewData(t *testing.T) {
	pr := prAt(t, "o/r", 5, 40*time.Hour, 2*time.Hour+59*time.Minute)
	m := stuckModel(t, 0, []PR{pr}, nil)
	if n := len(stuckRefs(m)); n != 0 {
		t.Fatalf("stuck at 2h59m: %v", stuckRefs(m))
	}
	p, _ := send(t, m, tickAt(time.Minute))
	if got := stuckRefs(p.(Model)); len(got) != 1 || got[0] != "pr-failing:o/r#5" {
		t.Errorf("a minute later (3h00m): %v", got)
	}
}

func TestLaterIssueIsStuckAtFourteenDaysAndNotBefore(t *testing.T) {
	for _, tc := range []struct {
		idle  time.Duration
		stuck bool
	}{
		{13*24*time.Hour + 23*time.Hour, false},
		{14*24*time.Hour - time.Second, false},
		{14 * 24 * time.Hour, true},
		{20 * 24 * time.Hour, true},
	} {
		m := stuckModel(t, 0, nil, []LaterIssue{laterAt(t, "o/r", 9, tc.idle)})
		if got := len(stuckRefs(m)) == 1; got != tc.stuck {
			t.Errorf("idle %s: stuck = %v, want %v", tc.idle, got, tc.stuck)
		}
	}
}

// A PR opened five hours ago whose checks failed one hour ago is not stuck, and
// one opened a day ago whose checks failed four hours ago is. Measuring from
// creation gets both wrong.
func TestFailingSinceIsMeasuredFromCheckCompletionNotPRCreation(t *testing.T) {
	young := prAt(t, "o/r", 1, 5*time.Hour, time.Hour)
	old := prAt(t, "o/r", 2, 24*time.Hour, 4*time.Hour)
	m := stuckModel(t, 0, []PR{young, old}, nil)
	if got := stuckRefs(m); len(got) != 1 || got[0] != "pr-failing:o/r#2" {
		t.Fatalf("stuck = %v, want only #2 (#1 has only been red for an hour)", got)
	}
	it := m.stuckReports()[0].Items[0]
	if want := t0.Add(-4 * time.Hour); !it.Since.Equal(want) {
		t.Errorf("Since = %v, want the check's completion %v", it.Since, want)
	}
	if !strings.Contains(screen(m), "4h00m") {
		t.Errorf("the row does not say it has been red for 4h00m:\n%s", screen(m))
	}
}

// With several red checks it is the first that counts, and a rerun that went
// green is not red at all.
func TestFailingSinceIsTheEarliestFailingCheck(t *testing.T) {
	body := fmt.Sprintf(`[{"number":3,"title":"t","createdAt":%q,"url":"u","statusCheckRollup":[
		{"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"FAILURE","completedAt":%q},
		{"__typename":"CheckRun","name":"b","status":"COMPLETED","conclusion":"TIMED_OUT","completedAt":%q}]}]`,
		ago(30*time.Hour), ago(2*time.Hour), ago(5*time.Hour))
	prs, err := parsePRs("o/r", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if want := t0.Add(-5 * time.Hour); !prs[0].FailingSince.Equal(want) {
		t.Errorf("FailingSince = %v, want %v", prs[0].FailingSince, want)
	}
}

// A red check that carries no time cannot be timed, and the tab says so. It is
// not stuck by a guess, and not silently dropped either.
func TestAFailingCheckWithNoTimeIsReportedNotGuessedAt(t *testing.T) {
	body := fmt.Sprintf(`[{"number":3,"title":"t","createdAt":%q,"url":"u","statusCheckRollup":[
		{"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"FAILURE"}]}]`, ago(90*time.Hour))
	prs, _ := parsePRs("o/r", []byte(body))
	m := stuckModel(t, 0, prs, nil)
	if n := len(stuckRefs(m)); n != 0 {
		t.Errorf("stuck from the PR's age: %v", stuckRefs(m))
	}
	if s := screen(m); !strings.Contains(s, "couldn't check") || !strings.Contains(s, "o/r#3") || !strings.Contains(s, "no check completion time") {
		t.Errorf("an untimed failing PR is not reported:\n%s", s)
	}
}

func TestPassingAndPendingPRsAreNeverStuck(t *testing.T) {
	green, _ := parsePRs("o/r", []byte(fmt.Sprintf(`[{"number":1,"title":"g","createdAt":%q,"url":"u","statusCheckRollup":[{"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"SUCCESS","completedAt":%q}]},
		{"number":2,"title":"p","createdAt":%q,"url":"u","statusCheckRollup":[{"__typename":"CheckRun","name":"a","status":"IN_PROGRESS","conclusion":""}]},
		{"number":3,"title":"n","createdAt":%q,"url":"u","statusCheckRollup":null}]`, ago(99*time.Hour), ago(50*time.Hour), ago(99*time.Hour), ago(99*time.Hour))))
	m := stuckModel(t, 0, green, nil)
	if got := stuckRefs(m); len(got) != 0 {
		t.Errorf("stuck = %v", got)
	}
}

// ---- empty is not broken ----

func TestNothingStuckAndCouldntCheckAreDifferentScreens(t *testing.T) {
	clean := stuckModel(t, 0, nil, nil)

	brokenPRs := clean
	p, _ := send(t, brokenPRs, PRsEvent{Err: "gh: rate limit exceeded", At: t0})
	brokenPRs = p.(Model)

	brokenLater := clean
	p, _ = send(t, brokenLater, LaterEvent{Err: "gh timed out after 45s", At: t0})
	brokenLater = p.(Model)

	cs, bp, bl := screen(clean), screen(brokenPRs), screen(brokenLater)
	if !strings.Contains(cs, "nothing stuck (checked PR checks failing for 3h at ") || !strings.Contains(cs, "Later issues idle for 14d at ") {
		t.Errorf("clean screen does not say what was checked and when:\n%s", cs)
	}
	if strings.Contains(cs, "couldn't check") {
		t.Errorf("clean screen claims a failure:\n%s", cs)
	}
	for name, s := range map[string]string{"PRs": bp, "Later": bl} {
		if strings.Contains(s, "nothing stuck") {
			t.Errorf("a broken %s source still says nothing is stuck:\n%s", name, s)
		}
		if !strings.Contains(s, "couldn't check: ") {
			t.Errorf("a broken %s source does not say so:\n%s", name, s)
		}
	}
	if !strings.Contains(bp, "couldn't check: gh: rate limit exceeded") {
		t.Errorf("the reason is missing:\n%s", bp)
	}
	if !strings.Contains(bl, "couldn't check: gh timed out after 45s") {
		t.Errorf("the reason is missing:\n%s", bl)
	}
	if cs == bp || cs == bl || bp == bl {
		t.Error("two of clean, PRs broken, Later broken render the same")
	}
	// The header says it too, in red, and names how many sources.
	if h := plain(brokenPRs.View().Lines[0]); !strings.Contains(h, "couldn't check 1 of 3 sources") {
		t.Errorf("header = %q", h)
	}
	if h := plain(clean.View().Lines[0]); strings.Contains(h, "couldn't") || !strings.Contains(h, "0 stuck") {
		t.Errorf("header = %q", h)
	}
}

// A source that has not answered yet is neither: it says it is still checking.
func TestBeforeAnythingHasAnsweredItIsNeitherEmptyNorBroken(t *testing.T) {
	p := onTab(t, tabModel(t, 140, 12), "5")
	s := screen(p.(Model))
	// Every source says so, each by name: one answered source must not speak for another.
	if strings.Contains(s, "nothing stuck") || strings.Contains(s, "couldn't check") || strings.Count(s, "checking…") != 3 {
		t.Errorf("screen:\n%s", s)
	}
}

// Repositories that errored while the rest answered are named; a clean-looking
// list from the ones that did answer is not a clean bill.
func TestAPartialSweepNamesTheRepositoriesItCouldNotReach(t *testing.T) {
	m := stuckModel(t, 0, nil, nil)
	p, _ := send(t, m, PRsEvent{Errors: []PRError{{Repo: "o/gone", Err: "HTTP 404"}}, At: t0})
	s := screen(p.(Model))
	if strings.Contains(s, "nothing stuck") || !strings.Contains(s, "couldn't check: 1 repos errored: o/gone (first: HTTP 404)") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestAFailedRefreshKeepsTheLastRowsAndSaysTheyAreOld(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	p, _ := send(t, m, PRsEvent{Err: "network is down", At: t0})
	s := screen(p.(Model))
	if !strings.Contains(s, "o/r#5") || !strings.Contains(s, "couldn't check: network is down (the rows below are from the last good check)") {
		t.Errorf("screen:\n%s", s)
	}
}

// ---- what a row says ----

func TestARowSaysWhatWhyHowLongAndWhere(t *testing.T) {
	m := stuckModel(t, 0,
		[]PR{prAt(t, "acme/widgets", 12, 30*time.Hour, 3*time.Hour+20*time.Minute)},
		[]LaterIssue{laterAt(t, "acme/kit", 7, 15*24*time.Hour)})
	s := screen(m)
	for _, want := range []string{
		"acme/widgets#12  PR 12",
		"3h20m", "1 check failing (test)", "https://github.com/acme/widgets/pull/12",
		"acme/kit#7  Issue 7",
		"15d0h", "parked with no activity since", "https://github.com/acme/kit/issues/7",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "2 stuck") {
		t.Errorf("header = %q", h)
	}
}

func TestOldestFirstWithinASection(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 1, 9*time.Hour, 4*time.Hour), prAt(t, "o/r", 2, 9*time.Hour, 8*time.Hour), prAt(t, "o/r", 3, 9*time.Hour, 6*time.Hour)}, nil)
	got := stuckRefs(m)
	if strings.Join(got, ",") != "pr-failing:o/r#2,pr-failing:o/r#3,pr-failing:o/r#1" {
		t.Errorf("order = %v", got)
	}
}

func TestAgentTextInARowIsSanitised(t *testing.T) {
	pr := prAt(t, "o/r", 1, 9*time.Hour, 5*time.Hour)
	pr.Title = "evil \x1b]0;pwned\x07 title"
	m := stuckModel(t, 0, []PR{pr}, nil)
	for _, l := range m.View().Lines {
		if strings.Contains(l, "\x1b]") || strings.Contains(l, "pwned\x07") {
			t.Errorf("an escape sequence from a title reached the screen: %q", l)
		}
	}
}

// The second line of a row must scroll into view with it.
func TestTheSelectedRowsSecondLineIsNeverScrolledOff(t *testing.T) {
	var prs []PR
	for i := 1; i <= 8; i++ {
		prs = append(prs, prAt(t, "o/r", i, 40*time.Hour, time.Duration(10+i)*time.Hour))
	}
	m := stuckModel(t, 0, prs, nil)
	m.H = 10 // 7 list rows
	var p Program = m
	for i := 0; i < 7; i++ {
		p, _ = feed(t, p, "j")
	}
	mm := p.(Model)
	it, ok := mm.selectedStuck()
	if !ok {
		t.Fatal("nothing selected")
	}
	if s := plainAll(mm.View()); !strings.Contains(s, it.Why) {
		t.Errorf("the selected row %s has its second line off screen:\n%s", it.Ref(), s)
	}
}

// ---- it reads what the tabs fetch and asks for nothing more ----

func TestTheStuckTabMakesNoGHCallsOfItsOwn(t *testing.T) {
	gh := &fakeGH{reply: func(args []string) ([]byte, error) {
		if args[0] == "pr" {
			return []byte("[]"), nil
		}
		return []byte("[]"), nil
	}}
	env := Env{Run: gh.run, Now: func() time.Time { return t0 },
		Roster: fleet.Roster{Project: []fleet.Entry{{Name: "a", Repo: "Acme/steps"}, {Name: "b", Repo: "Acme/kit"}}}}
	const perSweep = 3 // one `pr list` per repository, and one `search issues`

	var p Program = tabModel(t, 140, 20)
	run := func(cmds []Cmd) {
		for _, c := range cmds {
			var ev Event = env.Exec(c)
			var more []Cmd
			p, more = p.Update(ev)
			if len(more) > 0 {
				t.Fatalf("a fetch answered by asking for another: %v", kinds(more))
			}
		}
	}
	var cmds []Cmd
	p, cmds = send(t, p, tickAt(0))
	run(cmds) // Inbox tab: harvest and load only
	before := len(gh.called())

	p, cmds = feed(t, p, "5")
	if count(cmds, CmdPRs) != 1 || count(cmds, CmdLater) != 1 {
		t.Fatalf("switching to Stuck asked for %v, want one PRs and one Later", kinds(cmds))
	}
	run(cmds)
	if got := len(gh.called()) - before; got != perSweep {
		t.Errorf("the first visit made %d gh calls, want %d", got, perSweep)
	}
	after := len(gh.called())

	// Drawing it, any number of times, and every tick inside the throttle, ask nothing.
	for i := 0; i < 50; i++ {
		_ = p.View()
		p, cmds = send(t, p, tickAt(time.Duration(i+1)*5*time.Second))
		if count(cmds, CmdPRs)+count(cmds, CmdLater) != 0 {
			t.Fatalf("tick %d asked GitHub inside the 5-minute throttle: %v", i, kinds(cmds))
		}
	}
	if got := len(gh.called()); got != after {
		t.Errorf("rendering and ticking made %d more gh calls", got-after)
	}

	// The throttle ends at five minutes, and then it is exactly one sweep again.
	p, cmds = send(t, p, tickAt(5*time.Minute+time.Second))
	if count(cmds, CmdPRs) != 1 || count(cmds, CmdLater) != 1 {
		t.Fatalf("after 5m: %v", kinds(cmds))
	}
	run(cmds)
	if got := len(gh.called()) - after; got != perSweep {
		t.Errorf("the next sweep made %d gh calls, want %d", got, perSweep)
	}
}

// The PRs and Later data the Stuck tab reads is the same data those tabs show,
// so a fetch made from either tab satisfies the other's throttle.
func TestAFetchFromThePRsTabServesTheStuckTab(t *testing.T) {
	var p Program = tabModel(t, 140, 20)
	p, cmds := feed(t, p, "4")
	if count(cmds, CmdPRs) != 1 {
		t.Fatalf("PRs tab asked %v", kinds(cmds))
	}
	p, _ = send(t, p, PRsEvent{At: t0})
	_, cmds = feed(t, p, "5")
	if count(cmds, CmdPRs) != 0 || count(cmds, CmdLater) != 1 {
		t.Errorf("Stuck after a PRs fetch asked %v, want only Later", kinds(cmds))
	}
}

func TestRefreshOnStuckAsksBothSourcesAndTheThrottleDoesNotBlockIt(t *testing.T) {
	m := stuckModel(t, 0, nil, nil)
	_, cmds := feed(t, m, "r")
	if count(cmds, CmdPRs) != 1 || count(cmds, CmdLater) != 1 {
		t.Errorf("r asked %v", kinds(cmds))
	}
}

func TestStuckKeysOpenAndCopyTheLink(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	for _, key := range []string{"\r", "o"} {
		_, cmds := feed(t, m, key)
		if len(cmds) != 1 || cmds[0].Kind != CmdOpen || cmds[0].Text != "https://github.com/o/r/pull/5" {
			t.Errorf("%q: %+v", key, cmds)
		}
	}
	_, cmds := feed(t, m, "c")
	if len(cmds) != 1 || cmds[0].Kind != CmdCopy || cmds[0].Text != "https://github.com/o/r/pull/5" {
		t.Errorf("c: %+v", cmds)
	}
}

// ---- dismissal ----

func dismissKey(t *testing.T, m Model, typed string) (Model, []Cmd) {
	t.Helper()
	p, cmds := feed(t, m, "x")
	if !p.(Model).Stuck.Prompt.Active {
		t.Fatal("x did not open the prompt")
	}
	if len(cmds) != 0 {
		t.Fatalf("x asked for %v before a period was given", kinds(cmds))
	}
	p, cmds = feed(t, p, typed+"\r")
	return p.(Model), cmds
}

func TestDismissAsksForAPeriodAndSendsWhenItEnds(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	m2, cmds := dismissKey(t, m, "3d")
	if len(cmds) != 1 || cmds[0].Kind != CmdStuckDismiss || cmds[0].ID != "pr-failing:o/r#5" || !cmds[0].Until.Equal(t0.Add(72*time.Hour)) {
		t.Fatalf("cmds = %+v", cmds)
	}
	if m2.Stuck.Prompt.Active {
		t.Error("the prompt is still open after Enter")
	}
}

func TestDismissPromptShowsItsQuestionAndTakesEveryKey(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	p, _ := feed(t, m, "x")
	f := p.View()
	if last := plain(f.Lines[len(f.Lines)-1]); !strings.Contains(last, "dismiss o/r#5 for") || !f.ShowCursor {
		t.Errorf("prompt row = %q (cursor %v)", last, f.ShowCursor)
	}
	// q types a letter; it does not quit. 5 does not change tab. Esc cancels.
	p, cmds := feed(t, p, "q5")
	mm := p.(Model)
	if mm.Done() || mm.Active != 4 || mm.Stuck.Prompt.Buf != "q5" || len(cmds) != 0 {
		t.Errorf("done=%v active=%d buf=%q cmds=%v", mm.Done(), mm.Active, mm.Stuck.Prompt.Buf, cmds)
	}
	p, cmds = feed(t, p, "\x1b")
	if mm := p.(Model); mm.Stuck.Prompt.Active || len(cmds) != 0 || len(mm.Stuck.Dismissed) != 0 {
		t.Errorf("Esc did not cancel cleanly: %+v %v", mm.Stuck.Prompt, cmds)
	}
}

func TestDismissRefusesAnythingButAPeriod(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	for _, bad := range []string{"", "soon", "0d", "-1d", "91d", "1y", "3 d", "3.5d", "forever"} {
		p, _ := feed(t, m, "x")
		p, cmds := feed(t, p, bad+"\r")
		mm := p.(Model)
		if len(cmds) != 0 || !mm.Stuck.Prompt.Active || mm.Stuck.Prompt.Err == "" {
			t.Errorf("%q: cmds=%v active=%v err=%q", bad, cmds, mm.Stuck.Prompt.Active, mm.Stuck.Prompt.Err)
		}
		if bad != "" && !strings.Contains(plain(mm.View().Lines[mm.H-1]), mm.Stuck.Prompt.Err) {
			t.Errorf("%q: the refusal is not on screen", bad)
		}
	}
	for good, want := range map[string]time.Duration{"90m": 90 * time.Minute, "12h": 12 * time.Hour, "1d": 24 * time.Hour, "7D": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "90d": 90 * 24 * time.Hour} {
		if got, err := ParsePeriod(good); err != nil || got != want {
			t.Errorf("ParsePeriod(%q) = %v, %v", good, got, err)
		}
	}
}

func TestADismissedRowIsHiddenTheHeaderCountsItAndItComesBackWhenItExpires(t *testing.T) {
	prs := []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour), prAt(t, "o/r", 6, 9*time.Hour, 4*time.Hour)}
	m := stuckModel(t, 0, prs, nil)
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "2 stuck") || strings.Contains(h, "dismissed") {
		t.Fatalf("before: %q", h)
	}
	until := t0.Add(24 * time.Hour)
	p, _ := send(t, m, StuckDismissedEvent{Key: "pr-failing:o/r#5", Until: until, Dismissed: map[string]time.Time{"pr-failing:o/r#5": until}})
	m = p.(Model)
	s := screen(m)
	if strings.Contains(s, "o/r#5") || !strings.Contains(s, "o/r#6") {
		t.Errorf("after dismissing #5:\n%s", s)
	}
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "1 stuck") || !strings.Contains(h, "1 dismissed") {
		t.Errorf("header = %q", h)
	}

	// One second before it ends it is still hidden; at the end it is back.
	p, _ = send(t, m, tickAt(24*time.Hour-time.Second))
	if strings.Contains(screen(p.(Model)), "o/r#5") {
		t.Error("the row came back early")
	}
	p, _ = send(t, m, tickAt(24*time.Hour))
	m = p.(Model)
	if !strings.Contains(screen(m), "o/r#5") {
		t.Errorf("an expired dismissal did not bring the row back:\n%s", screen(m))
	}
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "2 stuck") || strings.Contains(h, "dismissed") {
		t.Errorf("header after expiry = %q", h)
	}
}

// A dismissal is for one condition. The same PR going stale another way, or a
// dismissal of a condition that has since cleared, must neither hide a row nor
// inflate the count.
func TestADismissalIsForOneConditionAndOnlyCountsWhileItHidesSomething(t *testing.T) {
	m := stuckModel(t, 0, nil, []LaterIssue{laterAt(t, "o/r", 5, 20*24*time.Hour)})
	until := t0.Add(24 * time.Hour)
	p, _ := send(t, m, StuckDismissedEvent{Key: "pr-failing:o/r#5", Until: until, Dismissed: map[string]time.Time{"pr-failing:o/r#5": until, "later-stale:o/r#99": until}})
	m = p.(Model)
	if s := screen(m); !strings.Contains(s, "o/r#5") {
		t.Errorf("a PR dismissal hid the Later issue with the same number:\n%s", s)
	}
	if h := plain(m.View().Lines[0]); strings.Contains(h, "dismissed") {
		t.Errorf("dismissals of things that are not stuck are counted: %q", h)
	}
}

func TestEverythingDismissedSaysSoInsteadOfLookingBroken(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	until := t0.Add(time.Hour)
	p, _ := send(t, m, StuckDismissedEvent{Key: "pr-failing:o/r#5", Until: until, Dismissed: map[string]time.Time{"pr-failing:o/r#5": until}})
	s := screen(p.(Model))
	if !strings.Contains(s, "nothing stuck (checked ") || !strings.Contains(s, "1 dismissed") {
		t.Errorf("screen:\n%s", s)
	}
}

func TestDismissWritesTheFileAndItSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	inboxPath := filepath.Join(dir, "inbox.jsonl")
	env := Env{InboxPath: inboxPath, Now: func() time.Time { return t0 }}

	ev := env.Exec(Cmd{Kind: CmdStuckDismiss, ID: "pr-failing:o/r#5", Until: t0.Add(72 * time.Hour)}).(StuckDismissedEvent)
	if ev.Err != "" || len(ev.Dismissed) != 1 {
		t.Fatalf("ev = %+v", ev)
	}
	if _, err := os.Stat(filepath.Join(dir, "stuck-dismissed.json")); err != nil {
		t.Fatalf("not next to the inbox: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".stuck-dismissed-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}

	// A new process: a fresh Env and a fresh Model, sharing only the directory.
	env2 := Env{InboxPath: inboxPath, Now: func() time.Time { return t0.Add(time.Hour) }, InboxDefault: true}
	loaded := env2.Exec(Cmd{Kind: CmdLoad}).(LoadedEvent)
	if loaded.Data.DismissedErr != "" || !loaded.Data.Dismissed["pr-failing:o/r#5"].Equal(t0.Add(72*time.Hour)) {
		t.Fatalf("after restart: %+v", loaded.Data)
	}
	m := stuckModel(t, time.Hour, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	loaded.At = t0.Add(time.Hour)
	p, _ := send(t, m, loaded)
	m = p.(Model)
	if strings.Contains(screen(m), "o/r#5") {
		t.Errorf("a dismissal did not survive a restart:\n%s", screen(m))
	}
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "1 dismissed") {
		t.Errorf("header = %q", h)
	}
}

func TestDismissPrunesWhatHasExpiredAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), StuckDismissedFile)
	if _, err := Dismiss(path, "pr-failing:o/r#1", t0.Add(time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := Dismiss(path, "pr-failing:o/r#2", t0.Add(48*time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	got, err := Dismiss(path, "later-stale:o/r#3", t0.Add(48*time.Hour), t0.Add(2*time.Hour)) // #1 has ended by now
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["pr-failing:o/r#1"]; ok || len(got) != 2 {
		t.Errorf("got %v", got)
	}
	back, _ := ReadDismissed(path)
	if len(back) != 2 {
		t.Errorf("file holds %v", back)
	}
}

func TestDismissRefusesAPastOrEndlessPeriod(t *testing.T) {
	path := filepath.Join(t.TempDir(), StuckDismissedFile)
	for name, until := range map[string]time.Time{"now": t0, "past": t0.Add(-time.Hour), "a year": t0.Add(365 * 24 * time.Hour)} {
		if _, err := Dismiss(path, "k", until, t0); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused dismissal wrote a file")
	}
}

// A file that cannot be read must never read as "no dismissals" silently, and
// must never be overwritten by the next dismissal.
func TestAnUnreadableDismissalsFileIsLoudAndHidesNothing(t *testing.T) {
	dir := t.TempDir()
	inboxPath := filepath.Join(dir, "inbox.jsonl")
	if err := os.WriteFile(StuckDismissedPath(inboxPath), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := Env{InboxPath: inboxPath, Now: func() time.Time { return t0 }, InboxDefault: true}
	loaded := env.Exec(Cmd{Kind: CmdLoad}).(LoadedEvent)
	if loaded.Data.DismissedErr == "" {
		t.Fatal("a corrupt file loaded without an error")
	}
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	loaded.At = t0
	p, _ := send(t, m, loaded)
	m = p.(Model)
	if !strings.Contains(screen(m), "o/r#5") {
		t.Error("a corrupt dismissals file hid a row")
	}
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "dismissals unreadable, none applied") {
		t.Errorf("header = %q", h)
	}
	ev := env.Exec(Cmd{Kind: CmdStuckDismiss, ID: "k", Until: t0.Add(time.Hour)}).(StuckDismissedEvent)
	if ev.Err == "" {
		t.Error("a dismissal overwrote a file that could not be read")
	}
	if b, _ := os.ReadFile(StuckDismissedPath(inboxPath)); string(b) != "{not json" {
		t.Errorf("file was changed: %q", b)
	}
	p, _ = send(t, m, ev)
	if n := plain(p.(Model).View().Lines[p.(Model).H-1]); !strings.Contains(n, "not dismissed") {
		t.Errorf("the failure is not shown: %q", n)
	}
}

// A read that began before this process wrote a dismissal predates it: applying
// it would bring the row back until the next read.
func TestASlowerReadDoesNotUndoADismissalJustMade(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	until := t0.Add(time.Hour)
	p, _ := send(t, m, tickAt(10*time.Second), StuckDismissedEvent{Key: "pr-failing:o/r#5", Until: until, Dismissed: map[string]time.Time{"pr-failing:o/r#5": until}, At: t0.Add(10 * time.Second)})
	p, _ = send(t, p, LoadedEvent{Data: Data{Dismissed: map[string]time.Time{}}, At: t0.Add(5 * time.Second)}) // started before the write
	if strings.Contains(screen(p.(Model)), "o/r#5") {
		t.Error("a read that began before the dismissal brought the row back")
	}
	p, _ = send(t, p, LoadedEvent{Data: Data{Dismissed: map[string]time.Time{"pr-failing:o/r#5": until}}, At: t0.Add(11 * time.Second)})
	if strings.Contains(screen(p.(Model)), "o/r#5") {
		t.Error("a later read lost it")
	}
}

func TestXOffTheStuckTabDoesNotOpenAPrompt(t *testing.T) {
	p, _ := feed(t, tabModel(t, 100, 10), "x")
	if p.(Model).Stuck.Prompt.Active {
		t.Error("x opened a prompt on the Inbox tab")
	}
}

// ---- write-back ----

const entryTitle = "AGENT-WRITTEN TITLE"
const entryBody = "AGENT-WRITTEN BODY: decide X"

func replyEnv(t *testing.T, fc *fakeCmd) (Env, string) {
	t.Helper()
	env, path := envFor(t, Overseer{Pane: "%7"}, fc)
	env.Now = func() time.Time { return time.Date(2026, 9, 25, 14, 2, 11, 0, time.UTC) }
	// The repositories the tests write back to are ones the watcher covers.
	env.ExtraRepos = []string{"o/r", "patrickserrano/lacquer"}
	env.Roster = fleet.Roster{Project: []fleet.Entry{{Name: "w", Repo: "acme/widgets"}}}
	return env, path
}

func knownDetail(t *testing.T, e inbox.Entry) Detail {
	t.Helper()
	d := loadedDetail(t, true, e)
	d.Repos = []string{"o/r", "acme/widgets"}
	return d
}

func TestWriteBackPostsForEveryShapeOfGitHubRef(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"https://github.com/patrickserrano/lacquer/issues/424", "gh issue comment 424 -R patrickserrano/lacquer --body-file -"},
		{"https://github.com/patrickserrano/lacquer/pull/493", "gh pr comment 493 -R patrickserrano/lacquer --body-file -"},
		{"patrickserrano/lacquer#424", "gh issue comment 424 -R patrickserrano/lacquer --body-file -"},
	} {
		fc := &fakeCmd{}
		env, _ := replyEnv(t, fc)
		ev := env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes, ship it", Ref: tc.ref}).(RepliedEvent)
		if !ev.OK || ev.CommentErr != "" {
			t.Errorf("%s: %+v", tc.ref, ev)
		}
		var gh []string
		for _, c := range fc.calls {
			if strings.HasPrefix(c, "gh ") {
				gh = append(gh, c)
			}
		}
		if len(gh) != 1 || gh[0] != tc.want {
			t.Errorf("%s: gh ran %q, want %q", tc.ref, gh, tc.want)
		}
	}
}

func TestWriteBackNeverPostsForARefThatIsNotAnIssueOrPR(t *testing.T) {
	for _, ref := range []string{
		"", "session:abc123", "session:o/r#12", "#424", "424", "lacquer#424",
		"https://gitlab.com/o/r/issues/1", "http://github.com/o/r/issues/1", "https://github.com.evil.io/o/r/issues/1",
		"https://github.com/o/r", "https://github.com/o/r/issues", "https://github.com/o/r/issues/1/", "https://github.com/o/r/issues/1/extra",
		"https://github.com/o/r/issues/1?x=1", "https://github.com/o/r/issues/1#issuecomment-5", "https://github.com/o/r/actions/runs/1",
		"https://github.com/o/r/discussions/1", "https://github.com/o/r/issues/0", "https://github.com/o/r/issues/01", "https://github.com/o/r/issues/-1",
		"o/r#0", "o/r#01", "o/r#x", "o/r#1 --web", "o/r#1\n", " o/r#1",
		// Flag injection: anything gh could read as an option, or that is not an owner/name.
		"-R/x#1", "--web/x#1", "o/--web#1", "-o/r#1", "o/r/s#1", "o//r#1", "/r#1", "o/#1", "o/r x#1", "o/../x#1", "o/..#1",
		"https://github.com/--web/r/issues/1", "https://github.com/o/-r/issues/1", "https://github.com/o/r%20x/issues/1",
	} {
		fc := &fakeCmd{}
		env, _ := replyEnv(t, fc)
		ev := env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes", Ref: ref}).(RepliedEvent)
		if !ev.OK || ev.CommentErr != "" {
			t.Errorf("%q: the reply itself was affected: %+v", ref, ev)
		}
		for _, c := range fc.calls {
			if strings.HasPrefix(c, "gh ") {
				t.Errorf("%q: gh ran %q", ref, c)
			}
		}
	}
}

func TestParseGitHubRefAcceptsExactlyTheTwoShapes(t *testing.T) {
	for ref, want := range map[string]GitHubRef{
		"https://github.com/a-b/c.d_e/issues/12": {Repo: "a-b/c.d_e", Number: 12},
		"https://github.com/o/r/pull/7":          {Repo: "o/r", Number: 7, PR: true},
		"o/r#7":                                  {Repo: "o/r", Number: 7},
		"patrickserrano/lacquer#424":             {Repo: "patrickserrano/lacquer", Number: 424},
	} {
		if got, ok := ParseGitHubRef(ref); !ok || got != want {
			t.Errorf("ParseGitHubRef(%q) = %+v, %v; want %+v", ref, got, ok, want)
		}
	}
}

// The comment is the operator's words under one line saying where they came
// from, and nothing the agent wrote. The text goes on stdin, never in argv.
func TestWriteBackBodyIsHeaderPlusVerbatimTextAndNothingTheAgentWrote(t *testing.T) {
	fc := &fakeCmd{}
	env, path := replyEnv(t, fc)
	if _, err := inbox.Add(path, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Body: entryBody, Ref: "https://github.com/o/r/issues/12"}); err != nil {
		t.Fatal(err)
	}
	text := "Go with option B.\n\n  - keep the `$(rm -rf ~)` and \"quotes\" as typed; --web; é 😀"

	// Through the popup, as the operator does it: the entry is loaded, r, type, Enter.
	d := loadedDetail(t, true, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Body: entryBody, Ref: "https://github.com/o/r/issues/12"})
	d.Replying, d.Buf = true, text
	p, cmds := feed(t, d, "\r")
	if len(cmds) != 1 || cmds[0].Kind != CmdReply || cmds[0].Ref != "https://github.com/o/r/issues/12" {
		t.Fatalf("popup asked for %+v", cmds)
	}
	ev := env.Exec(cmds[0]).(RepliedEvent)
	if !ev.OK || ev.CommentErr != "" {
		t.Fatalf("ev = %+v", ev)
	}
	_ = p

	var idx = -1
	for i, c := range fc.calls {
		if strings.HasPrefix(c, "gh ") {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("gh did not run")
	}
	want := "**From the operator's inbox** (lacquer inbox watch, 2026-09-25T14:02:11Z):\n\n" + strings.TrimSpace(text) + "\n"
	if got := fc.stdin[idx]; got != want {
		t.Errorf("stdin:\n%q\nwant\n%q", got, want)
	}
	for _, leak := range []string{entryTitle, entryBody, "AGENT"} {
		if strings.Contains(fc.stdin[idx], leak) {
			t.Errorf("agent-written text %q was posted", leak)
		}
	}
	// argv is fixed words, a number and a repo: none of the operator's text is in it.
	argv := fc.calls[idx]
	if argv != "gh issue comment 12 -R o/r --body-file -" {
		t.Errorf("argv = %q", argv)
	}
	for _, frag := range []string{"option B", "rm -rf", "quotes", "é"} {
		if strings.Contains(argv, frag) {
			t.Errorf("the operator's text %q is in argv", frag)
		}
	}
}

// Nothing is sent to GitHub for a reply the overseer never got: the operator's
// box comes back and they will send it again.
func TestNoCommentWhenTheOverseerDidNotGetTheReply(t *testing.T) {
	fc := &fakeCmd{fail: map[string]error{"tmux send-keys": errors.New("can't find pane: %7")}}
	env, _ := replyEnv(t, fc)
	ev := env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes", Ref: "o/r#1"}).(RepliedEvent)
	if ev.OK {
		t.Fatalf("ev = %+v", ev)
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "gh ") {
			t.Errorf("gh ran for a reply that was not sent: %q", c)
		}
	}
}

func TestANoteOnALaterIssueIsNotWrittenBack(t *testing.T) {
	fc := &fakeCmd{}
	env, _ := replyEnv(t, fc)
	env.Exec(Cmd{Kind: CmdReply, ID: "o/r#1", Text: "note", Tag: "later", Ref: "o/r#1"})
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "gh ") {
			t.Errorf("gh ran for a Later note: %q", c)
		}
	}
}

// A failing gh is reported by name and reason, keeps the popup open, and does
// not touch the reply: the overseer has it and it is recorded.
func TestAFailedCommentIsShownAndNeverUndoesOrBlocksTheReply(t *testing.T) {
	fc := &fakeCmd{fail: map[string]error{"gh issue": errors.New("HTTP 403: Resource not accessible")}}
	env, path := replyEnv(t, fc)
	d := loadedDetail(t, true, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Ref: "https://github.com/o/r/issues/12"})
	d.Replying, d.Buf = true, "yes"
	var p Program = d
	p, cmds := feed(t, p, "\r")
	ev := env.Exec(cmds[0]).(RepliedEvent)

	// The reply went out and was recorded exactly as without a comment.
	if !ev.OK || ev.Note != "sent" {
		t.Fatalf("ev = %+v", ev)
	}
	if got := strings.Join(fc.calls[:2], "\n"); got != "tmux send-keys -t %7 -l [inbox e1] yes\ntmux send-keys -t %7 Enter" {
		t.Errorf("overseer calls:\n%s", got)
	}
	if b, _ := os.ReadFile(RepliesPath(path)); !strings.Contains(string(b), `"id": "e1"`) || !strings.Contains(string(b), `"text": "yes"`) {
		t.Errorf("reply not recorded: %q", b)
	}
	if ev.CommentErr != "HTTP 403: Resource not accessible" {
		t.Errorf("CommentErr = %q", ev.CommentErr)
	}

	// The popup stays open and says so; the box is not left holding the sent text.
	p, more := p.Update(ev)
	dd := p.(Detail)
	if dd.Done() || dd.Sending {
		t.Fatalf("done=%v sending=%v", dd.Done(), dd.Sending)
	}
	if last := plain(dd.View().Lines[dd.H-1]); !strings.Contains(last, "reply sent; comment NOT posted: HTTP 403: Resource not accessible") {
		t.Errorf("status row = %q", last)
	}
	if dd.Buf != "" || dd.Replying {
		t.Errorf("the box still holds %q (replying %v)", dd.Buf, dd.Replying)
	}
	if len(more) != 1 || more[0].Kind != CmdEntry {
		t.Errorf("popup did not reload the entry: %v", kinds(more))
	}
}

func TestASuccessfulReplyStillClosesThePopupAsBefore(t *testing.T) {
	fc := &fakeCmd{}
	env, _ := replyEnv(t, fc)
	d := loadedDetail(t, true, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Ref: "https://github.com/o/r/issues/12"})
	d.Replying, d.Buf = true, "yes"
	p, cmds := feed(t, d, "\r")
	p, _ = p.Update(env.Exec(cmds[0]))
	if !p.Done() {
		t.Error("the popup did not close after a reply and a posted comment")
	}
}

func TestAReplyToASessionRefCarriesNoCommentAndClosesAsBefore(t *testing.T) {
	fc := &fakeCmd{}
	env, _ := replyEnv(t, fc)
	d := loadedDetail(t, true, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Ref: "session:abc123"})
	d.Replying, d.Buf = true, "yes"
	p, cmds := feed(t, d, "\r")
	p, _ = p.Update(env.Exec(cmds[0]))
	if !p.Done() {
		t.Error("popup stayed open")
	}
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "gh ") {
			t.Errorf("gh ran for a session ref: %q", c)
		}
	}
}

// The replies file keeps its format: one line, three keys, and no write-back
// field. foxy-inbox and the phone mirror read it.
func TestWriteBackDoesNotChangeTheRepliesLine(t *testing.T) {
	fc := &fakeCmd{}
	env, path := replyEnv(t, fc)
	env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes", Ref: "o/r#1"})
	b, _ := os.ReadFile(RepliesPath(path))
	if want := replyLine("e1", env.Now(), "yes"); string(b) != want {
		t.Errorf("replies file %q, want %q", b, want)
	}
}

// ctxCmd is a commander whose gh never finishes on its own. It records whether
// it was told to stop, which is what a killed process is.
type ctxCmd struct{ stopped chan struct{} }

func (c ctxCmd) Run(stdin, name string, args ...string) (string, error) {
	return c.RunContext(context.Background(), stdin, name, args...)
}

func (c ctxCmd) RunContext(ctx context.Context, _, _ string, _ ...string) (string, error) {
	<-ctx.Done()
	close(c.stopped)
	return "", ctx.Err()
}

// A comment that runs past the deadline is stopped, so it cannot post after the
// operator was told it had not; and what the operator is told is that it may
// have posted.
func TestATimedOutCommentIsKilledAndSaysItMayHavePosted(t *testing.T) {
	old := writeBackTimeout
	writeBackTimeout = 50 * time.Millisecond
	defer func() { writeBackTimeout = old }()
	cc := ctxCmd{stopped: make(chan struct{})}
	_, err := WriteBack(cc, "https://github.com/o/r/pull/9", "x", t0)
	var to *PostTimeoutError
	if !errors.As(err, &to) {
		t.Fatalf("err = %v, want a *PostTimeoutError", err)
	}
	select {
	case <-cc.stopped:
	default:
		t.Error("gh was not told to stop at the deadline")
	}
	for _, want := range []string{"may or may not have posted", "https://github.com/o/r/pull/9", "stopped"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "NOT posted") {
		t.Errorf("a timeout claims the comment was not posted: %q", err)
	}
}

// Through the popup: the operator is told to look, not that it failed.
func TestATimedOutCommentIsShownAsUnsureNotAsNotPosted(t *testing.T) {
	old := writeBackTimeout
	writeBackTimeout = 30 * time.Millisecond
	defer func() { writeBackTimeout = old }()
	fc := &fakeCmd{}
	env, _ := replyEnv(t, fc)
	env.Cmd = timeoutOnGH{fc, ctxCmd{stopped: make(chan struct{})}}
	d := knownDetail(t, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Ref: "https://github.com/o/r/issues/12"})
	d.Replying, d.Buf = true, "yes"
	p, cmds := feed(t, d, "\r")
	ev := env.Exec(cmds[0]).(RepliedEvent)
	if !ev.OK || !ev.CommentUnsure {
		t.Fatalf("ev = %+v", ev)
	}
	p, _ = p.Update(ev)
	dd := p.(Detail)
	last := plain(dd.View().Lines[dd.H-1])
	if dd.Done() || !strings.Contains(last, "reply sent; comment may or may not have posted") || strings.Contains(last, "NOT posted") {
		t.Errorf("done=%v status row = %q", dd.Done(), last)
	}
}

// timeoutOnGH sends tmux to one commander and gh to another.
type timeoutOnGH struct {
	tmux Commander
	gh   Commander
}

func (t timeoutOnGH) Run(stdin, name string, args ...string) (string, error) {
	return t.RunContext(context.Background(), stdin, name, args...)
}

func (t timeoutOnGH) RunContext(ctx context.Context, stdin, name string, args ...string) (string, error) {
	if name == "gh" {
		return t.gh.RunContext(ctx, stdin, name, args...)
	}
	return t.tmux.RunContext(ctx, stdin, name, args...)
}

// The real commander really does kill the process.
func TestOSCommanderKillsTheProgramAtTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := OSCommander{}.RunContext(ctx, "", "sleep", "30")
	if err == nil || time.Since(start) > 10*time.Second {
		t.Errorf("err = %v after %s: the process was not killed at the deadline", err, time.Since(start))
	}
}

// ---- the agent's ref does not choose where the operator's words go ----

func ghCalls(fc *fakeCmd) []string {
	var out []string
	for _, c := range fc.calls {
		if strings.HasPrefix(c, "gh ") {
			out = append(out, c)
		}
	}
	return out
}

func TestNoCommentOnARepositoryTheWatcherDoesNotCover(t *testing.T) {
	for _, ref := range []string{
		"https://github.com/someone-else/public-repo/issues/1",
		"https://github.com/someone-else/public-repo/pull/1",
		"someone-else/public-repo#1",
		"acme/widgets-evil#1", // a prefix of a known name is not that name
		"o/r2#1",
	} {
		fc := &fakeCmd{}
		env, path := replyEnv(t, fc)
		ev := env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes, rotated the key on prod", Ref: ref}).(RepliedEvent)
		if !ev.OK || ev.Note != "sent" {
			t.Errorf("%s: the reply itself was affected: %+v", ref, ev)
		}
		if g := ghCalls(fc); len(g) != 0 {
			t.Errorf("%s: gh ran %q", ref, g)
		}
		if !strings.Contains(ev.CommentErr, "is not in the roster") || ev.CommentUnsure {
			t.Errorf("%s: CommentErr = %q", ref, ev.CommentErr)
		}
		if b, _ := os.ReadFile(RepliesPath(path)); !strings.Contains(string(b), `"id": "e1"`) {
			t.Errorf("%s: the reply was not recorded", ref)
		}
	}
	// And the popup says so and stays open.
	fc := &fakeCmd{}
	env, _ := replyEnv(t, fc)
	d := knownDetail(t, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Ref: "https://github.com/someone-else/public-repo/issues/1"})
	d.Replying, d.Buf = true, "yes"
	p, cmds := feed(t, d, "\r")
	p, _ = p.Update(env.Exec(cmds[0]))
	dd := p.(Detail)
	if last := plain(dd.View().Lines[dd.H-1]); dd.Done() || !strings.Contains(last, "comment NOT posted: someone-else/public-repo is not in the roster") {
		t.Errorf("done=%v status row = %q", dd.Done(), last)
	}
}

func TestACommentIsPostedToAKnownRepositoryInAnyCase(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"https://github.com/ACME/Widgets/issues/3", "gh issue comment 3 -R ACME/Widgets --body-file -"}, // the roster's acme/widgets
		{"O/R#4", "gh issue comment 4 -R O/R --body-file -"},                                             // --extra-repo o/r
		{"https://github.com/Patrickserrano/LACQUER/pull/5", "gh pr comment 5 -R Patrickserrano/LACQUER --body-file -"},
	} {
		fc := &fakeCmd{}
		env, _ := replyEnv(t, fc)
		ev := env.Exec(Cmd{Kind: CmdReply, ID: "e1", Text: "yes", Ref: tc.ref}).(RepliedEvent)
		if g := ghCalls(fc); ev.CommentErr != "" || len(g) != 1 || g[0] != tc.want {
			t.Errorf("%s: gh %q, err %q; want %q", tc.ref, g, ev.CommentErr, tc.want)
		}
	}
}

// Before Enter, the box says what Enter will also do.
func TestTheReplyBoxSaysWhereItWillCommentBeforeItIsSent(t *testing.T) {
	hint := func(ref string) string {
		d := knownDetail(t, inbox.Entry{ID: "e1", Type: inbox.Action, Title: entryTitle, Ref: ref})
		d.W, d.H = 120, 20
		d.Replying = true
		return plain(d.View().Lines[19])
	}
	if h := hint("https://github.com/o/r/issues/12"); !strings.Contains(h, "⏎ send, and comments on o/r#12") {
		t.Errorf("known repo: %q", h)
	}
	if h := hint("ACME/widgets#7"); !strings.Contains(h, "and comments on ACME/widgets#7") {
		t.Errorf("mixed case: %q", h)
	}
	if h := hint("https://github.com/evil/repo/pull/3"); !strings.Contains(h, "no comment: evil/repo is not in the roster") || strings.Contains(h, "and comments on") {
		t.Errorf("unknown repo: %q", h)
	}
	for _, ref := range []string{"session:abc", "#12", "", "https://example.com/x"} {
		if h := strings.Join(strings.Fields(hint(ref)), " "); strings.Contains(h, "comment") || !strings.Contains(h, "⏎ send · Esc cancel") {
			t.Errorf("ref %q: %q", ref, h)
		}
	}
}

// ---- the "need you" pill survives a narrow terminal ----

func narrowModel(t *testing.T, w int, warn bool) Model {
	t.Helper()
	cfg := Config{Tabs: AllTabs, CanReply: true, HasRepos: false}
	m := model(t, cfg, w, 8,
		item("a1", inbox.Action, time.Hour, "needs you"),
		Item{ID: "a2", Type: inbox.Action, CreatedAt: t0, Title: "answered", Replied: true},
		item("f1", inbox.Unread, time.Hour, "fyi"))
	if warn {
		p, _ := m.Update(LoadedEvent{Data: Data{Items: m.Items}, Warn: "replies unreadable: permission denied", At: t0.Add(time.Second)})
		m = p.(Model)
	}
	return m
}

func TestNeedYouSurvivesNarrowTerminalsWithTheNoRosterWarning(t *testing.T) {
	for _, w := range []int{120, 100, 90, 80, 70, 60} {
		for _, warn := range []bool{false, true} {
			m := narrowModel(t, w, warn)
			head := plain(m.View().Lines[0])
			if !strings.Contains(head, "1 need you") {
				t.Errorf("width %d warn=%v: \"1 need you\" is gone: %q", w, warn, head)
			}
			if cells(head) > w {
				t.Errorf("width %d warn=%v: the row is %d cells wide", w, warn, cells(head))
			}
		}
	}
	// Room to spare: nothing is dropped.
	full := plain(narrowModel(t, 200, true).View().Lines[0])
	for _, want := range []string{"2 Later", "no roster", "1 need you", "1 replied", "1 fyi"} {
		if !strings.Contains(full, want) {
			t.Errorf("at 200 columns %q was dropped: %q", want, full)
		}
	}
}

// What is given up goes in order: the fyi pill, then replied, then the status
// wording, then the tab names.
func TestNarrowRowGivesUpTheLeastImportantFirst(t *testing.T) {
	seen := map[string]int{} // what is the widest terminal each thing disappears at
	for w := 200; w >= 40; w-- {
		head := plain(narrowModel(t, w, false).View().Lines[0])
		for _, k := range []string{"1 fyi", "1 replied", "merges not recorded: no roster", "2 Later"} {
			if _, gone := seen[k]; !gone && !strings.Contains(head, k) {
				seen[k] = w
			}
		}
	}
	order := []string{"1 fyi", "1 replied", "merges not recorded: no roster", "2 Later"}
	for i := 1; i < len(order); i++ {
		a, b := order[i-1], order[i]
		if seen[a] == 0 || seen[b] == 0 || seen[a] < seen[b] {
			t.Errorf("%q went at %d columns and %q at %d: not in order", a, seen[a], b, seen[b])
		}
	}
}

// Clicking a tab still lands on it when the names have been shortened.
func TestTabClicksStillHitWhenTheNamesAreShortened(t *testing.T) {
	m := narrowModel(t, 50, true)
	if lay := m.layout(50); !lay.compactTabs {
		t.Fatalf("layout = %+v, want compact tabs at 50 columns", lay)
	}
	hits := m.tabHits()
	head := plain(m.View().Lines[0])
	for _, h := range hits {
		seg := string([]rune(head)[h.x0:h.x1])
		if !strings.Contains(seg, string(m.Cfg.Tabs[h.tab].Key)) || strings.ContainsAny(seg, "2345") && h.tab == 0 {
			t.Errorf("tab %d hit columns %d-%d cover %q in %q", h.tab, h.x0, h.x1, seg, head)
		}
	}
}

// ---- the nits ----

func TestDismissedEventCarriesTheWriteTimeAndAnOlderReadCannotUndoIt(t *testing.T) {
	m := stuckModel(t, 0, []PR{prAt(t, "o/r", 5, 9*time.Hour, 5*time.Hour)}, nil)
	until := t0.Add(time.Hour)
	// The model's clock says t0+1s; the file was written at t0+9s. A read that
	// started at t0+5s began before that write, and must not bring the row back.
	p, _ := send(t, m, tickAt(time.Second),
		StuckDismissedEvent{Key: "pr-failing:o/r#5", Until: until, Dismissed: map[string]time.Time{"pr-failing:o/r#5": until}, At: t0.Add(9 * time.Second)})
	p, _ = send(t, p, LoadedEvent{Data: Data{Dismissed: map[string]time.Time{}}, At: t0.Add(5 * time.Second)})
	if strings.Contains(screen(p.(Model)), "o/r#5") {
		t.Error("a read that began before the write brought the row back")
	}
	// The env stamps it from its own clock.
	env := Env{InboxPath: filepath.Join(t.TempDir(), "inbox.jsonl"), Now: func() time.Time { return t0.Add(9 * time.Second) }}
	ev := env.Exec(Cmd{Kind: CmdStuckDismiss, ID: "k", Until: t0.Add(time.Hour)}).(StuckDismissedEvent)
	if !ev.At.Equal(t0.Add(9 * time.Second)) {
		t.Errorf("At = %v", ev.At)
	}
}

func TestConcurrentDismissalsAreNotLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), StuckDismissedFile)
	var wg sync.WaitGroup
	const n = 40
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Dismiss(path, fmt.Sprintf("pr-failing:o/r#%d", i), t0.Add(time.Hour), t0); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := ReadDismissed(path)
	if err != nil || len(got) != n {
		t.Errorf("kept %d of %d dismissals (err %v)", len(got), n, err)
	}
}

func TestStuckHeaderShowsTheOldestSourceTime(t *testing.T) {
	m := stuckModel(t, 0, nil, nil)
	p, _ := send(t, m, tickAt(10*time.Minute), PRsEvent{At: t0.Add(10 * time.Minute)}) // PRs re-read; Later still from t0
	head := plain(p.(Model).View().Lines[0])
	if want := t0.Local().Format("15:04"); !strings.HasSuffix(strings.TrimSpace(head), want) {
		t.Errorf("header = %q, want it to end with the older time %s", head, want)
	}
}

func TestStuckHeaderIsNotGoodNewsWhileASourceIsStillChecking(t *testing.T) {
	waiting := onTab(t, tabModel(t, 140, 10), "5").(Model)
	answered := stuckModel(t, 0, nil, nil)
	w, a := waiting.View().Lines[0], answered.View().Lines[0]
	if !strings.Contains(plain(w), "0 stuck") || !strings.Contains(plain(w), "still checking") {
		t.Fatalf("header = %q", plain(w))
	}
	if sw, sa := styleOf(t, w, "0 stuck"), styleOf(t, a, "0 stuck"); sw == sa {
		t.Errorf("0 stuck reads the same (%q) whether or not a source has answered", sw)
	}
	if got := styleOf(t, a, "0 stuck"); !strings.Contains(got, "32") {
		t.Errorf("an all-clear should stay green: %q", got)
	}
}

// A red check with a time and one without: the known time is a floor, so a PR
// past the threshold on it is stuck; a PR not yet past it might be, and is
// reported rather than passed over.
func TestOneUntimedFailingCheckDoesNotHideOrInventAnAge(t *testing.T) {
	mk := func(knownAgo time.Duration) []PR {
		body := fmt.Sprintf(`[{"number":3,"title":"t","createdAt":%q,"url":"u","statusCheckRollup":[
			{"__typename":"CheckRun","name":"a","status":"COMPLETED","conclusion":"FAILURE","completedAt":%q},
			{"__typename":"CheckRun","name":"b","status":"COMPLETED","conclusion":"FAILURE"}]}]`, ago(90*time.Hour), ago(knownAgo))
		prs, err := parsePRs("o/r", []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return prs
	}
	m := stuckModel(t, 0, mk(4*time.Hour), nil)
	if got := stuckRefs(m); len(got) != 1 {
		t.Errorf("stuck on the known 4h = %v", got)
	}
	m = stuckModel(t, 0, mk(time.Hour), nil)
	if got := stuckRefs(m); len(got) != 0 {
		t.Errorf("stuck on a known 1h = %v", got)
	}
	if s := screen(m); !strings.Contains(s, "couldn't check: o/r#3 failing with no check completion time") {
		t.Errorf("the untimed failure is not reported:\n%s", s)
	}
}

// A reader never sees half a file while dismissals are being written.
func TestDismissNeverExposesAHalfWrittenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), StuckDismissedFile)
	if _, err := Dismiss(path, "seed", t0.Add(time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	bad := make(chan error, 1)
	for r := 0; r < 4; r++ {
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := ReadDismissed(path); err != nil {
					select {
					case bad <- err:
					default:
					}
					return
				}
			}
		}()
	}
	for i := 0; i < 250; i++ {
		if _, err := Dismiss(path, fmt.Sprintf("pr-failing:o/r#%d", i), t0.Add(time.Hour), t0); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	select {
	case err := <-bad:
		t.Errorf("a reader saw a torn file: %v", err)
	default:
	}
}
