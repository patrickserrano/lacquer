package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/patrickserrano/lacquer/internal/console"
)

// Go's flag package stops at the first argument that is not a flag, and every
// console subcommand is one. So until this was fixed, a flag written after the
// subcommand was never parsed: `watch --relaunch` printed the status and
// exited 0 without relaunching anything -- the form fleet-ops' README
// documents -- and `kill <name> --force` refused an Alive session as if
// --force had not been passed. Nothing reported either as an error.
//
// Every test here goes through run(), with the fake claude and tmux from
// newPlaceFleet and a temp HOME, so nothing real is launched or killed.

// worktreeIDs matches the random id of a worktree bg plans for itself, the
// one part of a dry run that differs between two otherwise identical runs.
var worktreeIDs = regexp.MustCompile(`dispatch([-/])\d{8}-\d{6}-[0-9a-f]{8}`)

// sameOutputInEveryOrder runs each argument list, which must differ only in
// where their flags sit, and requires them all to exit 0 with one output.
func sameOutputInEveryOrder(t *testing.T, lq string, orders [][]string) string {
	t.Helper()
	var first string
	for i, args := range orders {
		out, errb, code := runConsole(t, lq, args)
		if code != 0 {
			t.Fatalf("%q exited %d\nstdout:\n%s\nstderr:\n%s", args, code, out, errb)
		}
		out = worktreeIDs.ReplaceAllString(out, "dispatch${1}<id>")
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Errorf("%q printed something different from %q:\n--- got\n%s\n--- want\n%s", args, orders[0], out, first)
		}
	}
	return first
}

