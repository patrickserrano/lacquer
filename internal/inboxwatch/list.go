package inboxwatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// Timings. The inbox is a local file, so re-reading it every few seconds is
// free. The merge harvest is not: it asks GitHub about every roster repository,
// and at sixteen repositories a harvest per refresh would spend about 3,800 of
// the 5,000 core calls an hour that every agent's gh shares. So it runs at most
// once per HarvestEvery, and `r` forces one.
const (
	RefreshEvery = 3 * time.Second
	HarvestEvery = 5 * time.Minute

	wheelStep = 3
	// tabRows and footRows are the rows the list does not use: the tab strip and
	// its rule above, the hint bar below.
	tabRows  = 2
	footRows = 1
)

// Program is one screen: a state that an Event turns into a new state and some
// Cmds, and a Frame for the state. None of it touches the terminal.
type Program interface {
	Update(Event) (Program, []Cmd)
	View() Frame
	Done() bool
}

// Frame is what to show: one entry per screen row, already styled, and where the
// cursor goes when it is shown.
type Frame struct {
	Lines            []string
	CursorX, CursorY int
	ShowCursor       bool
}

// Tab is one tab in the strip. Which of the four it is decides what it shows;
// the strip's layout, key handling and hit testing do not care.
type Tab struct {
	Key   rune
	Label string
	Kind  TabKind
}

// InboxTab is the first tab, and the only one a Config with none gets.
var InboxTab = Tab{Key: '1', Label: "Inbox", Kind: KindInbox}

// Config is what a Model is told about how it was started.
type Config struct {
	// Tabs is the strip, left to right. Tabs[0] is shown first.
	Tabs []Tab
	// CanReply is whether an overseer pane is configured. It changes the hint,
	// which then says why a reply is off; the popup enforces it.
	CanReply bool
	// HasRepos is whether the roster names a repository to harvest merges from.
	HasRepos bool
}

// Model is the live list.
type Model struct {
	Cfg  Config
	W, H int
	Now  time.Time

	Items  []Item
	Sel    int
	SelID  string
	Top    int
	Active int // index into Cfg.Tabs

	LoadedAt  time.Time
	LoadReqAt time.Time
	LoadErr   string
	Warn      string
	Malformed int

	Harvesting  bool
	HarvestedAt time.Time // when the last harvest was started
	HarvestErr  string

	Arm     string  // the id (or, on Later, the ref) a first d has armed
	ArmKind TabKind // which tab's row it is
	Note    string
	quit    bool

	Later  LaterState
	Closed DoneState
	PRs    PRsState
}

// NewModel is an empty list of a given size.
func NewModel(cfg Config, w, h int) Model {
	if len(cfg.Tabs) == 0 {
		cfg.Tabs = []Tab{InboxTab}
	}
	return Model{Cfg: cfg, W: w, H: h}
}

func (m Model) Done() bool { return m.quit }

func (m Model) viewH() int { return max(m.H-tabRows-footRows, 1) }

func (m *Model) clampTop() {
	m.Top = max(0, min(m.Top, max(len(m.Items)-m.viewH(), 0)))
}

func (m *Model) selectAt(i int) {
	if len(m.Items) == 0 {
		m.Sel, m.SelID = 0, ""
		return
	}
	m.Sel = max(0, min(i, len(m.Items)-1))
	m.SelID = m.Items[m.Sel].ID
	if m.Sel < m.Top {
		m.Top = m.Sel
	} else if m.Sel >= m.Top+m.viewH() {
		m.Top = m.Sel - m.viewH() + 1
	}
	m.clampTop()
}

func (m Model) selected() (Item, bool) {
	if m.Sel < 0 || m.Sel >= len(m.Items) {
		return Item{}, false
	}
	return m.Items[m.Sel], true
}

// Update implements Program.
func (m Model) Update(ev Event) (Program, []Cmd) {
	cmds := m.update(ev)
	return m, cmds
}

