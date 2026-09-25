package inboxwatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

const (
	sgrRed      = "\x1b[0;1;31;49m"
	sgrRedPlain = "\x1b[0;31;49m"
	sgrYellow   = "\x1b[0;33;49m"
	sgrDim      = "\x1b[0;90;49m"
	sgrMagenta  = "\x1b[0;35;49m"
)

var cfgReply = Config{CanReply: true, HasRepos: true}

func rowOf(f Frame, sub string) string {
	for _, l := range f.Lines {
		if strings.Contains(plain(l), sub) {
			return l
		}
	}
	return ""
}

// The colours: red for an ACTION waiting on the operator, yellow once they
// replied, dim plain text for an FYI, and the count pills in the header.
func TestRowsColourByWhatTheyNeedFromTheOperator(t *testing.T) {
	replied := item("bbb", inbox.Action, time.Hour, "answered already")
	replied.Replied = true
	m := model(t, cfgReply, 100, 12,
		item("sel", inbox.Unread, time.Hour, "the cursor sits here"), // the selected row has its own background
		item("aaa", inbox.Action, time.Hour, "needs a decision"),
		replied,
		item("ccc", inbox.Unread, time.Hour, "just so you know"))
	f := m.View()

	r := rowOf(f, "needs a decision")
	if got := styleOf(t, r, "●"); got != sgrRed {
		t.Errorf("ACTION mark = %q, want bold red %q", got, sgrRed)
	}
	if got := styleOf(t, r, "needs a decision"); got != sgrRed {
		t.Errorf("ACTION title = %q, want bold red", got)
	}
	r = rowOf(f, "answered already")
	if !strings.Contains(plain(r), "↩") || styleOf(t, r, "↩") != sgrYellow || styleOf(t, r, "answered already") != sgrYellow {
		t.Errorf("a replied entry must be yellow with ↩: %q", r)
	}
	r = rowOf(f, "just so you know")
	if !strings.Contains(plain(r), "·") || styleOf(t, r, "just so you know") != "\x1b[0;39;49m" {
		t.Errorf("an FYI is plain default-colour text: %q", r)
	}

	head := plain(f.Lines[0])
	for _, want := range []string{"1 Inbox", "1 need you", "1 replied", "2 fyi"} {
		if !strings.Contains(head, want) {
			t.Errorf("header %q lacks %q", head, want)
		}
	}
	if !strings.Contains(f.Lines[0], sgrRedPlain+capL) {
		t.Errorf("the need-you pill has no red accent: %q", f.Lines[0])
	}
}

func TestHeaderSaysInboxClearWhenNothingNeedsYou(t *testing.T) {
	m := model(t, cfgReply, 100, 10, item("ccc", inbox.Unread, time.Minute, "fyi only"))
	if h := plain(m.View().Lines[0]); !strings.Contains(h, "inbox clear") || strings.Contains(h, "need you") {
		t.Errorf("header = %q", h)
	}
}

// The age is magenta exactly when it renders in days, from 48h, as foxy-inbox's
// code does. Its docstring and #409 say "a day", but a 30-hour-old item shows
// `30h` in dim there, and that is the behaviour being matched.
func TestAgeTurnsMagentaWhenItRendersInDays(t *testing.T) {
	for _, tc := range []struct {
		age     time.Duration
		label   string
		magenta bool
	}{
		{45 * time.Minute, "45m", false},
		{24 * time.Hour, "24h", false},
		{30 * time.Hour, "30h", false},
		{47*time.Hour + 59*time.Minute, "47h", false},
		{48 * time.Hour, "2d", true},
		{5 * 24 * time.Hour, "5d", true},
	} {
		m := model(t, cfgReply, 100, 10, item("sel", inbox.Unread, time.Hour, "the cursor sits here"), item("aaa", inbox.Action, tc.age, "row"))
		r := rowOf(m.View(), "row")
		want := sgrDim
		if tc.magenta {
			want = sgrMagenta
		}
		if got := styleOf(t, r, tc.label); got != want {
			t.Errorf("age %v shows %q in %q, want %q", tc.age, tc.label, got, want)
		}
	}
}

