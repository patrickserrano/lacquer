# Whole-File Asset Sync + Git Guard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extend `harness sync` to also distribute whole-file assets (skills, commands, CI workflows, stack configs) from core + profiles into a project, overwriting them — but never clobbering a file with uncommitted local changes.

**Architecture:** Two new packages compose into the existing `sync.Run`. `internal/gitguard` answers "does this file have uncommitted changes in the project's git repo?" via `git status --porcelain`. `internal/assets` enumerates source→destination asset mappings per the design's placement rules. `sync.Run` gains a second phase that runs a **preflight guard pass** (abort if any asset target is dirty, listing all conflicts) and then copies every asset, reusing `internal/safepath` to confine destinations within the project root.

**Tech Stack:** Go 1.23 — build with `env -u GOROOT /opt/homebrew/bin/go` (PATH `go` is a stale 1.17; the shell profile exports a bad `GOROOT`, so it must be cleared per-invocation). Reference: `docs/plans/2026-06-15-harness-design.md`.

---

## Scope boundary (read first)

**In scope:** `internal/gitguard`; `internal/assets` (enumerate + placement); preflight dirty-check; whole-file copy into a project; wiring into `sync.Run`; CLI reporting; a closing security audit.

**Placement rules** (from the design — implement exactly these):

| Harness source | Project destination | Scope |
|----------------|---------------------|-------|
| `core/skills/**`, `profiles/<p>/skills/**` | `.claude/skills/**` (union) | root |
| `core/commands/**`, `profiles/<p>/commands/**` | `.claude/commands/**` (union) | root |
| `profiles/<p>/workflows/<f>` | `.github/workflows/<p>-<f>` (stack-prefixed) | root |
| `profiles/<p>/config/<f>` | `<component.path>/<f>` | per component that lists `<p>` |

**Explicitly deferred (later plans):** `harvest`, `scaffold`/`new`, `upgrade`, Renovate, absorbing `ios-template` (this plan ships the *mechanism*; real asset content arrives when ios-template is absorbed). Deletion of assets removed from the harness (sync only adds/overwrites for now) is out of scope — note it as a known limitation.

**Conventions:** "dirty" = a tracked file with unstaged/uncommitted modifications, or a file that exists but is untracked. A clean (committed) file is safe to overwrite because the change is then reviewable in git.

---

## Task 1: gitguard — detect uncommitted changes to a file

**Files:**
- Create: `internal/gitguard/gitguard.go`
- Test: `internal/gitguard/gitguard_test.go`

**Step 1: Write the failing test**

Create `internal/gitguard/gitguard_test.go`:

```go
package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirty(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")

	// Committed + unmodified => clean.
	write(t, filepath.Join(repo, "a.txt"), "v1\n")
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-qm", "add a")
	if dirty, err := Dirty(repo, "a.txt"); err != nil || dirty {
		t.Errorf("committed file: dirty=%v err=%v, want clean", dirty, err)
	}

	// Modified after commit => dirty.
	write(t, filepath.Join(repo, "a.txt"), "v2 local edit\n")
	if dirty, err := Dirty(repo, "a.txt"); err != nil || !dirty {
		t.Errorf("modified file: dirty=%v err=%v, want dirty", dirty, err)
	}

	// Untracked existing file => dirty.
	write(t, filepath.Join(repo, "b.txt"), "new\n")
	if dirty, err := Dirty(repo, "b.txt"); err != nil || !dirty {
		t.Errorf("untracked file: dirty=%v err=%v, want dirty", dirty, err)
	}

	// Non-existent file => clean (nothing to clobber).
	if dirty, err := Dirty(repo, "missing.txt"); err != nil || dirty {
		t.Errorf("missing file: dirty=%v err=%v, want clean", dirty, err)
	}
}
```

