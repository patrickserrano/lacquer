package main

import (
	"bytes"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
	"github.com/patrickserrano/lacquer/internal/inboxwatch"
)

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// untilReader is a terminal's input that sends keys once the screen shows what
// the test is waiting for.
type untilReader struct {
	out  *lockedBuf
	want string
	keys string
	sent bool
}

func (r *untilReader) Read(p []byte) (int, error) {
	for i := 0; i < 500 && !strings.Contains(r.out.String(), r.want); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, r.keys), nil
}

// fakeTerminal replaces the process's terminal for one test.
func fakeTerminal(t *testing.T, wantOnScreen, keys string) *lockedBuf {
	t.Helper()
	out := &lockedBuf{}
	oldTerm, oldTTY := systemTerm, stdinIsTerminal
	t.Cleanup(func() { systemTerm, stdinIsTerminal = oldTerm, oldTTY })
	stdinIsTerminal = func() bool { return true }
	systemTerm = func() inboxwatch.Term {
		return inboxwatch.Term{
			In: &untilReader{out: out, want: wantOnScreen, keys: keys}, Out: out,
			MakeRaw:   func() (func() error, error) { return func() error { return nil }, nil },
			Size:      func() (int, int, error) { return 100, 12, nil },
			TickEvery: 10 * time.Millisecond, EscWait: 10 * time.Millisecond, Now: time.Now,
		}
	}
	return out
}

func writeFixtureInbox(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if _, err := inbox.Add(path, inbox.Entry{ID: "fx01", Type: inbox.Action, Title: "fixture decision needed"}); err != nil {
		t.Fatal(err)
	}
	return path
}

