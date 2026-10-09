# Selective tests per PR, full suite weekly — design (2026-10-04)

Status: **DESIGN APPROVED 2026-10-04** (every decision as recommended; see §8).
Nothing is built yet.

**Public repo rule:** nothing fleet-specific (repo names, measured numbers,
findings about a particular repo) goes in lacquer, not even on a branch. That
material lives in the operator's private fleet repo. The step-by-step implementation plan
(superpowers:writing-plans) is written after they are answered, as
`2026-10-04-selective-tests-implementation.md`.

The operator, verbatim (2026-10-04):

> "deterministic tests. we shouldn't burn all this ci time running all of the
> tests on all the prs. we should run the ones that re needed and then just
> schedule a weekly full suite run."

> "weekly needs to be stagged and scattered so we don't bottleneck the runners"

## 1. Baseline: how CI cost is measured, and what the measurement showed

The fleet's measured numbers (per repo, per workflow, per runner class) are
operator data, so they live in the operator's private fleet repo, not in this
public one. What follows is the method, and the findings that shaped the
design.

**Method.** Four weeks of GitHub Actions run and job records, priced as
**billed minutes**:
- each job rounds UP to a whole minute;
- Blacksmith is vCPU-weighted: 4vcpu-x64 bills 4 per minute, and 2vcpu-arm
  bills 2 × 0.625 = 1.25 per minute (the same model the templates' runner
  comments use);
- a self-hosted Mac job counts its own rounded minutes as runner-slot
  minutes, per JOB rather than per run;
- the self-hosted `pi-gate` runners are free.

The operator, verbatim: "one gotcha to watch. we burn a lot of ci minutes for
jobs with shorter wall clock minutes. that's costly and something we need to
be mindful of". So wall clock is reported, but never used as the measure.

**Findings that shaped the design:**
1. **Short jobs on 4-vCPU Blacksmith runners bill many times their wall
   clock.** Across Blacksmith as a whole, billed minutes were a large multiple
   of minutes worked; Mac jobs were close to 1:1. The worst offenders are
   coordination jobs of a few seconds (table below).
2. **Re-running the full iOS pipeline on every push to main is a large share
   of Mac time.** That push re-tests a tree its PR usually just passed. This
   is D4.
3. **Inside an iOS Test job, compiling and running the tests is well under
   the whole job.** SPM cache, XcodeGen, LFS and simulator setup take the
   rest, and that part is the same whatever is selected.
4. **About half of merged iOS PRs touch a fail-safe path** (pbxproj,
   project.yml, lacquer-managed files, Package.*), so they must run the full
   suite. App-only PRs are the next largest class, and package-only PRs are
   rare. Because the local packages feed the app, a package change pulls the
   app's tests back in. **Path-based selection therefore trims the iOS test
   step modestly. Job consolidation and push-to-main reuse are the larger
   levers.** The pilot measures all three.
5. Supabase already runs pgTAP only when `supabase/` changes. Web selection
   saves Blacksmith minutes, not Mac time.

### 1b. Jobs whose billed minutes far exceed their wall clock

From the lacquer templates (repo-local workflows are reported to their own
PMs):

| template job | runner today | wall | billed per run | proposal |
|---|---|---|---|---|
| all profiles · `changes` (Detect changed paths) | 2vcpu-arm (iOS, web); **4vcpu-x64 (supabase)** | seconds | 1.25; **4** | move to `pi-gate` (free): it is git + grep + the arm64 lacquer binary, and CI OK already depends on pi-gate |
| iOS · `lint` (Mac) | Mac | seconds | 1 slot + checkout | fold `baseline` into it as steps |
| iOS · `baseline` (Mac) | Mac | seconds | 1 slot + checkout | folded into `lint`: one Mac job and one runner pickup fewer per run |
| supabase · `check` (Format · Lint · Test) | 4vcpu-x64 | seconds | 4 | 2vcpu-arm |
| supabase · `lint-database` + `test-database` | 4vcpu-x64 × 2 | ~1 min each | 8 | ONE job: one local-stack boot instead of two |
| supabase · `deploy-database` | 4vcpu-x64 | seconds | 4 | 2vcpu-x64 (the Supabase CLI stays on x64) |
| supabase · health `ping` (daily) | 4vcpu-x64 | seconds | 4 | `pi-gate` (a curl) |
| web · `dependency-review` (own workflow) | 4vcpu-x64 | seconds | 4 | a step in `changes`, run only when a manifest or lockfile changed |
| web · `env-validation` (own workflow) | 4vcpu-x64 | seconds | 4 | a step in `changes` |
| release · `verify-ci-provenance` + `select-products` + `notify-on-failure` | 2vcpu-arm × 3 | seconds | 3.75 | `pi-gate`, as one job (provenance + select) plus notify |

