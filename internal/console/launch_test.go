package console

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/fleet"
)

// These tests run the real launch path -- real git, real tmux -- against a
// fake `claude` on PATH, because every defect they cover lived in how a
// process was started, not in how an argv string was assembled: a dry-run
// display of `tmux new-session -A` looked fine while every agent-run launch
// of it failed with "open terminal failed: not a terminal".

// requireTmux skips when tmux is absent, unless LACQUER_TEST_REQUIRE_TMUX is
// set, in which case it fails. CI sets it: a skipped tmux test there would
// read exactly like a passing one.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		if os.Getenv("LACQUER_TEST_REQUIRE_TMUX") != "" {
			t.Fatal("tmux is required (LACQUER_TEST_REQUIRE_TMUX is set) but not installed")
		}
		t.Skip("tmux not installed in this test environment")
	}
}

// isolatedTmux points tmux at a private server for the rest of the test, so
// nothing here can see or touch the operator's own sessions. The directory
// is short and under /tmp on purpose: tmux's socket path must fit in
// sun_path (104 bytes on macOS), which t.TempDir() under a long test name
// does not.
func isolatedTmux(t *testing.T) {
	t.Helper()
	requireTmux(t)
	dir, err := os.MkdirTemp("/tmp", "lqtmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(dir)
	})
}

// tmuxSessions lists the isolated server's session names.
func tmuxSessions(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil // no server running: no sessions
	}
	names := strings.Fields(string(out))
	sort.Strings(names)
	return names
}

func startTmuxSession(t *testing.T, name string) {
	t.Helper()
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "sleep", "300").CombinedOutput(); err != nil {
		t.Fatalf("could not start tmux session %q: %v\n%s", name, err, out)
	}
}

// claudeCall is one observed invocation of the fake claude.
type claudeCall struct {
	cwd  string
	args []string
}

