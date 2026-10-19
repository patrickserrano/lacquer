# Selective tests + weekly full suite — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Status 2026-10-19.** Unit 1 shipped (#514); its actual deviations are
recorded under Unit 1 below. Nothing from Unit 2 onward is built. The plan was
refreshed against `main` on 2026-10-19 and applies the decisions quoted in the
design's §8 ("Decided 2026-10-19"). What changed here:

- **New unit order: Unit 1 (shipped) → Unit 5 → Unit 2 → Unit 3 → Unit 4 →
  Unit 6 → Unit 7.** Unit 5 is push-to-main tree reuse (D4b) and release
  provenance (D5) as ONE unit, shipping next, before any selection exists, so
  every PR run is FULL while it is proven. Unit numbers keep their 10-04
  meaning. Unit 5's tests carry both fail paths from the decision. A SELECTED run is
  never reused: Unit 3 adds no second path, only a test that the real
  engine's `selected` record is refused by the same "FULL and green on the
  same tree" rule (Task 5.3).
- **iOS only.** The web tasks (old 2.5 and 3.4) and the web pilot are removed
  from scope and kept in "Later: web" at the end of this file, with the
  reasoning.
- Mechanical updates to Units 2–7 against current `main` (renames, moved
  anchors, new surfaces), each with its `file:line`.
- The baseline numbers from 10-04 are superseded for before/after use
  (design §1).

**Goal:** Cut BILLED CI minutes per PR by running only the tests a diff can
affect, and by collapsing jobs that bill far more than they work. A
staggered weekly full suite is the backstop. Main and every release keep a
full green suite on their exact tree.

**Design (read first, it is the spec):** `docs/plans/2026-10-04-selective-tests.md`.
All decisions D1–D9 were approved 2026-10-04, as amended 2026-10-19. Where this
plan and the design disagree, the design wins: stop and report it.

**Architecture:** A pure Go function, `selecttests.Select(diff, manifest,
tree)`, sits behind a `lacquer select-tests` subcommand. The workflows run it
as a **step inside the existing `changes` job**, and no new job is ever added
for selection or gating. Templates pass its selectors to `xcodebuild
-only-testing:`. CI OK checks a selection exists and that what was selected
ran. A `schedule:` trigger, with a per-repo slot from `.lacquer.toml`, runs
everything weekly and writes a result artifact only. A console producer turns
red or missing weekly runs into inbox ACTIONs and the `weekly-suite` issue.

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
   repo on a throwaway branch, open a draft PR there, and record the run under
   `## Proven on` (see rule 6 for what may be named). CI rejects the PR without it.
   Close the proof PR afterwards: the 24-hour PR cap applies to it too.
3. **`## Fleet dry-run`** for anything that changes a detector or `audit`
   (units 2 and 4): run it against every managed repo, and paste the output.
   Render each repo the way CI sees it: a fresh clone with xcodegen (and any
   other generator lacquer invokes) hidden from `PATH`, because `sync`
   regenerates the project otherwise and both sides then carry a file CI never
   has (lacquer's CLAUDE.md, rule 1).
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
   proof runs, billed before/after and dry-run output go to the operator's
   private fleet repo. The lacquer PR's
   `## Proven on` says only "pilot repo (private; recorded in the operator's
   fleet repo), run <numeric id>, <date>". A run URL would name the repo, so
   it goes to the private fleet repo, not into the PR. CI checks for the heading.
7. Run `go test ./...` before every push. Two CI rounds per PR, through
   `lacquer ci-round begin`. No force-push.

