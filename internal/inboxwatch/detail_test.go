package inboxwatch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// loadedDetail is a popup of 80x20 that has read e.
func loadedDetail(t *testing.T, canReply bool, e inbox.Entry) Detail {
	t.Helper()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = t0.Add(-3 * time.Hour)
	}
	var p Program = NewDetail(e.ID, canReply, 80, 20)
	p, cmds := p.Update(TickEvent{Now: t0})
	if len(cmds) != 1 || cmds[0].Kind != CmdEntry || cmds[0].ID != e.ID {
		t.Fatalf("the popup must ask for its entry first: %v", cmds)
	}
	p, cmds = p.Update(TickEvent{Now: t0.Add(time.Second)})
	if len(cmds) != 0 {
		t.Fatalf("and only once: %v", cmds)
	}
	p, _ = p.Update(EntryEvent{Entry: e, Found: true})
	return p.(Detail)
}

func TestDetailShowsBadgeStatusFieldsAndBody(t *testing.T) {
	e := inbox.Entry{ID: "a1b2c3d4e5", Type: inbox.Action, Title: "ship the widget-fleet eval as blocking?", Project: "widgets",
		Ref: "https://github.com/acme/widgets/issues/9", Body: "Some context here.\nDecide: blocking or advisory\nOptions: a, b"}
	d := loadedDetail(t, true, e)
	f := d.View()
	got := plainAll(f)
	for _, want := range []string{"ACTION  a1b2c3d4e5   3h old", "ship the widget-fleet eval as blocking?", "● waiting on you",
		"  project: widgets", "     ref: https://github.com/acme/widgets/issues/9", "Some context here."} {
		if !strings.Contains(got, want) {
			t.Errorf("detail lacks %q:\n%s", want, got)
		}
	}
	if s := styleOf(t, rowOf(f, "ACTION  a1b2"), "ACTION"); s != sgrRed[:len(sgrRed)] {
		t.Errorf("badge style %q", s)
	}
	if s := styleOf(t, rowOf(f, "Decide:"), "Decide"); s != "\x1b[0;1;33;49m" {
		t.Errorf("a Decide line is bold yellow, got %q", s)
	}
	if s := styleOf(t, rowOf(f, "Options:"), "Options"); s != "\x1b[0;1;33;49m" {
		t.Errorf("an Options line is bold yellow, got %q", s)
	}
	if s := styleOf(t, rowOf(f, "Some context"), "Some context"); s != "\x1b[0;39;49m" {
		t.Errorf("plain body text, got %q", s)
	}
	if s := styleOf(t, rowOf(f, "ref:"), "ref:"); s != "\x1b[0;36;49m" {
		t.Errorf("ref is cyan, got %q", s)
	}

	// An UNREAD is blue and has no "waiting on you".
	u := loadedDetail(t, true, inbox.Entry{ID: "u1", Type: inbox.Unread, Title: "done"})
	if s := styleOf(t, rowOf(u.View(), "UNREAD"), "UNREAD"); s != "\x1b[0;1;34;49m" || strings.Contains(plainAll(u.View()), "waiting on you") {
		t.Errorf("unread badge %q:\n%s", s, plainAll(u.View()))
	}

	// A reply the operator sent shows in yellow, with when.
	var p Program = d
	p, _ = p.Update(EntryEvent{Entry: e, Found: true, Reply: Reply{At: "2026-09-24T13:45:00", Text: "yes ship it"}, HasReply: true})
	got = plainAll(p.View())
	if !strings.Contains(got, "↩ you replied 13:45, waiting on the overseer:") || !strings.Contains(got, "  yes ship it") || strings.Contains(got, "waiting on you") {
		t.Errorf("replied detail:\n%s", got)
	}
	// A resolved one says when.
	res := time.Date(2026, 9, 24, 12, 30, 0, 0, time.UTC)
	e.ResolvedAt = &res
	p, _ = p.Update(EntryEvent{Entry: e, Found: true})
	if got = plainAll(p.View()); !strings.Contains(got, "✓ resolved 2026-09-24 12:30") {
		t.Errorf("resolved detail:\n%s", got)
	}
	// An id that is not there.
	p, _ = p.Update(EntryEvent{Found: false})
	if got = plainAll(p.View()); !strings.Contains(got, "no inbox entry a1b2c3d4e5") {
		t.Errorf("missing entry:\n%s", got)
	}
}