**Step 2: Run the test to verify it fails**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/gitguard/...
```

Expected: FAIL — `undefined: Dirty`.

**Step 3: Write the minimal implementation**

Create `internal/gitguard/gitguard.go`:

```go
// Package gitguard reports whether a file in a project has uncommitted changes,
// so sync can refuse to overwrite unsaved work.
package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Dirty reports whether relPath inside projectRoot has uncommitted modifications
// or is an untracked existing file. A committed-and-unmodified file, or a file
// that does not exist, is clean (false). Errors from git are returned.
func Dirty(projectRoot, relPath string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(projectRoot, relPath)); os.IsNotExist(err) {
		return false, nil
	}
	cmd := exec.Command("git", "status", "--porcelain", "--", relPath)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	// Any porcelain output for the path means it differs from HEAD/index or is
	// untracked. No output means clean.
	return strings.TrimSpace(string(out)) != "", nil
}
```

**Step 4: Run the test to verify it passes**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/gitguard/...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/gitguard/
git commit -m "feat: gitguard detects uncommitted changes to a file"
```

---

## Task 2: assets — enumerate source→destination mappings

**Files:**
- Create: `internal/assets/assets.go`
- Test: `internal/assets/assets_test.go`

An `Asset` is one file to copy: its absolute source path and its project-relative destination. `Plan` walks the harness's `core` and selected `profiles` and applies the placement table.

**Step 1: Write the failing test**

Create `internal/assets/assets_test.go`:

```go
package assets

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/patrickserrano/harness/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlan(t *testing.T) {
	h := t.TempDir()
	// core assets
	write(t, filepath.Join(h, "core", "skills", "git.md"), "x")
	write(t, filepath.Join(h, "core", "commands", "sync-worktree.md"), "x")
	// ios profile assets
	write(t, filepath.Join(h, "profiles", "ios", "skills", "build.md"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "config", ".swiftlint.yml"), "x")

	cfg := &config.Config{
		Components: []config.Component{
			{Path: "ios", Profiles: []string{"ios"}},
		},
	}

	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Map dest -> src for assertion.
	dests := map[string]string{}
	for _, a := range got {
		dests[a.Dest] = a.Src
	}
	want := []string{
		filepath.Join(".claude", "skills", "git.md"),
		filepath.Join(".claude", "commands", "sync-worktree.md"),
		filepath.Join(".claude", "skills", "build.md"),
		filepath.Join(".github", "workflows", "ios-ci.yml"),
		filepath.Join("ios", ".swiftlint.yml"),
	}
	var gotDests []string
	for d := range dests {
		gotDests = append(gotDests, d)
	}
	sort.Strings(want)
	sort.Strings(gotDests)
	if len(gotDests) != len(want) {
		t.Fatalf("got %d assets %v, want %d %v", len(gotDests), gotDests, len(want), want)
	}
	for i := range want {
		if gotDests[i] != want[i] {
			t.Errorf("dest[%d] = %q, want %q", i, gotDests[i], want[i])
		}
	}
	// Source paths must be absolute and exist.
	for _, a := range got {
		if !filepath.IsAbs(a.Src) {
			t.Errorf("src not absolute: %q", a.Src)
		}
	}
}
```

**Step 2: Run the test to verify it fails**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/assets/...
```

Expected: FAIL — `undefined: Plan`.

**Step 3: Write the minimal implementation**

Create `internal/assets/assets.go`:

```go
// Package assets enumerates the whole-file assets (skills, commands, CI
// workflows, stack configs) that sync copies from the harness into a project,
// applying the design's placement rules.
package assets

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/patrickserrano/harness/internal/config"
)

// Asset is one file to copy: an absolute source path and a project-relative
// destination path.
type Asset struct {
	Src  string
	Dest string
}

// Plan returns every asset to copy for core plus the profiles named by the
// project's components. Skills/commands/workflows are root-scoped (deduped by
// destination across profiles); config is copied into each component that lists
// the owning profile.
func Plan(harnessRoot string, cfg *config.Config) ([]Asset, error) {
	var out []Asset
	seen := map[string]bool{}

	add := func(src, dest string) {
		if seen[dest] {
			return
		}
		seen[dest] = true
		out = append(out, Asset{Src: src, Dest: dest})
	}

	// core: skills + commands -> root .claude/
	for _, kind := range []string{"skills", "commands"} {
		if err := walkInto(filepath.Join(harnessRoot, "core", kind),
			func(src, rel string) { add(src, filepath.Join(".claude", kind, rel)) }); err != nil {
			return nil, err
		}
	}

	// distinct profiles across all components
	profiles := map[string]bool{}
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			profiles[p] = true
		}
	}

	for p := range profiles {
		base := filepath.Join(harnessRoot, "profiles", p)
		for _, kind := range []string{"skills", "commands"} {
			if err := walkInto(filepath.Join(base, kind),
				func(src, rel string) { add(src, filepath.Join(".claude", kind, rel)) }); err != nil {
				return nil, err
			}
		}
		// workflows -> .github/workflows/<p>-<file> (stack-prefixed; flat)
		if err := walkInto(filepath.Join(base, "workflows"),
			func(src, rel string) {
				add(src, filepath.Join(".github", "workflows", p+"-"+filepath.Base(rel)))
			}); err != nil {
			return nil, err
		}
	}

	// config -> each component dir that lists the owning profile
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			if err := walkInto(filepath.Join(harnessRoot, "profiles", p, "config"),
				func(src, rel string) { add(src, filepath.Join(c.Path, rel)) }); err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