func TestPositionMarkerOnTheRule(t *testing.T) {
	var items []Item
	for i := 0; i < 14; i++ {
		items = append(items, item(fmt.Sprintf("id%02d", i), inbox.Unread, time.Hour, fmt.Sprintf("entry %d", i)))
	}
	m := model(t, cfgReply, 80, 9, items...) // 6 rows of list
	if got := plain(m.View().Lines[1]); !strings.HasSuffix(got, " 1–6 of 14 ─") || !strings.HasPrefix(got, "───") {
		t.Errorf("rule = %q, want 1–6 of 14 at its right end", got)
	}
	m.Top = 3
	if got := plain(m.View().Lines[1]); !strings.HasSuffix(got, " 4–9 of 14 ─") {
		t.Errorf("rule = %q, want 4–9 of 14", got)
	}

	short := model(t, cfgReply, 80, 20, items[:3]...)
	if strings.Contains(plain(short.View().Lines[1]), " of ") {
		t.Errorf("a list that fits shows no marker: %q", plain(short.View().Lines[1]))
	}
}

func TestWheelScrollsBothWays(t *testing.T) {
	var items []Item
	for i := 0; i < 20; i++ {
		items = append(items, item(fmt.Sprintf("id%02d", i), inbox.Unread, time.Hour, fmt.Sprintf("entry %d", i)))
	}
	var p Program = model(t, cfgReply, 80, 9, items...)

	p, _ = feed(t, p, "\x1b[<65;10;5M") // wheel down
	m := p.(Model)
	if m.Top != 3 {
		t.Fatalf("wheel down: top = %d, want 3", m.Top)
	}
	if got := plain(m.View().Lines[1]); !strings.Contains(got, " 4–9 of 20 ") {
		t.Errorf("wheel down: rule = %q", got)
	}
	if m.Sel < m.Top {
		t.Errorf("the cursor must stay on screen: sel %d top %d", m.Sel, m.Top)
	}
	p, _ = feed(t, p, "\x1b[<65;10;5M")
	if p.(Model).Top != 6 {
		t.Errorf("second notch: top = %d, want 6", p.(Model).Top)
	}
	p, _ = feed(t, p, "\x1b[<64;10;5M") // wheel up
	if p.(Model).Top != 3 {
		t.Errorf("wheel up: top = %d, want 3", p.(Model).Top)
	}
	p, _ = feed(t, p, "\x1b[<64;10;5M\x1b[<64;10;5M")
	if p.(Model).Top != 0 {
		t.Errorf("wheel up past the top: top = %d, want 0", p.(Model).Top)
	}
	for i := 0; i < 10; i++ {
		p, _ = feed(t, p, "\x1b[<65;10;5M")
	}
	if got, want := p.(Model).Top, 20-6; got != want {
		t.Errorf("wheel down past the end: top = %d, want %d", got, want)
	}
	// Motion and releases are not notches.
	before := p.(Model).Top
	p, _ = feed(t, p, "\x1b[<65;10;5m\x1b[<67;10;5M")
	if p.(Model).Top != before {
		t.Errorf("a release or a horizontal notch scrolled: %d -> %d", before, p.(Model).Top)
	}
}

func TestKeysMoveTheCursorAndKeepItVisible(t *testing.T) {
	var items []Item
	for i := 0; i < 10; i++ {
		items = append(items, item(fmt.Sprintf("id%02d", i), inbox.Unread, time.Hour, fmt.Sprintf("entry %d", i)))
	}
	var p Program = model(t, cfgReply, 80, 6, items...) // 3 rows
	for i := 0; i < 5; i++ {
		p, _ = feed(t, p, "j")
	}
	m := p.(Model)
	if m.Sel != 5 || m.Top != 3 {
		t.Fatalf("after 5 j: sel %d top %d, want 5 and 3", m.Sel, m.Top)
	}
	p, _ = feed(t, p, "\x1b[B\x1b[A\x1b[A") // down, up, up
	if m = p.(Model); m.Sel != 4 {
		t.Errorf("arrows: sel %d, want 4", m.Sel)
	}
	p, _ = feed(t, p, strings.Repeat("k", 20))
	if m = p.(Model); m.Sel != 0 || m.Top != 0 {
		t.Errorf("k past the top: sel %d top %d", m.Sel, m.Top)
	}
	p, _ = feed(t, p, strings.Repeat("j", 20))
	if m = p.(Model); m.Sel != 9 {
		t.Errorf("j past the end: sel %d", m.Sel)
	}
}