None of these merges is meant to lose a required check name. The template's
own comment records which names branch protection requires. Unit 1 re-runs
`lacquer protection --roster` and reads the result before rendering, rather
than trusting a paragraph.

**Unverified, and verified first in unit 1:**
- that the pi-gate runners can run `changes`: an arm64 userland the
  linux_arm64 lacquer binary executes on, `git`, network, and enough capacity
  for every CI run in the fleet to start there;
- what happens when pi-gate is down. Today that blocks only CI OK; after this
  change it also blocks `changes`, the first job of every run. That is the
  same single point of failure, reached sooner. If either proof fails,
  `changes` stays on 2vcpu-arm and the rest of the unit still ships.

**Success metric:** billed minutes per PR (Mac slot-min and Blacksmith billed,
reported separately), median and mean, before and after, measured on the
pilot repos' real PRs over a week. Push-to-main billed minutes per merge are
reported alongside, for D4. The numbers go to the operator's private repo.

## 2. Selection: changed paths → test selectors, deterministic

**One engine, in lacquer:** `lacquer select-tests --base <sha> --head <sha>
--profile ios|web` prints JSON:
`{mode: full|selected|none, selectors: [...], reasons: [...]}`. It is a pure
function of the diff's path list, `.lacquer.toml`, and files read from the
head tree (`Package.swift` manifests, `project.yml`). So the same diff gives
the same set every time, and Go table tests can pin it (§5). It runs **as a step in
the existing `changes` job; no new job, for selection or for gating** (see
§1b), which already downloads the released lacquer binary
for the drift audit. Today that download happens only when the `lacquer`
filter fires; under this design it happens on every run. That costs a few
seconds inside a minute that is already billed.

### iOS: target-level selection, no file→suite mapping

The selectable unit is a **test target**, the same name `-only-testing:`
takes. lacquer already models these targets: `test_target`, `ui_test_target`,
`extra_test_targets` and `watch_tests`, checked against the scheme's
TestAction by `internal/testtargets`. Swift Testing tags and file→suite maps
are rejected: they are not derivable deterministically, and a stale map fails
silent.

Units and the rules that select them:

1. **Local SPM package P** (a directory with `Package.swift` that the project
   references by path): its test targets are the entries of
   `extra_test_targets` whose source lives under P.
   - Change under `P/Tests/**` → P's tests only. Tests are not imported by
     anything.
   - Any other change under P → P's tests, plus the tests of every local
     package that depends on P (transitively, from the `.package(path:)`
     edges), plus **all app-side targets**. The app is assumed to depend on
     every local package; we never try to prove it does not.
2. **App side**: everything outside the local packages and outside the
   fail-safe list. → `test_target`, `ui_test_target`, the non-package
   `extra_test_targets` (widgets, for example) and the watch tests. App-side
   changes never select package tests, because packages cannot import the
   app.
3. **No-test paths**: today's docs deny-list (`*.md`, `docs/**`, `LICENSE`,
   `.gitignore`) **plus agent tooling** (`.claude/**`, `.agents/**`,
   `.codex/**`, `CLAUDE.md`, `AGENTS.md`, `.mcp.json`). Nothing that builds or
   tests reads these, and today a skills-only lacquer sync wakes the full
   suite.