// fakeClaude puts a `claude` on PATH that writes its cwd and argv to its own
// file under the returned directory, prints the bg daemon's confirmation
// line, and -- when not run with --bg -- stays alive, the way an interactive
// claude in a tmux pane does.
func fakeClaude(t *testing.T) (callsDir string) {
	t.Helper()
	bin := t.TempDir()
	callsDir = t.TempDir()
	script := `#!/bin/sh
f="` + callsDir + `/$$"
{ pwd -P; for a in "$@"; do printf '%s\0' "$a"; done; } > "$f.tmp" && mv "$f.tmp" "$f"
echo "backgrounded · ${FAKE_DAEMON_ID:-1234abcd}"
case " $* " in *" --bg "*) exit 0 ;; esac
sleep 300
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return callsDir
}

func claudeCalls(t *testing.T, callsDir string) []claudeCall {
	t.Helper()
	entries, err := os.ReadDir(callsDir)
	if err != nil {
		t.Fatal(err)
	}
	var calls []claudeCall
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(callsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		cwd, argv, _ := strings.Cut(string(data), "\n")
		calls = append(calls, claudeCall{cwd: cwd, args: strings.Split(strings.TrimSuffix(argv, "\x00"), "\x00")})
	}
	return calls
}

// waitForClaude polls until the fake claude has been called n times. A tmux
// pane starts its command asynchronously, after new-session has returned.
func waitForClaude(t *testing.T, callsDir string, n int) []claudeCall {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		calls := claudeCalls(t, callsDir)
		if len(calls) >= n {
			return calls
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake claude was called %d time(s), want %d", len(calls), n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// noTerminal replaces os.Stdin with /dev/null for the test: an agent's shell
// has no terminal, and that is the condition the tmux defect needed.
func noTerminal(t *testing.T) {
	t.Helper()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = devnull
	t.Cleanup(func() {
		os.Stdin = orig
		devnull.Close()
	})
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// dispatchCaller runs one dispatch through either public entry point, so
// every launch-path test covers both: they share runDispatch, and a fix that
// only reached one would leave the other broken.
type dispatchCaller struct {
	name string
	run  func(t *testing.T, name, dir, task string, mode Mode) (Launch, error)
}

var dispatchCallers = []dispatchCaller{
	{"project", func(t *testing.T, name, dir, task string, mode Mode) (Launch, error) {
		roster := fleet.Roster{Project: []fleet.Entry{{Name: name, Path: dir}}}
		return Dispatch(roster, nil, name, task, mode, false)
	}},
	{"role", func(t *testing.T, name, dir, task string, mode Mode) (Launch, error) {
		return DispatchRole(roleRosterOf(Role{Name: name, Mode: mode, Task: "declared", Dir: dir}), nil, name, task, false)
	}},
}

// D2: `tmux new-session -A` with no -d tried to attach the caller's terminal,
// so every dispatch run by an agent (which has none) failed with "open
// terminal failed: not a terminal". And it started plain `claude`, which
// comes up asking the operator for approvals that nobody is there to give.
func TestTmuxDispatchStartsDetachedWithBypassWithoutATerminal(t *testing.T) {
	for _, c := range dispatchCallers {
		t.Run(c.name, func(t *testing.T) {
			isolatedTmux(t)
			noTerminal(t)
			calls := fakeClaude(t)
			dir := t.TempDir()

			launch, err := c.run(t, "lead", dir, "the task", Tmux)
			if err != nil {
				t.Fatalf("dispatch failed with no terminal: %v\n%s", err, launch.Output)
			}
			if !contains(tmuxSessions(t), "lead") {
				t.Fatalf("no tmux session named lead; sessions: %v", tmuxSessions(t))
			}
			got := waitForClaude(t, calls, 1)[0]
			if got.cwd != realPath(t, dir) {
				t.Errorf("claude ran in %s, want %s", got.cwd, realPath(t, dir))
			}
			want := []string{"--dangerously-skip-permissions", "--settings", `{"sandbox":{"enabled":false}}`}
			if c.name == "project" {
				want = append(want, "--model", "sonnet")
			}
			want = append(want, "the task")
			if strings.Join(got.args, "\x00") != strings.Join(want, "\x00") {
				t.Errorf("claude argv = %q, want %q", got.args, want)
			}
			if launch.Record == nil {
				t.Fatal("a started session must come back with a record, or watch cannot see it")
			}
			if launch.Record.Mode != Tmux || launch.Record.Name != "lead" || launch.Record.LaunchError != "" {
				t.Errorf("record = %+v", *launch.Record)
			}
			if status, detail, err := launch.Record.Check(); status != Alive || err != nil {
				t.Errorf("Check() = %s %q %v, want alive", status, detail, err)
			}
		})
	}
}

// Re-dispatching a role whose session is already up must not start a second
// claude inside it -- the operator decided "already running" is a no-op, and
// nothing new is recorded because nothing new was started.
func TestTmuxDispatchLeavesALiveSessionAlone(t *testing.T) {
	for _, c := range dispatchCallers {
		t.Run(c.name, func(t *testing.T) {
			isolatedTmux(t)
			noTerminal(t)
			calls := fakeClaude(t)
			startTmuxSession(t, "lead")

			launch, err := c.run(t, "lead", t.TempDir(), "the task", Tmux)
			if err != nil {
				t.Fatalf("an already-running session is not an error: %v\n%s", err, launch.Output)
			}
			if !strings.Contains(launch.Output, "already running") {
				t.Errorf("the output must say the session was already running:\n%s", launch.Output)
			}
			if launch.Record != nil {
				t.Errorf("nothing was started, so nothing may be recorded: %+v", *launch.Record)
			}
			time.Sleep(500 * time.Millisecond)
			if n := len(claudeCalls(t, calls)); n != 0 {
				t.Errorf("a second claude was started %d time(s) in the live session", n)
			}
		})
	}
}

// tmux resolves `-t lead` by prefix when no session is named exactly that, so
// a running lead-two made a dispatch of `lead` look already running.
func TestTmuxDispatchMatchesTheSessionNameExactly(t *testing.T) {
	isolatedTmux(t)
	noTerminal(t)
	calls := fakeClaude(t)
	startTmuxSession(t, "lead-two")

	launch, err := dispatchCallers[1].run(t, "lead", t.TempDir(), "the task", Tmux)
	if err != nil {
		t.Fatalf("%v\n%s", err, launch.Output)
	}
	waitForClaude(t, calls, 1)
	if got := tmuxSessions(t); !contains(got, "lead") || !contains(got, "lead-two") {
		t.Errorf("sessions = %v, want both lead and lead-two", got)
	}
}

// A dotted project name cannot be addressed as a tmux target at all ("can't
// find pane: com"), so the session lacquer creates, records, checks and kills
// must be one tmux can address -- and the round trip must close.
func TestTmuxDispatchRoundTripsADottedName(t *testing.T) {
	isolatedTmux(t)
	noTerminal(t)
	fakeClaude(t)
	dir := t.TempDir()

	launch, err := dispatchCallers[0].run(t, "example.com", dir, "the task", Tmux)
	if err != nil {
		t.Fatalf("%v\n%s", err, launch.Output)
	}
	rec := launch.Record
	if rec == nil {
		t.Fatal("no record")
	}
	if rec.Name != "example.com" {
		t.Errorf("record name = %q; relaunch looks the project up by it, so it must stay the roster name", rec.Name)
	}
	if !contains(tmuxSessions(t), rec.TmuxSession) {
		t.Fatalf("recorded tmux session %q is not among %v", rec.TmuxSession, tmuxSessions(t))
	}
	// The operator attaches by typing the name the output gives them.
	if out, err := exec.Command("tmux", "has-session", "-t", rec.TmuxSession).CombinedOutput(); err != nil {
		t.Errorf("the recorded session %q cannot be addressed by name, so `tmux attach -t` it fails: %s", rec.TmuxSession, out)
	}
	if !strings.Contains(launch.Output, "tmux attach -t '"+rec.TmuxSession+"'") {
		t.Errorf("the output must say how to attach:\n%s", launch.Output)
	}
	if status, detail, err := rec.Check(); status != Alive || err != nil {
		t.Fatalf("Check() = %s %q %v, want alive", status, detail, err)
	}

	sessionsPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	if err := AppendRecord(sessionsPath, *rec); err != nil {
		t.Fatal(err)
	}
	if note, err := Kill(sessionsPath, *rec, true); err != nil {
		t.Fatalf("Kill: %v (%s)", err, note)
	}
	if got := tmuxSessions(t); len(got) != 0 {
		t.Errorf("kill left sessions behind: %v", got)
	}
	if status, _, _ := rec.Check(); status != Missing {
		t.Errorf("after kill, Check() = %s, want missing", status)
	}
}

// Records written before this change carry no TmuxSession and name the
// session by its raw name, which is what tmux 3.7 created from a dotted
// name. They must still resolve.
func TestTmuxRecordWithoutASessionFieldStillResolves(t *testing.T) {
	isolatedTmux(t)
	startTmuxSession(t, "example.com")

	r := Record{Kind: ProjectKind, Mode: Tmux, Name: "example.com", Task: "t", StartedAt: time.Now().UTC()}
	if status, detail, err := r.Check(); status != Alive || err != nil {
		t.Errorf("Check() = %s %q %v, want alive", status, detail, err)
	}
	// And kill must reach it: `kill-session -t example.com` cannot.
	sessionsPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	if err := AppendRecord(sessionsPath, r); err != nil {
		t.Fatal(err)
	}
	if note, err := Kill(sessionsPath, r, true); err != nil {
		t.Fatalf("Kill: %v (%s)", err, note)
	}
	if got := tmuxSessions(t); len(got) != 0 {
		t.Errorf("kill left the old record's session running: %v", got)
	}
}

func TestCheckTmuxDoesNotPrefixMatch(t *testing.T) {
	isolatedTmux(t)
	startTmuxSession(t, "probe-long")

	r := Record{Kind: RoleKind, Mode: Tmux, Name: "probe"}
	if status, detail, err := r.Check(); status != Missing || err != nil {
		t.Errorf("Check() = %s %q %v; a dead probe must not read as alive because probe-long is running", status, detail, err)
	}
}

// Verified against tmux 3.7c before this fix: `tmux kill-session -t probe`
// killed a session named probe-long.
func TestKillTmuxDoesNotKillAPrefixMatch(t *testing.T) {
	isolatedTmux(t)
	startTmuxSession(t, "probe-long")

	sessionsPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	r := Record{Kind: RoleKind, Mode: Tmux, Name: "probe", Task: "t", StartedAt: time.Now().UTC()}
	if err := AppendRecord(sessionsPath, r); err != nil {
		t.Fatal(err)
	}
	if _, err := Kill(sessionsPath, r, true); err != nil {
		t.Fatal(err)
	}
	if !contains(tmuxSessions(t), "probe-long") {
		t.Errorf("killing probe killed probe-long; sessions now: %v", tmuxSessions(t))
	}
}

// fakeTmux puts a `tmux` on PATH that reports no server and fails every
// new-session the way the pre-fix launch did with no terminal. Needs no real
// tmux, so this test runs everywhere.
func fakeTmux(t *testing.T) (callLog string) {
	t.Helper()
	bin := t.TempDir()
	callLog = filepath.Join(bin, "calls")
	script := `#!/bin/sh
echo "$1" >> "` + callLog + `"
case "$1" in
  new-session) echo "open terminal failed: not a terminal" >&2; exit 1 ;;
  *) echo "no server running on /tmp/tmux-0/default" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return callLog
}

