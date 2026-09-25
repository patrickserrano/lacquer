package inboxwatch

import "strings"

// planView is the file an entry's `file:` ref names, shown read-only, as text the
// agent will act on: nothing is left out, and every line that is longer than the
// popup is wrapped, not cut.
type planView struct {
	loaded bool
	ev     PlanEvent
	top    int
	rows   []string
	rowsW  int
}

func newPlanView() *planView { return &planView{} }

func (v *planView) event(ev Event) []Cmd {
	if pe, ok := ev.(PlanEvent); ok {
		v.loaded, v.ev, v.rows, v.rowsW = true, pe, nil, 0
	}
	return nil
}

func (v *planView) lay(w int) {
	if v.rowsW == w && v.rows != nil {
		return
	}
	v.rowsW, v.rows = w, nil
	if v.ev.Err != "" {
		return
	}
	for _, l := range strings.Split(strings.TrimSuffix(v.ev.Text, "\n"), "\n") {
		v.rows = append(v.rows, hardWrap(expandTabs(l), max(w-1, 8))...)
	}
	if v.ev.Cut {
		v.rows = append(v.rows, PlanTruncated)
	}
}

func (v *planView) bodyH(h int) int { return max(h-2, 1) }

func (v *planView) scroll(to, w, h int) {
	v.lay(w)
	v.top = max(0, min(to, max(len(v.rows)-v.bodyH(h), 0)))
}

func (v *planView) wheel(delta, w, h int) { v.scroll(v.top+delta, w, h) }

func (v *planView) key(k KeyEvent, w, h int) ([]Cmd, bool) {
	page := max(v.bodyH(h)-1, 1)
	switch k.Key {
	case KeyEsc, KeyCtrlC:
		return nil, true
	case KeyDown:
		v.scroll(v.top+1, w, h)
	case KeyUp:
		v.scroll(v.top-1, w, h)
	case KeyPgDn:
		v.scroll(v.top+page, w, h)
	case KeyPgUp:
		v.scroll(v.top-page, w, h)
	case KeyRune:
		switch k.Rune {
		case 'q':
			return nil, true
		case 'j':
			v.scroll(v.top+1, w, h)
		case 'k':
			v.scroll(v.top-1, w, h)
		case ' ':
			v.scroll(v.top+page, w, h)
		case 'g':
			v.scroll(0, w, h)
		case 'G':
			v.scroll(len(v.rows), w, h)
		}
	}
	return nil, false
}

func (v *planView) view(w, h int) Frame {
	cw := max(w-1, 0)
	lines := make([]string, h)
	if h == 0 {
		return Frame{Lines: lines}
	}
	v.scroll(v.top, w, h)
	switch {
	case !v.loaded:
		lines[0] = line{{"loading the file…", fg(dim)}}.render(cw, false)
	case v.ev.Err != "":
		for i, t := range wrap(clean(v.ev.Err), max(w-2, 20)) {
			if i < h-1 {
				lines[i] = line{{t, fg(red)}}.render(cw, false)
			}
		}
	default:
		lines[0] = line{{"PLAN  " + v.ev.Path + "  (read-only)", fgBold(cyan)}}.render(cw, false)
		for r := 0; r < v.bodyH(h) && v.top+r < len(v.rows); r++ {
			st := fg(def)
			if v.ev.Cut && v.top+r == len(v.rows)-1 {
				st = fgBold(yellow)
			}
			lines[1+r] = line{{v.rows[v.top+r], st}}.render(cw, false)
		}
	}
	lines[h-1] = hint(v.hint()).render(cw, false)
	return Frame{Lines: lines}
}

func (v *planView) hint() string {
	if !v.loaded || v.ev.Err != "" {
		return "q back"
	}
	return "j/k scroll · space page · g/G top/end · q back"
}