Pilot repo (named in the operator's private brief, not here): one iOS repo with
local SPM packages. iOS only; the web pilot is under "Later: web".

---

## Unit 1 — Job consolidation. SHIPPED (#514).

Design §1b. This was independent of selection and probably the biggest billed
saving. The tasks below are kept as written, for the record and for the
reasoning; what actually shipped, and where it differs, is here.

**Record of what shipped (verified on `main`, 2026-10-19):**
- `changes` runs on `[self-hosted, Linux, pi-gate]` in all three profiles
  (`profiles/ios/workflows/ci.yml:72`, `profiles/web/workflows/ci.yml:72`,
  `profiles/supabase/workflows/ci.yml:74`). pi-gate is a role carried by
  runners of two architectures, so any download there maps `uname -m`
  (`ci.yml:55-71`).
- The iOS `baseline` job is gone; its steps are the tail of `Lint`
  (`ci.yml:808`, "Assert the project baseline"), and the job id `lint` / name
  `Lint` are unchanged.
- Supabase: `lint-database` + `test-database` became one `database` job
  (`profiles/supabase/workflows/ci.yml:605`); `check` and `deploy-database`
  were right-sized; the health `ping` is on pi-gate.
- Web: `dependency-review` and `env-validation` are steps of `changes`
  (`profiles/web/workflows/ci.yml:423-445`) and the two workflows are retired.
  `dep_review` is advisory in CI OK; `env_valid` is required.
- **Deviation: Task 1.6 did not ship.** The release workflow's three small
  jobs stay on a hosted Blacksmith runner
  (`profiles/ios/workflows/release.yml:88`, `:174`, `:1083`), and
  `internal/shipped/release_provenance_test.go:52-60` pins the provenance job
  to a hosted Blacksmith Linux runner as a property. Moving it is a separate
  decision, and Unit 5 below edits that job's step without moving it.

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
Tasks 1.2–1.6 still ship. Report the failing check.

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

### Task 1.6: Release's small jobs → pi-gate (NOT SHIPPED; see the record above)

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
- `## Proven on` in the lacquer PR: the numeric run ids only (see rule 6). The billed table,
  before and after per run, goes to the private fleet repo, never into lacquer. "Before" is the pilot's last 10 PR runs on main's
  templates; "after" is the proof runs. Compute billed with the design §1b
  rules. Use cached JSON, `per_page=100`, and stop at 1,000 remaining rate
  limit.
- Re-run `lacquer protection --roster <roster>` and paste its output: no
  required check name may disappear.


---

## Unit 5 — Push-to-main reuse + release provenance. Ships next.

D4b and D5 as ONE unit (decided 2026-10-19). Closes #508. It ships before the
selection engine, so every PR run is still FULL while it is proven and
measured, and the two halves read the same record.

**Today (verified on `main`, 2026-10-19).** The Test job runs on every push to
main (`profiles/ios/workflows/ci.yml:1387-1409`; push runs are path-filtered
per Swift component by `{{IOS_CI_PUSH_PATHS}}`, `ci.yml:12-17`). The release
gate accepts any check run named `CI OK` on the SHA, with no workflow filter
(`profiles/ios/workflows/release.yml:107-134`, `.name == "CI OK"` at `:126`),
from a job with `contents: read, checks: read` (`release.yml:90-96`). So a
Web or Supabase `CI OK` on that SHA lets a tag through: #508.

### Task 5.0: The run record

On every `pull_request` and `push` run, a step in `changes` uploads
`test-selection.json` as an artifact (`retention-days: 30`):
`{"pr": <n|null>, "head": <sha>, "tree": <git rev-parse HEAD^{tree}>,
"mode": "full"}`. Until Unit 3 ships, `mode` is always `full`. The `tree` is
read after checkout, so on a PR run it is the tree of `refs/pull/N/merge`, the
tree that was actually tested. The step must not fail the job (the same
reasoning as drift). Unit 6 reads this file too.

**As built (#513 Unit 5).** Two deviations, both toward FULL:
- The record is written by CI OK (`if: always()`, `continue-on-error`), not by
  `changes`. Every step after `changes`' filter step belongs to the drift
  audit, and `TestDriftIsAStepGatedOnTheFilter` holds them to that; CI OK is
  also the job that knows whether the test job was expected. `changes`
  publishes the `tree` (from its reuse step, which runs before the filter).
- `mode` is `full` only when the test job was expected to run (same-repo, code
  changed, a project exists; D9's condition), and `none` otherwise. A
  docs-only or fork PR's green CI OK ran no tests, so it must never be reused
  or prove a release. A push that reused a PR run records `mode: reused` with
  `reused_run` and `reused_run_id`, so a reader that checks only for `full`
  refuses it rather than trusting it; provenance follows the named run.

### Task 5.1: Tree reuse on push (D4b)

The reuse step in `changes`, on `push` to main only:
1. `tree=$(git rev-parse HEAD^{tree})` for the merge commit.
2. Find the PR (`gh api repos/$R/commits/$SHA/pulls`), then its most recent
   completed iOS CI run (`actions/runs?event=pull_request&head_sha=<PR head>`,
   filtered by the workflow's file name), and download that run's
   `test-selection.json`.
3. **Reuse only if ALL three:** the record's `tree == $tree`, AND its
   `mode == full`, AND that run's CI OK concluded `success`. Then output
   `reuse=true` and `reused_run=<url>`.
4. **Anything else runs FULL:** any API error, a missing or unparsable record,
   no PR, a different tree, a mode other than `full`, a run that was not
   green. The step never fails the job; its failure mode is `reuse=false`.

Every Mac job's `if:` gains `needs.changes.outputs.reuse != 'true'`: `lint`,
`build-release`, `test` **and `watch-test`** (the last is new to the walk;
`internal/tokens/watch.go:199`). CI OK, when `reuse == true`, prints "Reused
full green suite from <url> (identical tree <sha>)" to the step summary and
passes. When `reuse == true` but `reused_run` is empty, it fails.

**This reaches the GitHub API from a job**, which `ci.yml:19-20` forbids today
("No job talks to the GitHub API"; `permissions: contents: read`,
`ci.yml:29-30`). The unit amends that comment on purpose, grants `actions:
read` and `pull-requests: read` on `changes` ONLY, names both in the PR as a
permissions widening, and tests that no other job gained a permission.
`changes` runs on pi-gate (`ci.yml:72`), which has two architectures; before
relying on `gh` there, prove it is installed on both boxes, and if it is not,
use `curl` with the token. (Provenance uses `gh` on a hosted Linux runner.)
Accepted 2026-10-19 as a read-only exception, scoped exactly this way.

**The lookup fails toward FULL, and each way it can fail is a named test:**
an API error (non-2xx), a rate limit (403/429 with the limit headers), a
timeout (the call is bounded; a hang is not a reuse), a missing run (no PR
for the commit, no completed iOS CI run on its head, or no artifact), and an
ambiguous match (more than one PR for the commit, or more than one candidate
run for the head). Each fixture asserts `reuse=false` and that the Mac jobs
run. Mutation: treat an error or an ambiguous match as "no objection" → the
named tests fail.

**Fail paths, written first (the decision's, verbatim in intent):**
- **A merge whose tree differs must run CI on main.** Fixture: a PR run
  recorded `tree=A`, `mode=full`, green; the merge commit's tree is `B` (main
  moved after the PR's last run). Assert `reuse=false` and that the Mac jobs
  are not skipped. Mutation: compare the PR head SHA, or the PR number, instead
  of the tree → this test fails by name.
- **Only a FULL, green run is reused.** Fixtures: same tree, `mode=selected`
  → `reuse=false`; same tree, `mode=full`, CI OK `failure` → `reuse=false`;
  record missing → `reuse=false`. Mutation: drop `mode == full` from the
  condition → the selected fixture fails by name. (The `selected` fixture is
  authored now even though no selected run exists yet: it is what keeps Unit 3
  honest.)
- A behavioural CI OK test: `reuse=true` with an empty `reused_run` exits
  non-zero; `reuse=true` with one exits zero.
- A render test that walks every Mac job (a job whose `runs-on` carries
  `macOS`) and asserts the reuse guard, so a new Mac job without it fails.

### Task 5.2: Provenance requires a FULL, green iOS CI OK (D5)

**Files:** `profiles/ios/workflows/release.yml`, step "Require a passing CI OK
for this exact commit" (`:107-134`). It stays on its hosted runner
(`release.yml:88`); `release_provenance_test.go:52-60` pins that as a property
and must pass unchanged (the new `actions: write` is a permission, not a runner change). Change the selection from "any check run named `CI
OK`" to:
- check runs named `CI OK` whose workflow run `path` ends in `/ios-ci.yml`
  (`gh api .../check-runs` → `check_suite.id` →
  `actions/runs?check_suite_id=`);
- and whose run is FULL: its `test-selection.json` says `mode == full`, OR the
  run was a push run that reused a FULL green PR run (`reuse == true` with a
  `reused_run` whose record is `full`). A push run is one or the other by
  construction. A PR run never counts. The job gains `actions: read`
  (`release.yml:90-96` has only `contents` and `checks`); name it in the PR.
- no `CI OK` of the right workflow on the SHA, because the path filter
  produced no iOS push run for it (a commit that touched no Swift component):
  the release job **dispatches a FULL iOS CI run on that exact SHA and waits**
  (D5 as approved: "Otherwise `ios-release` dispatches a full run and
  waits"). It dispatches `workflow_dispatch` on the tag (a ref that resolves to
  the SHA; `inputs.full` is true by default), asserts the new run's `head_sha`
  equals the release SHA, waits with a bounded timeout, and then applies the
  SAME acceptance rules as above to that run: iOS CI workflow path, `mode ==
  full`, CI OK `success`. If the run is not FULL and green, times out, cannot
  be found, or the dispatch itself fails, **the release fails closed**. The
  job gains `actions: write` for the dispatch, named in the PR as a
  permissions widening on this job only. Considered and rejected: accepting
  the nearest first-parent ancestor's run (it proves a different commit's
  tree), and blocking the tag outright (it contradicts D5).

**Fail paths, written first:**
- **A non-FULL "CI OK" must not let a release through.** Fixture: the SHA has a
  green iOS `CI OK` whose record says `mode: selected` → the gate fails.
  Mutation: drop the `mode` check → the named test fails.
- **A wrong-workflow "CI OK" must not let a release through.** Fixture: the SHA
  has a green `CI OK` from the web or supabase CI workflow and none from iOS CI
  → the gate fails (this is #508). Mutation: revert to name-only matching →
  the named test fails.
- **A dispatched run that comes back non-FULL or red must block.** Fixtures,
  for a SHA with no iOS push run: the dispatched run reports `mode: selected`
  → the release fails; it concludes `failure` → fails; its `head_sha` is not
  the release SHA → fails; it never appears or times out → fails. Only a
  dispatched run that is FULL, green, on the right SHA and the iOS workflow
  lets the release through. Mutations: skip the `mode` check on the dispatched
  run, and skip the `head_sha` check → each flips its named test.
- A green FULL iOS `CI OK` passes; a reused-FULL push run passes; a missing
  record fails (not passes).
Extend `release_provenance_test.go` with these; the existing assertions are
not loosened.

**As built (#513 Unit 5).** The new fail paths live in
`release_full_ci_test.go` beside `release_provenance_test.go`, which is
unchanged. Also:
- The dispatched run is found by a nonce: iOS CI gained an optional
  `dispatch_id` input and a `run-name` that is `iOS CI provenance <nonce>` when
  it is set and empty (GitHub's default name) when it is not. Zero runs or more
  than one carrying the nonce blocks the release.
- If a push or dispatched iOS CI run on the SHA is red, cancelled or still in
  progress, the release is blocked and nothing is dispatched: a fresh run must
  not launder that verdict. A green run that cannot be shown FULL (its record
  expired, or says `none`/`selected`) is D5's "otherwise": the gate dispatches
  a FULL run. Records can expire long before the 30 days requested, because a
  repository's artifact retention limit caps them.
- There is no `inputs.full` yet: a dispatched run is FULL by construction
  until selection exists, and its record must still say `full`.
- `actions: write` is how "read + write" is spelled; a permissions map holds
  one value per scope.
- The tag-ancestry check runs first, so a tag that cannot ship is refused
  before any dispatch.

### Task 5.3: Extension for selected runs (lands with Unit 3)

When the engine ships, a selected PR run records `mode: selected`, and
reuse and provenance both refuse it through the same rules above. Unit 3 adds
no second path: it adds a test that a `selected` record, produced by the real
engine on a real fixture diff, is not reused and does not satisfy provenance.

### Task 5.4: Prove, measure, PR

`## Proven on`: a PR run and its merge on the pilot, one numeric run id each
(rule 6 of the rules above). Prove both a reuse (tree matches) and a non-reuse
(main moved between the PR's last run and the merge). Report the
push-to-main billed minutes per merge, before and after, and the share of
pushes that reused; they go to the operator's private repo, never here.

---

## Unit 2 — The selection engine

Design §2, §5. Pure Go, no workflow changes in this unit. iOS only.

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
	ModeNone     Mode = "none" // ONLY when every changed path is no-test (docs, agent tooling) or owned by a required non-Swift component
)

// Result is per product, because a project can declare several [[product]]s
// and each is its own matrix leg with its own selectors; the watch bundle runs
// in a separate job and is listed apart.
type Result struct {
	Mode     Mode      `json:"mode"`
	Products []Product `json:"products"`
	Watch    []string  `json:"watch"`   // WatchTestSelectors for full; the watch bundle when selected
	OwnedBy  []string  `json:"owned_by"` // components relied on when Mode is none because a required component owns the paths
	Reasons  []string  `json:"reasons"`  // one per decision, e.g. "CoreKit/Sources/A.swift → CoreKit (+ dependents DataKit, app)"
}

type Product struct {
	Name         string   `json:"name"`
	Selectors    []string `json:"selectors"`      // -only-testing target names; for full: TestSelectors()
	AppTargetsRan bool    `json:"app_targets_ran"` // every app test target of this product is in Selectors (the coverage gate reads it)
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
It covers every entry of design §2 rules 3 and 4, including **`.lacquer.ratchet.toml`
(fail-safe; today it would fall to "unclaimed" and reach FULL by accident)**
and **every pattern under a nested component prefix** (`App/project.yml`,
`App/Foo.xcodeproj/project.pbxproj`, `App/Package.swift`, `App/Info.plist`),
`docs/x.md` (no-test), `.claude/skills/a/SKILL.md` (no-test),
`.github/workflows/ios-ci.yml` (fail-safe), `CoreKit/Tests/Support/Fixtures/a.json`
(fail-safe: shared test helpers), `Weird/new.thing` (unclaimed, which later
becomes FULL).
**Implement:** two ordered regexp lists, `noTestPatterns` and
`failSafePatterns`, each anchored with an optional leading directory prefix,
plus the manifest's `[tests].full_on` appended to failSafe. Fail-safe wins
over no-test, so a `.md` under `.github/` is fail-safe.
**Mutation:** delete the `project.yml` pattern; `TestClassify/project.yml`
must fail by name. A second mutation: anchor the pattern at the root again;
`TestClassify` for the nested-prefix fixture must fail by name.

### Task 2.3: Local package graph (reuse, do not rescan)

**Test first:** `TestPackageGraph`, using the fixture `testdata/two-packages/`
(a tracked `project.yml` referencing `CoreKit` and `DataKit` as local packages;
`CoreKit/Package.swift` with no local deps; `DataKit/Package.swift` with
`.package(path: "../CoreKit")`; app sources in `App/`). Expect units
`{CoreKit: [CoreKitTests], DataKit: [DataKitTests]}` and reverse edges
`CoreKit → {DataKit}`. A second fixture has a tracked pbxproj and no spec; a
third has a generated pbxproj on disk and a spec: the graph must come from the
spec (the `ReadForAudit` rule: the answer must not depend on whether xcodegen
ran on this machine).

**Implement by reuse, not by a new scanner:**
- target → package: `testtargets.Target.Package` / `PackageDir`
  (`internal/testtargets/testtargets.go:41-53`; built in
  `internal/testtargets/packages.go:85-106`), obtained through
  `testtargets.ReadForAudit` (`internal/testtargets/watch.go:136`; spec side
  `ParseSpec`, `spec.go:121`);
- the declared packages: `config.Component.Packages`
  (`internal/config/config.go:1683`) and the package components
  `swiftcomponents.Components` / `swiftcomponents.Packages`
  (`internal/swiftcomponents/swiftcomponents.go:57-69`, `:211`);
- only the `.package(path: "...")` edges between packages are read from each
  `Package.swift` (a regexp, resolved relative to the manifest dir), because
  dependents are not in `testtargets`.
A `Target` with `Unread` set, or a `Package.swift` that cannot be parsed, puts
the whole Result in FULL with reason "unparsable Package.swift: <path>" /
"unreadable package: <reason>". Never a guess. `go doc` the existing readers
first and extend them if a field is missing, rather than copying them.

### Task 2.4: `Select` for iOS

**Tests first** (`TestSelectIOS`). Each case asserts the exact `Result` (mode,
per-product sorted selectors, watch list, reasons non-empty):

| case | diff | expected |
|---|---|---|
| package source | `CoreKit/Sources/A.swift` | selected: CoreKitTests, DataKitTests, + app targets (test_target, ui_test_target, non-package extras) per product; watch list = the watch bundle |
| package tests only | `CoreKit/Tests/CoreKitTests/ATests.swift` | selected: CoreKitTests; `app_targets_ran` false |
| app only | `App/Feature/View.swift` | selected: app targets only, no package tests; `app_targets_ran` true |
| app tests only | `AppTests/FooTests.swift` | selected: the app test target that owns it (by directory = target name), else all app targets |
| watch only | `WatchApp/View.swift` | selected: the watch list; no product selectors |
| two products | a change that selects app targets, with two `[[product]]`s | each product lists its own `test_target` / `ui_test_target` |
| docs + agent tooling | `README.md`, `.claude/x` | none (no owner) |
| fail-safe | `App.xcodeproj/project.pbxproj`, `.lacquer.ratchet.toml` | full |
| unmapped | `Weird/new.thing` | full |
| mixed no-test + package | `README.md`, `DataKit/Sources/B.swift` | selected: DataKitTests + app |
| deleted test target dir | `D CoreKit/Tests/CoreKitTests/ATests.swift` (last file) | full ("test target may have been removed") |
| rename across packages | `R CoreKit/Sources/A.swift → DataKit/Sources/A.swift` | selected: union of both sides' selections |
| empty computed set with code changes | an override map that selects nothing | full ("selection empty") |
| `not_run_in_ci` target | its directory changed | that target is never a selector (honour `config.NotRunInCI`, `internal/config/config.go:220`), the rest as normal |
| owned, required | `supabase/x.sql`, component `supabase/` declared with its profile CI required | none, `owned_by: [supabase]` |
| owned, not required | the same path, the context absent from the committed required claim | full |
| owned, claim unreadable | the same path, a malformed claim | full |
| owned inside the app component | `supabase/x.sql` under an ios+supabase root component | the declared component is the owner (most specific wins) → as above |
| mixed owned + app | `supabase/x.sql`, `App/Feature/View.swift` | selected: app targets (the owned path adds nothing) |

**Implement** in this order, and do not reorder:
1. Any fail-safe or unclaimed path → return FULL.
2. Drop no-test paths. Drop component-owned paths whose owner's profile CI is
   a REQUIRED check (rule 5; anything undeterminable is not dropped, it is
   FULL). If nothing is left → NONE (with `owned_by` set if an owned path was
   dropped).
3. For each remaining path: package tests dir → that package's tests;
   package other → its tests + transitive dependents' tests + all app
   targets; app-side → app targets.
4. Union, sort, dedupe per product. Empty → FULL.

**Required-check input (rule 5; decided 2026-10-19: a committed claim, cross-checked by audit).** `select-tests` reads the required context of each non-Swift component from a committed claim (recommended shape: a table in `.lacquer.toml` naming the component and its CI context; the exact shape is fixed in this unit's brief). It never calls the API, and it is not a live read, because reading classic branch protection needs admin (`internal/protection/gh.go:49-57`, `:250-255`) and `GITHUB_TOKEN` cannot be granted `administration`, which is not among the scopes a workflow's `permissions:` accepts (https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions; GitHub's REST page for the read states no token permission, so the admin requirement is **not confirmed by GitHub's docs**). Add the cross-check to `lacquer audit` / `lacquer doctor`, reusing `protection.Fetch` (`internal/protection/gh.go:126`), `protection.Local` (`internal/protection/workflows.go:78`) and `protection.Compare` (`internal/protection/protection.go:207`): **a claim the live required list does not back raises an inbox ACTION, not a log line** (existing entry types and fields only). Without the cross-check the claim cannot be trusted, so every owned path is FULL. Add a test per case: backed claim, unbacked claim (ACTION raised), no claim, malformed claim. Possible LATER upgrade only: where a repository uses rulesets, `GET /repos/{owner}/{repo}/rules/branches/{branch}` may be readable at read scope; not confirmed from GitHub's docs, and nothing here depends on it.

FULL's selector lists are exactly what `config.Product.TestSelectors()`
(`internal/config/config.go:1569`) and `WatchTestSelectors()` (`:1539`) give
today, per product, so FULL is byte-identical to the current `-only-testing:`
lists.

**Mutation guard test:** `TestSelectFailSafeIsLive`. It runs the "unmapped"
and "fail-safe" cases with `failSafePatterns` emptied through an unexported
test hook, and asserts they now return something other than FULL. This
proves the FULL assertions above can fail. A second one for rule 5: with the
required-check test removed, the "owned, not required" case must flip from
FULL to none, and the named test fails. Record both under `## Mutations`.

### Task 2.5: (removed from scope)

`Select` for web moved to "Later: web" (decided 2026-10-19).

### Task 2.6: CLI `lacquer select-tests`

**Files:** `cmd/lacquer/selecttests.go`, `cmd/lacquer/selecttests_test.go`,
plus the dispatch in `cmd/lacquer/main.go` (add `case "select-tests":` next
to `case "ci-round":`, which is at `cmd/lacquer/main.go:848`).

```
lacquer select-tests --base <sha> --head <sha> --profile ios [--component <path>] [--manifest .lacquer.toml]
```
It reads `git diff --name-status -M <base>...<head>` (three-dot, as `changes`
does) and the manifest, and reads files with `git show <head>:<path>`. It
prints the Result JSON to stdout. Exit codes: 0 means a Result was printed;
anything else means none was, and the workflow treats that as FULL. Tests
use a temp git repo (see `internal/gittest`) with two commits.

**Fleet dry-run (required, rule 1 of lacquer's CLAUDE.md):** render each
managed repo the way CI sees it, a fresh clone with xcodegen (and any other
generator lacquer invokes) hidden from `PATH`, because `sync` regenerates the
project otherwise. For every managed iOS repo, run `select-tests` over each of
its last 20 merged PRs (`base = merge-base`, `head = PR head`, read from local
clones, no API). In the lacquer PR, `## Fleet dry-run` gives only anonymized
totals ("N repos, M PRs: full X / selected Y / none Z; 0 unexpected none").
The per-repo output goes to the private fleet repo. Any `none` for a PR that touched a
non-docs file and no required owner is a bug. The `public-names` job
(`TestShippedContentNamesNoPrivateConsumer`) enforces the neutral-fixture rule
in CI.

Commit, then open the PR. Review the reasons strings closely: they are what a
reviewer sees in CI.

---

## Unit 3 — Wiring + the gate

Design §2 (Wiring), §6, D3 and D9. Depends on Unit 2 being merged and
released. Depends on Unit 5, which introduced the run record and the reuse
guard this unit extends.

### Task 3.1: Selection step in `changes` (iOS)

**Files:** `profiles/ios/workflows/ci.yml`.

- The lacquer binary download in `changes` becomes unconditional. Today it is
  inline in "Audit for drift", gated on the `lacquer` filter
  (`ci.yml:~212-260`, resolving the version from `.lacquer.lock` at
  `:253-256`); lift it into a step both consumers use, rather than a second
  copy. It stays one download, and it maps `uname -m`, since pi-gate runs on
  two architectures (`ci.yml:55-71`). The Lint job's doctor download
  (`ci.yml:~598-630`) and the Test job's coverage-gate download
  (`ci.yml:2170-2229`) are the existing patterns.
- New step "Select tests" (id `select`). On `pull_request` it runs
  `lacquer select-tests --base $base --head $head --profile ios`. On `push`,
  `schedule` and `workflow_dispatch` it writes `{"mode":"full",...}` without
  computing (push-to-main reuse is Unit 5's). Outputs: `mode`, per-product
  `selectors` and `app_targets_ran`, the watch selectors (newline-separated,
  the same separator `CIExtraTestSetup` uses), and `selection_ok=true` only
  when the JSON parsed.
- **Any failure of the step → mode=full.** That includes the binary predating
  the subcommand: the binary is the one at `.lacquer.lock`'s version, so a
  repository synced before `select-tests` shipped has none, and an unknown
  subcommand or a locked version below the first release that carries it
  resolves to FULL. Test it with a fake old binary. The step must not fail the
  job (the same reasoning as drift).
- The `test-selection.json` record (Unit 5, Task 5.0) now carries the real
  `mode` and `selectors`.
- D3: extend the existing `code` filter's no-test deny-list with the
  agent-tooling paths, so `code=false` for them. Same list as `noTestPatterns`;
  add a test that the YAML regexp and the Go list agree, path by path, over
  the classifier's fixtures. The deny-list regexp is at `ci.yml:135`.

### Task 3.2: Test and watch-test consume the selection

- Replace `{{IOS_CI_ONLY_TESTING}}` at the call site with an array built from
  the `changes` outputs. Reuse the bash-array construction from
  `CIExtraTestSetup` (`internal/tokens/tokens.go:769`; the old name in this plan
  was `IOSCIExtraTestSetup`, the token is `{{IOS_CI_EXTRA_TEST_SETUP}}`),
  because target names contain spaces. **The matrix form is in scope:** each
  leg takes its selectors from its own product's output instead of
  `$TEST_TARGET` / `$UI_TEST_TARGET` / `$EXTRA_TEST_TARGETS`
  (`internal/tokens/tokens.go:619-625`, `CIOnlyTesting` at `:714`). In FULL
  mode the array must equal today's rendered list, per leg: add a test that
  renders both and compares.
- **`watch-test` consumes the watch selection.** Its `if:`
  (`internal/tokens/watch.go:199`) gains the selection (it runs when the watch
  list is non-empty, or in FULL), and its `-only-testing:$WATCH_TEST_TARGET`
  (`watch.go:428`) comes from the watch output.
- "Verify Test Selectors Matched" renders for EVERY iOS product (D9). Its
  heredoc reads the same selectors the job used. Update
  `internal/tokens/tokens.go` `CIVerifySelectors` (`:801`, still gated on
  `anyExtra`) and the tests that assert it is absent when there are no
  extras: those assertions change on purpose, so state that in the PR.
- **The coverage ratchet gate runs only when every app test target ran.** The
  gate step (`ci.yml:2231-2297`, `lacquer ratchet --coverage-report`,
  `internal/ratchet/coverage.go:153-225`) is guarded by the product's
  `app_targets_ran`. Otherwise it does not run the ratchet, prints
  `skipped: partial selection` to the step log, and appends the same line to
  `$GITHUB_STEP_SUMMARY`. It is never a pass. **Test it both ways,
  behaviourally:** a partial selection prints the line in the summary and does
  not invoke the ratchet; a full one runs it. Mutation: let the gate run on a
  partial selection → the named test fails. An un-enrolled project still warns
  and passes as today (`coverage.go:166-176`).
- Any new `{{IOS_CI_*}}` token needs a `legacyIOSCITokens` entry
  (`internal/shipped/ios_ci_products_test.go:100`), so a project that has not
  adopted it renders its old text.

### Task 3.3: CI OK asserts (D9)

**Files:** `ci-ok` in the iOS profile (supabase's is unchanged). New test file
`internal/shipped/ci_ok_selection_gate_test.go`. `ci-ok` already needs
`test{{IOS_CI_WATCH_NEED}}` (`ci.yml:2379`).

**The condition ("the test job is expected"):** `code == true` AND `xcodeproj
== 'true'` AND a same-repo PR or any non-PR event. That is exactly the
Test and `watch-test` `if:` (`ci.yml:1409`, `watch.go:199`). A skipped test
with `code == true` alone would turn a pre-code component red (no project to
test) and a fork PR red (it must not run on the self-hosted runner), and both
skips are correct.

New checks, in this order, each with its own `::error::` line, each applying
only when the test job is expected:
1. `selection_ok != true` → fail ("no selection was computed").
2. `mode == none` → fail, **unless** the selection names its `owned_by`
   components (the required-owner `none` of design §2 rule 5), in which case
   the test job being skipped is the correct outcome.
3. `needs.test.result == skipped` → fail, **and `needs.watch-test.result ==
   skipped` when a watch list was selected.** This is the newly closed hole:
   today a skip passes.
4. Print mode, selectors and reasons to `$GITHUB_STEP_SUMMARY` under "Tests
   NOT run on this PR" (FULL minus selected).

Tests: render and parse, then assert each condition appears in the CI OK
script. Then a **behavioural test**: extract the CI OK `run:` script and
execute it with bash and a fake `needs` context (substitute the `${{ }}`
expressions with literal strings). Cover `code=true, xcodeproj=true,
test=skipped` → exit 1; **`code=true, xcodeproj=false, test=skipped` → exit
0** (pre-code); a fork PR with `test=skipped` → exit 0; `code=false,
test=skipped` → exit 0; `mode=none, code=true` without `owned_by` → exit 1;
`mode=none` with `owned_by` and the test skipped → exit 0; `watch-test`
skipped with a watch list selected → exit 1. Mutation: drop check 3 → the
behavioural test fails by name; drop the `xcodeproj` clause → the pre-code
case fails by name.

### Task 3.4: (removed from scope)

The web pre-push hook (#507) moved to "Later: web" (decided 2026-10-19).

### Task 3.5: Prove, measure, PR

Prove on the pilot with three PRs, run one at a time: docs-only, an app-only
change, and a package change. Paste each run's mode, selectors and billed
minutes. Answer the design's open question: did `Run Tests` shrink on the
app-only PR? Compare the step's minutes with the "before" median. The pilot
must first be synced from where it is to the last release before this unit
ships (Unit 7); do not prove on a pilot that has skipped many releases, because
every release in between changes what it runs.

---

## Unit 4 — Weekly slots

Design §3 and D6. iOS only.

### Task 4.1: Config `[weekly]`

**Files:** `internal/config/config.go`: a `Weekly struct { Slot string \`toml:"slot"\` }`
on Config (a `LinuxSlot` belongs to web, "Later: web"). Validate the form
`^(Mon|Tue|Wed|Thu|Fri|Sat|Sun) ([01][0-9]|2[0-3]):[0-5][0-9]$` (UTC). Tests in
`internal/config/weekly_test.go`: valid, invalid day, invalid time, empty
(allowed; renders no schedule).

### Task 4.2: Token and render

**Files:** `internal/tokens/tokens.go`: `{{CI_WEEKLY_SCHEDULE}}` expands to
nothing when the slot is empty, otherwise to:

```yaml
  # Weekly full suite: slot Tue 06:15 UTC (assigned by `lacquer fleet slots`).
  schedule:
    - cron: '15 6 * * 2'
```

**Keep the template parseable.** The `on:` block now ends in a single token,
`{{IOS_CI_PUSH_PATHS}}`, which owns the list body at column 0 under
`push.paths:` (`profiles/ios/workflows/ci.yml:12-17`). `internal/retire`
parses the `on:` block BEFORE substitution and strips column-0 token lines to
do it (`internal/retire/retire.go:108-160`, `Scheduled`; the live example is
`{{IOS_RELEASE_TAGS}}` in the release workflow). So the new token must stand alone at column 0 after the paths token's block,
owning its own indentation, so that `retire` drops it and still parses the
template. The consequence is deliberate: `retire` classifies a workflow whose
`on:` block carries a `schedule:` as a timer, and a retired project stops
receiving those (`internal/retire/retire.go:1-12`). If the template carried a
literal `schedule:`, `ci.yml` would be classed as scheduled and a retired
project would stop receiving its CI. With the token, `ci.yml` is unscheduled
until a slot is substituted, and a retired project has none. Add a test that
parses the unsubstituted template (`Scheduled` is false) and the substituted
one (true), and one that a retired project still receives `ci.yml`. It is placed at the END of the preceding
line, as the other whole-block tokens are (the comment in tokens.go explains
why: no blank line when it renders empty).

**The weekly run's own concurrency group, and why it is mandatory.** The
existing group is `${{ github.workflow }}-${{ github.ref }}`
(`ci.yml:32-46`). A schedule run's ref is `refs/heads/main`, so without a
separate group the weekly run shares push-to-main's group. GitHub keeps one
pending run per group, so a weekly run queued behind a running push replaces a
pending push run, which GitHub reports as `cancelled`: the silent
non-failure the long comment there warns about. The group expression gains
`github.event_name == 'schedule' && format('weekly-{0}', github.workflow)`
with `cancel-in-progress: false`. Read the long concurrency comment before
touching it. **Test that a schedule run and a push run never share a group
string**, by rendering the expression for both events. Mutation: remove the
schedule branch → that test fails by name.

**Tests:** extend `TestScheduledMacJobsDoNotShareASlot`
(`internal/shipped/workflow_schedule_test.go:57`, rendering through
`renderIOSWorkflow` at `:25`; its comment already says per-project spreading
needs a per-project token) with a configured slot, and a new
`TestWeeklyScheduleRendersOnlyWithASlot` (absent → byte-identical to today).
Mutation: render the cron with day `0`/`7` confusion (the cron weekday for Sun
is 0) → the named test fails. The cleanup cron is `0 7 * * *`
(`profiles/ios/workflows/cleanup-ci.yml:10`) and the Mac slots avoid it.

### Task 4.3: `lacquer fleet slots`

**Files:** `internal/fleet/slots.go`, `internal/fleet/slots_test.go`, plus
the subcommand under `case "fleet":` in `cmd/lacquer/main.go` (`:697`; `fleet
diff` is the existing positional sub-subcommand, `:705-712`, so `fleet slots
[--write] [--check]` follows that pattern; `--roster` and `$LACQUER_ROSTER`
already exist).

The algorithm, exactly (design §3): take the roster repos whose manifest has
an iOS/macOS profile. Order them by FNV-1a 32 of lowercased `owner/name`.
The k-th gets day `k mod 7` and time `["06:15","08:15"][k div 7]`; beyond 14
repos, add `"10:15"`. A repo whose manifest already has a slot KEEPS it,
and new repos fill the free slot on the least-loaded day.
`--check` exits non-zero if two Mac repos share a slot. `--write` edits each
repo's `.lacquer.toml` (only the `[weekly]` table) on disk; it does not
commit. Tests: a fixed fake roster of 16 names, with a golden table; adding
a 17th name moves nobody; `--check` catches a planted collision.

The operator's applied table lives in the operator's fleet repo (private). Do not commit the
real table here.

---

## Unit 6 — Weekly failure reporting

Design §4 and D7, reworked 2026-10-19. **The workflow makes NO GitHub API
call**; the template's rule that no job talks to the GitHub API
(`profiles/ios/workflows/ci.yml:19-20`) stands, and the workflow gets no
`issues: write`. The producer, which runs locally as the operator, raises the
inbox ACTION and the issue. There is no in-CI bisect.

### Task 6.1: The weekly result artifact

In the weekly (`schedule`) run, after tests, a step writes
`weekly-result.json` as an artifact and nothing else:
`{"sha": ..., "mode": "full", "failing": ["Target/Suite/test", ...],
"flake_check": "passed" | "failed" | "not run"}`. Failing test identifiers
come from the xcresult. The optional flake check reruns only those tests once,
on the same commit, **only if it fits the job's limits**: the Test job has
`timeout-minutes: 20` (`ci.yml:1412`) and its Run Tests step 10
(`ci.yml:1910`), so size the rerun against them and write `not run` rather than
extend the job. No new job (rule 5); no checkout of another commit; no
`gh`/API step. A test pins both: the weekly path has no step that calls the API
and `permissions` is still `contents: read` (the Unit-5 widening on `changes`
is the only exception, and it is on a different job).

### Task 6.2: `HarvestWeekly` producer

**Files:** `internal/producers/weekly.go`, `internal/producers/weekly_test.go`;
call it next to `HarvestMerges` in `internal/console/console.go:155` and
`internal/inboxwatch/env.go:615` (it was `:605` when this plan was written).
The producers doc now lists a fourth producer, `autoclear.go` (#500); the rule
that producers use only the existing entry types and fields still holds
(`internal/producers/producers.go:20-21`).

Mirror `HarvestMerges` exactly: roster repos, a cursor file, a scripted
runner in tests, idempotent per ref, and append before advancing the cursor.
For each repo and profile CI workflow:
- latest `schedule` run red → ACTION `Weekly full suite red: <owner/repo>`,
  ref = run URL. Its body names the failing test (from `weekly-result.json`,
  downloaded with the operator's `gh`), whether the flake check ran and what it
  said, and **the week's merges**: the first-parent merges between the last
  green weekly's SHA and the failing SHA, with each one's `test-selection.json`
  selection where there is one. Suspects (the merges whose selection did not
  include the failing target) are marked. That list is the input to an
  **on-demand bisect**; the producer never bisects;
- the same run opens or updates ONE issue per repo labelled `weekly-suite`
  (failing test IDs, flake result, the merge list) and closes it on the next
  green weekly, using the operator's `gh`;
- no `schedule` run in more than 8 days while the manifest has a slot →
  ACTION `Weekly full suite did not run: <owner/repo>`, ref
  `<repo>#weekly-missing-<ISO week>`.
Use only the existing entry types and fields: the phone mirror reads this
file, and a new field breaks it silently (see the producers package doc).

**Tests:** red → 1 ACTION and 1 issue; red twice in one harvest → still 1 of
each; green after red → issue closed, no ACTION; missing for 9 days → 1
ACTION; missing for 7 days → none; a repo with no slot → none; a red run with
no `weekly-result.json` → an ACTION that says the result was unreadable, never
silence. Mutation: drop the slot check → "repo with no slot" fails by name.

---

## Unit 7 — Pilot and measurement

iOS only: one iOS repo with local packages (named in the operator's private
brief, never here). Arranged with the pilot's maintainers when this unit is reached
(decided 2026-10-19).

1. **Sync the pilot to the last release before Unit 3 ships, first.** It is
   not on a recent release today, so its jump to the current templates changes
   what it runs (coverage gate, watch job, package builds, the Unit 1
   consolidation, Unit 5's reuse). That jump must not be confused with the
   effect of selection.
2. **Re-measure "before" on the pilot's next ~10 PRs** after that sync. The
   10-04 numbers are superseded (design §1) and are not used.
3. Sync the pilot to the release that contains Units 3–6, and apply its weekly
   slot from the private table.
4. For one week, collect billed minutes per PR and per push-to-main with the
   §1b rules, from cached API reads (rate-limit rules in the operator's
   brief).
5. Report before and after: median and mean billed per PR, Mac slot-min and
   Blacksmith billed separately, the share of push runs that reused, whether a
   weekly run fired in its slot and what it found, and, for the open
   question, whether `Run Tests` shrank.
6. Propose the fleet sync with those numbers. The operator
   schedules it.

---

## Later: web

Out of this project's scope (decided 2026-10-19: iOS only). Kept so it can be
picked up without redoing the analysis. See the design's "Later: web" for the
reasoning.

- **Old Task 2.5, `Select` for web.** A root file (`package.json`,
  `pnpm-lock.yaml`, `turbo.json`, `tsconfig.base.json`, `biome.json`, root
  `vitest.config.*`, `.env.schema`) → FULL. A change inside `apps/admin/` →
  selected `["...[<base>]"]`: a single filter, letting turbo compute
  dependents. Docs-only → NONE. The Result carries the filter, not package
  names, because the graph is turbo's job. **Stale as written:** the "pnpm
  workspaces without turbo" branch does not exist. The web test step is turbo
  or a single app (`profiles/web/workflows/ci.yml:597-603`).
- **Old Task 3.4, the web pre-push hook (#507).** The hook calls `lacquer
  select-tests --profile web --base origin/main --head HEAD`, runs nothing for
  NONE and the filter for selected. It is still an unconditional `turbo run
  test` (`profiles/web/root/lefthook.yml:97-106`). Test it through real git,
  following `internal/shipped/hooks_under_git_test.go`.
- **Old Task 3.1/3.2 web wiring:** "Test (coverage)" runs `turbo run test
  --filter="$FILTER"` when selected, today's command when full, skipped when
  none.
- **Linux weekly slots (05:15 UTC, stride 3 days)** and `LinuxSlot` in
  `[weekly]`.
- **The web pilot (D8).** A turbo web repo was to be the second pilot. It also
  falls outside the current active scope. Web selection saves Blacksmith
  minutes, not Mac time, and the saving is small.

---

## Order and dependencies

Unit 1 (shipped) → **Unit 5** → Unit 2 → Unit 3 → Unit 4 → Unit 6 → Unit 7.
Unit 5 needs only Unit 1. Unit 2 needs nothing but can wait behind Unit 5.
Units 3, 5 and 6 each need the previous unit merged **and released** (a lacquer
merge auto-releases), because the workflows download the released binary. Unit 3
additionally needs Unit 5's run record, and the pilot synced to the last
release before it ships (Unit 7, step 1).
