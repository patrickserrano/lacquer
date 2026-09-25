package inboxwatch

import (
	"fmt"
	"strings"
)

// overlay is a view the detail popup shows in place of its own text: the diff of
// a PR, or an agent's plan. Detail keeps it and hands it the keys, the answers to
// its Cmds and the size; q gives the detail back.
type overlay interface {
	key(k KeyEvent, w, h int) (cmds []Cmd, closed bool)
	wheel(delta, w, h int)
	// event takes an answer to a Cmd the overlay asked for.
	event(ev Event) []Cmd
	view(w, h int) Frame
}

// gutterW is the width of the line-number column, its trailing space included.
const gutterW = 6

// vrow is one screen row of a laid-out text: which line of it this is a part of.
type vrow struct {
	idx    int
	gutter string
	text   string
}

// diffView is the diff of one PR: a cursor on a line, scrolling, files to jump
// between, and "not this line" on the cursor's line.
type diffView struct {
	id       string // the entry's id, for the overseer
	canReply bool
	ref      string

	loaded bool
	err    string
	ev     DiffEvent
	lines  []diffLine // the file list, then the diff, then the truncation marker
	files  []int      // where each file starts in lines
	cur    int
	top    int
	note   string

	rows  []vrow
	first []int // the first row of each line
	rowsW int

	replyBox
	tPath  string
	tLine  int
	tSide  string
	result []frow // what the last answer did and did not do, until the next key
}

func newDiffView(id, ref string, canReply bool) *diffView {
	return &diffView{id: id, ref: ref, canReply: canReply}
}

// DiffTruncatedNote is the last line of a diff the popup cut short.
const DiffTruncatedNote = "[truncated: only the first 1 MB of this diff is shown; open the pull request for the rest]"

// event applies the answers to CmdDiff and CmdLine.
func (v *diffView) event(ev Event) []Cmd {
	switch ev := ev.(type) {
	case DiffEvent:
		v.loaded = true
		if ev.Err != "" {
			v.err = ev.Err
			return nil
		}
		v.ev = ev
		v.build()
		if ev.CacheErr != "" {
			v.note = "the phone's copy of this diff was not written: " + clean(ev.CacheErr)
		}
	case LineSentEvent:
		v.Sending = false
		add := func(text string, bad bool) {
			st := fg(green)
			if bad {
				st = fgBold(red)
			}
			v.result = append(v.result, frow{clean(text), st})
		}
		v.result = nil
		add("comment: "+ev.Comment, !ev.Commented)
		add("overseer: "+ev.Overseer, !ev.OverseerSent)
		if ev.Nothing() {
			// Neither half went out, so retrying loses nothing: the words come back.
			v.Replying = true
		} else {
			v.Buf = ""
		}
		if ev.OverseerSent {
			return []Cmd{{Kind: CmdEntry, ID: v.id}}
		}
	}
	return nil
}

// build makes the display lines: a file list, the diff, and the marker if it was cut.
func (v *diffView) build() {
	pd := parseDiff(v.ev.Text)
	v.lines = nil
	for i, f := range v.ev.Files {
		if i == maxFilesListed {
			v.lines = append(v.lines, diffLine{Kind: dkMeta, Text: fmt.Sprintf("  … and %d more files", len(v.ev.Files)-maxFilesListed)})
			break
		}
		v.lines = append(v.lines, diffLine{Kind: dkMeta, Text: fmt.Sprintf("  %s  +%d −%d", f.Path, f.Additions, f.Deletions)})
	}
	if len(v.ev.Files) > 0 {
		v.lines = append(v.lines, diffLine{Kind: dkMeta})
	}
	base := len(v.lines)
	v.lines = append(v.lines, pd.Lines...)
	v.files = nil
	for _, i := range pd.Files {
		v.files = append(v.files, base+i)
	}
	switch {
	case len(pd.Lines) == 0:
		v.lines = append(v.lines, diffLine{Kind: dkMeta, Text: "this pull request's diff is empty (gh printed nothing)"})
	case v.ev.Cut:
		v.lines = append(v.lines, diffLine{Kind: dkMeta, Text: DiffTruncatedNote})
	}
	v.cur = base
	if len(v.files) > 0 {
		v.cur = v.files[0]
	}
	v.top, v.rowsW = 0, 0
}