func TestDetailScrollsWithKeysAndWheel(t *testing.T) {
	var body []string
	for i := 0; i < 60; i++ {
		body = append(body, fmt.Sprintf("line %02d of the body", i))
	}
	var p Program = loadedDetail(t, true, inbox.Entry{ID: "a", Type: inbox.Unread, Title: "long", Body: strings.Join(body, "\n")})
	top := func() int { return p.(Detail).Top }

	p, _ = feed(t, p, "jj\x1b[B ")
	if top() != 4 {
		t.Errorf("j j down space: top %d, want 4", top())
	}
	p, _ = feed(t, p, "k\x1b[A")
	if top() != 2 {
		t.Errorf("k up: top %d, want 2", top())
	}
	p, _ = feed(t, p, "\x1b[<65;5;5M")
	if top() != 5 {
		t.Errorf("wheel down: top %d, want 5", top())
	}
	p, _ = feed(t, p, "\x1b[<64;5;5M\x1b[<64;5;5M")
	if top() != 0 {
		t.Errorf("wheel up: top %d, want 0", top())
	}
	if !strings.Contains(plainAll(p.View()), "line 00 of the body") {
		t.Errorf("top of the body missing")
	}
	p, _ = feed(t, p, strings.Repeat("j", 200))
	last := plainAll(p.View())
	if !strings.Contains(last, "line 59 of the body") || top() != len(p.(Detail).lines())-p.(Detail).bodyH() {
		t.Errorf("scrolling stops with the last line at the bottom: top %d", top())
	}
	p, _ = feed(t, p, "\x1b[<64;5;5M")
	if top() != len(p.(Detail).lines())-p.(Detail).bodyH()-3 {
		t.Errorf("wheel up from the bottom: top %d", top())
	}
}

func TestDetailDTwiceResolvesAndDOnceDoesNot(t *testing.T) {
	var p Program = loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t"})
	p, cmds := feed(t, p, "d")
	if len(cmds) != 0 || !strings.Contains(plain(p.View().Lines[19]), "press d again to resolve") {
		t.Fatalf("one d: cmds %v bar %q", cmds, plain(p.View().Lines[19]))
	}
	if p.Done() {
		t.Fatal("one d closed the popup")
	}
	q, cmds := feed(t, p, "d")
	if len(cmds) != 1 || cmds[0].Kind != CmdResolve || cmds[0].ID != "a1" {
		t.Fatalf("d d = %v", cmds)
	}
	// A failed resolve stays open and says why; a good one closes the popup.
	q2, _ := q.Update(DoneEvent{Kind: CmdResolve, ID: "a1", Note: `inbox entry "a1" was already resolved`})
	if q2.Done() || !strings.Contains(plain(q2.View().Lines[19]), "already resolved") {
		t.Errorf("failed resolve: done %v bar %q", q2.Done(), plain(q2.View().Lines[19]))
	}
	q3, _ := q.Update(DoneEvent{Kind: CmdResolve, ID: "a1", OK: true, Note: "resolved a1"})
	if !q3.Done() {
		t.Errorf("a resolved entry must close the popup")
	}
	// d, then anything else, disarms.
	p, _ = feed(t, p, "j")
	if _, cmds = feed(t, p, "d"); len(cmds) != 0 {
		t.Errorf("d j d resolved: %v", cmds)
	}
}

