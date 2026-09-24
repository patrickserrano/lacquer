Core section references below now live in skills: Fundamental Rules in
`engineering-workflow`, Documentation in `project-documentation`, and Local
Checks Match CI in `working-with-lacquer`. Load the named skill when needed.

## Shipping more than one app from one repository

A repository that ships a paid and a free variant declares each as a
`[[product]]` in `.lacquer.toml`:

```toml
[[product]]
name = "MyApp"
scheme = "MyApp"
bundle_id = "com.example.myapp"
asc_app_id = "1234567890"

[[product]]
name = "MyApp Lite"
scheme = "MyApp Lite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "0987654321"
```

The release workflow becomes a matrix with one leg per product: separate
archive, IPA, TestFlight upload and GitHub Release asset for each.

**CI does the same.** `Build (Release)` and `Test` get one leg per product, so
the free variant is compiled and its own test bundle is run. Four optional
fields drive the test leg, each defaulting to the historical single-app value:

```toml
[[product]]
name = "MyApp Lite"
scheme = "MyApp Lite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "0987654321"
tag_prefix = "myapplite"
test_target = "MyApp LiteTests"      # defaults to "<scheme>Tests"
ui_test_target = ""                   # blank = this variant has no UI tests
extra_test_targets = []               # local package suites to run as well
app_target = "MyApp.app"              # coverage target; defaults to "<scheme>.app"
```

Target defaults use `name` only when `scheme` is empty. Explicit
`test_target` and `app_target` values override these defaults.

`app_target` must be declared when a scheme and its built
product differ — one app in this fleet builds `A Bible Verse
Daily.app` from a scheme named `A Bible Verse Each Day Free`. A derived value
would select no coverage row, and `jq` selecting nothing reports 0.0%, not an
error.

`ui_test_target` is conditional in the shell rather than always passed: an empty
`-only-testing:` selector matches nothing and still exits 0.

`extra_test_targets` exists because `-only-testing:` is a **whitelist**. A local
Swift package's test target that no selector names is run by nothing — the app's
selector excludes it, xcodebuild exits 0, and a maintained suite (and any
coverage rule over the code it covers) is enforced by no one. The observed
workaround was copying an assertion into the app's test target via `@testable
import` purely so something would execute it.

```toml
extra_test_targets = ["CoreKitTests", "Feature KitTests"]
```

Per-product, for the same reason `test_target` is: a package linked into one
scheme and not the other must not be selected on the leg that cannot run it,
where it would match nothing and pass. **A single-product project declares it
under `[project]` instead**, where it folds into the product the manifest
synthesises — the same fallback `asc_app_id`, `bundle_id`, `extra_bundle_ids`
and `scheme` already have. The per-product rationale is about paid and free
variants compiling different bundles; with one product there is no other leg
for a selector to be wrong on. Setting it in both places is rejected rather
than merged, since which product an unattached list belonged to would have to
be guessed. It is validated identically either way. The selectors are built as a shell
**array**, so a target name containing a space stays one argument rather than
word-splitting into two selectors that each match nothing.

Declaring any turns on a `Verify Test Selectors Matched` step, which reads the
result bundle back and **fails the job for any selector that produced no test
bundle** — the only thing standing between an extra selector and a green run
over a suite that did not execute. It fails closed: an unreadable bundle, a
missing tool or a changed schema is red, not a pass. Opting in hardens
`test_target` and `ui_test_target` too, since the step checks every selector the
job passed. The pre-commit `Swift Tests` hook runs the same extras, so the fast
local loop does not cover less than CI.

Each leg's simulator and uploaded test results are scoped by a slug derived from
the product name. Two legs sharing one simulator name means the second leg's
stale-simulator cleanup deletes the simulator the first is mid-test on, which
reports as "the test runner crashed before establishing connection" and reads
like an app bug.

**Declare nothing and you get exactly one product**, synthesised from
`[project]`. That is not a special case in the workflow — it is a one-entry
matrix, the same code path. A single-app project's release is unchanged, and its
CI workflow is rendered byte-for-byte as it was before products existed.

`fail-fast: false` because the products are separate App Store submissions with
separate review outcomes: one failing validation must not cancel the other's
upload. `max-parallel: 1` because both legs sign on the same runner and share
its certificate directory.

Two things that bite specifically on a paid/free pair, both learned the hard way:

- **Guideline 2.3.7** rejects a price reference in the *name or icon* of the free
  product — see App Store Requirements above.
- **Error 90186**: a version train closes permanently at `READY_FOR_SALE`, so a
  release trigger that fans out to a product which has already shipped that
  version can only fail. Bump the version rather than the build number.

### Watch tests: a scheme AND a destination the iOS leg does not have

A **watchOS** suite is not reachable from the iOS test leg, and for two
independent reasons — fixing either one alone fixes nothing:

1. **Scheme.** It is a testable of a *different* scheme, so naming it in
   `extra_test_targets` fails hard: `Tests in the target "… Watch AppTests"
   can't be run because … isn't a member of the specified test plan or scheme`.
2. **Destination.** The Test job carries exactly one, `platform=iOS Simulator`.
   A watch bundle cannot run there whatever scheme the leg names.

Declare the bundle and the lacquer renders a `watch-test` job for it:

```toml
[project.watch_tests]                   # the single-product spelling
scheme      = "DailyBreadWatchApp Watch App"
test_target = "DailyBreadWatchApp Watch AppTests"
```

```toml
[[product]]                             # or per product, when there are several
name   = "Paid"
scheme = "DailyBread"

  [product.watch_tests]
  scheme      = "DailyBreadWatchApp Watch App"
  test_target = "DailyBreadWatchApp Watch AppTests"
  platform    = "watchOS"               # optional; the only value today