4. **Fail-safe → FULL** (built in; a repo can add to it with
   `[tests].full_on = [...]` and can never remove from it):
   `*.xcodeproj/**`, `project.yml`, `Package.swift`, `Package.resolved`,
   `*.xcconfig`, `*.xctestplan`, `*.entitlements`, `Info.plist`,
   `.github/**`, `scripts/**`, `.lacquer.toml`, `.lacquer.lock`,
   `.swiftlint*`, `.swiftformat`, `Brewfile`, `lefthook.yml`,
   `Secrets.xcconfig.example`, any shared test-support directory
   (`*TestSupport*/**`, `*Fixtures*/**`, `TestHelpers/**`), **and any path no
   rule above claims.**
5. **Invariant:** when `code == true`, the result is never `none` and never an
   empty selector list. An empty computation becomes `full` with the reason
   "selection empty". A rename or deletion considers both of its paths.

`[[tests.map]]` (`paths = [...]`, `targets = [...]`) is an optional override
for odd layouts. Every target it names must exist in the scheme. `lacquer
audit` checks this, reusing `testtargets`.

**Wiring.** `ci.yml`'s Test job takes the selectors from the `changes` job's
output instead of the rendered `{{IOS_CI_ONLY_TESTING}}`. In `full` mode it
passes exactly today's selector list. **"Verify Test Selectors Matched" then
renders for every iOS project, not only those with `extra_test_targets`**:
with selection, a selector that matches nothing is a live risk on every
repo, so the opt-in no longer holds.

**Open empirical question for the pilot:** whether `-only-testing:` cuts
COMPILE time, or only run time, in these projects. xcodebuild may still build
every test target in the scheme. The pilot measures the `Run Tests` step on
the pilot iOS repo with and without the package selectors. If only run time drops, the
saving is the smaller end of the estimate, and we say so.

### Web

- turbo repos: `turbo run test --filter='...[<base>]'`. The `...` prefix pulls
  in dependents.
- pnpm workspaces without turbo: `pnpm -r --filter '...[<base>]' test`, the
  same graph semantics.
- Fail-safe → full: root `package.json`, `pnpm-lock.yaml`, `turbo.json`,
  `tsconfig*.json`, `biome.json`, `vitest.config.*` at the root, `.env.schema`
  and `.github/**`.
- Lint, typecheck and build stay as they are. Only the test step is selected.
  (#507, the web pre-push hook that runs full turbo on docs-only pushes,
  folds in here: the same selector drives the hook.)

### Supabase

Already selected: pgTAP and Splinter run only when the `db` filter fires.
Nothing changes except the weekly run (§3).

## 3. What still runs everything

| trigger | runs | why |
|---|---|---|
| PR | selected (§2) | the point of the project |
| **weekly slot** | FULL, every profile | the backstop for whatever selection skipped |
| push to main | **D4**: full, unless the identical tree already passed the full suite on its PR | keeps "every main commit's tree passed the full suite" |
| release tag | the release SHA must carry a FULL green CI OK from the iOS CI workflow (**D5**) | ios-release provenance must not accept a selected run |
| lacquer sync PR | full, through the fail-safe list (`.github/**`); a skills-only sync is no-test | a CI-config change has to prove itself on everything |
| `workflow_dispatch` | full (`inputs.full`, default true) | manual means "check everything" |

### Weekly: staggered and scattered (operator requirement)

- **One slot per repo per profile**, stored in `.lacquer.toml` as
  `[weekly] slot = "Tue 06:15"` (UTC) and rendered into the cron line with a
  comment that names the slot. A `schedule:` trigger goes on the existing
  `ci.yml`, so the weekly run is the same jobs and the same CI OK, in `full`
  mode.
- **Assignment is deterministic.** `lacquer fleet slots --roster <fleet.toml>`
  orders repos by FNV-1a hash of `owner/name` and deals them round-robin
  across the 7 days: the k-th repo gets day k mod 7 and time slot k div 7. It
  writes the result into each repo's `.lacquer.toml` once. After that, the
  file is the source of truth, so adding a repo never moves anyone else. A
  new repo takes the first free slot on the least-loaded day. `lacquer fleet
  slots --check` fails if two iOS repos share a slot.
