package inboxwatch

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is anything a Program reacts to.
type Event interface{}

// Key names the keys that are not a plain rune.
type Key int

const (
	KeyRune Key = iota
	KeyEnter
	KeyEsc
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyTab
	KeyBackTab
	KeyBackspace
	KeyCtrlU
	KeyCtrlC
	KeyPgUp
	KeyPgDn
)

// KeyEvent is one key press. Rune is set when Key is KeyRune.
type KeyEvent struct {
	Key  Key
	Rune rune
}

// Button is a decoded mouse button.
type Button int

const (
	ButtonLeft Button = iota
	ButtonWheelUp
	ButtonWheelDown
)

// MouseEvent is one mouse press or wheel notch, 0-based, so X and Y index the
// screen's cells the way a frame's lines do.
type MouseEvent struct {
	Button Button
	X, Y   int
}

// ResizeEvent reports the terminal's size.
type ResizeEvent struct{ W, H int }

// TickEvent is the clock; models decide from it when to refresh.
type TickEvent struct{ Now time.Time }

func rk(r rune) KeyEvent { return KeyEvent{Key: KeyRune, Rune: r} }

// ParseInput decodes terminal input: keys, and xterm SGR mouse reports
// (ESC [ < button ; x ; y M) which the loop turns on itself, so nothing
// outside this process (a tmux binding, say) has to translate the wheel.
//
// A sequence cut off at the end of b is returned in rest for the caller to
// prepend to the next read. A lone ESC is such a sequence until flush says no
// more bytes are coming, which is how Esc is told from the start of an arrow.
func ParseInput(b []byte, flush bool) (evs []Event, rest []byte) {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			if i+1 >= len(b) {
				if flush {
					evs = append(evs, KeyEvent{Key: KeyEsc})
					return evs, nil
				}
				return evs, b[i:]
			}
			switch b[i+1] {
			case '[':
				ev, n, ok := parseCSI(b[i+2:])
				if !ok {
					if flush {
						return append(evs, KeyEvent{Key: KeyEsc}), nil
					}
					return evs, b[i:]
				}
				if ev != nil {
					evs = append(evs, ev)
				}
				i += 2 + n
			case 'O': // SS3: application-mode arrows
				if i+2 >= len(b) {
					if flush {
						return append(evs, KeyEvent{Key: KeyEsc}), nil
					}
					return evs, b[i:]
				}
				if k, ok := csiKey(b[i+2], ""); ok {
					evs = append(evs, k)
				}
				i += 3
			default: // ESC then a plain key: the Esc stands alone
				evs = append(evs, KeyEvent{Key: KeyEsc})
				i++
			}
		case c == '\r' || c == '\n':
			evs = append(evs, KeyEvent{Key: KeyEnter})
			i++
		case c == '\t':
			evs = append(evs, KeyEvent{Key: KeyTab})
			i++
		case c == 0x7f || c == 0x08:
			evs = append(evs, KeyEvent{Key: KeyBackspace})
			i++
		case c == 0x15:
			evs = append(evs, KeyEvent{Key: KeyCtrlU})
			i++
		case c == 0x03:
			evs = append(evs, KeyEvent{Key: KeyCtrlC})
			i++
		case c < 0x20:
			i++ // another control key: nothing here uses it
		default:
			if !utf8.FullRune(b[i:]) {
				if flush {
					return evs, nil
				}
				return evs, b[i:]
			}
			r, n := utf8.DecodeRune(b[i:])
			evs = append(evs, rk(r))
			i += n
		}
	}
	return evs, nil
}

// parseCSI decodes the bytes after "ESC [". n is how many it used; ok is false
// when the sequence is not complete yet. ev is nil for a sequence with no
// meaning here (which is still consumed).
func parseCSI(b []byte) (ev Event, n int, ok bool) {
	for j, c := range b {
		if c < 0x40 || c > 0x7e {
			continue // a parameter or intermediate byte
		}
		params := string(b[:j])
		if strings.HasPrefix(params, "<") && (c == 'M' || c == 'm') {
			return parseMouse(params[1:], c == 'M'), j + 1, true
		}
		if k, found := csiKey(c, params); found {
			return k, j + 1, true
		}
		return nil, j + 1, true
	}
	return nil, 0, false
}

func csiKey(final byte, params string) (KeyEvent, bool) {
	switch final {
	case 'A':
		return KeyEvent{Key: KeyUp}, true
	case 'B':
		return KeyEvent{Key: KeyDown}, true
	case 'C':
		return KeyEvent{Key: KeyRight}, true
	case 'D':
		return KeyEvent{Key: KeyLeft}, true
	case 'Z':
		return KeyEvent{Key: KeyBackTab}, true
	case '~':
		switch params {
		case "5":
			return KeyEvent{Key: KeyPgUp}, true
		case "6":
			return KeyEvent{Key: KeyPgDn}, true
		}
	}
	return KeyEvent{}, false
}

// parseMouse decodes "button;x;y" from an SGR report. Only a press (or a wheel
// notch, which reports as a press) is an event; releases and motion are not,
// so a click is one event and not two. Modifier bits are ignored, so a wheel
// notch with shift held still scrolls.
func parseMouse(p string, press bool) Event {
	f := strings.Split(p, ";")
	if len(f) != 3 || !press {
		return nil
	}
	b, e1 := strconv.Atoi(f[0])
	x, e2 := strconv.Atoi(f[1])
	y, e3 := strconv.Atoi(f[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return nil
	}
	if b&32 != 0 { // motion
		return nil
	}
	switch b &^ (4 | 8 | 16) {
	case 0:
		return MouseEvent{Button: ButtonLeft, X: x - 1, Y: y - 1}
	case 64:
		return MouseEvent{Button: ButtonWheelUp, X: x - 1, Y: y - 1}
	case 65:
		return MouseEvent{Button: ButtonWheelDown, X: x - 1, Y: y - 1}
	}
	return nil
}