// D2's other half: the failed launch left nothing in the sessions file, so
// watch could not see it and nobody else could either. A launch attempt that
// fails is recorded, carrying the failure, and watch reports it Failed.
func TestTmuxLaunchFailureIsRecordedAndReadsAsFailed(t *testing.T) {
	for _, c := range dispatchCallers {
		t.Run(c.name, func(t *testing.T) {
			fakeTmux(t)
			launch, err := c.run(t, "lead", t.TempDir(), "the task", Tmux)
			if err == nil {
				t.Fatal("expected the launch to fail")
			}
			if !strings.Contains(err.Error(), "not a terminal") {
				t.Errorf("the error must carry tmux's own message, got: %v", err)
			}
			if launch.Record == nil {
				t.Fatal("a failed launch attempt must still be recorded, or nobody can see it failed")
			}
			if !strings.Contains(launch.Record.LaunchError, "not a terminal") {
				t.Errorf("record LaunchError = %q", launch.Record.LaunchError)
			}
			status, detail, cerr := launch.Record.Check()
			if status != Failed || cerr != nil || !strings.Contains(detail, "not a terminal") {
				t.Errorf("Check() = %s %q %v, want failed naming the launch error", status, detail, cerr)
			}
		})
	}
}

// Refusals before any launch attempt are input errors returned to the caller
// synchronously; there is no session to watch and a relaunch could never
// succeed, so nothing is recorded.
func TestRefusalsBeforeLaunchAreNotRecorded(t *testing.T) {
	cases := map[string]func() (Launch, error){
		"unknown project": func() (Launch, error) {
			return Dispatch(rosterOf("alpha"), nil, "nope", "t", Tmux, false)
		},
		"empty task": func() (Launch, error) { return Dispatch(rosterOf("alpha"), nil, "alpha", " ", Tmux, false) },
		"unknown mode": func() (Launch, error) {
			return Dispatch(rosterOf("alpha"), nil, "alpha", "t", Mode("x"), false)
		},
		"unknown role": func() (Launch, error) {
			return DispatchRole(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "t", Dir: "/w"}), nil, "pm", "", false)
		},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			launch, err := f()
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if launch.Record != nil {
				t.Errorf("a refusal must not be recorded: %+v", *launch.Record)
			}
		})
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// D3: bg dispatch ran `(cd <project> && claude --bg ...)` and never made a
// worktree, so the session edited the operator's real checkout while the
// help text promised isolation.
func TestBackgroundDispatchRunsInADedicatedWorktree(t *testing.T) {
	for _, c := range dispatchCallers {
		t.Run(c.name, func(t *testing.T) {
			calls := fakeClaude(t)
			repo := realPath(t, t.TempDir())
			initGitRepo(t, repo)
			branchBefore := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")

			launch, err := c.run(t, "alpha", repo, "the task", Background)
			if err != nil {
				t.Fatalf("%v\n%s", err, launch.Output)
			}
			rec := launch.Record
			if rec == nil {
				t.Fatal("no record")
			}
			if !strings.HasPrefix(rec.Worktree, filepath.Join(repo, ".claude", "worktrees")+string(filepath.Separator)) {
				t.Fatalf("record worktree = %q, want a directory under %s/.claude/worktrees/", rec.Worktree, repo)
			}
			got := claudeCalls(t, calls)
			if len(got) != 1 {
				t.Fatalf("claude called %d times, want 1", len(got))
			}
			if got[0].cwd != realPath(t, rec.Worktree) {
				t.Errorf("claude ran in %s, want its worktree %s", got[0].cwd, rec.Worktree)
			}
			want := []string{"--bg", "--dangerously-skip-permissions", "--settings", `{"sandbox":{"enabled":false}}`}
			if c.name == "project" {
				want = append(want, "--model", "sonnet")
			}
			want = append(want, "the task")
			if strings.Join(got[0].args, "\x00") != strings.Join(want, "\x00") {
				t.Errorf("claude argv = %q, want %q", got[0].args, want)
			}
			if rec.Branch == "" || git(t, rec.Worktree, "rev-parse", "--abbrev-ref", "HEAD") != rec.Branch {
				t.Errorf("worktree is not on the recorded branch %q", rec.Branch)
			}
			if rec.Dir != repo {
				t.Errorf("record dir = %q, want the project checkout %q", rec.Dir, repo)
			}
			if rec.DaemonID != "1234abcd" {
				t.Errorf("record daemon id = %q", rec.DaemonID)
			}
			if b := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); b != branchBefore {
				t.Errorf("the checkout moved from %s to %s", branchBefore, b)
			}
			// The nested worktree must not show up as untracked in the
			// checkout, where a `git add -A` would commit it as a gitlink.
			if st := git(t, repo, "status", "--porcelain"); st != "" {
				t.Errorf("the checkout is not clean after dispatch:\n%s", st)
			}
		})
	}
}

