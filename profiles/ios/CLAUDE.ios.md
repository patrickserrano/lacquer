# iOS / Swift profile rules

Synced into the `CLAUDE.md` of any component declaring the `ios` profile. These
rules assume an Xcode project under `{{COMPONENT_PREFIX}}`. Where a value is project-specific
(your app name, scheme, bundle id), substitute your own — the project's own
identity lives in its root `CLAUDE.md`, not here. Replace `<YourApp>` /
`<YourScheme>` below with your target and scheme names.

## Xcode-Specific Prohibitions

- **NEVER modify `.pbxproj` or `.xcodeproj/` contents.** Create `.swift`/resource files only. **If the project uses Xcode 16 file-system synchronized groups** (check for `PBXFileSystemSynchronizedRootGroup` in the `.pbxproj`), files placed under a synced folder are **auto-included in the target — do NOT add target membership manually and do NOT edit the `.pbxproj`**. If the project does NOT use synchronized groups, ask the user to add new files to the target in Xcode rather than editing the project file.
- **NEVER modify `.xcworkspace` contents.**
- **NEVER add Swift Package Manager dependencies** without explicit user permission. To *bump* existing deps (no new deps added), use `flowdeck project packages update` — it re-resolves `Package.resolved` to the latest versions allowed by the existing `upToNextMajorVersion` constraints **without touching the `.pbxproj`**; build + test afterward.
- **NEVER change the deployment target** without explicit user request.
- **NEVER modify `.entitlements` files** without explicit user request.
- **NEVER use `NavigationView`** — always `NavigationStack`.
- **NEVER use `ObservableObject`** — always `@Observable`.
- **NEVER use `@StateObject`** — always `@State` with `@Observable` objects.
- **NEVER use `@Published`** — `@Observable` properties publish automatically.

## App Store Requirements

- **`ITSAppUsesNonExemptEncryption` must be set** in `Info.plist` (or as `INFOPLIST_KEY_ITSAppUsesNonExemptEncryption` build setting). Value is `NO` for apps using only standard HTTPS; `YES` for apps with custom encryption. Missing or wrong value causes export compliance failures on every TestFlight upload.

