package shipped

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// #387 moved the docs commands to resolve scripts/docs-relaxation.sh and
// .lacquer.toml from `git rev-parse --show-toplevel`, and its tests ran the
// rendered command with `sh -c` from the component directory — which is how
// lefthook runs it, but not the environment git runs lefthook in.
//
// Inside a hook, git may export GIT_DIR. It does in a LINKED WORKTREE (measured
// on git 2.55: pre-commit and pre-push both get GIT_DIR=<common>/worktrees/<wt>;
// a main checkout gets none). With GIT_DIR set and no GIT_WORK_TREE, git takes
// the CURRENT DIRECTORY as the top of the working tree, and `root: "admin/"`
// has already cd'd into the component. So `--show-toplevel` answered
// <worktree>/admin, the command looked for the relaxation script there, and
// failed "missing from the repository root (.../admin)" — blocking momfriend's
// sync push while the same command passed every test and `lefthook run`.
//
// These tests therefore go through git itself: a real `git push` (or `git
// commit`) whose hook cds into the component and runs the rendered command, from
// a main checkout and from a linked worktree. The iOS docs-hook.sh, which
// pre-commit runs, is exercised with GIT_DIR exported the way git exports it.

// gitHookEnv is the environment for a git command a test drives: this process's,
// minus anything that would point git at a repository other than the one the
// command runs in. `go test` run from inside a hook would otherwise inherit the
// outer repository's GIT_DIR, and every assertion here would be about that.
// binDir, when set, goes first on PATH so a hook can find a stub tool.
func gitHookEnv(binDir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_PREFIX":
			continue
		case "PATH":
			if binDir != "" {
				kv = "PATH=" + binDir + string(os.PathListSeparator) + kv[len("PATH="):]
			}
		}
		env = append(env, kv)
	}
	return append(env, "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@e")
}

// runGit runs git in dir and returns its combined output and exit code; unlike
// gitIn, a non-zero exit is a result, because a hook refusing is what some of
// these tests assert.
func runGit(t *testing.T, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	default:
		t.Fatalf("could not run git %v: %v", args, err)
		return "", -1
	}
}

