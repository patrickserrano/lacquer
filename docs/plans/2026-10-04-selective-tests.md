# Selective tests per PR, full suite weekly — design (2026-10-04)

Status: **DESIGN APPROVED 2026-10-04** (every decision as recommended; see §8).

**Status 2026-10-19.** Unit 1 (job consolidation) shipped (#514), with the
deviations recorded in the implementation plan. Nothing from the selection
engine onward is built. The points below were decided on 2026-10-19;
they are marked "decided 2026-10-19" where they change this document, and
quoted in full in §8.

- **New unit order:** push-to-main tree reuse (D4b) and release provenance (D5)
  ship next, as ONE unit, before the selection engine. Without selection every
  PR run is FULL, so that unit can be proven and measured on its own. Then the
  engine, the wiring and gate, weekly slots, weekly failure reporting, the pilot.
- **Scope is iOS only.** Web selection is moved to "Later: web" at the end of
  this document; the analysis is kept.
- **Paths owned by a separately declared non-Swift component** select no iOS
  tests only when that component's own profile CI is a required check (§2).
- **The coverage ratchet gate** runs only when every app test target ran (§2).
- **Weekly failure reporting** (D7) is reworked: the workflow makes no GitHub
  API call and the producer raises the ACTION and the issue (§4).
- **The 10-04 baseline numbers are superseded** for before/after use (§1).

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

**Superseded for before/after use (2026-10-19).** The 10-04 measurements
describe the templates as they were on 10-04. They are not the "before" for
any later unit, for two reasons:

1. CI work has landed since that changes the Mac and Linux work per run in both
   directions. Unit 1 removed the Baseline Mac job and made `changes` free; the
   coverage ratchet gate, the watch job on a pinned runtime, multi-component
   Swift and package builds in Lint, and the release-time embedded-bundle and
   archived-plist checks added work; disabling Homebrew auto-update and the
   test-diagnostics setting removed some. Any one of these moves Mac slot-min
   per PR by about as much as the selection estimate.
2. Most consumers have not synced past the release that predates Unit 1, so
   what they run today is not what a freshly synced repo will run, and a
   repo that jumps many releases at once confounds the comparison.

So "before" is re-measured by Unit 7, on the pilot's next ~10 PRs after it is
synced to the last release before selection ships (§7). The qualitative
findings below still hold in shape; the numbers are not to be reused.

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
projects):

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
| release · `verify-ci-provenance` + `select-products` + `notify-on-failure` | 2vcpu-arm × 3 | seconds | 3.75 | `pi-gate`, as one job (provenance + select) plus notify. **Not shipped** (see the plan's Unit 1 record): they stay on a hosted runner |

None of these merges is meant to lose a required check name. The template's
own comment records which names branch protection requires. Unit 1 re-runs
`lacquer protection --roster` and reads the result before rendering, rather
than trusting a paragraph.

**Unverified, and verified first in unit 1** (resolved 2026-10-19: Unit 1
shipped, and `changes` runs on pi-gate in all three profiles,
`profiles/ios/workflows/ci.yml:72`):
- that the pi-gate runners can run `changes`: an arm64 userland the
  linux_arm64 lacquer binary executes on, `git`, network, and enough capacity
  for every CI run in the fleet to start there;
- what happens when pi-gate is down. Today that blocks only CI OK; after this
  change it also blocks `changes`, the first job of every run. That is the
  same single point of failure, reached sooner. If either proof fails,
  `changes` stays on 2vcpu-arm and the rest of the unit still ships.

**Two facts Unit 1 left behind, which later units must respect.** The pi-gate
label is carried by runners of two different architectures (one aarch64, one
x86_64; `ci.yml:55-71`), so a step that downloads a lacquer binary there maps
`uname -m` to the asset and never hardcodes `linux_arm64`. And the release
workflow's three small jobs did NOT move to pi-gate (the plan's Task 1.6): the
provenance job stays on a hosted runner, pinned as a property by
`internal/shipped/release_provenance_test.go:52-60`.

**Success metric:** billed minutes per PR (Mac slot-min and Blacksmith billed,
reported separately), median and mean, before and after, measured on the
pilot repos' real PRs over a week. Push-to-main billed minutes per merge are
reported alongside, for D4. The numbers go to the operator's private repo.

## 2. Selection: changed paths → test selectors, deterministic

**Scope (decided 2026-10-19): iOS only.** Web selection is out of this
project's scope and is kept as a note in "Later: web" at the end.