// walkInto calls fn(absSrc, relPath) for every file under dir. A missing dir is
// not an error (a profile need not define every asset kind).
func walkInto(dir string, fn func(src, rel string)) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		fn(abs, rel)
		return nil
	})
}
```

**Step 4: Run the test to verify it passes**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/assets/...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/assets/
git commit -m "feat: enumerate whole-file assets with placement rules"
```

---

## Task 3: assets.Copy — preflight git guard, then copy

**Files:**
- Modify: `internal/assets/assets.go`
- Test: `internal/assets/assets_test.go:append`

`Copy` first checks **every** destination for dirtiness (so a single dirty file aborts the whole asset sync with a complete conflict list, writing nothing), then copies each asset, confining destinations with `safepath` and creating parent dirs.

**Step 1: Write the failing test**

Append to `internal/assets/assets_test.go`:

```go
import (
	// add to the existing import block:
	"os/exec"
	"strings"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestCopyWritesAssets(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "core", "skills", "git.md"), "SKILL BODY")

	cfg := &config.Config{}
	plan, err := Plan(h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "git.md"))
	if err != nil {
		t.Fatalf("asset not written: %v", err)
	}
	if string(got) != "SKILL BODY" {
		t.Errorf("content = %q", got)
	}
}

func TestCopyRefusesDirtyTarget(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	write(t, filepath.Join(h, "core", "commands", "build.md"), "NEW")

	// Pre-create the destination with a local edit and commit, then dirty it.
	dest := filepath.Join(project, ".claude", "commands", "build.md")
	write(t, dest, "committed\n")
	gitInit(t, project)
	cmd := exec.Command("git", "commit", "-qm", "init")
	cmd.Dir = project
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	write(t, dest, "LOCAL UNSAVED EDIT\n") // now dirty

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = Copy(project, plan)
	if err == nil {
		t.Fatal("expected Copy to refuse dirty target, got nil")
	}
	if !strings.Contains(err.Error(), "build.md") {
		t.Errorf("error should name the dirty file: %v", err)
	}
	// The local edit must be preserved.
	got, _ := os.ReadFile(dest)
	if string(got) != "LOCAL UNSAVED EDIT\n" {
		t.Errorf("dirty target was overwritten: %q", got)
	}
}
```