// If the worktree cannot be made, nothing is launched -- never a silent
// fallback to the real checkout.
func TestBackgroundDispatchLaunchesNothingWithoutAWorktree(t *testing.T) {
	for _, c := range dispatchCallers {
		t.Run(c.name, func(t *testing.T) {
			calls := fakeClaude(t)
			notARepo := t.TempDir()

			launch, err := c.run(t, "alpha", notARepo, "the task", Background)
			if err == nil {
				t.Fatalf("expected a refusal to dispatch outside a git repository\n%s", launch.Output)
			}
			if n := len(claudeCalls(t, calls)); n != 0 {
				t.Fatalf("claude was launched %d time(s) without a worktree", n)
			}
			if launch.Record == nil || launch.Record.LaunchError == "" {
				t.Errorf("the failed attempt must be recorded with its error, got %+v", launch.Record)
			}
			if _, err := os.Stat(filepath.Join(notARepo, ".claude")); !os.IsNotExist(err) {
				t.Errorf("a failed dispatch left %s/.claude behind", notARepo)
			}
		})
	}
}

func TestConcurrentBackgroundDispatchesDoNotCollide(t *testing.T) {
	fakeClaude(t)
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)

	const n = 4
	type result struct {
		launch Launch
		err    error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			l, err := dispatchCallers[0].run(t, "alpha", repo, "the task", Background)
			results <- result{l, err}
		}()
	}
	worktrees := map[string]bool{}
	branches := map[string]bool{}
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("%v\n%s", r.err, r.launch.Output)
		}
		worktrees[r.launch.Record.Worktree] = true
		branches[r.launch.Record.Branch] = true
	}
	if len(worktrees) != n || len(branches) != n {
		t.Errorf("%d dispatches produced %d worktrees and %d branches", n, len(worktrees), len(branches))
	}
}