**One engine, in lacquer:** `lacquer select-tests --base <sha> --head <sha>
--profile ios` prints JSON. It is a pure function of the diff's path list,
`.lacquer.toml`, and files read from the head tree (`project.yml` or the
tracked pbxproj, and the `Package.swift` manifests they reference). So the
same diff gives the same set every time, and Go table tests can pin it (§5).
It runs **as a step in the existing `changes` job; no new job, for selection
or for gating** (see §1b). That job already downloads the released lacquer
binary for the drift audit, resolving the version from `.lacquer.lock` rather
than taking the latest (`profiles/ios/workflows/ci.yml:253-256`), and today
only when the `lacquer` filter fires; under this design it happens on every
run. That costs a few seconds inside a minute that is already billed (and
`changes` is on pi-gate, which is free). The download must map `uname -m`,
because pi-gate carries two architectures (`ci.yml:55-71`).

**The result is per product, and watch tests are listed separately.** A
project can declare several `[[product]]`s, each a matrix leg with its own
`test_target`, `ui_test_target` and extras (`internal/tokens/tokens.go:610`,
`CITestStrategy`, with the env at `:619-625`), and the watch bundle runs in its
own `watch-test` job (`internal/tokens/watch.go:180`), not in the leg's
`-only-testing:` list. `config.Product` keeps the two lists apart on purpose
(`WatchTestSelectors`, `internal/config/config.go:1539`; `TestSelectors`,
`:1569`). So:

```
{mode: full|selected|none,
 reasons: [...],
 products: [{name, selectors: [...], app_targets_ran: bool}],
 watch: {selectors: [...]}}
```

`full` for a product is exactly its `TestSelectors()`, and `full` for the watch
list is exactly its `WatchTestSelectors()`, so a FULL result renders the
workflow's present selector lists. `app_targets_ran` is what the coverage gate
below reads.

**A locked binary that predates `select-tests` means FULL.** The step runs the
binary at the project's locked version (`.lacquer.lock`), so a repository that
has not synced since the subcommand shipped has no `select-tests` to run. That
must resolve to FULL, never to an error and never to a skipped test job. Both
causes are covered: an unknown subcommand (non-zero exit) and a locked version
below the first release that carries it.

### iOS: target-level selection, no file→suite mapping

The selectable unit is a **test target**, the same name `-only-testing:`
takes. lacquer already models these targets: `test_target`, `ui_test_target`,
`extra_test_targets` and `watch_tests`, checked against the scheme's
TestAction by `internal/testtargets`. Swift Testing tags and file→suite maps
are rejected: they are not derivable deterministically, and a stale map fails
silent.

**Where the package graph comes from.** Nothing here scans the tree for
`Package.swift`. The engine reuses what lacquer already reads:
- which local packages the project references, and each one's test targets:
  `testtargets.Target.Package` and `PackageDir`
  (`internal/testtargets/testtargets.go:41-53`, built from the project's
  package references in `packages.go:85-106`), read the way the audit reads
  them through `testtargets.ReadForAudit` (`internal/testtargets/watch.go:136`):
  the tracked pbxproj, else `project.yml` through `ParseSpec`
  (`spec.go:121`), never a generated project that happens to be on disk;
- the packages the manifest declares: `Component.Packages`
  (`internal/config/config.go:1683`, "nothing scans for Package.swift") and the
  package components (`[[component]] stack = "ios"`,
  `internal/swiftcomponents/swiftcomponents.go:57-69`, `Packages` at `:211`).

The `.package(path:)` edges between packages are still read from each
manifest, because dependents come from them. A manifest that cannot be read
puts the whole result in FULL.

Units and the rules that select them:

1. **Local SPM package P** (referenced by the project, or listed in
   `packages`): its test targets are its `.testTarget`s.
   - Change under `P/Tests/**` → P's tests only. Tests are not imported by
     anything.
   - Any other change under P → P's tests, plus the tests of every local
     package that depends on P (transitively, from the `.package(path:)`
     edges), plus **all app-side targets**. The app is assumed to depend on
     every local package; we never try to prove it does not.
