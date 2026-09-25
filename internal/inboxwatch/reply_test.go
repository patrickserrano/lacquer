package inboxwatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
	"github.com/patrickserrano/lacquer/internal/inbox"
)

// Lines written by foxy-inbox's log_reply, which is json.dumps of the record:
//
//	python3 -c 'import json;print(json.dumps({"id":"a1b2c3d4e5","at":"2026-09-24T14:02:11","text":T}))'
//
// The replies file is shared by the two tools while one replaces the other, and
// the phone mirror may read it, so the bytes must be the same, not merely
// equivalent JSON.
const (
	pyPlain   = `{"id": "a1b2c3d4e5", "at": "2026-09-24T14:02:11", "text": "plain reply"}` + "\n"
	pyAwkward = `{"id": "a1b2c3d4e5", "at": "2026-09-24T14:02:11", "text": "say \"hi\" \\ path \u00e9 \ud83d\ude00\ttab\nnl \u0007 <&> \u007f end"}` + "\n"
)

func TestReplyLineIsByteIdenticalToFoxyInboxs(t *testing.T) {
	at := time.Date(2026, 9, 24, 14, 2, 11, 0, time.Local)
	if got := replyLine("a1b2c3d4e5", at, "plain reply"); got != pyPlain {
		t.Errorf("got  %q\nwant %q", got, pyPlain)
	}
	awkward := "say \"hi\" \\ path é 😀\ttab\nnl \a <&> \x7f end"
	if got := replyLine("a1b2c3d4e5", at, awkward); got != pyAwkward {
		t.Errorf("got  %q\nwant %q", got, pyAwkward)
	}
}

func TestAppendReplyAppendsToTheFileFoxyInboxWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), RepliesFile)
	// A line foxy-inbox wrote earlier is kept, and read back.
	if err := os.WriteFile(path, []byte(pyPlain), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 24, 14, 2, 11, 0, time.Local)
	if err := AppendReply(path, "a1b2c3d4e5", at, "plain reply"); err != nil {
		t.Fatal(err)
	}
	if err := AppendReply(path, "zzz", at, "é"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if want := pyPlain + pyPlain + `{"id": "zzz", "at": "2026-09-24T14:02:11", "text": "\u00e9"}` + "\n"; string(b) != want {
		t.Errorf("file:\n%q\nwant\n%q", b, want)
	}
	got, err := ReadReplies(path)
	if err != nil || got["a1b2c3d4e5"] != (Reply{"2026-09-24T14:02:11", "plain reply"}) || got["zzz"].Text != "é" {
		t.Errorf("read back %v %v", got, err)
	}
}

// foxy-inbox skips a line that is not a reply record, and the latest reply for
// an id wins.
func TestReadRepliesSkipsBadLinesAndTheLatestWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), RepliesFile)
	body := `{"id": "a", "at": "2026-09-24T10:00:00", "text": "first"}` + "\n" +
		"not json\n" +
		`{"id": "b", "at": "2026-09-24T10:00:00"}` + "\n" + // no text
		`{"id": "a", "at": "2026-09-24T11:00:00", "text": "second"}` + "\n"
	os.WriteFile(path, []byte(body), 0o644)
	got, err := ReadReplies(path)
	if err != nil || len(got) != 1 || got["a"].Text != "second" {
		t.Errorf("got %v %v", got, err)
	}
	if got, err := ReadReplies(filepath.Join(t.TempDir(), "absent")); err != nil || len(got) != 0 {
		t.Errorf("a missing file is no replies: %v %v", got, err)
	}
}

func envFor(t *testing.T, o Overseer, c Commander) (Env, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	return Env{InboxPath: path, Overseer: o, Cmd: c, Now: func() time.Time { return time.Date(2026, 9, 24, 14, 2, 11, 0, time.Local) },
		Resolve: inbox.Resolve}, path
}