func (m *Model) update(ev Event) []Cmd {
	switch ev := ev.(type) {
	case ResizeEvent:
		m.W, m.H = ev.W, ev.H
		m.selectAt(m.Sel)
		vh := m.viewH()
		m.Later.keep(m.laterRows(), vh, 1)
		m.Closed.keep(m.doneRows(), vh, 0)
		m.PRs.keep(m.prRows(), vh, 1)
	case LaterEvent:
		return m.laterLoaded(ev)
	case PRsEvent:
		return m.prsLoaded(ev)
	case TickEvent:
		return m.tick(ev.Now)
	case LoadedEvent:
		m.loaded(ev)
	case DoneEvent:
		return m.done(ev)
	case HarvestedEvent:
		return m.harvested(ev)
	case KeyEvent:
		return m.key(ev)
	case MouseEvent:
		return m.mouse(ev)
	}
	return nil
}

func (m *Model) tick(now time.Time) []Cmd {
	m.Now = now
	var cmds []Cmd
	if m.LoadReqAt.IsZero() || now.Sub(m.LoadReqAt) >= RefreshEvery {
		m.LoadReqAt = now
		cmds = append(cmds, Cmd{Kind: CmdLoad})
	}
	if m.Cfg.HasRepos && !m.Harvesting && (m.HarvestedAt.IsZero() || now.Sub(m.HarvestedAt) >= HarvestEvery) {
		m.Harvesting, m.HarvestedAt = true, now
		cmds = append(cmds, Cmd{Kind: CmdHarvest})
	}
	return append(cmds, m.fetches(now)...)
}

// fetches is the GitHub asks that are due: only for the tab being looked at,
// and each at most once per its throttle. `r` and what the operator does are
// forced separately.
func (m *Model) fetches(now time.Time) []Cmd {
	switch m.kind() {
	case KindLater:
		if m.Later.due(now, LaterEvery) {
			m.Later.start(now)
			return []Cmd{{Kind: CmdLater}}
		}
	case KindPRs:
		if m.PRs.due(now, PRsEvery) {
			m.PRs.start(now)
			return []Cmd{{Kind: CmdPRs}}
		}
	}
	return nil
}

func (m *Model) laterLoaded(ev LaterEvent) []Cmd {
	again := m.Later.answered(ev.At, ev.Err)
	if ev.Err == "" { // on a failure the last good rows stay
		m.Later.Issues = ev.Issues
		m.Later.keep(m.laterRows(), m.viewH(), 1)
		if m.Arm != "" && m.ArmKind == KindLater && !m.hasLater(m.Arm) {
			m.Arm = ""
		}
	}
	if again {
		m.Later.start(m.Now)
		return []Cmd{{Kind: CmdLater}}
	}
	return nil
}

func (m *Model) prsLoaded(ev PRsEvent) []Cmd {
	again := m.PRs.answered(ev.At, ev.Err)
	if ev.Err == "" {
		m.PRs.PRs, m.PRs.Errors, m.PRs.Full = ev.PRs, ev.Errors, ev.Full
		m.PRs.keep(m.prRows(), m.viewH(), 1)
	}
	if again {
		m.PRs.start(m.Now)
		return []Cmd{{Kind: CmdPRs}}
	}
	return nil
}

func (m Model) hasLater(ref string) bool {
	for _, i := range m.Later.Issues {
		if i.Ref() == ref {
			return true
		}
	}
	return false
}

func (m *Model) loaded(ev LoadedEvent) {
	if ev.At.Before(m.LoadedAt) {
		return // a slow read that started before the last one applied: it would bring back what that one dropped
	}
	m.LoadedAt = ev.At
	m.LoadErr, m.Warn = ev.Err, ev.Warn
	if ev.Err != "" {
		return // keep showing the last good read
	}
	m.Items, m.Malformed = ev.Data.Items, ev.Data.Malformed
	m.Closed.Items = ev.Data.Done
	m.Closed.keep(m.doneRows(), m.viewH(), 0)
	idx := m.Sel
	for i, it := range m.Items {
		if it.ID == m.SelID {
			idx = i
			break
		}
	}
	m.selectAt(idx)
	if m.Arm != "" && m.ArmKind == KindInbox && !m.has(m.Arm) {
		m.Arm = ""
	}
}

