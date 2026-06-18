# `harness onboard` Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `harness onboard` command that runs init and — when the project has no git remote — creates a private GitHub repo and wires it as `origin`. Keep `init`/`sync` pure (filesystem-only); `onboard` is the one explicitly-outward command.

**Architecture:** `internal/onboardcmd` orchestrates: ensure `.harness.toml` (reuse `initcmd`), detect whether an `origin` remote exists, and if not (and not `--no-repo`) create a private repo via `gh repo create <org>/<name> --private --source=. --remote=origin`. The `gh` invocation goes through an injectable function var so tests never hit the network. Org defaults to `PixelFoxStudio`, `--private` always. `onboard` does NOT run sync (it would fail closed on the stubbed blank values); it prints next steps.

**Tech Stack:** Go 1.23 — build/test with `env -u GOROOT /opt/homebrew/bin/go`. Requires `gh` (authed) at runtime; tests stub it.

---

## Task 1: onboardcmd — orchestrate init + (no-remote) repo creation

**Files:** Create `internal/onboardcmd/onboardcmd.go`, `internal/onboardcmd/onboardcmd_test.go`.

**Behavior of `Run(projectRoot, org string, createRepo bool) (string, error)`:**
1. If `.harness.toml` is absent, run `initcmd.Run(projectRoot)` to write it (detect components). If present, leave it.
2. If `createRepo` and there is no `origin` remote: resolve repo name (from `[project].name`, else dir basename), then call `ghCreate(projectRoot, org, name)`.
3. Return a human summary (what it did; remind to fill `[project]` values then `harness sync`).
4. If `createRepo` but a remote already exists: skip creation, note it.

**Step 1: Failing tests** — `onboardcmd_test.go` (stub `ghCreate`, use temp git repos):

```go
package onboardcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string, args ...string) {
	t.Helper()
	for _, a := range append([][]string{{"init", "-q"}}, args...) {
		cmd := exec.Command("git", a...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

func mk(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardCreatesRepoWhenNoRemote(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "ShelfLife.xcodeproj", "project.pbxproj"))

	var gotOrg, gotName, gotDir string
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { gotDir, gotOrg, gotName = dir, org, name; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(root, "PixelFoxStudio", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotOrg != "PixelFoxStudio" || gotName != "ShelfLife" || gotDir != root {
		t.Errorf("ghCreate called with dir=%q org=%q name=%q", gotDir, gotOrg, gotName)
	}
	// init wrote a manifest
	if _, err := os.Stat(filepath.Join(root, ".harness.toml")); err != nil {
		t.Errorf(".harness.toml not written: %v", err)
	}
}

func TestOnboardSkipsRepoWhenRemoteExists(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root, []string{"remote", "add", "origin", "git@github.com:x/y.git"})
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))

	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()

	if _, err := Run(root, "PixelFoxStudio", true); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("ghCreate must NOT be called when an origin remote already exists")
	}
}

func TestOnboardNoRepoFlag(t *testing.T) {
	root := t.TempDir()
	gitInit(t, root)
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))
	called := false
	orig := ghCreate
	ghCreate = func(dir, org, name string) error { called = true; return nil }
	defer func() { ghCreate = orig }()
	if _, err := Run(root, "PixelFoxStudio", false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("ghCreate must NOT be called when createRepo is false")
	}
}
```

**Step 2:** RED.

**Step 3: Implement** `onboardcmd.go`:

```go
// Package onboardcmd implements `harness onboard`: init + (when no git remote
// exists) create a private GitHub repo. This is the one harness command with an
// outward side effect; init/sync stay filesystem-only.
package onboardcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/harness/internal/config"
	"github.com/patrickserrano/harness/internal/initcmd"
)

// ghCreate creates a private repo and wires it as origin. Injectable for tests.
var ghCreate = func(dir, org, name string) error {
	cmd := exec.Command("gh", "repo", "create", org+"/"+name,
		"--private", "--source=.", "--remote=origin")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh repo create: %v\n%s", err, out)
	}
	return nil
}

// Run ensures a .harness.toml exists, then (if createRepo and no origin remote)
// creates a private repo under org. It does not sync.
func Run(projectRoot, org string, createRepo bool) (string, error) {
	var out strings.Builder

	manifest := filepath.Join(projectRoot, ".harness.toml")
	if _, err := os.Stat(manifest); os.IsNotExist(err) {
		summary, err := initcmd.Run(projectRoot)
		if err != nil {
			return "", err
		}
		out.WriteString(summary)
		out.WriteString("\n")
	} else if err != nil {
		return "", err
	} else {
		out.WriteString("Using existing .harness.toml\n")
	}

	if createRepo {
		if hasOriginRemote(projectRoot) {
			out.WriteString("Remote 'origin' already exists; skipping repo creation.\n")
		} else {
			name, err := repoName(projectRoot, manifest)
			if err != nil {
				return "", err
			}
			if err := ghCreate(projectRoot, org, name); err != nil {
				return "", err
			}
			fmt.Fprintf(&out, "Created private repo %s/%s and set origin.\n", org, name)
		}
	}

	out.WriteString("Next: fill any blank [project] values in .harness.toml, then run `harness sync`.")
	return out.String(), nil
}

func hasOriginRemote(dir string) bool {
	cmd := exec.Command("git", "remote")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, r := range strings.Fields(string(out)) {
		if r == "origin" {
			return true
		}
	}
	return false
}

// repoName uses [project].name when present, else the project dir's basename.
func repoName(projectRoot, manifest string) (string, error) {
	if cfg, err := config.Load(manifest); err == nil && cfg.Project.Name != "" {
		return cfg.Project.Name, nil
	}
	abs, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Base(abs), nil
}
```