2. **App side**: everything outside the local packages and outside the
   fail-safe list. → `test_target`, `ui_test_target`, the non-package
   `extra_test_targets` (widgets, for example) and the watch tests (listed
   separately, above). App-side changes never select package tests, because
   packages cannot import the app.
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
   **`.lacquer.ratchet.toml`** (the coverage baseline, `internal/ratchet/ratchet.go:17`;
   unlisted it would fall to "unclaimed", which is FULL by accident rather than
   by rule), `.swiftlint*`, `.swiftformat`, `Brewfile`, `lefthook.yml`,
   `Secrets.xcconfig.example`, any shared test-support directory
   (`*TestSupport*/**`, `*Fixtures*/**`, `TestHelpers/**`), **and any path no
   rule above claims.**
   **Every pattern, in this list and the no-test one, matches under a
   component prefix too** (`App/project.yml`, `App/Foo.xcodeproj/...`, a
   `Package.swift` two levels down): the app can live under
   `{{COMPONENT_PREFIX}}`, so root-anchored patterns would silently stop
   matching in exactly the layouts that have one.
5. **Component-owned paths (decided 2026-10-19).** A path owned by a
   separately declared non-Swift component (a `[[component]]` that is not an
   iOS component, per `swiftcomponents.Components`; the most specific
   component wins, as `swiftcomponents.Group` resolves it) selects NO iOS
   tests, **only when that component's own profile CI is a REQUIRED check**.
   Otherwise the path is FULL. The reason: a no-test result for iOS is safe
   only because something else tests that path before merge, and a check that
   is present but not required can be red on a merged PR.
   - *How "required" is determined (decided 2026-10-19: a committed claim, cross-checked by audit).*
     The authority is the branch's required status-check list, which
     `internal/protection` already reads (`protection.Fetch`,
     `internal/protection/gh.go:126`, into `Requirements.Contexts`,
     `internal/protection/protection.go:107`) and matches against the contexts
     the local workflows post (`protection.Local`, `workflows.go:78`;
     `protection.Compare`, `protection.go:207`). `changes` does NOT read it
     live, for a reason checked rather than assumed: reading classic branch
     protection needs repository ADMIN (lacquer's own reader records this,
     `gh.go:49-57` and `:250-255`, where a non-admin read comes back as 404),
     and `GITHUB_TOKEN` cannot be granted it, because `administration` is not
     among the scopes a workflow's `permissions:` key accepts
     (https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions;
     the REST page for the read, https://docs.github.com/en/rest/branches/branch-protection,
     states no token permission for it, so the admin requirement rests on
     lacquer's observation and is **not confirmed by GitHub's docs**). A live
     read in `changes` would usually error, and an error means FULL anyway. So
     `select-tests` takes the component's required context as a **committed,
     deterministic claim** (a table in `.lacquer.toml` naming the component and
     its CI context), and the claim is proven out of band: `lacquer audit` /
     `lacquer doctor` (which run as the operator, with admin) compare it with
     the live required list through the protection code above. **A claim the
     live list does not back raises an inbox ACTION, not a log line.** The rule
     is not negotiable: **if "required" cannot be determined (no claim, an
     unreadable claim, a claim the live list does not back), the answer is
     FULL.** Possible LATER upgrade only: where a repository uses rulesets,
     `GET /repos/{owner}/{repo}/rules/branches/{branch}` may be readable at
     read scope, which would allow a live read; its permission requirement was
     not confirmed from GitHub's docs, so nothing here depends on it.
   - *What the result says.* A result that selects nothing because every
     changed path was component-owned is `none` with `owned_by` naming each
     component relied on. It is distinct from a docs-only `none` so the merge
     gate (§6) can tell the two apart.
   - *Not covered by this rule:* a Swift package component that the app
     project does not reference. It is a Swift component, so rule 1 applies to
     it, and a package nothing references selects its own tests only.
6. **Invariant:** when `code == true`, the result is never `none` and never an
   empty selector list, **except** the component-owned `none` of rule 5. An
   empty computation becomes `full` with the reason "selection empty". A
   rename or deletion considers both of its paths.

`[[tests.map]]` (`paths = [...]`, `targets = [...]`) is an optional override
for odd layouts. Every target it names must exist in the scheme. `lacquer
audit` checks this, reusing `testtargets`. A target listed in
`[[project.not_run_in_ci]]` (`internal/config/config.go:220`) is never a
selector.

**The coverage ratchet gate under selection (decided 2026-10-19).** The Test
job's coverage gate reads the APP target's uncovered lines against an 80% floor
and, on a PR, also fails an improvement that was not recorded
(`profiles/ios/workflows/ci.yml:2231-2297`, the verdict in
`internal/ratchet/coverage.go:153`). A selected run that skips the app's unit
tests would measure the app target as uncovered or absent and go red for a
reason unrelated to the change. So the gate runs only when **every app test
target of that product ran** (`app_targets_ran`: its `test_target`,
`ui_test_target` and non-package extras; the watch bundle has its own job and
result and is not counted). Otherwise the step does not run the ratchet, and
it prints