func (m Model) has(id string) bool {
	for _, it := range m.Items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func (m *Model) done(ev DoneEvent) []Cmd {
	switch ev.Kind {
	case CmdResolve, CmdPopup:
		if ev.Note != "" {
			m.Note = ev.Note
		}
		m.LoadReqAt = m.Now
		return []Cmd{{Kind: CmdLoad}}
	case CmdUnpark, CmdPopupIssue:
		if ev.Note != "" {
			m.Note = ev.Note
		}
		if !ev.OK {
			return nil
		}
		// The issue may have been un-parked, from here or in the popup, so the
		// last answer is out of date whatever the throttle says.
		if c, ok := m.Later.force(m.Now, CmdLater); ok {
			return []Cmd{c}
		}
		return nil
	}
	m.Note = ev.Note
	return nil
}

func (m *Model) harvested(ev HarvestedEvent) []Cmd {
	m.Harvesting = false
	if len(ev.Unavailable) > 0 {
		m.HarvestErr = strings.Join(ev.Unavailable, "; ")
		m.Note = "merge harvest unavailable: " + m.HarvestErr
		return nil
	}
	m.HarvestErr = ""
	if ev.Added > 0 {
		m.Note = fmt.Sprintf("recorded %d PR merge(s)", ev.Added)
		m.LoadReqAt = m.Now
		return []Cmd{{Kind: CmdLoad}}
	}
	return nil
}

func (m *Model) key(k KeyEvent) []Cmd {
	arm := m.Arm
	m.Note = ""
	if !(k.Key == KeyRune && k.Rune == 'd') {
		m.Arm = ""
	}
	switch k.Key {
	case KeyDown:
		m.move(1)
	case KeyUp:
		m.move(-1)
	case KeyEnter:
		return m.enter()
	case KeyTab, KeyBackTab:
		step := 1
		if k.Key == KeyBackTab {
			step = -1
		}
		m.Active = (m.Active + step + len(m.Cfg.Tabs)) % len(m.Cfg.Tabs)
		return m.fetches(m.Now)
	case KeyCtrlC: // not Esc: foxy-inbox's inbox tab ignores it, and a stray one must not close the view
		m.quit = true
	case KeyRune:
		return m.rune(k.Rune, arm)
	}
	return nil
}

// move is j, k and the arrows: the selection on the active tab, by delta rows.
func (m *Model) move(delta int) {
	if c := m.cur(); c != nil {
		rows, ctx := m.rows()
		c.selectAt(rows, c.Sel+delta, m.viewH(), ctx)
		return
	}
	m.selectAt(m.Sel + delta)
}

// enter is Enter: the detail of an entry or an issue, or the PR on GitHub.
func (m *Model) enter() []Cmd {
	switch m.kind() {
	case KindLater:
		if it, ok := m.selectedLater(); ok {
			return []Cmd{{Kind: CmdPopupIssue, ID: it.Ref()}}
		}
	case KindDone:
		if it, ok := m.selectedDone(); ok {
			return []Cmd{{Kind: CmdPopup, ID: it.ID}}
		}
	case KindPRs:
		return m.openPR()
	default:
		if it, ok := m.selected(); ok {
			return []Cmd{{Kind: CmdPopup, ID: it.ID}}
		}
	}
	return nil
}

func (m Model) selectedDone() (DoneItem, bool) {
	rows := m.doneRows()
	i, ok := m.Closed.at(rows)
	if !ok {
		return DoneItem{}, false
	}
	for _, it := range m.Closed.Items {
		if it.ID == rows[i].key {
			return it, true
		}
	}
	return DoneItem{}, false
}

// open is a Cmd that opens url, or a note saying there is nothing to open.
func (m *Model) open(url, label, none string) []Cmd {
	if !isLink(url) {
		m.Note = none
		return nil
	}
	return []Cmd{{Kind: CmdOpen, Text: url, Label: label}}
}

func (m *Model) openPR() []Cmd {
	if p, ok := m.selectedPR(); ok {
		return m.open(p.URL, p.Key(), "no link on this pull request")
	}
	return nil
}

func (m *Model) rune(r rune, arm string) []Cmd {
	switch r {
	case 'j':
		m.move(1)
	case 'k':
		m.move(-1)
	case 'q':
		m.quit = true
	case 'r':
		return m.refresh()
	case 'c':
		return m.copy()
	case 'o':
		return m.openSelected()
	case 'd':
		return m.armKey(arm)
	default:
		for i, t := range m.Cfg.Tabs {
			if r == t.Key {
				m.Active = i
				return m.fetches(m.Now)
			}
		}
	}
	return nil
}

func (m *Model) copy() []Cmd {
	switch m.kind() {
	case KindLater:
		if it, ok := m.selectedLater(); ok {
			return []Cmd{{Kind: CmdCopy, ID: it.Ref(), Text: it.URL}}
		}
	case KindDone:
		if it, ok := m.selectedDone(); ok {
			return []Cmd{{Kind: CmdCopy, ID: it.ID, Text: it.ID}}
		}
	case KindPRs:
		if p, ok := m.selectedPR(); ok {
			return []Cmd{{Kind: CmdCopy, ID: p.Key(), Text: p.URL}}
		}
	default:
		if it, ok := m.selected(); ok {
			return []Cmd{{Kind: CmdCopy, ID: it.ID, Text: it.ID}}
		}
	}
	return nil
}

func (m *Model) openSelected() []Cmd {
	switch m.kind() {
	case KindLater:
		if it, ok := m.selectedLater(); ok {
			return m.open(it.URL, it.Ref(), "no link on this issue")
		}
	case KindDone:
		if it, ok := m.selectedDone(); ok {
			return m.open(it.Ref, "", "no link on this item")
		}
	case KindPRs:
		return m.openPR()
	default:
		if it, ok := m.selected(); ok {
			return m.open(it.Ref, "", "no link on this item")
		}
	}
	return nil
}

// armKey is d. On the inbox it resolves, on Later it un-parks; each needs the
// key twice, with nothing between, and an armed d is undone by any other key.
func (m *Model) armKey(arm string) []Cmd {
	switch m.kind() {
	case KindLater:
		it, ok := m.selectedLater()
		if !ok {
			return nil
		}
		ref := it.Ref()
		if arm == ref && m.ArmKind == KindLater {
			m.Arm = "" // a failed un-park must not leave the next d removing the label at once
			return []Cmd{{Kind: CmdUnpark, ID: ref}}
		}
		m.Arm, m.ArmKind = ref, KindLater
		m.Note = fmt.Sprintf("press d again to take %s off Later (the issue stays open)", ref)
	case KindInbox:
		it, ok := m.selected()
		if !ok {
			return nil
		}
		if arm == it.ID && m.ArmKind == KindInbox {
			m.Arm = "" // a failed resolve must not leave the next d resolving at once
			return []Cmd{{Kind: CmdResolve, ID: it.ID}}
		}
		m.Arm, m.ArmKind = it.ID, KindInbox
		m.Note = fmt.Sprintf("press d again to resolve %s (any other key cancels)", it.ID)
	}
	return nil
}

// refresh is `r`. On the inbox and Done it re-reads the inbox now and harvests
// now, whatever the throttle says; on Later and PRs it asks GitHub again now.
func (m *Model) refresh() []Cmd {
	switch m.kind() {
	case KindLater:
		return m.forced(&m.Later.fetchState, CmdLater)
	case KindPRs:
		return m.forced(&m.PRs.fetchState, CmdPRs)
	}
	m.LoadReqAt = m.Now
	cmds := []Cmd{{Kind: CmdLoad}}
	switch {
	case !m.Cfg.HasRepos:
		m.Note = "merges are not being recorded: " + noRosterWhy
	case m.Harvesting:
		m.Note = "a merge harvest is already running"
	default:
		m.Harvesting, m.HarvestedAt = true, m.Now
		m.Note = "refreshing, and harvesting PR merges"
		cmds = append(cmds, Cmd{Kind: CmdHarvest})
	}
	return cmds
}

func (m *Model) forced(f *fetchState, kind CmdKind) []Cmd {
	c, ok := f.force(m.Now, kind)
	if !ok {
		m.Note = "already asking GitHub"
		return nil
	}
	m.Note = "asking GitHub"
	return []Cmd{c}
}

// noRosterWhy is why, with no repositories to look at, merges go unrecorded.
const noRosterWhy = "no roster names a repository (pass --roster or set $LACQUER_ROSTER)"

// isLink is true for the refs that can be opened: only http(s) ones are links.
// "#374", "session:..." and the like are plain text.
func isLink(ref string) bool {
	return strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
}

func (m *Model) mouse(e MouseEvent) []Cmd {
	m.Note, m.Arm = "", ""
	switch e.Button {
	case ButtonWheelUp, ButtonWheelDown:
		step := wheelStep
		if e.Button == ButtonWheelUp {
			step = -wheelStep
		}
		if c := m.cur(); c != nil {
			rows, ctx := m.rows()
			c.wheel(rows, step, m.viewH(), ctx)
			return nil
		}
		m.Top += step
		m.clampTop()
		// The cursor stays on screen, so the next j or k does not snap the view back.
		if m.Sel < m.Top {
			m.selectAt(m.Top)
		} else if m.Sel >= m.Top+m.viewH() {
			m.selectAt(m.Top + m.viewH() - 1)
		}
	case ButtonLeft:
		if e.Y == 0 {
			for _, h := range m.tabHits() {
				if e.X >= h.x0 && e.X < h.x1 {
					m.Active = h.tab
				}
			}
			return m.fetches(m.Now)
		}
		if c := m.cur(); c != nil {
			rows, ctx := m.rows()
			i, ok := c.clickRow(rows, e.Y, m.viewH())
			if !ok {
				return nil
			}
			c.selectAt(rows, i, m.viewH(), ctx)
			return m.enter() // a click is Enter on that row
		}
		row := e.Y - tabRows
		if row < 0 || row >= m.viewH() || m.Top+row >= len(m.Items) {
			return nil
		}
		m.selectAt(m.Top + row)
		return []Cmd{{Kind: CmdPopup, ID: m.Items[m.Sel].ID}}
	}
	return nil
}

type hit struct{ x0, x1, tab int }

// tabHits are the columns each tab occupies on row 0.
func (m Model) tabHits() []hit {
	x := 1
	var hits []hit
	for i, t := range m.Cfg.Tabs {
		w := cells(tabLabel(t)) + 2
		hits = append(hits, hit{x, x + w, i})
		x += w + 1
	}
	return hits
}

func tabLabel(t Tab) string { return string(t.Key) + " " + t.Label }

// Age is "45m", "3h" or "2d" for how long ago created was; "" when it has no
// time. It counts hours up to two days, as foxy-inbox does.
func Age(created, now time.Time) string {
	if created.IsZero() {
		return ""
	}
	mins := max(int(now.Sub(created)/time.Minute), 0)
	switch {
	case mins < 60:
		return fmt.Sprintf("%dm", mins)
	case mins < 48*60:
		return fmt.Sprintf("%dh", mins/60)
	}
	return fmt.Sprintf("%dd", mins/1440)
}

// StaleAfter is when an age turns magenta: exactly when it renders in days.
// foxy-inbox's docstring and #409 say "a day", but its code colours the age
// magenta only when the label ends in "d", which is from 48h; that is what the
// operator has looked at every day, so that is what this matches.
const StaleAfter = 48 * time.Hour

func (it Item) kindStyle() (mark string, markStyle, titleStyle style) {
	switch {
	case it.Type == inbox.Action && !it.Replied:
		return "●", fgBold(red), fgBold(red)
	case it.Replied:
		return iconReplied, fg(yellow), fg(yellow)
	}
	// An FYI is plain text, so only what needs the operator carries colour.
	return "·", fg(dim), fg(def)
}

// View implements Program.
func (m Model) View() Frame {
	w, h := m.W, m.H
	cw := max(w-1, 0) // foxy-inbox never writes the last column
	lines := make([]string, h)
	set := func(y int, l line, selected bool) {
		if y >= 0 && y < h {
			lines[y] = l.render(cw, selected)
		}
	}

	set(0, m.tabRow(w), false)
	total, top := len(m.Items), m.Top
	if c := m.cur(); c != nil {
		rows, _ := m.rows()
		total, top = len(rows), c.Top
	}
	set(1, m.ruleRow(cw, total, top), false)

	vh := m.viewH()
	if c := m.cur(); c != nil {
		m.viewList(set, *c, vh)
	} else {
		m.viewInbox(set, vh)
	}

	switch {
	case m.Note != "" || m.Arm != "":
		st := fg(dim)
		if m.Arm != "" {
			st = style{fg: red, bg: def, reverse: true}
		}
		set(h-1, line{{m.Note, st}}, false)
	default:
		set(h-1, hint(m.hintFor()), false)
	}
	return Frame{Lines: lines}
}

// viewList draws Later, Done or PRs: their rows, or, with none, why not.
func (m Model) viewList(set func(int, line, bool), c cursor, vh int) {
	rows, _ := m.rows()
	cur, _ := c.at(rows)
	for k := 0; k < vh && c.Top+k < len(rows); k++ {
		i := c.Top + k
		set(tabRows+k, rows[i].l, !rows[i].header && i == cur)
	}
	if len(rows) == 0 {
		set(tabRows, line{{m.emptyMessage(), fg(dim)}}, false)
	}
}

// emptyMessage is what a tab with no rows says. It is never the same words for
// "there are none" and "could not find out": each says which.
func (m Model) emptyMessage() string {
	switch m.kind() {
	case KindLater:
		return m.laterEmpty()
	case KindPRs:
		return m.prsEmpty()
	case KindDone:
		switch {
		case m.LoadErr != "":
			return "the inbox could not be read: " + m.LoadErr
		case m.LoadedAt.IsZero():
			return "loading…"
		}
		return "nothing closed yet"
	}
	return ""
}

func (m Model) viewInbox(set func(int, line, bool), vh int) {
	for k := 0; k < vh && m.Top+k < len(m.Items); k++ {
		i := m.Top + k
		it := m.Items[i]
		mark, ms, ts := it.kindStyle()
		a := Age(it.CreatedAt, m.Now)
		as := fg(dim)
		if !it.CreatedAt.IsZero() && m.Now.Sub(it.CreatedAt) >= StaleAfter {
			as = fg(magenta)
		}
		set(tabRows+k, line{
			{" " + mark + " ", ms},
			{fmt.Sprintf("%4s ", a), as},
			{clean(it.ID) + "  ", fg(dim)},
			{clean(it.Title), ts},
		}, i == m.Sel)
	}
	if len(m.Items) == 0 {
		msg := "inbox clear — nothing waiting on you"
		for _, t := range m.Cfg.Tabs {
			if t.Kind == KindDone {
				msg += fmt.Sprintf(" (%s for what closed)", tabLabel(t))
			}
		}
		if m.LoadErr != "" {
			msg = "the inbox could not be read: " + m.LoadErr
		}
		set(tabRows, line{{msg, fg(dim)}}, false)
	}
}

func (m Model) hint() string {
	detail := "⏎ detail+reply"
	if !m.Cfg.CanReply {
		detail = "⏎ detail (reply off: no overseer pane)"
	}
	parts := []string{detail, "d resolve", "o link", "c copy id"}
	if len(m.Cfg.Tabs) > 1 {
		parts = append(parts, "Tab next tab")
	}
	parts = append(parts, "r refresh", "q quit")
	return strings.Join(parts, " · ")
}

// tabRow is the strip on row 0: the active tab a peach pill, the rest dim text,
// then the status, then the counts as pills against the right edge.
func (m Model) tabRow(w int) line {
	l := line{{" ", fg(def)}}
	for i, t := range m.Cfg.Tabs {
		if i > 0 {
			l = l.add(" ", fg(def))
		}
		if i == m.Active {
			l = append(l, pill(tabLabel(t), peach, "")...)
		} else {
			l = l.add(" "+tabLabel(t)+" ", fg(dim))
		}
	}
	switch m.kind() {
	case KindLater:
		st := m.laterStatus()
		l = l.add(st.text, st.st).add(timeMark(m.Later.At, "15:04"), fg(dim))
		return l
	case KindPRs:
		st := m.prsStatus()
		l = l.add(st.text, st.st).add(timeMark(m.PRs.At, "15:04"), fg(dim))
		return l
	case KindDone:
		replied := 0
		for _, it := range m.Closed.Items {
			if it.Replied {
				replied++
			}
		}
		l = l.add(fmt.Sprintf(" %d closed", len(m.Closed.Items)), fgBold(green)).add(" · ", fg(def)).
			add(fmt.Sprintf("%d you answered", replied), fgBold(yellow))
		l = l.add(timeMark(m.LoadedAt, "15:04:05"), fg(dim))
		if m.LoadErr != "" {
			l = l.add(" inbox unavailable", fgBold(red))
		}
		return l
	}
	l = l.add(timeMark(m.LoadedAt, "15:04"), fg(dim))
	for _, s := range m.status() {
		l = append(l, s)
	}

	waiting, answered := 0, 0
	for _, it := range m.Items {
		if it.Replied {
			answered++
		} else if it.Type == inbox.Action {
			waiting++
		}
	}
	fyi := len(m.Items) - waiting - answered
	type count struct {
		label, icon string
		accent      int
	}
	var counts []count
	if waiting > 0 {
		counts = append(counts, count{fmt.Sprintf("%d need you", waiting), iconAction, red})
	} else {
		counts = append(counts, count{"inbox clear", iconClear, green})
	}
	if answered > 0 {
		counts = append(counts, count{fmt.Sprintf("%d replied", answered), iconReplied, yellow})
	}
	if fyi > 0 {
		counts = append(counts, count{fmt.Sprintf("%d fyi", fyi), iconInfo, blue})
	}
	total := 0
	for _, c := range counts {
		total += pillWidth(c.label, c.icon) + 1
	}
	px := max(l.width()+2, w-1-total)
	l = l.add(strings.Repeat(" ", px-l.width()), fg(def))
	for _, c := range counts {
		l = append(l, pill(c.label, c.accent, c.icon)...)
		l = l.add(" ", fg(def))
	}
	return l
}

// status is what the header says about the inbox and the harvest. None of it is
// silent: a read that failed, a harvest that could not run and a roster that
// gives the harvest nothing to watch each show.
func (m Model) status() []seg {
	var out []seg
	if m.LoadErr != "" {
		out = append(out, seg{" inbox unavailable", fgBold(red)})
	}
	if m.Warn != "" {
		out = append(out, seg{" " + m.Warn, fg(yellow)})
	}
	switch {
	case !m.Cfg.HasRepos:
		out = append(out, seg{" merges not recorded: no roster", fg(yellow)})
	case m.HarvestErr != "":
		out = append(out, seg{" merge harvest unavailable", fgBold(red)})
	}
	if m.Malformed > 0 {
		out = append(out, seg{fmt.Sprintf(" %d malformed line(s) skipped", m.Malformed), fg(dim)})
	}
	return out
}

// ruleRow is the rule under the tabs, with "4–9 of 14" at its right end when the
// list is longer than the view, so a cut-off list never looks complete.
func (m Model) ruleRow(cw, total, top int) line {
	vh := m.viewH()
	r := []rune(strings.Repeat("─", cw))
	if total <= vh {
		return line{{string(r), fg(rule)}}
	}
	mark := fmt.Sprintf(" %d–%d of %d ", top+1, min(top+vh, total), total)
	x := max(cw-1-len([]rune(mark)), 0)
	var l line
	l = l.add(string(r[:x]), fg(rule))
	l = l.add(mark, fg(dim))
	if end := x + len([]rune(mark)); end < len(r) {
		l = l.add(string(r[end:]), fg(rule))
	}
	return l
}

// timeMark is "   HH:MM" for when a tab was last read, and nothing before it was.
func timeMark(t time.Time, layout string) string {
	if t.IsZero() {
		return ""
	}
	return "   " + t.Format(layout)
}