// The operator's own agents keep worktrees under .claude/worktrees/ too.
// Dispatch must never reuse or disturb one it did not create.
func TestBackgroundDispatchLeavesExistingWorktreesAlone(t *testing.T) {
	fakeClaude(t)
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	theirs := filepath.Join(repo, ".claude", "worktrees", "operator-work")
	git(t, repo, "worktree", "add", "-q", "-b", "operator-work", theirs)
	if err := os.WriteFile(filepath.Join(theirs, "f.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	launch, err := dispatchCallers[0].run(t, "alpha", repo, "the task", Background)
	if err != nil {
		t.Fatalf("%v\n%s", err, launch.Output)
	}
	if launch.Record.Worktree == theirs {
		t.Fatal("dispatch reused the operator's worktree")
	}
	if st := git(t, theirs, "status", "--porcelain"); st != "M f.txt" {
		t.Errorf("the operator's worktree changed; status:\n%s", st)
	}
}

// A dry run describes the worktree it would make and makes nothing.
func TestBackgroundDryRunCreatesNothing(t *testing.T) {
	calls := fakeClaude(t)
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)

	roster := fleet.Roster{Project: []fleet.Entry{{Name: "alpha", Path: repo}}}
	launch, err := Dispatch(roster, nil, "alpha", "the task", Background, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(launch.Output, filepath.Join(repo, ".claude", "worktrees")) {
		t.Errorf("a dry run must say where the worktree would go:\n%s", launch.Output)
	}
	if launch.Record != nil {
		t.Error("a dry run must not produce a record")
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude")); !os.IsNotExist(err) {
		t.Error("a dry run created .claude/")
	}
	if n := len(claudeCalls(t, calls)); n != 0 {
		t.Errorf("a dry run launched claude %d time(s)", n)
	}
}

// `a.b` and `a_b` map to the same tmux session name, so dispatching one
// would find (or kill) the other's session. Refused, in both entry points,
// before anything is started -- dry run included.
func TestTmuxDispatchRefusesANameThatCollidesAfterMapping(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "a.b", Path: repo}, {Name: "a_b", Path: "/w/a_b"}}}
	_, err := Dispatch(roster, nil, "a.b", "t", Tmux, true)
	if err == nil || !strings.Contains(err.Error(), "a_b") {
		t.Errorf("expected a refusal naming the colliding project, got %v", err)
	}
	roles := roleRosterOf(Role{Name: "x:y", Mode: Tmux, Task: "t", Dir: "/w"}, Role{Name: "x_y", Mode: Tmux, Task: "t", Dir: "/w"})
	_, err = DispatchRole(roles, nil, "x_y", "", true)
	if err == nil || !strings.Contains(err.Error(), "x:y") {
		t.Errorf("expected a refusal naming the colliding role, got %v", err)
	}
	// bg sessions have no tmux name, so the same roster dispatches fine there.
	if _, err := Dispatch(roster, nil, "a.b", "t", Background, true); err != nil {
		t.Errorf("bg mode has no tmux name to collide on: %v", err)
	}
}

