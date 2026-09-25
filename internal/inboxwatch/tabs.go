package inboxwatch

import (
	"fmt"
	"strings"
	"time"
)

// TabKind says what a tab shows.
type TabKind int

const (
	KindInbox TabKind = iota // the zero value, so a Tab without a Kind is the inbox
	KindLater
	KindDone
	KindPRs
)

// The four tabs of foxy-inbox, in its order and with its keys.
var (
	LaterTab = Tab{Key: '2', Label: "Later", Kind: KindLater}
	DoneTab  = Tab{Key: '3', Label: "Done", Kind: KindDone}
	PRsTab   = Tab{Key: '4', Label: "PRs", Kind: KindPRs}
	AllTabs  = []Tab{InboxTab, LaterTab, DoneTab, PRsTab}
)

// Refresh throttles for the tabs that ask GitHub. Both are foxy-inbox's: `gh`
// search is rate limited, and the PR sweep is one `gh` process per repository.
const (
	LaterEvery = 5 * time.Minute
	PRsEvery   = 5 * time.Minute
	// StalePR is the operator's standing rule: no PR open longer than this.
	StalePR = 24 * time.Hour
)

// row is one line of a list: a project header, or something to select.
type row struct {
	header bool
	key    string // what identifies a selectable row across refreshes
	l      line
}

// cursor is which selectable row is selected and how far the list is scrolled.
type cursor struct {
	Sel int    // among the selectable rows
	Key string // the selected row's key, so a refresh keeps the cursor on it
	Top int    // the first row shown
}

func selectable(rows []row) []int {
	var out []int
	for i, r := range rows {
		if !r.header {
			out = append(out, i)
		}
	}
	return out
}

// at is the index in rows of the selected row.
func (c cursor) at(rows []row) (int, bool) {
	sel := selectable(rows)
	if c.Sel < 0 || c.Sel >= len(sel) {
		return 0, false
	}
	return sel[c.Sel], true
}

func (c *cursor) clamp(rows []row, vh int) {
	c.Top = max(0, min(c.Top, max(len(rows)-vh, 0)))
}

// keep is called after the rows change: the cursor stays on the row it was on,
// or on the same place in the list when that row is gone.
func (c *cursor) keep(rows []row, vh, context int) {
	sel := selectable(rows)
	idx := c.Sel
	for i, r := range sel {
		if rows[r].key == c.Key {
			idx = i
			break
		}
	}
	c.selectAt(rows, idx, vh, context)
}

// selectAt moves the selection to the i-th selectable row and scrolls it into
// view. context is how many rows above it to keep in view when scrolling up: a
// grouped list keeps the project header.
func (c *cursor) selectAt(rows []row, i, vh, context int) {
	sel := selectable(rows)
	if len(sel) == 0 {
		c.Sel, c.Key = 0, ""
		c.clamp(rows, vh)
		return
	}
	c.Sel = max(0, min(i, len(sel)-1))
	r := sel[c.Sel]
	c.Key = rows[r].key
	if r < c.Top {
		c.Top = max(r-context, 0)
	} else if r >= c.Top+vh {
		c.Top = r - vh + 1
	}
	c.clamp(rows, vh)
}

// wheel scrolls the view by step rows and drags the cursor along if it fell out,
// so the next j or k does not snap the view back.
func (c *cursor) wheel(rows []row, step, vh, context int) {
	c.Top += step
	c.clamp(rows, vh)
	cr, ok := c.at(rows)
	if !ok {
		return
	}
	sel := selectable(rows)
	switch {
	case cr < c.Top:
		for i, r := range sel {
			if r >= c.Top {
				c.selectAt(rows, i, vh, context)
				return
			}
		}
	case cr >= c.Top+vh:
		for i := len(sel) - 1; i >= 0; i-- {
			if sel[i] < c.Top+vh {
				c.selectAt(rows, i, vh, context)
				return
			}
		}
	}
}

// clickRow is the selectable index of the row drawn at screen row y, if any.
func (c cursor) clickRow(rows []row, y, vh int) (int, bool) {
	k := y - tabRows
	if k < 0 || k >= vh || c.Top+k >= len(rows) || rows[c.Top+k].header {
		return 0, false
	}
	for i, r := range selectable(rows) {
		if r == c.Top+k {
			return i, true
		}
	}
	return 0, false
}

