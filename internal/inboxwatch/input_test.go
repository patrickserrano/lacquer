package inboxwatch

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []Event
	}{
		{"wheel up", "\x1b[<64;10;5M", []Event{MouseEvent{ButtonWheelUp, 9, 4}}},
		{"wheel down", "\x1b[<65;10;5M", []Event{MouseEvent{ButtonWheelDown, 9, 4}}},
		{"wheel with shift held", "\x1b[<69;10;5M", []Event{MouseEvent{ButtonWheelDown, 9, 4}}},
		{"left click", "\x1b[<0;3;7M", []Event{MouseEvent{ButtonLeft, 2, 6}}},
		{"click release", "\x1b[<0;3;7m", nil},
		{"wheel release", "\x1b[<65;3;7m", nil},
		{"motion", "\x1b[<32;3;7M", nil},
		{"right click", "\x1b[<2;3;7M", nil},
		{"horizontal wheel", "\x1b[<66;3;7M", nil},
		{"wide columns", "\x1b[<0;212;48M", []Event{MouseEvent{ButtonLeft, 211, 47}}},
		{"arrows", "\x1b[A\x1b[B\x1b[C\x1b[D", []Event{KeyEvent{Key: KeyUp}, KeyEvent{Key: KeyDown}, KeyEvent{Key: KeyRight}, KeyEvent{Key: KeyLeft}}},
		{"application arrows", "\x1bOA\x1bOB", []Event{KeyEvent{Key: KeyUp}, KeyEvent{Key: KeyDown}}},
		{"page keys", "\x1b[5~\x1b[6~", []Event{KeyEvent{Key: KeyPgUp}, KeyEvent{Key: KeyPgDn}}},
		{"shift-tab", "\x1b[Z", []Event{KeyEvent{Key: KeyBackTab}}},
		{"enter, tab, backspace", "\r\t\x7f", []Event{KeyEvent{Key: KeyEnter}, KeyEvent{Key: KeyTab}, KeyEvent{Key: KeyBackspace}}},
		{"ctrl-u, ctrl-c", "\x15\x03", []Event{KeyEvent{Key: KeyCtrlU}, KeyEvent{Key: KeyCtrlC}}},
		{"runes", "jé😀", []Event{rk('j'), rk('é'), rk('😀')}},
		{"lone escape", "\x1b", []Event{KeyEvent{Key: KeyEsc}}},
		{"escape then a key", "\x1bq", []Event{KeyEvent{Key: KeyEsc}, rk('q')}},
		{"a key then a mouse report", "j\x1b[<65;1;1Mk", []Event{rk('j'), MouseEvent{ButtonWheelDown, 0, 0}, rk('k')}},
		{"an unknown sequence is swallowed whole", "\x1b[15~j", []Event{rk('j')}},
	} {
		got, rest := ParseInput([]byte(tc.in), true)
		if len(rest) != 0 || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: ParseInput(%q) = %v rest %q, want %v", tc.name, tc.in, got, rest, tc.want)
		}
	}
}

// A report split across two reads is not lost, and a lone ESC waits for flush.
func TestParseInputKeepsAPartialSequenceForTheNextRead(t *testing.T) {
	evs, rest := ParseInput([]byte("j\x1b[<65;1"), false)
	if !reflect.DeepEqual(evs, []Event{rk('j')}) || string(rest) != "\x1b[<65;1" {
		t.Fatalf("got %v rest %q", evs, rest)
	}
	evs, rest = ParseInput(append(rest, ";1M"...), false)
	if !reflect.DeepEqual(evs, []Event{MouseEvent{ButtonWheelDown, 0, 0}}) || len(rest) != 0 {
		t.Fatalf("got %v rest %q", evs, rest)
	}

	if evs, rest = ParseInput([]byte("\x1b"), false); len(evs) != 0 || string(rest) != "\x1b" {
		t.Errorf("a lone ESC must wait: %v %q", evs, rest)
	}
	if evs, rest = ParseInput([]byte("\x1b["), false); len(evs) != 0 || string(rest) != "\x1b[" {
		t.Errorf("ESC [ must wait: %v %q", evs, rest)
	}
	// Flushing turns only a LONE ESC into Esc. An unfinished ESC [ stays put for
	// its tail, or a report split across two reads would quit the view.
	if evs, rest = ParseInput([]byte("\x1b["), true); len(evs) != 0 || string(rest) != "\x1b[" {
		t.Errorf("flushed, ESC [ must wait for its tail: %v %q", evs, rest)
	}
	if evs, rest = ParseInput([]byte("\x1bO"), true); len(evs) != 0 || string(rest) != "\x1bO" {
		t.Errorf("flushed, ESC O must wait for its tail: %v %q", evs, rest)
	}
	if evs, _ = ParseInput([]byte("\x1b"), true); !reflect.DeepEqual(evs, []Event{KeyEvent{Key: KeyEsc}}) {
		t.Errorf("flushed, a lone ESC is Esc: %v", evs)
	}
	if _, rest = ParseInput([]byte("\x1b["+strings.Repeat("1;", 40)), true); len(rest) != 0 {
		t.Errorf("a runaway sequence must be dropped, not held forever: %q", rest)
	}
	// Half a UTF-8 character waits for its other half.
	evs, rest = ParseInput([]byte("\xc3"), false)
	if len(evs) != 0 || len(rest) != 1 {
		t.Fatalf("got %v rest %q", evs, rest)
	}
	if evs, _ = ParseInput(append(rest, 0xa9), false); !reflect.DeepEqual(evs, []Event{rk('é')}) {
		t.Errorf("got %v", evs)
	}
}

// A mouse report that arrives in two reads with the flush timer firing between
// them (ssh, a loaded machine) is one wheel notch: no Esc, and no digits read
// as keys.
func TestSplitSGRReportAcrossFlushYieldsNoEscAndNoStrayRunes(t *testing.T) {
	for cut := 1; cut < len("\x1b[<64;10;5M"); cut++ {
		full := "\x1b[<64;10;5M"
		evs, rest := ParseInput([]byte(full[:cut]), true) // the timer fired mid-report
		if cut == 1 {
			// A lone ESC is indistinguishable from Esc until more arrives.
			if !reflect.DeepEqual(evs, []Event{KeyEvent{Key: KeyEsc}}) {
				t.Fatalf("cut %d: %v", cut, evs)
			}
			continue
		}
		if len(evs) != 0 {
			t.Errorf("cut at %d: the head produced %v", cut, evs)
		}
		evs, rest = ParseInput(append(rest, full[cut:]...), true)
		if !reflect.DeepEqual(evs, []Event{MouseEvent{ButtonWheelUp, 9, 4}}) || len(rest) != 0 {
			t.Errorf("cut at %d: got %v rest %q", cut, evs, rest)
		}
	}
}