// bgDispatchOnce makes a real bg dispatch into a fresh repo and returns the
// roster and record, for tests of what happens to that session afterwards.
func bgDispatchOnce(t *testing.T) (fleet.Roster, Record, string) {
	t.Helper()
	calls := fakeClaude(t)
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "alpha", Path: repo}}}
	launch, err := Dispatch(roster, nil, "alpha", "the task", Background, false)
	if err != nil {
		t.Fatalf("%v\n%s", err, launch.Output)
	}
	// Without this, every test built on it passes vacuously against a
	// dispatch that made no worktree: filepath.Join("", x) is just x.
	if launch.Record == nil || launch.Record.Worktree == "" {
		t.Fatalf("the dispatch made no worktree: %+v", launch.Record)
	}
	return roster, *launch.Record, calls
}

// A relaunched bg session resumes in the worktree its record names, where its
// own commits and uncommitted work are -- not in a fresh one from the base.
func TestRelaunchResumesInTheRecordedWorktree(t *testing.T) {
	roster, rec, calls := bgDispatchOnce(t)
	if err := os.WriteFile(filepath.Join(rec.Worktree, "wip.txt"), []byte("half done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outLaunch, err := Relaunch(rec, roster, RoleRoster{}, nil, false)
	out := outLaunch.Output
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := claudeCalls(t, calls)
	if len(got) != 2 {
		t.Fatalf("claude called %d times, want 2", len(got))
	}
	for _, c := range got {
		if c.cwd == realPath(t, rec.Worktree) && strings.Contains(c.args[len(c.args)-1], "wip.txt") {
			return
		}
	}
	t.Errorf("the relaunch must run in %s and hand over its uncommitted work; calls: %+v", rec.Worktree, got)
}

// A directory that is no longer a registered worktree is not reused: that
// would run the session in whatever that directory now is. A fresh worktree
// is made instead, and the output says so.
func TestRelaunchMakesAFreshWorktreeWhenTheRecordedOneIsGone(t *testing.T) {
	roster, rec, calls := bgDispatchOnce(t)
	git(t, rec.Dir, "worktree", "remove", "--force", rec.Worktree)
	if err := os.MkdirAll(rec.Worktree, 0o755); err != nil { // same path, no longer a worktree
		t.Fatal(err)
	}

	outLaunch, err := Relaunch(rec, roster, RoleRoster{}, nil, false)
	out := outLaunch.Output
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "no longer a registered worktree") {
		t.Errorf("the output must say the recorded worktree was not reused:\n%s", out)
	}
	got := claudeCalls(t, calls)
	if len(got) != 2 {
		t.Fatalf("claude called %d times, want 2", len(got))
	}
	var relaunched *claudeCall
	for i := range got {
		if got[i].args[len(got[i].args)-1] != "the task" {
			relaunched = &got[i]
		}
	}
	if relaunched == nil {
		t.Fatalf("no relaunch call among %+v", got)
	}
	if relaunched.cwd == realPath(t, rec.Worktree) {
		t.Errorf("the relaunch ran in the unregistered directory %s", relaunched.cwd)
	}
	if !strings.HasPrefix(relaunched.cwd, filepath.Join(rec.Dir, ".claude", "worktrees")+string(filepath.Separator)) {
		t.Errorf("the relaunch ran in %s, want a fresh worktree under %s/.claude/worktrees/ -- never the checkout", relaunched.cwd, rec.Dir)
	}
}

// Kill used to force-remove whatever worktreePath Claude's own state file
// named. With the session now running inside lacquer's worktree, that path
// can be the recorded worktree itself (or inside it) -- and force-removing
// it deletes the uncommitted work Kill is not allowed to touch.
func TestKillNeverRemovesTheRecordedWorktree(t *testing.T) {
	for _, sub := range []string{"", "nested"} {
		t.Run("state points at worktree/"+sub, func(t *testing.T) {
			_, rec, _ := bgDispatchOnce(t)
			if err := os.WriteFile(filepath.Join(rec.Worktree, "wip.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			jobDir := filepath.Join(home, ".claude", "jobs", rec.DaemonID)
			if err := os.MkdirAll(jobDir, 0o755); err != nil {
				t.Fatal(err)
			}
			state := `{"state":"failed","worktreePath":"` + filepath.Join(rec.Worktree, sub) + `"}`
			if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(state), 0o644); err != nil {
				t.Fatal(err)
			}
			sessionsPath := filepath.Join(t.TempDir(), "sessions.jsonl")
			if err := AppendRecord(sessionsPath, rec); err != nil {
				t.Fatal(err)
			}

			note, err := Kill(sessionsPath, rec, false)
			if err != nil {
				t.Fatalf("Kill: %v (%s)", err, note)
			}
			if _, err := os.Stat(filepath.Join(rec.Worktree, "wip.txt")); err != nil {
				t.Fatalf("Kill deleted the recorded worktree's uncommitted work: %v", err)
			}
			if !strings.Contains(note, rec.Worktree) {
				t.Errorf("Kill must say where the kept worktree is, got note %q", note)
			}
		})
	}
}

// Defence in depth for the 1.37.3 regression: the loaders now resolve paths to
// absolute, but a Roster or Role built any other way can still carry a
// relative dir. A bg dispatch given one must still make its worktree (not fail
// taking filepath.Rel of git's absolute toplevel against it) and record
// absolute paths, which `watch --relaunch` later uses from another cwd.
func TestBackgroundDispatchWithARelativeDir(t *testing.T) {
	for _, c := range dispatchCallers {
		t.Run(c.name, func(t *testing.T) {
			calls := fakeClaude(t)
			parent := realPath(t, t.TempDir())
			repo := filepath.Join(parent, "proj")
			fleetOps := filepath.Join(parent, "fleet-ops")
			for _, d := range []string{repo, fleetOps} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			initGitRepo(t, repo)
			orig, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(fleetOps); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(orig) })

			launch, err := c.run(t, "alpha", filepath.Join("..", "proj"), "the task", Background)
			if err != nil {
				t.Fatalf("%v\n%s", err, launch.Output)
			}
			rec := launch.Record
			if rec == nil {
				t.Fatal("no record")
			}
			if rec.Dir != repo {
				t.Errorf("record dir = %q, want the absolute checkout %q", rec.Dir, repo)
			}
			if !strings.HasPrefix(rec.Worktree, filepath.Join(repo, ".claude", "worktrees")+string(filepath.Separator)) {
				t.Errorf("record worktree = %q, want an absolute directory under %s/.claude/worktrees/", rec.Worktree, repo)
			}
			got := claudeCalls(t, calls)
			if len(got) != 1 {
				t.Fatalf("claude called %d times, want 1", len(got))
			}
			if got[0].cwd != realPath(t, rec.Worktree) {
				t.Errorf("claude ran in %s, want its worktree %s", got[0].cwd, rec.Worktree)
			}
		})
	}
}

