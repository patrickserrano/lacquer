package inboxwatch

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/inbox"
)

func popupEnv(fc *fakeCmd) Env {
	return Env{Cmd: fc, InTmux: true, PopupArgv: func(id string) []string { return []string{"/bin/lacquer", "popup", id} }}
}

// tmuxVerbs is the tmux subcommand sequence of a run, with the arguments that
// matter for the mouse toggle and none of the popup's own.
func tmuxVerbs(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, "tmux display-popup") {
			out = append(out, "display-popup")
			continue
		}
		out = append(out, c)
	}
	return out
}

// The mouse is turned off before the popup and put back to the session's own
// value after it, and never with -g.
func TestPopupTurnsMouseOffAndRestoresTheSessionValue(t *testing.T) {
	fc := &fakeCmd{out: map[string]string{"tmux show": "on\n"}}
	ev := popupEnv(fc).Exec(Cmd{Kind: CmdPopup, ID: "a1"}).(DoneEvent)
	if !ev.OK {
		t.Fatalf("ev = %+v", ev)
	}
	want := []string{"tmux show -qv mouse", "tmux set mouse off", "display-popup", "tmux set mouse on"}
	if got := tmuxVerbs(fc.calls); !reflect.DeepEqual(got, want) {
		t.Errorf("calls\n%q\nwant\n%q", got, want)
	}
	for _, c := range fc.calls {
		if strings.Contains(c, " -g") {
			t.Errorf("a global tmux option was touched: %q", c)
		}
	}
}

// A session with no value of its own gets it unset, so the global one applies
// again, rather than a copy of the global frozen into the session.
func TestPopupUnsetsMouseWhenTheSessionHadNoValue(t *testing.T) {
	fc := &fakeCmd{out: map[string]string{"tmux show": "\n"}}
	popupEnv(fc).Exec(Cmd{Kind: CmdPopup, ID: "a1"})
	want := []string{"tmux show -qv mouse", "tmux set mouse off", "display-popup", "tmux set -u mouse"}
	if got := tmuxVerbs(fc.calls); !reflect.DeepEqual(got, want) {
		t.Errorf("calls\n%q\nwant\n%q", got, want)
	}
}

// The restore runs when the popup command fails, and the failure is still reported.
func TestPopupRestoresMouseWhenDisplayPopupFails(t *testing.T) {
	fc := &fakeCmd{out: map[string]string{"tmux show": "on"}, fail: map[string]error{"tmux display-popup": errors.New("boom")}}
	ev := popupEnv(fc).Exec(Cmd{Kind: CmdPopup, ID: "a1"}).(DoneEvent)
	if ev.OK || !strings.Contains(ev.Note, "boom") {
		t.Errorf("ev = %+v, want the display-popup error reported", ev)
	}
	want := []string{"tmux show -qv mouse", "tmux set mouse off", "display-popup", "tmux set mouse on"}
	if got := tmuxVerbs(fc.calls); !reflect.DeepEqual(got, want) {
		t.Errorf("calls\n%q\nwant\n%q", got, want)
	}
}

// Every popup kind goes through showPopup, so the Later issue popup gets it too.
func TestIssuePopupTurnsMouseOffToo(t *testing.T) {
	fc := &fakeCmd{out: map[string]string{"tmux show": "on"}}
	env := Env{Cmd: fc, InTmux: true, IssueArgv: func(ref string) []string { return []string{"/bin/lacquer", "popup", "--issue-hex=6f"} }}
	env.Exec(Cmd{Kind: CmdPopupIssue, ID: "o/r#1"})
	want := []string{"tmux show -qv mouse", "tmux set mouse off", "display-popup", "tmux set mouse on"}
	if got := tmuxVerbs(fc.calls); !reflect.DeepEqual(got, want) {
		t.Errorf("calls\n%q\nwant\n%q", got, want)
	}
}

// y copies title, body and ref as the entry holds them: not wrapped to the
// popup's width, and not the cleaned, decorated text the view draws.
func TestYCopiesTheWholeItemUnwrapped(t *testing.T) {
	long := strings.Repeat("word ", 60)
	cmdLine := "lacquer sync --repo acme/widgets --profile ios --dry-run --verbose --some-very-long-flag value-that-would-wrap"
	d := NewDetail("a1", true, 40, 20)
	d.Loaded, d.Found = true, true
	d.Entry = inbox.Entry{ID: "a1", Type: inbox.Action, Title: "Pick " + long, Body: "Run:\n" + cmdLine, Ref: "https://github.com/acme/w/pull/5"}
	_, cmds := d.Update(KeyEvent{Key: KeyRune, Rune: 'y'})
	if len(cmds) != 1 || cmds[0].Kind != CmdCopy {
		t.Fatalf("cmds = %+v", cmds)
	}
	want := strings.TrimSpace("Pick "+long) + "\n\nRun:\n" + cmdLine + "\n\nhttps://github.com/acme/w/pull/5"
	if got := cmds[0].Text; got != want {
		t.Errorf("copied\n%q\nwant\n%q", got, want)
	}

	// Through Exec it reaches pbcopy as stdin, and c still copies only the id.
	fc := &fakeCmd{}
	Env{Cmd: fc}.Exec(cmds[0])
	if fc.calls[0] != "pbcopy" || fc.stdin[0] != want {
		t.Errorf("pbcopy ran %q with stdin %q", fc.calls[0], fc.stdin[0])
	}
	_, cmds = d.Update(KeyEvent{Key: KeyRune, Rune: 'c'})
	if len(cmds) != 1 || cmds[0].Text != "a1" {
		t.Errorf("c copied %+v, want the id", cmds)
	}
}

// Before the entry has loaded there is nothing to copy, and y says so.
func TestYBeforeTheEntryLoadsCopiesNothing(t *testing.T) {
	d := NewDetail("a1", true, 40, 20)
	p, cmds := d.Update(KeyEvent{Key: KeyRune, Rune: 'y'})
	if len(cmds) != 0 || !strings.Contains(p.(Detail).Note, "nothing to copy") {
		t.Errorf("cmds %+v note %q", cmds, p.(Detail).Note)
	}
}
