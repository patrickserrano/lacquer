package inboxwatch

import "strings"

// replyBox is the box under a popup's text that a reply or a note is typed in.
// The entry popup and the issue popup share it: they differ in what they send,
// not in how it is typed.
type replyBox struct {
	Replying bool
	Sending  bool
	Buf      string
}

type boxAct int

const (
	boxNone   boxAct = iota
	boxSend          // Enter on text: send it
	boxCancel        // Esc, Ctrl-C, or Enter on nothing
)

// box is the rows the box takes, wrapped to w and cut to a third of the h rows
// so a long reply scrolls within it.
func (b replyBox) box(w, h int) []string {
	if !b.Replying {
		return nil
	}
	t := []rune("> " + b.Buf)
	width := max(w-1, 10)
	var box []string
	for i := 0; i < len(t); i += width {
		box = append(box, string(t[i:min(i+width, len(t))]))
	}
	if len(t)%width == 0 {
		box = append(box, "") // room for the cursor after a full line
	}
	if n := max(h/3, 1); len(box) > n {
		box = box[len(box)-n:]
	}
	return box
}

// key applies one key to the box. text is what to send when act is boxSend.
func (b *replyBox) key(k KeyEvent) (text string, act boxAct) {
	switch k.Key {
	case KeyEnter:
		text = strings.TrimSpace(b.Buf)
		if text == "" {
			b.Replying, b.Buf = false, ""
			return "", boxCancel
		}
		// Buf is kept: if the overseer never gets it, the box comes back with it.
		b.Replying, b.Sending = false, true
		return text, boxSend
	case KeyEsc, KeyCtrlC:
		b.Replying, b.Buf = false, ""
		return "", boxCancel
	case KeyBackspace:
		if r := []rune(b.Buf); len(r) > 0 {
			b.Buf = string(r[:len(r)-1])
		}
	case KeyCtrlU:
		b.Buf = ""
	case KeyRune:
		if k.Rune >= ' ' && k.Rune != 0x7f {
			b.Buf += string(k.Rune)
		}
	}
	return "", boxNone
}