// End to end through main.go: `console inbox watch` reads the inbox named by
// --inbox, draws it, and leaves the terminal restored.
func TestConsoleInboxWatchDrawsTheInboxAndRestoresTheTerminal(t *testing.T) {
	lq := realLacquer(t)
	path := writeFixtureInbox(t)
	out := fakeTerminal(t, "fixture decision needed", "q")
	var stdout, stderr bytes.Buffer
	env := envMap(map[string]string{"LACQUER_ROOT": lq, "XDG_STATE_HOME": t.TempDir()})
	if code := run([]string{"console", "--inbox", path, "inbox", "watch"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	got := out.String()
	for _, want := range []string{"fixture decision needed", "1 need you", "fx01", "reply off: no overseer pane", "merges not recorded: no roster"} {
		if !strings.Contains(plainOut(got), want) {
			t.Errorf("screen lacks %q:\n%s", want, plainOut(got))
		}
	}
	if !strings.HasSuffix(got, "\x1b[?1049l") || !strings.Contains(got, "\x1b[?1006l") {
		t.Errorf("the terminal was not restored: %q", got[max(len(got)-60, 0):])
	}
}

// The overseer pane comes from the flag or the environment, and reply is off
// with neither: the list's hint says which.
func TestConsoleInboxWatchOverseerFromFlagOrEnv(t *testing.T) {
	lq := realLacquer(t)
	path := writeFixtureInbox(t)
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		off  bool
	}{
		{"flag", []string{"--overseer-pane", "%3"}, nil, false},
		{"env pane", nil, map[string]string{"LACQUER_OVERSEER_PANE": "%3"}, false},
		{"env title", nil, map[string]string{"LACQUER_OVERSEER_TITLE": "lead"}, false},
		{"neither", nil, nil, true},
	} {
		out := fakeTerminal(t, "fixture decision needed", "q")
		env := map[string]string{"LACQUER_ROOT": lq, "XDG_STATE_HOME": t.TempDir()}
		for k, v := range tc.env {
			env[k] = v
		}
		args := append([]string{"console", "--inbox", path}, tc.args...)
		var stdout, stderr bytes.Buffer
		if code := run(append(args, "inbox", "watch"), envMap(env), &stdout, &stderr); code != 0 {
			t.Fatalf("%s: code %d: %s", tc.name, code, stderr.String())
		}
		if off := strings.Contains(plainOut(out.String()), "reply off"); off != tc.off {
			t.Errorf("%s: reply-off hint = %v, want %v", tc.name, off, tc.off)
		}
	}
}

func TestConsoleInboxWatchNeedsATerminal(t *testing.T) {
	lq := realLacquer(t)
	old := stdinIsTerminal
	defer func() { stdinIsTerminal = old }()
	stdinIsTerminal = func() bool { return false }
	var stdout, stderr bytes.Buffer
	env := envMap(map[string]string{"LACQUER_ROOT": lq, "XDG_STATE_HOME": t.TempDir()})
	code := run([]string{"console", "--inbox", writeFixtureInbox(t), "inbox", "watch"}, env, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "terminal") {
		t.Errorf("code %d, stderr %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("wrote %q to a pipe", stdout.String())
	}
}

func TestOverseerFlagsAreScopedToInboxWatch(t *testing.T) {
	lq := realLacquer(t)
	for _, args := range [][]string{
		{"--overseer-pane", "%1", "inbox", "list"},
		{"--overseer-title", "x", "kill", "a"},
		{"--overseer-session", "s"},
	} {
		var stdout, stderr bytes.Buffer
		env := envMap(map[string]string{"LACQUER_ROOT": lq, "XDG_STATE_HOME": t.TempDir()})
		code := run(append([]string{"console"}, args...), env, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "applies only to inbox watch") {
			t.Errorf("%v: code %d stderr %q", args, code, stderr.String())
		}
	}
}

// The popup is run by the tmux server, without the shell's environment, so it
// must not need a lacquer checkout or LACQUER_ROOT to start; it is the same
// screen as the list's detail.
func TestInboxPopupRunsWithoutALacquerRoot(t *testing.T) {
	path := writeFixtureInbox(t)
	out := fakeTerminal(t, "fixture decision needed", "q")
	var stdout, stderr bytes.Buffer
	env := rawEnv(map[string]string{"XDG_STATE_HOME": t.TempDir()}) // no LACQUER_ROOT at all
	code := run([]string{"console", "inbox", "popup", "--inbox", path, "fx01"}, env, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	got := plainOut(out.String())
	for _, want := range []string{"ACTION  fx01", "fixture decision needed", "● waiting on you", "r reply (off: no overseer pane)"} {
		if !strings.Contains(got, want) {
			t.Errorf("popup lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(out.String(), "\x1b[?1049l") {
		t.Errorf("the terminal was not restored")
	}
}

func TestInboxPopupNeedsAnIDAndATerminal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := rawEnv(map[string]string{"XDG_STATE_HOME": t.TempDir()})
	if code := run([]string{"console", "inbox", "popup"}, env, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "usage") {
		t.Errorf("no id: code %d stderr %q", code, stderr.String())
	}
	old := stdinIsTerminal
	defer func() { stdinIsTerminal = old }()
	stdinIsTerminal = func() bool { return false }
	stderr.Reset()
	if code := run([]string{"console", "inbox", "popup", "x"}, env, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "needs a terminal") {
		t.Errorf("no terminal: code %d stderr %q", code, stderr.String())
	}
}

// What the list runs in the tmux popup carries the inbox and overseer settings
// with it, because the tmux server does not have them.
func TestPopupCommandCarriesTheConfiguration(t *testing.T) {
	old := executablePath
	defer func() { executablePath = old }()
	executablePath = func() (string, error) { return "/opt/bin/lacquer", nil }
	env := newWatchEnv("/state/inbox.jsonl", false, overseerFlags{pane: "%3", session: "=ops"}, fleetRoster(), envMap(nil))
	got := strings.Join(env.PopupArgv("a1b2"), " ")
	want := "/opt/bin/lacquer console inbox popup --inbox /state/inbox.jsonl --overseer-pane %3 --overseer-session =ops a1b2"
	if got != want {
		t.Errorf("popup argv\n%s\nwant\n%s", got, want)
	}
	if env.Resolve == nil || env.InTmux {
		t.Errorf("env = %+v", env)
	}
}

var ansiOut = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func plainOut(s string) string { return ansiOut.ReplaceAllString(s, "") }

func fleetRoster() fleet.Roster { return fleet.Roster{} }