```
skipped: partial selection
```

on the step and **in the job summary** (`$GITHUB_STEP_SUMMARY`). It is never
rendered as a pass, never green by omission: the reviewer sees that coverage
was not evaluated on this run. A package-only selection never needs the app's
coverage, because packages cannot import the app; the weekly full run and the
push run give the authoritative number. An un-enrolled project still warns and
passes exactly as today (`internal/ratchet/coverage.go:166-176`).

**Wiring.** `ci.yml`'s Test job takes the selectors from the `changes` job's
output instead of the rendered `{{IOS_CI_ONLY_TESTING}}`. In `full` mode it
passes exactly today's selector list. **"Verify Test Selectors Matched" then
renders for every iOS project, not only those with `extra_test_targets`**:
with selection, a selector that matches nothing is a live risk on every
repo, so the opt-in no longer holds. The `watch-test` job's `if:`
(`internal/tokens/watch.go:199`) consumes the watch list, and CI OK covers it
too (§6).

**Open empirical question for the pilot:** whether `-only-testing:` cuts
COMPILE time, or only run time, in these projects. xcodebuild may still build
every test target in the scheme. The pilot measures the `Run Tests` step on
the pilot iOS repo with and without the package selectors. If only run time drops, the
saving is the smaller end of the estimate, and we say so.

### Supabase

Already selected: pgTAP and Splinter run only when the `db` filter fires.
Nothing changes except the weekly run (§3).

## 3. What still runs everything

| trigger | runs | why |
|---|---|---|
| PR | selected (§2) once the engine ships; **FULL until then** | the point of the project |
| **weekly slot** | FULL, iOS | the backstop for whatever selection skipped |
| push to main | **D4b**: full, unless a PR run on the **same tree** was FULL and green (below) | keeps "every main commit's tree passed the full suite" |
| release tag | the release SHA must carry a FULL green CI OK from the iOS CI workflow (**D5**) | ios-release provenance must not accept a selected run, or another workflow's check |
| lacquer sync PR | full, through the fail-safe list (`.github/**`); a skills-only sync is no-test | a CI-config change has to prove itself on everything |
| `workflow_dispatch` | full (`inputs.full`, default true) | manual means "check everything" |

### Push to main: tree reuse (D4b), decided 2026-10-19

The push run reuses a PR run's result only when **all** of these hold:
1. the merge commit's **tree** sha (`git rev-parse HEAD^{tree}`) equals the
   tree sha the PR run recorded for the tree it tested;
2. that PR run was **FULL**, and
3. it concluded **green** (its CI OK succeeded).

Anything else runs FULL: no matching run, a missing or unreadable record, an
API error, a mode other than `full`, a run that was not green. In particular a
merge whose tree differs from what the PR tested, because main moved after the
PR's last run, **runs CI on main**: branch protection's `strict` setting is off
on purpose (the reasoning is in the Test job's own comment,
`profiles/ios/workflows/ci.yml:1387-1403`), so a squash-merged tree is NOT
guaranteed to equal the tree that was tested. Comparing the tree, not the PR
number or the SHA, is what makes reuse safe without `strict`.

