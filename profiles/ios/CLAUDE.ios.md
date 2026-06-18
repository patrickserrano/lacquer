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

## SwiftData + CloudKit Rules (when syncing to CloudKit)

If a SwiftData store syncs via CloudKit (`ModelConfiguration(cloudKitDatabase:)` /
`NSPersistentCloudKitContainer`), the schema MUST stay CloudKit-compatible at all
times — CloudKit imposes hard constraints, and violating them fails store setup
or silently drops data:

- **No `@Attribute(.unique)`** — CloudKit has no cross-device uniqueness; enforce uniqueness in code.
- **Every property is optional OR has a default value** — CloudKit records can arrive partially; non-optional, non-defaulted properties are rejected.
- **Relationships must be optional** — a `@Relationship` is allowed but the property must be optional (CloudKit requirement); avoid `.deny` delete rules. Set inverse relationships.
- **Migrations are add-only once CloudKit is enabled** — do NOT delete or rename existing entities/attributes (CloudKit treats a rename as delete-then-add → data loss). Lightweight migration only.

Optional stricter convention (some apps adopt this to sidestep relationship-sync
complexity): keep the schema **flat** and JSON-encode complex children into a
`Data` property (e.g. `songsJSON: Data`) instead of modeling them as relationships.

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

The `no_task_sleep_in_tests` lint rule bans arbitrary `Task.sleep` delays in tests — they cause flaky failures. Wait for the actual state change instead. Add this `TestHelpers.swift` to your test target (the lint rules already exclude `*TestHelpers.swift`):

```swift
import Foundation

/// Thrown by `waitUntil` when the condition never became true within the timeout.
struct WaitTimeoutError: Error, CustomStringConvertible {
    let timeout: Duration
    var description: String { "waitUntil timed out after \(timeout)" }
}

/// Polls `condition` until true, throwing on timeout. Runs on the caller's actor
/// (via `#isolation`) so the closure may read `@MainActor` fixtures safely.
func waitUntil(
    timeout: Duration = .seconds(2),
    pollInterval: Duration = .milliseconds(10),
    isolation _: isolated (any Actor)? = #isolation,
    _ condition: () -> Bool
) async throws {
    let clock = ContinuousClock()
    let deadline = clock.now.advanced(by: timeout)
    while !condition() {
        guard clock.now < deadline else { throw WaitTimeoutError(timeout: timeout) }
        try await Task.sleep(for: pollInterval)
    }
}
```

Usage: `try await waitUntil { viewModel.isLoaded }` instead of `try await Task.sleep(for: .seconds(1))`.

## Battery & Performance Patterns

Apply these whenever touching widgets, animations, networking, or background work.

### Widgets
- Limit `Timeline` entries to **≤ 2** (current + one next-day refresh). More entries run the provider repeatedly and drain battery.
- Use `.atEnd` reload policy — let WidgetKit decide when to refresh.

### Animations
- Always stop animations in `.onDisappear`. Animations left running off-screen still consume CPU/GPU.
- Bind repeating animations to a `@State var isAnimating = false`: set `true` in `.onAppear`, `false` in `.onDisappear`, and pass `value: isAnimating` to `withAnimation`.
- Use `.repeatCount(N)` instead of `.repeatForever` for attention animations.

### Low Power Mode
Guard expensive operations before they start:
```swift
guard !ProcessInfo.processInfo.isLowPowerModeEnabled else { return }
```
Apply to: image preloading, background downloads, video prefetch, heavy sync.

### Network
```swift
let config = URLSessionConfiguration.default
config.allowsConstrainedNetworkAccess = false  // respect Low Data Mode
config.allowsExpensiveNetworkAccess = false    // avoid cellular when Wi-Fi preferred
config.waitsForConnectivity = true             // queue rather than fail when offline
```

### Observer & Task Cleanup
`@Observable` macro-generated storage prevents `nonisolated deinit` from removing `NotificationCenter` observers. Use reference-type boxes instead:

```swift
final class NotificationObserverBox {
    private var tokens: [NSObjectProtocol] = []
    func add(_ token: NSObjectProtocol) { tokens.append(token) }
    deinit { tokens.forEach { NotificationCenter.default.removeObserver($0) } }
}

