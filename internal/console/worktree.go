package console

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// A bg dispatch runs in a git worktree and branch made for it alone, under
// <repo>/.claude/worktrees/. It never runs in the checkout it was dispatched
// against: if the worktree cannot be made, nothing is launched.
//
// The location is the one Claude Code's own bg sessions treat as already
// isolated. A bg session started anywhere else is told to make a worktree of
// its own before editing, which would leave the session working somewhere
// other than the worktree this records.
//
// Only a worktree whose path lacquer recorded (Record.Worktree) is ever
// reused, and lacquer never removes one. The operator's own agents keep
// worktrees in the same directory.

// worktreesDir is where dispatch worktrees go, relative to the repo root.
var worktreesDir = filepath.Join(".claude", "worktrees")

// dispatchWorktree is the worktree one bg session runs in.
type dispatchWorktree struct {
	path   string // the worktree's root, recorded as Record.Worktree
	branch string
	runDir string // where the session starts: dir's counterpart inside path
	// setup is what making (or finding) the worktree did, shown to whoever
	// dispatched: the git command, what it was based on, and any fallback.
	setup string
}

// newWorktreeID names one dispatch's worktree and branch. The timestamp
// keeps them sortable, and the random suffix keeps concurrent dispatches of
// one project apart. Should two ever collide anyway, `git worktree add -b`
// refuses an existing branch or path, and the dispatch fails loudly.
func newWorktreeID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate worktree id: %w", err)
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b), nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// repoRoot returns the root of the git repository dir is in, and dir's path
// relative to it, so a session dispatched to a subdirectory starts in the
// same subdirectory of its worktree.
//
// dir is made absolute first. git's toplevel always is, and filepath.Rel of
// an absolute root against a relative dir fails ("can't make ../proj
// relative to /.../proj") -- which refused every bg dispatch from a relative
// --roster in 1.37.3 through 1.37.9. The loaders now resolve their paths to
// absolute; this does not rely on every caller having done so.
func repoRoot(dir string) (root, rel string, err error) {
	dir = absPath(dir)
	root, err = gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("%s is not in a git repository: %w", dir, err)
	}
	// --show-toplevel resolves symlinks (/var -> /private/var on macOS), so
	// dir must be resolved the same way before taking the difference.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", dir, err)
	}
	rel, err = filepath.Rel(root, realDir)
	if err != nil {
		return "", "", err
	}
	return root, rel, nil
}

// absPath is p made absolute against the process's cwd, or p unchanged if
// that cannot be done (only when the cwd itself is unreadable).
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// worktreeBase picks the commit a new worktree branches from: the remote's
// default branch, freshly fetched, when the repository has one, so a session
// never inherits whatever branch (and unpushed work) the operator happens to
// have checked out. Otherwise the checkout's HEAD. When fetch is false (a dry
// run) nothing is fetched. note says which was used and why.
func worktreeBase(root string, fetch bool) (base, note string) {
	ref, err := gitOutput(root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil || ref == "" {
		head, _ := gitOutput(root, "rev-parse", "--abbrev-ref", "HEAD")
		return "HEAD", fmt.Sprintf("branching from the checkout's HEAD (%s): the repository has no origin/HEAD", head)
	}
	if !fetch {
		return ref, "branching from " + ref + " (fetched first on a real dispatch)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "fetch", "--quiet", "origin", strings.TrimPrefix(ref, "origin/"))
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return ref, fmt.Sprintf("branching from %s as last fetched: git fetch failed (%v: %s)", ref, err, strings.TrimSpace(string(out)))
	}
	return ref, "branching from " + ref + ", just fetched"
}

// excludeWorktrees makes sure git ignores .claude/worktrees/ in root's
// checkout. A nested worktree otherwise shows as untracked there, and a
// `git add -A` in the checkout would commit it as an embedded repository.
// Written to .git/info/exclude, never to a tracked .gitignore: this is local
// state, not a change to the project.
func excludeWorktrees(root string) error {
	probe := filepath.Join(worktreesDir, "probe")
	if _, err := gitOutput(root, "check-ignore", "-q", probe); err == nil {
		return nil
	}
	exclude, err := gitOutput(root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(root, exclude)
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(exclude), err)
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", exclude, err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n# lacquer console dispatch worktrees\n**/.claude/worktrees/\n"); err != nil {
		return fmt.Errorf("write %s: %w", exclude, err)
	}
	return nil
}