- **Terms of Use (EULA).** Configure a custom EULA in App Store Connect → **License Agreement** (if you set none, Apple's standard EULA applies automatically). *Separately*, App Review requires a **functional Terms of Use link in the App Store description** — link your custom EULA, or Apple's standard EULA: `https://www.apple.com/legal/internet-services/itunes/dev/stdeula/`. A missing/broken EULA link is a common rejection.

- **Privacy Policy link.** Required for every app: set the **Privacy Policy URL** in App Store Connect → App Information, AND make the policy reachable **inside the app**. Include the link in the App Store description too. A non-functional or missing privacy policy link is a frequent rejection.

- **Subscription / IAP apps (Guideline 3.1.2):** the **paywall/purchase screen itself** must clearly show price, duration, auto-renewal terms, and how to cancel — not just the description — and both the **privacy policy** and **terms of use (EULA)** links must be **clickable on that screen** (in the binary, not only in metadata). See [Premium / Subscription Gating](#premium--subscription-gating-if-monetized).

## Secrets & Service Keys

Two separate buckets — never mix them.

### App-runtime keys → `Secrets.xcconfig` (compiled into the app)

Service keys the app needs at runtime (RevenueCat, Aptabase, …) live in a
gitignored `Secrets.xcconfig`, never in source or the committed `project.yml`.
The lacquer syncs a `Secrets.xcconfig.example` template into the component dir.
See the `ios-secrets-setup` skill for wiring a new key through `project.yml`
into `Info.plist` and reading it at runtime. `Secrets.xcconfig` values are
**build-time** — they are baked into the binary, so treat them as obfuscated,
not secret. A truly sensitive secret belongs on a server, never in the app.

> **RevenueCat ships two different keys — do not confuse them.** The
> `REVENUECAT_API_KEY` above is the **public SDK key** (`appl_…`), safe to compile
> into the app. RevenueCat's **REST API** uses a separate **secret key** (`sk_…`)
> that grants full account access — it must **never** go in `Secrets.xcconfig` or
> the binary. It is a CI/server secret (`REVENUECAT_REST_API_KEY`, below).

### CI / server secrets → GitHub Actions (never in the app)

The release and quality workflows — and any server-side job that calls a vendor
REST API — read these from repo/org **GitHub Actions secrets**, never from an
xcconfig.

**Organization secrets only exist for organizations.** `gh secret set --org` 404s
against a personal account, so check which `{{GITHUB_ORG}}` is before choosing:

```bash
gh api /orgs/{{GITHUB_ORG}} >/dev/null 2>&1 \
  && gh secret set <NAME> --org {{GITHUB_ORG}} \
  || gh secret set <NAME> -R {{GITHUB_ORG}}/<repo>   # personal account: per repo
```

This is not a nitpick. Every repo in this fleet was missing
`CLAUDE_CODE_OAUTH_TOKEN` for months because the instruction here was
unconditionally org-level and the account owning them is personal — so the
command silently could not have worked, and four workflows failed on every run.
When a secret is per-repo, fan it out deliberately rather than one at a time:

```bash
for r in $(gh repo list <owner> --limit 200 --json name --jq '.[].name'); do
  gh secret set <NAME> -R <owner>/"$r" < secret.txt
done
```

| Secret | Used by | Source |
|--------|---------|--------|
| `ASC_KEY_ID` | release | App Store Connect → Users and Access → Integrations → API key |
| `ASC_ISSUER_ID` | release | same page (issuer ID) |
| `ASC_KEY_CONTENT` | release | the `.p8` private key contents |
| `APPLE_TEAM_ID` | release | Apple Developer membership |
| `KEYCHAIN_PASSWORD` | release (signing) | the dedicated runner's **login**-keychain password — set this as an **org-level** secret so every repo's release can unlock the system keychain (release never creates its own, and its final `always()` step re-locks it so the keychain never stays unlocked past the job) |
| `CLAUDE_CODE_OAUTH_TOKEN` | claude, quality-review, dependency-audit, issue-deduplication | `claude setup-token` |
| `SENTRY_AUTH_TOKEN` | release (dSYM upload) | Sentry → Settings → Auth Tokens, scoped to `project:releases` |
| `SENTRY_ORG` | release (dSYM upload) | the Sentry org slug (e.g. `pixel-fox-studio`) |
| `SENTRY_PROJECT` | release (dSYM upload) | the Sentry project slug (e.g. `rail`) — differs per repo, so this one is never org-level |
| `REVENUECAT_REST_API_KEY` | server/REST API calls | RevenueCat → API keys → **secret** key (`sk_…`) — full account access |
| `APP_STORE_CONNECT_FEEDBACK_KEY_IDENTIFIER` | testflight-feedback | a **separate, least-privilege** ASC API key id (read-only) |
| `APP_STORE_CONNECT_FEEDBACK_ISSUER_ID` | testflight-feedback | issuer id for that key |
| `APP_STORE_CONNECT_FEEDBACK_PRIVATE_KEY` | testflight-feedback | that key's `.p8` contents |

The TestFlight-feedback job uses its **own** App Store Connect key, distinct from
the release/signing key (`ASC_*`) — it only needs read access to beta feedback,
and it runs on a GitHub-hosted runner, so it must never carry the signing key.

`GITHUB_TOKEN` is provided automatically by Actions — do not set it.

**The Sentry dSYM upload is opt-in and fails open.** All three `SENTRY_*` secrets
must be present or the step skips — a project with no Sentry gets a clean release,
not a red one. The presence check is a **job-level** `env` var (`HAS_SENTRY_TOKEN`)
rather than one declared in the step's own `env:` block: `secrets` is not usable in
a step-level `if`, and a var set in that same step's `env:` is not in scope for its
`if` either, so the obvious-looking version of this gate skips silently on every
release and looks configured while uploading nothing.

### Claude-powered workflows

Four synced workflows call `anthropics/claude-code-action` and therefore need
`CLAUDE_CODE_OAUTH_TOKEN`: `ios-claude.yml` (responds to an `@claude` mention),
`ios-quality-review.yml` (weekly), `ios-dependency-audit.yml` (twice monthly),
and `ios-issue-deduplication.yml` (on every opened issue).

The action **hard-fails** when the token is empty — `Environment variable
validation failed` — before doing any work. Each workflow now checks for the
token first and behaves according to who is waiting on it:

- **Unattended** (quality-review, dependency-audit, issue-deduplication): skip
  the Claude step with a `::warning::` and a job-summary remedy. The job stays
  green, because a permanently red scheduled run is noise that trains you to
  stop reading the Actions tab — which is exactly how this went unnoticed.
  Dependency-audit still collects and publishes its report, so the run is not
  worthless without the token.
- **Interactive** (claude.yml): fail, *and post a comment on the thread* saying
  why. Someone typed `@claude` and is waiting; a silent skip is the worst
  outcome, and a red X on a workflow they will not open is barely better.

The `secrets` context is unavailable in a job-level `if`, so the check is always
a step that exports an output the later steps gate on.

## CI Runners

Every synced workflow already sets the correct runner per job — when editing
an existing job, keep whatever `runs-on` it already has; don't re-derive it.
The rule below matters only when authoring a **brand-new** job:

Xcode-touching work (build/test/lint/archive/sign/release) uses
`runs-on: [self-hosted, macOS, ARM64, dedicated]` — never a GitHub-hosted
macOS runner (`macos-latest`) or a stray self-hosted label like `mac-mini`. A
pure script/REST-call job with no Xcode dependency (merge gates, a
TestFlight-feedback fetch, a deploy) uses `ubuntu-latest` instead — don't tie
up the Mac for work that doesn't need it.

See the `macos-ci-recipes` skill for the reasoning and copy-in recipes when
the new job is a macOS-only or hybrid iOS+macOS workflow.

## Build & Test Tooling (flowdeck)

**Use `flowdeck` for ALL Apple-platform work** — build, run, test, simulator, device, logs, UI automation. Do NOT use `xcodebuild`, `xcrun`, `simctl`, or `devicectl` directly (raw `simctl`/`devicectl` are typically hook-blocked).

```bash
flowdeck simulator list           # find an available simulator UDID (names are ambiguous across OS versions)
flowdeck build -w {{XCODEPROJ}} -s <YourScheme> -S <udid> -d {{COMPONENT_PREFIX}}DerivedData
flowdeck test  -w {{XCODEPROJ}} -s <YourScheme> -S <udid> -d {{COMPONENT_PREFIX}}DerivedData
flowdeck project packages update  # bump SPM deps within constraints (no .pbxproj edit)
```

**Prefer a UDID over a simulator name** — names duplicate across OS versions and resolve ambiguously.

### Working in worktrees

- Pass a **unique derived-data path per worktree** (`-d {{COMPONENT_PREFIX}}DerivedData-<feature>`) so parallel worktrees don't collide on one DerivedData dir (collisions surface as SIGKILL test crashes).
- **Delete that derived-data dir before running format/lint** — otherwise it lints compiled dependency sources and reports phantom `file_length`/format violations. (The `.swiftformat`/`.swiftlint.yml` excludes cover `DerivedData*`; keep your path matching that glob.)
- **Ignore SourceKit diagnostics in a fresh worktree** (`No such module 'X'`, `Cannot find type`) — the worktree has no built index, so they're false positives. The authoritative signals are `flowdeck build` / `flowdeck test`.

## Editor hooks (.claude/settings.json)

The synced `.claude/settings.json` installs hooks that: block edits to
`.pbxproj`/`.xcworkspace`/`.xib`/`.storyboard`/`.entitlements` (PreToolUse),
run SwiftFormat + SwiftLint on every `.swift` write (PostToolUse), and — on
SessionStart — **auto-approve the Xcode MCP permission dialog** via
`allow_mcp.js` (requires macOS Accessibility permission for your terminal).
That auto-approve is a deliberate convenience; remove the SessionStart hook if
you'd rather approve the Xcode MCP dialog manually.

## Test Timeout Rule

Tests must NEVER run longer than **5 minutes (300 seconds)**. If tests exceed 5 minutes, they are hung. Kill the process immediately and investigate. When invoking builds/tests via a Bash tool, set a 300000 ms timeout.

## Architecture

```
View (SwiftUI) → ViewModel (@Observable, @MainActor) → Service → Repository → DataSource
```

**Key patterns:**
- All ViewModels: `@Observable` + `@MainActor`.
- All service/repository protocols: `Sendable`.
- Stateless services: `final class`; stateful services: `actor`.
- Async operations: `async/await` and `AsyncStream`.
- Constructor injection for dependencies.

**Project structure:**
```
{{COMPONENT_PREFIX}}<YourApp>/
├── App/           # App entry point, dependency container
├── Features/      # Feature modules (one folder per feature)
├── Core/          # Services, Repositories, Models, Networking
├── Shared/        # Components, Extensions, Utilities
└── Resources/
```

**Layer rule:** ViewModels MUST NOT depend directly on Repository protocols. Inject Service protocols instead.

## SwiftData + CloudKit

If this project imports `SwiftData`, `lacquer sync` suggests the
`dpearson2699/swift-ios-skills@swiftdata` skill (see `internal/skillsuggest`) —
it covers CloudKit-compatible schema constraints. Install it with `skills add`
if it wasn't suggested.

## Testing

**Swift Testing is the standard for all new test files.** Use `@Test`, `@Suite`, and `#expect`. XCTest is legacy — only modify existing XCTest files when touched for other reasons. Never create new XCTest files.

```swift
import Testing
@testable import <YourApp>

@Suite("Feature Tests", .serialized)
@MainActor
struct FeatureTests {
    private var mockService = MockService()
    private var sut: FeatureViewModel {
        FeatureViewModel(service: mockService)
    }

    @Test func testBehavior() async {
        // arrange, act, assert
    }
}
```

### Targeted tests during development

During RED/GREEN, run **targeted** tests only (`-only-testing:<YourApp>Tests/SomeSuite/someTest`) — never a full self-run. The **full** suite runs at pre-commit and again in CI (fresh checkout). Treat **SwiftLint warnings as errors** — fix the code, never suppress (see core Fundamental Rule #7).

### Test support: `waitUntil` (no `Task.sleep` in tests)

The `no_task_sleep_in_tests` lint rule bans arbitrary `Task.sleep` delays in
tests — they cause flaky failures. See the `swift-testing-wait-until` skill
for the polling helper to add to your test target instead.

## Local Checks vs CI

Every CI gate and where it runs before push. See core "Local Checks Match CI" —
a new CI job adds a row here, and a hook never runs weaker than its CI twin.

| CI job / step | Local |
|---|---|
| `Lint` → SwiftLint `--strict` | pre-commit `swiftlint` (**`--strict`**, staged files) |
| `Lint` → SwiftFormat `--lint` | pre-commit `swiftformat` (writes; a changed file fails the commit) |
| `Docs` → `missing_docs` | pre-commit `swiftlint-docs` (**`--strict`**, staged files) |
| `Docs` → DocC builds clean | **pre-push** `docs-build` (too slow for pre-commit) |
| `Test` | pre-commit `swift-test` |
| `Baseline` | `lacquer audit` (exit 4) — CI-only, it reads the pbxproj |
| `Build (Release)` | CI-only: a full Release archive is not a commit-time cost |
| `No lacquer drift` | `lacquer audit` (exit 3) — run it locally any time |

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

## Documentation (DocC)

**DocC is a requirement, not a nicety** — see core "Documentation" for the rule
and the relaxation mechanism. This section is the Swift half.

Every declaration above `private` carries a `///` doc comment, and the DocC
archive builds with zero warnings. Both are checked by the `Docs` job in
`ios-ci.yml` and published to `https://<owner>.github.io/<repo>/swift/` by
`ios-docs.yml` on merge.

```swift
/// Loads the user's saved sessions, newest first.
///
/// - Parameter limit: The maximum number of sessions to return. Pass `nil` for
///   all of them.
/// - Returns: The sessions, or an empty array when the user has none.
/// - Throws: ``SessionError/unauthorized`` when the session token has expired.
///
/// Runs off the main actor — the decode is large enough to drop frames.
nonisolated func loadSessions(limit: Int?) async throws -> [Session]
```

Use `- Parameter` / `- Returns` / `- Throws` for anything a caller must know, and
double-backtick symbol links (``` ``SessionError/unauthorized`` ```) rather than
plain-text type names — a link is checked by the build, prose is not. That is the
whole reason the build gate exists.

### Run the checks locally

```sh
swiftlint --strict --config .swiftlint-docs.yml .   # every declaration documented
scripts/build-docs.sh docs-site                     # docs build clean
```

`.swiftlint-docs.yml` is a **separate** config from `.swiftlint.yml` on purpose:
the documentation baseline is the one rule set a project may relax, and mixing it
into the main config would either hand every style rule an escape hatch or leave
this one without the escape hatch the standard promises.

### Three settings that decide whether this checks anything

`scripts/build-docs.sh` carries them; do not "simplify" them out.

- **`DOCC_MINIMUM_ACCESS_LEVEL=internal`.** DocC extracts `public` and above by
  default, and an app target's code is `internal` by default — so without this
  an app builds an archive containing essentially nothing, succeeds, and reports
  as a pass. Verified on a real app target: 3 symbols at the default, 6 with
  `internal`.
- **`OTHER_DOCC_FLAGS=--warnings-as-errors`.** `DOCC_FLAGS` is *also* a real
  build setting name — it is in Xcode's own xcspecs — and it is **silently
  ignored**. A docbuild with `DOCC_FLAGS=--warnings-as-errors` and a deliberately
  broken symbol link still exits 0; the same input with `OTHER_DOCC_FLAGS` exits
  65. Picking the obvious-looking name turns the gate off without turning the job
  red.
- **`DOCC_TRANSFORM_FOR_STATIC_HOSTING=YES`** plus `DOCC_HOSTING_BASE_PATH`,
  which is baked into every asset URL. It must match where the site is served
  from (`<repo>/swift`) or the published site loads and renders unstyled, every
  CSS and JS request 404ing.

### Articles and catalogs

A `.docc` catalog next to the sources adds landing pages, articles, and tutorials
beyond the symbol reference — a `<Target>.md` root page is the highest-value
addition, because it is what a reader lands on. Symbol links from an article are
checked by the same build, so a catalog raises the value of the gate rather than
working around it.

## Battery & Performance Patterns

Apply these whenever touching widgets, animations, networking, or background
work — see the `ios-performance-battery-patterns` skill for the concrete
patterns (Timeline entry limits, animation cleanup, Low Power Mode guards,
constrained-network config, observer/task cleanup under `@Observable`).

## Swift 6 Concurrency & Default Actor Isolation

**Swift 6 language mode is the baseline, in every build configuration — not just the app target.** `SWIFT_VERSION = 6` and `SWIFT_TREAT_WARNINGS_AS_ERRORS = YES` are asserted by the lacquer and checked two ways: `lacquer audit` reads the pbxproj statically across every configuration, and the CI `Baseline` job reads the effective settings via `xcodebuild -showBuildSettings`. Below Swift 6, data-race diagnostics are warnings rather than errors, so violations accrue invisibly until the migration has to happen as one large risky change. A target left behind (tests, widget, watch app) reports as a coverage ratio like `4/12`, not as a pass. Genuine exceptions go in `[baseline.relax]` with a reason and an expiry.

If the app target sets `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor` (approachable concurrency), classes without an explicit isolation annotation — **including services** — are implicitly `@MainActor`.

- `await urlSession.data(for:)` still does its network I/O **off** the main thread; the suspension yields. Only the synchronous work around it (e.g. JSON decoding) runs on the main actor — fine at small payload sizes.
- If a method does **heavy synchronous work** (large decode, image processing, crypto), mark that method (or the type) `nonisolated` / `@concurrent` **deliberately** so it runs off-main.
- **NEVER** reach for `@unchecked Sendable` or `nonisolated(unsafe)` to silence a diagnostic. Fix the root cause: make the type a value type, isolate it to an actor, or make stored state immutable.

## iOS 26 API Gotchas

- **Mini-player / bottom accessory:** the shipping API is `.tabViewBottomAccessory { ... }` — **not** `.tabViewAccessory`.
- **Tab-bar morphing search:** declare the search tab with `Tab(role: .search)` and use `.searchable(text:prompt:)` with automatic placement. `SearchFieldPlacement.tabBar` **does not exist** in the iOS 26 SDK.
- **Naming:** name your tab enum `AppTab` (or similar) — a type named `Tab` shadows SwiftUI's `Tab` builder struct and breaks the `TabView` content.

## URL Validation Security Posture

Validate every user-provided URL before it reaches `AVPlayer`, `URLSession`,
or a `WKWebView` — see the `url-validation-security` skill for the
positive-allowlist validator and where to apply it.

## Accessibility & Design-Token Contrast (WCAG 1.4.11)

Audit **non-text** contrast, not just text. Ship two distinct boundary tokens and use them for their intended roles:

- `controlBorder` — ~white @ 30% opacity, **≥ 3:1** against its background — for the boundary of an **interactive** control (button outline, text-field border, selected chip).
- a decorative hairline — ~white @ 8% — for dividers and separators that carry no meaning.

Other rules:
- Use a **saturated** `controlAccent` for controls that sit against a **white system thumb** (e.g. `Toggle`). A near-white accent fails ~3:1 against the white thumb and reads as "off" to low-vision users.
- Selection states must be **non-color-redundant**: show a checkmark / icon, not just a colored ring or tint, so the state survives color-blindness and grayscale.

## Premium / Subscription Gating (if monetized)

If this project imports `StoreKit`, `lacquer sync` suggests the
`dpearson2699/swift-ios-skills@storekit` skill (see `internal/skillsuggest`) —
it covers paywall/entitlement architecture. Install it with `skills add` if it
wasn't suggested.
