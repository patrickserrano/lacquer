# Selective tests + weekly full suite — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Cut BILLED CI minutes per PR by running only the tests a diff can
affect, and by collapsing jobs that bill far more than they work. A
staggered weekly full suite is the backstop. Main and every release keep a
full green suite on their exact tree.

**Design (read first, it is the spec):** `docs/plans/2026-10-04-selective-tests.md`.
All decisions D1–D9 were approved 2026-10-04. Where this plan and the design
disagree, the design wins: stop and report to your PM.

**Architecture:** A pure Go function, `selecttests.Select(diff, manifest,
tree)`, sits behind a `lacquer select-tests` subcommand. The workflows run it
as a **step inside the existing `changes` job**, and no new job is ever added
for selection or gating. Templates pass its selectors to `xcodebuild
-only-testing:` / `turbo --filter`. CI OK checks a selection exists and that
what was selected ran. A `schedule:` trigger, with a per-repo slot from
`.lacquer.toml`, runs everything weekly. A console producer turns red or
missing weekly runs into inbox ACTIONs.

**Tech stack:** Go (lacquer), GitHub Actions YAML templates under
`profiles/*/workflows/`, `internal/tokens` substitution, the Go tests in
`internal/shipped` that render and parse templates.

**The metric:** billed minutes per PR, as defined in design §1b: per-job
round-up, Blacksmith vCPU × (0.625 if ARM), Mac counted per job, pi-gate free.
Every unit that changes a workflow reports its before/after on its proof run
in that unit, never in wall clock.

---

## Rules every unit follows (lacquer's CLAUDE.md, restated)

1. **Mutation proof.** For every guard you add, break the implementation and
   confirm a specific NAMED test fails. Restore it, and record
   "mutation → test that caught it" in the PR body under `## Mutations`.
2. **`## Proven on`.** Any change under `profiles/*/workflows/` is proven on
   ONE real repo before merge. Sync the branch's lacquer build into the pilot
   repo on a throwaway branch, open a draft PR there, and paste the run URL
   and its billed minutes into the lacquer PR. CI rejects the PR without it.
   Close the proof PR afterwards: the 24-hour PR cap applies to it too.
3. **`## Fleet dry-run`** for anything that changes a detector or `audit`
   (units 2 and 4): run it against every managed repo, and paste the output.
4. **Byte-identical by default.** A repo that has not opted in, or a code path
   not exercised, renders exactly the workflow it had. Existing
   `internal/shipped` tests encode this. Never loosen or skip a test to pass;
   report it instead.
5. **No new jobs** for selection, gating or the weekly run. Every new runner
   label goes into `internal/shipped/blacksmith_runner_labels_test.go`'s
   table with its reason.
6. **Public repo: nothing fleet-specific (repo names, measured numbers,
   findings about a particular repo) goes in lacquer, not even on a branch.**
   That includes PR bodies, commit messages, test fixtures and code
   comments. Tests use generic fixtures (`CoreKit`, `DataKit`, `Demo`). The
   proof runs, billed before/after and dry-run output go to your PM, who
   files them in the operator's private fleet repo. The lacquer PR says only
   "proven on the pilot repo, run linked in the private record" plus the
   run's URL. Pilot repos are private, so their run URLs reveal nothing to
   the public.
7. Run `go test ./...` before every push. Two CI rounds per PR, through
   `lacquer ci-round begin`. No force-push.