**A SELECTED run is never reused.** When the engine ships, a PR run in
`selected` mode records `mode: selected`, and rule 2 refuses it. No other path
can make a selected run reusable: the engine adds only a test that its real
`selected` record is refused by this same rule ("FULL and green on the same
tree").

Reuse needs the GitHub API from a job, which `ci.yml:19-20` forbids ("No job
talks to the GitHub API", with `contents: read` only, `:29-30`). **Accepted
2026-10-19 as a read-only exception, scoped exactly:** `changes` only,
`actions: read` + `pull-requests: read`, the comment amended, and a test that
no other job gained a permission. **Every failure of the lookup means FULL,
never a reuse:** any API error, a rate limit, a timeout, a missing run, or an
ambiguous match (more than one candidate PR or run). Each is a named fail path
that the unit proves.

### Weekly: staggered and scattered (operator requirement)

- **One slot per repo per profile**, stored in `.lacquer.toml` as
  `[weekly] slot = "Tue 06:15"` (UTC) and rendered into the cron line with a
  comment that names the slot. A `schedule:` trigger goes on the existing
  `ci.yml`, so the weekly run is the same jobs and the same CI OK, in `full`
  mode. The `on:` block now has `push.paths` as the single column-0 token
  `{{IOS_CI_PUSH_PATHS}}` (`ci.yml:12-17`), which `internal/retire` strips
  before it parses the block (`internal/retire/retire.go:152-160`); the
  schedule token must keep the template parseable the same way.
- **Assignment is deterministic.** `lacquer fleet slots --roster <fleet.toml>`
  orders repos by FNV-1a hash of `owner/name` and deals them round-robin
  across the 7 days: the k-th repo gets day k mod 7 and time slot k div 7. It
  writes the result into each repo's `.lacquer.toml` once. After that, the
  file is the source of truth, so adding a repo never moves anyone else. A
  new repo takes the first free slot on the least-loaded day. `lacquer fleet
  slots --check` fails if two iOS repos share a slot.
- **Mac slots: 06:15 and 08:15 UTC**, 2 hours apart, in the US-Eastern
  small hours, clear of the Mac's daily `Cleanup CI Runner`
  cron at 07:00 UTC (`profiles/ios/workflows/cleanup-ci.yml:10`). A full iOS
  run takes minutes, not hours, so two slots
  can never overlap even if one queues. In the measured run history this was
  the quietest band on the Macs, with almost no releases; releases cluster in
  the operator's working hours. Minutes are :15, not :00, because
  GitHub delays top-of-hour schedules under load.
- **No pile-up, and why the group is mandatory:** the weekly run uses its own
  concurrency group, `weekly-<repo>-<profile>`, with `cancel-in-progress:
  false`. A schedule run's `github.ref` is `refs/heads/main`, so under the
  existing group key (`${{ github.workflow }}-${{ github.ref }}`,
  `ci.yml:45`) it would **share the push-to-main group**. GitHub keeps at most
  one pending run per group, so a weekly run arriving behind a running push
  would replace a pending push run, which GitHub reports as `cancelled`, the
  silent non-failure `ci.yml:33-45` was written to prevent. With its own group
  a slot that lands while the runners are busy queues behind them, a late one
  never stacks, and it never displaces a push.
- **Absence is a finding:** a repo with no weekly run in the last 8 days
  raises an ACTION (§4). GitHub disables schedules on repos with no activity
  for 60 days, and a missing weekly run must not look the same as a green
  one.

**The applied slot table** for the current roster lives with the roster, in
the operator's private fleet repo, because lacquer is public and the roster is
not. Two Mac slots per night give 14 a week; a 10:15 UTC slot is added if the
roster outgrows them. A macOS app (as opposed to an iOS app) still takes a Mac
slot. Linux slots belong to web selection and move to "Later: web".

## 4. When the weekly run fails

**Reworked 2026-10-19 (D7).** The weekly workflow writes a **result artifact
only and makes NO GitHub API call**; the template's rule that no job talks to
the GitHub API (`ci.yml:19-20`) stands, and the 10-09 ruling that refused a PR
comment for the same reason is not reopened. Everything that needs the API runs
locally, as the operator, in the producer.

**The artifact.** `weekly-result.json`: the run's SHA, the mode (always `full`),
the failing test identifiers read from the xcresult, and, if the job could,
whether a single rerun of exactly those tests on the same commit passed. It is
a file in the run, nothing more.

**Who hears.** A GitHub workflow cannot write the operator's inbox: the
self-hosted runners run as a different macOS user (see
`internal/producers`). So this follows the existing `HarvestMerges` pattern.
A new producer, **`HarvestWeekly`**, runs when the console reads. For each
roster repo it finds the latest `schedule` run of each CI workflow since its
cursor:
- red → one ACTION, `Weekly full suite red: <repo>`, idempotent per run id.
  Its body names the failing test and **lists the week's merges** (the
  first-parent merges between the last green weekly's SHA and the failing
  SHA), with each one's recorded selection where there is one. That list is
  what an **on-demand bisect** starts from; nothing bisects automatically.
- missing for more than 8 days → one ACTION, `Weekly full suite did not
  run: <repo>`.

`HarvestWeekly` also opens or updates ONE issue per repo labelled
`weekly-suite`, holding the failing test IDs and the merge list, and closes it
the next time the run is green. It does so with the operator's own `gh`, so
the workflow needs no `issues: write` and no write permission at all.

**Tying the failure to a PR, deterministically and cheaply first:**
1. Every PR run uploads its selection (`test-selection.json`: PR number,
   tested tree, mode, selectors) as a small artifact (introduced by the
   tree-reuse unit, §7).
2. **Suspects** = PRs merged since the last green weekly whose selection did
   NOT include the failing test's target. A PR that ran that target and
   passed is cleared. Usually this leaves zero or one suspect. The producer
   computes this from the artifacts; the ACTION shows it beside the full list.
3. **Flake check (kept, bounded):** the weekly job may rerun only the failing
   tests (`-only-testing:Target/Suite/test`), once, on the same commit, and
   record the outcome in the artifact; the ACTION then says "flaky: <test>"
   rather than "regression". It runs only if it fits the job's own limits
   (the Test job has `timeout-minutes: 20`, its Run Tests step 10:
   `ci.yml:1412`, `:1910`); if it does not fit, the artifact says "flake check
   not run" and the ACTION says so. This is sized in the unit's brief.
4. **No in-CI bisect (decided 2026-10-19).** It was dropped: each rerun on a
   different commit rebuilds in persistent DerivedData inside the same job, and
   rule 5 forbids a new job. The merge list above is the input to a bisect
   someone starts on demand.

## 5. Proof the selection can fail (lacquer's own tests)

Go table tests in `internal/selecttests`. Each fixture is a small repo tree
(a `.lacquer.toml`, `Package.swift` files with `.package(path:)` edges, an
app dir) plus a diff. These cases MUST hold:

- a change in `CoreKit/Sources` selects CoreKitTests, DataKitTests (which
  depends on CoreKit) and the app targets; dropping any one fails the test;
- a change in `CoreKit/Tests` selects CoreKitTests only;
- an app-only change selects the app targets and never a package's tests;
- every fail-safe path (including `.lacquer.ratchet.toml`), and an **unmapped
  path** (`Weird/new.thing`), selects FULL, and so does each of them under a
  component prefix;
- `code == true` with an empty computed selection gives FULL, never `none`;
- deleting or renaming a test target gives FULL;
- a `[[tests.map]]` naming a target the scheme lacks makes `audit` fail;
- a path owned by a non-Swift component selects no iOS tests when that
  component's profile CI is required, and **FULL when it is not, when the
  required list cannot be read, or when the claim is absent**; the fixture
  where it is present but not required is the one that matters;
- the result is per product, and a watch-only change selects the watch list
  and no product list;
- `app_targets_ran` is false for a package-only selection and true for FULL;
- a locked binary older than the subcommand maps to FULL (a workflow-level
  test with a fake old binary);
- **mutation guard:** each test reruns with the fail-safe list emptied and
  asserts that the "unmapped path" case now FAILS. This proves the assertion
  is live, not vacuous.

Plus a rendered-workflow test: `Verify Test Selectors Matched` renders for
every iOS product, and the selector source is the `changes` output.

The tree-reuse and provenance unit has its own fail paths, written first and
broken on purpose (§7, plan Unit 5): a merge whose tree differs from the PR
run's must run CI on main, and a non-FULL or wrong-workflow "CI OK" must not
let a release through.

## 6. The merge gate

CI OK keeps every check it has today, and asserts, newly:
1. `changes` produced a selection whose mode is `full` or `selected`, with at
   least one selector, whenever the test job is expected (below). A missing or
   unparsable selection fails CI OK, and so does `none` unless the result is
   the component-owned `none` of §2 rule 5 with every owner named.
2. When tests were selected, the `test` job's result is `success`, and so is
   `watch-test`'s when a watch list was selected. **A `skipped` test job (or
   `watch-test`) now FAILS CI OK when the test job is expected.** Today a skip
   counts as OK, which is exactly the "nothing ran" hole selection would
   widen.
