# Platform Basics

Project structure, entry point, and the Info.plist keys that decide how a watchOS app is distributed and run.

## Submission requirements (watchOS 26, effective April 2026)

Two hard gates apply to every Watch target:

| Rule | Effective | Detail |
|---|---|---|
| 64-bit / ARM64 required | April 2026 | Use Xcode's `Standard Architectures` build setting — don't lock a legacy architecture list. Apple Watch Series 9+ and Ultra 2 already run arm64 on watchOS 26. |
| Built with the watchOS 26 SDK or later | April 28, 2026 | Same rule applies across the iOS/iPadOS/tvOS/visionOS 26 SDK family. |

Before submitting: confirm `Standard Architectures` is set on every Watch target individually (not just at the project level), audit `Float`/`Int`/pointer math for arm64 behavior differences, and test on a physical device — the simulator always runs arm64 on Apple Silicon and can mask armv7k-only defects in older code.

## Project structure — three valid shapes

Xcode ships one template with three configurations, distinguished by `WKRunsIndependentlyOfCompanionApp` and whether an iOS target exists:

| Shape | Companion iOS app | `WKRunsIndependentlyOfCompanionApp` | When to use |
|---|---|---|---|
| Watch-only | None | N/A | Wrist-first apps with no iPhone surface |
| Paired companion | Yes | `false` | Existing iPhone app where the watch is a remote UI |
| Independent + companion | Yes | `true` | Paired-or-alone — the default for new work |

**Independent + companion is the default recommendation.** It works on Family Setup watches (no paired iPhone at all) and matches how people add iPhone-side controls to a watch that has no Watch app installed. An independent app must handle, on its own:

- Account creation and sign-in
- System permission prompts
- Data fetches over the network (no Watch Connectivity fallback)
- Push notification registration, including complication pushes

Enable it on an existing target: Watch App target → General → Deployment Info → "Supports Running Without iOS App Installation".

## Canonical entry point

SwiftUI `App`, no storyboard, no WatchKit Extension target:

```swift
import SwiftUI

@main
struct MyWatch_Watch_App: App {
    var body: some Scene {
        WindowGroup {
            NavigationStack {
                ContentView()
            }
        }
    }
}
```

`NavigationView` has been deprecated since watchOS 9 — use `NavigationStack`. `List` on watchOS supports platter styling, swipe actions, and row reordering that `WKInterfaceTable` never did, which is the main reason to prefer SwiftUI over WatchKit interface builder for new screens. If an existing app is still on WatchKit storyboards, see `modernization.md`.

## Notification long-looks

A custom long-look for a notification category needs a `WKNotificationScene` alongside the main `WindowGroup`:

```swift
var body: some Scene {
    WindowGroup {
        NavigationStack { ContentView() }
    }
    WKNotificationScene(controller: NotificationController.self, category: "myCategory")
}
```

```swift
import SwiftUI
import UserNotifications

class NotificationController: WKUserNotificationHostingController<NotificationLongLook> {
    var content: UNNotificationContent!
    var date: Date!

    override var body: NotificationLongLook {
        NotificationLongLook(content: content, date: date)
    }

    override class var isInteractive: Bool { true }

    override func didReceive(_ notification: UNNotification) {
        content = notification.request.content
        date = notification.date
    }
}
```

`isInteractive` controls whether the system shows action buttons; `didReceive(_:)` is the only hand-off point from `UNNotification` to SwiftUI state.

## What SwiftUI already covers

Most lifecycle needs no longer require a delegate:

| Need | SwiftUI hook |
|---|---|
| Foreground/background/inactive transitions | `@Environment(\.scenePhase)` + `.onChange(of:)` |
| Handoff / `NSUserActivity` | `.onContinueUserActivity(_:perform:)` |
| Background refresh, snapshot, URLSession background delivery | `.backgroundTask(_:action:)` (see `background-and-networking.md`) |

## When you still need an app delegate

Adopt `WKApplicationDelegate` via `@WKApplicationDelegateAdaptor`:

```swift
import SwiftUI
import WatchKit

@main
struct MyWatch_Watch_App: App {
    @WKApplicationDelegateAdaptor var appDelegate: MyAppDelegate

    var body: some Scene {
        WindowGroup {
            NavigationStack { ContentView() }
        }
        WKNotificationScene(controller: NotificationController.self, category: "myCategory")
    }
}
```

The delegate is the *only* path for: `userInfo` dictionaries from handoff/complications, remote Now Playing activity, workout configuration and crash-resilient recovery, extended runtime sessions, and APNs device-token registration. If none of those apply, don't add the delegate — an empty one adopted "just in case" is dead weight and one more thing to break on arm64.

## Info.plist keys that matter

| Key | Purpose |
|---|---|
| `WKWatchKitApp` (Bool) | Marks this bundle as a watchOS app |
| `WKAppBundleIdentifier` | Bundle ID of the watchOS app |
| `WKCompanionAppBundleIdentifier` | Paired iOS app's bundle ID (companion configs only) |
| `WKExtensionDelegateClassName` | Legacy WatchKit Extension delegate class — SwiftUI apps usually don't need it |
| `WKRunsIndependentlyOfCompanionApp` (Bool) | `true` for independent or independent+companion apps |
| `WKWatchOnly` (Bool) | `true` for watch-only apps with no iOS target |

`WKRunsIndependentlyOfCompanionApp = YES` + `WKWatchOnly = NO` is the independent+companion shape; `WKWatchOnly = YES` is watch-only.

## Foundation Models on watchOS (OS27)

Foundation Models reaches watchOS in the 27 SDK, but **Private Cloud Compute only** — there is no on-device text model. `SystemLanguageModel` is explicitly unavailable on watchOS; use `PrivateCloudComputeLanguageModel`, which makes every watch FM feature network-dependent.

```swift
import FoundationModels

let model = PrivateCloudComputeLanguageModel()

switch model.availability {
case .available:
    let session = LanguageModelSession(model: model)
    // prompt as usual — see apple-on-device-ai for session patterns
case .unavailable(let reason):
    showNonAIFallback(reason)   // .deviceNotEligible or .systemNotReady
}

let usage = model.quotaUsage           // PCC requests are budgeted per app
let tokens = try? await model.contextSize
```

| Fact | Detail |
|---|---|
| PCC only | `SystemLanguageModel` is unavailable on watchOS |
| Entitlement | Requires the Private Cloud Compute entitlement; without it the model reports unavailable |
| Quota | `quotaUsage.status`, plus optional `limitIncreaseSuggestion`/`resetDate` |
| Vision tools | `BarcodeReaderTool` ships on watchOS 27; `OCRTool` does not |
| Beta caveats | PCC may not work in the simulator (test on device); `@Generable` on enums fails to compile for watchOS; decoding is greedy-only |

Full Foundation Models depth (sessions, `@Generable`, tools) lives in the `apple-on-device-ai` skill — this section only covers watch-specific scoping.

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| `Standard Architectures` set at the project level but not on the Watch target | Rejected at submission; crashes on device from a wrong-architecture link | Verify the build setting on every Watch target individually |
| Treating the simulator as sufficient arm64 testing | Device-only crashes that never reproduce locally | Test on a physical Apple Watch Series 9+/Ultra 2 running watchOS 26 |
| Building "Watch App with Companion iOS App" without checking "Supports Running Without iOS App Installation" | App fails Family Setup; won't install without the iPhone app present | Check the box — independent+companion is the safe default |
| Independent app still treats `WCSession.transferFile` as the primary data path | No data on Family Setup or unpaired watches | Fetch primary data via `URLSession` + auth token directly from the watch; see `watch-connectivity.md` |
| Empty `WKApplicationDelegate` adopted "just in case" | Obscures whether any delegate callback is actually needed; adds a build dependency for nothing | Remove it until a specific callback (workout recovery, remote notifications, Now Playing) requires it |
| Custom toolbar/control styles never audited against Liquid Glass | Low-contrast or broken appearance on watchOS 26 | Run on watchOS 26 and verify every custom style, or adopt system defaults |
| Assuming a control needs a Watch app target | Wasted work building a Watch target for an action that already works | iPhone-side controls surface on Apple Watch automatically — see `controls-and-live-activities.md` |