- **Mac slots: 06:15 and 08:15 UTC**, 2 hours apart, in the US-Eastern
  small hours, clear of the Mac's daily `Cleanup CI Runner`
  cron at 07:00 UTC. A full iOS run takes minutes, not hours, so two slots
  can never overlap even if one queues. In the measured run history this was
  the quietest band on the Macs, with almost no releases; releases cluster in
  the operator's working hours. Minutes are :15, not :00, because
  GitHub delays top-of-hour schedules under load.
- **Linux slots (web/supabase): 05:15 UTC**, scattered across the days by the
  same hash order with a stride of 3 days. These may overlap each other.
- **No pile-up:** the weekly run uses its own concurrency group,
  `weekly-<repo>-<profile>`, with `cancel-in-progress: false`. GitHub keeps
  at most one pending run per group, so a slot that lands while the runners
  are busy queues behind them, and a late one never stacks.
- **Absence is a finding:** a repo with no weekly run in the last 8 days
  raises an ACTION (§4). GitHub disables schedules on repos with no activity
  for 60 days, and a missing weekly run must not look the same as a green
  one.

**The applied slot table** for the current roster lives with the roster, in
the operator's private fleet repo, because lacquer is public and the roster is
not. Two Mac slots per night give 14 a week; a 10:15 UTC slot is added if the
roster outgrows them. A repo with several profiles gets one slot per runner type.
A macOS app (as opposed to an iOS app) still takes a Mac slot.

## 4. When the weekly run fails

**Who hears.** A GitHub workflow cannot write the operator's inbox: the
self-hosted runners run as a different macOS user (see
`internal/producers`). So this follows the existing `HarvestMerges` pattern.
A new producer, **`HarvestWeekly`**, runs when the console reads. For each
roster repo it finds the latest `schedule` run of each CI workflow since its
cursor:
- red → one ACTION, `Weekly full suite red: <repo>`, whose body names the
  failing tests and the suspects (below). It is idempotent per run id.
- missing for more than 8 days → one ACTION, `Weekly full suite did not
  run: <repo>`.

In the same run, the weekly job opens or updates ONE issue per repo labelled
`weekly-suite`, holding the failing test IDs and the suspects, and closes it
the next time the run is green. This needs `issues: write` on the scheduled
trigger only (D7).

**Tying the failure to a PR, deterministically and cheaply first:**
1. Every PR run uploads its selection (`test-selection.json`: PR number,
   tested tree, mode, selectors) as a small artifact.
2. **Suspects** = PRs merged since the last green weekly whose selection did
   NOT include the failing test's target. A PR that ran that target and
   passed is cleared. Usually this leaves zero or one suspect.
3. **Flake check:** the weekly job re-runs only the failing tests
   (`-only-testing:Target/Suite/test`), once, on the same commit. If they pass,
   the ACTION says "flaky: <test>", not "regression".
4. If more than one suspect remains: an automatic bisect re-runs only the
   failing tests on the suspects' merge commits. It is capped at 3 reruns (8
   suspects), runs inside the same slot, and names the first failing merge.
   If the cap is reached, the ACTION lists the remaining suspects.

## 5. Proof the selection can fail (lacquer's own tests)

Go table tests in `internal/selecttests`. Each fixture is a small repo tree
(a `.lacquer.toml`, `Package.swift` files with `.package(path:)` edges, an
app dir) plus a diff. These cases MUST hold:

- a change in `CoreKit/Sources` selects CoreKitTests, DataKitTests (which
  depends on CoreKit) and the app targets; dropping any one fails the test;
- a change in `CoreKit/Tests` selects CoreKitTests only;
- an app-only change selects the app targets and never a package's tests;
- every fail-safe path, and an **unmapped path** (`Weird/new.thing`), selects
  FULL;
- `code == true` with an empty computed selection gives FULL, never `none`;
- deleting or renaming a test target gives FULL;
- a `[[tests.map]]` naming a target the scheme lacks makes `audit` fail;
- **mutation guard:** each test reruns with the fail-safe list emptied and
  asserts that the "unmapped path" case now FAILS. This proves the assertion
  is live, not vacuous.

Plus a rendered-workflow test: `Verify Test Selectors Matched` renders for
every iOS product, and the selector source is the `changes` output.

## 6. The merge gate