3. "Verify Test Selectors Matched" passed: every selector ran at least one
   test bundle, read back from the xcresult, failing closed.
4. CI OK's step summary prints the mode, the selectors and the reasons, so a
   reviewer sees what was NOT tested, including the coverage gate's
   "skipped: partial selection" when it applies.

**"The test job is expected" (D9, corrected 2026-10-19):** `code == true` AND
an Xcode project exists (`xcodeproj == 'true'`) AND the run is not a fork PR
(a same-repo PR, `head.repo.full_name == github.repository`). It is exactly the
condition the Test job and `watch-test` use to run at all
(`ci.yml:1409`, `internal/tokens/watch.go:199`). The 10-04 wording, "a skipped
test with `code == true` fails", would have turned a pre-code component red,
which legitimately has no project to test (`ci.yml:1405-1407`), and a fork PR,
which legitimately must not run on the self-hosted runner. The gate covers
`watch-test` as well as Test, and `test{{IOS_CI_WATCH_NEED}}` already puts both
in CI OK's `needs` (`ci.yml:2379`).

The 2-round cap (`ci-round`) is unchanged. A round is a push. Selection only
makes each round cheaper.

## 7. Phase 2 units (one IC at a time)

**Order, decided 2026-10-19:** Unit 1 (shipped, #514) → **Unit 5** (tree reuse
+ provenance, ONE unit) → Unit 2 → Unit 3 → Unit 4 → Unit 6 → Unit 7. The
numbers keep their 10-04 meaning so references to them stay valid.