func (v *diffView) title() string {
	var adds, dels int
	for _, f := range v.ev.Files {
		adds += f.Additions
		dels += f.Deletions
	}
	head := v.ev.Head
	if len(head) > 7 {
		head = head[:7]
	}
	return fmt.Sprintf("DIFF  %s  head %s  ·  %d files  +%d −%d", v.ev.Ref, head, len(v.ev.Files), adds, dels)
}

// lay wraps every line to w, once per width.
func (v *diffView) lay(w int) {
	if v.rowsW == w && v.rows != nil {
		return
	}
	v.rows, v.first, v.rowsW = nil, make([]int, len(v.lines)), w
	tw := max(w-gutterW-1, 8)
	for i, l := range v.lines {
		v.first[i] = len(v.rows)
		n := l.New
		if l.Kind == dkDel {
			n = l.Old
		}
		gut := strings.Repeat(" ", gutterW)
		if n > 0 && l.Kind != dkMeta && l.Kind != dkFile && l.Kind != dkHunk {
			gut = fmt.Sprintf("%*d ", gutterW-1, n)
		}
		for j, t := range hardWrap(expandTabs(l.Text), tw) {
			if j > 0 {
				gut = strings.Repeat(" ", gutterW)
			}
			v.rows = append(v.rows, vrow{idx: i, gutter: gut, text: t})
		}
	}
}

func (v *diffView) rowStyle(k diffKind) style {
	switch k {
	case dkAdd:
		return fg(green)
	case dkDel:
		return fg(red)
	case dkHunk:
		return fg(dim)
	case dkFile:
		return fgBold(def)
	case dkMeta:
		return fg(dim)
	}
	return fg(def)
}

// footer is what sits under the diff: the target and the box while an answer is
// typed, or the result of the last one.
func (v *diffView) footer(w, h int) []frow {
	var out []frow
	for _, r := range v.result {
		for _, t := range wrap(r.text, max(w-1, 10)) {
			out = append(out, frow{t, r.st})
		}
	}
	if v.Replying || v.Sending {
		who := "the PR, and tells the overseer"
		if !v.canReply {
			who = "the PR only (no overseer pane)"
		}
		label := fmt.Sprintf("not this line → %s  %s:%d (%s-file line)  ·  comments on %s", v.ev.Ref, clean(v.tPath), v.tLine, v.tSide, who)
		for _, t := range wrap(label, max(w-1, 10)) {
			out = append(out, frow{t, fgBold(cyan)})
		}
		for _, t := range v.replyBox.box(w, h) {
			out = append(out, frow{t, fgBold(yellow)})
		}
	}
	return out
}

func (v *diffView) bodyH(w, h int) int { return max(h-1-len(v.footer(w, h))-1, 1) }

// show scrolls just far enough that the cursor's line is on screen.
func (v *diffView) show(w, h int) {
	v.lay(w)
	if len(v.lines) == 0 {
		return
	}
	bh := v.bodyH(w, h)
	first, last := v.first[v.cur], len(v.rows)-1
	if v.cur+1 < len(v.first) {
		last = v.first[v.cur+1] - 1
	}
	switch {
	case first < v.top:
		v.top = first
	case last >= v.top+bh:
		v.top = max(last-bh+1, 0)
	}
	v.top = max(0, min(v.top, max(len(v.rows)-bh, 0)))
}

func (v *diffView) move(to, w, h int) {
	if len(v.lines) > 0 {
		v.cur = max(0, min(to, len(v.lines)-1))
	}
	v.show(w, h)
}

func (v *diffView) wheel(delta, w, h int) {
	if v.loaded && v.err == "" {
		v.move(v.cur+delta, w, h)
	}
}

