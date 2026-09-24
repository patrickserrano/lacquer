## Build data stays in the worktree

Use `-d "$(git rev-parse --show-toplevel)/DerivedData"` on every FlowDeck
`build`, `run`, `test`, and `clean`; use `-derivedDataPath` with that same path
on raw `xcodebuild`, including settings, list, and package-resolution queries.
The Bash PreToolUse hook rejects these commands without an explicit path.

## Build & Test Tooling (flowdeck)

**In an interactive session, reach for `flowdeck` first** — build, run, test, simulator, device, logs, UI automation. It resolves schemes and destinations, keeps DerivedData where you point it, and returns structured output, so it beats hand-assembling an `xcodebuild` line every time.

```bash
flowdeck simulator list           # find an available simulator UDID (names are ambiguous across OS versions)
flowdeck build -w Rootapp.xcodeproj -s <YourScheme> -S <udid> -d DerivedData
flowdeck test  -w Rootapp.xcodeproj -s <YourScheme> -S <udid> -d DerivedData
flowdeck project packages update  # bump SPM deps within constraints (no .pbxproj edit)
```

**Prefer a UDID over a simulator name** — names duplicate across OS versions and resolve ambiguously.

**Two flowdeck test selectors silently run fewer tests than you asked for.**
Both turn a genuinely failing suite green. Measured 2026-09-09 against a
purpose-built probe project on flowdeck 1.26.5 / Xcode 27.0 (27A266a), with a
parameterized test whose `n == 3` argument case is written to FAIL — so "did the
argument cases run?" is answered by whether the run goes red, not by a count:

| invocation | ran | verdict | exit |
|---|---|---|---|
| everything (baseline, 19 declared) | 19 | 1 failed | 1 |
| `--test-targets AlphaTests` | 3 | 1 failed | 1 |
| `--test-targets AlphaTests,BetaTests` | 9 | 1 failed | 1 |
| `--only AlphaTests/AlphaSuite` | **2** of `Resolved to 3` | **All tests passed!** | **0** |
| `--test-cases AlphaTests/AlphaSuite` | **2** of `Resolved to 3` | **All tests passed!** | **0** |
| `--test-targets AlphaTests --test-targets BetaTests` | **6** | **All tests passed!** | **0** |

**1. `--test-cases` and `--only` both drop `@Test(arguments:)` cases.** They
expand to per-function selectors, so every argument case disappears. The failing
canary never ran and the run reported success. `--only` carries the identical
bug to `--test-cases`; only `--test-cases` was documented here before.

**2. Repeating `--test-targets` silently discards all but the last.** It is a
single-value option — `flowdeck test --help` spells it *"comma-separated"* — so
a second `--test-targets` overwrites the first with no warning. Above, the
target holding the failing test was thrown away and the run went green.
Reversing the order ran 3 and failed 1, which is the tell: **the result depends
on flag order.**

