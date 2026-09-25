package inboxwatch

import (
	"fmt"
	"strings"
	"time"
)

// IssuePopup is one parked issue's scrollable view, the program behind the tmux
// popup a Later row opens. o opens it on GitHub, c copies its URL, r types a note
// about it to the overseer, d twice takes it off Later.
type IssuePopup struct {
	Ref      string
	CanReply bool
	W, H     int
	Now      time.Time

	Requested bool
	Loaded    bool
	OK        bool
	Data      IssueData
	Err       string

	Top  int
	Note string
	Arm  bool
	quit bool

	replyBox
}

// NewIssuePopup is the view of the issue ref.
func NewIssuePopup(ref string, canReply bool, w, h int) IssuePopup {
	return IssuePopup{Ref: ref, CanReply: canReply, W: w, H: h}
}

func (d IssuePopup) Done() bool { return d.quit }

func (d IssuePopup) Update(ev Event) (Program, []Cmd) {
	cmds := d.update(ev)
	return d, cmds
}

func (d *IssuePopup) update(ev Event) []Cmd {
	switch ev := ev.(type) {
	case ResizeEvent:
		d.W, d.H = ev.W, ev.H
		d.clampTop()
	case TickEvent:
		d.Now = ev.Now
		if !d.Requested {
			d.Requested = true
			return []Cmd{{Kind: CmdIssue, ID: d.Ref}}
		}
	case IssueEvent:
		d.Loaded, d.OK, d.Data, d.Err = true, ev.OK, ev.Data, ev.Err
		d.clampTop()
	case DoneEvent:
		switch {
		case ev.Kind == CmdUnpark && ev.OK:
			d.quit = true
		default:
			d.Note = ev.Note
		}
	case RepliedEvent:
		d.Sending = false
		if ev.OK {
			d.quit = true
			return nil
		}
		d.Replying, d.Note = true, ev.Note // the overseer did not get it: keep what was typed
	case MouseEvent:
		switch ev.Button {
		case ButtonWheelUp:
			d.Top -= wheelStep
		case ButtonWheelDown:
			d.Top += wheelStep
		}
		d.clampTop()
	case KeyEvent:
		return d.key(ev)
	}
	return nil
}

func (d IssuePopup) box() []string { return d.replyBox.box(d.W, d.H) }

func (d IssuePopup) bodyH() int { return max(d.H-len(d.box())-1, 1) }

func (d *IssuePopup) clampTop() {
	d.Top = max(0, min(d.Top, max(len(d.lines())-d.bodyH(), 0)))
}

func (d *IssuePopup) key(k KeyEvent) []Cmd {
	if d.Sending {
		return nil
	}
	if d.Replying {
		d.Note = ""
		text, act := d.replyBox.key(k)
		switch act {
		case boxSend:
			// "[later <ref>] <text>": the ref and no title. The title is
			// agent- or user-written, and typed into the overseer's input it
			// would read as the operator's own words.
			return []Cmd{{Kind: CmdReply, ID: d.Ref, Text: text, Tag: "later"}}
		case boxCancel:
			d.Note = "note cancelled"
		}
		d.clampTop()
		return nil
	}
	arm := d.Arm
	d.Note, d.Arm = "", false
	switch k.Key {
	case KeyEsc, KeyCtrlC:
		d.quit = true
	case KeyDown:
		d.Top++
		d.clampTop()
	case KeyUp:
		d.Top--
		d.clampTop()
	case KeyRune:
		switch k.Rune {
		case 'q':
			d.quit = true
		case 'r':
			if !d.CanReply {
				d.Note = "note disabled: " + DisabledWhy
				return nil
			}
			d.Replying = true
			d.clampTop()
		case 'c':
			if !d.OK {
				d.Note = "no issue loaded"
				return nil
			}
			return []Cmd{{Kind: CmdCopy, ID: d.Ref, Text: d.Data.URL, Label: "url"}}
		case 'o':
			if !d.OK {
				d.Note = "no issue loaded"
				return nil
			}
			if !isLink(d.Data.URL) {
				d.Note = "no link on this issue"
				return nil
			}
			return []Cmd{{Kind: CmdOpen, Text: d.Data.URL, Label: "on GitHub"}}
		case 'd':
			if arm {
				return []Cmd{{Kind: CmdUnpark, ID: d.Ref}}
			}
			d.Arm = true
			d.Note = "press d again to take this off Later (removes the label; the issue stays open)"
		case 'j', ' ':
			d.Top++
			d.clampTop()
		case 'k':
			d.Top--
			d.clampTop()
		}
	}
	return nil
}

