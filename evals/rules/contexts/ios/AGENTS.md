# Engineering rules

- Read README.md, relevant docs/ files and nearby tests before changing code.
  Work only in the assigned worktree; preserve unrelated edits.
- Use atomic conventional commits and a pull request. Never push directly to main.
  Never force-push, including `--force` or `--force-with-lease` on git or gh.
  Never use `--no-verify` or `--admin` to bypass checks or branch protection.
  Never rebase a pushed branch; update it with `git fetch origin` then
  `git merge origin/main` in the assigned worktree.
- Never merge a PR marked "do not merge", or with failing or pending required
  checks. Merge only when authorized. After updating a branch, require new checks
  on its new head; a previous green SHA is not evidence for the current one.
- Wait on CI with `lacquer wait pr <N>`; never `gh pr checks --watch` or polling.
  Exit 0 is passed; 1 failed, 2 timed out, 3 no checks, 4 wait failed.
  Before a follow-up push run `lacquer ci-round begin <N>` with `--reason` for a
  failure fix or `--review` for requested changes. Stop on exit 10; never reset
  the round budget without human authorization.
- Never commit or log secrets. Commit examples, not real `.env`, `Secrets.xcconfig`,
  signing keys or credentials. Never put server/service-role keys in a client
  binary, public environment variable, bundle, response or log. Declare secret
  names rather than values in the manifest; use the configured secret store.
- Local checks must match CI strictness. Read `.github/workflows/` and the local
  check configuration; run their exact commands for the affected components.
  Treat warnings as errors. Do not suppress failures, hide stderr, add `|| true`,
  weaken checks or edit generated checks to get green. Fix shared configuration
  at its source or explicitly exclude project-owned files in `.lacquer.toml`.
- Add a regression test, prove it fails on the broken behavior, then passes with
  the fix. Build and run relevant tests. Zero selected tests, skipped checks and
  unreadable results are not success. Report commands, results and limitations.
- Keep build output inside the assigned worktree. Never kill a simulator or process
  this run did not create; record owned IDs and clean up only those resources.
- Preserve the PR number, branch, worktree, CI-round budget, verification evidence
  and next action across compaction. Resume from that state.
- In Codex, read `.codex/README.md` for guard activation and coverage limits.
  These rules still apply when runtime guards are inactive or cannot inspect a tool.

# iOS / Swift

- Never hand-edit `.pbxproj`, `.xcworkspace`, `.xib`, `.storyboard` or project/workspace
  contents. Never modify `.entitlements` without explicit permission from the operator.
  Dependency and deployment-target changes also require permission.
- Before bumping a version, find where MARKETING_VERSION is actually read:
  pbxproj, xcconfig or project.yml. For a generated project, update the generator
  spec or referenced xcconfig and run `xcodegen generate`. For a hand-maintained
  pbxproj, the sole exception is `scripts/bump-marketing-version.sh <new-version>`
  from the component root: it changes MARKETING_VERSION lines only. Verify the
  resolved value for every release target/configuration. Structural changes go
  through XcodeGen or the operator; never open Xcode just to bump the version.
- Build output stays in `<worktree>/DerivedData`, never shared across worktrees.
  Create `.metadata_never_index` at the worktree root before building. From the
  repository root, choose the scheme and simulator that CI actually exercises:

  ```sh
  flowdeck build -w Rootapp.xcodeproj -s <scheme> -S <owned-udid> -d "$(git rev-parse --show-toplevel)/DerivedData"
  flowdeck test -w Rootapp.xcodeproj -s <scheme> -S <owned-udid> -d "$(git rev-parse --show-toplevel)/DerivedData"
  pre-commit run --all-files
  ```

  Use FlowDeck for interactive builds/tests and simulator mutations. Raw Xcode
  queries/builds must pass the same absolute path via `-derivedDataPath`.
  Never kill a simulator or process this run did not create. For read-only system
  logs use `scripts/sim-os-log.sh <owned-udid> --last 10m`; disclose its use.
- Check `flowdeck --version` (at least 1.26.5 for meaningful failure exit codes).
  Select complete test targets with one comma-separated `--test-targets` value;
  do not use `--only`/`--test-cases` for parameterized suites. Inspect actual
  counts/results. A test running longer than 300 seconds needs investigation.
- Local checks in `.pre-commit-config.yaml` cover strict formatting/lint,
  documentation, secrets and model defaults. CI's `.github/workflows/ios-ci.yml`
  adds the full build/test matrix; tests are deliberately CI-only at commit time.
  Run affected tests during development anyway. Preserve warnings-as-errors and
  all intended targets, including extra/watch targets. Do not replace CI's raw
  Xcode commands with FlowDeck or treat a skipped matrix row as a pass.
- Test SwiftData containers must use in-memory storage and `cloudKitDatabase: .none`.
  Give non-optional persisted properties inline defaults for CloudKit compatibility.
- Runtime public keys use gitignored `Secrets.xcconfig`; examples never feed a
  release. Run `scripts/write-release-config.sh` using declared secret names and
  formats; server credentials never enter the app. Release only a completed-green
  CI SHA reachable from the default branch. Use the configured archive volume;
  missing storage fails rather than falling back into the checkout. Inspect App
  Store processing state before retrying an upload.