final class TaskBox {
    private var cancel: (() -> Void)?
    func store<Success, Failure>(_ task: Task<Success, Failure>) { cancel = { task.cancel() } }
    deinit { cancel?() }
}
```

For `MPRemoteCommandCenter`: store `addTarget` return values; call `removeTarget(nil)` on each in `deinit`.

## Swift 6 Concurrency & Default Actor Isolation

If the app target sets `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor` (approachable concurrency), classes without an explicit isolation annotation — **including services** — are implicitly `@MainActor`.

- `await urlSession.data(for:)` still does its network I/O **off** the main thread; the suspension yields. Only the synchronous work around it (e.g. JSON decoding) runs on the main actor — fine at small payload sizes.
- If a method does **heavy synchronous work** (large decode, image processing, crypto), mark that method (or the type) `nonisolated` / `@concurrent` **deliberately** so it runs off-main.
- **NEVER** reach for `@unchecked Sendable` or `nonisolated(unsafe)` to silence a diagnostic. Fix the root cause: make the type a value type, isolate it to an actor, or make stored state immutable.

## iOS 26 API Gotchas

- **Mini-player / bottom accessory:** the shipping API is `.tabViewBottomAccessory { ... }` — **not** `.tabViewAccessory`.
- **Tab-bar morphing search:** declare the search tab with `Tab(role: .search)` and use `.searchable(text:prompt:)` with automatic placement. `SearchFieldPlacement.tabBar` **does not exist** in the iOS 26 SDK.
- **Naming:** name your tab enum `AppTab` (or similar) — a type named `Tab` shadows SwiftUI's `Tab` builder struct and breaks the `TabView` content.

## URL Validation Security Posture

Validate **every** user-provided URL through a positive-allowlist validator before it reaches `AVPlayer`, `URLSession`, or a `WKWebView`. Validate at **both** the manager and service boundaries (the duplication is intentional defense-in-depth). Known limitation: homograph / IDN look-alike hosts are not detected.

The validator parses once via `URLComponents` and asserts: http/https scheme only, non-empty host, no userinfo (credentials), a UTF-8 **byte-length** cap, and rejection of C0 controls / DEL / literal & percent-encoded null bytes. The dangerous-scheme denylist is redundant belt-and-suspenders.

```swift
enum SecureURLValidator {
    /// Returns true only when the URL satisfies every required property.
    /// Known limitation: homograph / IDN look-alike hosts are not detected.
    nonisolated static func validate(_ urlString: String) -> Bool {
        guard !urlString.isEmpty else { return false }
        guard urlString.utf8.count <= 2048 else { return false }
        guard !urlString.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7F }) else { return false }
        guard !urlString.contains("\0"), !urlString.lowercased().contains("%00") else { return false }
        let dangerous = ["javascript:", "data:", "file:", "vbscript:"]
        guard !dangerous.contains(where: { urlString.lowercased().hasPrefix($0) }) else { return false }
        guard let components = URLComponents(string: urlString) else { return false }
        guard let scheme = components.scheme?.lowercased(), ["http", "https"].contains(scheme) else { return false }
        guard components.user == nil, components.password == nil else { return false }
        guard let host = components.host, !host.isEmpty else { return false }
        return true
    }
}
```

## Accessibility & Design-Token Contrast (WCAG 1.4.11)

Audit **non-text** contrast, not just text. Ship two distinct boundary tokens and use them for their intended roles:

- `controlBorder` — ~white @ 30% opacity, **≥ 3:1** against its background — for the boundary of an **interactive** control (button outline, text-field border, selected chip).
- a decorative hairline — ~white @ 8% — for dividers and separators that carry no meaning.

Other rules:
- Use a **saturated** `controlAccent` for controls that sit against a **white system thumb** (e.g. `Toggle`). A near-white accent fails ~3:1 against the white thumb and reads as "off" to low-vision users.
- Selection states must be **non-color-redundant**: show a checkmark / icon, not just a colored ring or tint, so the state survives color-blindness and grayscale.

## Premium / Subscription Gating (if monetized)

When gating features behind a subscription, keep the seam clean and testable:

- Inject a `SubscriptionService` with an `isPremium` property — never read a singleton inline.
- One **shared paywall presenter** and one **reusable lock badge** — don't reimplement per feature.
- Gate logic lives in **pure, fail-closed** functions: `func canUseX(isPremium: Bool, ...) -> Bool` that default to denying access on any ambiguity.
- Keep the apply/perform logic in a **view-free, testable controller** with an **injectable apply-seam**, so gating decisions are unit-tested without SwiftUI.