```

`platform` is a **closed set**, not a free-form `-destination`. The value is
spliced into the rendered job's shell, and each platform needs its own device
type, runtime pin and boot-readiness signal — none of which can be guessed from
a name. The destination, the device type and the runtime are all constants in
the lacquer; nothing the manifest writes reaches the runner except the scheme
and the target, held to the same charset as every other Xcode name.

**Declare nothing and no job is rendered**, and the workflow is byte-identical
to the one the project already had — including the `CI OK` gate, which gains the
job in its `needs` *and* in its result loop only when there is one. A job the
gate waits for but never reads is worse than no job: it looks like coverage.

Three things the job knows that a project should not have to rediscover:

- **The watch simulator must be UNPAIRED.** A paired watch activates a real
  `WCSession`, so a suite written against the unpaired state exercises different
  behaviour — silently, and only on CI. The job **creates** a run-scoped device
  (`CI-Watch-$GITHUB_RUN_ID-<slug>`) and asserts it is absent from `simctl list
  pairs`. "Find an existing unpaired watch" is not a strategy: measured on this
  fleet's runner at watchOS 27.0, three of the five stock watch simulators were
  already paired to phones, and which three is not a property to rely on.
- **A watchOS simulator runs Carousel, not SpringBoard.** Measured on a booted
  watchOS 27.0 device, `launchctl list` carries `com.apple.Carousel` and no
  SpringBoard at all — so the iOS job's readiness poll copied across would time
  out and warn on a device that had been ready for forty seconds.
- **The verdict comes from the result bundle, not the exit code.** A selector
  that matches nothing exits 0 and reports "0 failed". The assertion step fails
  if the bundle is absent, if `totalTestCount` is 0, or if `.result` is anything
  but `Passed` — `unknown` included, because that is `xcresulttool` saying it
  could not tell. There is no `|| true` anywhere in that read path.

The watch job does not archive, sign or release. A watch app ships inside its
host app, and it is deliberately **not** a second `[[product]]`: every product
needs a `bundle_id` and an `asc_app_id`, and the release matrix, tag filter and
product catalog are all derived from the product list — so a watch entry would
mean inventing an App Store Connect id and rendering a release leg that uploads
a watch archive.

### A test target the managed workflow still cannot run

For anything the lacquer has no job for, say so rather than leaving the audit to
guess:

```toml
[[project.covered_elsewhere]]
target   = "SomeTargetTests"
workflow = ".github/workflows/some-ci.yml"
reason   = "run by a project-owned workflow; no managed job covers it"
```

**It is checked, not believed.** `audit` opens that file and requires all of it:
the workflow exists, is not one the lacquer writes, names the target outside a
comment, contains a test invocation, and is triggered by a code change. Fail any
one and the target goes back in the uncovered list with the failed check printed
on its line — a declaration that suppressed a finding just by being written
would be a check whose passing state is reachable without the checked thing
having happened, failing open and in silence.

What it proves is that the arrangement is real and current. It does **not**
prove the tests ran or passed, that the mention is the `-only-testing:` selector
rather than a job name, or that the workflow's result is required to merge. If
the suite has to be green before a merge, require that workflow's check on the
branch — this declaration is not a substitute for that.

**There is no `until`**, and that is the deliberate divergence from
`dependabot_ignore`, where a date is required. An ignore is debt with a term the
project can pay: upstream ships, the pin is dropped, or the breakage is
accepted. This is not — the missing capability is in the lacquer, and no date a
project writes brings it closer. An expiry would come due on a project whose CI
is correct and offer two moves: delete a passing suite, or push the date. What
replaces it is stricter, because it is event-driven: the declaration has to go
on verifying. Rename the workflow, delete it, stop naming the target, or rename
the target, and it is reported on the next audit rather than on an anniversary.

## CI Runners

Every synced workflow already sets the correct runner per job — when editing
an existing job, keep whatever `runs-on` it already has; don't re-derive it.
The rule below matters only when authoring a **brand-new** job:

Xcode-touching work (build/test/lint/archive/sign/release) uses
`runs-on: [self-hosted, macOS, ARM64, dedicated]` — never a GitHub-hosted
macOS runner (`macos-latest`) or a stray self-hosted label like `mac-mini`. A
pure script/REST-call job with no Xcode dependency (a docs publish, a
deploy) uses `blacksmith-4vcpu-ubuntu-2404` instead — don't tie up
the Mac for work that doesn't need it.

Two rules about the Linux label, because GitHub bills **per job started, with
a one-minute minimum** — cost tracks job *count*, not duration:

- Ordinary Linux jobs use `blacksmith-4vcpu-ubuntu-2404`. Blacksmith is a
  drop-in `runs-on` replacement that bills separately from the GitHub Actions
  allowance, so these jobs no longer draw down the account's included minutes.
- The `ci-ok` merge gate uses `[self-hosted, Linux, pi-gate]`. It is an
  `if: always()` job that only reads `needs.*.result`, so it starts on **every**
  run and its one-minute minimum was pure waste. `pi-gate` is a **role** label
  carried by more than one box (the Raspberry Pi and the Synology), so the gate
  fails over instead of blocking the whole fleet on a single runner.

See the `macos-ci-recipes` skill for the reasoning and copy-in recipes when
the new job is a macOS-only or hybrid iOS+macOS workflow.

## Editor hooks (.claude/settings.json)

The synced `.claude/settings.json` installs hooks that: block edits to
`.pbxproj`/`.xcworkspace`/`.xib`/`.storyboard`/`.entitlements` (PreToolUse),
run SwiftFormat + SwiftLint on every `.swift` write (PostToolUse), and — on
SessionStart — **auto-approve the Xcode MCP permission dialog** via
`allow_mcp.js` (requires macOS Accessibility permission for your terminal).
That auto-approve is a deliberate convenience; remove the SessionStart hook if
you'd rather approve the Xcode MCP dialog manually.

## Local Checks vs CI

Every CI gate and where it runs before push. See core "Local Checks Match CI" —
a new CI job adds a row here, and a hook never runs weaker than its CI twin,
**except** a job that is itself a build or test run (see the note below the
table).

| CI job / step | Local |
|---|---|
| `Lint` → SwiftLint `--strict` | pre-commit `swiftlint` (**`--strict`**, staged files) |
| `Lint` → SwiftFormat `--lint` | pre-commit `swiftformat` (writes; a changed file fails the commit) |
| `missing_docs` | pre-commit `swiftlint-docs` (**`--strict`**, staged files) |
| `Test` | CI-only — see below |
| `Baseline` | `lacquer audit` (exit 4) — CI-only, it reads the pbxproj |
| `Build (Release)` | CI-only: a full Release archive is not a commit-time cost, and CI now runs it on every code PR — so building Release locally duplicates it on the same Mac |
| `Detect changed paths` → drift audit | `lacquer audit` (exit 3) — run it locally any time |

**No local `xcodebuild test`/`docbuild` hook, deliberately.** This fleet's
self-hosted Mac runner is frequently the very same physical machine you commit
from. On separate hardware, a local build/test hook buys you an earlier signal
before a slower CI run; here it buys nothing but a second, identical
`xcodebuild` invocation on the one shared box you're also trying not to tie up.
`Test` is CI-only for that reason — not an oversight, and not a case of "a
hook never runs weaker than its CI twin," since there is no weaker local
version, only none. Everything else in the table above is static analysis
(lint/format) with no build cost, so it stays local as usual.

The `--strict` flags are the load-bearing part. `line_length`, `file_length`,
`type_body_length` and `function_body_length` are all **warning** severity in
`.swiftlint.yml`, so without `--strict` they print and pass locally and then fail
the PR. A hook carrying `|| true`, or missing `--strict`, is the single most
common way this fleet produces a "worked on my machine" failure — and it is now a
drift violation, not just a bad idea.

**The editor hook counts too.** The `PostToolUse` hooks in
`.claude/settings.json` run on every Swift write, and they were the worst
offender of all:

```
swiftlint lint --path "$FP" --quiet 2>/dev/null || true
```

`--path` has not been a valid SwiftLint option for some time — the command
errored on every single invocation, and `2>/dev/null || true` swallowed the
error, so the hook linted **nothing, ever**, and looked healthy doing it. It now
passes the path positionally, names the project config explicitly, runs
`--strict`, and on a violation exits 2 so the diagnostic reaches the agent that
just wrote the file. Fix it there and it never reaches the commit.

The formatter hook likewise names `--config` explicitly. SwiftFormat does
discover `.swiftformat` by walking up from the file, so this was not silently
formatting to defaults — but relying on discovery breaks the moment a source file
sits outside the component, and an explicit config costs nothing.

Neither hook suppresses stderr any more. A swallowed error is indistinguishable
from a clean run, which is precisely how a dead hook survives for months.