**Step 2: Run the test to verify it fails**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/assets/...
```

Expected: FAIL — `undefined: Copy`.

**Step 3: Write the minimal implementation**

Add to `internal/assets/assets.go` (and add imports `fmt`, the `gitguard` and `safepath` packages):

```go
// Copy preflights every asset destination for uncommitted changes; if any are
// dirty it aborts with the full list and writes nothing. Otherwise it copies
// each asset, confining the destination within projectRoot.
func Copy(projectRoot string, plan []Asset) error {
	var dirty []string
	for _, a := range plan {
		isDirty, err := gitguard.Dirty(projectRoot, a.Dest)
		if err != nil {
			return fmt.Errorf("git guard %s: %w", a.Dest, err)
		}
		if isDirty {
			dirty = append(dirty, a.Dest)
		}
	}
	if len(dirty) > 0 {
		return fmt.Errorf("refusing to overwrite uncommitted changes in:\n  %s\n(commit or stash them, then re-run)",
			strings.Join(dirty, "\n  "))
	}

	for _, a := range plan {
		target, err := safepath.Resolve(projectRoot, a.Dest)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", a.Dest, err)
		}
		if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", a.Dest)
		}
		data, err := os.ReadFile(a.Src)
		if err != nil {
			return fmt.Errorf("read asset %s: %w", a.Src, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
```

Update the import block of `internal/assets/assets.go` to include:

```go
import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/gitguard"
	"github.com/patrickserrano/harness/internal/safepath"
)
```

**Step 4: Run the test to verify it passes**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/assets/...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/assets/
git commit -m "feat: copy assets with preflight git guard and path confinement"
```

---

## Task 4: Wire asset sync into sync.Run

**Files:**
- Modify: `internal/sync/sync.go`
- Test: `internal/sync/sync_test.go:append`

After the CLAUDE.md region phase, run the asset phase. Reuse the already-loaded `cfg`.

**Step 1: Write the failing test**

Append to `internal/sync/sync_test.go`:

```go
import (
	// add to the existing import block:
	"os/exec"
)

func TestSyncCopiesAssets(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()

	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "core", "skills", "git.md"), "GIT SKILL")

	// init project as a git repo (gitguard needs one)
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")

	if err := Run(harness, project); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "git.md"))
	if err != nil {
		t.Fatalf("skill asset not synced: %v", err)
	}
	if string(got) != "GIT SKILL" {
		t.Errorf("content = %q", got)
	}
}
```

**Step 2: Run the test to verify it fails**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/sync/... -run Assets
```

Expected: FAIL — the asset is not synced (no `.claude/skills/git.md`).

**Step 3: Write the minimal implementation**

In `internal/sync/sync.go`, add the import and a final asset phase to `Run`. Add `"github.com/patrickserrano/harness/internal/assets"` to the import block. Then, just before `return nil` at the end of `Run`:

```go
	// Phase 2: whole-file assets (skills, commands, workflows, configs).
	plan, err := assets.Plan(harnessRoot, cfg)
	if err != nil {
		return fmt.Errorf("plan assets: %w", err)
	}
	if err := assets.Copy(projectRoot, plan); err != nil {
		return err
	}

	return nil
```

(Replace the existing bare `return nil` at the end of `Run` with the block above.)

**Step 4: Run the test to verify it passes**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/sync/...
```

Expected: PASS (asset test plus all prior sync tests).

**Step 5: Run the full suite**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./...
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/sync/
git commit -m "feat: sync whole-file assets after CLAUDE.md regions"
```

---

## Task 5: CLI reporting + smoke test

**Files:**
- Modify: `internal/sync/sync.go` (return a small summary)
- Modify: `cmd/harness/main.go` (print it)
- Test: `internal/sync/sync_test.go` (assert the count)

**Step 1: Write the failing test**

Change the signature expectation: `Run` should return `(Result, error)` where `Result` carries counts. Append to `internal/sync/sync_test.go`:

```go
func TestRunReportsCounts(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "core", "skills", "git.md"), "S")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"), "[project]\nname=\"x\"\n")

	res, err := Run(harness, project)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Regions < 1 || res.Assets != 1 {
		t.Errorf("Result = %+v, want Regions>=1 Assets=1", res)
	}
}
```

> Note: this changes `Run`'s signature, so the other sync tests that call `Run(...)` for a single `error` must be updated to `_, err := Run(...)`. Update them in this step.

**Step 2: Run the test to verify it fails**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./internal/sync/...
```

Expected: FAIL — compile error (Run returns one value) until you update the signature and the other call sites.

**Step 3: Write the minimal implementation**

In `internal/sync/sync.go`:
- Define `type Result struct{ Regions, Assets int }`.
- Change `func Run(...) error` to `func Run(...) (Result, error)`.
- Count each successful `mergeInto` as a region; set `Assets = len(plan)`.
- Return `Result{}, err` on every error path; `Result{Regions: n, Assets: len(plan)}, nil` at the end.

In `cmd/harness/main.go`, update the `sync` case:

```go
	case "sync":
		res, err := syncpkg.Run(harnessRoot, projectRoot)
		if err != nil {
			fail(err)
		}
		fmt.Printf("sync complete: %d regions, %d assets\n", res.Regions, res.Assets)
```

Update the other `Run(...)` call sites in `sync_test.go` to `_, err := Run(...)`.