**Step 4:** GREEN; full suite. **Step 5: Commit** — `feat: harness onboard creates a private repo when none exists`.

---

## Task 2: wire `harness onboard` into the CLI (flags)

**Files:** Modify `cmd/harness/main.go`.

**Step 1:** Add an `onboard` case that parses `--org` (default `PixelFoxStudio`) and `--no-repo`, then calls `onboardcmd.Run(projectRoot, org, !noRepo)` and prints the summary. Use a `flag.NewFlagSet("onboard", ...)` over `os.Args[2:]`. Update `usage` to list `onboard` and its flags. Add the `onboardcmd` import and `flag`.

Sketch:
```go
case "onboard":
	fs := flag.NewFlagSet("onboard", flag.ExitOnError)
	org := fs.String("org", "PixelFoxStudio", "GitHub org for repo creation")
	noRepo := fs.Bool("no-repo", false, "do not create a repo even if no remote exists")
	_ = fs.Parse(os.Args[2:])
	summary, err := onboardcmd.Run(projectRoot, *org, !*noRepo)
	if err != nil {
		fail(err)
	}
	fmt.Println(summary)
```

**Step 2:** Build: `env -u GOROOT /opt/homebrew/bin/go build -o bin/harness ./cmd/harness`.

**Step 3: Manual smoke (with --no-repo, so no real repo is created):**
```bash
H=/Users/patrickserrano/Developer/harness
tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
mkdir -p "$tmp/App.xcodeproj"; printf '//\n' > "$tmp/App.xcodeproj/project.pbxproj"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" onboard --no-repo )
echo "--- manifest written? ---"; test -f "$tmp/.harness.toml" && echo yes
rm -rf "$tmp"
```
Expect: writes `.harness.toml`, prints next-step guidance, creates no repo.

**Step 4:** `env -u GOROOT /opt/homebrew/bin/go test ./... && go vet ./...`. **Step 5: Commit** — `feat: wire harness onboard command with --org/--no-repo`.

---

## Task 3: bump VERSION + docs

**Step 1:** No content/profile change, but a new command — bump `printf '7\n' > VERSION` (so the command set is versioned).

**Step 2: Commit** — `feat: bump harness to v7 (onboard command)`.

---

## Task 4: Security audit (gate)

**Step 1:** `/security-review` on `origin/main..HEAD`.

**Step 2:** Threat-model the outward `gh` exec:
- **Command/arg injection:** `ghCreate` uses `exec.Command("gh", ...)` with a list argv (no shell), and `org`/`name` are passed as separate args (not interpolated into a shell string). Confirm `name` (from `[project].name`, charset-validated by config, or dir basename) and `org` (CLI flag, trusted) can't inject. Consider a malicious dir basename (e.g. a dir literally named `--something` or containing shell metachars) — does it reach `gh` as a flag? `org+"/"+name` is one positional arg, so a name like `--x` becomes `PixelFoxStudio/--x` (one token, not a flag). Confirm. If `[project].name` is used, it's charset-validated.
- **No unintended outward action:** confirm `gh` is only ever invoked via the `onboard` command (not `init`/`sync`), only when `createRepo` is true AND no origin remote exists. Verify `init`/`sync` remain network-free.
- **Visibility:** confirm `--private` is always passed (no public-repo path).

**Step 3:** Fix findings ≥ 8 (test-first). **Step 4:** Commit.

---

## Done — what this milestone delivers

`harness onboard` makes onboarding turnkey: it writes the manifest and, for a project with no remote, creates a private GitHub repo — while `init`/`sync` stay filesystem-only and safe.

## Next

(Nice-to-have) scheduled sync automation that re-runs `harness sync` across onboarded projects when the harness version bumps. Then retire `ios-template`; `harvest`; Renovate.
