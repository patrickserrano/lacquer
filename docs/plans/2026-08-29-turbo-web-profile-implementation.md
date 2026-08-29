# Turborepo-Aware Web Profile Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make `profiles/web/workflows/ci.yml`'s `check` job opt-in turbo-aware — if a component has a `turbo.json`, run each task through `./node_modules/.bin/turbo`; otherwise keep today's flat `pnpm run <script>` behavior unchanged. This lets a pnpm workspace with multiple Next.js apps (e.g. pixelfoxstudio.com's root site + `apps/admin`) be reached by ONE lacquer `web` component, without lacquer ever needing to know about individual packages inside it.

**Architecture:** See `docs/plans/2026-08-28-turborepo-web-profile-design.md` for the validated design. This plan implements exactly that design: `turbo.json` is never synced (project-authored), the CI branch is `if [ -f turbo.json ]`, and every turbo invocation uses the local binary (`./node_modules/.bin/turbo`) — never `pnpm dlx`/`npx` — per the repo's resolver-dispatch ban in `internal/shipped/shipped_test.go`.

**Tech Stack:** Go 1.23 — build/test with `env -u GOROOT /opt/homebrew/bin/go`. This is content-only work in `profiles/web/` plus a hardening addition to `internal/shipped`'s shipped-content linter; no change to `internal/config`, `internal/assets`, `internal/sync`, or `internal/tokens`.

---

## Task 1: Harden the resolver-dispatch ban to cover `turbo`

**Why first:** The design's `./node_modules/.bin/turbo` choice exists specifically because this repo already bans `pnpm dlx <tool>`/`npx <tool>` for `biome`, `typedoc`, `vitest`, `tsc` (see `internal/shipped/shipped_test.go`'s "resolver-dispatched invocation of a project dependency" ban, born from a real incident: `sleevetap` had a passing Biome step that was silently running an unpinned global binary). `turbo` is the same class of tool and isn't in that list yet — add it now so a future accidental `pnpm dlx turbo` in this file (or any profile) fails loudly, the same way a stray `npx biome` would.