**Step 4: Run the test to verify it passes**

```bash
env -u GOROOT /opt/homebrew/bin/go test ./...
```

Expected: PASS.

**Step 5: Manual smoke test**

```bash
env -u GOROOT /opt/homebrew/bin/go build -o bin/harness ./cmd/harness
# seed a workflow + config asset in the ios profile
mkdir -p profiles/ios/workflows profiles/ios/config core/skills
printf 'name: ci\n' > profiles/ios/workflows/ci.yml
printf 'disabled_rules: []\n' > profiles/ios/config/.swiftlint.yml
printf 'GIT SKILL\n' > core/skills/git.md
tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
printf '[project]\nname="smoke"\n\n[[component]]\npath="ios"\nprofiles=["ios"]\n' > "$tmp/.harness.toml"
( cd "$tmp" && HARNESS_ROOT="/Users/patrickserrano/Developer/harness" /Users/patrickserrano/Developer/harness/bin/harness sync )
echo "--- tree ---"; find "$tmp" -type f -not -path '*/.git/*' | sort
rm -rf "$tmp"
# revert the throwaway seed files
git checkout -- core profiles 2>/dev/null; rm -rf profiles/ios/workflows profiles/ios/config core/skills 2>/dev/null
```

Expected: `.claude/skills/git.md`, `.github/workflows/ios-ci.yml`, and `ios/.swiftlint.yml` all present; sync prints a region+asset count.

**Step 6: Commit**

```bash
git add internal/sync/ cmd/harness/main.go
git commit -m "feat: report region and asset counts from sync"
```

---

## Task 6: Security audit (gate — required before finishing)

Per the harness security discipline (every step includes a security audit), do NOT finish this branch until this gate passes.

**Step 1: Run the security-review skill**

Run `/security-review` on the branch diff (`origin/main..HEAD`). If `origin/HEAD` is unset, first run `git remote set-head origin main`.

**Step 2: Threat-model the new surface explicitly**

The new code shells out to `git` and copies files. Verify each:
- **Argument injection into `git`:** `relPath` reaches `exec.Command("git", "status", "--porcelain", "--", relPath)`. Confirm the `--` separator is present so a path that looks like a flag (e.g. `--foo`) cannot become a git option. Confirm `cmd.Dir` is the trusted `projectRoot` and no shell is involved (no `sh -c`).
- **Asset destination traversal:** every copy destination must pass through `safepath.Resolve` and the final-element symlink guard (mirror the sync hardening). Confirm a malicious profile/config name or a `..` in an asset path cannot escape the project root. Note: profile names are already validated by `config.Load`; confirm asset *file* names from the harness tree (trusted) and component paths (validated) compose safely.
- **Symlinked destination dirs:** confirm `assets.Copy` is covered by the same parent-dir confinement as `sync.mergeInto` (it calls `safepath.Resolve`). Add a test if missing: a symlinked `.claude` or component dir must not let a copy escape.
- **Git guard bypass:** confirm a file that is dirty cannot be silently overwritten (the preflight aborts before any write).

**Step 3: Resolve findings**

Fix every finding at confidence ≥ 8 (write the failing test first, then fix). Re-run `/security-review` until clean. Record any accepted residual risk explicitly in the PR description.

**Step 4: Commit any fixes**

```bash
git add -A
git commit -m "fix: address security audit findings for asset sync"
```

---

## Done — what this milestone delivers

`harness sync` now distributes skills, commands, CI workflows, and stack configs (per the placement rules) in addition to CLAUDE.md regions, refusing to overwrite any file with uncommitted local changes, and confining every write within the project root.

## Known limitations (document in PR)

- **No deletion:** assets removed from the harness are not removed from projects (sync only adds/overwrites). A future `--prune` is out of scope.
- **Asset sync is not atomic across files** (same recoverable-partial-sync property as the region phase); the preflight guard makes destructive partial writes unlikely but a mid-copy I/O error can still leave some assets updated.

## Next plans

1. **Absorb `ios-template`** — split its CLAUDE.md into `core` + `profiles/ios`; move the 23 skills, commands, CI workflows, and configs into `profiles/ios/` so this sync mechanism has real content.
2. **`init` / `scaffold` / `new`**, then **`harvest`**, then **Renovate**.