// lines is the body: badge, title, fields, then the issue's own text. Every
// string in it is written by someone else and is drawn through the sanitizer.
func (d IssuePopup) lines() []line {
	w := max(d.W-2, 20)
	ref := clean(d.Ref)
	switch {
	case !d.Loaded:
		return []line{{{"loading " + ref, fg(dim)}}}
	case !d.OK:
		out := []line{{{fmt.Sprintf("could not load %s (gh issue view failed)", ref), fg(red)}}}
		for _, t := range wrap(clean(d.Err), w) {
			out = append(out, line{{t, fg(dim)}})
		}
		return out
	}
	i := d.Data
	out := []line{{{fmt.Sprintf("LATER  %s   %s old   %s", ref, Age(i.CreatedAt, d.Now), clean(i.State)), fgBold(magenta)}}}
	for _, t := range wrap(clean(oneLine(i.Title)), w) {
		out = append(out, line{{t, fgBold(def)}})
	}
	if len(i.Labels) > 0 {
		out = append(out, line{{"   labels: " + clean(strings.Join(i.Labels, ", ")), fg(dim)}})
	}
	if len(i.Assignees) > 0 {
		out = append(out, line{{" assigned: " + clean(strings.Join(i.Assignees, ", ")), fg(dim)}})
	}
	out = append(out, line{{"      url: " + clean(i.URL), fg(cyan)}})
	if i.Comments > 0 {
		out = append(out, line{{fmt.Sprintf(" comments: %d (o to read them on GitHub)", i.Comments), fg(dim)}})
	}
	out = append(out, nil)
	// GitHub bodies are CRLF; left in, every line would end in a visible ^M.
	body := strings.ReplaceAll(strings.ReplaceAll(i.Body, "\r\n", "\n"), "\r", "\n")
	for _, para := range strings.Split(body, "\n") {
		para = clean(para)
		st := fg(def)
		switch {
		case strings.HasPrefix(para, "#"):
			st = fgBold(yellow)
		case strings.HasPrefix(strings.TrimLeft(para, " \t"), "- [ ]"), strings.HasPrefix(strings.TrimLeft(para, " \t"), "- [x]"):
			st = fg(green)
		case highlight.MatchString(para), strings.HasPrefix(para, "**"):
			st = fgBold(def)
		}
		for _, t := range wrap(para, w) {
			out = append(out, line{{t, st}})
		}
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (d IssuePopup) hint() string {
	switch {
	case d.Replying:
		return "⏎ send · Esc cancel · ctrl-u clear"
	case !d.CanReply:
		return "o open on GitHub · r note (off: no overseer pane) · d un-park · c copy url · j/k scroll · q close"
	}
	return "o open on GitHub · r note to overseer · d un-park · c copy url · j/k scroll · q close"
}

func (d IssuePopup) View() Frame {
	w, h := d.W, d.H
	cw := max(w-1, 0)
	lines := make([]string, h)
	box := d.box()
	bodyH := d.bodyH()
	body := d.lines()
	for row := 0; row < bodyH && d.Top+row < len(body); row++ {
		lines[row] = body[d.Top+row].render(cw, false)
	}
	for i, t := range box {
		if y := bodyH + i; y < h {
			lines[y] = line{{t, fgBold(yellow)}}.render(cw, false)
		}
	}
	if h > 0 {
		switch {
		case d.Note != "" || d.Arm:
			st := style{fg: def, bg: def, reverse: true}
			if d.Arm {
				st = style{fg: red, bg: def, reverse: true}
			}
			lines[h-1] = line{{d.Note + strings.Repeat(" ", max(cw-cells(d.Note), 0)), st}}.render(cw, false)
		default:
			lines[h-1] = hint(d.hint()).render(cw, false)
		}
	}
	f := Frame{Lines: lines}
	if d.Replying && len(box) > 0 {
		f.ShowCursor = true
		f.CursorY = bodyH + len(box) - 1
		f.CursorX = min(len([]rune(box[len(box)-1])), max(w-2, 0))
	}
	return f
}