**Files:**
- Modify: `internal/shipped/shipped_test.go:136` (the ban's regex) and its fixture table around `internal/shipped/shipped_test.go:242-250` (`TestResolverBanUnderstandsArgvArraysAndShell`)

**Step 1: Write the failing fixtures**

In `TestResolverBanUnderstandsArgvArraysAndShell`, add one banned case and one allowed case:

```go
	banned := []struct{ name, line string }{
		{"the historical fix.toml line", `argv = ["npx", "--no-install", "biome", "check", "--write", "."]`},
		{"argv with no flag between", `argv = ["npx", "typedoc"]`},
		{"argv pnpm exec", `argv = ["pnpm", "exec", "biome", "check", "--write", "."]`},
		{"argv pnpm dlx", `argv = ["pnpm", "dlx", "typedoc"]`},
		{"shell npx", `        run: npx biome ci .`},
		{"shell npx with a flag between", `        run: npx --yes vitest run`},
		{"shell pnpm exec", `        run: pnpm exec tsc --noEmit`},
		{"a yaml step, which isProse would have exempted", `      - run: npx biome ci .`},
		{"shell pnpm dlx turbo", `        run: pnpm dlx turbo run build`},
	}
```

And in the `allowed` table just below it, add:

```go
		{"the local turbo binary in shell", `        run: ./node_modules/.bin/turbo run build`},
```

**Step 2: Run test to verify the new banned case fails**

Run: `env -u GOROOT /opt/homebrew/bin/go test ./internal/shipped/ -run TestResolverBanUnderstandsArgvArraysAndShell -v`
Expected: `--- FAIL: TestResolverBanUnderstandsArgvArraysAndShell/banned/shell_pnpm_dlx_turbo` — "the ban does not match" (the allowed case passes trivially since the regex not matching `turbo` at all means it's vacuously "allowed").

**Step 3: Extend the regex**

In `internal/shipped/shipped_test.go`, change:

```go
		re: regexp.MustCompile(`(?:\bnpx\b|\bpnpm\b[\s,"']*(?:exec|dlx)\b)[^\n]*?\b(?:biome|typedoc|vitest|tsc)\b`),
```

to:

```go
		re: regexp.MustCompile(`(?:\bnpx\b|\bpnpm\b[\s,"']*(?:exec|dlx)\b)[^\n]*?\b(?:biome|typedoc|vitest|tsc|turbo)\b`),
```

**Step 4: Run test to verify it passes**

Run: `env -u GOROOT /opt/homebrew/bin/go test ./internal/shipped/ -run TestResolverBan -v`
Expected: PASS, both `TestResolverBanUnderstandsArgvArraysAndShell` and `TestResolverBanSeesThroughAWrapperBody`.

Also run the full-content scan to confirm nothing currently shipped trips it:

Run: `env -u GOROOT /opt/homebrew/bin/go test ./internal/shipped/ -run TestShippedContentAvoidsBannedPatterns -v`
Expected: PASS (nothing shipped yet uses `turbo` at all).

**Step 5: Commit**

```bash
git add internal/shipped/shipped_test.go
git commit -m "test(shipped): extend resolver-dispatch ban to cover turbo"
```

---

## Task 2: Add turbo/no-turbo branching to the web CI `check` job

**Files:**
- Modify: `profiles/web/workflows/ci.yml:170-190`
- Create: `internal/shipped/web_turbo_test.go`

**Step 1: Write the failing test**

This mirrors the pattern the (now-removed) iOS `namedStep` helper used: parse the rendered YAML, pull one step's `run:` body by job+step name.

```go
package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/tokens"
	"gopkg.in/yaml.v3"
)

// renderWebCI substitutes profiles/web/workflows/ci.yml the way sync does.
func renderWebCI(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root(t), "profiles", "web", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Demo", Scheme: "Demo", BundleID: "com.x.demo",
		AscAppID: "1", Xcodeproj: "Demo.xcodeproj", SwiftVersion: "6", GithubOrg: "acme",
	}}
	out, missing := tokens.Substitute(string(raw), tokens.Values(cfg, ""))
	if len(missing) > 0 {
		t.Fatalf("unsubstituted tokens: %v", missing)
	}
	return out
}

// webCheckStep returns the `run:` body of one step in the `check` job, by step name.
func webCheckStep(t *testing.T, rendered, step string) string {
	t.Helper()
	var doc struct {
		Jobs map[string]struct {
			Steps []map[string]any `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		t.Fatalf("rendered workflow is not valid YAML: %v", err)
	}
	for _, st := range doc.Jobs["check"].Steps {
		if n, _ := st["name"].(string); n == step {
			run, _ := st["run"].(string)
			if run == "" {
				t.Fatalf("step %q has no run: block", step)
			}
			return run
		}
	}
	t.Fatalf("no step named %q in job check", step)
	return ""
}

// TestWebCIUsesLocalTurboBinaryWhenTurboJSONPresent is the one property this
// milestone adds: a component with turbo.json gets each task run through
// turbo, fanning out across every package in the workspace; a component
// without one keeps today's single-package pnpm invocation untouched.
//
// The local binary only — ./node_modules/.bin/turbo, never `pnpm dlx turbo` —
// for the same reason the Biome step below it uses ./node_modules/.bin/biome:
// see internal/shipped/shipped_test.go's resolver-dispatch ban.
func TestWebCIUsesLocalTurboBinaryWhenTurboJSONPresent(t *testing.T) {
	rendered := renderWebCI(t)
	cases := []struct {
		step     string
		turbo    string
		fallback string
	}{
		{"Lint", "./node_modules/.bin/turbo run lint", "./node_modules/.bin/biome ci --error-on-warnings ."},
		{"Type check", "./node_modules/.bin/turbo run typecheck", "pnpm run typecheck"},
		{"Test (coverage)", "./node_modules/.bin/turbo run test", "pnpm run test:coverage"},
		{"Build", "./node_modules/.bin/turbo run build", "pnpm run build"},
	}
	for _, c := range cases {
		t.Run(c.step, func(t *testing.T) {
			run := webCheckStep(t, rendered, c.step)
			if !strings.Contains(run, "if [ -f turbo.json ]") {
				t.Errorf("step %q does not branch on turbo.json:\n%s", c.step, run)
			}
			if !strings.Contains(run, c.turbo) {
				t.Errorf("step %q does not call %q on the turbo branch:\n%s", c.step, c.turbo, run)
			}
			if !strings.Contains(run, c.fallback) {
				t.Errorf("step %q does not keep %q on the fallback branch:\n%s", c.step, c.fallback, run)
			}
			if strings.Contains(run, "pnpm dlx turbo") || strings.Contains(run, "npx turbo") {
				t.Errorf("step %q dispatches turbo through a resolver instead of the local binary:\n%s", c.step, run)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `env -u GOROOT /opt/homebrew/bin/go test ./internal/shipped/ -run TestWebCIUsesLocalTurboBinaryWhenTurboJSONPresent -v`
Expected: FAIL on all four subtests — "does not branch on turbo.json" (today's `ci.yml` has no such branch at all).

**Step 3: Implement — edit `profiles/web/workflows/ci.yml`**

Replace the block currently at `profiles/web/workflows/ci.yml:170-190`:

```yaml
      - name: Biome
        # The LOCAL binary, not `npx biome`. npx silently downloads and runs an
        # arbitrary biome when the project has none installed, so a project that
        # never added @biomejs/biome still gets a green Biome step — linted by
        # whatever version npm served that minute, against rules nobody chose.
        # sleevetap was in exactly that state: biome.json synced, biome absent
        # from every package.json, Biome step green, and only `lacquer doctor`
        # noticing that the check could not be running at all.
        #
        # This also removes a subtler version drift: with a local install npx
        # prefers it, so the two agreed by luck rather than by construction.
        run: ./node_modules/.bin/biome ci --error-on-warnings .

      - name: Type check
        run: pnpm run typecheck

      - name: Test (coverage)
        run: pnpm run test:coverage

      - name: Build
        run: pnpm run build
```

with:

```yaml
      # A component with turbo.json is a pnpm workspace with more than one app
      # inside it (e.g. a Next.js site plus apps/admin); turbo fans each task
      # out across every package that defines it (packages missing the script
      # are silently skipped — that's turbo's own behavior, not a CI failure).
      # A component without turbo.json is a single app, and keeps running its
      # own package.json scripts exactly as before.
      #
      # ./node_modules/.bin/turbo, never `pnpm dlx turbo`/`npx turbo` — same
      # reason the Biome branch below uses the local binary: see the
      # resolver-dispatch ban in internal/shipped/shipped_test.go.
      - name: Lint
        run: |
          if [ -f turbo.json ]; then
            ./node_modules/.bin/turbo run lint
          else
            ./node_modules/.bin/biome ci --error-on-warnings .
          fi

      - name: Type check
        run: |
          if [ -f turbo.json ]; then
            ./node_modules/.bin/turbo run typecheck
          else
            pnpm run typecheck
          fi

      - name: Test (coverage)
        run: |
          if [ -f turbo.json ]; then
            ./node_modules/.bin/turbo run test
          else
            pnpm run test:coverage
          fi

      - name: Build
        run: |
          if [ -f turbo.json ]; then
            ./node_modules/.bin/turbo run build
          else
            pnpm run build
          fi
```

Note the step comment explaining `--error-on-warnings` (originally above `- name: Biome`, `profiles/web/workflows/ci.yml:157-169`) stays where it is, immediately above the new `- name: Lint` step — it still documents why the fallback branch carries that flag.

**Step 4: Run test to verify it passes**

Run: `env -u GOROOT /opt/homebrew/bin/go test ./internal/shipped/ -run TestWebCIUsesLocalTurboBinaryWhenTurboJSONPresent -v`
Expected: PASS, all four subtests.

Then run the broader shipped-content suite to make sure nothing else regressed:

Run: `env -u GOROOT /opt/homebrew/bin/go test ./internal/shipped/... -v`
Expected: PASS. In particular `TestRenderedWorkflowsAreValidYAML` (the multiline `run: |` blocks must still parse as valid YAML) and `TestShippedContentAvoidsBannedPatterns`.

**Step 5: Commit**

```bash
git add profiles/web/workflows/ci.yml internal/shipped/web_turbo_test.go
git commit -m "feat(web): opt-in turbo.json support in the web CI check job"
```

---

## Task 3: Document the convention in `profiles/web/CLAUDE.web.md`

**Files:**
- Modify: `profiles/web/CLAUDE.web.md` (new subsection after `## CI`, currently at line 295)

**Step 1: Add the subsection**

Append after the existing `## CI` section (`profiles/web/CLAUDE.web.md:295-300`):

```markdown

## Monorepos — Turborepo

A component with more than one app (a Next.js site plus an admin app, a docs
site, etc.) should be a pnpm workspace with a root `turbo.json`, not a second
lacquer component — the lacquer supports one component per profile
(`internal/config/config.go`), so a second web app has no other way to get
CI, hooks, or lint config.

`turbo.json` is **project-authored, not synced** — task graphs and outputs
(`.next/**` vs `dist/**`, DB migrations, …) are genuinely per-project. What
`web-ci.yml` requires is that the task **names** match what it invokes:

| turbo.json task | Runs |
|---|---|
| `lint` | your linter (e.g. `biome ci --error-on-warnings .`) |
| `typecheck` | `tsc --noEmit` |
| `test` | your test runner with coverage |
| `build` | the framework build |

A package inside the workspace that doesn't define one of these scripts is
silently skipped by turbo when that task runs — not a CI failure, so partial
adoption across a workspace's packages is fine.

Without a `turbo.json` at the component root, `web-ci.yml` runs the single-app
path unchanged (the scripts in [Required package.json scripts](#required-packagejson-scripts)
against the one package.json).
```

**Step 2: No test — this is documentation.** Read the rendered section back and confirm it renders correctly as Markdown (visual check, not a Go test).

**Step 3: Commit**

```bash
git add profiles/web/CLAUDE.web.md
git commit -m "docs(web): document the turbo.json monorepo convention"
```

---

## Task 4: End-to-end verification against a real sync

**Step 1: Build the binary**

```bash
H=/Users/patrickserrano/Developer/harness/.worktrees/turbo-web-profile
env -u GOROOT /opt/homebrew/bin/go build -o "$H/bin/lacquer" ./cmd/lacquer
```

**Step 2: Probe both branches**

```bash
H=/Users/patrickserrano/Developer/harness/.worktrees/turbo-web-profile

probe() { # $1 = "with-turbo" or "no-turbo"
  tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
  printf '[project]\nname="p"\n\n[[component]]\npath="."\nprofiles=["web"]\n' > "$tmp/.lacquer.toml"
  if [ "$1" = "with-turbo" ]; then
    printf '{}' > "$tmp/turbo.json"
  fi
  ( cd "$tmp" && LACQUER_ROOT="$H" "$H/bin/lacquer" sync >/dev/null )
  echo "--- $1: check job steps ---"
  grep -A2 'name: Lint$\|name: Type check$\|name: Test (coverage)$\|name: Build$' \
    "$tmp/.github/workflows/web-ci.yml"
  rm -rf "$tmp"
}
probe "no-turbo"    # expect: pnpm run typecheck / pnpm run test:coverage / pnpm run build,
                     # and ./node_modules/.bin/biome — same as before this change
probe "with-turbo"  # expect: identical file (turbo.json presence is a RUNTIME check
                     # inside the `if`, not a sync-time branch) — the point being proven
                     # here is that sync renders ONE workflow either way, and the shell
                     # `if [ -f turbo.json ]` is what picks the branch at CI time.
```

Expect: both probes render the **same** `web-ci.yml` content (the branch is a shell `if`, evaluated when the workflow runs — not something `lacquer sync` picks between). This step exists to confirm `sync` still renders valid, complete output regardless of the target project's turbo status, not to prove two different outputs.

**Step 3: Full suite**

```bash
H=/Users/patrickserrano/Developer/harness/.worktrees/turbo-web-profile
cd "$H" && env -u GOROOT /opt/homebrew/bin/go build ./... && env -u GOROOT /opt/homebrew/bin/go vet ./... && env -u GOROOT /opt/homebrew/bin/go test ./...
```

Expect: all packages `ok`, 0 `FAIL`.

**Step 4: No commit** — this task is verification only, nothing to add.

---

## Done — what this milestone delivers

`profiles/web/workflows/ci.yml`'s `check` job runs every task through
`./node_modules/.bin/turbo` when a component has a `turbo.json`, fanning out
across every package in a pnpm workspace; a single-app component is
unaffected. This makes a second (or third) app inside an existing `web`
component's workspace reachable by the lacquer's existing CI, hooks, and
drift-audit machinery, without any change to `internal/config`'s
one-component-per-profile rule.

## Explicitly not built here (see design doc's "Explicitly deferred" section)

- Multiple components per profile in `internal/config/config.go`.
- Turborepo remote caching.
- A doctor/audit check verifying workspace package scripts match `turbo.json` task names.

## Next

Separate PR, in pixelfoxstudio.com, after this ships and the project runs
`lacquer sync`: add `turbo` devDependency + root `turbo.json`, give
`apps/admin` a `lint` script, add its secrets to `[project].build_env`,
remove the `apps/admin` exclude entry from `.lacquer.toml`.