Pilot repos (named in the operator's private brief, not here): one iOS +
supabase repo with two local SPM packages, and one turbo web repo.

---

## Unit 1 — Job consolidation (Sonnet). Ships first, template-only.

Design §1b. This is independent of selection and probably the biggest billed
saving. Measure it alone.

### Task 1.1: Prove pi-gate can run `changes`

**Files:** none (investigation). Record the findings in the PR body.

**Step 1:** On a pilot-repo proof branch, change only `changes.runs-on` to
`[self-hosted, Linux, pi-gate]` in the rendered `ios-ci.yml`. Push, and open
a draft PR.
**Step 2:** From the job log, confirm: `uname -m` is aarch64; the "Fetch the
lacquer" step resolves `linux_arm64` and the binary executes (`lacquer
version` prints); `actions/checkout` with `fetch-depth: 0` completes; and the
time from queue to start is under 30 s.
**Step 3:** Run two PR runs at once (two proof PRs) and confirm both start on
pi-gate without waiting for each other for longer than a run takes.
**Decision rule:** if any check fails, `changes` stays on 2vcpu-arm, and
Tasks 1.2–1.6 still ship. Report the failing check to your PM.

### Task 1.2: `changes` → pi-gate in all three profiles (only if 1.1 passed)

**Files:**
- Modify: `profiles/ios/workflows/ci.yml` (job `changes`, `runs-on`)
- Modify: `profiles/web/workflows/ci.yml` (job `changes`)
- Modify: `profiles/supabase/workflows/ci.yml` (job `changes`; today it is 4vcpu-x64)
- Modify: `internal/shipped/blacksmith_runner_labels_test.go` (remove the
  three `changes` rows from the Blacksmith table)
- Test: a new `internal/shipped/pi_gate_jobs_test.go`

**Step 1: Write the failing test.** `TestCoordinationJobsRunOnPiGate`. Render
each profile's `ci.yml` (use `renderIOSWorkflow`, and add web/supabase
equivalents next to it if they don't exist). Parse the YAML, and assert
`jobs.changes.runs-on == [self-hosted, Linux, pi-gate]` and
`jobs.ci-ok.runs-on` the same. Guard the guard: fail if fewer than 3
workflows were checked.
**Step 2:** `go test ./internal/shipped -run TestCoordinationJobsRunOnPiGate`.
Expected: FAIL, naming the 3 `changes` jobs.
**Step 3:** Change the three `runs-on` values. Rewrite the cost comment above
each `changes` job: pi-gate is free, so the per-run cost goes from 1.25 (or
4 on supabase) to 0. Keep the reason ARM was chosen, because
pi-gate is ARM too.
**Step 4:** Run the test (PASS), then `go test ./...`. The label-table test
now reports the `changes` rows as stale: remove them.
**Step 5:** Mutation: put supabase `changes` back on `x64_4`, confirm
`TestCoordinationJobsRunOnPiGate` fails, then restore.
**Step 6:** Commit: `ci(profiles): run changes on pi-gate (free) instead of Blacksmith`.

### Task 1.3: Fold iOS `baseline` into `lint`

**Files:**
- Modify: `profiles/ios/workflows/ci.yml`: move the `baseline` job's steps
  ("Create Secrets.xcconfig", "Generate Xcode project", "Detect Xcode
  project", "Read the baseline relaxations", "Assert the project baseline")
  to the end of `lint`. Delete the `baseline` job. Remove `baseline` from
  `ci-ok.needs`, the echo line and the result loop.
- Modify: any `internal/shipped` test that names the `baseline` job (`git grep -n '"baseline"' internal/shipped`).
- Test: `internal/shipped/ios_ci_job_count_test.go` (new)

**Watch:** `lint` and `baseline` have different `if:` conditions today.
`baseline` runs on `code == 'true'`. Lint has the same condition, but its
early steps skip when there are no Swift sources ("Detect Swift sources").
The baseline steps must keep running when a repo has no Swift files yet,
since a Phase-0 project still needs its baseline asserted. Give each moved
step its original condition, and test that case.

**Step 1: Write the failing tests.**
`TestIOSCIHasNoBaselineJob`: the rendered workflow has no `jobs.baseline`,
and `jobs.lint.steps` contains a step named "Assert the project baseline".
`TestCIOKNeedsOnlyRealJobs`: every `ci-ok.needs` entry exists as a job, and
every job other than `ci-ok` is in `ci-ok.needs`. This catches a
dangling-`needs` YAML error, which GitHub reports only at run time.
**Step 2:** Run them: FAIL.
**Step 3:** Move the steps. Keep the job id `lint` and the name `Lint`:
at least one repo's branch protection names checks, so the name must survive.
**Step 4:** `go test ./...` PASS. Mutation: leave `baseline` in
`ci-ok.needs` and confirm `TestCIOKNeedsOnlyRealJobs` fails.
**Step 5:** Commit: `ci(ios): fold Baseline into Lint (one Mac job per run instead of two)`.

### Task 1.4: Right-size supabase jobs and merge the DB jobs

**Files:**
- Modify: `profiles/supabase/workflows/ci.yml`:
  - `check` (Format · Lint · Test): `blacksmith-4vcpu-ubuntu-2404` → `blacksmith-2vcpu-ubuntu-2404-arm`.
    First confirm every tool it installs has an arm64 Linux build (Deno,
    the Supabase CLI if used). If one doesn't, use `blacksmith-2vcpu-ubuntu-2404`
    (x64, 2 per minute) and say why in the label table.
  - `lint-database` + `test-database` → ONE job `database` (name
    `DB Lint · Tests`): start the local stack once, run Splinter, then pgTAP,
    then stop the stack. Keep `if: needs.changes.outputs.db == 'true'`. Keep
    the pgTAP relaxation read. Update `deploy-database.needs` and `ci-ok.needs`.
  - `deploy-database`: 4vcpu-x64 → `blacksmith-2vcpu-ubuntu-2404` (the
    Supabase CLI stays x64, as the existing comment requires).
- Modify: `profiles/supabase/workflows/health.yml` job `ping` → `[self-hosted, Linux, pi-gate]` (it is a curl).
- Modify: `internal/shipped/blacksmith_runner_labels_test.go` rows, plus `internal/shipped/db_gate_test.go` wherever it names the old jobs.
- Test: extend `TestCIOKNeedsOnlyRealJobs` to supabase; new `TestSupabaseStartsOneLocalStackPerRun` (counts steps that run `supabase start`; must be 1).

Steps follow 1.2's TDD shape: failing test, change, pass, mutation (two
`supabase start` steps → the named test fails), commit
`ci(supabase): right-size runners, one local stack per run`.
Prove on the pilot repo's supabase component, with one PR that touches
`supabase/` so the DB job really runs.

### Task 1.5: Web `dependency-review` and `env-validation` become steps in `changes`

**Files:**
- Modify: `profiles/web/workflows/ci.yml` job `changes`: add two steps at the
  end:
  - "Dependency review" (`actions/dependency-review-action`, same pin and
    config as `dependency-review.yml`), only when a manifest or lockfile
    changed (`package.json`, `pnpm-lock.yaml`, `package-lock.json`,
    `yarn.lock`). This needs `pull-requests: read`/`contents: read` on that
    job only; check what the action requires and grant no more.
  - "Validate .env.example against schema": the body of `env-validation.yml`,
    only when `.env.example` or `.env.schema` changed.
  Both report through outputs (`dep_review`, `env_valid`), the same way
  `drift` does, and CI OK fails on anything but `pass` or empty.
  `changes` must never fail outright (see the drift comment).
- Retire: `profiles/web/workflows/dependency-review.yml` and `env-validation.yml`.
  Use lacquer's retire mechanism so `sync` deletes them from repos
  (`internal/retire`; read how a past retired workflow was done:
  `git log --oneline -S'retire' -- internal/retire`).
- Tests: `internal/shipped/web_ci_folded_checks_test.go`: both steps exist in
  `changes`; CI OK reads both outputs; the two files are listed as retired.
  Mutation: CI OK ignoring `dep_review` → the named test fails.

Commit: `ci(web): dependency review and env validation run inside changes`.

### Task 1.6: Release's small jobs → pi-gate

**Files:** `profiles/ios/workflows/release.yml`: `verify-ci-provenance`,
`select-products` and `notify-on-failure` → `[self-hosted, Linux, pi-gate]`.
Merge `verify-ci-provenance` and `select-products` into ONE job only if no
`needs:` consumer breaks (`build-and-deploy` needs both; give the merged job
both outputs). `internal/shipped/release_provenance_test.go` must still pass
unchanged. That is the proof provenance behaviour didn't move.
Commit: `ci(release): coordination jobs on pi-gate`.

### Task 1.7: Prove, measure, PR

- Sync the branch build into the pilot iOS+supabase repo and the pilot web
  repo (throwaway branches). Open one draft PR each that touches code, so
  every job runs.
- `## Proven on` in the lacquer PR: the run URLs only. The billed table,
  before and after per run, goes to your PM, never into lacquer. "Before" is the pilot's last 10 PR runs on main's
  templates; "after" is the proof runs. Compute billed with the design §1b
  rules. Use cached JSON, `per_page=100`, and stop at 1,000 remaining rate
  limit.
- Re-run `lacquer protection --roster <roster>` and paste its output: no
  required check name may disappear.

---

## Unit 2 — The selection engine (Opus)

Design §2, §5. Pure Go, no workflow changes in this unit.

### Task 2.1: Package skeleton and types

**Files:**
- Create: `internal/selecttests/selecttests.go`
- Create: `internal/selecttests/selecttests_test.go`
- Create: `internal/selecttests/testdata/` (fixture trees)

```go
// Package selecttests maps a diff to the tests it can affect. It is a pure
// function of (changed paths, manifest, files read from the head tree): the
// same inputs give the same Result every time, and every rule fails toward
// running MORE.
package selecttests

type Mode string

const (
	ModeFull     Mode = "full"
	ModeSelected Mode = "selected"
	ModeNone     Mode = "none" // ONLY when every changed path is no-test (docs, agent tooling)
)

type Result struct {
	Mode      Mode     `json:"mode"`
	Selectors []string `json:"selectors"` // -only-testing target names (iOS) or package filters (web); for full: the complete list
	Reasons   []string `json:"reasons"`   // one per decision, e.g. "CoreKit/Sources/A.swift → CoreKit (+ dependents DataKit, app)"
}

// Change is one changed path. A rename carries both sides; both are classified.
type Change struct {
	Path    string
	OldPath string // set for renames
	Status  string // A, M, D, R
}

// Tree reads files from the HEAD tree (a fs.FS in tests, git show in the CLI).
type Tree interface{ ReadFile(path string) ([]byte, error) }
```

### Task 2.2: Fail-safe and no-test classification (TDD, table-driven)

**Test first:** `TestClassify`. Columns: path → `noTest | failSafe | unclaimed`.
It covers every entry of design §2 rules 3 and 4, including nested component
prefixes (`App/Foo.xcodeproj/project.pbxproj`), `docs/x.md` (no-test),
`.claude/skills/a/SKILL.md` (no-test), `.github/workflows/ios-ci.yml`
(fail-safe), `CoreKit/Tests/Support/Fixtures/a.json` (fail-safe: shared test
helpers), `Weird/new.thing` (unclaimed, which later becomes FULL).
**Implement:** two ordered regexp lists, `noTestPatterns` and
`failSafePatterns`, plus the manifest's `[tests].full_on` appended to
failSafe. Fail-safe wins over no-test, so a `.md` under `.github/` is
fail-safe.
**Mutation:** delete the `project.yml` pattern; `TestClassify/project.yml`
must fail by name.

### Task 2.3: Local package graph

**Test first:** `TestPackageGraph`, using the fixture
`testdata/two-packages/` (`CoreKit/Package.swift` with no local deps;
`DataKit/Package.swift` with `.package(path: "../CoreKit")`; app sources in
`App/`). Expect units `{CoreKit: [CoreKitTests], DataKit: [DataKitTests]}`
and reverse edges `CoreKit → {DataKit}`.
**Implement:** find every `Package.swift` in the tree under the component.
Extract `.package(path: "...")` with a regexp, resolve it relative to the
manifest dir, and map test targets to packages by the location of
`extra_test_targets` sources (`<pkg>/Tests/<Target>/`). A Package.swift that
cannot be parsed puts the whole Result in FULL with reason "unparsable
Package.swift: <path>". Never a guess.

### Task 2.4: `Select` for iOS

**Tests first** (`TestSelectIOS`). Each case asserts the exact `Result` (mode,
sorted selectors, reasons non-empty):

| case | diff | expected |
|---|---|---|
| package source | `CoreKit/Sources/A.swift` | selected: CoreKitTests, DataKitTests, + app targets (test_target, ui_test_target, non-package extras, watch) |
| package tests only | `CoreKit/Tests/CoreKitTests/ATests.swift` | selected: CoreKitTests |
| app only | `App/Feature/View.swift` | selected: app targets only, no package tests |
| app tests only | `AppTests/FooTests.swift` | selected: the app test target that owns it (by directory = target name), else all app targets |
| docs + agent tooling | `README.md`, `.claude/x` | none |
| fail-safe | `App.xcodeproj/project.pbxproj` | full |
| unmapped | `Weird/new.thing` | full |
| mixed no-test + package | `README.md`, `DataKit/Sources/B.swift` | selected: DataKitTests + app |
| deleted test target dir | `D CoreKit/Tests/CoreKitTests/ATests.swift` (last file) | full ("test target may have been removed") |
| rename across packages | `R CoreKit/Sources/A.swift → DataKit/Sources/A.swift` | selected: union of both sides' selections |
| empty computed set with code changes | an override map that selects nothing | full ("selection empty") |
| `not_run_in_ci` target | its directory changed | that target is never a selector (honour config.NotRunInCI), the rest as normal |

**Implement** in this order, and do not reorder:
1. Any fail-safe or unclaimed path → return FULL.
2. Drop no-test paths. If nothing is left → NONE.
3. For each remaining path: package tests dir → that package's tests;
   package other → its tests + transitive dependents' tests + all app
   targets; app-side → app targets.
4. Union, sort, dedupe. Empty → FULL.

FULL's selector list is exactly what `config.Product.TestSelectors()` and
`WatchTestSelectors()` give today, so FULL is byte-identical to the current
`-only-testing:` list.

**Mutation guard test:** `TestSelectFailSafeIsLive`. It runs the "unmapped"
and "fail-safe" cases with `failSafePatterns` emptied through an unexported
test hook, and asserts they now return something other than FULL. This
proves the FULL assertions above can fail. Record it under `## Mutations`.

### Task 2.5: `Select` for web

**Tests first:** `TestSelectWeb`. A root file (`package.json`,
`pnpm-lock.yaml`, `turbo.json`, `tsconfig.base.json`, `biome.json`, root
`vitest.config.*`, `.env.schema`) → FULL. A change inside `apps/admin/` →
selected `["...[<base>]"]`: a single filter, letting turbo/pnpm compute
dependents. Docs-only → NONE.
**Implement:** web's Result carries the filter, not package names. The graph
is turbo's job, and duplicating it is how they'd disagree.

### Task 2.6: CLI `lacquer select-tests`

**Files:** `cmd/lacquer/selecttests.go`, `cmd/lacquer/selecttests_test.go`,
plus the dispatch in `cmd/lacquer/main.go` (add `case "select-tests":` next
to `case "ci-round":`).

```
lacquer select-tests --base <sha> --head <sha> --profile ios|web [--component <path>] [--manifest .lacquer.toml]
```
It reads `git diff --name-status -M <base>...<head>` (three-dot, as `changes`
does) and the manifest, and reads files with `git show <head>:<path>`. It
prints the Result JSON to stdout. Exit codes: 0 means a Result was printed;
anything else means none was, and the workflow treats that as FULL. Tests
use a temp git repo (see `internal/gittest`) with two commits.

**Fleet dry-run (required):** for every managed repo, run `select-tests`
over each of its last 20 merged PRs (`base = merge-base`, `head = PR head`,
read from local clones, no API). In the lacquer PR, `## Fleet dry-run` gives
only anonymized totals ("N repos, M PRs: full X / selected Y / none Z; 0
unexpected none"). The per-repo output goes to your PM. Any `none` for a PR
that touched a non-docs file is a bug.

Commit, then open the PR. Opus reviews the reasons strings: they are what a
reviewer sees in CI.

---

## Unit 3 — Wiring + the gate (Opus)

Design §2 (Wiring), §6, D3 and D9. Depends on Unit 2 being merged and released.

### Task 3.1: Selection step in `changes` (iOS, web)

**Files:** `profiles/ios/workflows/ci.yml`, `profiles/web/workflows/ci.yml`.

- The lacquer binary download in `changes` becomes unconditional; it is
  still one download.
- New step "Select tests" (id `select`). On `pull_request` it runs
  `lacquer select-tests --base $base --head $head --profile ios`. On `push`,
  `schedule` and `workflow_dispatch` it writes
  `{"mode":"full",...}` without computing (push-to-main is Unit 5's).
  Outputs: `mode`, `selectors` (newline-separated, the same separator
  `IOSCIExtraTestSetup` uses), and `selection_ok=true` only when the JSON
  parsed.
- **Any failure of the step → mode=full.** The step must not fail the job
  (the same reasoning as drift).
- Upload `test-selection.json` as an artifact (`retention-days: 30`), holding
  `{pr, base, head, tree: git rev-parse HEAD^{tree}, mode, selectors}`. Unit 6
  reads it.
- D3: extend the existing `code` filter's no-test deny-list with the
  agent-tooling paths, so `code=false` for them. Same list as `noTestPatterns`;
  add a test that the YAML regexp and the Go list agree, path by path, over
  the classifier's fixtures.

### Task 3.2: Test job consumes the selection

- Replace `{{IOS_CI_ONLY_TESTING}}` at the call site with an array built from
  `needs.changes.outputs.selectors`. Reuse the bash-array construction from
  `CIExtraTestSetup`, because target names contain spaces. In FULL mode the
  array must equal today's rendered list: add a test that renders both and
  compares.
- "Verify Test Selectors Matched" renders for EVERY iOS product (D9). Its
  heredoc reads the same selectors the job used. Update
  `internal/tokens/tokens.go` `CIVerifySelectors` and the tests that assert
  it is absent when there are no extras: those assertions change on purpose,
  so state that in the PR.
- Web: the "Test (coverage)" step runs `turbo run test --filter="$FILTER"`
  when mode=selected, today's command when full, and is skipped when none.
  The pnpm-workspace branch uses the same filter.

### Task 3.3: CI OK asserts (D9)

**Files:** `ci-ok` in all three profiles. New test file
`internal/shipped/ci_ok_selection_gate_test.go`.

New checks, in this order, each with its own `::error::` line:
1. `code == true` and `selection_ok != true` → fail ("no selection was computed").
2. `code == true` and `mode == none` → fail.
3. `code == true` and `needs.test.result == skipped` → fail. This is the
   newly closed hole: today a skip passes.
4. Print mode, selectors and reasons to `$GITHUB_STEP_SUMMARY` under "Tests
   NOT run on this PR" (FULL minus selected).

Tests: render and parse, then assert each condition appears in the CI OK
script. Then a **behavioural test**: extract the CI OK `run:` script and
execute it with bash and a fake `needs` context (substitute the `${{ }}`
expressions with literal strings). Cover `code=true, test=skipped` → exit 1;
`code=false, test=skipped` → exit 0; `mode=none, code=true` → exit 1.
Mutation: drop check 3 → the behavioural test fails by name.

### Task 3.4: web pre-push hook (#507 fold-in)

The web profile's pre-push hook calls `lacquer select-tests --profile web
--base origin/main --head HEAD`, runs nothing for NONE (docs-only) and the
filter for selected. Test it through real git (a linked worktree, real
`git push` to a bare remote), following `internal/shipped/hooks_under_git_test.go`.

### Task 3.5: Prove, measure, PR

Prove on both pilots with three PRs each, run one at a time: docs-only, an
app-only change, and a package change (iOS) / a single-app change (web).
Paste each run's mode, selectors and billed minutes. On the pilot iOS repo,
answer the design's open question: did `Run Tests` shrink on the app-only
PR? Compare the step's minutes with the "before" median.

---

## Unit 4 — Weekly slots (Sonnet)

Design §3 and D6.

### Task 4.1: Config `[weekly]`

**Files:** `internal/config/config.go`: a `Weekly struct { Slot string \`toml:"slot"\`; LinuxSlot string \`toml:"linux_slot"\` }`
on Config. Validate the form `^(Mon|Tue|Wed|Thu|Fri|Sat|Sun) ([01][0-9]|2[0-3]):[0-5][0-9]$`
(UTC). Tests in `internal/config/weekly_test.go`: valid, invalid day, invalid
time, empty (allowed; renders no schedule).

### Task 4.2: Token and render

**Files:** `internal/tokens/tokens.go`: `{{CI_WEEKLY_SCHEDULE}}` expands to
nothing when the slot is empty, otherwise to:

```yaml
  # Weekly full suite: slot Tue 06:15 UTC (assigned by `lacquer fleet slots`).
  schedule:
    - cron: '15 6 * * 2'
```

It is placed under `on:` at the END of the preceding line, as the other
whole-block tokens are (the comment in tokens.go explains why: no blank line
when it renders empty). The weekly run's concurrency: the existing group
expression gains `github.event_name == 'schedule' && format('weekly-{0}',
github.workflow)` with `cancel-in-progress: false`. Read the long
concurrency comment in `ci.yml` before touching it.

**Tests:** extend `TestScheduledMacJobsDoNotShareASlot` with a configured
slot, and new `TestWeeklyScheduleRendersOnlyWithASlot` (absent → byte-
identical to today). Mutation: render the cron with day `0`/`7` confusion
(the cron weekday for Sun is 0) → the named test fails.

### Task 4.3: `lacquer fleet slots`

**Files:** `internal/fleet/slots.go`, `internal/fleet/slots_test.go`, plus
the subcommand under `case "fleet":` in `cmd/lacquer/main.go`
(`fleet slots [--write] [--check]`).

The algorithm, exactly (design §3): take the roster repos whose manifest has
an iOS/macOS profile. Order them by FNV-1a 32 of lowercased `owner/name`.
The k-th gets day `k mod 7` and time `["06:15","08:15"][k div 7]`; beyond 14
repos, add `"10:15"`. Web/supabase repos: same ordering over `owner/name#linux`,
day `(3k) mod 7`, `05:15`. A repo whose manifest already has a slot KEEPS it,
and new repos fill the free slot on the least-loaded day.
`--check` exits non-zero if two Mac repos share a slot. `--write` edits each
repo's `.lacquer.toml` (only the `[weekly]` table) on disk; it does not
commit. Tests: a fixed fake roster of 16 names, with a golden table; adding
a 17th name moves nobody; `--check` catches a planted collision.

The operator's applied table lives in fleet-ops (private). Do not commit the
real table here.

---

## Unit 5 — Push-to-main reuse + release provenance (Opus)

D4 = (b), D5. Closes #508.

### Task 5.1: Tree-reuse on push

The "Select tests" step, on `push` to main:
1. `tree=$(git rev-parse HEAD^{tree})`.
2. Find the PR (`gh api repos/$R/commits/$SHA/pulls`). Find that PR's most
   recent successful iOS CI run (`actions/runs?event=pull_request&head_sha=<PR head>`,
   by workflow file name) and download its `test-selection.json`.
3. **Reuse only if** that artifact's `tree == $tree` AND `mode == full` AND
   the run's CI OK concluded success. Then output `reuse=true` plus
   `reused_run=<url>`.
4. Any other case, any API error or a missing artifact → `reuse=false`, which
   means FULL.

Every Mac job's `if:` gains `needs.changes.outputs.reuse != 'true'`. CI OK,
when `reuse == true`, prints "Reused full green suite from <url> (identical
tree <sha>)" to the summary and passes. When `reuse == true` but
`reused_run` is empty, it fails.

`permissions:` on `changes` gains `actions: read` (for the artifact) and
`pull-requests: read`. Name both in the PR as a permissions widening.

**Tests:** a behavioural CI OK test for reuse true/false; a render test that
every Mac job carries the reuse guard (walk the jobs, so a new Mac job
without it fails). Mutation: drop `mode == full` from the reuse condition,
and a fixture where the PR run was `selected` must fail by name.

### Task 5.2: Provenance requires a FULL iOS CI OK (D5)

**Files:** `profiles/ios/workflows/release.yml`, step "Require a passing CI
OK for this exact commit". Today it accepts any check run named `CI OK`
(#508). Change it to:
- select check runs named `CI OK` whose `check_suite`'s workflow run
  `path` ends in `/ios-ci.yml` (`gh api .../check-runs` → `check_suite.id` →
  `actions/runs?check_suite_id=`);
- and whose run's `test-selection.json` says `mode == full` OR reuse==true
  (whose reused run was full). A push run is always one or the other by
  construction; a PR run never counts.
Tests: extend `release_provenance_test.go`. A Web CI `CI OK` on the SHA must
NOT satisfy it; a selected-mode run must NOT satisfy it. Mutation: revert to
name-only matching → the named test fails.

---

## Unit 6 — Weekly failure reporting (Opus)

Design §4 and D7.

### Task 6.1: Weekly job behaviour

In the weekly (`schedule`) run, after tests, on failure:
1. Flake check: re-run only the failing test IDs
   (`-only-testing:Target/Suite/test`, from the xcresult `testFailures`), once,
   on the same commit, in the same job.
2. Suspects: list the PRs merged since the last green `schedule` run
   (`git log --merges`/`--first-parent` between the two SHAs, PR numbers from
   the subjects). Download each PR's `test-selection.json`, and keep the PRs
   whose selectors did NOT include the failing target.
3. If more than one suspect: bisect by checking out suspects' merge commits
   and re-running only the failing tests. At most 3 reruns, all inside the
   same job. Stop at the first merge where it fails.
4. Open or update ONE issue labelled `weekly-suite`, with the failing tests,
   flaky-or-not, suspects and the bisect result. Close it on the next green
   weekly. `issues: write` goes on the weekly path only. Since permissions are
   per job, not per event, put this in a step the job runs only
   `if: github.event_name == 'schedule'`, and add a test that the permission
   exists on this job alone.

### Task 6.2: `HarvestWeekly` producer

**Files:** `internal/producers/weekly.go`, `internal/producers/weekly_test.go`;
call it next to `HarvestMerges` in `internal/console/console.go:155` and
`internal/inboxwatch/env.go:605`.

Mirror `HarvestMerges` exactly: roster repos, a cursor file, a scripted
runner in tests, idempotent per ref, and append before advancing the cursor.
For each repo and profile CI workflow:
- latest `schedule` run red → ACTION `Weekly full suite red: <owner/repo>`,
  ref = run URL, with the body taken from the `weekly-suite` issue;
- no `schedule` run in more than 8 days while the manifest has a slot →
  ACTION `Weekly full suite did not run: <owner/repo>`, ref
  `<repo>#weekly-missing-<ISO week>`.
Use only the existing entry types and fields: the phone mirror reads this
file, and a new field breaks it silently (see the producers package doc).

**Tests:** red → 1 ACTION; red twice in one harvest → still 1; green → none;
missing for 9 days → 1 ACTION; missing for 7 days → none; a repo with no
slot → none. Mutation: drop the slot check → "repo with no slot" fails by name.

---

## Unit 7 — Pilot and measurement (Sonnet)

1. Sync both pilots to the released lacquer that contains units 1–6, and
   apply their weekly slots from the private table.
2. For one week, collect billed minutes per PR and per push-to-main with the
   §1b rules, from cached API reads (rate-limit rules in the operator's
   brief).
3. Report before and after: median and mean billed per PR, Mac slot-min and
   Blacksmith billed separately, the share of push runs that reused, and
   whether a weekly run fired in its slot and what it found.
4. Propose the fleet sync to your PM with those numbers. Foxy schedules it.

---

## Order and dependencies

Unit 1 → (Unit 2 → Unit 3) → Unit 4 → Unit 5 → Unit 6 → Unit 7. Unit 1
needs nothing else. Units 3, 5 and 6 each need the previous unit merged
**and released** (a lacquer merge auto-releases), because the workflows
download the released binary.
