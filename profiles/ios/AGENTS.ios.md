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
  flowdeck build -w {{XCODEPROJ}} -s <scheme> -S <owned-udid> -d "$(git rev-parse --show-toplevel)/DerivedData"
  flowdeck test -w {{XCODEPROJ}} -s <scheme> -S <owned-udid> -d "$(git rev-parse --show-toplevel)/DerivedData"
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
