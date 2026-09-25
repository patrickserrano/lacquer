package inboxwatch

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// highlight marks the body lines a decision hangs on.
var highlight = regexp.MustCompile(`(?i)^\s*(Decide|Decided|Reply|To do|Options?|Your calls?|Recommend)\b`)

// Detail is one entry's scrollable detail view, the program behind the tmux
// popup. r opens a reply box under the text, so the entry stays visible while
// the operator writes; Enter sends it, Esc cancels. d twice resolves; o opens
// the link.
type Detail struct {
	ID       string
	CanReply bool
	W, H     int
	Now      time.Time

	Requested bool
	Loaded    bool
	Entry     inbox.Entry
	Found     bool
	Reply     Reply
	HasReply  bool
	LoadErr   string

	Top  int
	Note string
	Arm  bool
	quit bool

	replyBox
}

// NewDetail is a detail view of the entry id.
func NewDetail(id string, canReply bool, w, h int) Detail {
	return Detail{ID: id, CanReply: canReply, W: w, H: h}
}

func (d Detail) Done() bool { return d.quit }

func (d Detail) Update(ev Event) (Program, []Cmd) {
	cmds := d.update(ev)
	return d, cmds
}

func (d *Detail) update(ev Event) []Cmd {
	switch ev := ev.(type) {
	case ResizeEvent:
		d.W, d.H = ev.W, ev.H
		d.clampTop()
	case TickEvent:
		d.Now = ev.Now
		if !d.Requested {
			d.Requested = true
			return []Cmd{{Kind: CmdEntry, ID: d.ID}}
		}
	case EntryEvent:
		d.Loaded, d.Entry, d.Found, d.Reply, d.HasReply, d.LoadErr = true, ev.Entry, ev.Found, ev.Reply, ev.HasReply, ev.Err
	case DoneEvent:
		if ev.Kind == CmdResolve && ev.OK {
			d.quit = true
		} else {
			d.Note = ev.Note
		}
	case RepliedEvent:
		d.Sending = false
		if ev.OK && ev.CommentErr != "" {
			// The overseer has it, so nothing is retried and the box is cleared; but
			// the popup stays open so this is read, not lost with the window.
			d.Buf, d.Note = "", "reply sent; comment NOT posted: "+ev.CommentErr
			return []Cmd{{Kind: CmdEntry, ID: d.ID}}
		}
		if ev.OK {
			d.quit = true
			return nil
		}
		// The overseer did not get it: keep what was typed, so it is not lost.
		d.Replying, d.Note = true, ev.Note
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

func (d Detail) box() []string { return d.replyBox.box(d.W, d.H) }

func (d Detail) bodyH() int { return max(d.H-len(d.box())-1, 1) }

func (d *Detail) clampTop() {
	d.Top = max(0, min(d.Top, max(len(d.lines())-d.bodyH(), 0)))
}

func (d *Detail) key(k KeyEvent) []Cmd {
	if d.Sending {
		return nil
	}
	if d.Replying {
		return d.replyKey(k)
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
				d.Note = "reply disabled: " + DisabledWhy
				return nil
			}
			d.Replying = true
			d.clampTop()
		case 'c':
			return []Cmd{{Kind: CmdCopy, ID: d.ID, Text: d.ID}}
		case 'o':
			if !isLink(d.Entry.Ref) {
				d.Note = "no link on this item"
				return nil
			}
			return []Cmd{{Kind: CmdOpen, Text: d.Entry.Ref}}
		case 'd':
			if arm {
				return []Cmd{{Kind: CmdResolve, ID: d.ID}}
			}
			d.Arm = true
			d.Note = "press d again to resolve this item (any other key cancels)"
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

func (d *Detail) replyKey(k KeyEvent) []Cmd {
	d.Note = ""
	text, act := d.replyBox.key(k)
	switch act {
	case boxSend:
		ref := ""
		if d.Found {
			ref = d.Entry.Ref
		}
		return []Cmd{{Kind: CmdReply, ID: d.ID, Text: text, Ref: ref}}
	case boxCancel:
		d.Note = "reply cancelled"
	}
	d.clampTop()
	return nil
}

// lines is the body: badge, status, fields, then the entry's own text.
func (d Detail) lines() []line {
	w := max(d.W-2, 20)
	if !d.Loaded {
		return []line{{{"loading " + clean(d.ID), fg(dim)}}}
	}
	if d.LoadErr != "" && !d.Found {
		return []line{{{"could not read the inbox: " + d.LoadErr, fg(red)}}}
	}
	if !d.Found {
		return []line{{{"no inbox entry " + clean(d.ID), fg(def)}}}
	}
	e := d.Entry
	kind := strings.ToUpper(string(e.Type))
	badge := fgBold(blue)
	if e.Type == inbox.Action {
		badge = fgBold(red)
	}
	var out []line
	out = append(out, line{{fmt.Sprintf("%s  %s   %s old", clean(kind), clean(e.ID), Age(e.CreatedAt, d.Now)), badge}})
	for _, t := range wrap(clean(e.Title), w) {
		out = append(out, line{{t, fgBold(def)}})
	}
	switch {
	case e.ResolvedAt != nil:
		out = append(out, line{{"✓ resolved " + e.ResolvedAt.UTC().Format("2006-01-02 15:04"), fgBold(green)}})
	case d.HasReply:
		at := clean(d.Reply.At)
		if len(at) >= 16 {
			at = at[11:16]
		}
		out = append(out, line{{"↩ you replied " + at + ", waiting on the overseer:", fgBold(yellow)}})
		for _, t := range wrap(clean(d.Reply.Text), w-2) {
			out = append(out, line{{"  " + t, fg(yellow)}})
		}
	case e.Type == inbox.Action:
		out = append(out, line{{"● waiting on you", fg(red)}})
	}
	out = append(out, nil)
	for _, f := range []struct{ key, val string }{{"project", clean(e.Project)}, {"createdAt", e.CreatedAt.Format(time.RFC3339Nano)}, {"ref", clean(e.Ref)}} {
		if f.val == "" {
			continue
		}
		st := fg(dim)
		if f.key == "ref" {
			st = fg(cyan)
		}
		out = append(out, line{{fmt.Sprintf("%9s: %s", f.key, f.val), st}})
	}
	out = append(out, nil)
	for _, para := range strings.Split(e.Body, "\n") {
		para = clean(para)
		st := fg(def)
		if highlight.MatchString(para) {
			st = fgBold(yellow)
		}
		for _, t := range wrap(para, w) {
			out = append(out, line{{t, st}})
		}
	}
	return out
}

func (d Detail) hint() string {
	switch {
	case d.Replying:
		return "⏎ send · Esc cancel · ctrl-u clear"
	case !d.CanReply:
		return "r reply (off: no overseer pane) · d resolve · o open link · c copy id · j/k scroll · q close"
	}
	return "r reply · d resolve · o open link · c copy id · j/k scroll · q close"
}

func (d Detail) View() Frame {
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