func TestDetailReplyFlow(t *testing.T) {
	var p Program = loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "ship it?"})
	p, cmds := feed(t, p, "r")
	if len(cmds) != 0 || !p.(Detail).Replying {
		t.Fatal("r opens the reply box")
	}
	f := p.View()
	if h := plain(f.Lines[19]); !strings.Contains(h, "⏎ send") || !strings.Contains(h, "Esc cancel") || !strings.Contains(h, "ctrl-u clear") {
		t.Errorf("hint = %q", h)
	}
	if !f.ShowCursor || f.CursorY != 18 || f.CursorX != 2 || !strings.HasPrefix(plain(f.Lines[18]), "> ") {
		t.Errorf("the cursor belongs after the prompt: %+v box %q", f, plain(f.Lines[18]))
	}
	// q types a q while replying, it does not quit; backspace and ctrl-u edit.
	p, _ = feed(t, p, "yes qq\x7f é\x15ok go ")
	if got := p.(Detail).Buf; got != "ok go " {
		t.Errorf("buf = %q, want ctrl-u to have cleared it", got)
	}
	if p.Done() {
		t.Fatal("typing a q quit the popup")
	}
	p, cmds = feed(t, p, "\r")
	if len(cmds) != 1 || cmds[0].Kind != CmdReply || cmds[0].Text != "ok go" || cmds[0].ID != "a1" || cmds[0].Note != "ship it?" {
		t.Fatalf("Enter = %v, want the trimmed text", cmds)
	}
	// The overseer did not get it: the reply box comes back with the text.
	p, _ = p.Update(RepliedEvent{Note: `no pane titled "lead"`})
	d := p.(Detail)
	if !d.Replying || d.Buf != "ok go " || !strings.Contains(plain(p.View().Lines[19]), "no pane titled") {
		t.Errorf("failed send: replying %v buf %q bar %q", d.Replying, d.Buf, plain(p.View().Lines[19]))
	}
	// Sent: the popup closes.
	p, _ = feed(t, p, "\r")
	p, _ = p.Update(RepliedEvent{OK: true, Note: "sent"})
	if !p.Done() {
		t.Errorf("a sent reply closes the popup")
	}
}

func TestDetailReplyCancelAndEmpty(t *testing.T) {
	var p Program = loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t"})
	p, _ = feed(t, p, "rabc\x1b")
	d := p.(Detail)
	if d.Replying || d.Buf != "" || p.Done() || !strings.Contains(plain(p.View().Lines[19]), "reply cancelled") {
		t.Errorf("Esc: replying %v buf %q done %v", d.Replying, d.Buf, p.Done())
	}
	p, cmds := feed(t, p, "r   \r")
	if len(cmds) != 0 || p.(Detail).Replying || !strings.Contains(plain(p.View().Lines[19]), "reply cancelled") {
		t.Errorf("an empty reply sends nothing: %v", cmds)
	}
	// Esc outside the reply box closes the popup, as q does.
	for _, k := range []string{"q", "\x1b"} {
		if q, _ := feed(t, p, k); !q.Done() {
			t.Errorf("%q did not close the popup", k)
		}
	}
}

func TestDetailLongReplyScrollsInsideItsBox(t *testing.T) {
	var p Program = loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t"})
	p, _ = feed(t, p, "r"+strings.Repeat("x", 600))
	d := p.(Detail)
	if n := len(d.box()); n != 20/3 {
		t.Errorf("box is %d rows, want it capped at a third of the screen (6)", n)
	}
	if f := d.View(); !f.ShowCursor || f.CursorY != 18 {
		t.Errorf("cursor %+v", f)
	}
}

func TestDetailOpenAndCopy(t *testing.T) {
	var p Program = loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: "https://github.com/a/b/pull/1"})
	if _, cmds := feed(t, p, "o"); len(cmds) != 1 || cmds[0].Kind != CmdOpen || cmds[0].Text != "https://github.com/a/b/pull/1" {
		t.Errorf("o = %v", cmds)
	}
	if _, cmds := feed(t, p, "c"); len(cmds) != 1 || cmds[0].Kind != CmdCopy || cmds[0].Text != "a1" {
		t.Errorf("c = %v", cmds)
	}
	plainRef := loadedDetail(t, true, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "t", Ref: "session:abc"})
	q, cmds := feed(t, plainRef, "o")
	if len(cmds) != 0 || !strings.Contains(plain(q.View().Lines[19]), "no link on this item") {
		t.Errorf("a session: ref is plain text: %v", cmds)
	}
}

func TestWrapMatchesPythonsTextwrap(t *testing.T) {
	for _, tc := range []struct {
		in   string
		w    int
		want []string
	}{
		{"", 10, []string{""}},
		{"short", 10, []string{"short"}},
		{"the quick brown fox jumps", 10, []string{"the quick", "brown fox", "jumps"}},
		{"abcdefghijklmnop", 6, []string{"abcdef", "ghijkl", "mnop"}},
		{"  indented first line", 30, []string{"  indented first line"}},
		{"a  b", 10, []string{"a  b"}},
		{"trailing   ", 20, []string{"trailing"}},
		{"one two\tthree", 8, []string{"one two", "three"}},
	} {
		got := wrap(tc.in, tc.w)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("wrap(%q, %d) = %q, want %q", tc.in, tc.w, got, tc.want)
		}
	}
}
