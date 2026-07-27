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
xcconfig. Set each at the project's org:

```bash
gh secret set <NAME> --org {{GITHUB_ORG}}
```

| Secret | Used by | Source |
|--------|---------|--------|
| `ASC_KEY_ID` | release | App Store Connect → Users and Access → Integrations → API key |
| `ASC_ISSUER_ID` | release | same page (issuer ID) |
| `ASC_KEY_CONTENT` | release | the `.p8` private key contents |
| `APPLE_TEAM_ID` | release | Apple Developer membership |
| `KEYCHAIN_PASSWORD` | release (signing) | the dedicated runner's **login**-keychain password — set this as an **org-level** secret so every repo's release can unlock the system keychain (release never creates its own, and its final `always()` step re-locks it so the keychain never stays unlocked past the job) |
| `CLAUDE_CODE_OAUTH_TOKEN` | quality-review | `claude setup-token` |
| `REVENUECAT_REST_API_KEY` | server/REST API calls | RevenueCat → API keys → **secret** key (`sk_…`) — full account access |
| `APP_STORE_CONNECT_FEEDBACK_KEY_IDENTIFIER` | testflight-feedback | a **separate, least-privilege** ASC API key id (read-only) |
| `APP_STORE_CONNECT_FEEDBACK_ISSUER_ID` | testflight-feedback | issuer id for that key |
| `APP_STORE_CONNECT_FEEDBACK_PRIVATE_KEY` | testflight-feedback | that key's `.p8` contents |

The TestFlight-feedback job uses its **own** App Store Connect key, distinct from
the release/signing key (`ASC_*`) — it only needs read access to beta feedback,
and it runs on a GitHub-hosted runner, so it must never carry the signing key.

`GITHUB_TOKEN` is provided automatically by Actions — do not set it.

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
