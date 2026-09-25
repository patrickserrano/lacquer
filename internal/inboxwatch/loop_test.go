package inboxwatch

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

// safeBuf is a terminal's output, written by the loop and read by the test.
type safeBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fakeTerm struct {
	Term
	in       *io.PipeWriter
	out      *safeBuf
	raw      atomic.Int32
	unraw    atomic.Int32
	sig      chan os.Signal
	winch    chan os.Signal
	w, h     atomic.Int32
	rawError error
}

func newFakeTerm() *fakeTerm {
	pr, pw := io.Pipe()
	f := &fakeTerm{in: pw, out: &safeBuf{}, sig: make(chan os.Signal, 1), winch: make(chan os.Signal, 1)}
	f.w.Store(80)
	f.h.Store(12)
	f.Term = Term{
		In:  pr,
		Out: f.out,
		MakeRaw: func() (func() error, error) {
			if f.rawError != nil {
				return nil, f.rawError
			}
			f.raw.Add(1)
			return func() error { f.unraw.Add(1); return nil }, nil
		},
		Size:      func() (int, int, error) { return int(f.w.Load()), int(f.h.Load()), nil },
		Signals:   f.sig,
		Resize:    f.winch,
		TickEvery: 10 * time.Millisecond,
		EscWait:   20 * time.Millisecond,
		Now:       func() time.Time { return t0 },
	}
	return f
}

// run starts the loop and returns what it returned, and whether it panicked.
func (f *fakeTerm) run(p Program, env Env) <-chan any {
	done := make(chan any, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- v
			}
		}()
		done <- Run(f.Term, p, env)
	}()
	return done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func finish(t *testing.T, done <-chan any) any {
	t.Helper()
	select {
	case v := <-done:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
		return nil
	}
}

// The terminal is always put back: raw mode off, cursor shown, mouse reporting
// off, main screen restored. Each way out of the loop is a test, and each test
// checks all four.
func assertRestored(t *testing.T, f *fakeTerm) {
	t.Helper()
	out := f.out.String()
	if f.raw.Load() != 1 || f.unraw.Load() != 1 {
		t.Errorf("raw mode entered %d times and left %d, want 1 and 1", f.raw.Load(), f.unraw.Load())
	}
	if !strings.HasPrefix(out, enterSeq) {
		t.Errorf("output does not start by entering: %q", out[:min(len(out), 60)])
	}
	i := strings.LastIndex(out, leaveSeq)
	if i < 0 || i+len(leaveSeq) != len(out) {
		t.Fatalf("output does not END with the restore sequence %q; it ends %q", leaveSeq, out[max(len(out)-60, 0):])
	}
	for _, want := range []string{"\x1b[?1006l", "\x1b[?1000l", "\x1b[?25h", "\x1b[?1049l"} {
		if !strings.Contains(leaveSeq, want) {
			t.Errorf("restore lacks %q", want)
		}
	}
	// Every mode turned on has been turned off.
	for on, off := range map[string]string{"\x1b[?1000h": "\x1b[?1000l", "\x1b[?1006h": "\x1b[?1006l", "\x1b[?1049h": "\x1b[?1049l", "\x1b[?25l": "\x1b[?25h"} {
		if strings.Contains(out, on) && strings.LastIndex(out, off) < strings.LastIndex(out, on) {
			t.Errorf("%q was turned on and not off again", on)
		}
	}
}

func TestTerminalRestoredAfterQ(t *testing.T) {
	f := newFakeTerm()
	done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: writeInbox(t)})
	waitFor(t, "the first frame", func() bool { return strings.Contains(f.out.String(), "inbox clear") })
	f.in.Write([]byte("q"))
	if v := finish(t, done); v != nil {
		t.Fatalf("Run returned %v", v)
	}
	assertRestored(t, f)
}

func TestTerminalRestoredOnSIGTERMAndSIGINT(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, os.Kill} {
		f := newFakeTerm()
		done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: writeInbox(t)})
		waitFor(t, "the first frame", func() bool { return strings.Contains(f.out.String(), "inbox clear") })
		f.sig <- sig
		if v := finish(t, done); v != nil {
			t.Fatalf("Run returned %v", v)
		}
		assertRestored(t, f)
	}
}

func TestTerminalRestoredWhenInputCloses(t *testing.T) {
	f := newFakeTerm()
	done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: writeInbox(t)})
	waitFor(t, "the first frame", func() bool { return strings.Contains(f.out.String(), "inbox clear") })
	f.in.Close() // the popup or the terminal went away
	if v := finish(t, done); v != nil {
		t.Fatalf("Run returned %v", v)
	}
	assertRestored(t, f)
}

type panicky struct{ Model }

func (p panicky) Update(ev Event) (Program, []Cmd) {
	if k, ok := ev.(KeyEvent); ok && k.Rune == 'x' {
		panic("boom in the model")
	}
	m, c := p.Model.Update(ev)
	return panicky{m.(Model)}, c
}