1. Job consolidation from §1b (shipped, #514). Template-only and
   measured by itself, with the deviations recorded in the plan.
5. Tree reuse on push to main (D4b) **and** release
   provenance (D5, closes #508) as one unit, proven and measured while every
   PR run is still FULL. It also introduces the `test-selection.json` record
   that both halves read (`full` when the test job was expected to run,
   `none` when it was not, `reused` on a push that reused a PR run).
2. The `lacquer select-tests` engine with the §5 fixtures. Pure Go,
   no workflow change.
3. Wiring and the gate: the `changes` step, the Test and `watch-test`
   consumers, the coverage-gate rule, the CI OK asserts (§6), and the reuse
   extension that keeps a SELECTED run from ever being reused.
4. Weekly slots: the `[weekly]` config, the schedule token and its
   own concurrency group, `lacquer fleet slots`.
6. Weekly failure reporting: the result artifact and `HarvestWeekly`
   (ACTION and issue), per §4.
7. The pilot, an iOS repo with local packages (named in the
   operator's private brief), synced to the last release before Unit 3 ships;
   "before" is re-measured on its next ~10 PRs, then measure after on real
   PRs for a week, then propose the fleet sync. The pilot's name never appears
   in lacquer.

## 8. Decisions

Answers, as recorded (2026-10-04). the operator approved D1–D3 and D6–D8 as
recommended. The operator approved D4, D5 and D9, verbatim: "yeah those
reccomendations all sounds good". So **D4 = (b), D5 = yes, D9 = yes**. The
operator's billed-minutes constraint is quoted in §1b and governs every unit.
D1–D9 stand, as amended by the 2026-10-19 decisions below.

- **D1 Selection granularity (iOS).** Target-level, from the package graph.
  No Swift Testing tags, no file maps. *Recommended.*
- **D2 Fail-safe list** as in §2.4, built in and extend-only. *Recommended.*
  Amended 2026-10-19: it gains `.lacquer.ratchet.toml`, every pattern matches
  under a component prefix, and a path owned by a required non-Swift component
  is no longer "unclaimed" (§2 rule 5).
- **D3 Agent-tooling paths count as no-test**, like docs. *Recommended.*
- **D4 Push to main** (changes what protects main → operator):
  (a) always full, as today, which keeps the push-to-main cost;
  (b) **full unless the PR's last green run was FULL on the byte-identical
  tree (`HEAD^{tree}` matches); then reuse that result and spend no Mac**.
  *Recommended*: every main tree still has a full green suite, and the reused
  PRs are exactly the fail-safe ones (about half of all PRs);
  (c) selected against the previous main commit, which leaves main trees
  with no full run between weeklies.
  Made precise 2026-10-19 (see R1 below and §3).
- **D5 Releases** (→ operator): the release SHA needs a FULL green CI OK from
  the iOS CI workflow by name. With D4(a/b) that holds by construction.
  Otherwise `ios-release` dispatches a full run and waits. Fixes #508 in the
  same change. *Recommended.* Amended 2026-10-19: ships with D4b. The path
  filter on iOS push runs means a main commit that touched no Swift component
  has no iOS CI run to accept; that is the "otherwise" above, now specified:
  `ios-release` dispatches a FULL iOS CI run on that exact SHA and waits. If
  that run is not FULL and green, the release fails closed. Considered and
  rejected: accepting the nearest first-parent ancestor's run (it proves a
  different commit's tree), and blocking the tag outright (it contradicts the
  approved D5).
- **D6 Weekly slots** as in §3: 06:15 and 08:15 UTC on Mac, stored in
  `.lacquer.toml`. *Recommended.* (The 05:15 UTC Linux slot moves to "Later:
  web".)
- **D7 Weekly failure**: reworked 2026-10-19, see §4 and Q3 below. The
  original (an `issues: write` job step, a one-shot flake rerun and a bisect
  capped at 3) is superseded.
- **D8 Web scope**: moved to "Later: web" by R2 below. *Recommended* then; not
  in this project's scope now.
- **D9 Gate change**: a skipped test job when the test job is expected fails CI
  OK; "Verify Test Selectors Matched" renders fleet-wide. *Recommended*
  (→ operator: it changes what protects main). Amended 2026-10-19: "expected"
  is `code && xcodeproj && same-repo`, and `watch-test` is covered (§6).

### Decided 2026-10-19

Decided 2026-10-19, quoted:

> "R1 yes: D4b+D5 first as one Opus unit. D4b reuses a run only when the merge commit's TREE sha equals the PR run's tree sha AND that run was FULL and green. Anything else runs FULL. Prove the fail path: a merge whose tree differs (main moved) must run CI on main. D5's fail path: a non-FULL or wrong-workflow "CI OK" must not let a release through.
> R2 yes: iOS only, web as a later note.
> R3 yes.
> Q1 yes, with one condition: the component's own profile CI must be a REQUIRED check, not just present. Otherwise FULL.
> Q2 yes, and the skipped line must be visible in the job summary.
> Q3 yes.
> Q4 yes, via the pilot's PM when you get there."

How each is applied in this project:
- **R1:** the unit order in §7, and §3's reuse rule. Both fail paths are written
  into that unit's tests.
- **R2:** §2 and the "Later: web" section below.
- **R3:** the mechanical updates, throughout: the package graph reuses
  `internal/testtargets` and the config's declared packages, the result is per
  product with watch separate, the fail-safe list and component prefixes, the
  D9 condition, an old locked binary means FULL, the weekly concurrency group,
  and the corrected anchors in the plan.
- **A and B (2026-10-19), on the two open points the above raised.** B, accepted: "ACCEPT as a read-only exception, scoped exactly as planned: `changes` only, actions:read + pull-requests:read, the comment amended, and a test that no other job gained a permission. Add: any API error, rate limit, timeout, missing run or ambiguous match means FULL, never a reuse. Name that in the plan as a fail path to prove." A, "A-CLAIM, with the cross-check in fleet audit/doctor. Not A-live: classic branch-protection reads need admin, and GITHUB_TOKEN can't get administration:read, so a live read in `changes` would usually error and go FULL anyway. ... If the repo uses rulesets, GET /rules/branches/<b> is readable at read scope, so mention that as a possible later upgrade only. An unbacked claim from audit raises an inbox ACTION, not a log line." Applied in §2 rule 5 and §3.
- **Q1:** §2 rule 5. **Q2:** §2, the coverage ratchet gate. **Q3:** §4.
- **Q4:** §7 Unit 7: the pilot is synced to the last release before Unit 3
  ships, and "before" is re-measured on its next ~10 PRs.

## Later: web

Not in this project's scope (decided 2026-10-19: iOS only). The analysis is
kept so it can be picked up without redoing it.

**What was designed.**
- turbo repos: `turbo run test --filter='...[<base>]'`. The `...` prefix pulls
  in dependents. The engine would return a single filter and leave the graph to
  turbo, because duplicating it is how the two would disagree.
- Fail-safe → full: root `package.json`, `pnpm-lock.yaml`, `turbo.json`,
  `tsconfig*.json`, `biome.json`, `vitest.config.*` at the root, `.env.schema`
  and `.github/**`.
- Lint, typecheck and build stay as they are. Only the test step is selected.
- #507, the web pre-push hook that runs full turbo on docs-only pushes, would
  fold in: the same selector drives the hook. Its present state is an
  unconditional `turbo run test` (`profiles/web/root/lefthook.yml:97-106`); it
  is covered by `internal/shipped/hooks_under_git_test.go`'s real-git pattern.
- Linux weekly slots (05:15 UTC, scattered by the same hash order with a stride
  of 3 days, allowed to overlap each other) would ride with it.

**What changed since 10-04.** The "pnpm workspaces without turbo" branch above
does not exist: the web test step is either turbo or a single app
(`profiles/web/workflows/ci.yml:597-603`), so a non-turbo filter has nothing to
render into. Web selection saves Blacksmith minutes, not Mac time, and the
saving is small. Pick it up only with its own pilot and an operator who has
web in scope.