func TestDTwiceResolvesAndDOnceDoesNot(t *testing.T) {
	var p Program = model(t, cfgReply, 80, 10,
		item("aaa", inbox.Action, time.Hour, "first"), item("bbb", inbox.Action, time.Hour, "second"))

	p, cmds := feed(t, p, "d")
	if len(cmds) != 0 {
		t.Fatalf("one d must not do anything: %v", cmds)
	}
	if bar := plain(p.View().Lines[9]); !strings.Contains(bar, "press d again to resolve aaa") {
		t.Errorf("one d must arm and say so, bar = %q", bar)
	}
	p, cmds = feed(t, p, "d")
	if len(cmds) != 1 || cmds[0].Kind != CmdResolve || cmds[0].ID != "aaa" {
		t.Fatalf("d d = %v, want one resolve of aaa", cmds)
	}

	// Any other key disarms: d, j, d is two arms, not a resolve.
	p, _ = model(t, cfgReply, 80, 10, item("aaa", inbox.Action, time.Hour, "first"), item("bbb", inbox.Action, time.Hour, "second")).Update(TickEvent{Now: t0})
	p, _ = feed(t, p, "d")
	p, _ = feed(t, p, "j")
	p, cmds = feed(t, p, "d")
	if count(cmds, CmdResolve) != 0 {
		t.Errorf("d, j, d resolved: %v", cmds)
	}
	p, cmds = feed(t, p, "d")
	if len(cmds) != 1 || cmds[0].ID != "bbb" {
		t.Errorf("the second entry's own d d = %v", cmds)
	}
	// A key that moves nothing disarms too: d, c, d on the same row is not a resolve.
	p, _ = feed(t, p, "d")
	p, _ = feed(t, p, "c")
	if _, cmds = feed(t, p, "d"); count(cmds, CmdResolve) != 0 {
		t.Errorf("d, c, d resolved: %v", cmds)
	}
	// A mouse event disarms too.
	p, _ = feed(t, p, "\x1b[<65;1;1M")
	if p.(Model).Arm != "" {
		t.Errorf("the wheel left d armed")
	}
}

func TestOpenOnlyFollowsHTTPLinks(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		open bool
	}{
		{"https://github.com/acme/widgets/pull/5", true},
		{"http://example.com/x", true},
		{"session:abc123", false},
		{"#374", false},
		{"", false},
		{"ftp://host/file", false},
	} {
		it := item("aaa", inbox.Unread, time.Hour, "row")
		it.Ref = tc.ref
		p, cmds := feed(t, model(t, cfgReply, 80, 10, it), "o")
		if tc.open != (count(cmds, CmdOpen) == 1) {
			t.Errorf("ref %q: cmds %v, want open=%v", tc.ref, cmds, tc.open)
		}
		if tc.open && cmds[0].Text != tc.ref {
			t.Errorf("opened %q, want %q", cmds[0].Text, tc.ref)
		}
		if !tc.open {
			if bar := plain(p.View().Lines[9]); !strings.Contains(bar, "no link on this item") {
				t.Errorf("ref %q: bar = %q", tc.ref, bar)
			}
		}
	}
}

func TestCopyEnterAndQuit(t *testing.T) {
	var p Program = model(t, cfgReply, 80, 10, item("aaa", inbox.Action, time.Hour, "first"))
	_, cmds := feed(t, p, "c")
	if len(cmds) != 1 || cmds[0].Kind != CmdCopy || cmds[0].Text != "aaa" {
		t.Errorf("c = %v, want a copy of the id", cmds)
	}
	_, cmds = feed(t, p, "\r")
	if len(cmds) != 1 || cmds[0].Kind != CmdPopup || cmds[0].ID != "aaa" {
		t.Errorf("Enter = %v, want the detail popup", cmds)
	}
	for _, q := range []string{"q", "\x03", "\x1b"} {
		if p2, _ := feed(t, p, q); !p2.Done() {
			t.Errorf("%q did not quit", q)
		}
	}
	if p2, _ := feed(t, p, "j"); p2.Done() {
		t.Error("j quit")
	}
	empty := model(t, cfgReply, 80, 10)
	for _, k := range []string{"\r", "c", "o", "d", "j", "k"} {
		if _, cmds := feed(t, empty, k); len(cmds) != 0 {
			t.Errorf("%q on an empty list did %v", k, cmds)
		}
	}
	if !strings.Contains(plainAll(empty.View()), "inbox clear — nothing waiting on you") {
		t.Errorf("an empty list must say so:\n%s", plainAll(empty.View()))
	}
}