func TestReplyTypesTheTaggedLineIntoTheOverseerPaneAndRecordsIt(t *testing.T) {
	fc := &fakeCmd{}
	env, path := envFor(t, Overseer{Pane: "%7"}, fc)
	ev := env.Exec(Cmd{Kind: CmdReply, ID: "a1b2c3d4e5", Text: "yes, ship it"}).(RepliedEvent)
	if !ev.OK {
		t.Fatalf("reply failed: %s", ev.Note)
	}
	want := []string{
		"tmux send-keys -t %7 -l [inbox a1b2c3d4e5] yes, ship it",
		"tmux send-keys -t %7 Enter",
	}
	if strings.Join(fc.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("tmux calls:\n%s\nwant\n%s", strings.Join(fc.calls, "\n"), strings.Join(want, "\n"))
	}
	b, _ := os.ReadFile(RepliesPath(path))
	if want := `{"id": "a1b2c3d4e5", "at": "2026-09-24T14:02:11", "text": "yes, ship it"}` + "\n"; string(b) != want {
		t.Errorf("replies file %q, want %q", b, want)
	}
}

// A reply the overseer never received must not read as answered.
func TestFailedSendIsNotRecorded(t *testing.T) {
	for _, failing := range []string{"tmux send-keys"} {
		fc := &fakeCmd{fail: map[string]error{failing: errors.New("can't find pane: %7")}}
		env, path := envFor(t, Overseer{Pane: "%7"}, fc)
		ev := env.Exec(Cmd{Kind: CmdReply, ID: "a", Text: "x"}).(RepliedEvent)
		if ev.OK || !strings.Contains(ev.Note, "can't find pane") {
			t.Errorf("ev = %+v", ev)
		}
		if _, err := os.Stat(RepliesPath(path)); err == nil {
			t.Errorf("a reply that was not sent was recorded")
		}
		if len(fc.calls) != 1 {
			t.Errorf("after the text failed, Enter was still sent: %v", fc.calls)
		}
	}
}