- **Pass one `--test-targets` with a comma-separated list** —
  `--test-targets A,B,C`. This is the supported spelling, it unions correctly
  (all three of the probe's suites gave 19, identical to the baseline), and it
  keeps the failing case. Do not repeat the flag, and do not loop one invocation
  per target to work around repeating it.
- **Never use `--only` or `--test-cases` on a suite that contains — or could
  later contain — a parameterized test.** Keep them for a single
  non-parameterized function in a tight RED/GREEN loop, and re-run the full
  target before believing a result you intend to report or commit behind.
- If a run's passed count is **lower than its own `Resolved to N`**, tests were
  skipped. That discrepancy is printed; it just is not acted on.

**What is NOT a trap here: a selector that matches nothing is a hard error.**
`--test-targets NopeTests`, `--only AlphaTests/NoSuchSuite`, and a typo inside a
comma list (`--test-targets AlphaTests,Typoo`) each exited **1** with
"Test run failed". Misspelling a target is loud. The silent failure is
specifically the *partial* selection above — which is why the comma form is the
safe one: its bad names are caught, and its good names all run.

This is the *"never ran looks like passed"* failure sitting inside the test
runner, which is the last place it can be caught by reading a result. Anything
built on top of it inherits a silently smaller denominator — a green targeted
run, a coverage figure, a report that says "N/N passed".

**Check `flowdeck --version` before trusting `$?` from `flowdeck build` or
`flowdeck test`.** Before 1.26.5, a genuine build or test failure — and a plain
usage error — exited `0`. From 1.26.5 the exit code is correct: test failure,
compile error, unknown scheme and missing required flag all exit `1`.

- **On anything older than 1.26.5, do not use `$?`**: check the printed output
  for `✗`/`Error`/"failed", or parse `--json` and read its `success`/`failed`
  fields, because `flowdeck test && echo passed` prints "passed" after a real
  failure.
- **The exit-code fix does not rescue the selector bugs above, and this is the
  part that still bites.** A `--only`/`--test-cases` run that skipped every
  parameterized case exits `0` *legitimately* — every test it chose to run did
  pass. A correct exit code on the wrong denominator still means nothing, so
  the selector rules stand on their own.
- When measuring an exit code, capture it from the command itself
  (`out=$(flowdeck test ...); code=$?`). `flowdeck test ... | tail -20; echo $?`
  reports **`tail`'s** status and will read `0` no matter what flowdeck did —
  a mistake made while gathering exactly these numbers.
- If you are the one writing a pre-commit hook, CI step, or any script that
  gates on a flowdeck command's result, gate on the parsed output, not the
  shell's `$?`.

**`flowdeck simulator runtime install` reports success without installing
anything — same bug, different subcommand.** Verified 2026-09-03:
`flowdeck simulator runtime install <platform> <version>` printed "✓ Platform
installed successfully", returned `"success": true`, and exited `0` in under
2 seconds for a runtime requiring a ~9.8GB download — but nothing was
installed. Confirmed absent from `flowdeck simulator runtime list --json`, no
new CoreSimulator volume created, no disk space consumed. Same failure shape
as the build/test exit-0 bug above, this is FlowDeck's own bug, not something
this profile can fix. **The build/test half of that shape was fixed in 1.26.5;
this subcommand has not been re-measured since, so do not assume it was fixed
too.**

- **Always verify with `flowdeck simulator runtime list --json` after any
  `runtime install` — don't trust the command's own exit code or `success`
  field.** A script that does `flowdeck simulator runtime install ... && echo
  done` will print "done" after installing nothing.
- This matters most after deleting a runtime that looked stale
  (`deletable: true`, no SDK resolved to it) — a silently no-op'd reinstall
  can leave you believing simulators that depend on it are fixed when they're
  still completely unbootable.

**This is a preference, not a wall — nothing blocks the raw tools.** The synced `.claude/settings.json` blocks exactly three things: Xcode project files, `.entitlements`, and force flags (see *Editor hooks* below). `simctl` and `devicectl` are **not** blocked; if you run one it will simply work. Reach for flowdeck because it is better here, not because something will stop you.

**`os_log` is invisible to flowdeck — read it with `scripts/sim-os-log.sh`.** flowdeck's log stream carries stdout only, and SwiftUI and CoreData diagnostics go to `os_log`, so a bug that only logs there looks like silence. Reading `os_log` from **the run's own simulator** (the UDID you passed to `flowdeck build -S`) is a sanctioned, read-only exception to "reach for flowdeck first":

```bash
scripts/sim-os-log.sh <udid> --subsystem <your.bundle.id> --last 10m
scripts/sim-os-log.sh <udid> --predicate 'subsystem == "com.apple.coredata"' --last 2m
```

The script runs exactly `xcrun simctl spawn <udid> log show --last <window> …`, always with a bounded window (default `5m`), and prints that command line to stderr. It refuses `booted`, an empty UDID, a UDID this user's CoreSimulator does not list, and any argument that is not one of `--last`, `--predicate`, `--subsystem`, `--style`. Everything that mutates a simulator or device — boot, shutdown, erase, delete, install, launch — stays with flowdeck. A session following a global flowdeck skill that says to avoid `simctl`/`xcrun` should treat this as the sanctioned exception: use the script rather than a hand-typed `xcrun simctl`, and **disclose the read in the PR body** (the line the script printed, and why stdout was not enough).

**Raw `xcodebuild`/`xcrun` is correct in non-interactive contexts, and this profile ships it that way.** `.pre-commit-config.yaml` runs `xcodebuild test` with an explicit `-scheme`/`-destination`; `ci.yml` and `release.yml` run `xcodebuild` for build, test and archive, `xcodebuild -showBuildSettings` in the Baseline job, and `xcrun simctl` for the whole simulator lifecycle. Those pin their destination and toolchain deliberately, and their output is parsed by the steps around them. Do not "fix" a hook or a workflow to call flowdeck instead — that is a change to what CI actually verifies, not a style cleanup.

### Working in worktrees

- Pass a **unique derived-data path per worktree** (`-d DerivedData-<feature>`) so parallel worktrees don't collide on one DerivedData dir (collisions surface as SIGKILL test crashes).
- **Delete that derived-data dir before running format/lint** — otherwise it lints compiled dependency sources and reports phantom `file_length`/format violations. (The `.swiftformat`/`.swiftlint.yml` excludes cover `DerivedData*`; keep your path matching that glob.)
- **Ignore SourceKit diagnostics in a fresh worktree** (`No such module 'X'`, `Cannot find type`) — the worktree has no built index, so they're false positives. The authoritative signal is `flowdeck build` / `flowdeck test`'s printed output — not its exit code (see above).

## Test Timeout Rule

Tests must NEVER run longer than **5 minutes (300 seconds)**. If tests exceed 5 minutes, they are hung. Kill the process immediately and investigate. When invoking builds/tests via a Bash tool, set a 300000 ms timeout.