func TestClickSelectsAndOpensARowAndTabsAreHitTested(t *testing.T) {
	var p Program = model(t, cfgReply, 80, 10,
		item("aaa", inbox.Action, time.Hour, "first"), item("bbb", inbox.Action, time.Hour, "second"))
	p, cmds := feed(t, p, "\x1b[<0;5;4M") // 1-based: column 5, row 4 = list row 1
	if p.(Model).Sel != 1 || len(cmds) != 1 || cmds[0].Kind != CmdPopup || cmds[0].ID != "bbb" {
		t.Errorf("click on the second row: sel %d cmds %v", p.(Model).Sel, cmds)
	}
	if _, cmds = feed(t, p, "\x1b[<0;5;4m"); len(cmds) != 0 {
		t.Errorf("the release of a click opened it again: %v", cmds)
	}
	if _, cmds = feed(t, p, "\x1b[<0;5;9M"); len(cmds) != 0 {
		t.Errorf("a click below the list did %v", cmds)
	}
	if _, cmds = feed(t, p, "\x1b[<0;5;2M"); len(cmds) != 0 { // the rule
		t.Errorf("a click on the rule did %v", cmds)
	}

	// With two tabs the strip is hit-tested; 409b adds them for real.
	two := NewModel(Config{Tabs: []Tab{InboxTab, {Key: '2', Label: "Later"}}, CanReply: true}, 80, 10)
	var q Program = two
	q, _ = feed(t, q, "\x1b[<0;15;1M") // " 1 Inbox " is columns 1-9; " 2 Later " starts at 11
	if q.(Model).Active != 1 {
		t.Errorf("click on the second tab: active = %d", q.(Model).Active)
	}
	q, _ = feed(t, q, "\x1b[<0;3;1M")
	if q.(Model).Active != 0 {
		t.Errorf("click on the first tab: active = %d", q.(Model).Active)
	}
	q, _ = feed(t, q, "\t")
	if q.(Model).Active != 1 {
		t.Errorf("Tab: active = %d", q.(Model).Active)
	}
	q, _ = feed(t, q, "\x1b[Z")
	if q.(Model).Active != 0 {
		t.Errorf("Shift-Tab: active = %d", q.(Model).Active)
	}
	q, _ = feed(t, q, "2")
	if q.(Model).Active != 1 {
		t.Errorf("2: active = %d", q.(Model).Active)
	}
}

func TestSelectionFollowsTheEntryAcrossARefresh(t *testing.T) {
	m := model(t, cfgReply, 80, 10,
		item("aaa", inbox.Action, time.Hour, "first"), item("bbb", inbox.Action, time.Hour, "second"), item("ccc", inbox.Action, time.Hour, "third"))
	p, _ := feed(t, m, "jj") // on ccc
	p, _ = p.Update(LoadedEvent{Data: Data{Items: []Item{
		item("new", inbox.Action, time.Hour, "arrived"), item("ccc", inbox.Action, time.Hour, "third"), item("aaa", inbox.Action, time.Hour, "first")}}, At: t0})
	if got := p.(Model).Items[p.(Model).Sel].ID; got != "ccc" {
		t.Errorf("after a refresh the cursor is on %q, want ccc", got)
	}
	// When its entry is gone the cursor stays in range.
	p, _ = p.Update(LoadedEvent{Data: Data{Items: []Item{item("aaa", inbox.Action, time.Hour, "first")}}, At: t0})
	if m := p.(Model); m.Sel != 0 {
		t.Errorf("sel = %d, want 0", m.Sel)
	}
}

// A read that fails must not blank the list: it shows the last good one, and says so.
func TestFailedLoadKeepsTheLastListAndSaysSo(t *testing.T) {
	m := model(t, cfgReply, 100, 10, item("aaa", inbox.Action, time.Hour, "still here"))
	p, _ := m.Update(LoadedEvent{Err: "permission denied", At: t0})
	f := p.(Model).View()
	if !strings.Contains(plainAll(f), "still here") || !strings.Contains(plain(f.Lines[0]), "inbox unavailable") {
		t.Errorf("frame:\n%s", plainAll(f))
	}
	empty := NewModel(cfgReply, 100, 10)
	p, _ = empty.Update(LoadedEvent{Err: "no such file", At: t0})
	if got := plainAll(p.(Model).View()); !strings.Contains(got, "the inbox could not be read: no such file") {
		t.Errorf("an unreadable inbox must not read as an empty one:\n%s", got)
	}
}