func TestUnconfiguredOverseerDisablesReply(t *testing.T) {
	fc := &fakeCmd{}
	env, path := envFor(t, Overseer{}, fc)
	ev := env.Exec(Cmd{Kind: CmdReply, ID: "a", Text: "x"}).(RepliedEvent)
	if ev.OK || !strings.HasPrefix(ev.Note, "reply disabled: no overseer pane configured") {
		t.Errorf("ev = %+v", ev)
	}
	// Resolving the pane refuses too, on its own: nothing downstream may guess.
	if pane, err := (Overseer{}).target(fc); pane != "" || err == nil {
		t.Errorf("target of an unconfigured overseer = %q, %v", pane, err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("with no pane configured, tmux was called: %v", fc.calls)
	}
	if _, err := os.Stat(RepliesPath(path)); err == nil {
		t.Errorf("a reply was recorded with nowhere to send it")
	}
	if (Overseer{}).Configured() || !(Overseer{Pane: "%1"}).Configured() || !(Overseer{Title: "x"}).Configured() {
		t.Errorf("Configured is wrong")
	}
	// In the popup the r key says so and opens no reply box.
	d := loadedDetail(t, Overseer{}.Configured(), inbox.Entry{ID: "a", Type: inbox.Action, Title: "t"})
	p, cmds := feed(t, d, "r")
	if len(cmds) != 0 || p.(Detail).Replying || !strings.Contains(plain(p.View().Lines[p.(Detail).H-1]), "reply disabled: no overseer pane configured") {
		t.Errorf("r with no overseer: replying=%v bar=%q", p.(Detail).Replying, plain(p.View().Lines[p.(Detail).H-1]))
	}
	if h := plain(d.View().Lines[d.H-1]); !strings.Contains(h, "r reply (off: no overseer pane)") {
		t.Errorf("hint = %q", h)
	}
}

func TestOverseerFoundByTitleNeverGuessed(t *testing.T) {
	list := "tmux list-panes"
	for _, tc := range []struct {
		name    string
		out     string
		want    string
		wantErr string
		calls   string
	}{
		{"one match", "%1\tzsh\n%4\tlead\n%9\tvim\n", "%4", "", "tmux list-panes -F #{pane_id}\t#{pane_title} -s -t =work"},
		{"none", "%1\tzsh\n", "", `no pane titled "lead"`, ""},
		{"several", "%4\tlead\n%5\tlead\n", "", `2 panes are titled "lead" (%4, %5)`, ""},
	} {
		fc := &fakeCmd{out: map[string]string{list: tc.out}}
		got, err := Overseer{Title: "lead", Session: "=work"}.target(fc)
		if got != tc.want || (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: got %q, %v", tc.name, got, err)
		}
		if tc.calls != "" && fc.calls[0] != tc.calls {
			t.Errorf("%s: ran %q, want %q", tc.name, fc.calls[0], tc.calls)
		}
	}
	// With no session named it looks in all of them.
	fc := &fakeCmd{out: map[string]string{list: "%4\tlead\n"}}
	if _, err := (Overseer{Title: "lead"}).target(fc); err != nil || !strings.HasSuffix(fc.calls[0], " -a") {
		t.Errorf("ran %v, %v", fc.calls, err)
	}
	// A configured target is used as is; it wins over a title.
	fc = &fakeCmd{}
	if got, err := (Overseer{Pane: "work:0.1", Title: "lead"}).target(fc); got != "work:0.1" || err != nil || len(fc.calls) != 0 {
		t.Errorf("got %q %v %v", got, err, fc.calls)
	}
	// A reply through a title that is ambiguous types nothing.
	fc = &fakeCmd{out: map[string]string{list: "%4\tlead\n%5\tlead\n"}}
	env, _ := envFor(t, Overseer{Title: "lead"}, fc)
	if ev := env.Exec(Cmd{Kind: CmdReply, ID: "a", Text: "x"}).(RepliedEvent); ev.OK || len(fc.calls) != 1 {
		t.Errorf("ev %+v calls %v", ev, fc.calls)
	}
}

func TestSortItemsPutsWhatNeedsTheOperatorFirst(t *testing.T) {
	mk := func(id string, typ inbox.Type, replied bool) Item {
		return Item{ID: id, Type: typ, Replied: replied}
	}
	items := []Item{
		mk("fyi1", inbox.Unread, false), mk("ans1", inbox.Action, true), mk("act1", inbox.Action, false),
		mk("fyiR", inbox.Unread, true), mk("act2", inbox.Action, false), mk("fyi2", inbox.Unread, false), mk("ans2", inbox.Action, true),
	}
	sortItems(items)
	var ids []string
	for _, i := range items {
		ids = append(ids, i.ID)
	}
	// ACTIONs waiting on the operator, then ACTIONs they answered, then FYIs, then
	// FYIs they answered; file order inside each group.
	if got, want := strings.Join(ids, " "), "act1 act2 ans1 ans2 fyi1 fyi2 fyiR"; got != want {
		t.Errorf("order %s, want %s", got, want)
	}
}

func writeInbox(t *testing.T, entries ...inbox.Entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	for _, e := range entries {
		if _, err := inbox.Add(path, e); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLoadReadsOpenEntriesInOrderAndMarksReplied(t *testing.T) {
	path := writeInbox(t,
		inbox.Entry{ID: "f1", Type: inbox.Unread, Title: "fyi one", CreatedAt: t0},
		inbox.Entry{ID: "a1", Type: inbox.Action, Title: "act one", CreatedAt: t0, Ref: "https://x/1"},
		inbox.Entry{ID: "a2", Type: inbox.Action, Title: "act two", CreatedAt: t0},
		inbox.Entry{ID: "gone", Type: inbox.Action, Title: "resolved", CreatedAt: t0})
	if _, err := inbox.Resolve(path, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := AppendReply(RepliesPath(path), "a1", t0, "on it"); err != nil {
		t.Fatal(err)
	}
	ev := Env{InboxPath: path}.Exec(Cmd{Kind: CmdLoad}).(LoadedEvent)
	var got []string
	for _, i := range ev.Data.Items {
		got = append(got, i.ID)
	}
	if strings.Join(got, " ") != "a2 a1 f1" || ev.Err != "" {
		t.Errorf("items %v err %q, want a2 a1 f1 (a resolved entry is not open, a replied one sorts after)", got, ev.Err)
	}
	if !ev.Data.Items[1].Replied || ev.Data.Items[0].Replied || ev.Data.Items[1].Ref != "https://x/1" {
		t.Errorf("items %+v", ev.Data.Items)
	}
}

func TestLoadDistinguishesMissingDefaultFromMissingExplicitInbox(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.jsonl")
	if ev := (Env{InboxPath: missing, InboxDefault: true}).Exec(Cmd{Kind: CmdLoad}).(LoadedEvent); ev.Err != "" || len(ev.Data.Items) != 0 {
		t.Errorf("a default inbox nothing wrote to yet is empty: %+v", ev)
	}
	if ev := (Env{InboxPath: missing}).Exec(Cmd{Kind: CmdLoad}).(LoadedEvent); ev.Err == "" {
		t.Errorf("an --inbox that does not exist is a misconfiguration, not an empty inbox")
	}
	path := writeInbox(t, inbox.Entry{Type: inbox.Action, Title: "ok"})
	os.WriteFile(RepliesPath(path), nil, 0o000)
	defer os.Chmod(RepliesPath(path), 0o644)
	if os.Getuid() != 0 {
		if ev := (Env{InboxPath: path}).Exec(Cmd{Kind: CmdLoad}).(LoadedEvent); ev.Warn == "" || len(ev.Data.Items) != 1 {
			t.Errorf("an unreadable replies file must warn and still list the inbox: %+v", ev)
		}
	}
}

// d in the list resolves through inbox.Resolve, the function `console inbox
// resolve` calls, so the file ends up exactly as the CLI would leave it.
func TestResolveGoesThroughInboxResolve(t *testing.T) {
	path := writeInbox(t, inbox.Entry{ID: "a1", Type: inbox.Action, Title: "act"})
	var calledWith string
	env := Env{InboxPath: path, Resolve: func(p, id string) (inbox.Entry, error) { calledWith = p + "|" + id; return inbox.Resolve(p, id) }}
	if ev := env.Exec(Cmd{Kind: CmdResolve, ID: "a1"}).(DoneEvent); !ev.OK || ev.Note != "resolved a1" {
		t.Fatalf("ev = %+v", ev)
	}
	if calledWith != path+"|a1" {
		t.Errorf("Resolve called with %q", calledWith)
	}
	open, _, _ := inbox.ListOpen(path)
	if len(open) != 0 {
		t.Errorf("still open: %v", open)
	}
	if ev := env.Exec(Cmd{Kind: CmdResolve, ID: "a1"}).(DoneEvent); ev.OK || !strings.Contains(ev.Note, "already resolved") {
		t.Errorf("resolving twice: %+v", ev)
	}
}

func TestOpenCopyAndPopupRunTheRightPrograms(t *testing.T) {
	fc := &fakeCmd{}
	env := Env{Cmd: fc, InTmux: true, PopupArgv: func(id string) []string {
		return []string{"/bin/lacquer", "console", "inbox", "popup", "--inbox", "/tmp/my inbox.jsonl", id}
	}}
	env.Exec(Cmd{Kind: CmdOpen, Text: "https://github.com/acme/w/pull/5"})
	env.Exec(Cmd{Kind: CmdCopy, ID: "a1b2", Text: "a1b2"})
	env.Exec(Cmd{Kind: CmdPopup, ID: "a1b2"})
	if !strings.HasSuffix(fc.calls[0], " https://github.com/acme/w/pull/5") || !(strings.HasPrefix(fc.calls[0], "open ") || strings.HasPrefix(fc.calls[0], "xdg-open ")) {
		t.Errorf("open ran %q", fc.calls[0])
	}
	if fc.calls[1] != "pbcopy" || fc.stdin[1] != "a1b2" {
		t.Errorf("copy ran %q with stdin %q", fc.calls[1], fc.stdin[1])
	}
	want := "tmux display-popup -w 80% -h 70% -T  inbox a1b2  (r reply · d resolve · o link · c copy · q close)  -E /bin/lacquer console inbox popup --inbox '/tmp/my inbox.jsonl' a1b2"
	if fc.calls[2] != want {
		t.Errorf("popup ran\n%q\nwant\n%q", fc.calls[2], want)
	}
	// Outside tmux there is no popup, and it says so instead of failing quietly.
	fc = &fakeCmd{}
	ev := Env{Cmd: fc, InTmux: false}.Exec(Cmd{Kind: CmdPopup, ID: "x"}).(DoneEvent)
	if ev.OK || !strings.Contains(ev.Note, "not tmux") || len(fc.calls) != 0 {
		t.Errorf("ev %+v calls %v", ev, fc.calls)
	}
	// A failing open reports it.
	fc = &fakeCmd{fail: map[string]error{"open https://x": errors.New("boom"), "xdg-open https://x": errors.New("boom")}}
	if ev := (Env{Cmd: fc}).Exec(Cmd{Kind: CmdOpen, Text: "https://x"}).(DoneEvent); ev.OK || !strings.Contains(ev.Note, "boom") {
		t.Errorf("ev %+v", ev)
	}
}

func TestHarvestRecordsMergesThroughProducersAndReportsFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	merged := `[{"number":5,"title":"ship it","url":"https://github.com/acme/widgets/pull/5","mergedAt":"2999-01-01T00:00:00Z"}]`
	var ghErr error
	env := Env{
		InboxPath: path,
		Roster:    fleet.Roster{Project: []fleet.Entry{{Name: "widgets", Path: t.TempDir(), Repo: "acme/widgets"}}},
		Now:       func() time.Time { return t0 },
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			return []byte(merged), ghErr
		},
	}
	if !env.HasRepos() || (Env{}).HasRepos() || (Env{Roster: fleet.Roster{Project: []fleet.Entry{{Name: "x", Path: "/x"}}}}).HasRepos() {
		t.Fatal("HasRepos must be true only for a roster that names a repository")
	}
	// The first look only records where to start.
	if ev := env.Exec(Cmd{Kind: CmdHarvest}).(HarvestedEvent); ev.Added != 0 || len(ev.Unavailable) != 0 {
		t.Fatalf("first look: %+v", ev)
	}
	ev := env.Exec(Cmd{Kind: CmdHarvest}).(HarvestedEvent)
	if ev.Added != 1 || len(ev.Unavailable) != 0 {
		t.Fatalf("second look: %+v", ev)
	}
	open, _, _ := inbox.ListOpen(path)
	if len(open) != 1 || open[0].Title != "acme/widgets#5 merged: ship it" || open[0].Type != inbox.Unread {
		t.Errorf("inbox: %+v", open)
	}
	// A failing gh is reported, never read as "no merges".
	ghErr = errors.New("HTTP 502")
	ev = env.Exec(Cmd{Kind: CmdHarvest}).(HarvestedEvent)
	if len(ev.Unavailable) != 1 || !strings.Contains(ev.Unavailable[0], "HTTP 502") {
		t.Errorf("a failed harvest: %+v", ev)
	}
}

// -T is a tmux format on every version: an id from the inbox file holding
// #(cmd) would run cmd when the popup opened. (On tmux 3.7c a raw #(touch f) in
// -T created f; the doubled form did not.) The -E command is not escaped, because
// tmux versions disagree on whether they expand it, so nothing agent-written may
// be in it, and a # in what is left is refused.
func TestPopupTitleEscapesTmuxFormatsAndCommandCarriesNoHash(t *testing.T) {
	fc := &fakeCmd{}
	env := Env{Cmd: fc, InTmux: true, PopupArgv: func(id string) []string { return []string{"lacquer", "popup", "--id-hex=" + "78232874"} }}
	env.Exec(Cmd{Kind: CmdPopup, ID: "x#(touch pwned)#{pane_id}#[fg=red]\x1b[2J"})
	call := fc.calls[0]
	i := strings.Index(call, " -E ")
	title, cmd := call[:i], call[i:]
	if !strings.Contains(title, " inbox x##(touch pwned)##{pane_id}##[fg=red]^[[2J  (r reply") {
		t.Errorf("title not escaped: %q", title)
	}
	if left := strings.ReplaceAll(title, "##", ""); strings.Contains(left, "#") {
		t.Errorf("title has an unescaped #: %q", title)
	}
	if strings.Contains(cmd, "#") {
		t.Errorf("the command holds a #: %q", cmd)
	}

	// A # the operator put in a path is refused, with a note, and tmux is not run.
	fc = &fakeCmd{}
	env = Env{Cmd: fc, InTmux: true, PopupArgv: func(id string) []string { return []string{"lacquer", "--inbox", "/tmp/a#b"} }}
	ev := env.Exec(Cmd{Kind: CmdPopup, ID: "x"}).(DoneEvent)
	if ev.OK || !strings.Contains(ev.Note, "cannot open the popup") || len(fc.calls) != 0 {
		t.Errorf("ev %+v calls %v", ev, fc.calls)
	}
}

// The title is agent-written; the reply tag carries the id only.
func TestReplyTagCarriesTheIDNotTheTitle(t *testing.T) {
	fc := &fakeCmd{}
	env, _ := envFor(t, Overseer{Pane: "%7"}, fc)
	env.Exec(Cmd{Kind: CmdReply, ID: "a1\x1b[2J", Text: "yes"})
	if got, want := fc.calls[0], "tmux send-keys -t %7 -l [inbox a1^[[2J] yes"; got != want {
		t.Errorf("typed %q, want %q", got, want)
	}
}