// failedRecord is a session whose launch failed, the one kind of record
// `watch --relaunch` relaunches.
func (f placeFleet) failedRecord(t *testing.T) console.Record {
	t.Helper()
	r := console.Record{
		Kind: console.ProjectKind, Name: "proj", Mode: console.Background, Dir: f.proj,
		Task: "do the unit", LaunchError: "claude: failed to start", FailedLaunches: 1,
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := console.AppendRecord(f.sessions, r); err != nil {
		t.Fatal(err)
	}
	return r
}

// aliveRecord is a bg session whose job state says it is working: Alive, so
// kill refuses it without --force.
func (f placeFleet) aliveRecord(t *testing.T) (console.Record, string) {
	t.Helper()
	r := console.Record{
		Kind: console.ProjectKind, Name: "proj", Mode: console.Background, Dir: f.proj,
		Task: "do the unit", DaemonID: "1234abcd", StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(home, ".claude", "jobs", r.DaemonID)
	if err := os.MkdirAll(job, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "state.json"), []byte(`{"state":"working"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := console.AppendRecord(f.sessions, r); err != nil {
		t.Fatal(err)
	}
	return r, job
}

func TestConsoleWatchRelaunchAfterTheSubcommandRelaunches(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	f.failedRecord(t)
	before, err := os.ReadFile(f.sessions)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"--roster", f.roster, "--sessions", f.sessions}

	out := sameOutputInEveryOrder(t, lq, [][]string{
		append(append([]string{}, paths...), "--relaunch", "--dry-run", "watch"),
		append(append([]string{}, paths...), "watch", "--relaunch", "--dry-run"),
		append(append([]string{}, paths...), "--relaunch", "watch", "--dry-run"),
		append(append([]string{}, paths...), "--dry-run", "watch", "--relaunch=true"),
		{"watch", "--sessions", f.sessions, "--relaunch", "--roster", f.roster, "--dry-run"},
	})
	if !strings.Contains(out, "relaunching:") || !strings.Contains(out, "dry run — nothing started") {
		t.Fatalf("watch --relaunch --dry-run must show the relaunch it would make:\n%s", out)
	}
	if n := len(f.launches(t)); n != 0 {
		t.Fatalf("a dry run launched claude %d time(s)", n)
	}
	if after, _ := os.ReadFile(f.sessions); string(after) != string(before) {
		t.Fatalf("a dry run rewrote the sessions file:\n%s", after)
	}

	// A bool's =value is read, on either side.
	out, errb, code := runConsole(t, lq, append(append([]string{}, paths...), "watch", "--relaunch=false", "--dry-run"))
	if code != 0 || strings.Contains(out, "relaunching:") {
		t.Fatalf("--relaunch=false relaunched (exit %d):\n%s%s", code, out, errb)
	}

	// And for real, as fleet-ops documents it: `./console.sh watch --relaunch`.
	out, errb, code = runConsole(t, lq, append(append([]string{}, paths...), "watch", "--relaunch"))
	if code != 0 {
		t.Fatalf("watch --relaunch exited %d\n%s%s", code, out, errb)
	}
	if n := len(f.launches(t)); n != 1 {
		t.Fatalf("watch --relaunch launched claude %d time(s), want 1\n%s", n, out)
	}
	recs := readRecords(t, f.sessions)
	if len(recs) != 1 || recs[0].LaunchError != "" || recs[0].DaemonID != "1234abcd" {
		t.Fatalf("the relaunched session must replace the failed record; records = %+v", recs)
	}
}

func TestConsoleKillForceAfterTheSubcommandForces(t *testing.T) {
	lq := realLacquer(t)

	// Without --force an Alive session is refused, and stays.
	f := newPlaceFleet(t)
	_, job := f.aliveRecord(t)
	_, errb, code := runConsole(t, lq, []string{"--sessions", f.sessions, "kill", "proj"})
	if code == 0 || !strings.Contains(errb, "alive") {
		t.Fatalf("kill without --force must refuse an Alive session (exit %d):\n%s", code, errb)
	}
	if _, err := os.Stat(job); err != nil {
		t.Fatalf("a refused kill removed the job: %v", err)
	}

	for _, args := range [][]string{
		{"--sessions", f.sessions, "--force", "kill", "proj"},
		{"--sessions", f.sessions, "kill", "proj", "--force"},
		{"--sessions", f.sessions, "kill", "--force", "proj"},
		{"kill", "proj", "--force=true", "--sessions", f.sessions},
	} {
		t.Run(strings.Join(args[len(args)-3:], " "), func(t *testing.T) {
			f := newPlaceFleet(t)
			args := append([]string{}, args...)
			for i, a := range args {
				if a == "--sessions" {
					args[i+1] = f.sessions
				}
			}
			_, job := f.aliveRecord(t)
			out, errb, code := runConsole(t, lq, args)
			if code != 0 {
				t.Fatalf("%q exited %d\n%s%s", args, code, out, errb)
			}
			if !strings.Contains(out, "killed proj") {
				t.Errorf("kill must report what it killed:\n%s", out)
			}
			if _, err := os.Stat(job); !os.IsNotExist(err) {
				t.Errorf("--force did not force: the job directory is still there (%v)", err)
			}
			if recs := readRecords(t, f.sessions); len(recs) != 0 {
				t.Errorf("the killed session is still recorded: %+v", recs)
			}
		})
	}
}

func TestConsoleDispatchFlagsParseOnEitherSide(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	wt := filepath.Join(f.root, "worktrees", "unit")
	addWorktree(t, f.proj, wt, "pm/unit")

	t.Run("dispatch", func(t *testing.T) {
		out := sameOutputInEveryOrder(t, lq, [][]string{
			{"--roster", f.roster, "--mode", "bg", "--branch", "feat/x", "--dry-run", "dispatch", "proj", "do the unit"},
			{"dispatch", "proj", "do the unit", "--roster", f.roster, "--mode", "bg", "--branch", "feat/x", "--dry-run"},
			{"--roster", f.roster, "dispatch", "--dry-run", "proj", "--mode=bg", "do the unit", "--branch=feat/x"},
			// A task in several words, flags among them: the words are the task.
			{"--roster", f.roster, "dispatch", "proj", "do", "--mode", "bg", "the", "--dry-run", "unit", "--branch", "feat/x"},
		})
		if !strings.Contains(out, "feat/x") || !strings.Contains(out, "do the unit") || !strings.Contains(out, "dry run — nothing started") {
			t.Fatalf("not the dry run of a dispatch on feat/x with the task:\n%s", out)
		}
	})
	t.Run("dispatch --worktree", func(t *testing.T) {
		out := sameOutputInEveryOrder(t, lq, [][]string{
			{"--roster", f.roster, "--mode", "tmux", "--worktree", wt, "--dry-run", "dispatch", "proj", "do the unit"},
			{"--roster", f.roster, "--mode", "tmux", "dispatch", "proj", "--worktree", wt, "do the unit", "--dry-run"},
		})
		if !strings.Contains(out, wt) {
			t.Fatalf("the dry run must run in the assigned worktree %s:\n%s", wt, out)
		}
	})
	t.Run("dispatch-role", func(t *testing.T) {
		out := sameOutputInEveryOrder(t, lq, [][]string{
			{"--roles", f.roles, "--branch", "feat/y", "--dry-run", "dispatch-role", "pm-bg", "take over"},
			{"dispatch-role", "pm-bg", "take over", "--roles", f.roles, "--branch", "feat/y", "--dry-run"},
			{"dispatch-role", "--dry-run", "pm-bg", "--branch=feat/y", "take over", "--roles=" + f.roles},
		})
		if !strings.Contains(out, "feat/y") || !strings.Contains(out, "take over") || !strings.Contains(out, "dry run — nothing started") {
			t.Fatalf("not the dry run of the role on feat/y with the override:\n%s", out)
		}
	})
	if n := len(f.launches(t)); n != 0 {
		t.Fatalf("a dry run launched %d time(s)", n)
	}
	if _, err := os.Stat(filepath.Join(f.proj, ".claude")); !os.IsNotExist(err) {
		t.Error("a dry run created a worktree")
	}
	if recs := readRecords(t, f.sessions); len(recs) != 0 {
		t.Errorf("a dry run was recorded: %+v", recs)
	}
}

// A flag-looking word the task needs goes after `--`, which ends the flags.
// Without it the word is parsed as a flag, and an unknown one is an error:
// the task never silently absorbs it, and a trailing --dry-run is never task
// text that launches for real.
func TestConsoleDispatchDoubleDashEndsTheFlags(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	head := []string{"--roster", f.roster, "--mode", "bg", "--branch", "feat/x"}

	out, errb, code := runConsole(t, lq, append(append([]string{}, head...), "--dry-run", "dispatch", "proj", "--", "fix", "the", "--verbose", "flag"))
	if code != 0 {
		t.Fatalf("exited %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "fix the --verbose flag") {
		t.Errorf("the words after -- must be the task:\n%s", out)
	}

	out, errb, code = runConsole(t, lq, append(append([]string{}, head...), "--dry-run", "dispatch", "proj", "fix", "the", "--verbose", "flag"))
	if code == 0 {
		t.Fatalf("an unknown flag in the task was accepted:\n%s", out)
	}
	if !strings.Contains(errb, "--verbose") || !strings.Contains(errb, " -- ") {
		t.Errorf("the error must name the flag and say how to pass it as task text:\n%s", errb)
	}

	// After --, even a console flag is task text: this launches (the fake),
	// with --dry-run in its task.
	out, errb, code = runConsole(t, lq, append(append([]string{}, head...), "dispatch", "proj", "--", "try", "--dry-run"))
	if code != 0 {
		t.Fatalf("exited %d\n%s%s", code, out, errb)
	}
	got := f.launches(t)
	if len(got) != 1 || got[0].args[len(got[0].args)-1] != "try --dry-run" {
		t.Fatalf("want one launch with the task %q, got %+v", "try --dry-run", got)
	}
}

func TestConsoleInboxFlagsParseOnEitherSide(t *testing.T) {
	lq := realLacquer(t)
	dir := t.TempDir()
	inboxPath := filepath.Join(dir, "inbox.jsonl")
	reset := func() {
		if err := os.Remove(inboxPath); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	for _, args := range [][]string{
		{"--inbox", inboxPath, "inbox", "add", "--type", "action", "--title", "decide"},
		{"inbox", "add", "--inbox", inboxPath, "--type", "action", "--title", "decide"},
		{"--type", "action", "inbox", "--inbox=" + inboxPath, "add", "--title", "decide"},
	} {
		reset()
		out, errb, code := runConsole(t, lq, args)
		if code != 0 {
			t.Fatalf("%q exited %d\n%s", args, code, errb)
		}
		id := strings.TrimSpace(out)
		list, _, _ := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "list"})
		if id == "" || !strings.Contains(list, id+"\taction\topen") || !strings.Contains(list, "decide") {
			t.Fatalf("%q did not add an open action entry %q:\n%s", args, id, list)
		}
	}

	id, _, _ := runConsole(t, lq, []string{"--inbox", inboxPath, "inbox", "add", "--type", "unread", "--title", "done"})
	id = strings.TrimSpace(id)
	out, errb, code := runConsole(t, lq, []string{"inbox", "resolve", id, "--inbox", inboxPath})
	if code != 0 || !strings.Contains(out, "resolved "+id) {
		t.Fatalf("resolve with --inbox after it exited %d\n%s%s", code, out, errb)
	}
	out = sameOutputInEveryOrder(t, lq, [][]string{
		{"--inbox", inboxPath, "--all", "inbox", "list"},
		{"inbox", "list", "--all", "--inbox", inboxPath},
		{"inbox", "--all", "list", "--inbox", inboxPath},
	})
	if !strings.Contains(out, id) {
		t.Fatalf("list --all must show the resolved entry %s:\n%s", id, out)
	}
}

// An unknown flag, or one the subcommand has no use for, is an error on
// either side of the subcommand. Accepted and ignored, it reads as honoured:
// `--dry-run kill` would kill for real.
func TestConsoleRefusesAnUnknownOrMisplacedFlag(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	_, job := f.aliveRecord(t)
	inboxPath := filepath.Join(f.root, "inbox.jsonl")
	before, err := os.ReadFile(f.sessions)
	if err != nil {
		t.Fatal(err)
	}
	S, R, L, I := f.sessions, f.roster, f.roles, inboxPath

	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown after watch", []string{"--sessions", S, "watch", "--relunch"}, "--relunch"},
		{"unknown before watch", []string{"--sessions", S, "--relunch", "watch"}, "--relunch"},
		{"unknown after kill", []string{"--sessions", S, "kill", "proj", "--froce"}, "--froce"},
		{"unknown after dispatch", []string{"--roster", R, "--mode", "bg", "dispatch", "proj", "t", "--dry-rn"}, "--dry-rn"},
		{"unknown after dispatch-role", []string{"--roles", L, "dispatch-role", "pm-bg", "-x"}, "-x"},
		{"unknown after inbox add", []string{"--inbox", I, "inbox", "add", "--type", "action", "--title", "T", "--titel", "U"}, "--titel"},
		{"unknown with the dashboard", []string{"--roster", R, "--rooster", R}, "--rooster"},
		{"--dry-run with kill", []string{"--sessions", S, "--dry-run", "kill", "proj", "--force"}, "--dry-run"},
		{"--dry-run after kill", []string{"--sessions", S, "kill", "proj", "--force", "--dry-run"}, "--dry-run"},
		{"--relaunch with kill", []string{"--sessions", S, "kill", "proj", "--relaunch"}, "--relaunch"},
		{"--model with watch", []string{"--sessions", S, "watch", "--model", "opus"}, "--model"},
		{"--effort with the dashboard", []string{"--roster", R, "--effort", "low"}, "--effort"},
		{"--force with watch", []string{"--sessions", S, "watch", "--force"}, "--force"},
		{"--mode with dispatch-role", []string{"--roles", L, "dispatch-role", "pm-bg", "--mode", "tmux", "--dry-run"}, "--mode"},
		{"--relaunch with dispatch", []string{"--roster", R, "--mode", "bg", "dispatch", "proj", "t", "--relaunch"}, "--relaunch"},
		{"--all with inbox add", []string{"--inbox", I, "inbox", "add", "--type", "action", "--title", "T", "--all"}, "--all"},
		{"--title with inbox list", []string{"--inbox", I, "inbox", "list", "--title", "T"}, "--title"},
		{"--all with inbox resolve", []string{"--inbox", I, "inbox", "resolve", "x", "--all"}, "--all"},
		{"--type with watch", []string{"--sessions", S, "--type", "action", "watch"}, "--type"},
		{"--force with the dashboard", []string{"--roster", R, "--force"}, "--force"},
		{"--interval without a value", []string{"--sessions", S, "watch", "--interval"}, "--interval"},
		{"--relaunch with a bad value", []string{"--sessions", S, "watch", "--relaunch=maybe"}, "--relaunch"},
		{"an unknown subcommand", []string{"--roster", R, "wacth", "--relaunch"}, "wacth"},
		{"an extra argument to watch", []string{"--sessions", S, "watch", "now"}, "now"},
		{"an extra argument to kill", []string{"--sessions", S, "kill", "proj", "--force", "other"}, "other"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, errb, code := runConsole(t, lq, tt.args)
			if code == 0 {
				t.Fatalf("%q was accepted\n%s", tt.args, out)
			}
			if !strings.Contains(errb, tt.want) {
				t.Errorf("stderr must name %q:\n%s", tt.want, errb)
			}
		})
	}
	if n := len(f.launches(t)); n != 0 {
		t.Errorf("a refused command launched %d time(s)", n)
	}
	if _, err := os.Stat(job); err != nil {
		t.Errorf("a refused kill removed the job: %v", err)
	}
	if after, _ := os.ReadFile(f.sessions); string(after) != string(before) {
		t.Errorf("a refused command changed the sessions file:\n%s", after)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Errorf("a refused inbox add wrote the inbox (%v)", err)
	}
}

// Every flag console defines has a declared scope. One without is refused
// everywhere (checkConsoleFlagScope fails closed), which this test would
// surface as an unusable flag rather than a silently unscoped one.
func TestConsoleEveryFlagHasADeclaredScope(t *testing.T) {
	lq := realLacquer(t)
	_, errb, _ := runConsole(t, lq, []string{"-h"})
	names := regexp.MustCompile(`(?m)^\s+-([a-z-]+)`).FindAllStringSubmatch(errb, -1)
	if len(names) < len(consoleFlagScope) {
		t.Fatalf("console -h listed %d flags, fewer than the %d scoped ones:\n%s", len(names), len(consoleFlagScope), errb)
	}
	for _, m := range names {
		if _, ok := consoleFlagScope[m[1]]; !ok {
			t.Errorf("--%s has no entry in consoleFlagScope", m[1])
		}
	}

	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Bool("unscoped", false, "")
	if err := fs.Set("unscoped", "true"); err != nil {
		t.Fatal(err)
	}
	if err := checkConsoleFlagScope(fs, "watch"); err == nil || !strings.Contains(err.Error(), "--unscoped") {
		t.Errorf("an unscoped flag must be refused, got %v", err)
	}
}