func TestTerminalRestoredWhenTheModelPanics(t *testing.T) {
	f := newFakeTerm()
	done := f.run(panicky{NewModel(cfgReply, 0, 0)}, Env{InboxPath: writeInbox(t)})
	waitFor(t, "the first frame", func() bool { return strings.Contains(f.out.String(), "inbox clear") })
	f.in.Write([]byte("x"))
	if v := finish(t, done); v != "boom in the model" {
		t.Fatalf("the panic must still surface, got %v", v)
	}
	assertRestored(t, f)
}

// A panic in the goroutine that performs a side effect would otherwise kill the
// process with the terminal still raw.
func TestTerminalRestoredWhenASideEffectPanics(t *testing.T) {
	f := newFakeTerm()
	it := item("aaa", inbox.Action, time.Hour, "row")
	it.Ref = "https://example.com"
	path := writeInbox(t, inbox.Entry{ID: "aaa", Type: inbox.Action, Title: "row", Ref: "https://example.com"})
	// Env.Cmd is nil, so the open below dereferences it in the exec goroutine.
	done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: path})
	waitFor(t, "the row", func() bool { return strings.Contains(f.out.String(), "row") })
	f.in.Write([]byte("o"))
	v := finish(t, done)
	if v == nil {
		t.Fatal("the panic was swallowed")
	}
	assertRestored(t, f)
}

func TestNoRawModeMeansNoOutput(t *testing.T) {
	f := newFakeTerm()
	f.rawError = errors.New("not a terminal")
	err := Run(f.Term, NewModel(cfgReply, 0, 0), Env{})
	if err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("err = %v", err)
	}
	if f.out.String() != "" {
		t.Errorf("wrote to a terminal it could not put in raw mode: %q", f.out.String())
	}
}

// End to end through the loop: the list draws from a real inbox file, and the SGR
// wheel bytes scroll it with no tmux binding anywhere.
func TestLoopDrawsScrollsAndReadsSGRWheelBytes(t *testing.T) {
	var entries []inbox.Entry
	for i := 0; i < 30; i++ {
		entries = append(entries, inbox.Entry{Type: inbox.Unread, Title: "entry number " + string(rune('A'+i%26)) + string(rune('a'+i/26)), CreatedAt: t0.Add(-time.Hour)})
	}
	f := newFakeTerm()
	f.h.Store(9) // 6 rows of list
	done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: writeInbox(t, entries...)})
	waitFor(t, "the first page", func() bool { return strings.Contains(f.out.String(), " 1–6 of 30 ") })
	f.in.Write([]byte("\x1b[<65;10;5M")) // one wheel-down notch
	waitFor(t, "the scrolled page", func() bool { return strings.Contains(f.out.String(), " 4–9 of 30 ") })
	f.in.Write([]byte("\x1b[<64;10;5M"))
	waitFor(t, "back at the top", func() bool {
		return strings.LastIndex(f.out.String(), " 1–6 of 30 ") > strings.LastIndex(f.out.String(), " 4–9 of 30 ")
	})
	f.in.Write([]byte("q"))
	if v := finish(t, done); v != nil {
		t.Fatalf("Run returned %v", v)
	}
	assertRestored(t, f)
}

func TestResizeRedrawsAtTheNewSize(t *testing.T) {
	f := newFakeTerm()
	done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: writeInbox(t)})
	waitFor(t, "the first frame", func() bool { return strings.Contains(f.out.String(), "\x1b[12;1H") })
	f.h.Store(20)
	f.winch <- os.Interrupt
	waitFor(t, "a 20-row frame", func() bool { return strings.Contains(f.out.String(), "\x1b[20;1H") })
	f.in.Write([]byte("q"))
	finish(t, done)
	assertRestored(t, f)
}

// The flush timer fires between the two halves of a mouse report, and Esc does
// not quit the list anyway: the watcher survives and the report still scrolls.
func TestLoopSurvivesAMouseReportSplitAcrossTheEscTimer(t *testing.T) {
	var entries []inbox.Entry
	for i := 0; i < 30; i++ {
		entries = append(entries, inbox.Entry{Type: inbox.Unread, Title: "entry " + string(rune('A'+i%26)) + string(rune('a'+i/26)), CreatedAt: t0.Add(-time.Hour)})
	}
	f := newFakeTerm()
	f.h.Store(9)
	done := f.run(NewModel(cfgReply, 0, 0), Env{InboxPath: writeInbox(t, entries...)})
	waitFor(t, "the first page", func() bool { return strings.Contains(f.out.String(), " 1–6 of 30 ") })
	f.in.Write([]byte("\x1b[<65;1"))
	time.Sleep(150 * time.Millisecond) // several EscWaits (20ms)
	select {
	case v := <-done:
		t.Fatalf("the watcher quit on half a mouse report: %v", v)
	default:
	}
	f.in.Write([]byte("0;5M"))
	waitFor(t, "the notch to scroll", func() bool { return strings.Contains(f.out.String(), " 4–9 of 30 ") })
	f.in.Write([]byte("\x1b")) // a real Esc is delivered, and is ignored by the list
	time.Sleep(100 * time.Millisecond)
	select {
	case v := <-done:
		t.Fatalf("Esc quit the list: %v", v)
	default:
	}
	f.in.Write([]byte("q"))
	finish(t, done)
	assertRestored(t, f)
}
