# iOS CI gates: coverage ratchet, watchOS targets, multi-component Swift (#522 U1, U3, U4)

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement
> this plan task-by-task. One IC per unit, stacked in the order of §2. Nothing in
> this document is code: it names files, tests, fixtures, mutations and proof legs.

Status: **DRAFT for review, 2026-10-09.** Nothing is built. Each unit becomes
its own PR and release; each PR carries a `## Fleet dry-run` (U3 and U4 change
detectors) and a `## Proven on` heading (all three change
`profiles/ios/workflows/ci.yml`), per CLAUDE.md rules 1 and 3.

**Public-repo rule.** This repository is public. No consumer, app, external issue
number or private finding is named here. "The proof consumer" is the one iOS
consumer with a watch app, extra SwiftPM packages, and a hand-written coverage
ratchet and watch-test step in its own workflow. Measured numbers below are fleet
aggregates or ranges; the per-repository figures stay in the operator's private
fleet repo.

**Goal:** Turn three warning-shaped or absent iOS CI checks into gates that can fail:
a coverage ratchet with a floor, a managed watch-test job that every watch project
gets, and lint/build coverage for every Swift component a manifest declares.

**Architecture:** Each gate's verdict is computed once, in Go, inside the lacquer
binary that CI already downloads at the project's own locked version. The rendered
workflow calls that binary and fails at a *named step*; the same Go code backs
`lacquer audit`, `lacquer ratchet` and the pre-commit wrapper, so local and CI
checks cannot come apart. Every render stays byte-identical for a project that
declares nothing new (`TestIOSCISingleProductRenderIsUnchanged` pins it).

**Tech stack:** Go (`internal/ratchet`, `internal/tokens`, `internal/testtargets`,
`internal/config`), the `profiles/ios` templates (`workflows/ci.yml`,
`root/scripts/precommit-swift.sh`, `root/.pre-commit-config.yaml`, `baseline.toml`),
`xcrun xccov`, `xcrun simctl`, `swift build`.

---

## 1. Evidence this plan rests on (measured 2026-10-09)

Read-only, from the fleet roster's checkouts and the GitHub Actions API (one call at
a time, core budget never below 5,000).

**What already exists.**

- `profiles/ios/workflows/ci.yml` "Check Coverage" (Test job): computes the app
  target's line coverage from `TestResults.xcresult` and prints
  `::warning::App coverage N% is below 80% threshold`. It cannot fail on a number.
- `internal/tokens/watch.go`: a complete `watch-test` job (unpaired watch simulator,
  Carousel readiness poll, `-only-testing:` on a watch scheme, and an assertion that
  fails on an absent bundle, `totalTestCount == 0`, or any result but `Passed`)
  renders from `[project.watch_tests]` / `[product.watch_tests]`. Its runtime is a
  **second hard-coded pin**, `watchOS-27-0`, in `config.SimulatorPlatforms`,
  independent of ci.yml's `PINNED_RUNTIME="…iOS-27-0"` literal. Nothing detects a
  watch app that declares no `watch_tests`.
