# Core engineering rules (all projects)

## Git and workspace safety

- Work in a git worktree under `.worktrees/`; keep build output in that worktree.
- Never push directly to main. Use atomic commits and a pull request.
- Never force-push or rebase a pushed branch. Do not bypass hooks, CI or branch
  protection (`--force`, `--force-with-lease`, `--no-verify`, `--admin`).
- Never merge with failing or pending required checks. After updating a branch,
  verify the required checks ran again and passed on its new head.
- Never commit or log secrets. Commit examples, not credentials; sensitive
  server keys never belong in a client binary or bundle.

## Verification

- Deliver code proven to work: compile/build, update related tests, and run them.
  Pre-existing failures need fixing or explicit guidance, not a workaround.
- **prove the check can fail**: feed known-bad input or mutate the implementation,
  confirm a named test rejects it, restore it, then confirm the test passes.
  A skipped check, zero selected tests, or an unreadable result is not a pass.
- Treat compiler/linter warnings as errors. Fix the code; never suppress warnings,
  weaken a hook, hide stderr or add `|| true` to a gate without user approval.
- Local checks match CI strictness; a new gate needs its local counterpart or an
  explicit CI-only rationale. Keep managed files identical or explicitly excluded
  through `.lacquer.toml`; never silently fork a generated check.
- Report only what this session's tool output proves; label unverified or skipped work.

## CI and compaction

- Wait with `lacquer wait pr <N>`, not polling or `gh pr checks --watch`.
  Exit 0 means passed; 1 failed, 2 timed out, 3 no checks, 4 wait failed.
  Only 0 is green. The `github-ci-fix` skill carries the recovery procedure.
- Before a follow-up push: `lacquer ci-round begin <N>` with `--reason` for a
  failure fix or `--review` for requested changes. Both spend the two-round budget
  (or configured cap). Unrecorded pushes spend it too; stop on exit 10 and surface
  the ACTION. Never reset the budget without human authorization.
- Across auto-compaction preserve the PR number, branch, worktree path, CI-round
  state (spent/remaining and latest result), verification evidence and next action.
  Resume from that state; do not reset the budget or switch worktrees.

## Recorded decisions

- Before briefing or starting work, run `lacquer decisions` and
  `lacquer decisions --fleet`: the operator's words, to quote verbatim in a brief.

## On-demand procedures

Load the skill matching the task; its references retain the full procedures:

- `engineering-workflow`: implementation/review, delegation, context handoff,
  response style and the optional machine-local papercuts log.
- `project-documentation`: brief → PRD → PCD → plan, doc comments and docs checks.
- `working-with-lacquer`: audit/sync, exclusions, baseline relaxations,
  dependency-update refusals and retirement.
- `github-ci-fix`: failed checks, CI hygiene and round accounting details.

# iOS / Swift profile rules

## Project and build safety

- Never hand-edit `.pbxproj` or `.xcodeproj/` contents beyond `MARKETING_VERSION`.
  Use `scripts/bump-marketing-version.sh` for that exception; for file membership,
  check XcodeGen first, then synchronized groups, otherwise ask for Xcode changes.
  The `ios-project-development` skill carries the procedure.
- Never modify `.xcworkspace`; changing dependencies, deployment targets or
  `.entitlements` requires explicit user permission.
- Build data stays in the worktree: pass
  `-d "$(git rev-parse --show-toplevel)/DerivedData"` to FlowDeck build/run/test/clean,
  or the same path via `-derivedDataPath` to raw Xcode queries and builds.
  Never share DerivedData across worktrees. Create `.metadata_never_index` before
  any build-output directory is populated; preserve the `DerivedData*` naming.
- Interactive build/run/test and simulator mutations use FlowDeck; CI's raw
  Xcode commands are deliberate. Read `ios-build-verification` before selecting
  tests: partial selection can silently drop parameterized cases. Verify actual
  results and counts, not merely exit status. Tests over 300 seconds are hung;
  stop and investigate.
- FlowDeck streams stdout only. `scripts/sim-os-log.sh` is the sanctioned exception
  for read-only `os_log` from the run's own simulator; mutation stays with flowdeck.
  Disclose the command and why stdout was insufficient in the PR body.
  Details: `ios-build-verification`.

## Secrets and release safety

- App-runtime public keys live in gitignored `Secrets.xcconfig`; examples alone
  must never feed a release. Declare release secret names and formats in the
  manifest. CI/server credentials (including RevenueCat `sk_…`) never enter the app.
- Use `ios-secrets-setup` for wiring and `scripts/write-release-config.sh` for
  release config. Release only a SHA with successful completed CI; tags must
  point to a commit reachable from the default branch. Read `ios-release-guide`.
- Release archives use the configured archive volume, never the checkout;
  missing storage must fail, not fall back. ASC processing delays are not proof
  an upload failed; do not blindly retry a non-idempotent upload.

## On-demand procedures

- `ios-project-development`: file membership, architecture, Swift Testing,
  SwiftData, DocC and subscription-skill routing.
- `ios-ci-configuration`: product matrices, extra/watch test targets,
  runner placement, editor hooks and local/CI parity.
- `ios-build-verification`: FlowDeck commands, selector traps, runtimes,
  simulator logs and worktree troubleshooting.
- `ios-release-guide`: App Store requirements, archive location, upload recovery.
- `ios-secrets-setup`: runtime vs server secrets and release provenance.
- `ios-ui-verification`: accessibility-tree interaction, contrast, SwiftUI API
  pitfalls, concurrency and battery/performance guidance.