CI OK keeps every check it has today, and asserts, newly:
1. `changes` produced a selection whose mode is `full` or `selected`, with at
   least one selector, whenever `code == true`. A missing or unparsable
   selection fails CI OK, and so does `none` while `code == true`.
2. When tests were selected, the `test` job's result is `success`. **A
   `skipped` test job with `code == true` now FAILS CI OK.** Today a skip
   counts as OK, which is exactly the "nothing ran" hole selection would
   widen.
3. "Verify Test Selectors Matched" passed: every selector ran at least one
   test bundle, read back from the xcresult, failing closed.
4. CI OK's step summary prints the mode, the selectors and the reasons, so a
   reviewer sees what was NOT tested.

The 2-round cap (`ci-round`) is unchanged. A round is a push. Selection only
makes each round cheaper.

## 7. Phase 2 units (one IC at a time; detailed after approval)

1. **Opus**: `lacquer select-tests` engine with the §5 fixtures, plus the CI
   OK gate (§6) and the provenance fix (D5, closes #508).
2. **Sonnet**: template plumbing: `ci.yml` iOS/web wiring, the schedule
   trigger, slot rendering, `lacquer fleet slots`, the selection artifact.
3. **Opus**: `HarvestWeekly` producer, the weekly issue, flake check and
   bounded bisect.
4. **Sonnet**: pilot sync to one iOS + supabase repo with local SPM packages and one
   turbo web repo (named in the operator's private brief)
   (web), then measure before and after on real PRs for a week, then propose
   the fleet sync.
5. **Opus**: tree-reuse on push to main (D4 = b).
6. **Sonnet**: job consolidation from §1b (`changes` → pi-gate, baseline
   folded into lint, supabase right-sizing and DB jobs merged, web
   dependency-review/env-validation as steps, release small jobs). This is
   template-only, can ship BEFORE units 1–5 and is measured by itself. It is
   probably the largest billed saving in the project.

## 8. Decisions

Answers, as recorded (2026-10-04). the operator approved D1–D3 and D6–D8 as
recommended. The operator approved D4, D5 and D9, verbatim: "yeah those
reccomendations all sounds good". So **D4 = (b), D5 = yes, D9 = yes**. The
operator's billed-minutes constraint is quoted in §1b and governs every unit.

- **D1 Selection granularity (iOS).** Target-level, from the package graph.
  No Swift Testing tags, no file maps. *Recommended.*
- **D2 Fail-safe list** as in §2.4, built in and extend-only. *Recommended.*
- **D3 Agent-tooling paths count as no-test**, like docs. *Recommended.*
- **D4 Push to main** (changes what protects main → operator):
  (a) always full, as today, which keeps the push-to-main cost;
  (b) **full unless the PR's last green run was FULL on the byte-identical
  tree (`HEAD^{tree}` matches); then reuse that result and spend no Mac**.
  *Recommended*: every main tree still has a full green suite, and the reused
  PRs are exactly the fail-safe ones (about half of all PRs);
  (c) selected against the previous main commit, which leaves main trees
  with no full run between weeklies.
- **D5 Releases** (→ operator): the release SHA needs a FULL green CI OK from
  the iOS CI workflow by name. With D4(a/b) that holds by construction.
  Otherwise `ios-release` dispatches a full run and waits. Fixes #508 in the
  same change. *Recommended.*
- **D6 Weekly slots** as in §3: 06:15 and 08:15 UTC on Mac, 05:15 UTC on
  Linux, stored in `.lacquer.toml`. *Recommended.*
- **D7 Weekly failure**: an inbox ACTION through the HarvestWeekly producer,
  plus one `weekly-suite` issue per repo (needs `issues: write` on the
  scheduled trigger), a one-shot flake rerun, and a bisect capped at 3.
  *Recommended.*
- **D8 Web scope**: test step only, turbo/pnpm `...[base]`. Small saving,
  piloted on one turbo web repo; #507 folds in. *Recommended.*
- **D9 Gate change**: a skipped test job with `code == true` fails CI OK;
  "Verify Test Selectors Matched" renders fleet-wide. *Recommended*
  (→ operator: it changes what protects main).
