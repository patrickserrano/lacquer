package inboxwatch

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"
)

// Terminal control, written by hand. The alternate screen keeps the operator's
// scrollback intact; 1000 asks for press and release reports and 1006 for the
// SGR form of them, which (unlike the older encoding) has room for a wheel
// notch and any column.
const (
	enterSeq = "\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1006h"
	// leaveSeq undoes enterSeq in reverse order, and resets colours, so the
	// shell prompt that follows is not left in the last row's style.
	leaveSeq = "\x1b[0m\x1b[?1006l\x1b[?1000l\x1b[?25h\x1b[?1049l"
)

// Term is the terminal a Program runs on. Everything that touches the real one
// is a field, so a test supplies pipes and a fake raw mode.
type Term struct {
	In  io.Reader
	Out io.Writer
	// MakeRaw puts the terminal in raw mode and returns how to undo it.
	MakeRaw func() (restore func() error, err error)
	Size    func() (w, h int, err error)
	// Signals ends the run (SIGINT, SIGTERM, SIGHUP); Resize reports a new size.
	Signals <-chan os.Signal
	Resize  <-chan os.Signal

	TickEvery time.Duration
	// EscWait is how long a lone ESC waits for the rest of an arrow key before
	// it is taken as the Esc key.
	EscWait time.Duration
	Now     func() time.Time
}

// SystemTerm is the process's own terminal.
func SystemTerm() Term {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	fd := int(os.Stdin.Fd())
	return Term{
		In:  os.Stdin,
		Out: os.Stdout,
		MakeRaw: func() (func() error, error) {
			old, err := term.MakeRaw(fd)
			if err != nil {
				return nil, err
			}
			return func() error { return term.Restore(fd, old) }, nil
		},
		Size:      func() (int, int, error) { return term.GetSize(int(os.Stdout.Fd())) },
		Signals:   sig,
		Resize:    winch,
		TickEvery: 500 * time.Millisecond,
		EscWait:   25 * time.Millisecond,
		Now:       time.Now,
	}
}

// panicked carries a panic from a goroutine to the loop, which re-raises it
// where the deferred restore is, so a crash cannot leave the terminal raw.
type panicked struct{ v any }

// Run drives p on t until p is done, input ends, or a signal arrives. However
// it ends, including by panic, the terminal is put back: raw mode off, cursor
// shown, mouse reporting off, main screen restored.
func Run(t Term, p Program, env Env) error {
	unraw, err := t.MakeRaw()
	if err != nil {
		return fmt.Errorf("terminal: %w", err)
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			io.WriteString(t.Out, leaveSeq)
			unraw()
		})
	}
	defer restore()
	io.WriteString(t.Out, enterSeq)

	w, h, err := t.Size()
	if err != nil {
		return fmt.Errorf("terminal size: %w", err)
	}

	if t.TickEvery <= 0 {
		t.TickEvery = 500 * time.Millisecond
	}
	if t.EscWait <= 0 {
		t.EscWait = 25 * time.Millisecond
	}
	if t.Now == nil {
		t.Now = time.Now
	}

	events := make(chan Event, 64)
	chunks := make(chan []byte)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := t.In.Read(buf)
			if n > 0 {
				select {
				case chunks <- append([]byte(nil), buf[:n]...):
				case <-stop:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	var pending []Cmd
	step := func(ev Event) {
		var cmds []Cmd
		p, cmds = p.Update(ev)
		pending = append(pending, cmds...)
	}
	launch := func() {
		for _, c := range pending {
			go func(c Cmd) {
				defer func() {
					if v := recover(); v != nil {
						events <- panicked{v}
					}
				}()
				events <- env.Exec(c)
			}(c)
		}
		pending = nil
	}

	var last string
	draw := func(force bool) {
		f := p.View()
		var b strings.Builder
		if force {
			b.WriteString("\x1b[2J")
		}
		for i, l := range f.Lines {
			fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[K", i+1, l)
		}
		if f.ShowCursor {
			fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", f.CursorY+1, f.CursorX+1)
		} else {
			b.WriteString("\x1b[?25l")
		}
		if s := b.String(); force || s != last {
			io.WriteString(t.Out, s)
			last = s
		}
	}

	step(ResizeEvent{W: w, H: h})
	step(TickEvent{Now: t.Now()})
	launch()
	draw(true)

	tick := time.NewTicker(t.TickEvery)
	defer tick.Stop()
	esc := time.NewTimer(time.Hour)
	esc.Stop()
	var buf []byte
	for !p.Done() {
		force := false
		select {
		case c, ok := <-chunks:
			if !ok {
				return nil // input closed: the popup or terminal went away
			}
			buf = append(buf, c...)
			var evs []Event
			evs, buf = ParseInput(buf, false)
			for _, ev := range evs {
				step(ev)
			}
			if len(buf) > 0 {
				esc.Reset(t.EscWait)
			}
		case <-esc.C:
			var evs []Event
			evs, buf = ParseInput(buf, true)
			for _, ev := range evs {
				step(ev)
			}
		case ev := <-events:
			if pv, ok := ev.(panicked); ok {
				panic(pv.v)
			}
			step(ev)
		case <-tick.C:
			step(TickEvent{Now: t.Now()})
		case <-t.Signals:
			return nil
		case <-t.Resize:
			if w, h, err := t.Size(); err == nil {
				step(ResizeEvent{W: w, H: h})
				force = true
			}
		}
		launch()
		draw(force)
	}
	return nil
}