// worktreePlan is a new dispatch worktree, resolved but not yet created.
type worktreePlan struct {
	root   string // the repository's root, where the worktree is added
	rel    string // the dispatched directory, relative to root
	path   string
	branch string
	base   string
	note   string // which base was chosen, and why
}

func (p worktreePlan) addArgs() []string {
	return []string{"git", "-C", p.root, "worktree", "add", "--quiet", "--no-track", "-b", p.branch, p.path, p.base}
}

// planWorktree resolves where a new dispatch worktree for dir would go,
// without creating anything. branch names its branch, and its directory is
// derived from it; empty means dispatch/<id>. A named branch or directory
// that already exists is refused, never reused: a dispatcher that means an
// existing worktree says so with --worktree (assignedWorktree).
func planWorktree(dir, branch string, fetch bool) (worktreePlan, error) {
	root, rel, err := repoRoot(dir)
	if err != nil {
		return worktreePlan{}, fmt.Errorf("no worktree to isolate a bg session in; refusing rather than running it in the checkout: %w", err)
	}
	p := worktreePlan{root: root, rel: rel, branch: branch}
	if branch == "" {
		id, err := newWorktreeID()
		if err != nil {
			return worktreePlan{}, err
		}
		p.path = filepath.Join(root, worktreesDir, "dispatch-"+id)
		p.branch = "dispatch/" + id
	} else {
		if err := checkNewBranch(root, branch); err != nil {
			return worktreePlan{}, err
		}
		p.path = filepath.Join(root, worktreesDir, branchDirName(branch))
		if _, err := os.Lstat(p.path); err == nil {
			return worktreePlan{}, fmt.Errorf("refusing --branch %s: its worktree directory %s already exists; to run in an existing worktree, pass --worktree instead", branch, p.path)
		}
	}
	p.base, p.note = worktreeBase(root, fetch)
	return p, nil
}

// checkNewBranch refuses a --branch name git would not create as written, or
// one that already exists.
func checkNewBranch(root, branch string) error {
	// check-ref-format --branch expands @{-N} to the name of a branch that
	// exists, so its output must be the name exactly as given. It refuses a
	// leading '-', which would otherwise reach `git worktree add -b` as an
	// option.
	out, err := gitOutput(root, "check-ref-format", "--branch", branch)
	if err != nil || out != branch {
		return fmt.Errorf("refusing --branch %q: not a valid branch name (git check-ref-format --branch)", branch)
	}
	if _, err := gitOutput(root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return fmt.Errorf("refusing --branch %s: that branch already exists in %s; to run in its existing worktree, pass --worktree instead", branch, root)
	}
	return nil
}