// realPath resolves symlinks, so a path compares equal to what git prints
// (t.TempDir is under /var on macOS, which git reports as /private/var).
func realPath(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// checkout is where a test drives git from: the main checkout, or a linked
// worktree of it.
type checkout struct {
	name   string
	linked bool
}

var checkouts = []checkout{{"main checkout", false}, {"linked worktree", true}}

// underGit is a synced multistack project with a dated documentation relaxation
// committed, a local bare remote, and — for a linked checkout — a worktree on a
// branch of its own. work is the directory git is driven from.
type underGit struct {
	p      *project
	work   string
	branch string
}

func newUnderGit(t *testing.T, fixture string, c checkout) *underGit {
	t.Helper()
	p := fromFixture(t, fixture)
	p.sync()
	appendManifest(t, p, relaxedDocs)
	p.commit("sync, with a dated documentation relaxation")

	remote := t.TempDir()
	gittest.Init(t, remote, "-q", "--bare")
	gitIn(t, p.root, "remote", "add", "origin", remote)

	u := &underGit{p: p, work: p.root, branch: "main"}
	if c.linked {
		u.work = filepath.Join(t.TempDir(), "wt")
		u.branch = "feature"
		gitIn(t, p.root, "worktree", "add", "-q", "-b", u.branch, u.work)
	}
	return u
}

// installHook writes a git hook that does what lefthook does with a command:
// cd into its `root:`, relative to the top of the working tree git runs the hook
// from, and run its `run:` with `sh -c`. Hooks live in the common git dir, so a
// linked worktree runs the same one.
func (u *underGit) installHook(t *testing.T, hook, root, run string) {
	t.Helper()
	snippet := filepath.Join(t.TempDir(), hook+".run")
	if err := os.WriteFile(snippet, []byte(run), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n" +
		"cd '" + root + "' || exit 1\n" +
		"exec sh -c \"$(cat '" + snippet + "')\"\n"
	writeExe(t, filepath.Join(u.p.root, ".git", "hooks", hook), body)
}

// trigger makes git run hook for real: a push to the bare remote, or a commit.
func (u *underGit) trigger(t *testing.T, hook, binDir string) (string, int) {
	t.Helper()
	env := gitHookEnv(binDir)
	switch hook {
	case "pre-push":
		return runGit(t, u.work, env, "push", "origin", "HEAD:refs/heads/"+u.branch)
	case "pre-commit":
		if err := os.WriteFile(filepath.Join(u.work, "CHANGE"), []byte(t.Name()), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, code := runGit(t, u.work, env, "add", "CHANGE"); code != 0 {
			t.Fatalf("git add: %s", out)
		}
		return runGit(t, u.work, env, "commit", "-qm", "change")
	}
	t.Fatalf("no trigger for hook %q", hook)
	return "", -1
}

// docsUnderGit is one rendered docs command, the hook it lives in, and how to
// make its tool observable from the directory git will run it in.
type docsUnderGit struct {
	name, hook string
	// prepare installs the stub tool into the working tree and returns a PATH
	// directory (or ""). The stub is written after the worktree exists: the
	// component's node_modules/ is ignored, so a commit would not carry it.
	prepare func(t *testing.T, work, root string) string
}

var docsUnderGitCases = []docsUnderGit{
	{
		name: "web pre-push typedoc", hook: "pre-push",
		prepare: func(t *testing.T, work, root string) string {
			writeExe(t, filepath.Join(work, filepath.FromSlash(root), "node_modules", ".bin", "typedoc"),
				"#!/bin/sh\necho "+toolRan+"\n")
			return ""
		},
	},
	{
		name: "supabase pre-commit deno doc", hook: "pre-commit",
		prepare: func(t *testing.T, _, _ string) string { return stubDeno(t) },
	},
}

// TestDocsRelaxationHonouredUnderRealGit is the momfriend defect as git
// produces it: the docs command, run by git's own push or commit from a nested
// component, with a dated relaxation at the repository root. From a linked
// worktree it failed before the fix; from a main checkout it is the control
// that must keep passing.
func TestDocsRelaxationHonouredUnderRealGit(t *testing.T) {
	t.Parallel()
	for _, tc := range docsUnderGitCases {
		for _, c := range checkouts {
			t.Run(tc.name+" from a "+c.name, func(t *testing.T) {
				t.Parallel()
				u := newUnderGit(t, "multistack", c)
				root, run := hookCommand(t, u.p, tc.hook, "docs")
				if root == "" {
					t.Fatalf("%s.docs rendered with an empty root in the multistack fixture; this test needs a nested component", tc.hook)
				}
				u.installHook(t, tc.hook, root, run)
				bin := tc.prepare(t, u.work, root)

				out, code := u.trigger(t, tc.hook, bin)
				if code != 0 {
					t.Fatalf("with a valid relaxation, a real %s from a %s failed in %s (exit %d). If the "+
						"message names %s as the repository root, the command took git's GIT_DIR-relative "+
						"answer for the top of the working tree:\n%s", tc.hook, c.name, root, code, root, out)
				}
				if strings.Contains(out, toolRan) {
					t.Errorf("a valid [baseline.relax] documentation entry was ignored under a real %s from a %s; "+
						"the documentation tool ran anyway:\n%s", tc.hook, c.name, out)
				}
				// Also proves the hook ran at all: a push that never invoked it
				// would exit 0 without saying anything.
				if !strings.Contains(out, "relaxed") {
					t.Errorf("the hook did not report the relaxation; either it never ran or it skipped silently:\n%s", out)
				}
			})
		}
	}
}

// TestDocsRelaxationMissingScriptFailsUnderRealGit: #387's fail-loudly
// behaviour, under git. A missing script must still refuse the push or commit —
// and must name the REAL repository root, since a message pointing at the
// component is the defect wearing the right exit code.
func TestDocsRelaxationMissingScriptFailsUnderRealGit(t *testing.T) {
	t.Parallel()
	const missing = "scripts/docs-relaxation.sh"
	for _, tc := range docsUnderGitCases {
		for _, c := range checkouts {
			t.Run(tc.name+" from a "+c.name, func(t *testing.T) {
				t.Parallel()
				u := newUnderGit(t, "multistack", c)
				root, run := hookCommand(t, u.p, tc.hook, "docs")
				u.installHook(t, tc.hook, root, run)
				bin := tc.prepare(t, u.work, root)
				if err := os.Remove(filepath.Join(u.work, filepath.FromSlash(missing))); err != nil {
					t.Fatal(err)
				}

				out, code := u.trigger(t, tc.hook, bin)
				if code == 0 {
					t.Errorf("a real %s from a %s succeeded with %s missing; a check that cannot read its "+
						"inputs must fail:\n%s", tc.hook, c.name, missing, out)
				}
				if !strings.Contains(out, missing) {
					t.Errorf("the failure does not name %s:\n%s", missing, out)
				}
				if want := "(" + realPath(t, u.work) + ")"; !strings.Contains(out, want) {
					t.Errorf("the failure does not name the repository root %s; it resolved some other "+
						"directory as the root:\n%s", want, out)
				}
				if strings.Contains(out, toolRan) {
					t.Errorf("the documentation tool ran anyway with %s missing:\n%s", missing, out)
				}
			})
		}
	}
}

// TestDocsRelaxationUnderLefthookInARealPush runs the shipped hook the way a
// consumer does — lefthook, invoked by git's own pre-push, from a linked
// worktree — so the lefthook layer between git and the command is covered too.
// CI has no lefthook; the tests above do not depend on it.
func TestDocsRelaxationUnderLefthookInARealPush(t *testing.T) {
	t.Parallel()
	bin, err := exec.LookPath("lefthook")
	if err != nil {
		t.Skip("lefthook is not installed; skipping (the real-push tests above cover the command without it)")
	}
	u := newUnderGit(t, "multistack", checkout{"linked worktree", true})
	root, _ := hookCommand(t, u.p, "pre-push", "docs")
	docsUnderGitCases[0].prepare(t, u.work, root)
	writeExe(t, filepath.Join(u.p.root, ".git", "hooks", "pre-push"),
		"#!/bin/sh\nexec '"+bin+"' run pre-push --command docs --force\n")

	out, code := u.trigger(t, "pre-push", "")
	if code != 0 {
		t.Fatalf("with a valid relaxation, `lefthook run pre-push` under a real push from a linked worktree "+
			"failed (exit %d):\n%s", code, out)
	}
	if strings.Contains(out, toolRan) || !strings.Contains(out, "relaxed") {
		t.Errorf("lefthook's pre-push docs command did not honour the relaxation under a real push:\n%s", out)
	}
}

// TestIOSDocsHookWithGitDirExported: pre-commit runs scripts/docs-hook.sh from
// the top of the working tree, where GIT_DIR happens to give the right answer.
// From anywhere else it does not, and git exports GIT_DIR to hooks in a linked
// worktree. The hook must find the repository root regardless.
func TestIOSDocsHookWithGitDirExported(t *testing.T) {
	t.Parallel()
	for _, c := range checkouts {
		t.Run("from a "+c.name, func(t *testing.T) {
			t.Parallel()
			p, hook, stub := iosDocsHook(t)
			appendManifest(t, p, relaxedDocs)
			p.commit("sync, with a dated documentation relaxation")
			work := p.root
			if c.linked {
				work = filepath.Join(t.TempDir(), "wt")
				gitIn(t, p.root, "worktree", "add", "-q", "-b", "feature", work)
			}
			gitDir := strings.TrimSpace(gitIn(t, work, "rev-parse", "--absolute-git-dir"))

			cmd := exec.Command("sh", "-c", filepath.Join(work, filepath.FromSlash(hook))+" "+stub)
			cmd.Dir = filepath.Join(work, "Rootapp")
			cmd.Env = append(gitHookEnv(""), "GIT_DIR="+gitDir)
			b, err := cmd.CombinedOutput()
			out := string(b)
			if err != nil {
				t.Fatalf("with GIT_DIR=%s exported, %s run from Rootapp/ in a %s failed: %v\n%s",
					gitDir, hook, c.name, err, out)
			}
			if strings.Contains(out, toolRan) || !strings.Contains(out, "relaxed") {
				t.Errorf("with GIT_DIR exported, %s did not honour the relaxation at the repository root:\n%s", hook, out)
			}
		})
	}
}
