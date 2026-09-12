//go:build eval

package eval

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// repoRoot is the lacquer checkout running this test binary. Unlike
// cmd/lacquer/fixture_test.go's realLacquer, this FAILS rather than skips when
// it cannot find one: a scenario that cannot locate the checkout it is meant to
// grade has not passed, it has never run, and this suite exists specifically to
// stop that distinction from being silent.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("setup failed: resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "VERSION")); err != nil {
		t.Fatalf("setup failed: %s is not a lacquer checkout (no VERSION): %v", root, err)
	}
	if fi, err := os.Stat(filepath.Join(root, "profiles")); err != nil || !fi.IsDir() {
		t.Fatalf("setup failed: %s is not a lacquer checkout (no profiles/): %v", root, err)
	}
	return root
}

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// buildLacquer builds cmd/lacquer once per test binary invocation and returns
// the path to the binary. Scenarios that need to grade the actual dispatch
// wiring in cmd/lacquer/main.go (not just an internal package's own logic) exec
// this rather than the source's unexported run(), so what is graded is the
// artifact a person actually runs.
func buildLacquer(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRootOnce(t)
		dir, err := os.MkdirTemp("", "lacquer-eval-bin-")
		if err != nil {
			buildErr = fmt.Errorf("mktemp: %w", err)
			return
		}
		bin := filepath.Join(dir, "lacquer")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/lacquer")
		cmd.Dir = root
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("go build ./cmd/lacquer: %w\n%s", err, out.String())
			return
		}
		builtBin = bin
	})
	if buildErr != nil {
		t.Fatalf("setup failed: %v", buildErr)
	}
	return builtBin
}

// repoRootOnce resolves the repo root without requiring a *testing.T call
// stack that is still alive when buildOnce's func runs later — sync.Once's
// callback can outlive the t that first triggered it across subtests, so this
// re-derives the path directly instead of caching t.
func repoRootOnce(t *testing.T) string {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("setup failed: resolve repo root: %v", err)
	}
	return root
}

// runGit runs git in dir with a fixed, deterministic identity so commits never
// depend on the host's global git config.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=eval@lacquer.test", "-c", "user.name=lacquer eval"}, args...)
	cmd := exec.Command("git", full...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("setup failed: git %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// initGitRepo turns dir into a git repository with everything committed.
func initGitRepo(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "init", "-q", "--initial-branch=main")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", msg)
}

// writeFile writes body to path, creating parent directories as needed.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("setup failed: mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("setup failed: write %s: %v", path, err)
	}
}

// runResult is one invocation of the built lacquer binary.
type runResult struct {
	Stdout string
	Stderr string
	Code   int
}

// Combined is stdout+stderr concatenated, the way a terminal scrollback reads.
func (r runResult) Combined() string { return r.Stdout + r.Stderr }

// runLacquer execs the built binary bin in dir with the given environment
// (LACQUER_ROOT etc.) and args, e.g. runLacquer(t, bin, projectDir, env,
// "status").
func runLacquer(t *testing.T, bin, dir string, env map[string]string, args ...string) runResult {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("setup failed: run %s %v: %v", bin, args, err)
		}
	}
	return runResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: code}
}

// minimalLacquerRoot builds a self-contained, synthetic lacquer content root
// (VERSION + empty profiles/, enough to satisfy cmd/lacquer's
// requireLacquerRoot) as its own git repository, deliberately decoupled from
// this repo's real content so scenarios that only need root PROVENANCE
// (version, branch, dirty state) do not also depend on — or risk being masked
// by errors from — real profile rendering.
func minimalLacquerRoot(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "VERSION"), version+"\n")
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		t.Fatalf("setup failed: mkdir profiles: %v", err)
	}
	writeFile(t, filepath.Join(dir, "profiles", ".gitkeep"), "")
	initGitRepo(t, dir, "release "+version)
	return dir
}

// minimalProject builds a throwaway project valid enough for `lacquer status`
// to load: a git repo with an empty-component manifest. [project].name is the
// only field validateProject requires to be non-empty-but-well-formed here, and
// zero [[component]] entries is a legal, common state (a project that has
// never adopted a stack).
func minimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".lacquer.toml"), "[project]\nname = \"probe\"\n")
	initGitRepo(t, dir, "project baseline")
	return dir
}

// webSupabaseFixtureProject mirrors cmd/lacquer/fixture_test.go's
// fixtureProject: a throwaway project shaped like a real one (git repo,
// manifest, the markers stack detection keys on) using profiles this repo
// actually ships, for scenarios that need a REAL sync (real rendered files,
// real .lacquer.lock) rather than just a loadable manifest.
func webSupabaseFixtureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []struct{ path, body string }{
		{"package.json", "{}"},
		{"server/supabase/config.toml", ""},
		{".lacquer.toml", "[project]\nname = \"fixture\"\ngithub_org = \"acme\"\n\n" +
			"[[component]]\npath = \".\"\nprofiles = [\"web\"]\n\n" +
			"[[component]]\npath = \"server\"\nprofiles = [\"supabase\"]\n"},
	} {
		writeFile(t, filepath.Join(dir, f.path), f.body)
	}
	initGitRepo(t, dir, "fixture")
	return dir
}