// branchDirName is the directory under .claude/worktrees/ for a --branch
// worktree: the branch with its slashes flattened, so feat/x is feat-x.
func branchDirName(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// createWorktree makes a new worktree and branch for one bg dispatch, on
// branch when one is named (see planWorktree).
func createWorktree(dir, branch string) (dispatchWorktree, error) {
	lock, err := lockWorktrees(dir)
	if err != nil {
		return dispatchWorktree{}, err
	}
	defer lock.Close()
	p, err := planWorktree(dir, branch, true)
	if err != nil {
		return dispatchWorktree{}, err
	}
	if err := excludeWorktrees(p.root); err != nil {
		return dispatchWorktree{}, fmt.Errorf("could not exclude %s from %s's git status: %w", worktreesDir, p.root, err)
	}
	argv := p.addArgs()
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil { // #nosec G204 -- fixed git subcommand; the path is generated here and the branch passed check-ref-format
		return dispatchWorktree{}, fmt.Errorf("could not create a worktree for the bg session (%s): %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return dispatchWorktree{
		path:   p.path,
		branch: p.branch,
		runDir: filepath.Join(p.path, p.rel),
		setup:  strings.Join(argv, " ") + "\n  (" + p.note + ")\n",
	}, nil
}

// assignedWorktree is the existing worktree a dispatcher named with
// --worktree: a fleet PM decides each IC's branch and worktree, creates them,
// and names them in the brief (fleet-ops personas/pm.md), so the session must
// run there, not in one lacquer makes beside it. It is checked, and never
// created, changed or removed.
//
// It must be a registered worktree of dir's repository -- not merely a
// directory, which could be anything, nor another repository's worktree -- and
// for a bg session not the checkout itself, which bg never runs in.
func assignedWorktree(dir, path string, mode Mode) (dispatchWorktree, error) {
	abs := filepath.Clean(absPath(path))
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: it does not exist", abs)
		}
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: %w", abs, err)
	}
	root, rel, err := repoRoot(dir)
	if err != nil {
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: %w", abs, err)
	}
	registered, err := worktreePaths(dir)
	if err != nil {
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: %w", abs, err)
	}
	if !slices.Contains(registered, real) {
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: it is not a registered worktree of %s's repository (see `git -C %s worktree list`)", abs, root, root)
	}
	if mode == Background && (real == root || real == registered[0]) {
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: it is the repository's checkout, not a worktree of its own, and a bg session never runs in the checkout", abs)
	}
	runDir := filepath.Join(abs, rel)
	if fi, err := os.Stat(runDir); err != nil || !fi.IsDir() {
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: it has no %s, the dispatched directory's counterpart inside it", abs, runDir)
	}
	branch, err := gitOutput(abs, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return dispatchWorktree{}, fmt.Errorf("refusing --worktree %s: %w", abs, err)
	}
	return dispatchWorktree{
		path:   abs,
		branch: branch,
		runDir: runDir,
		setup:  "running in the assigned worktree " + abs + " (branch " + branch + "); lacquer does not create, change or remove it\n",
	}, nil
}

// worktreePaths lists the registered worktrees of dir's repository, the main
// one first, each resolved where it exists.
func worktreePaths(dir string) ([]string, error) {
	out, err := gitOutput(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		p, ok := strings.CutPrefix(sc.Text(), "worktree ")
		if !ok {
			continue
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// registeredWorktree reports whether path is one of dir's repository's
// registered worktrees. A directory that merely exists at that path is not
// enough: it may since have been removed from git and something else put
// there.
func registeredWorktree(dir, path string) bool {
	want, err := filepath.EvalSymlinks(absPath(path))
	if err != nil {
		return false
	}
	registered, err := worktreePaths(dir)
	return err == nil && slices.Contains(registered, want)
}

// resumeWorktree returns the recorded worktree for a relaunch if it is still
// a registered worktree of dir's repository, or otherwise a fresh one, saying
// so in setup. Never the checkout.
func resumeWorktree(dir, recorded string) (dispatchWorktree, error) {
	if registeredWorktree(dir, recorded) {
		_, rel, err := repoRoot(dir)
		if err != nil {
			return dispatchWorktree{}, err
		}
		branch, err := gitOutput(recorded, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return dispatchWorktree{}, err
		}
		return dispatchWorktree{
			path:   recorded,
			branch: branch,
			runDir: filepath.Join(recorded, rel),
			setup:  "resuming in the recorded worktree " + recorded + " (branch " + branch + ")\n",
		}, nil
	}
	wt, err := createWorktree(dir, "")
	if err != nil {
		return wt, err
	}
	wt.setup = "the recorded worktree " + recorded + " is no longer a registered worktree, so it is not reused; making a fresh one:\n" + wt.setup
	return wt, nil
}

// within reports whether path is dir or inside it, comparing resolved paths
// where they exist.
func within(path, dir string) bool {
	resolve := func(p string) string {
		p = absPath(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	p, d := resolve(path), resolve(dir)
	return p == d || strings.HasPrefix(p, d+string(filepath.Separator))
}
