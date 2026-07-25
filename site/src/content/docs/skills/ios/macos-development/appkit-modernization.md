---
title: "AppKit Modernization"
description: "Reference for the macos-development skill."
---

:::note
Generated from [`profiles/ios/skills/macos-development/references/appkit-modernization.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-development/references/appkit-modernization.md) — edit that file, not this page.
:::

# AppKit Modernization

Replacing `mouseDown` tracking with modern input APIs, keyboard navigation,
graceful termination, state restoration, and the macOS 27 look (concentric
corners, interactive glass, touch input) in an existing AppKit app.

## Modern input — replace mouseDown overrides

Gesture recognizers are the common event-handling language across AppKit,
SwiftUI, and Catalyst — reach for the dedicated API before a `mouseDown`
override:

| Overriding mouseDown for | Use instead |
|---|---|
| Tracking selection | Observe `selected` on `NSCollectionViewItem`/`NSTableRowView`, or delegate callbacks |
| Context menus | `NSView.defaultMenu` (class-wide), `NSResponder.menu` (per responder), or `menu(for:)` (per event) |
| Drag-and-drop from containers | Modern dragging delegates: `tableView(_:pasteboardWriterForRow:)` → return an `NSPasteboardItem`; equivalents on `NSCollectionView`/`NSOutlineView`/`NSBrowser` |
| Text selection outside `NSTextView` | `NSTextSelectionManager` (macOS 27) — attach to a view + set a text-selection data source |
| Custom interactions | Standard `NSGestureRecognizer`s, or your own subclass |

### Control events (UIKit-style, now in AppKit)

React to tracking-state changes on standard controls without subclassing.
`addTarget(_:action:for:)`/`removeTarget(_:action:for:)` work from macOS 11;
the semantic cases below are new in the macOS 27 SDK:

```swift
button.addTarget(self, action: #selector(trackingEndedOutsideHandler), for: .trackingEndedOutside)
```

| `NSControl.Events` case | Availability |
|---|---|
| down/up/tracking cases (`.trackingBegan`, `.trackingEndedInside`, `.trackingEndedOutside`, …) | macOS 11 |
| `.trackingRepeated` (click count > 1) | macOS 27 |
| `.valueChanged` (sliders, etc.) | macOS 27 |
| `.primaryActionTriggered` | macOS 27 |
| `.menuActionTriggered` (menu gesture fired, before the menu presents) | macOS 27 |
| `.applicationReserved` (app-defined event range) | macOS 27 |

### Hit-testing gotcha

Gesture recognizers operate on a view and its subviews, so an overlapping
**sibling** view silently swallows clicks. Resize the sibling so it doesn't
overlap, or let events fall through a deliberate overlay:

```swift
override func hitTest(_ point: NSPoint) -> NSView? { nil }   // fall through to content underneath
```

## Keyboard navigation

Full Keyboard Navigation (System Settings > Keyboard) moves focus via
Tab/Shift-Tab through the key view loop. Set
`window.autorecalculatesKeyViewLoop = true` to recalculate automatically as
views come and go, or own loop maintenance manually. Give a status item's
`button` a target/action so Return fires it during keyboard navigation.

### Status item expanded interface sessions (macOS 27)

A status item that shows a custom window ("expanded interface") must tell
AppKit that UI is active, or keyboard focus and menu tracking misbehave.
Don't toggle the window from a plain target/action:

```swift
lightStatusItem.expandedInterfaceDelegate = self   // set when the item is created

// @MainActor on the conformance — the protocol isn't actor-annotated, so a
// main-actor delegate needs it to satisfy the requirements in Swift 6 mode
extension LightAppDelegate: @MainActor NSStatusItemExpandedInterfaceDelegate {
    func statusItem(_ statusItem: NSStatusItem, didBegin session: NSStatusItemExpandedInterfaceSession) {
        // show the window
    }
    func statusItemDidEndExpandedInterfaceSession(_ statusItem: NSStatusItem, animated: Bool) {
        // order the window out
    }
}

lightStatusItem.expandedInterfaceSession?.cancel()   // dismiss programmatically; may also auto-cancel on focus loss
```

Items that assign an `NSMenu` directly to their button don't need this — menu
tracking is automatic. SwiftUI's `MenuBarExtra` does this work for you (see
`appkit-interop.md`, SwiftUI Scenes from AppKit).

## Graceful termination and state restoration

A modern Mac app quits without pushback (overnight updates need to reboot)
and relaunches as if it never quit.

**Termination**: `NSWindow.preventsApplicationTerminationWhenModal` defaults
to `true` — a presented sheet blocks quit. Set `false` on every modal/sheet
that doesn't strictly need intervention before quitting.

**Restoration** (`NSWindowRestoration`, three steps):

```swift
// 1. Opt in
window.identifier = NSUserInterfaceItemIdentifier(WindowIdentifiers.mainWindow)
window.setFrameAutosaveName(WindowIdentifiers.mainWindow)   // skip for document windows
window.isRestorable = true                                   // also restores minimized/frontmost/full-screen
window.restorationClass = WindowRestorationHandler.self

// 2. Encode UI state (any NSResponder can override this) — encode UI, never model/document data
override func encodeRestorableState(with coder: NSCoder) {
    super.encodeRestorableState(with: coder)
    coder.encode(selectedProduct?.identifier.uuid, forKey: RestorationKeys.productIdentifier)
}
invalidateRestorableState()   // call when UI state changes — encoding only happens for invalidated objects

// 3. On relaunch: recreate windows, then decode
class WindowRestorationHandler: NSObject, NSWindowRestoration {
    static func restoreWindow(withIdentifier identifier: NSUserInterfaceItemIdentifier, state: NSCoder,
                               completionHandler: @escaping (NSWindow?, Error?) -> Void) {
        // recreate the window controller for this identifier...
        // ALWAYS call completionHandler — AppKit waits on every restorable window; pass the error on failure
    }
}
override func restoreState(with coder: NSCoder) {
    super.restoreState(with: coder)
    // decode keys, hand values to view controllers
}
```

## macOS 27 look and feel

**System-wide, no rebuild needed** — apps that adopted Liquid Glass on macOS
26 pick these up just by running on 27: `NSScrollEdgeEffectStyle` resolves to
a hard edge under free-floating text (window titles), sidebars extend to the
window edges with semibold selection text and content flowing behind them,
bordered toolbar items over the sidebar adopt Liquid Glass.

**New API in the 27 SDK**:

- **Interactive glass**: `NSGlassEffectView.effectIsInteractive = true`
  makes glass subtly bounce on click — use on controls/buttons/interactive
  glass containers only, sparingly.
- **Concentric corners** — content near a container's corner follows the
  container's curve:

```swift
class LocalWeatherView: NSView {
    override var cornerConfiguration: NSViewCornerConfiguration? {   // readonly — override the getter
        .uniformCorners(radius: .containerConcentric(8))              // 8pt minimum, always rounded
    }
}
```

| API | Notes |
|---|---|
| `NSViewCornerRadius` | `.containerConcentric` / `.containerConcentric(minimum:)` / `.fixed(_:)` |
| `NSViewCornerConfiguration` factories | `.uniformCorners(radius:)`, `.corners(radius:)`, per-corner `.corners(topLeftRadius:…)`, `.capsule`, `.capsule(maximumRadius:)`, `.uniformEdges(topRadius:bottomRadius:)` |
| `NSView.effectiveCornerRadii` / `viewDidChangeEffectiveCornerRadii()` / `invalidateCornerConfiguration()` | Read resolved radii; react to changes; force re-resolution |

Also new: `NSSegmentedControl.role` (`.automatic`/`.tabs`/`.valueSelection`)
and `NSToolbarItemGroup.role` declare semantic meaning so the system styles
them appropriately.

## Touch and gesture additions (macOS 27 SDK)

| API | What it is |
|---|---|
| `NSScreen.touchCapabilities` | Whether the current screen reports touch input |
| `NSScrollView.isTouchScrollingEnabled` + `minimumNumberOfTouchesForScrolling`/`maximumNumberOfTouchesForScrolling` | Touch-driven scrolling with finger-count thresholds |
| `NSScrollView.refreshController` (`NSRefreshController`) | Pull-to-refresh for scroll views |
| `NSView.beginDraggingSession(items:gesture:source:)` | Start a dragging session from a gesture recognizer |
| `NSEvent.isTouchSwipeNavigationEnabled` | Class property — user's touch swipe-navigation preference |
| `NSGestureRecognizer.isCancellableByScrollGesture` | Whether a scroll gesture can cancel this recognizer |
| `NSPanGestureRecognizer.minimumNumberOfTouches`/`maximumNumberOfTouches` | Finger-count thresholds for pans |

`WKWebView` hosts the same `NSRefreshController` via its own
`refreshController` property on macOS 27 — pull-to-refresh for web content is
first-class there, macOS-only (on iOS wire a `UIRefreshControl` to
`WKWebView.scrollView` yourself).

## Checklist

- [ ] No `mouseDown` overrides where a dedicated API exists
- [ ] Control interactions via control events or gesture recognizers, not tracking loops
- [ ] `hitTest` fall-through (or resized siblings) for overlay views blocking clicks
- [ ] `autorecalculatesKeyViewLoop` enabled, or the key view loop maintained manually
- [ ] Status-item custom UI tracked with expanded interface sessions
- [ ] `preventsApplicationTerminationWhenModal = false` on every sheet that doesn't need intervention
- [ ] Windows restorable: identifier + `isRestorable` + `restorationClass`; `restoreWindow` always calls its completion handler
- [ ] `invalidateRestorableState()` called on UI changes that should persist
- [ ] Corner-adjacent views adopt `cornerConfiguration` concentricity
