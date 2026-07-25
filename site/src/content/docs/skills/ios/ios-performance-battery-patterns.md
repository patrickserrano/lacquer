---
title: "ios-performance-battery-patterns"
description: "Use when touching widgets, animations, networking configuration, or background/observer cleanup — battery and performance patterns for WidgetKit timelines, SwiftUI animations, Low Power Mode handling, URLSession config, and NotificationCenter/Task cleanup under `@Observable`."
---

:::note
Generated from [`profiles/ios/skills/ios-performance-battery-patterns/SKILL.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-performance-battery-patterns/SKILL.md) — edit that file, not this page.
:::


# Battery & Performance Patterns

Apply these whenever touching widgets, animations, networking, or background work.

## Widgets

- Limit `Timeline` entries to **≤ 2** (current + one next-day refresh). More entries run the provider repeatedly and drain battery.
- Use `.atEnd` reload policy — let WidgetKit decide when to refresh.

## Animations

- Always stop animations in `.onDisappear`. Animations left running off-screen still consume CPU/GPU.
- Bind repeating animations to a `@State var isAnimating = false`: set `true` in `.onAppear`, `false` in `.onDisappear`, and pass `value: isAnimating` to `withAnimation`.
- Use `.repeatCount(N)` instead of `.repeatForever` for attention animations.

## Low Power Mode

Guard expensive operations before they start:
```swift
guard !ProcessInfo.processInfo.isLowPowerModeEnabled else { return }
```
Apply to: image preloading, background downloads, video prefetch, heavy sync.

## Network

```swift
let config = URLSessionConfiguration.default
config.allowsConstrainedNetworkAccess = false  // respect Low Data Mode
config.allowsExpensiveNetworkAccess = false    // avoid cellular when Wi-Fi preferred
config.waitsForConnectivity = true             // queue rather than fail when offline
```

## Observer & Task Cleanup

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
