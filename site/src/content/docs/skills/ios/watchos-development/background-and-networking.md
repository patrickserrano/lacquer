---
title: "Background Tasks and Networking"
description: "Reference for the watchos-development skill."
---

:::note
Generated from [`profiles/ios/skills/watchos-development/references/background-and-networking.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/watchos-development/references/background-and-networking.md) — edit that file, not this page.
:::

# Background Tasks and Networking

## Core principle

**Route everything through `URLSession`.** Low-level networking is blocked on watchOS by policy (TN3135), not by accident: `NWConnection`, Network-framework TCP/UDP, `URLSessionStreamTask`, `URLSessionWebSocketTask`, `NWBrowser`, `NetService`, and raw BSD sockets are all disabled for ordinary apps, enforced since watchOS 9. An `NWConnection` on a blocked app sits in `.waiting(_:)` with `ENETDOWN`; an `NWPathMonitor` stays `.unsatisfied`. The simulator does **not** enforce this — code that "works" there can fail outright on device.

Three exceptions, each with a specific active window:

| App type | Low-level window | Minimum version |
|---|---|---|
| Audio streaming (background audio session) | While actively streaming | watchOS 6 |
| VoIP + CallKit | While running a CallKit call | watchOS 9 |
| tvOS pairing (DeviceDiscoveryUI service listener) | Persistent | watchOS 9 / tvOS 16 |

If the app isn't in one of those three lanes, everything goes through `URLSession` — always test the real behavior on a physical device.

## URLSession choice matrix

| Session | Use when |
|---|---|
| Default (`URLSessionConfiguration.default`) | Foreground and active; normal cookie/cache behavior |
| Ephemeral (`URLSessionConfiguration.ephemeral`) | Foreground, no persistence needed (sensitive or private requests) |
| Background (`URLSessionConfiguration.background(withIdentifier:)`) | The app may go inactive or terminate before the request finishes; guarantees eventual completion |

Background sessions can be delayed or deferred by the system based on resource conditions — for anything foreground and latency-sensitive, default or ephemeral is faster and more predictable.

Avoid Foundation's synchronous byte-loading convenience APIs on network URLs — `Data(contentsOf: url)`, `String(contentsOf: url)`, and similar are explicitly unsupported on watchOS. Use `URLSession.shared.data(from:)` or a configured session instead.

## Background scheduling moves to BGTaskScheduler (OS27)

The watchOS 27 SDK brings the BackgroundTasks framework to watch and deprecates the WatchKit scheduling methods:

| Legacy (deprecated in 27) | Replacement |
|---|---|
| `WKApplication.scheduleBackgroundRefresh(withPreferredDate:userInfo:...)` | `BGAppRefreshTaskRequest(identifier:)` + `earliestBeginDate` + `submitTaskRequest` |
| `userInfo` string dispatch through one handler | One identifier (and one `.backgroundTask` hook) per flow; list each in Info.plist `BGTaskSchedulerPermittedIdentifiers` |
| Bare `.backgroundTask(.appRefresh)` receiving `String?` | `.backgroundTask(.appRefresh("identifier"))` |
| `scheduleSnapshotRefresh` | Nothing — snapshots can no longer be scheduled manually |
| `BGTaskScheduler.submit(_:)` | `submitTaskRequest(_:)` async |

```swift
import BackgroundTasks

let request = BGAppRefreshTaskRequest(identifier: "com.example.weather-refresh")
request.earliestBeginDate = Date(timeIntervalSinceNow: 15 * 60)
try await BGTaskScheduler.shared.submitTaskRequest(request)

.backgroundTask(.appRefresh("com.example.weather-refresh")) {
    await fetchWeather()
}
.backgroundTask(.processingTask("com.example.maintenance")) {  // OS27
    await runMaintenance()
}
```

Also new on 27: `BGProcessingTaskRequest` (longer maintenance work — 1 pending refresh + 10 pending processing tasks allowed), `BGHealthResearchTaskRequest`, and `BGContinuedProcessingTaskRequest` for user-visible continued work (no GPU on watch; not tvOS/visionOS). Delivery mechanics are unchanged — `handle(_:)` and `WKRefreshBackgroundTask` still exist for Watch Connectivity and URLSession wake-ups.

**SDK gotcha:** the headers annotate `BGTaskScheduler` back to watchOS 26, but the 26.5 SDK marked it watch-unavailable. Building against it requires the Xcode 27 SDK even though the deployment target can stay at watchOS 26.

## Background refresh — the pre-27 SwiftUI way

`.backgroundTask(_:action:)` on the scene is preferred over the app-delegate path — the task completes when the closure returns.

```swift
@main
struct MyWatch_Watch_App: App {
    var body: some Scene {
        WindowGroup { ContentView() }
            .backgroundTask(.appRefresh) { context in
                await refreshData()
            }
    }
}
```

### Distinguishing flows by `userInfo` (deprecated in 27)

```swift
// Scheduling
WKApplication.shared().scheduleBackgroundRefresh(
    withPreferredDate: Date(timeIntervalSinceNow: 15 * 60),
    userInfo: "WEATHER_UPDATE" as NSString
) { error in /* handle */ }

// Handling — the bare .appRefresh closure receives the scheduled userInfo
// bridged DIRECTLY to String? — it is not a context object, and not the
// SwiftUI identifier form `.appRefresh("id")` that iOS uses.
.backgroundTask(.appRefresh) { reason in
    switch reason {
    case "WEATHER_UPDATE": await fetchWeather()
    case "WIDGET_RELOAD": await reloadWidgetData()
    default: await performDefaultRefresh()
    }
}
```

### Cancellation on long tasks

```swift
.backgroundTask(.appRefresh) { _ in
    await withTaskCancellationHandler {
        // main work
    } onCancel: {
        // persist partial progress, release resources
    }
}
```

## Background refresh — WatchKit app delegate

For apps still on `WKApplicationDelegate`, `handle(_:)` receives **every** background task — yours, Watch Connectivity, URLSession — and each must reach `setTaskCompletedWithSnapshot(_:)`:

```swift
func handle(_ backgroundTasks: Set<WKRefreshBackgroundTask>) {
    for task in backgroundTasks {
        switch task {
        case let t as WKApplicationRefreshBackgroundTask:
            handleAppRefresh(t)
        case let t as WKSnapshotRefreshBackgroundTask:
            t.setTaskCompleted(restoredDefaultState: false, estimatedSnapshotExpiration: .distantFuture, userInfo: nil)
        case let t as WKURLSessionRefreshBackgroundTask:
            handleURLSessionRefresh(t)  // save; complete after delegate callbacks
        case let t as WKWatchConnectivityRefreshBackgroundTask:
            handleWatchConnectivity(t)  // save; complete via KVO — see watch-connectivity.md
        default:
            task.setTaskCompletedWithSnapshot(false)
        }
    }
}
```

Missing a completion drains the budget and eventually produces `EXC_CRASH (SIGKILL)`. For URLSession and Watch Connectivity tasks, complete **after** the session delegate callbacks fire, not synchronously inside `handle(_:)`.

Use `task.expirationHandler` to clean up (or hand off to a background `URLSession`) before the budget forces a kill.

## Background URLSession — the wake-up flow

1. `handle(_:)` receives a `WKURLSessionRefreshBackgroundTask` — save it, don't complete it yet.
2. The system calls `URLSessionDelegate` methods (`didFinishDownloadingTo:`, then `didCompleteWithError:`).
3. Inside `didCompleteWithError:`, call `setTaskCompletedWithSnapshot(false)` on the saved task.

Move the downloaded file out of its temp location synchronously inside `didFinishDownloadingTo:` — the system deletes the temp file as soon as the delegate method returns.

## Budget reality

The system, not your app, decides when background tasks actually run:

- Apps with a complication on the active watch face get higher background priority.
- Apps in the Dock get higher priority than apps that aren't.
- Workouts, navigation, and other high-priority user activity throttle background work.
- Low battery suspends background tasks even with unused budget remaining.
- Never assume every scheduled task fires — always refresh on foreground as a fallback.

Schedule with a preferred date (`earliestBeginDate` or legacy `withPreferredDate:`) and expect the actual wake time to slip.

## Fresh-data strategy

| Scenario | Best primary path |
|---|---|
| Paired iPhone online | `URLSession` on the watch — the system auto-routes over Bluetooth through the iPhone |
| LTE watch, iPhone asleep or out of range | `URLSession` over known Wi-Fi or cellular |
| CloudKit-backed data | `CKSubscription` + notifications (watchOS 6+) |
| Live on-device refresh | `TimelineView` for date/time redraws, `.backgroundTask` app-refresh for data |
| Instant update from iPhone | Opportunistic `transferUserInfo` + `transferCurrentComplicationUserInfo` |
| Widget refresh from server | APNs widget push (watchOS 26+, see `smart-stack-and-complications.md`) |

Test over all three network routes the watch uses — iPhone proxy via Bluetooth, known Wi-Fi, and cellular (Series 3+). To force Wi-Fi/LTE-only testing, disable both from the iPhone **Settings app**, not Control Center — Control Center only disconnects, it doesn't fully disable.

## Debugging checklist

| Symptom | Probable cause | Check |
|---|---|---|
| `NWConnection` stuck `.waiting(_:)` with `ENETDOWN` on device, fine in simulator | Low-level networking blocked (TN3135) | Switch to `URLSession` — the simulator doesn't enforce the rule |
| `EXC_CRASH (SIGKILL)` shortly after a background wake | A `WKRefreshBackgroundTask` never reached `setTaskCompletedWithSnapshot(_:)` | Audit every branch in `handle(_:)`; complete async work only after delegate callbacks finish |
| Background refresh never fires | No complication on the active face; app not in Dock; low battery; high-priority user activity; identifier missing from `BGTaskSchedulerPermittedIdentifiers` (27) | Add a complication; check Settings → Background App Refresh; verify Info.plist identifiers; test on charger |
| Download completes but data is lost | File wasn't moved out of `didFinishDownloadingTo:` before it returned | Copy to a permanent location synchronously inside the delegate |
| `NWPathMonitor` always `.unsatisfied` | Blocked path monitor outside the three exceptions | Rely on `URLSession` error state instead of pre-checking the path |

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| Starting `NWConnection` from a non-audio, non-CallKit app | Connection stuck `.waiting`; `ENETDOWN` | Switch to `URLSession` — this is policy, not a bug |
| Testing only in the simulator | Works in simulator, fails on device | Always test networking on a physical device |
| Not saving `WKURLSessionRefreshBackgroundTask`/`WKWatchConnectivityRefreshBackgroundTask` before returning from `handle(_:)` | Task completes before the delegate finishes; data loss, mystery crashes | Save the task; complete only after the delegate finishes |
| Using `Data(contentsOf: url)` on a network URL | Unsupported path | Use `URLSession.shared.data(from:)` |
| Mixing SwiftUI `.backgroundTask` with a delegate's `handle(_:)` | Duplicate or dropped tasks | Pick one path — SwiftUI for new work, delegate only if the rest of the app already uses it |
| Expecting every scheduled refresh to fire | Stale UI even after a "scheduled" refresh | Design for missed refreshes; always refresh on foreground |
| No `expirationHandler` on long tasks | `SIGKILL` when the budget expires mid-task | Set `expirationHandler` (delegate path) or wrap in `withTaskCancellationHandler` (SwiftUI path) |
| Using `URLSession.shared` for a background transfer | Download cancels when the app suspends | Use a dedicated session with `URLSessionConfiguration.background(withIdentifier:)` |
| Toggling Bluetooth/Wi-Fi from iPhone Control Center for a network test | Unreliable results — Control Center disconnects but doesn't fully disable | Toggle from the iPhone Settings app |
| Migrating to `BGTaskScheduler` but keeping a single `userInfo`-dispatch handler | Tasks fire but there's no payload to dispatch on | One identifier + one `.backgroundTask(.appRefresh("id"))` per flow, listed in `BGTaskSchedulerPermittedIdentifiers` |
| Calling `BGTaskScheduler` while building with the 26.5 SDK | `'BGTaskScheduler' is unavailable in watchOS` | Build with the Xcode 27 SDK; deployment target can stay watchOS 26 |
| Still calling `scheduleSnapshotRefresh` on 27 | Deprecation warning, no effect | Remove it — snapshots can no longer be manually scheduled |
