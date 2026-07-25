---
title: "Watch Connectivity"
description: "Reference for the watchos-development skill."
---

:::note
Generated from [`profiles/ios/skills/watchos-development/references/watch-connectivity.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/watchos-development/references/watch-connectivity.md) — edit that file, not this page.
:::

# Watch Connectivity

## Core principle

**Watch Connectivity is an opportunistic optimization, never the primary data path.** Independent watchOS apps can be installed without their iOS companion at all (Family Setup, or a user who simply never installed the phone app), so any feature that only works via `WCSession` breaks for those users. Design the app to fetch from the network or CloudKit first; layer Watch Connectivity on top for when a paired, reachable iPhone happens to be available.

## Session activation

One `WCSession` singleton per process, activated once at launch on both sides:

```swift
import WatchConnectivity

final class ConnectivityProvider: NSObject, WCSessionDelegate {
    static let shared = ConnectivityProvider()

    override init() {
        super.init()
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    func session(_ session: WCSession,
                 activationDidCompleteWith activationState: WCSessionActivationState,
                 error: Error?) { /* handle */ }

    #if os(iOS)
    func sessionDidBecomeInactive(_ session: WCSession) { }
    func sessionDidDeactivate(_ session: WCSession) {
        WCSession.default.activate()  // reactivate for the next paired watch
    }
    #endif
}
```

Assign the delegate **before** calling `activate()` — otherwise the activation callback can arrive before anything is listening. On iOS, implementing both `sessionDidBecomeInactive(_:)` and `sessionDidDeactivate(_:)` is required to support more than one paired watch.

## Choosing a transfer method

| Method | Queued | Overwrites prior | Wakes receiver | Best for |
|---|---|---|---|---|
| `updateApplicationContext(_:)` | No | Yes — replaces old context | Next launch | Snapshots where only the latest value matters (current song, settings, last sync time) |
| `transferUserInfo(_:)` | Yes | No — FIFO | Next launch (background) | Events that all matter in order (messages, appointments, score updates) |
| `transferCurrentComplicationUserInfo(_:)` | Yes | No — FIFO | Immediately (50/day limit) | Complication refresh triggers — the only method that wakes the watch specifically for complications |
| `transferFile(_:metadata:)` | Yes | No — FIFO | On receipt (background) | File payloads (images, audio, large JSON) |
| `sendMessage(_:replyHandler:errorHandler:)` | No | — | Only if both apps are reachable/active | Live request-response while both apps are running |

### `updateApplicationContext` — latest-wins state

```swift
try WCSession.default.updateApplicationContext([
    "lastSync": Date().timeIntervalSince1970,
    "trackTitle": currentTrack.title,
])
```

Received via `session(_:didReceiveApplicationContext:)`. If three updates queue while the receiver sleeps, only the newest survives.

### `transferUserInfo` — ordered queue

```swift
let transfer = WCSession.default.transferUserInfo([
    "event": "new-message",
    "id": messageID,
    "text": messageText,
])
```

Each call produces a `WCSessionUserInfoTransfer`. Inspect `session.outstandingUserInfoTransfers` to see what's in flight, and cancel a stale one with `transfer.cancel()` rather than letting it deliver.

### `transferCurrentComplicationUserInfo` — budgeted

```swift
if WCSession.default.isComplicationEnabled {
    WCSession.default.transferCurrentComplicationUserInfo(payload)
}
let remaining = WCSession.default.remainingComplicationUserInfoTransfers
```

**50 transfers per day per complication.** watchOS 27 fixes a longstanding bug (FB12819178) where this method didn't work with WidgetKit-based complications at all on earlier releases. The receiver persists the payload (typically to shared `UserDefaults` via an App Group) and reloads the widget:

```swift
WidgetCenter.shared.getCurrentConfigurations { result in
    if case .success(let list) = result {
        for info in list {
            WidgetCenter.shared.reloadTimelines(ofKind: info.kind)
        }
    }
}
```

### `transferFile` — background file transfer

```swift
let transfer = WCSession.default.transferFile(fileURL, metadata: ["kind": "image"])
// transfer.progress for UI
```

Delete the source file inside `session(_:didFinish:error:)` once the transfer completes — it stays on disk otherwise.

### `sendMessage` — live only

```swift
WCSession.default.sendMessage(
    ["request": "nowPlaying"],
    replyHandler: { reply in /* runs on a background thread */ },
    errorHandler: { error in /* not reachable, or timed out */ }
)
```

Requires `isReachable == true` on both sides, and the reply handler must return quickly or the system times it out. From the watch, `sendMessage` wakes a reachable companion iPhone app.

## Always complete every background task

**This is the single most common Watch Connectivity crash pattern.** watchOS wakes the app with a `WKWatchConnectivityRefreshBackgroundTask` to deliver queued transfers. Every task must reach `setTaskCompletedWithSnapshot(_:)` — skipping it drains the background-time budget, and the app gets `SIGKILL`ed once it's exhausted, often long after the actual bug.

Retain tasks and complete them once activation settles and no content is pending:

```swift
private var wcBackgroundTasks: [WKWatchConnectivityRefreshBackgroundTask] = []

func handle(_ backgroundTasks: Set<WKRefreshBackgroundTask>) {
    for task in backgroundTasks {
        if let wcTask = task as? WKWatchConnectivityRefreshBackgroundTask {
            wcBackgroundTasks.append(wcTask)
        } else {
            task.setTaskCompletedWithSnapshot(false)
        }
    }
    completeBackgroundTasks()
}

private var activationObs: NSKeyValueObservation?
private var pendingObs: NSKeyValueObservation?

func bootstrap() {
    activationObs = WCSession.default.observe(\.activationState) { _, _ in
        DispatchQueue.main.async { self.completeBackgroundTasks() }
    }
    pendingObs = WCSession.default.observe(\.hasContentPending) { _, _ in
        DispatchQueue.main.async { self.completeBackgroundTasks() }
    }
}

private func completeBackgroundTasks() {
    guard WCSession.default.activationState == .activated,
          !WCSession.default.hasContentPending else { return }
    wcBackgroundTasks.forEach { $0.setTaskCompletedWithSnapshot(false) }
    wcBackgroundTasks.removeAll()
}
```

## Reachability and companion state

```swift
let s = WCSession.default

s.activationState         // .notActivated / .inactive / .activated
s.isPaired                // iOS only — is any watch paired
s.isWatchAppInstalled     // iOS only — does the paired watch have the companion
s.isComplicationEnabled   // is a complication on an active watch face
s.isReachable             // both apps active and reachable right now
```

Guard every send against the right precondition: `transferUserInfo` works offline, `sendMessage` fails immediately if `isReachable == false`, and `isComplicationEnabled` should gate every spend of the 50/day complication budget.

**`isReachable` is a hint, not a delivery guarantee** — it can read `true` while a `sendMessage`/`sendMessageData` call still fails or never arrives. Always pass an `errorHandler`, and use the queued/background APIs (`transferUserInfo`, `updateApplicationContext`, `transferFile`) for anything that must arrive. For genuine low-latency needs on a shared network, a direct HTTP/SSE channel is the honest escape hatch around Watch Connectivity's reliability ceiling.

## App Group required for complication updates

Watch Connectivity delivers to the watchOS app's process; WidgetKit reads from the widget extension's own process. Bridge them:

1. Enable App Groups on both the watchOS app target and the widget target (same group identifier).
2. Write the incoming payload to `UserDefaults(suiteName: "group.com.yourco.app")` or a file inside the shared container.
3. Call `WidgetCenter.shared.reloadTimelines(ofKind:)` so the widget's `TimelineProvider` re-reads the shared storage.

## Design for the disconnected watch

| Configuration | Required behavior |
|---|---|
| Independent app on a Family Setup watch | No iPhone companion exists — `WCSession` never activates on the watch |
| Independent app, companion not installed | `isWatchAppInstalled == false` on iPhone — fall back to network/CloudKit |
| Paired watch, iPhone asleep or out of Bluetooth range | `isReachable == false`; queued transfers deliver on reconnection |
| LTE watch, iPhone off | `isReachable == false`; the app should still fetch directly over the network |

Concrete pattern: fetch primary data over `URLSession` or CloudKit. Only when `activationState == .activated && isReachable == true`, opportunistically refresh or cache via Watch Connectivity. Never block core content on a transfer arriving.

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| Not calling `setTaskCompletedWithSnapshot` on every `WKWatchConnectivityRefreshBackgroundTask` | App crashes at seemingly random times after a WC wake; background budget exhausted | Retain tasks, complete via `handle(_:)` **and** KVO on `activationState`/`hasContentPending` |
| Using `sendMessage` as the primary sync path | Silent failures when the companion is suspended; timeout errors | Switch to `transferUserInfo` (queued) or `updateApplicationContext` (latest-wins) |
| Watch Connectivity as the only data path | Family Setup watches show empty state; LTE watches away from iPhone show stale data | Fetch primary data via `URLSession`/CloudKit; use WC opportunistically |
| Spamming `transferCurrentComplicationUserInfo` on every small change | Silent throttling past 50/day; complication stops updating | Batch updates, check `remainingComplicationUserInfoTransfers`, use APNs widget push for high frequency |
| Updating a widget without an App Group | Transfer arrives but the widget never reflects it | Share via App Group `UserDefaults`/file; call `WidgetCenter.shared.reloadTimelines(ofKind:)` |
| Missing `sessionDidBecomeInactive`/`sessionDidDeactivate` on iOS | Second paired watch never receives data | Implement both; call `activate()` again inside `sessionDidDeactivate` |
| Activating the session before assigning a delegate | Activation callback fires before anything listens; dropped events | Assign the delegate first, then call `activate()` |
| Assuming `sendMessage` errors fire at most once | Duplicate retries or duplicated side effects | WC offers no exactly-once guarantee — tag messages with your own ID and dedupe |
| Using `updateApplicationContext` when ordered delivery is needed | Receiver misses events between wake intervals | Use `transferUserInfo` for anything where every event matters |
| Not deleting files after `transferFile` completes | Files accumulate on the sender's disk | Remove the source file in `session(_:didFinish:error:)` |
