package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/console"
	"github.com/patrickserrano/lacquer/internal/gittest"
)

// The fleet's PM decides each IC's branch and worktree, creates them, and
// names them in the brief (fleet-ops personas/pm.md). Before --worktree and
// --branch, `console --mode bg dispatch` ignored that and always made a
// dispatch-<id> worktree of its own: the session ran somewhere other than
// where the brief said, and the PM's worktree sat unused beside an extra one
// that someone then had to clean up.

// placeFleet is a project repository and the files a dispatch reads, with a
// fake claude on PATH.
type placeFleet struct {
	root     string // resolved temp directory everything lives in
	proj     string // the project's repository, resolved
	roster   string
	roles    string
	sessions string
	calls    string // one file per fake claude launch: cwd, then argv
	failFlag string // while this file exists, the fake `claude --bg` fails
}

func newPlaceFleet(t *testing.T) placeFleet {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := placeFleet{
		root:     root,
		proj:     filepath.Join(root, "proj"),
		roster:   filepath.Join(root, "fleet.toml"),
		roles:    filepath.Join(root, "roles.toml"),
		sessions: filepath.Join(root, "sessions.jsonl"),
		calls:    filepath.Join(root, "calls"),
		failFlag: filepath.Join(root, "claude-fails"),
	}
	for _, d := range []string{filepath.Join(f.proj, "sub"), f.calls} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Init(t, f.proj, "-q")
	// A tracked file under sub/, so the subdirectory exists in every worktree.
	if err := os.WriteFile(filepath.Join(f.proj, "sub", "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, f.proj, "add", "-A")
	gitIn(t, f.proj, "commit", "-q", "-m", "init")

	roster := "[[project]]\nname = \"proj\"\npath = \"" + f.proj + "\"\n\n" +
		"[[project]]\nname = \"sub\"\npath = \"" + filepath.Join(f.proj, "sub") + "\"\n"
	roles := "[[role]]\nname = \"pm-bg\"\nmode = \"bg\"\ntask = \"run the unit\"\ndir = \"" + f.proj + "\"\n\n" +
		"[[role]]\nname = \"pm-tmux\"\ntask = \"run the unit\"\ndir = \"" + f.proj + "\"\n"
	if err := os.WriteFile(f.roster, []byte(roster), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.roles, []byte(roles), 0o644); err != nil {
		t.Fatal(err)
	}

	// `claude agents --json` lists no sessions; any other call is a launch,
	// recorded and never real.
	bin := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = agents ]; then echo '[]'; exit 0; fi
c="` + f.calls + `/$$"
{ pwd -P; for a in "$@"; do printf '%s\0' "$a"; done; } > "$c.tmp" && mv "$c.tmp" "$c"
if [ -e "` + f.failFlag + `" ]; then echo 'claude: failed to start' >&2; exit 1; fi
echo "backgrounded · 1234abcd"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Nor may a test reach the operator's tmux server: any tmux call counts
	// as a launch, and fails.
	tmux := "#!/bin/sh\n{ pwd -P; for a in tmux \"$@\"; do printf '%s\\0' \"$a\"; done; } > \"" + f.calls + "/tmux-$$\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(tmux), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// kill removes ~/.claude/jobs/<daemon id>: never the real one.
	t.Setenv("HOME", t.TempDir())
	return f
}

type launchCall struct {
	cwd  string
	args []string
}

func (f placeFleet) launches(t *testing.T) []launchCall {
	t.Helper()
	entries, err := os.ReadDir(f.calls)
	if err != nil {
		t.Fatal(err)
	}
	var out []launchCall
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(f.calls, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		cwd, argv, _ := strings.Cut(string(data), "\n")
		out = append(out, launchCall{cwd: cwd, args: strings.Split(strings.TrimSuffix(argv, "\x00"), "\x00")})
	}
	return out
}

// worktreeList is `git worktree list --porcelain` for repo: the set of
// worktrees, compared before and after to prove nothing was created.
func worktreeList(t *testing.T, repo string) string {
	t.Helper()
	return gitIn(t, repo, "worktree", "list", "--porcelain")
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// addWorktree creates a worktree of repo at path on a new branch, the way a
// PM does before briefing an IC -- outside the repository, as the fleet's
// PMs keep them.
func addWorktree(t *testing.T, repo, path, branch string) {
	t.Helper()
	gitIn(t, repo, "worktree", "add", "-q", "-b", branch, path)
}

func readRecords(t *testing.T, path string) []console.Record {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	recs, err := console.ReadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func TestConsoleBgDispatchRunsInTheAssignedWorktree(t *testing.T) {
	lq := realLacquer(t)
	for _, tt := range []struct {
		name string
		// args follow the global flags; "W" is replaced by the worktree.
		args []string
		sub  string // the project's subdirectory the session starts in
	}{
		{name: "project", args: []string{"--mode", "bg", "--worktree", "W", "dispatch", "proj", "do the unit"}},
		{name: "project in a subdirectory", args: []string{"--mode", "bg", "--worktree", "W", "dispatch", "sub", "do the unit"}, sub: "sub"},
		{name: "bg role", args: []string{"--worktree", "W", "dispatch-role", "pm-bg"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newPlaceFleet(t)
			wt := filepath.Join(f.root, "worktrees", "unit")
			addWorktree(t, f.proj, wt, "pm/unit")
			before := worktreeList(t, f.proj)
			args := func(extra ...string) []string {
				a := append([]string{"--roster", f.roster, "--roles", f.roles, "--sessions", f.sessions}, extra...)
				for _, x := range tt.args {
					a = append(a, strings.ReplaceAll(x, "W", wt))
				}
				return a
			}
			runDir := filepath.Join(wt, tt.sub)

			// Dry run: names the assigned worktree, plans no new one.
			out, errb, code := runConsole(t, lq, args("--dry-run"))
			if code != 0 {
				t.Fatalf("dry run exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errb)
			}
			if !strings.Contains(out, "(cd "+runDir+" && claude --bg") {
				t.Errorf("dry run does not start the session in %s:\n%s", runDir, out)
			}
			if !strings.Contains(out, "pm/unit") {
				t.Errorf("dry run does not name the worktree's branch:\n%s", out)
			}
			if strings.Contains(out, "worktree add") {
				t.Errorf("dry run plans a new worktree despite --worktree:\n%s", out)
			}

			// Real launch.
			out, errb, code = runConsole(t, lq, args())
			if code != 0 {
				t.Fatalf("dispatch exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errb)
			}
			if after := worktreeList(t, f.proj); after != before {
				t.Errorf("dispatch changed the repository's worktrees:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			if _, err := os.Stat(filepath.Join(f.proj, ".claude")); !os.IsNotExist(err) {
				t.Errorf("dispatch created %s/.claude", f.proj)
			}
			got := f.launches(t)
			if len(got) != 1 {
				t.Fatalf("claude launched %d time(s), want 1", len(got))
			}
			if got[0].cwd != runDir {
				t.Errorf("claude ran in %s, want %s", got[0].cwd, runDir)
			}
			recs := readRecords(t, f.sessions)
			if len(recs) != 1 {
				t.Fatalf("%d records, want 1", len(recs))
			}
			if recs[0].Worktree != wt || recs[0].Branch != "pm/unit" {
				t.Errorf("record worktree=%q branch=%q, want %q on pm/unit", recs[0].Worktree, recs[0].Branch, wt)
			}
			if b := gitIn(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); b != "pm/unit" {
				t.Errorf("the assigned worktree moved to %s", b)
			}
		})
	}
}

// Every way a path can fail to be one of this project's worktrees is refused
// before anything starts -- dry run included, and nothing is recorded, since
// relaunching an input error could never succeed.
func TestConsoleDispatchRefusesAWorktreeThatIsNotTheProjects(t *testing.T) {
	lq := realLacquer(t)
	for _, tt := range []struct {
		name string
		// path returns the --worktree value for fleet f.
		path func(t *testing.T, f placeFleet) string
		want string
	}{
		{
			name: "a plain directory",
			path: func(t *testing.T, f placeFleet) string {
				d := filepath.Join(f.root, "plain")
				if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
					t.Fatal(err)
				}
				return d
			},
			want: "not a registered worktree",
		},
		{
			name: "another repository's worktree",
			path: func(t *testing.T, f placeFleet) string {
				other := filepath.Join(f.root, "other")
				if err := os.MkdirAll(filepath.Join(other, "sub"), 0o755); err != nil {
					t.Fatal(err)
				}
				gittest.Init(t, other, "-q")
				if err := os.WriteFile(filepath.Join(other, "sub", "f.txt"), []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitIn(t, other, "add", "-A")
				gitIn(t, other, "commit", "-q", "-m", "init")
				wt := filepath.Join(f.root, "worktrees", "other-unit")
				addWorktree(t, other, wt, "pm/other")
				return wt
			},
			want: "not a registered worktree",
		},
		{
			name: "a worktree removed from git, its directory still there",
			path: func(t *testing.T, f placeFleet) string {
				wt := filepath.Join(f.root, "worktrees", "gone")
				addWorktree(t, f.proj, wt, "pm/gone")
				gitIn(t, f.proj, "worktree", "remove", wt)
				if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o755); err != nil {
					t.Fatal(err)
				}
				return wt
			},
			want: "not a registered worktree",
		},
		{
			name: "a path that does not exist",
			path: func(t *testing.T, f placeFleet) string { return filepath.Join(f.root, "nowhere") },
			want: "does not exist",
		},
	} {
		for _, mode := range []string{"bg", "tmux"} {
			for _, dry := range []bool{true, false} {
				name := tt.name + "/" + mode
				if dry {
					name += "/dry run"
				}
				t.Run(name, func(t *testing.T) {
					f := newPlaceFleet(t)
					wt := tt.path(t, f)
					before := worktreeList(t, f.proj)
					args := []string{"--roster", f.roster, "--sessions", f.sessions, "--mode", mode, "--worktree", wt}
					if dry {
						args = append(args, "--dry-run")
					}
					out, errb, code := runConsole(t, lq, append(args, "dispatch", "proj", "do the unit"))
					if code == 0 {
						t.Fatalf("dispatch into %s was not refused\nstdout:\n%s", wt, out)
					}
					if !strings.Contains(errb, tt.want) || !strings.Contains(errb, wt) {
						t.Errorf("stderr must say %q and name %s:\n%s", tt.want, wt, errb)
					}
					if n := len(f.launches(t)); n != 0 {
						t.Errorf("claude launched %d time(s)", n)
					}
					if strings.Contains(out, "tmux new-session") || strings.Contains(out, "claude --bg") {
						t.Errorf("a refused dispatch still printed a launch:\n%s", out)
					}
					if recs := readRecords(t, f.sessions); len(recs) != 0 {
						t.Errorf("a refusal was recorded: %+v", recs)
					}
					if after := worktreeList(t, f.proj); after != before {
						t.Errorf("a refused dispatch changed the worktrees:\n%s", after)
					}
				})
			}
		}
	}
}

// bg isolation means never the checkout dispatch was pointed at, and naming
// it as the "assigned worktree" must not be a way round that.
func TestConsoleBgDispatchRefusesTheCheckoutAsItsWorktree(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	_, errb, code := runConsole(t, lq, []string{"--roster", f.roster, "--mode", "bg", "--worktree", f.proj, "dispatch", "proj", "do the unit"})
	if code == 0 {
		t.Fatal("a bg dispatch into the project's own checkout was not refused")
	}
	if !strings.Contains(errb, "checkout") {
		t.Errorf("stderr must say why:\n%s", errb)
	}
	if n := len(f.launches(t)); n != 0 {
		t.Errorf("claude launched %d time(s)", n)
	}
}

func TestConsoleBgDispatchOnANamedBranch(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	wantWT := filepath.Join(f.proj, ".claude", "worktrees", "feat-x")
	base := []string{"--roster", f.roster, "--sessions", f.sessions, "--mode", "bg", "--branch", "feat/x"}

	out, errb, code := runConsole(t, lq, append(append([]string{}, base...), "--dry-run", "dispatch", "proj", "do the unit"))
	if code != 0 {
		t.Fatalf("dry run exited %d\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "-b feat/x "+wantWT+" ") {
		t.Errorf("dry run does not plan branch feat/x at %s:\n%s", wantWT, out)
	}
	if strings.Contains(out, "dispatch/") {
		t.Errorf("dry run still plans a dispatch/<id> branch:\n%s", out)
	}
	if _, err := os.Stat(wantWT); !os.IsNotExist(err) {
		t.Fatal("a dry run created the worktree")
	}

	out, errb, code = runConsole(t, lq, append(append([]string{}, base...), "dispatch", "proj", "do the unit"))
	if code != 0 {
		t.Fatalf("dispatch exited %d\n%s%s", code, out, errb)
	}
	if b := gitIn(t, wantWT, "rev-parse", "--abbrev-ref", "HEAD"); b != "feat/x" {
		t.Errorf("worktree %s is on %s, want feat/x", wantWT, b)
	}
	got := f.launches(t)
	if len(got) != 1 || got[0].cwd != wantWT {
		t.Fatalf("claude launches = %+v, want one in %s", got, wantWT)
	}
	recs := readRecords(t, f.sessions)
	if len(recs) != 1 || recs[0].Worktree != wantWT || recs[0].Branch != "feat/x" {
		t.Errorf("records = %+v, want worktree %s on feat/x", recs, wantWT)
	}
	// Not left dirty by the nested worktree.
	if st := gitIn(t, f.proj, "status", "--porcelain"); st != "" {
		t.Errorf("the checkout is not clean:\n%s", st)
	}
}

// A branch or directory that already exists is refused, never reused: reusing
// one silently is what --worktree is for, said explicitly.
func TestConsoleBgDispatchRefusesABranchItCannotCreate(t *testing.T) {
	lq := realLacquer(t)
	for _, tt := range []struct {
		name   string
		branch string
		setup  func(t *testing.T, f placeFleet)
		want   string
	}{
		{name: "existing branch", branch: "pm/taken", setup: func(t *testing.T, f placeFleet) { gitIn(t, f.proj, "branch", "pm/taken") }, want: "already exists"},
		{name: "existing directory", branch: "pm/dir", setup: func(t *testing.T, f placeFleet) {
			if err := os.MkdirAll(filepath.Join(f.proj, ".claude", "worktrees", "pm-dir"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "already exists"},
		{name: "invalid name", branch: "bad..name", want: "not a valid branch name"},
		{name: "option-like name", branch: "-f", want: "not a valid branch name"},
		// With a previous branch to name, check-ref-format --branch accepts
		// @{-1} and prints that branch instead.
		{name: "a name git would expand", branch: "@{-1}", setup: func(t *testing.T, f placeFleet) {
			gitIn(t, f.proj, "checkout", "-q", "-b", "prev")
			gitIn(t, f.proj, "checkout", "-q", "-")
			if out := gitIn(t, f.proj, "check-ref-format", "--branch", "@{-1}"); out != "prev" {
				t.Fatalf("fixture: @{-1} expands to %q, want prev", out)
			}
		}, want: "not a valid branch name"},
	} {
		for _, dry := range []bool{true, false} {
			name := tt.name
			if dry {
				name += "/dry run"
			}
			t.Run(name, func(t *testing.T) {
				f := newPlaceFleet(t)
				if tt.setup != nil {
					tt.setup(t, f)
				}
				before := worktreeList(t, f.proj)
				args := []string{"--roster", f.roster, "--sessions", f.sessions, "--mode", "bg", "--branch=" + tt.branch}
				if dry {
					args = append(args, "--dry-run")
				}
				out, errb, code := runConsole(t, lq, append(args, "dispatch", "proj", "do the unit"))
				if code == 0 {
					t.Fatalf("--branch %s was not refused\n%s", tt.branch, out)
				}
				if !strings.Contains(errb, tt.want) {
					t.Errorf("stderr must say %q:\n%s", tt.want, errb)
				}
				if n := len(f.launches(t)); n != 0 {
					t.Errorf("claude launched %d time(s)", n)
				}
				if recs := readRecords(t, f.sessions); len(recs) != 0 {
					t.Errorf("a refusal was recorded: %+v", recs)
				}
				if after := worktreeList(t, f.proj); after != before {
					t.Errorf("a refused dispatch changed the worktrees:\n%s", after)
				}
			})
		}
	}
}

func TestConsoleDispatchRefusesAmbiguousOrMisplacedPlacement(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	wt := filepath.Join(f.root, "worktrees", "unit")
	addWorktree(t, f.proj, wt, "pm/unit")
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "--worktree and --branch", args: []string{"--roster", f.roster, "--mode", "bg", "--worktree", wt, "--branch", "feat/y", "dispatch", "proj", "t"}, want: "--worktree and --branch"},
		{name: "--branch in tmux mode", args: []string{"--roster", f.roster, "--mode", "tmux", "--branch", "feat/y", "dispatch", "proj", "t"}, want: "tmux"},
		{name: "--branch on a tmux role", args: []string{"--roles", f.roles, "--branch", "feat/y", "dispatch-role", "pm-tmux"}, want: "tmux"},
		{name: "--worktree after dispatch", args: []string{"--roster", f.roster, "--mode", "bg", "dispatch", "proj", "--worktree", wt, "t"}, want: "--worktree after dispatch"},
		{name: "--branch= after dispatch-role", args: []string{"--roles", f.roles, "dispatch-role", "pm-bg", "--branch=feat/y"}, want: "--branch=feat/y after dispatch-role"},
		{name: "--dry-run after the task", args: []string{"--roster", f.roster, "--mode", "bg", "dispatch", "proj", "t", "--dry-run"}, want: "--dry-run after dispatch"},
		{name: "--worktree with watch", args: []string{"--sessions", f.sessions, "--worktree", wt, "watch"}, want: "--worktree"},
		{name: "--branch with the dashboard", args: []string{"--roster", f.roster, "--branch", "feat/y"}, want: "--branch"},
	} {
		for _, dry := range []bool{true, false} {
			name := tt.name
			if dry {
				name += "/dry run"
			}
			t.Run(name, func(t *testing.T) {
				args := tt.args
				if dry {
					args = append([]string{"--dry-run"}, args...)
				}
				out, errb, code := runConsole(t, lq, args)
				if code == 0 {
					t.Fatalf("not refused\n%s", out)
				}
				if !strings.Contains(errb, tt.want) {
					t.Errorf("stderr must mention %q:\n%s", tt.want, errb)
				}
				if n := len(f.launches(t)); n != 0 {
					t.Errorf("claude launched %d time(s)", n)
				}
			})
		}
	}
	if _, err := os.Stat(filepath.Join(f.proj, ".claude")); !os.IsNotExist(err) {
		t.Error("a refused dispatch created a worktree")
	}
}

// An assigned worktree is recorded exactly like one lacquer made: a relaunch
// resumes in it, and kill keeps it.
func TestConsoleRelaunchAndKillKeepTheAssignedWorktree(t *testing.T) {
	lq := realLacquer(t)
	f := newPlaceFleet(t)
	wt := filepath.Join(f.root, "worktrees", "unit")
	addWorktree(t, f.proj, wt, "pm/unit")
	before := worktreeList(t, f.proj)

	// The first launch fails, which is what makes the record relaunchable.
	if err := os.WriteFile(f.failFlag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, errb, code := runConsole(t, lq, []string{"--roster", f.roster, "--sessions", f.sessions, "--mode", "bg", "--worktree", wt, "dispatch", "proj", "do the unit"})
	if code == 0 {
		t.Fatal("the failing launch exited 0")
	}
	recs := readRecords(t, f.sessions)
	if len(recs) != 1 || recs[0].LaunchError == "" || recs[0].Worktree != wt {
		t.Fatalf("the failed launch must be recorded with its worktree; records = %+v\nstderr:\n%s", recs, errb)
	}
	if err := os.Remove(f.failFlag); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("half done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errb, code := runConsole(t, lq, []string{"--roster", f.roster, "--sessions", f.sessions, "--relaunch", "watch"})
	if code != 0 {
		t.Fatalf("watch --relaunch exited %d\n%s%s", code, out, errb)
	}
	got := f.launches(t)
	if len(got) != 2 {
		t.Fatalf("claude launched %d time(s), want 2\n%s", len(got), out)
	}
	for _, c := range got {
		if c.cwd != wt {
			t.Errorf("a launch ran in %s, want the assigned worktree %s", c.cwd, wt)
		}
	}
	if !strings.Contains(out, "resuming in the recorded worktree "+wt) {
		t.Errorf("the relaunch must say it resumed in %s:\n%s", wt, out)
	}
	if after := worktreeList(t, f.proj); after != before {
		t.Errorf("the relaunch changed the worktrees:\n%s", after)
	}

	_, errb, code = runConsole(t, lq, []string{"--sessions", f.sessions, "kill", "proj"})
	if code != 0 {
		t.Fatalf("kill exited %d\n%s", code, errb)
	}
	if !strings.Contains(errb, "kept worktree "+wt) {
		t.Errorf("kill must say it kept %s:\n%s", wt, errb)
	}
	if _, err := os.Stat(filepath.Join(wt, "wip.txt")); err != nil {
		t.Errorf("kill deleted the assigned worktree's work: %v", err)
	}
	if after := worktreeList(t, f.proj); after != before {
		t.Errorf("kill changed the worktrees:\n%s", after)
	}
}

func TestConsoleTmuxDispatchStartsInTheAssignedWorktree(t *testing.T) {
	lq := realLacquer(t)
	for _, tt := range []struct {
		name string
		args []string
		sess string
		sub  string
	}{
		{name: "project", args: []string{"--mode", "tmux", "--worktree", "W", "--dry-run", "dispatch", "proj", "t"}, sess: "proj"},
		{name: "project in a subdirectory", args: []string{"--mode", "tmux", "--worktree", "W", "--dry-run", "dispatch", "sub", "t"}, sess: "sub", sub: "sub"},
		{name: "tmux role", args: []string{"--worktree", "W", "--dry-run", "dispatch-role", "pm-tmux"}, sess: "pm-tmux"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newPlaceFleet(t)
			wt := filepath.Join(f.root, "worktrees", "unit")
			addWorktree(t, f.proj, wt, "pm/unit")
			args := []string{"--roster", f.roster, "--roles", f.roles}
			for _, a := range tt.args {
				args = append(args, strings.ReplaceAll(a, "W", wt))
			}
			out, errb, code := runConsole(t, lq, args)
			if code != 0 {
				t.Fatalf("dry run exited %d\n%s%s", code, out, errb)
			}
			if want := "tmux new-session -d -s " + tt.sess + " -c " + filepath.Join(wt, tt.sub) + " claude"; !strings.Contains(out, want) {
				t.Errorf("want %q in:\n%s", want, out)
			}
		})
	}
}
