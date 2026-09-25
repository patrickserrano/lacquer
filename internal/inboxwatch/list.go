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

// Tab is one tab in the strip. 409b adds Later, PRs and Completed as more Tabs;
// nothing else in the strip's layout, key handling or hit testing is about the
// Inbox in particular.
type Tab struct {
	Key   rune
	Label string
}

// InboxTab is the tab this unit ships.
var InboxTab = Tab{Key: '1', Label: "Inbox"}

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

	Arm  string // the id a first d has armed
	Note string
	quit bool
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
	return cmds
}

func (m *Model) loaded(ev LoadedEvent) {
	m.LoadedAt = ev.At
	m.LoadErr, m.Warn = ev.Err, ev.Warn
	if ev.Err != "" {
		return // keep showing the last good read
	}
	m.Items, m.Malformed = ev.Data.Items, ev.Data.Malformed
	idx := m.Sel
	for i, it := range m.Items {
		if it.ID == m.SelID {
			idx = i
			break
		}
	}
	m.selectAt(idx)
	if m.Arm != "" && !m.has(m.Arm) {
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
		m.selectAt(m.Sel + 1)
	case KeyUp:
		m.selectAt(m.Sel - 1)
	case KeyEnter:
		if it, ok := m.selected(); ok {
			return []Cmd{{Kind: CmdPopup, ID: it.ID}}
		}
	case KeyTab, KeyBackTab:
		step := 1
		if k.Key == KeyBackTab {
			step = -1
		}
		m.Active = (m.Active + step + len(m.Cfg.Tabs)) % len(m.Cfg.Tabs)
	case KeyEsc, KeyCtrlC:
		m.quit = true
	case KeyRune:
		return m.rune(k.Rune, arm)
	}
	return nil
}

func (m *Model) rune(r rune, arm string) []Cmd {
	switch r {
	case 'j':
		m.selectAt(m.Sel + 1)
	case 'k':
		m.selectAt(m.Sel - 1)
	case 'q':
		m.quit = true
	case 'r':
		return m.refresh()
	case 'c':
		if it, ok := m.selected(); ok {
			return []Cmd{{Kind: CmdCopy, ID: it.ID, Text: it.ID}}
		}
	case 'o':
		it, ok := m.selected()
		if !ok {
			return nil
		}
		if !isLink(it.Ref) {
			m.Note = "no link on this item"
			return nil
		}
		return []Cmd{{Kind: CmdOpen, Text: it.Ref}}
	case 'd':
		it, ok := m.selected()
		if !ok {
			return nil
		}
		if arm == it.ID {
			return []Cmd{{Kind: CmdResolve, ID: it.ID}}
		}
		m.Arm = it.ID
		m.Note = fmt.Sprintf("press d again to resolve %s (any other key cancels)", it.ID)
	default:
		for i, t := range m.Cfg.Tabs {
			if r == t.Key {
				m.Active = i
			}
		}
	}
	return nil
}

// refresh is `r`: re-read the inbox now, and harvest now, whatever the throttle says.
func (m *Model) refresh() []Cmd {
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
			return nil
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
	set(1, m.ruleRow(cw), false)

	vh := m.viewH()
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
			{it.ID + "  ", fg(dim)},
			{it.Title, ts},
		}, i == m.Sel)
	}
	if len(m.Items) == 0 {
		msg := "inbox clear — nothing waiting on you"
		if m.LoadErr != "" {
			msg = "the inbox could not be read: " + m.LoadErr
		}
		set(tabRows, line{{msg, fg(dim)}}, false)
	}

	switch {
	case m.Note != "" || m.Arm != "":
		st := fg(dim)
		if m.Arm != "" {
			st = style{fg: red, bg: def, reverse: true}
		}
		set(h-1, line{{m.Note, st}}, false)
	default:
		set(h-1, hint(m.hint()), false)
	}
	return Frame{Lines: lines}
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
	if !m.LoadedAt.IsZero() {
		l = l.add("   "+m.LoadedAt.Format("15:04"), fg(dim))
	}
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
func (m Model) ruleRow(cw int) line {
	total, vh := len(m.Items), m.viewH()
	r := []rune(strings.Repeat("─", cw))
	if total <= vh {
		return line{{string(r), fg(rule)}}
	}
	mark := fmt.Sprintf(" %d–%d of %d ", m.Top+1, min(m.Top+vh, total), total)
	x := max(cw-1-len([]rune(mark)), 0)
	var l line
	l = l.add(string(r[:x]), fg(rule))
	l = l.add(mark, fg(dim))
	if end := x + len([]rune(mark)); end < len(r) {
		l = l.add(string(r[end:]), fg(rule))
	}
	return l
}