// fetchState is what the tabs that ask GitHub remember about asking.
type fetchState struct {
	Asked    bool      // whether one has been started
	ReqAt    time.Time // when the last fetch started
	At       time.Time // when the last one answered
	Fetching bool
	Answered bool   // whether any fetch has answered yet: "no rows" is only "none" after that
	Err      string // why the last one failed; the rows are the last good ones
	Again    bool   // something changed while one was in flight, so ask again when it lands
}

func (f fetchState) due(now time.Time, every time.Duration) bool {
	return !f.Fetching && (!f.Asked || now.Sub(f.ReqAt) >= every)
}

func (f *fetchState) start(now time.Time) { f.Fetching, f.Asked, f.ReqAt = true, true, now }

// answered records an answer and reports whether to ask again at once.
func (f *fetchState) answered(at time.Time, err string) (again bool) {
	f.Fetching, f.Answered, f.At, f.Err = false, true, at, err
	again, f.Again = f.Again, false
	return again
}

// force is a request for a fresh answer whatever the throttle says: an operator's
// `r`, or something they did that the last answer predates.
func (f *fetchState) force(now time.Time, kind CmdKind) (cmd Cmd, started bool) {
	if f.Fetching {
		f.Again = true
		return Cmd{}, false
	}
	f.start(now)
	return Cmd{Kind: kind}, true
}

// LaterState and PRsState are the two GitHub-backed tabs.
type (
	LaterState struct {
		fetchState
		cursor
		Issues []LaterIssue
	}
	PRsState struct {
		fetchState
		cursor
		PRs    []PR
		Errors []PRError
	}
	// DoneState is the Done tab: resolved entries, read from the inbox file with
	// everything else, so it has no fetch of its own.
	DoneState struct {
		cursor
		Items []DoneItem
	}
)

// DoneItem is one resolved entry.
type DoneItem struct {
	ID        string
	Type      string
	CreatedAt time.Time
	Title     string
	Ref       string
	Replied   bool
}

func (m Model) kind() TabKind { return m.Cfg.Tabs[m.Active].Kind }

func (m Model) hasTab(k TabKind) bool {
	for _, t := range m.Cfg.Tabs {
		if t.Kind == k {
			return true
		}
	}
	return false
}

// rows is the active tab's rows, and the context its cursor keeps when scrolling up.
func (m Model) rows() ([]row, int) {
	switch m.kind() {
	case KindLater:
		return m.laterRows(), 1
	case KindDone:
		return m.doneRows(), 0
	case KindPRs:
		return m.prRows(), 1
	}
	return nil, 0
}

func (m *Model) cur() *cursor {
	switch m.kind() {
	case KindLater:
		return &m.Later.cursor
	case KindDone:
		return &m.Closed.cursor
	case KindPRs:
		return &m.PRs.cursor
	}
	return nil
}

// projectHeader is "name  (count)" for a repository, as foxy-inbox draws it.
func projectHeader(repo string, n int) row {
	short := repo
	if _, after, ok := strings.Cut(repo, "/"); ok {
		short = after
	}
	return row{header: true, l: line{{fmt.Sprintf("%s  (%d)", short, n), fgBold(cyan)}}}
}

func (m Model) doneRows() []row {
	out := make([]row, 0, len(m.Closed.Items))
	for _, it := range m.Closed.Items {
		mark, ms := "✓", green
		if it.Replied {
			mark, ms = "↩", yellow
		}
		out = append(out, row{key: it.ID, l: line{
			{" " + mark + " ", fg(ms)},
			{fmt.Sprintf("%4s ", Age(it.CreatedAt, m.Now)), fg(dim)},
			{clean(it.ID) + "  ", fg(dim)},
			{clean(it.Title), fg(dim)},
		}})
	}
	return out
}

// hintFor is the key-hint row of a tab: foxy-inbox's text, with q quit added.
func (m Model) hintFor() string {
	var h string
	switch m.kind() {
	case KindLater:
		h = "⏎ issue · o open on GitHub · d un-park · c copy url · Tab next tab · r refresh"
	case KindDone:
		h = "⏎ detail+your reply · o link · c copy id · Tab next tab · ↩ you answered"
	case KindPRs:
		h = "⏎/o open on GitHub · Tab next tab · r refresh"
	default:
		return m.hint()
	}
	return h + " · q quit"
}