- `internal/ratchet` (#457): `.lacquer.ratchet.toml` with `[ratchet] metric = int`
  (lower is better) and `[reasons]`; `audit` exits 4 on regression; `sync` and
  `ratchet --write` tighten; `--loosen --reason` records an accepted increase.
  Every metric is measured locally from the working tree. `Read` requires every
  known metric to be present; `Compare` reads a missing value as 0.
- `precommit-swift.sh` resolves one `{{COMPONENT_PREFIX}}`; the `swiftlint` hook's
  `files: ^{{COMPONENT_PREFIX}}` filter hides every other directory from it. The
  Lint job runs `swiftlint --strict` over the one component and guards only the
  "config matched nothing" case inside it.
- `lacquer audit`'s uncovered-target report reads `project.pbxproj` only.

**Fleet shape (14 iOS consumers).**

| Fact | Count |
|---|---|
| XcodeGen-only (no tracked `project.pbxproj`) | 7 of 14 |
| Have a watchOS app target (in `project.yml`) | 3, all XcodeGen-only |
| … of which declare `watch_tests` | 0 |
| … of which run watch tests in a hand-written workflow or step | 2 |
| … of which have no watch test bundle at all | 1 |
| Have tracked Swift outside every directory that holds a `.swiftlint.yml` | 3 (6, 26 and 96 files) |
| Have SwiftPM packages beside the app component | 7 |
| Have a second `.swiftlint.yml` directory beside the app's | 2 (one is a tools tree with five packages) |
| Coverage warning present in the latest main Test log (below 80%) | 2 measured (13.6% and 30.3%); a third is known from its own gate to be under 50% |
| No coverage warning in the log (80%+, or the step did not run) | 9 |

The consumer with the hand-written ratchet has changed its baseline file exactly
once: the commit that introduced it. Its gate has posted "coverage is above the
baseline, bump it" on PRs since, and nobody did. **Advisory tightening does not
happen.** That fact drives U1's design.

**Mac wall-clock today (latest main run per consumer, self-hosted Mac).** Lint
13–24 s; Build (Release) 0.5–4.5 min; Test 1.5–6 min (two-product projects run
two legs). The hand-written watch job on one consumer: 1 min 48 s total, of which
simulator creation 22 s and the test run 1 min 11 s. The hand-written inline watch
steps on the proof consumer: 24 s setup + 15 s run. `swift build --build-tests`
over six packages, warm cache: 40 s. The hand-written Coverage Gate job on a 4-vCPU
hosted runner: 12 s wall, billed as a full minute at 4×.

**The runtime-pin incident the brief refers to.** Two consumers' test hosts
crash-looped when a sync moved `PINNED_RUNTIME` from 26.2 to 27.0; one fixed it
with a dated whole-file exclusion of `ios-ci.yml`, which freezes that file out of
every future profile improvement. There is no per-project way to pin a different
runtime today.

---

## 2. Stack order, and where the two small items go

1. **U1 coverage** (Test job + `internal/ratchet`). Carries both small items.
2. **U3 watch** (`internal/tokens/watch.go`, `internal/config`, `internal/testtargets`,
   the Setup Simulator step's pin).
3. **U4 components** (Lint job, push filter, `precommit-swift.sh`,
   `.pre-commit-config.yaml`, `internal/config`).

They touch disjoint regions of ci.yml (Check Coverage; Setup Simulator and the
watch job; Lint and `on.push.paths`), so rebases are mechanical. The issue's own
order is U1, U3, U4, and U1 is the one every consumer feels, so it ships first.

**Small item A: the drift step prints the audit's exit code.** `changes` job,
"Audit for drift" step: after `code=$?`, `echo "lacquer audit exit=$code"` before
the `case`, and in `ci-ok` change `echo "drift=${drift:-<not run>}"` to
`echo "drift=${drift:-<not run>} (the Detect changed paths job prints the audit's exit code)"`.
Both steps then say `drift=fail` / `exit=N` in the same vocabulary.

**Small item B: the baseline step names Package.resolved drift.** Lint job,
"Assert the project baseline" step: when `-showBuildSettings` fails and
`$settings_err` contains `an out-of-date resolved file was detected`, print
`::error::Package.resolved is out of date: xcodebuild refused -onlyUsePackageVersionsFromResolvedFile. Resolve packages locally and commit <path to Package.resolved>.`
(path from `{{XCODEPROJ}}/project.xcworkspace/xcshareddata/swiftpm/Package.resolved`)
instead of the generic "cannot verify the baseline". Any other failure keeps the
generic message plus the captured stderr.

Both ride in **U1's PR**: they are annotation-only ci.yml changes with no
behaviour change, U1 is first in the stack so they reach consumers in the first
release, and keeping them out of U4 keeps U4's Lint-job diff about lint. Tests:
`TestDriftStepPrintsTheAuditExitCode` and `TestBaselineStepNamesResolvedFileDrift`
in `internal/shipped/` extract the step bodies from the rendered workflow and run
them against a stub `xcodebuild` on PATH that prints the Xcode phrase (mutation:
remove the `grep` for the phrase; the test must fail).

---

## 3. U1: iOS CI coverage gate

### 3.1 Decisions

**D1. The ratchet metric is `ios_uncovered_lines`, an integer, lower is better.**
Uncovered lines = `executableLines − coveredLines` of the app target, read from
`xcrun xccov view --report --json`. This fits `internal/ratchet` exactly (integer,
regression = increase) and answers the reviewer's question about deleting covered code:
deleting a tested file removes equal numbers from both counts, so the metric does
not move and the PR passes. A percentage would drop and fail that PR for no fault.
Adding untested code raises the metric; deleting untested code lowers it.
*Rejected:* percentage in tenths (fails on deletion of tested code; the proof
consumer's design carries a tolerance only to paper over that); per-file or
per-target metrics (no consumer asked; YAGNI).

**D2. Format and location: `.lacquer.ratchet.toml`, the file #457 created.**

```toml
[ratchet]
claude_md_project_lines = 120
unjustified_suppressions = 7
ios_uncovered_lines = 4312          # lone product
# ios_uncovered_lines_<slug> = N    # one per [[product]] when there is a matrix
```

*Rejected:* a separate `coverage-baseline.json` (the proof consumer's file:
a second mechanism beside the one #457 shipped); the lock (regenerated by sync);
the manifest (a measured number is not configuration).

**D3. The metric is measured by CI, never from the working tree.** `ratchet.Measure`
gains an *external* metric class: it is absent from the values map unless the caller
supplies an xccov report. Consequences that must be built, each with a test:
`Read` accepts a baseline that omits `ios_uncovered_lines` (not enrolled);
`Compare` and `Tighten` skip metrics absent from the values map and **never write
0 for a metric that was not measured**; `audit` and `sync` print
`ratchet: ios_uncovered_lines is measured in CI (not checked here)` when enrolled.
New CLI surface, all on `lacquer ratchet`:

- `--coverage-report <xccov.json> --product <slug|->` : measure the external
  metric from a report. Combines with `--write`, `--loosen` and the default check.
- `--accept ios_uncovered_lines=<N>` : write N **only if lower** than the recorded
  value, without a report. Tightening unmeasured is safe: if N is too low, the next
  CI run fails as a regression. Raising via `--accept` is refused.
- `--loosen ios_uncovered_lines --to <N> --reason "<text>"` : the existing loosen,
  with `--to` because a loosen on an external metric has no local measurement.
  A loosen without `--reason`, or to a value at or below the recorded one, is an
  error.

**D4. Where the gate runs: the Test job's "Check Coverage" step, calling the
lacquer binary.** The step already has the xccov JSON. It writes it to
`coverage-report.json`, downloads the project's locked lacquer release the way the
Lint job's "Prove the checks can fail" step does (release tarball + checksum, no
lacquer checkout needed since `ratchet` reads only `.lacquer.toml` and the ratchet
file), then runs
`lacquer ratchet --coverage-report coverage-report.json --product <slug|-> --ci-event "$GITHUB_EVENT_NAME"`.
Exit 0 passes; exit 4 fails the step. The Go side owns every message; the shell
owns nothing but plumbing. *Rejected:* a shell twin of the comparison (two
implementations of one rule, the thing the CLAUDE.md "measurement, not more eyes"
section warns about); a separate Linux job reading an artifact (the consumer's
design: one extra billed job per PR, and the verdict lands in a job that is not
the one that produced the number); reading the base ref's file via the API
(the workflow's `permissions: contents: read` and "no job talks to the GitHub API"
stance; a loosen is already a reviewed diff with a reason, which is the protection
that API read was buying).

**D5. The band, and who may move the number.** With recorded value B and slack S:

| Measured M | Verdict | Who acts |
|---|---|---|
| M > B + S | **fail**: `regressed B → M; add tests, or lacquer ratchet --loosen ios_uncovered_lines --to M --reason "…"` | the PR author, in the PR, reviewed |
| B − S ≤ M ≤ B + S | pass; prints the band | nobody |
| M < B − S on `pull_request` | **fail**: `improved B → M; run lacquer ratchet --accept ios_uncovered_lines=M and commit` | the PR author; one line, printed |
| M < B − S on `push` / dispatch | `::warning::` with the same command | the next PR |

The ratchet goes **down** (tighter) by `--accept` or `--write`, and nobody may push
it below the measured value (CI then fails as a regression). It goes **up** only
by `--loosen … --reason`, which lands in `[reasons]` and in the PR diff. Forcing
the tighten on PRs is deliberate and is the one place this plan departs from
"advisory": the measured consumer shows that advisory tightening is never done.
Failing on push would redden `main` when two in-band PRs merge close together,
so push only warns. *Slack* S = max(20, 0.5 % of the app target's executable
lines), a Go constant pair, not a manifest value: the consumer's 0.5-point
tolerance was about 40 lines on an 8k-line app, and 20 lines keeps small apps from
tripping on a one-function change.

**D6. The floor is the fleet standard, 80 %, in `profiles/ios/baseline.toml`,
relaxable per project exactly like `swift_version`.**

```toml
# profiles/ios/baseline.toml
coverage_floor = 80

# a consumer's .lacquer.toml
[baseline.relax]
coverage = { until = "2027-01-31", reason = "enrolled at 31%; tracking issue in the project" }
```

The floor is checked in the same `lacquer ratchet` call (line coverage percent of
the app target below the floor fails, relaxed prints `::warning::` and passes;
expired relax fails as every expired relax does). ci.yml's "Read the baseline
relaxations" step adds `coverage` to its key loop so the Test job sees the relax
through `RELAX_COVERAGE`. *Rejected:* a per-project floor in the ratchet file
(the proof consumer has one; it has never moved; two numbers where one never
changes); a low fleet-wide "sanity" floor such as 5 % (that case is caught better
by D7's validity checks). **The value, 80, is the only number that would redden
consumers today, so it is gated by enrollment (D8).**

**D7. Validity before verdict.** The gate fails, with its own message and before
any comparison, when: the report is not JSON; the app target is absent from
`.targets[]` (a renamed scheme reads as 0 % otherwise); `executableLines == 0`;
or the Test job's `test_result` output is not `passed` (the step keeps its
`if:` guard, and the Go side re-checks the summary bundle count it is given).
This is the #333 property: "verified and passing" must be distinguishable from
"never ran".

**D8. Enrollment and rollout.** Not enrolled (no `ios_uncovered_lines` key): the
step prints `::warning::coverage gate not enrolled: run lacquer ratchet --write --coverage-report <xccov.json> and commit .lacquer.ratchet.toml`
and passes, floor included. `lacquer audit` prints the same line (non-blocking)
and `lacquer fleet` gains an `enrolled` column so the operator can drive it.
Enrolled: band and floor both gate. So **no consumer goes red on sync**; each
goes red only after it enrolls, which is deliberate and local. The proof consumer
enrolls in the proof PR. *Rejected:* enrolling everyone at sync (needs a
measurement sync does not have); gating un-enrolled projects on the floor (reddens
at least three consumers on the day of release, the retroactive-drift failure
the `changes` job's comments describe).

**D9. Optional PR comment: not in this unit.** It needs `pull-requests: write` and
an API call from the Test job, which the workflow forbids by design. The step
summary carries the same table. Open question for the operator (§7).

### 3.2 Files

- Modify `internal/ratchet/ratchet.go`: external-metric class; `Read` tolerates an
  absent external key; `Compare`/`Tighten` skip unmeasured metrics; `Accept`,
  `LoosenTo`; the band verdict `Gate(values, baseline, floor, relax, event)`.
- Create `internal/ratchet/coverage.go`: parse xccov JSON, select the app target,
  compute `ios_uncovered_lines`, percent, validity errors; slack constants.
- Modify `cmd/lacquer/ratchet.go`: the flags in D3 and D4; exit 4 on fail.
- Modify `internal/baseline` (reads `baseline.toml`) and `internal/config`
  (`[baseline.relax].coverage`): `coverage_floor`.
- Modify `profiles/ios/baseline.toml`: `coverage_floor = 80`.
- Modify `profiles/ios/workflows/ci.yml`: Check Coverage step (write JSON, fetch
  lacquer, call it, drop the `bc` comparison); "Read the baseline relaxations"
  adds `coverage`; small items A and B.
- Modify `internal/tokens/tokens.go`: `{{IOS_CI_COVERAGE_PRODUCT}}` renders `-`
  for a lone product and `${PRODUCT_SLUG}` in a matrix (the slug the ratchet key
  uses); register it in `legacyIOSCITokens`.
- Modify `internal/fleet/*.go`: `enrolled` column.
- Modify `README.md` (Ratcheting section): the external metric, `--accept`,
  `--to`, the floor and relax.
- Fixtures, inline in tests: an xccov report with two targets (`Demo.app`
  3,000/8,000 lines; `DemoTests.xctest`), one with the app target missing, one
  with `executableLines: 0`, one with an app above and one below the floor.

### 3.3 Tests, fail path first (`go test ./internal/ratchet ./cmd/lacquer ./internal/shipped`)

1. `TestCoverageGateFailsOnRegressionBeyondSlack`: B=4312, S computed, M=4400 →
   exit 4, message names both numbers and the `--loosen … --to 4400` command.
2. `TestCoverageGateFailsOnUnacceptedImprovementOnPullRequest`: M=4200 on
   `pull_request` → exit 4, message names `--accept ios_uncovered_lines=4200`.
3. `TestCoverageGateWarnsOnUnacceptedImprovementOnPush`: same M on `push` → exit 0,
   `::warning::`.
4. `TestCoverageGatePassesInsideTheBand`: M=4320 → exit 0, prints the band.
5. `TestCoverageFloorFailsBelow80Unrelaxed` / `…WarnsWhenRelaxed` /
   `…FailsWhenRelaxExpired`.
6. `TestCoverageGateFailsWhenTheAppTargetIsAbsentFromTheReport`,
   `…WhenExecutableLinesIsZero`, `…WhenTheReportIsNotJSON`: each a distinct
   message, none of them "0 lines, passed".
7. `TestTightenNeverWritesAnUnmeasuredMetric`: baseline has the key, values map
   lacks it → file unchanged (this is the `Compare` default-0 hazard).
8. `TestReadAcceptsABaselineWithoutTheCoverageKey` and
   `TestAuditSaysCoverageIsMeasuredInCI`.
9. `TestAcceptRefusesToRaise`, `TestLoosenToRequiresAReasonAndAHigherValue`.
10. `TestNotEnrolledWarnsAndPasses`.
11. `internal/shipped`: `TestIOSCISingleProductRenderIsUnchanged` keeps passing
    with the new token pinned; `TestCheckCoverageStepCallsTheLacquerBinary`
    extracts the step and runs it against a stub `lacquer` on PATH that records
    argv (asserts `--coverage-report`, `--product -`, `--ci-event`), and against
    a stub exiting 4 (the step must exit non-zero).
12. The `lacquer doctor` ratchet probe (`cmd/lacquer/ratchet.go: proveRatchet`)
    gains a coverage leg: write a report, `--write`, then a regressed report must
    exit 4. Doctor exits 5 if it does not.

### 3.4 Mutations (each must fail a named test above)

| Mutation | Must fail |
|---|---|
| Drop the slack from the regression comparison | 4 (in-band M now fails) |
| Compare `M > B` instead of `M > B + S` | 4 |
| Return 0 instead of 4 on regression | 1 |
| Treat `pull_request` improvement as warning | 2 |
| Treat `push` improvement as failure | 3 |
| Select `.targets[0]` instead of the named app target | 6 (absent-target case passes wrongly) |
| Allow `executableLines == 0` | 6 |
| `Tighten` writes `values[key]` for a missing key | 7 |
| `Read` still requires the coverage key | 8 |
| `Accept` writes a higher value | 9 |
| Floor read as 0 when `baseline.toml` lacks the key | 5 |
| Step runs the comparison in shell and ignores the binary's exit | 11 |

### 3.5 Consumer-proof fail leg

On the proof consumer (enrolled in the clean leg, with a dated `coverage` relax
because it is below 80 %): add one Swift file with a type whose methods total more
than S uncovered lines and no test. **Expected:** the Test job's "Check Coverage"
step fails with `ratchet: ios_uncovered_lines regressed B → M`, naming the
`--loosen … --to M` command; "Run Tests", "Generate Test Summary" and every other
job are green; CI OK is red because Test failed. A second leg deletes the file and
adds tests beyond S: "Check Coverage" fails with the `--accept` command; running
it and pushing turns the step green.

### 3.6 Cost per PR

Mac: +5–10 s (release tarball + checksum) and under 1 s for the verdict, inside a
step that already exists; no new job; no billed minutes (the Mac is self-hosted).
The proof consumer retires a hosted Coverage Gate job (1 billed minute at 4× per
PR) and its API calls when it adopts the rendered file.

### 3.7 Rollout risk

None goes red on sync (D8). Consumers with a hand-written coverage gate keep their
whole-file exclusion until they enroll and adopt; the proof consumer does so in the
proof PR. The `.lacquer.ratchet.toml` of a project that enrolled under #457 keeps
parsing (the coverage key is optional). The `bc` dependency leaves the step.

---

## 4. U3: watchOS targets

### 4.1 Decisions

**D10. One runtime pin, in Go, rendered into both places.**
`config.SimulatorRuntimeMajor = "27"` (one constant). ci.yml's literal
`PINNED_RUNTIME="com.apple.CoreSimulator.SimRuntime.iOS-27-0"` becomes
`PINNED_RUNTIME="{{IOS_CI_SIM_RUNTIME}}"`, rendered to the identical string
(byte-identical for everyone). `SimulatorPlatforms[watchOS].Runtime` is derived as
`watchOS-<major>-0` from the same constant; `TestWatchRuntimeFollowsTheIOSPin`
asserts the two majors are equal and `TestEverySimulatorPlatformIsComplete` keeps
rejecting a hand-typed runtime. The staleness warning against the host SDK stays
where it is. *Rejected:* two literals (the defect the brief names); pinning to the
newest installed runtime (that is the fallback's job; #327 pinned deliberately so
every repository tests the same OS).

**D11. A per-project, dated runtime override, applying to both platforms.**

```toml
[baseline.relax]
simulator_runtime = { major = "26", minor = "2", until = "2026-12-31", reason = "test host crash-loops on 27.0 (CoreData/CloudKit, no iCloud account on the simulator)" }
```

Renders `iOS-26-2` and `watchOS-26-<minor or 0>` into the two steps, with the
`::warning::` the relax step already prints and the expiry failure it already
enforces. This exists because the only escape today is a whole-file exclusion of
ci.yml, which two consumers took and which silently freezes that file out of every
later profile change. It is a dated relaxation and not a plain setting because a
lagging runtime is debt. **The reviewer's call** (§7): it widens U3 beyond the issue's
words, but the brief's constraint "follow the iOS runtime pin policy" has no
meaning if the policy's only escape hatch is to stop syncing the file.
*Rejected:* a free-form `runtime` string (it reaches `simctl create` as an
argument; the override is two validated numeric fields and nothing else).

**D12. Detection reads XcodeGen specs, because every watch consumer is
XcodeGen-only.** `internal/testtargets` gains `ParseSpec(project.yml)` returning
the same `[]Target` as `Parse(pbxproj)`, plus application targets with a
platform: `type: application` (or `application.watchapp2`) with
`platform: watchOS` is a watch app; `type: bundle.unit-test` with
`platform: watchOS` is a watch test bundle. For a pbxproj, the product types
`com.apple.product-type.application.watchapp2` and `…watchapp2-container` mark the
app. The audit uses the pbxproj when tracked and the spec otherwise (today it
reads nothing for 7 of 14 consumers). This is a detector change: the PR carries a
fleet dry-run of the uncovered-target report before and after.

**D13. What detection reports, and what blocks.**

| Finding | Today | After U3 |
|---|---|---|
| Watch app, watch test bundle exists, nothing runs it (no `watch_tests`, no verified `covered_elsewhere`, no in-term `not_run_in_ci`) | invisible for XcodeGen-only projects | `uncovered`, with the exact `[project.watch_tests]` TOML derived from the spec's scheme that lists the bundle; **exit 4** |
| Watch app, no watch test bundle at all | nothing | `::notice`-level line: `watch app <name> has no test bundle; a [product.watch_tests] cannot be declared` (non-blocking) |
| `watch_tests` declared, `watch_target` false | runtime install step not rendered | `watch_target` is derived from detection or `watch_tests`; the manifest field is accepted and ignored with a notice |
| No watch app | nothing | nothing (control) |

Making the first row blocking changes the whole uncovered report from
report-only to exit 4. **The fleet dry-run supports it:** with spec parsing, every
consumer's unit-test target is either its derived selector, an `extra_test_targets`
entry, or a watch bundle that a workflow in the repository already runs, so the
report has zero blocking findings on the day it ships. If the reviewer prefers to keep the
report advisory, the watch row alone stays report-only and the gate is CI's job on
the projects that declare `watch_tests`; say which in review (§7).
*Rejected:* deriving `watch_tests` automatically (the scheme/test-bundle pair is
not derivable from a pbxproj without parsing `.xcscheme` XML, and `config.go`
already records why appending "Tests" to a scheme name was wrong); a `watch_tests`
auto-render from detection (a job that appears without a manifest line is a
change a consumer cannot see in its diff).

**D14. The job itself is unchanged in shape.** It already fails on zero tests at a
named step, "Assert the watch suite ran and passed". U3 does not touch that logic
except to consume D10/D11's runtime.

### 4.2 Files

- Modify `internal/config/config.go`: `SimulatorRuntimeMajor`; derived watch
  runtime; `[baseline.relax].simulator_runtime` (two numeric fields, `until`,
  `reason`; validated); `WatchTarget` derivation; deprecation notice.
- Modify `internal/tokens/tokens.go` and `watch.go`: `{{IOS_CI_SIM_RUNTIME}}`
  (registered in `legacyIOSCITokens`); the watch leg's `runtime:` from the
  derived value or the relax.
- Modify `profiles/ios/workflows/ci.yml`: the one literal becomes the token; the
  relax-reading step adds `simulator_runtime`.
- Create `internal/testtargets/spec.go`: `ParseSpec`, `WatchApps`.
- Modify `internal/testtargets/testtargets.go`: watch app product types; `Target`
  gains `Platform`.
- Modify `cmd/lacquer/main.go` (audit): spec fallback when the pbxproj is absent;
  the watch findings; the exit-4 wiring (if D13 is accepted as blocking).
- Modify `internal/fleet`: `watch` column (declared / hand-written / none).
- Fixtures: `internal/shipped/testdata/projects/watchapp/` (XcodeGen-only: a
  `project.yml` with an iOS app, a watch app, both test bundles, and the two
  schemes; no pbxproj) and, inline in tests, a `project.yml` with an iOS app plus
  a widget `app-extension` and **no** watch target (the control).

### 4.3 Tests, fail path first

1. `TestAuditReportsAnUndeclaredWatchTestBundleFromTheSpec`: the `watchapp` fixture
   with no `watch_tests` → finding names the bundle and prints the TOML to add;
   exit 4 (or report-only, per §7).
2. `TestAuditIsSilentForAWidgetOnlySpec` (the control) and
   `TestAuditIsSilentForEveryShippedFixture` (rootapp, multistack, duoapp,
   spmpackage produce no watch finding).
3. `TestAuditNoticesAWatchAppWithNoTestBundle`: non-blocking line, exit 0.
4. `TestWatchRuntimeFollowsTheIOSPin`; `TestSimRuntimeTokenRendersTheSamePinAsBefore`
   (the rendered string equals the literal the template carried, so
   `TestIOSCISingleProductRenderIsUnchanged` passes unchanged).
5. `TestSimulatorRuntimeRelaxRendersBothPlatforms`,
   `…RequiresUntilAndReason`, `…RejectsNonNumericFields`, `…ExpiredFails`.
6. `TestWatchTargetIsDerivedFromWatchTests` and `…FromTheSpec`.
7. `TestSpecParseMatchesPbxprojParseOnTheSameProject`: the `duoapp` fixture's
   pbxproj and an equivalent spec yield the same target set (guards the two parsers
   drifting apart).
8. Existing `TestWatchJobFailsWhenTheSuiteDidNotActuallyRun` stays, untouched.

### 4.4 Mutations

| Mutation | Must fail |
|---|---|
| Hard-code `watchOS-27-0` again in the platform table | 4 |
| Render the iOS token from a second constant | 4 |
| `ParseSpec` ignores `platform:` and treats every application as iOS | 1 and 3 |
| `ParseSpec` treats `app-extension` as an application | 2 (control) |
| Relax renders iOS only | 5 |
| Relax accepts a string with a space or slash | 5 |
| Audit reads the spec even when a pbxproj is tracked | 7 |
| The watch finding is printed but not counted in the gate | 1 (if blocking) |

### 4.5 Consumer-proof fail leg

On the proof consumer, after the clean leg (declare `[project.watch_tests]` with
the real scheme and bundle; the rendered "Watch Tests" job runs and reports N
tests): change `test_target` to the bundle's name with one character wrong.
**Expected:** "Run Watch Tests" is green (xcodebuild exits 0 for a selector that
matches nothing), "Assert the watch suite ran and passed" fails with
`executed 0 tests`, and no other step or job is red; CI OK is red through
`needs.watch-test.result`. **Control leg:** `lacquer audit` on a consumer with no
watch app reports nothing new, and its rendered ci.yml differs from the previous
release only in the small items of §2.

### 4.6 Cost per PR

Watch consumers: one more Mac job, about 2 min warm (checkout 3 s, runtime check
1 s, simulator 22 s, test build and run 1–1.5 min, cleanup 4 s), 3–5 min on the
first run per checkout because `WatchDerivedData` starts cold. Non-watch
consumers: 0. No billed minutes. The proof consumer's inline watch steps leave its
Test job (about 40 s shorter) when it adopts.

### 4.7 Rollout risk

Spec parsing can surface targets the audit never saw in the 7 XcodeGen-only
consumers. The dry-run in §1 found none uncovered, so no consumer's `changes` job
goes red; the PR re-runs that dry-run against the release candidate. The two
consumers with hand-written watch CI see a `watch: hand-written` fleet row and a
non-blocking hint until they declare `watch_tests` and delete their workflow or
steps; the one with no watch bundle sees a notice. The runtime token renders
identically, so nothing moves runtimes.

---

## 5. U4: multi-component Swift

### 5.1 Decisions

**D15. A Swift component is a manifest `[[component]]` whose `profiles` contains
`ios` or whose `stack` is `ios`.** Exactly one carries the profile (the app, which
owns `xcodeproj`); every other is a *package component*. Discovery is from the
manifest only. *Rejected:* finding every `.swiftlint.yml` in the tree (the reviewer's
constraint; a vendored checkout's config would become a component; and two
consumers carry nested test-directory configs that are SwiftLint nesting, not
components).

**D16. Every Swift component must hold a `.swiftlint.yml` at its root; the Lint job
lints each component from inside it.** The profile component's config is the
rendered one. A package component's config is project-owned for now (U8 is where a
managed child config with `parent_config` belongs); a package component with no
config **fails** Lint: `component tools declares stack ios but has no .swiftlint.yml`.
The existing "config matched no files" guard applies per component. The step name
stays "Run SwiftLint"; its log groups by component.

**D17. Un-linted Swift is a failure, reported in one place and checked in two.**
`internal/swiftcomponents.Stray(root, cfg)` lists tracked (`git ls-files
--cached --others --exclude-standard`) `.swift` files under no Swift component
path. The Lint job's new step "Every Swift file belongs to a declared component"
runs `lacquer swift-components --check` (the binary the job already downloads)
and fails listing the files and the components it did discover, with the fix
(`add [[component]] path = "<dir>" stack = "ios" and a .swiftlint.yml, or move the files`).
`lacquer audit` prints the same list as a finding and counts it under exit 6
(undeclared stack), because that is exactly what it is. *Rejected:* an awk/grep
twin in the workflow (two implementations); gating only in audit (a PR that adds
a stray directory touches no lacquer path, so the `changes` job's audit does not
run on it).

**D18. Package components are built.** The Lint job gains "Build Swift packages":
for every directory with a `Package.swift` directly under a package component (depth
0 or 1), `swift build --build-tests --package-path <dir> --only-use-versions-from-resolved-file -Xswiftc -warnings-as-errors`.
The directory list is rendered by `{{IOS_CI_PACKAGE_DIRS}}` from the manifest plus
a tracked-file scan at render time, so a package added without a sync is reported
by `lacquer audit` as drift of the rendered list (the way `dependabot.yml` entries
already are). Packages under the profile component are not built here: the Xcode
project builds them in Test. *Rejected:* `swift test` (the issue says build;
tests in tool packages are the project's call, and `--build-tests` already
compiles them); a separate job (one more runner pickup for 40 s of work; the proof
consumer folds it into Lint for the same reason).

**D19. Push filter.** `on.push.paths` gains one `'<component>/**'` line per package
component through `{{IOS_CI_PUSH_PATHS}}`; empty for every single-component
project (byte-identical).

**D20. Pre-commit.** The `swiftlint` hook drops its `files: ^{{COMPONENT_PREFIX}}`
filter so every staged `.swift` reaches `precommit-swift.sh`; the script receives
the component list through `{{IOS_SWIFT_COMPONENTS}}` (newline-separated, the
profile component first), groups staged paths by longest-prefix component, `cd`s
into each and runs `swiftlint lint --strict --quiet --force-exclude --config
.swiftlint.yml <relative paths>`; a staged path under no component fails closed
naming it; a component without a config fails closed; a final count asserts every
staged file was handed to exactly one invocation. `swiftformat` and
`swiftlint-docs` keep their filter: their configs exist only in the profile
component. *Rejected:* having the hook call the lacquer binary (not guaranteed on
PATH at commit time; the rendered list needs nothing).

### 5.2 Files

- Create `internal/swiftcomponents/`: `Components(cfg)`, `PackageDirs(root, cfg)`,
  `Stray(root, cfg)`, `Group(staged, components)` (shared with tests of the shell).
- Modify `cmd/lacquer/main.go`: `swift-components [--check]`; audit's exit-6
  wiring for strays.
- Modify `internal/tokens/tokens.go`: `{{IOS_CI_PACKAGE_DIRS}}`,
  `{{IOS_CI_PUSH_PATHS}}`, `{{IOS_SWIFT_COMPONENTS}}`, `{{IOS_CI_LINT_COMPONENTS}}`
  (the Run SwiftLint loop's list), all empty or single-valued for a lone
  component and registered in `legacyIOSCITokens`.
- Modify `profiles/ios/workflows/ci.yml`: `on.push.paths`; Lint job's Run
  SwiftLint, new "Every Swift file belongs to a declared component" and "Build
  Swift packages" steps.
- Modify `profiles/ios/root/scripts/precommit-swift.sh` and
  `profiles/ios/root/.pre-commit-config.yaml` (D20).
- Modify `internal/config/config.go`: `Components()` accessor for Swift components;
  validation that at most one carries the `ios` profile (already) and that a
  package component's path is not inside the profile component.
- Fixtures: `internal/shipped/testdata/projects/multiswift/` (an `ios/` profile
  component, a `tools/` package component with two packages and its own
  `.swiftlint.yml`, and one stray `Stray.swift` at the root).

### 5.3 Tests, fail path first

1. `TestSwiftComponentsCheckFailsOnAStrayFile` (the `multiswift` fixture: lists
   `Stray.swift`, names the two components, exit 1); `TestAuditCountsStraySwiftAsUndeclared`
   (exit 6).
2. `TestSwiftComponentsCheckIsSilentWhenEverythingIsCovered` (rootapp, duoapp,
   multistack: exit 0; a local package under the profile component is covered).
3. `TestLintStepLintsEveryComponentFromItsOwnDirectory`: rendered step against a
   stub `swiftlint` recording cwd and argv: two invocations, cwd `ios` and `tools`.
4. `TestLintFailsWhenAPackageComponentHasNoConfig`.
5. `TestBuildPackagesStepRunsOncePerPackage`: stub `swift` records
   `--package-path` for both packages; nothing for the profile component's package.
6. `TestPushPathsRenderOnePerPackageComponent` and
   `TestIOSCISingleProductRenderIsUnchanged`.
7. `precommit_swift_test.go` additions: `TestPrecommitGroupsStagedFilesByComponent`
   (stub records two cwds), `TestPrecommitFailsClosedOnAStagedFileOutsideEveryComponent`,
   `TestPrecommitFailsClosedWhenAComponentHasNoConfig`,
   `TestPrecommitCountsEveryStagedFileExactlyOnce` (a path that is a prefix of
   another component's path must not match it: `ios` vs `ios-tools`).
8. `TestPreCommitConfigSwiftlintHookHasNoFilesFilter` and
   `…SwiftformatHookKeepsItsFilter`.

### 5.4 Mutations

| Mutation | Must fail |
|---|---|
| `Stray` compares with `strings.HasPrefix(path, comp)` instead of `comp + "/"` | 7 (prefix case) and 1 |
| `Stray` skips untracked-but-not-ignored files (`--others` dropped) | 1 (fixture stages the stray without committing) |
| The check step ignores the binary's exit code | 1 (shipped) |
| Lint runs every component from the repo root | 3 |
| A missing config is skipped instead of failing | 4 |
| `PackageDirs` includes the profile component's packages | 5 |
| Push paths render for a lone component | 6 |
| The hook keeps `files: ^{{COMPONENT_PREFIX}}` | 8 |
| The script lints unmatched files with the profile component's config | 7 |

### 5.5 Consumer-proof fail leg

On the proof consumer, after the clean leg (declare its tools tree as a package
component, delete its own component script and let the rendered hook and Lint
run): commit a `.swift` file in a new top-level directory that no component
covers. **Expected:** "Every Swift file belongs to a declared component" fails,
listing that one file and both components; "Run SwiftLint", "Build Swift
packages" and every other job are green. Two more legs, one each: a `--strict`
violation in a tools package fails "Run SwiftLint" in the `tools` group only; a
broken `Package.swift` fails "Build Swift packages" only. Locally, before pushing,
the pre-commit hook blocks the stray file and the violation with the same text.

### 5.6 Cost per PR

Package-component consumers: Lint +1 s for the stray check, +1–5 s per extra
component for lint, +40 s warm to +3–4 min cold for the package builds (measured on
six packages). Everyone else: +1 s. No new job; no billed minutes.

### 5.7 Rollout risk

Three consumers have Swift outside every component today (6, 26 and 96 files) and
**would fail the new Lint step on their first code PR after syncing.** Handling,
in the PR's fleet dry-run: the list of stray paths per consumer (privately), and
for each a one-line remedy: the 96- and 26-file cases are local packages beside
the app component that need a `[[component]] … stack = "ios"` block and a
`.swiftlint.yml` (and will then also be built by D18, roughly a minute each); the
6-file case is stray fixtures at the repository root that belong under the
component or in no repository. `lacquer audit` reports the strays before any sync
(exit 6), so each consumer's PM sees it in the fleet sweep the day the release
lands rather than on a red PR. The consumer that already groups by component with
its own script retires that script and its exclusion in the proof PR.

---

## 6. The single proof session (the proof consumer, one branch per leg, PRs titled `[lacquer proof, do not merge] <leg>`)

| # | Leg | Must fail at | Must stay green |
|---|---|---|---|
| 0 | Control: sync the pre-stack release | nothing | everything |
| 1 | U1 clean: sync U1; `lacquer ratchet --write --coverage-report`; add `[baseline.relax] coverage`; commit | nothing; Check Coverage prints the band | everything |
| 2 | U1 fail: untested type beyond slack | Test › Check Coverage (`regressed`) | Run Tests, Lint, Build (Release) |
| 3 | U1 tighten: tests beyond slack, file untouched; then `--accept` | Test › Check Coverage (`improved … --accept`), then green | as above |
| 4 | U1 loosen: `--loosen … --to M --reason` on leg 2's tree | nothing; `[reasons]` in the diff | everything |
| 5 | U3 clean: declare `[project.watch_tests]`; remove the inline watch steps on the branch | nothing; Watch Tests reports N tests | everything |
| 6 | U3 fail: `test_target` one character wrong | Watch Tests › Assert the watch suite ran and passed (`executed 0 tests`) | Run Watch Tests, Test, Lint |
| 7 | U3 control: a consumer with no watch app, `lacquer audit` and a render diff | nothing | no watch job rendered; runtime string unchanged |
| 8 | U4 clean: declare the tools package component; drop the project's own lint script and exclusion | nothing; Lint shows two groups and the package builds | everything |
| 9 | U4 fail a: stray `.swift` in a new top-level directory | Lint › Every Swift file belongs to a declared component | Run SwiftLint, Build Swift packages, Test |
| 10 | U4 fail b: `--strict` violation in a tools package | Lint › Run SwiftLint (tools group) | the stray check, Build Swift packages |
| 11 | U4 fail c: broken `Package.swift` in tools | Lint › Build Swift packages | the two steps before it |
| 12 | U4 local: pre-commit blocks legs 9 and 10 before any push | the hook, with the same text | n/a |

Each leg's run id and date go under `## Proven on` in the unit's PR. Legs 2, 6 and
9 are the three "fail legs" the brief asks for; the others are controls and the
remedy paths. Wait on each with one background `lacquer wait pr <N>`.

---

## 7. Open questions (recommendation first; keep going unless told otherwise)

1. **D13, blocking the uncovered-target report.** Recommend exit 4, because the
   dry-run shows zero blocking findings on the day it ships and the escape hatches
   (`covered_elsewhere`, dated `not_run_in_ci`) already exist. Alternative: keep
   it advisory and let only the declared `watch_tests` job gate. The reviewer's call.
2. **D11, the dated runtime override.** Recommend shipping it in U3. Alternative:
   a separate unit after U4; until then the two affected consumers keep their
   whole-file exclusions. The reviewer's call.
3. **D9, the PR comment.** Recommend no: it needs write permissions and an API
   call the workflow forbids by design, and the step summary carries the same
   table. Operator's call, since the issue lists it.
4. **D6, 80 % as the floor.** Recommend 80 (the number the warning already names)
   with the dated relax; the two measured consumers under 50 % and the one under
   15 % relax at enrollment. Alternative: a lower fleet standard. Operator's call.
5. **D8, an enrollment deadline.** Recommend none in U1; `lacquer fleet`'s
   `enrolled` column makes the state visible and the operator drives it.
   Alternative: `lacquer audit` exits 4 for an un-enrolled iOS project after a
   date. Operator's call.

---

## 8. Review decisions (2026-10-09), binding on the implementation units

- **Proof session cut to five CI runs:** one clean run at the top of the stack (U1, U3 and U4 together), then four fail legs: U1 regression (§3.5), U3 zero tests (§4.5), U4 stray Swift and U4 broken package (§5.5). The tighten and loosen paths, the strict-lint leg, the pre-commit leg and the no-watch control are proven by unit tests or local runs. Each unit's `## Proven on` says which proof covers which path.
- **U4 stray Swift ships as a warning until a dated deadline, 14 days after the release that ships it, then blocks.** The warning text prints that date.
- **U1 fails a PR whose coverage improves beyond the slack**, and its message prints the exact one-line `lacquer ratchet --accept …` command to run.
- §7 answers:
  1. The uncovered-target report blocks (exit 4).
  2. The dated runtime override ships in U3.
  3. No PR comment; the step summary carries the table.
  4. The floor is 80 %, with the dated relax.
  5. No enrollment deadline.

### Addendum (2026-10-09, during U3): §7.1 reversed

U3's fleet dry-run contradicted the premise behind answer 1. §4.1 D13 assumed
the uncovered-target report would have zero blocking findings on release day. It
does not: measured against every iOS consumer before any U3 change, the report
already lists unit, UI and local-package suites that no selector names in
half the fleet. Making the whole report exit 4 would turn those repositories red
at their next sync, which is the retroactive-drift failure D8 avoided for U1.
The reviewer's ruling, which replaces answer 1:

- **The uncovered-target report stays report-only.** The one row that blocks is
  a **watchOS unit-test bundle that nothing runs** (no `watch_tests` selector, no
  verified `covered_elsewhere`, no in-term `not_run_in_ci`).
- **That row has a dated grace period, like U4's stray Swift:** it warns with the
  date printed, and blocks (exit 4) from the release that ships it plus 14 days.
  The date is one constant, `testtargets.WatchGateFrom`.
- **`[[project.covered_elsewhere]]` cannot become a silent mute.** It requires a
  `job` (checked against the workflow's `jobs:`) and a `reason` that names both
  the workflow file and the job, and an entry whose workflow file does not exist
  fails `lacquer audit` (exit 4).
- The broader UI and package-suite findings stay report-only and are tracked on
  the issue.

### Addendum (2026-10-09, during U4): what the implementation decided

- **One date for both gates.** Stray Swift reads `testtargets.WatchGateFrom`
  through `swiftcomponents.GateFrom`; the pre-commit hook gets the same value
  rendered as `{{IOS_SWIFT_GATE_FROM}}`. Moving the date moves the watch gate,
  the Lint step, `lacquer audit`, the fleet sweep and the hook together.
- **The pre-commit hook honours the grace period too.** D20 said a staged file
  under no component fails closed. Before the gate date it now warns with the
  date and leaves the file unlinted, as CI does; from the date it blocks.
  Otherwise the consumers the grace period exists for would be blocked at commit
  time from the day they sync, before CI ever is.
- **"Could not list" is never "nothing stray".** Outside a git work tree, or
  without git, `lacquer swift-components --check` (the CI step) fails;
  `lacquer audit` and the fleet sweep print "NOT checked" and do not gate.
- **No Swift component may sit inside another,** a root-layout app included:
  each is linted from its own directory, so a nested one would be linted twice
  under two configs. No fleet manifest has this shape (measured).
- **`{{IOS_CI_PUSH_PATHS}}` owns the whole `paths:` list body at column 0,**
  like `{{IOS_RELEASE_TAGS}}`. A token trailing the quoted list item would make
  the template's `on:` block unparseable before substitution, and
  `internal/retire` parses that block.
- **Measured against §5.7:** the three consumers carry 6, 26 and 106 stray files
  (the plan said 96 for the last; measured at its main of 2026-10-08). The
  6-file case is six loose root files with no declarable directory, so its
  remedy is to move them.

### Addendum (2026-10-09, during U4): D18 narrowed

U4's fleet dry-run contradicted D18's premise. `swift build` compiles for the
host, macOS, and every package behind the plan's "40 s warm over six packages"
figure was a macOS tool. The first iOS-only package measured (its `platforms:`
lists only `.iOS`) cannot be built that way at all, and following the stray
remedy would have turned that consumer red on a step it could not satisfy. The
reviewer's ruling, which replaces D18's build list:

- **Build packages that declare macOS, or no platforms; skip iOS-only ones with
  a visible notice that names each package** ("Not built: <dir> is an iOS-only
  package …"), never a silent pass. The platform is read from Package.swift at
  render time, textually and with comments removed, because the drift audit
  renders on Linux without a Swift toolchain. A `platforms:` value it cannot
  read (a variable) counts as buildable, so it fails loudly rather than being
  skipped unseen.
- **Building iOS-only packages (`xcodebuild build-for-testing` against an iOS
  Simulator destination) is a follow-up item on #522**, with its cost still to
  be measured.
- **The package build is not covered by the stray-Swift grace date.** It only
  has work once a project declares a package component, and a package a project
  declared should build, so it blocks from the first declaration. The step's
  comment and its error text say so.