func TestHintRowSaysWhyReplyIsOff(t *testing.T) {
	on := model(t, Config{CanReply: true, HasRepos: true}, 120, 10, item("a", inbox.Action, time.Hour, "x"))
	off := model(t, Config{CanReply: false, HasRepos: true}, 120, 10, item("a", inbox.Action, time.Hour, "x"))
	hOn, hOff := plain(on.View().Lines[9]), plain(off.View().Lines[9])
	if !strings.Contains(hOn, "⏎ detail+reply") || strings.Contains(hOn, "off") {
		t.Errorf("hint = %q", hOn)
	}
	if !strings.Contains(hOff, "reply off: no overseer pane") || strings.Contains(hOff, "detail+reply") {
		t.Errorf("hint with no overseer = %q", hOff)
	}
	for _, want := range []string{"d resolve", "o link", "c copy id", "r refresh", "q quit"} {
		if !strings.Contains(hOn, want) {
			t.Errorf("hint %q lacks %q", hOn, want)
		}
	}
	// Each key bright (bold cyan) and its description dim, as draw_hint does.
	raw := on.View().Lines[9]
	if !strings.Contains(raw, "\x1b[0;1;36;49md"+sgrDim+" resolve") {
		t.Errorf("hint styles wrong: %q", raw)
	}
}

// The merge harvest asks GitHub about every roster repository, so it runs at
// most once every five minutes, and `r` forces one.
func TestHarvestRunsAtMostEveryFiveMinutesAndRForcesIt(t *testing.T) {
	var p Program = NewModel(cfgReply, 80, 10)
	at := func(d time.Duration) []Cmd {
		var cmds []Cmd
		p, cmds = p.Update(TickEvent{Now: t0.Add(d)})
		return cmds
	}
	finish := func(d time.Duration) { p, _ = p.Update(HarvestedEvent{At: t0.Add(d)}) }

	if n := count(at(0), CmdHarvest); n != 1 {
		t.Fatalf("the first tick harvests %d times, want 1", n)
	}
	if n := count(at(time.Second), CmdHarvest); n != 0 {
		t.Errorf("a tick while a harvest runs started another")
	}
	finish(2 * time.Second)
	for _, d := range []time.Duration{3 * time.Second, time.Minute, 4*time.Minute + 59*time.Second} {
		if n := count(at(d), CmdHarvest); n != 0 {
			t.Errorf("harvested at +%v, inside the five minutes", d)
		}
	}
	if n := count(at(5*time.Minute), CmdHarvest); n != 1 {
		t.Errorf("at +5m: %d harvests, want 1", n)
	}
	finish(5*time.Minute + time.Second)
	if n := count(at(9*time.Minute), CmdHarvest); n != 0 {
		t.Errorf("the throttle restarts from the last harvest, not from the first: harvested at +9m")
	}

	// r forces one, inside the window...
	var cmds []Cmd
	p, cmds = feed(t, p, "r")
	if count(cmds, CmdHarvest) != 1 || count(cmds, CmdLoad) != 1 {
		t.Errorf("r = %v, want a load and a harvest", kinds(cmds))
	}
	// ...but not a second one while it runs,
	p, cmds = feed(t, p, "r")
	if count(cmds, CmdHarvest) != 0 {
		t.Errorf("r while a harvest runs started another")
	}
	if bar := plain(p.View().Lines[9]); !strings.Contains(bar, "already running") {
		t.Errorf("bar = %q", bar)
	}
	// and the forced one restarts the window.
	finish(9*time.Minute + time.Second)
	if n := count(at(13*time.Minute), CmdHarvest); n != 0 {
		t.Errorf("harvested at +13m, four minutes after a forced one")
	}
	if n := count(at(14*time.Minute+time.Second), CmdHarvest); n != 1 {
		t.Errorf("no harvest five minutes after the forced one")
	}
}

func TestListRefreshesEveryFewSecondsNotEveryTick(t *testing.T) {
	var p Program = NewModel(cfgReply, 80, 10)
	loads := 0
	for i := 0; i <= 12; i++ { // 6 seconds of 500ms ticks
		var cmds []Cmd
		p, cmds = p.Update(TickEvent{Now: t0.Add(time.Duration(i) * 500 * time.Millisecond)})
		loads += count(cmds, CmdLoad)
	}
	if loads != 3 { // at 0s, 3s and 6s
		t.Errorf("%d loads in 6s, want 3", loads)
	}
}

