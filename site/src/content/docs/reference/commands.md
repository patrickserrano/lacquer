---
title: Command reference
description: Every lacquer CLI subcommand.
---

| Command | Does |
|---------|------|
| `lacquer init [--stack S]` | Detect components, write a `.lacquer.toml` stub (and a `docs/brief.md` stub). `--list-stacks` prints the archetypes `--stack` accepts. |
| `lacquer onboard --org O [--no-repo]` | `init`, then create a private GitHub repo under `O` when the repo has no `origin`. |
| `lacquer adopt` | Record stacks that appeared since `init` into `.lacquer.toml` — the fix for `audit`'s exit 6. |
| `lacquer sync [--force] [--fix]` | Render core + per-profile content into the project (managed regions + whole-file assets). `--fix` runs the profiles' autofixers afterwards. |
| `lacquer skills` | Install `[project].skills` entries via the [`skills` CLI](https://github.com/vercel-labs/skills). See [Third-party skills](/lacquer/guides/getting-started/#third-party-skills). |
| `lacquer plugins` | Install `core/bootstrap/plugins.toml` (machine-level Claude Code plugins) via `claude plugin`. See [Plugins](/lacquer/guides/getting-started/#plugins-machine-level-bootstrap). |
| `lacquer doctor [--profile P]` | Prove each check can actually fail; exit 5 if one cannot. `--profile` limits it to one stack's checks, for a runner that has only that toolchain. |
| `lacquer fix` | Run the profiles' autofixers (formatters, `lint --fix`) over the project. |
| `lacquer status` | Show each region's stamped version vs the lacquer's latest. |
| `lacquer audit` | Classify project drift and check the project baseline. Exit 3 if a sync would clobber a local change, 4 on a baseline violation or an expired `[project].exclude`, `dependabot_ignore` or `[[project.not_run_in_ci]]`, 6 if a stack on disk is undeclared (usable as a CI gate). It also reports, without ever changing the exit code, any rendered agent, skill or command that Claude Code would skip silently (frontmatter missing or not on line 1, an agent with no `name` or a `name` containing `:`), and runs `claude plugin validate --strict` over them when the `claude` CLI is installed (otherwise it says "not checked"). |
| `lacquer fleet --roster F [--json]` | Audit every project in a roster; exit 4 if any would fail its own audit. `--json` emits a snapshot. |
| `lacquer fleet diff A.json B.json` | What changed between two snapshots; exit 4 on a regression. |
| `lacquer protection [--repo O/N] [--branch B] [--roster F]` | Compare what branch protection **requires** against what CI can **post**. GitHub counts a skipped check as satisfying a required one, so a repo passes only if it requires the always-running `CI OK` aggregate — or some other context posted by a job nothing can skip. Reaches the GitHub API through `gh`, so it is opt-in and separate from `audit`. Exit 4 on a finding; **exit 7 if a repository could not be checked** — never reported as a pass. |
| `lacquer wait pr <N> [--repo O/N] [--timeout D] [--interval D] [--json] [--inbox F] [--no-inbox]` | Block, in one process, until every check on PR `N` is terminal, then print each check's name, conclusion and duration. **The sanctioned way to wait for CI.** It sleeps between polls, so waiting costs no model tokens: run it in the background and you are woken once, when it returns. Defaults: `--timeout 20m`, `--interval 15s`; `--repo` is inferred by `gh` from the checkout. Four outcomes, each its own exit code, never conflated — see [Waiting for CI](#waiting-for-ci-lacquer-wait-pr). Exits 2, 3 and 4 also put an ACTION in the operator's inbox. |
| `lacquer ci-round begin <N> [--reason TEXT] [--review TEXT] [--sha SHA] [--repo O/N] [--inbox F] [--manifest-ref REF]` | Ask for one of the rounds of CI an **agent** gets on PR `N` (default 2, `[project].ci_round_cap`), *before* the push it covers. The count is recorded on the PR, so a session ending, or a new one picking the PR up, changes nothing. Round 2 must name a failing check or a review request; a third attempt is refused with a report, never a failure. See [Capping CI rounds](#capping-ci-rounds-lacquer-ci-round). |
| `lacquer ci-round reset <N> --reason TEXT` | Explicitly authorize a fresh budget, retaining the ledger audit trail. |
| `lacquer ci-round status <N>` | Record unknown heads as unrecorded pushes or neutral GitHub updates; show rounds by kind and rounds left on PR `N`. Exit `10` if exhausted, else `0`. |
| `lacquer console [--roster F] [--inbox F]` | One screen, with no flags or config: the inbox's open ACTION/UNREAD entries, then every live session on the machine (name, kind, status, project, cwd, age) read from `claude agents --json` (10 s limit). If `claude` is missing or fails it prints `sessions: unavailable — <reason>`, never an empty list; a healthy zero prints `sessions: none running`. `--roster`/`$LACQUER_ROSTER` adds fleet truth and open PRs and maps each session to its project by cwd. The inbox is `--inbox`, else `$LACQUER_INBOX`, else `$XDG_STATE_HOME/lacquer/inbox.jsonl` (`~/.local/state/lacquer/inbox.jsonl`), created with its directory on the first write; a default file that does not exist yet reads as empty, an explicit one that does not exist is reported unavailable. `ci-round` raises its exhaustion ACTION in the same file, and so does `wait pr` (exits 2, 3, 4). With a roster, every run also **harvests PR merges**: one `gh pr list --state merged` per roster repo (20 s limit), one UNREAD `<owner/repo>#<N> merged: <title>` per merge since the repo's cursor in `merge-cursor.json` beside the inbox, deduped by PR URL, and one harvest runs at a time (a lock beside the cursor; a second one is skipped with a note), so two consoles add each merge once. A repo's first look sets its cursor to now and backfills nothing; a `gh` failure is listed under unavailable, never read as no merges; without a roster it says merges are not being recorded. A **background agent going idle** is recorded by a Claude Code Stop hook, `lacquer console inbox hook stop`, that the iOS profile ships in `.claude/settings.json` (timeout 10 s; web, supabase and marketing ship no Claude settings, so their projects do not get it): it reads the hook JSON on stdin and, only when `$CLAUDE_JOB_DIR` is set (a `claude --bg` session; interactive sessions never write), adds one UNREAD `<session name> is idle in <project>: <first line of the last message>` with ref `session:<id>` and the full message (capped at 2 KB) plus the cwd as body. The name comes from `claude agents --json` (else the short id), the project from the roster (else the cwd's base name). At most one open entry per session: while it is open a further idle adds nothing, and once resolved the next one adds a new entry. It skips when `stop_hook_active` is true or there is no last message, always exits 0 (problems are warnings on stderr), and gives up after 5 s. `--sessions` is optional: it names the dispatch records `watch --relaunch`, `watch --live` and `kill` need (they hold the tmux pane, daemon id and worktree that `claude agents` does not report), and plain `watch` without it lists the live sessions. |
| `lacquer console … dispatch` / `dispatch-role` / `watch` / `kill` | Start, check, relaunch, or stop work on a project or a named role. `--mode bg` runs `claude --bg` in a new git worktree and branch under `<repo>/.claude/worktrees/`, and launches nothing if one cannot be made. `--mode tmux` starts a detached tmux session in the checkout itself, which it edits directly; attach with `tmux attach -t <name>`, and a session already running under that name is left alone. `--worktree <path>` runs the session, in either mode, in an existing worktree instead: for the worktree a PM created and named in an IC's brief. It must be a registered worktree of the project's repository (and, for bg, not the checkout itself), or nothing launches; lacquer never creates or removes it, adds `.metadata_never_index` before launch (ignored through global excludes), and records it like one it made, so a relaunch resumes in it and `kill` keeps it. `--branch <name>` (bg only) names the branch of the worktree bg creates, under `.claude/worktrees/` with `/` flattened to `-`, instead of `dispatch/<id>`: for a branch a PM chose. It is refused if the branch or directory already exists (pass `--worktree` for that), and together with `--worktree`. Every console flag works on either side of the subcommand, with the same meaning (`watch --relaunch` is `--relaunch watch`), and among a dispatch task's words, so a trailing `--dry-run` is a dry run; a task word that starts with `-` goes after `--`, which ends the flags (`dispatch <project> -- <task>`). An unknown flag, or one the subcommand has no use for (`--dry-run` with `kill`), is an error; `--roster`, `--roles`, `--sessions` and `--inbox` are accepted by every subcommand. Both modes pass `--dangerously-skip-permissions` with the sandbox off, and neither needs a terminal, so an agent can dispatch. With `--sessions`, every launch attempt is recorded, a failed one included, and `watch` reports a failed launch as failed. `watch --relaunch` puts the relaunched session's record in place of the dead one (a bg session resumes in its recorded worktree), and stops retrying a record after 3 failed launches in a row, leaving it for you. See `lacquer help` for the flag combinations each takes. |
| `lacquer version` | Print labeled content and build versions, plus the resolved content root path. |

`lacquer help` (or `--help`/`-h`) prints usage, including the full `console`
flag surface this table abbreviates.

## Waiting for CI: `lacquer wait pr`

Waiting for CI is the most common thing an agent does, and hand-rolled waiters
keep getting it wrong. One reported `FINAL` and exited 0 while both test jobs
were still running: it branched on `(.conclusion // "PENDING")`, and jq's `//`
substitutes for `null`, not for the empty string a check **in flight** reports.
`gh pr checks --watch --fail-fast` is no substitute: it exits 0 even when checks
fail. `lacquer wait pr` puts the predicate in one tested place.

```sh
lacquer wait pr 425                      # blocks; prints every check at the end
lacquer wait pr 425 --timeout 45m --json
```

| Exit | Outcome | Meaning |
|------|---------|---------|
| `0` | passed | Every check is terminal and none failed. Skipped checks are named on a `skipped:` line. |
| `1` | failed | At least one check failed (or was cancelled, timed out, needs action, or concluded something unrecognised). Each is named. A failure is decisive: if the ceiling hit with other checks still running it is still `1`, and the running ones are listed as abandoned. |
| `2` | timed out | `--timeout` hit while a check was still running and **none had failed**. The running checks are named. **Not a failure and not a pass**: the result is unknown. |
| `3` | no checks | The PR reports no checks, so nothing tested it. **Never a pass.** |
| `4` | could not wait | `gh` is missing or kept failing, the PR is closed or merged, or the usage was wrong. The PR's state is unknown. |

Choices worth knowing:

- **Empty is not green.** "No check is non-terminal" is trivially true of zero
  checks. An empty rollup is re-checked for `--empty-grace` (default 30s), because
  workflows register a moment after a PR opens; if checks appear they are judged
  normally, and if none do the answer is exit 3.
- **Skipped exits 0, loudly.** A skipped job did not run, and a skipped *required*
  job is how a PR looks green untested, so every skipped check is named in the
  output. If every check was skipped it says `PASSED, BUT NOTHING RAN`. Read that
  line before treating exit 0 as "tested".
- **Both check shapes are read.** `CheckRun` entries carry `status` and
  `conclusion`; legacy commit statuses (`StatusContext`) carry `state` and no
  `status`. A pending commit status is still running.
- **All terminal must hold for two polls.** The first reading can predate a slower
  workflow registering; a check that appears in between is not missed.
- **Only the latest run of a check counts.** The rollup lists every workflow run on
  the commit, so editing a PR while CI runs leaves a `cancelled` check from the run
  the concurrency group killed beside the latest run's real one. For the same
  workflow and check name across different runs, only the newest run is judged, as
  on GitHub's checks tab; the ignored entries are listed (`ignored, superseded by a
  newer run`) so nothing is hidden. Two same-named jobs in one run, or entries
  with no run id, are never collapsed.
- **A known failure beats a timeout.** A failure is a fact and CI cannot become
  green from it, whereas a timeout means "not known yet". If the ceiling hits with
  a failure and some checks still running, the exit is `1`; the running checks are
  still listed, marked as abandoned, and their results no longer matter. A caller
  deciding whether to spend another CI round can key on the exit code alone.
- **It writes the inbox for you.** A wait that ends timed out (2), untested (3) or
  unable to run (4) raises an inbox ACTION (`<owner/repo>#<N>: ...`, ref the PR's
  URL, body the head sha and the reason), so a stuck PR reaches the operator
  without anyone remembering to say so. Nothing is written for `0`, nor for `1`
  (a failed check: the author is already fixing it, and the operator's stuck view
  raises a PR left failing), nor when the PR is merged or closed or the wait was
  interrupted. The same open entry for the same PR and head commit is never added
  twice. The file is `--inbox`, else `$LACQUER_INBOX`, else the default under
  `$XDG_STATE_HOME`, as for `console`; `--no-inbox` turns the write off. If the
  write fails it prints `WARNING ... NOT written` on stderr and the exit code is
  unchanged.
- **A new head commit mid-wait** means new checks. The old commit's results are
  discarded (and the output says `head moved a -> b`), and the wait continues on the
  new commit. The ceiling is **not** reset: `--timeout` bounds the whole wait.
- **A PR that is closed or merged** ends the wait with exit 4; its checks no longer
  decide anything.
- **`gh` failures are retried**, five in a row before the wait gives up with exit 4
  and gh's own message. A failed call is never read as "no checks" or as success,
  and a `gh` response with no `statusCheckRollup` is an error, not an empty list.

## Manifest shape

A project opts in via `.lacquer.toml` at its root:

```toml
[project]
name = "my-app"
project_name = "MyApp"
scheme = "MyApp"
bundle_id = "com.example.myapp"
asc_app_id = "0000000000"
xcodeproj = "MyApp.xcodeproj"
swift_version = "6.0"
github_org = "my-org"
tools = []
exclude = []
skills = ["dpearson2699/swift-ios-skills@healthkit"]

[[component]]
path = "."
profiles = ["ios"]
```

`core` applies to every project regardless of `[[component]]` entries. A
component detected as an unshipped stack (e.g. Rust/Go) is recorded with an
empty profile list and a notice — it doesn't break `sync`.

`optional_workflows` opts into a workflow the lacquer ships but does not install
by default, named without its `.yml`:

```toml
optional_workflows = ["some-workflow"]
```

**The profiles currently ship none**, so this installs nothing today and a name
with no file behind it is an error rather than a silent no-op. The mechanism is
kept because the reason it exists has not gone away: a workflow needing
credentials nobody has doesn't fail loudly, it fails **daily and quietly**.
`testflight-feedback` was the case that produced it — it wanted
`APP_STORE_CONNECT_FEEDBACK_ISSUER_ID` and two siblings, no project in this fleet
had them, and it was red on every scheduled run in every repo that received it.
A scheduled job nobody can satisfy is worse than a missing feature: it trains
people to ignore red. It has since been removed outright rather than left
default-off, because in three years nothing opted in.

A name with no matching file is an error, not a silent no-op.

### Retired projects

`retired` marks a project that is no longer worth investing in but is not being
deleted:

```toml
[project]
retired = { since = "2026-08-18", reason = "not a viable app" }
```

Retired means **stop the spend, stay consistent.** `sync` keeps shipping
everything that holds the repo to the fleet's shape — PR-triggered CI, lint and
format configs, `CLAUDE.md` / `AGENTS.md`, `.gitignore`, `.gitattributes`, hooks,
skills — so the project still audits clean and can be picked back up. It stops
shipping everything that costs money or attention **on a schedule**:

| Dropped | Kept |
|---------|------|
| Any workflow whose `on:` block has a `schedule:` trigger (`ios-cleanup-ci.yml`, `supabase-health.yml`, and `ios-testflight-feedback.yml` where opted in) | `*-ci.yml`, `web-dependency-review.yml`, `web-env-validation.yml`, `ios-release.yml` |
| `.github/dependabot.yml` | every non-workflow asset |

"Is scheduled" is read from each workflow's **content**, not from a list of
filenames — a filename list silently misses the next scheduled workflow someone
adds. A workflow declaring `workflow_dispatch:` beside `schedule:` is still
dropped: the dispatch entry is a convenience, the cron is what runs unattended.

Both fields are required and a malformed entry fails the load. `retired = true`
records that someone retired the project and not why, and six months later why is
the only thing anyone wants to know. Unlike `[baseline.relax]` and the dated form
of `exclude`, retirement takes **no `until`** — it is not debt with a term, and an
expiry would either be rubber-stamped forever or quietly turn a dead project's
cron jobs back on.

`lacquer status` and `lacquer audit` both lead with the retirement and its date,
and `audit` still exits 0: the dropped assets stop being managed units, so they
read as neither drift nor missing. Nothing is deleted — files already in the repo
stay until someone removes them by hand.

`skills` entries are `"<owner>/<repo>@<skill-name>"` strings, installed by
`lacquer skills` — see [Third-party
skills](/lacquer/guides/getting-started/#third-party-skills).

### Multiple products from one repo

A repo that ships more than one App Store app — a paid app and a free or lite
sibling built from the same source — declares each as a `[[product]]`:

```toml
[[product]]
name = "MyApp"
scheme = "MyApp"
bundle_id = "com.example.myapp"
asc_app_id = "0000000000"
tag_prefix = "myapp"

[[product]]
name = "MyApp Lite"
scheme = "MyAppLite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "1111111111"
tag_prefix = "myapplite"
```

Declaring none is the normal case: `[project]` is then treated as the single
product, and every tag releases it.

`tag_prefix` is required once a project declares more than one product — a blank
prefix means "every tag releases this", which is right with one product and
incoherent with two. It also **derives the release workflow's push-tag filter**:
a repo whose products are prefixed `steps-v` and `stepsfree-v` triggers on those
patterns, not on `v*`. A project declaring no products keeps the historical
`v*`.

`tag_prefix` decides which product a tag releases. `myapp-v2.1.0` releases
MyApp and leaves the Lite app alone. **One tag must release exactly one
product.** An App Store version train closes permanently once its version
reaches `READY_FOR_SALE`, so a tag that fanned out to both apps would push the
already-shipped one at a closed train and fail with error 90186 — every time,
for the life of that version.

A tag matching no product's prefix fails the release rather than guessing;
guessing signs a product with another app's credentials. A product with a blank
`tag_prefix` matches any tag, which is what makes the single-product case work
unchanged.

### Per-product CI targets

The iOS CI workflow builds and tests **every** declared product — one
`Build (Release)` leg and one `Test` leg each, with `fail-fast: false` so one
product failing does not cancel the other's run. Four optional fields describe
the test leg:

```toml
[[product]]
name = "MyApp Lite"
scheme = "MyAppLite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "1111111111"
tag_prefix = "myapplite"
test_target = "MyAppLiteTests"   # defaults to "<scheme>Tests"
ui_test_target = ""              # blank = no UI tests for this variant
extra_test_targets = ["CoreKitTests"]  # local package suites to run as well
app_target = "MyApp.app"         # coverage target; defaults to "<scheme>.app"
```

Target defaults use `name` only when `scheme` is empty. Explicit
`test_target` and `app_target` values override these defaults.

`extra_test_targets` adds `-only-testing:` selectors for suites the app's own
bundle does not contain — typically a local Swift package's test target, which
is otherwise run by nothing while xcodebuild still exits 0. Declaring any turns
on a CI step that reads the result bundle back and fails the job for a selector
that matched no tests; blank and repeated entries are rejected at load, because
both render a selector that runs nothing. The pre-commit `Swift Tests` hook runs
the same extras.

`audit` checks each selector against the targets that exist. A native test
target in `project.pbxproj` counts, and so does a `.testTarget` in the
`Package.swift` of a local package the project references
(`XCLocalSwiftPackageReference`, resolved from the `.xcodeproj`'s directory). A
selector found in neither place is reported as naming a target that does not
exist. If a referenced package can't be read, the selector is reported as
*could not check*, not as missing, and the report gives the reason. That happens
when the manifest is absent or declares a test target whose name is computed
rather than written as a string.

Package suites are part of the "no selector covers it" report too, with one
difference: a package suite can also be run by `swift test` in its package, so
it is reported only if no workflow a pull request starts runs it either. The
audit recognises `swift test` in the package (by `--package-path`, a step's or a
job's `working-directory`, or a `cd`), an `xcodebuild test` or `flowdeck test`
that selects the suite or runs a scheme testing it (including the scheme Xcode
generates for a package), and the same commands inside a script in the
repository that the step runs. It does not recognise `swift build
--build-tests`, which compiles the suite and runs none of it. A suite run some
other way can be declared in `[[project.covered_elsewhere]]`, and a suite
deliberately run in no CI job in `[[project.not_run_in_ci]]`, both below. If the
package can't be read, or a workflow that might run the suite can't be (a
`${{ matrix }}` directory, a scheme that isn't committed), the suite is
reported as *could not check* rather than as running nowhere.

`app_target` is declared, not derived: a scheme and the product it builds
genuinely differ in real projects, and a wrong target selects no coverage row at
all — which reports 0.0% rather than failing.

Each leg's CI simulator and test-results artifact are scoped by a slug derived
from the product name, so two legs on one runner cannot delete each other's
simulator or collide on an artifact name. Two products whose names reduce to the
same slug are rejected at load.

**A project declaring no products renders the CI workflow byte-for-byte as it
did before products existed** — the matrix machinery expands to nothing.

### A target the managed workflow cannot run

`audit` reports every test target in the Xcode project that no selector names.
Some targets are unreachable from the iOS test leg by construction — a watchOS
bundle is a testable of a different scheme, and the workflow carries one
`platform=iOS Simulator` destination — so the only way to run them today is a
project-owned workflow. Declare that, rather than leaving the audit wrong about
it:

```toml
[[project.covered_elsewhere]]
target   = "DailyBreadWatchApp Watch AppTests"
workflow = ".github/workflows/watch-ci.yml"
reason   = "watchOS bundle: different scheme, watch simulator destination"
```

The declaration is verified on every audit, never taken on trust: the workflow
must exist, must not be one the lacquer writes, must name the target outside a
comment, must contain a test invocation, and must be triggered by a code change.
Anything short of that and the target is reported again with the failed check
printed beside it. There is no `until` — the declaration expires by ceasing to
verify, not on a date, and a declaration naming a target the project no longer
has is reported as stale.

### A suite deliberately not run in CI

Some suites are run on purpose somewhere CI can't reach. momfriend's
`MomFriendCoreTests` needs on-device models and is written to fail, not skip,
without them, so CI builds it and never runs it. The audit is right that nothing
in CI runs it, and none of its suggested fixes applies. Say so, with a reason and
a date:

```toml
[[project.not_run_in_ci]]
target = "MomFriendCoreTests"
reason = "needs on-device models; built in CI, run on device before release"
until  = "2026-12-31"
```

All three fields are required. `until` is `YYYY-MM-DD` and covers the whole of
that day. It works for native targets and local-package suites alike, and it
needs `[project].xcodeproj`, because that is where the audit reads test targets
from. A target can't be declared in both this and `covered_elsewhere`, since only
one of them can be true.

While the declaration is in term, the suite leaves the "no selector covers" list
and is printed on a line of its own, so it stays visible:

```
deliberately not run in CI: MomFriendCoreTests — needs on-device models; built in CI, run on device before release (until 2026-12-31)
```

**Past `until`, it expires.** The suite goes back in the report, the expiry is
named, and `audit` exits 4, the same as an expired `dependabot_ignore`. This is
the divergence from `covered_elsewhere`, which has no date because the project
holds no remedy for it. Here the project does hold the remedies: make the suite
runnable in CI, delete it, or review the reason and set a new date.

A declaration is reported as **stale** when it no longer describes a gap: the
target doesn't exist, or something now runs it (a selector, a verified
`covered_elsewhere`, or a workflow the audit sees running the suite). Stale
declarations are reported but don't gate. Remove them.

### Release-time secrets

A project that needs real values at release — monetization SDK keys, ad unit
IDs, an analytics key, a crash-reporting DSN — maps each xcconfig key to the
GitHub secret holding it. A single-app project (no `[[product]]` block) declares
them under `[project]`, where they fold into the product the manifest
synthesises — the same fallback `scheme`, `bundle_id`, `asc_app_id` and
`extra_test_targets` have:

```toml
[project]
name = "MyApp"
scheme = "MyApp"
bundle_id = "com.example.myapp"
asc_app_id = "1111111110"
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY", SENTRY_DSN = "SENTRY_DSN" }
secret_formats = { REVENUECAT_API_KEY = "appl_*", SENTRY_DSN = "https://*@*/*" }
```

A project with several products declares them per product, since each app's
keys are its own. Setting `secrets`, `secrets_file` or `secret_formats` under
`[project]` alongside any `[[product]]` block is rejected rather than merged,
because which product they belong to would have to be guessed:

```toml
[[product]]
name = "MyApp Lite"
scheme = "MyAppLite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "1111111111"
tag_prefix = "myapplite"
secrets_file = "Config/Monetization.xcconfig"   # optional, defaults to Secrets.xcconfig
secrets = { REVENUECAT_API_KEY = "LITE_REVENUECAT_KEY", ADMOB_APP_ID = "LITE_ADMOB_APP_ID" }
secret_formats = { REVENUECAT_API_KEY = "appl_*", ADMOB_APP_ID = "ca-app-pub-*~*" }
```

`secret_formats` optionally constrains a value's **shape**, as a shell glob
checked at release time. Non-empty is not the same as correct: the two ways
these keys actually go wrong — pasting another app's key, or leaving Google's
public test AdMob ID in place — both produce a perfectly non-empty value that
builds, signs, uploads and passes review, then serves the wrong ads to real
users. A mismatch fails the release without echoing the value. A pattern may
use letters, digits and `_ ~ . : / * ? @ -`; anything else (quotes, `|`, `(`,
`&`, spaces) is rejected at load, because the pattern is used unquoted in a
shell `case`.

The manifest holds the secret's **name**; the value stays in GitHub. `lacquer`
rejects a value that looks like a real credential, because this file is
committed.

The release writes those values into the xcconfig on that product's matrix leg
only, and **fails if any of them is unset or empty**. It does not fall back to
the placeholder seeding `ci.yml` does: CI seeds placeholders because tests must
run without production keys, whereas a release doing the same would sign and
ship an IPA wired to `appl_xxxxxxxx`, with nothing looking wrong until the
revenue didn't arrive. A product that declares no secrets renders no step, which
is the normal case.

A `workflow_dispatch` run picks its product from a dropdown, defaulting to
`all`. Scoping matters there too: a dispatch of `all` after one app has shipped
a version hits the same closed train. Naming an unknown product fails rather
than falling back to everything.

## Capping CI rounds: `lacquer ci-round`

An agent gets **at most two rounds of CI on a pull request**. If checks still fail
after the second push it stops and hands the PR to a human instead of pushing a
third time. Re-running CI is the most expensive reflex an agent has (minutes of a
shared Mac, plus a whole agent context to read the result), and a third attempt is
almost never a fix: it is a guess. Forcing the stop turns "push again and see" into
"say what you do not understand". Until this command it was persona prose, which
depends on every agent reading and honouring it; now the tool enforces it.

```sh
lacquer ci-round begin 431                       # round 1: after the PR exists, before the push
lacquer wait pr 431                              # exit 1: lint failed
lacquer ci-round begin 431 --reason "lint failed: unused import in wait.go, removed it"   # round 2
lacquer ci-round begin 431 ...                   # exit 10: EXHAUSTED. Do not push
```

`begin` records the commit you are about to push (`--sha`, default `git rev-parse
HEAD`), so run it **before** `git push` and push exactly that commit. It is
idempotent for a commit it already granted.

For a review-requested correction, use
`lacquer ci-round begin 431 --review "PM requested correcting the misleading comment"`
instead of `--reason`. This works on green CI and consumes a round under the same
cap. The ledger records kind `review`; status shows, for example,
`2/2 used (1 failure, 1 review)`. There is no comment-only exemption.

| Exit | Meaning |
|------|---------|
| `0` | Granted, or already recorded: push this commit. |
| `10` | **Exhausted.** A report, not a failure: the PR is not failed or closed and nothing is pushed. It comments on the PR with what is still failing, raises an inbox ACTION in the default inbox (or `--inbox` / `$LACQUER_INBOX`); if that cannot be written it prints the exact `console inbox add` command to run, and prints what was spent. Do not push; say what you do not understand. |
| `11` | Round 2 or later needs `--reason` naming at least one check the previous round reported failing (at a word boundary, in a reason of four or more words). The refusal lists the names it will accept. Reset also requires a non-empty reason. No new push is granted; an observed unrecorded head still counts. |
| `12` | **Nothing to spend a round on.** Either the previous round reported no failure (its checks are still running, `lacquer wait pr` exit 2; it has none, exit 3; or they passed), without a review request, or the commit you named is already the PR's head without a grant. No new push is granted; an observed unrecorded head still spends a round. |
| `13` | The tool **could not check**: `gh` failing (`lacquer wait pr` exit 4), the PR closed, an unreadable ledger or manifest, bad usage. It cannot say a round is allowed, so do not push. |

The codes start at 10 so none is read as `lacquer wait pr`'s 0-4. The mapping to
that command is the point: only a wait that exits **1** leaves something concrete
to fix for a failure-driven round. Review requests use `--review` instead;
exits 2, 3 and 4 do not themselves justify a failure-driven round.

Choices worth knowing:

- **The count lives on the PR.** Each round, update, reset and stop is one comment opening
  with an HTML-comment marker holding one JSON object (round number, head SHA, UTC
  time, the failing checks addressed, the reason), above prose saying the same
  thing, so the stop is visible without opening CI *and* parseable. Append-only,
  not one comment edited in place: a lost update would silently refund a round,
  whereas two racing appends are ordered by GitHub and the later one is told it
  lost. Only comments from an `OWNER`, `MEMBER` or `COLLABORATOR` count, so a
  stranger on a public repo cannot forge a stop or a reset. A ledger comment the
  tool cannot read is exit 13, never skipped.
- **An unrecorded push spends a round.** Agents and humans share an account, so
  the tool cannot infer authorship. Both `begin` and `status` append an
  `unrecorded` entry for a newly observed unknown head, once per SHA, except for
  the GitHub update-branch case below. Local merge commits pushed without `begin`
  are charged too.
  The initial `begin` can register the head that opened the PR as round 1.
  Only observed heads can be accounted for; this does not inspect every intervening
  push or install a pre-push hook.
- **GitHub update-branch merges are neutral.** For an unknown head, the commit API
  must report committer email `noreply@github.com`, exactly two parents, and the
  previous known head as one parent. The tool records kind `update`, marks the SHA
  known, and neither spends a round nor resets the budget. Later observations
  do not charge it. Any other unknown head is still `unrecorded`; if the commit
  API fails, no push is granted (exit 13).
- **Only explicit reset refills the budget.** A human authorizes
  `lacquer ci-round reset <N> --reason "<why>"`. This writes a `reset` entry;
  subsequent rounds count fresh, while earlier entries remain on the PR.
- **Failure-driven round 2 must address round 1.** The cap is on guessing, not on rounds. A reason
  that is empty, too short, or names no failing check is refused. It is a forcing
  function for saying which failure you understand, not proof that you fixed it.
- **Check state is `lacquer wait pr`'s**, not re-derived: a cancelled job from a
  superseded run does not read as a failure on a green PR.
- **The cap is read from `--manifest-ref` (default `origin/main`), not the working
  tree.** A branch that could edit its own `.lacquer.toml` could raise its own cap.
  A ref with no manifest means the default.

### `[project].ci_round_cap`

```toml
[project]
ci_round_cap = 2   # optional; 1-10, default 2
```

Below 1 would refuse the push that opens the PR, and a large cap is no cap, so
both are rejected at load rather than read as "use the default".