func (v *diffView) key(k KeyEvent, w, h int) ([]Cmd, bool) {
	if v.Sending {
		return nil, false
	}
	v.result = nil
	if v.Replying {
		text, act := v.replyBox.key(k)
		switch act {
		case boxSend:
			return []Cmd{{Kind: CmdLine, ID: v.id, Ref: v.ev.Ref.String(), Head: v.ev.Head, Path: v.tPath, Line: v.tLine, Side: v.tSide, Text: text}}, false
		case boxCancel:
			v.note = "cancelled; nothing was sent"
		}
		v.show(w, h)
		return nil, false
	}
	v.note = ""
	if k.Key == KeyEsc || k.Key == KeyCtrlC || (k.Key == KeyRune && k.Rune == 'q') {
		return nil, true
	}
	if !v.loaded || v.err != "" {
		return nil, false
	}
	page := max(v.bodyH(w, h)-1, 1)
	switch k.Key {
	case KeyDown:
		v.move(v.cur+1, w, h)
	case KeyUp:
		v.move(v.cur-1, w, h)
	case KeyPgDn:
		v.move(v.cur+page, w, h)
	case KeyPgUp:
		v.move(v.cur-page, w, h)
	case KeyRune:
		switch k.Rune {
		case 'j':
			v.move(v.cur+1, w, h)
		case 'k':
			v.move(v.cur-1, w, h)
		case ' ':
			v.move(v.cur+page, w, h)
		case 'g':
			v.move(0, w, h)
		case 'G':
			v.move(len(v.lines)-1, w, h)
		case 'n':
			v.jump(1, w, h)
		case 'p':
			v.jump(-1, w, h)
		case 'r':
			l := v.lines[v.cur]
			if !l.respondable() {
				v.note = "move to a +, - or context line first: those are the lines that can be answered"
				return nil, false
			}
			v.tPath = l.Path
			v.tLine, v.tSide = l.target()
			v.Replying = true
			v.show(w, h)
		}
	}
	return nil, false
}

// jump moves to the next file (dir 1) or the start of this one, then the one
// before it (dir -1).
func (v *diffView) jump(dir, w, h int) {
	if len(v.files) == 0 {
		v.note = "no files in this diff"
		return
	}
	to := -1
	if dir > 0 {
		for _, f := range v.files {
			if f > v.cur {
				to = f
				break
			}
		}
		if to < 0 {
			v.note = "last file"
			return
		}
	} else {
		for _, f := range v.files {
			if f < v.cur {
				to = f
			}
		}
		if to < 0 {
			v.note = "first file"
			return
		}
	}
	v.move(to, w, h)
}

func (v *diffView) hint() string {
	switch {
	case v.Replying:
		return "⏎ send · Esc cancel · ctrl-u clear"
	case !v.loaded || v.err != "":
		return "q back"
	}
	return "j/k line · n/p file · space page · g/G top/end · r not this line · q back"
}

func (v *diffView) view(w, h int) Frame {
	cw := max(w-1, 0)
	lines := make([]string, h)
	if h == 0 {
		return Frame{Lines: lines}
	}
	switch {
	case !v.loaded:
		lines[0] = line{{"loading the diff…", fg(dim)}}.render(cw, false)
	case v.err != "":
		for i, t := range wrap(clean(v.err), max(w-2, 20)) {
			if i < h-1 {
				lines[i] = line{{t, fg(red)}}.render(cw, false)
			}
		}
	default:
		v.show(w, h)
		lines[0] = line{{v.title(), fgBold(cyan)}}.render(cw, false)
		bh := v.bodyH(w, h)
		for r := 0; r < bh && v.top+r < len(v.rows); r++ {
			vr := v.rows[v.top+r]
			l := v.lines[vr.idx]
			lines[1+r] = line{{vr.gutter, fg(dim)}, {vr.text, v.rowStyle(l.Kind)}}.render(cw, vr.idx == v.cur)
		}
		for i, f := range v.footer(w, h) {
			if y := 1 + bh + i; y < h-1 {
				lines[y] = line{{f.text, f.st}}.render(cw, false)
			}
		}
	}
	if v.note != "" {
		st := style{fg: def, bg: def, reverse: true}
		lines[h-1] = line{{v.note + strings.Repeat(" ", max(cw-cells(v.note), 0)), st}}.render(cw, false)
	} else {
		lines[h-1] = hint(v.hint()).render(cw, false)
	}
	f := Frame{Lines: lines}
	if v.Replying {
		if foot := v.footer(w, h); len(foot) > 0 {
			f.ShowCursor = true
			f.CursorY = 1 + v.bodyH(w, h) + len(foot) - 1
			f.CursorX = min(len([]rune(foot[len(foot)-1].text)), max(w-2, 0))
		}
	}
	return f
}