// A harvest that fails, or cannot run, is never silent.
func TestHarvestFailureAndNoRosterShowOnScreen(t *testing.T) {
	m := model(t, cfgReply, 120, 10, item("aaa", inbox.Action, time.Hour, "row"))
	p, _ := m.Update(HarvestedEvent{Unavailable: []string{"merge harvest acme/widgets (gh: HTTP 502)"}, At: t0})
	f := p.(Model).View()
	if !strings.Contains(plain(f.Lines[0]), "merge harvest unavailable") {
		t.Errorf("header = %q", plain(f.Lines[0]))
	}
	if !strings.Contains(plain(f.Lines[9]), "gh: HTTP 502") {
		t.Errorf("the reason must reach the bar: %q", plain(f.Lines[9]))
	}
	if styleOf(t, f.Lines[0], "merge harvest unavailable") != sgrRed {
		t.Errorf("unavailable must be red bold")
	}
	// It clears when the next harvest works.
	p, _ = p.Update(HarvestedEvent{At: t0})
	if strings.Contains(plain(p.(Model).View().Lines[0]), "unavailable") {
		t.Errorf("a good harvest left the warning up")
	}

	noRoster := model(t, Config{CanReply: true, HasRepos: false}, 120, 10, item("aaa", inbox.Action, time.Hour, "row"))
	if !strings.Contains(plain(noRoster.View().Lines[0]), "merges not recorded: no roster") {
		t.Errorf("header = %q", plain(noRoster.View().Lines[0]))
	}
	first, cmds := NewModel(Config{CanReply: true, HasRepos: false}, 120, 10).Update(TickEvent{Now: t0})
	if count(cmds, CmdHarvest) != 0 {
		t.Errorf("a harvest with no repositories was started")
	}
	p2, cmds := first.Update(TickEvent{Now: t0.Add(time.Hour)})
	if count(cmds, CmdHarvest) != 0 {
		t.Errorf("a harvest with no repositories was started an hour later")
	}
	_ = noRoster
	p2, cmds = feed(t, p2, "r")
	if count(cmds, CmdHarvest) != 0 || !strings.Contains(plain(p2.View().Lines[9]), "merges are not being recorded") {
		t.Errorf("r with no roster: %v, bar %q", cmds, plain(p2.View().Lines[9]))
	}
}

func TestRecordedMergesReloadTheList(t *testing.T) {
	p, cmds := model(t, cfgReply, 80, 10).Update(HarvestedEvent{Added: 2, At: t0})
	if count(cmds, CmdLoad) != 1 || !strings.Contains(plain(p.View().Lines[9]), "recorded 2 PR merge(s)") {
		t.Errorf("cmds %v bar %q", cmds, plain(p.View().Lines[9]))
	}
}

func TestResolveDoneReloadsAndReports(t *testing.T) {
	p, cmds := model(t, cfgReply, 80, 10, item("aaa", inbox.Action, time.Hour, "row")).Update(DoneEvent{Kind: CmdResolve, ID: "aaa", OK: true, Note: "resolved aaa"})
	if count(cmds, CmdLoad) != 1 || !strings.Contains(plain(p.View().Lines[9]), "resolved aaa") {
		t.Errorf("cmds %v bar %q", cmds, plain(p.View().Lines[9]))
	}
	_, cmds = model(t, cfgReply, 80, 10).Update(DoneEvent{Kind: CmdPopup, ID: "aaa", OK: true})
	if count(cmds, CmdLoad) != 1 {
		t.Errorf("closing the popup must reload: %v", cmds)
	}
}

func TestSelectedRowIsPaintedAcrossTheWidth(t *testing.T) {
	m := model(t, cfgReply, 60, 10, item("aaa", inbox.Action, time.Hour, "first"), item("bbb", inbox.Unread, time.Hour, "second"))
	f := m.View()
	sel, other := rowOf(f, "first"), rowOf(f, "second")
	if !strings.Contains(sel, "48;5;236") || strings.Contains(other, "48;5;236") {
		t.Errorf("only the selected row has the selection background:\n%q\n%q", sel, other)
	}
	if got := cells(plain(sel)); got != 59 {
		t.Errorf("the selected row is %d cells wide, want the full 59", got)
	}
	if styleOf(t, sel, "first") != "\x1b[0;1;31;48;5;236m" {
		t.Errorf("the selected ACTION keeps its red on the selection background: %q", styleOf(t, sel, "first"))
	}
}
