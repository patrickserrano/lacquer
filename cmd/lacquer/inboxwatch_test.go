package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"flag"
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
	oldTerm, oldTTY, oldWait := systemTerm, stdinIsTerminal, waitForKey
	t.Cleanup(func() { systemTerm, stdinIsTerminal, waitForKey = oldTerm, oldTTY, oldWait })
	waitForKey = func() {}
	stdinIsTerminal = func() bool { return true }
	systemTerm = func() inboxwatch.Term {
		return inboxwatch.Term{
			In: &untilReader{out: out, want: wantOnScreen, keys: keys}, Out: out,
			MakeRaw:   func() (func() error, error) { return func() error { return nil }, nil },
			Size:      func() (int, int, error) { return 120, 12, nil },
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
	want := "/opt/bin/lacquer console inbox popup --inbox /state/inbox.jsonl --overseer-pane=%3 --overseer-title= --overseer-session==ops --id-hex=61316232"
	if got != want {
		t.Errorf("popup argv\n%s\nwant\n%s", got, want)
	}
	if env.Resolve == nil || env.InTmux {
		t.Errorf("env = %+v", env)
	}
	// With reply off in the list, the popup is told so explicitly: it would
	// otherwise read $LACQUER_OVERSEER_* from the tmux server's environment.
	// An id written by an agent reaches tmux hex-encoded: nothing in it can be a tmux format.
	hostile := strings.Join(env.PopupArgv("x#(touch pwned)#{pane_id}\x1b[2J'; rm -rf ~; '"), " ")
	if strings.Contains(hostile, "#") || strings.Contains(hostile, "pwned") || strings.Contains(hostile, "'") {
		t.Errorf("agent text reached the popup command: %s", hostile)
	}
	off := newWatchEnv("/state/inbox.jsonl", false, overseerFlags{}, fleetRoster(), envMap(nil))
	if got := strings.Join(off.PopupArgv("a1b2"), " "); got != "/opt/bin/lacquer console inbox popup --inbox /state/inbox.jsonl --overseer-pane= --overseer-title= --overseer-session= --id-hex=61316232" {
		t.Errorf("popup argv with no overseer:\n%s", got)
	}
}

// End to end: the tmux server's environment names a pane, the list has reply
// off, and the popup it opens must not pick the environment up.
func TestPopupDoesNotTurnReplyOnFromTheEnvironment(t *testing.T) {
	path := writeFixtureInbox(t)
	old := executablePath
	defer func() { executablePath = old }()
	executablePath = func() (string, error) { return "lacquer", nil }
	argv := newWatchEnv(path, false, overseerFlags{}, fleetRoster(), envMap(nil)).PopupArgv("fx01") // id passed hex-encoded

	out := fakeTerminal(t, "fixture decision needed", "q")
	var stdout, stderr bytes.Buffer
	env := rawEnv(map[string]string{"XDG_STATE_HOME": t.TempDir(), "LACQUER_OVERSEER_PANE": "%9"})
	if code := run(append([]string{"console"}, argv[2:]...), env, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	if got := plainOut(out.String()); !strings.Contains(got, "r reply (off: no overseer pane)") {
		t.Errorf("the popup turned reply on from $LACQUER_OVERSEER_PANE:\n%s", got)
	}
}

// tmux closes a popup the moment its command exits, so an error must wait for a
// key or it is never seen.
func TestPopupErrorWaitsForAKeyOnATerminal(t *testing.T) {
	waits := 0
	old, oldTTY := waitForKey, stdinIsTerminal
	defer func() { waitForKey, stdinIsTerminal = old, oldTTY }()
	waitForKey = func() { waits++ }
	var stdout, stderr bytes.Buffer
	env := rawEnv(map[string]string{"XDG_STATE_HOME": t.TempDir()})

	stdinIsTerminal = func() bool { return true }
	if code := run([]string{"console", "inbox", "popup"}, env, &stdout, &stderr); code == 0 || waits != 1 || !strings.Contains(stderr.String(), "usage") || !strings.Contains(stderr.String(), "press any key") {
		t.Errorf("no id on a terminal: code %d waits %d stderr %q", code, waits, stderr.String())
	}
	stdinIsTerminal = func() bool { return false }
	waits = 0
	if code := run([]string{"console", "inbox", "popup"}, env, &stdout, &stderr); code == 0 || waits != 0 {
		t.Errorf("off a terminal there is no one to wait for: code %d waits %d", code, waits)
	}
}

var ansiOut = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func plainOut(s string) string { return ansiOut.ReplaceAllString(s, "") }

func fleetRoster() fleet.Roster { return fleet.Roster{} }

// ---- 409b: the tabs, the issue popup, --show ----

// stepReader is a terminal's input that sends each step's keys once the screen
// shows what that step waits for, so a test can move through the tabs and see
// each one before the next key.
type stepReader struct {
	out   *lockedBuf
	steps []struct{ want, keys string }
}

func (r *stepReader) Read(p []byte) (int, error) {
	if len(r.steps) == 0 {
		return 0, io.EOF
	}
	s := r.steps[0]
	r.steps = r.steps[1:]
	for i := 0; i < 500 && !strings.Contains(plainOut(r.out.String()), s.want); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return copy(p, s.keys), nil
}

// End to end through the real loop and the real Env, with only gh faked: every
// tab is reached by its key, asks GitHub once, and shows what came back.
func TestWatchCyclesThroughAllFourTabs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	now := time.Now().UTC()
	if _, err := inbox.Add(path, inbox.Entry{ID: "open1", Type: inbox.Action, Title: "still waiting on you"}); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Add(path, inbox.Entry{ID: "gone1", Type: inbox.Unread, Title: "an entry already closed", CreatedAt: now.Add(-3 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Resolve(path, "gone1"); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var ghCalls []string
	fakeGH := func(_ context.Context, args ...string) ([]byte, error) {
		mu.Lock()
		ghCalls = append(ghCalls, strings.Join(args, " "))
		mu.Unlock()
		switch {
		case args[0] == "search":
			return []byte(`[{"repository":{"nameWithOwner":"Acme/Widgets"},"number":12,"title":"parked idea about widgets","createdAt":"` + now.Add(-50*time.Hour).Format(time.RFC3339) + `","url":"https://github.com/Acme/Widgets/issues/12"}]`), nil
		case args[0] == "pr" && args[3] != "Acme/Widgets":
			return []byte("[]"), nil
		case args[0] == "pr":
			return []byte(`[{"number":41,"title":"open pull request title","author":{"login":"app/dependabot"},"isDraft":false,"createdAt":"` + now.Add(-30*time.Hour).Format(time.RFC3339) + `","url":"https://github.com/Acme/Widgets/pull/41","mergeStateStatus":"BLOCKED","statusCheckRollup":[]}]`), nil
		}
		return nil, errors.New("unexpected gh call " + strings.Join(args, " "))
	}
	out := &lockedBuf{}
	term := inboxwatch.Term{
		In: &stepReader{out: out, steps: []struct{ want, keys string }{
			{"still waiting on you", "2"},
			{"parked idea about widgets", "3"},
			{"an entry already closed", "4"},
			{"open pull request title", "1q"},
		}},
		Out:       out,
		MakeRaw:   func() (func() error, error) { return func() error { return nil }, nil },
		Size:      func() (int, int, error) { return 110, 12, nil },
		TickEvery: 10 * time.Millisecond, EscWait: 10 * time.Millisecond, Now: time.Now,
	}
	env := newWatchEnv(path, false, overseerFlags{}, fleet.Roster{Project: []fleet.Entry{{Name: "widgets", Repo: "Acme/Widgets"}}}, envMap(nil))
	env.Run = fakeGH
	env.ExtraRepos = []string{"patrickserrano/lacquer"}
	var stderr bytes.Buffer
	if code := runInboxWatch(term, env, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	screen := plainOut(out.String())
	for _, want := range []string{
		"1 Inbox", "2 Later", "3 Done", "4 PRs",
		"Widgets  (1)", "#12", "parked idea about widgets", "1 parked · 1 projects",
		"an entry already closed", "1 closed",
		"#41", "dependabot", "BLOCKED", "open pull request title", "1 open · 1 over 24h",
		"⏎ issue", "⏎/o open on GitHub", // the whole hint rows are checked in the model tests
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("no frame shows %q", want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	var search, prs int
	for _, c := range ghCalls {
		switch {
		case strings.HasPrefix(c, "search issues --label later"):
			search++
			if !strings.HasSuffix(c, "--owner Acme --owner patrickserrano") {
				t.Errorf("owners: %s", c)
			}
		case strings.HasPrefix(c, "pr list -R"):
			prs++
		}
	}
	if search != 1 || prs != 2 {
		t.Errorf("gh ran %d searches and %d PR lists (one per repository), want 1 and 2: %q", search, prs, ghCalls)
	}
}

// What the list runs in a popup for a Later row carries the issue hex-encoded.
func TestIssuePopupCommandCarriesTheRefHexEncoded(t *testing.T) {
	old := executablePath
	defer func() { executablePath = old }()
	executablePath = func() (string, error) { return "/opt/bin/lacquer", nil }
	env := newWatchEnv("/state/inbox.jsonl", false, overseerFlags{pane: "%3"}, fleetRoster(), envMap(nil))
	got := strings.Join(env.IssueArgv("Acme/Widgets#12"), " ")
	want := "/opt/bin/lacquer console inbox popup --inbox /state/inbox.jsonl --overseer-pane=%3 --overseer-title= --overseer-session= --issue-hex=" + hex.EncodeToString([]byte("Acme/Widgets#12"))
	if got != want {
		t.Errorf("issue argv\n%s\nwant\n%s", got, want)
	}
	hostile := strings.Join(env.IssueArgv("o/r#(touch pwned)#{pane_id}'; rm -rf ~; '#1"), " ")
	if strings.Contains(strings.ReplaceAll(hostile, "--overseer-pane=%3", ""), "#") || strings.Contains(hostile, "pwned") || strings.Contains(hostile, "'") {
		t.Errorf("agent text reached the popup command: %s", hostile)
	}
}

func TestIssuePopupRefusesBadHexAndBothIdentifiers(t *testing.T) {
	old, oldTTY := stdinIsTerminal, waitForKey
	defer func() { stdinIsTerminal, waitForKey = old, oldTTY }()
	stdinIsTerminal = func() bool { return false }
	for name, args := range map[string][]string{
		"not hex":   {"--inbox", "/tmp/x.jsonl", "--issue-hex=zz"},
		"both":      {"--inbox", "/tmp/x.jsonl", "--issue-hex=" + hex.EncodeToString([]byte("o/r#1")), "--id-hex=6161"},
		"an id too": {"--inbox", "/tmp/x.jsonl", "--issue-hex=" + hex.EncodeToString([]byte("o/r#1")), "a1"},
	} {
		var stderr bytes.Buffer
		if code := popupMain(args, envMap(nil), &stderr); code == 0 || !strings.Contains(stderr.String(), "--issue-hex") {
			t.Errorf("%s: code %d, stderr %q", name, code, stderr.String())
		}
	}
}

func TestInboxWatchShowPrintsOneEntryWithoutATerminalOrRoster(t *testing.T) {
	lq := realLacquer(t)
	path := filepath.Join(t.TempDir(), "inbox.jsonl")
	if _, err := inbox.Add(path, inbox.Entry{ID: "fx01", Type: inbox.Action, Title: "fixture decision needed", Project: "lacquer", Body: "line one\nline\x1b[2Jtwo",
		CreatedAt: time.Date(2026, 9, 24, 12, 30, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	stdinIsTerminal = func() bool { return false }
	env := envMap(map[string]string{"LACQUER_ROOT": lq, "XDG_STATE_HOME": t.TempDir()})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"console", "--inbox", path, "inbox", "watch", "--show", "fx01"}, env, &stdout, &stderr); code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	want := "ACTION  fx01\nfixture decision needed\n\n  project: lacquer\ncreatedAt: 2026-09-24T12:30:00Z\n\nline one\nline^[[2Jtwo\n"
	if stdout.String() != want {
		t.Errorf("--show printed\n%q\nwant\n%q", stdout.String(), want)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"console", "--inbox", path, "inbox", "watch", "--show", "nope"}, env, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "no inbox entry nope") {
		t.Errorf("a missing id: code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	// --show belongs to inbox watch and nothing else.
	stderr.Reset()
	if code := run([]string{"console", "--inbox", path, "--show", "fx01", "inbox", "list"}, env, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "--show applies only to inbox watch") {
		t.Errorf("--show on inbox list: code %d, stderr %q", code, stderr.String())
	}
}

func TestExtraRepoFlagAndEnvironment(t *testing.T) {
	var f extraRepoFlags
	for _, ok := range []string{"a/b", "a/b, c/d", ""} {
		if err := f.Set(ok); err != nil {
			t.Errorf("Set(%q) = %v", ok, err)
		}
	}
	if got := f.String(); got != "a/b,a/b,c/d" {
		t.Errorf("list = %q", got)
	}
	for _, bad := range []string{"nobody", "/x", "a/", "a/b/c", "--repo"} {
		if err := (&extraRepoFlags{}).Set(bad); err == nil {
			t.Errorf("Set(%q) accepted it", bad)
		}
	}
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	e := addExtraRepoFlag(fs, envMap(map[string]string{"LACQUER_EXTRA_REPOS": "o/one,o/two"}))
	if err := fs.Parse([]string{"--extra-repo", "o/three"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(e.list, " ") != "o/one o/two o/three" || e.envErr != nil {
		t.Errorf("list %v, envErr %v", e.list, e.envErr)
	}
	if bad := addExtraRepoFlag(flag.NewFlagSet("y", flag.ContinueOnError), envMap(map[string]string{"LACQUER_EXTRA_REPOS": "oops"})); bad.envErr == nil {
		t.Error("a malformed $LACQUER_EXTRA_REPOS was ignored")
	}
}