// The other path comparisons in this package resolve a relative input
// against the cwd before comparing it with an absolute one, rather than
// silently answering "no": a session's cwd, a recorded worktree and git's
// worktree list are all absolute.
func TestPathComparisonsResolveRelativeInputs(t *testing.T) {
	parent := realPath(t, t.TempDir())
	repo := filepath.Join(parent, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	if !under(filepath.Join(repo, ".claude", "worktrees", "x"), "proj") {
		t.Error("under: a session in the project's worktree does not belong to the project named relatively")
	}
	if !within(filepath.Join("proj", "sub"), repo) {
		t.Error("within: proj/sub is not within the project")
	}
	if !registeredWorktree(repo, "proj") {
		t.Error("registeredWorktree: the checkout itself, named relatively, is not a registered worktree")
	}
}

// A tmux session given an assigned worktree (Placement.Worktree) really
// starts there, not in the checkout, and is recorded with it so a relaunch
// returns to it.
func TestTmuxDispatchStartsInTheAssignedWorktree(t *testing.T) {
	for _, c := range []struct {
		name string
		run  func(repo, wt string) (Launch, error)
	}{
		{"project", func(repo, wt string) (Launch, error) {
			return DispatchPlaced(fleet.Roster{Project: []fleet.Entry{{Name: "lead", Path: repo}}}, nil, "lead", "the task", Tmux, false, Placement{Worktree: wt})
		}},
		{"role", func(repo, wt string) (Launch, error) {
			return DispatchRolePlaced(roleRosterOf(Role{Name: "lead", Mode: Tmux, Task: "declared", Dir: repo}), nil, "lead", "the task", false, Placement{Worktree: wt})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolatedTmux(t)
			noTerminal(t)
			calls := fakeClaude(t)
			repo := realPath(t, t.TempDir())
			initGitRepo(t, repo)
			wt := filepath.Join(realPath(t, t.TempDir()), "unit")
			git(t, repo, "worktree", "add", "-q", "-b", "pm/unit", wt)

			launch, err := c.run(repo, wt)
			if err != nil {
				t.Fatalf("%v\n%s", err, launch.Output)
			}
			got := waitForClaude(t, calls, 1)[0]
			if got.cwd != wt {
				t.Errorf("claude ran in %s, want the assigned worktree %s", got.cwd, wt)
			}
			if launch.Record == nil || launch.Record.Worktree != wt || launch.Record.Branch != "pm/unit" {
				t.Errorf("record = %+v, want worktree %s on pm/unit", launch.Record, wt)
			}
		})
	}
}

// A tmux relaunch returns to its recorded assigned worktree while it is still
// registered, and is refused once it is not -- never quietly moved into the
// checkout, which tmux would then edit.
func TestTmuxRelaunchReturnsToTheAssignedWorktree(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	wt := filepath.Join(realPath(t, t.TempDir()), "unit")
	git(t, repo, "worktree", "add", "-q", "-b", "pm/unit", wt)
	roster := fleet.Roster{Project: []fleet.Entry{{Name: "alpha", Path: repo}}}
	rec := Record{Kind: ProjectKind, Name: "alpha", Mode: Tmux, Dir: repo, Task: "t", Worktree: wt, Branch: "pm/unit", LaunchError: "boom"}

	launch, err := Relaunch(rec, roster, RoleRoster{}, nil, true)
	if err != nil {
		t.Fatalf("%v\n%s", err, launch.Output)
	}
	if !strings.Contains(launch.Output, "-c "+wt+" claude") {
		t.Errorf("the relaunch does not start in %s:\n%s", wt, launch.Output)
	}

	git(t, repo, "worktree", "remove", wt)
	launch, err = Relaunch(rec, roster, RoleRoster{}, nil, true)
	if err == nil || !strings.Contains(err.Error(), "refusing --worktree "+wt) {
		t.Errorf("a relaunch into a worktree no longer registered must be refused, got %v\n%s", err, launch.Output)
	}
	if strings.Contains(launch.Output, "-c "+repo) {
		t.Errorf("the relaunch fell back to the checkout:\n%s", launch.Output)
	}
}
