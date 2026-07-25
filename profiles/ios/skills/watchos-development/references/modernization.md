# Modernizing a watchOS Project

## Core principle

Modernize deliberately, along four independent axes, on the way to watchOS 26:

1. Dependent → **independent** app
2. Dual-target → **single-target**
3. WatchKit storyboards → **SwiftUI**
4. ClockKit → **WidgetKit** complications

None of these are optional for new watchOS 10+ development, and none require a full rewrite — each is addressable as an incremental project change.

## Axis 1 — dependent to independent

If the watchOS app can't function when the iPhone isn't reachable, it's dependent — and that breaks on Family Setup watches and unpaired watches. Switch to independent: project editor → Watch App target → General → Deployment Info → check "Supports Running Without iOS App Installation". Then:

- Move account creation and sign-in to the watch
- Route data fetching through `URLSession` directly from the watch
- Demote `WCSession` from primary data path to opportunistic optimization (see `watch-connectivity.md`)
- Register for APNs directly on the watch, rather than relying on iPhone push handoff

Full detail on the independent app shape: `platform-basics.md`.

## Axis 2 — dual-target to single-target

A dual-target app has a Watch App target *and* a separate WatchKit Extension target. Xcode's consolidation tool does most of the mechanical work:

1. Back up the project (a full copy — rollback is otherwise painful)
2. Xcode → Editor → **Validate Settings**
3. Check "Project — Upgrade to a single-target watch app"
4. **Perform Changes**
5. For any remaining storyboards, re-point each interface controller's Class module to the watchOS app module (Identity inspector → Custom Class)
6. Delete the extension's Info.plist and other extension-only files, and clean up project-navigator groups

The tool performs `WKExtension` → `WKApplication` and `WKExtensionDelegate` → `WKApplicationDelegate` renames, merges Info.plist content (moving `CLKComplicationPrincipalClass`/`CLKComplicationSupportedFamilies` to the app's plist), and relocates complication-controller code to the app target. This swap is not optional cleanup anymore: the watchOS 27 SDK formally deprecates `WKExtension`/`WKExtensionDelegate` for any app with a deployment target of watchOS 9.2+. Background-refresh scheduling is also deprecated in 27 in favor of `BGTaskScheduler` — see `background-and-networking.md`.

| Requirement | Minimum |
|---|---|
| Single-target watchOS app | watchOS 7 |
| HealthKit authorization inheritance from companion iOS app | watchOS 9.2 |

**Keep the dual-target configuration if the app must support watchOS 9.1 or earlier and uses HealthKit** — permission inheritance from the companion requires 9.2+. Everyone else should migrate.

## Axis 3 — WatchKit storyboards to SwiftUI

Storyboards have been deprecated since watchOS 7; SwiftUI is the only forward path. Two strategies:

### Full rewrite (recommended for smaller apps)

```swift
import SwiftUI

@main
struct MyWatchApp: App {
    var body: some Scene {
        WindowGroup { RootView() }
    }
}
```

If a `WKApplicationDelegate` is still needed (remote notifications, workout recovery, Now Playing), attach it via `@WKApplicationDelegateAdaptor` — see `platform-basics.md`. Rebuild each storyboard scene as SwiftUI using the navigation patterns in `design-for-watchos.md` (`NavigationStack`, `NavigationSplitView`, `TabView(.verticalPage)`) rather than reimplementing the old paging model, then delete the storyboard once the last scene migrates.

### Incremental (for large apps)

Host SwiftUI views inside existing WatchKit interface controllers via `WKHostingController`, migrating one screen at a time:

```swift
class MySettingsController: WKHostingController<SettingsView> {
    override var body: SettingsView { SettingsView() }
}
```

Keep the storyboard, swap scenes one by one, delete the storyboard when every scene is SwiftUI. If the app still has `WKExtensionDelegate`, migrate it to `WKApplicationDelegate` in the same pass — it's a rename-and-move, and integrates with SwiftUI via `@WKApplicationDelegateAdaptor`.

## Axis 4 — ClockKit to WidgetKit complications

Target architecture lives in `smart-stack-and-complications.md`; this covers the transition mechanics.

### The partial-migration trap

The instant a WidgetKit extension provides any widget-based complication, the system disables ClockKit entirely for that app — it stops calling `CLKComplicationDataSource` methods to request timeline entries. There is no "both at once" state. **Migrate every ClockKit complication to WidgetKit in a single release** — a partial migration silently disables the ones left behind on every user's device.

### Migration steps

1. Add a Widget Extension target (watchOS → Widget Extension). Enable "Include Configuration App Intent" if the app supports multiple complication variants dynamically.
2. Build one WidgetKit widget per existing ClockKit complication, implementing `placeholder(in:)` (generic redacted entry), `getSnapshot(in:completion:)` (gate on `context.isPreview`), and `getTimeline(in:completion:)` (returns `Timeline<Entry>` with a reload policy).

   ```swift
   struct CoffeeTrackerEntry: TimelineEntry {
       let date: Date
       let mgCaffeine: Double
       let totalCups: Double
   }
   ```

3. Add `CLKComplicationWidgetMigrator` to the existing `CLKComplicationDataSource` so the system can map a user's existing watch-face customization onto the new widget:

   ```swift
   extension ComplicationController: CLKComplicationWidgetMigrator {
       func getWidgetConfiguration(
           from complicationDescriptor: CLKComplicationDescriptor,
           completionHandler: @escaping (CLKComplicationWidgetMigrationConfiguration?) -> Void
       ) {
           // CLKComplicationWidgetMigrationConfiguration is an abstract base (init is
           // NS_UNAVAILABLE) — construct a concrete subclass: Static for static widgets,
           // Intent for intent-configured ones.
           let config = CLKComplicationStaticWidgetMigrationConfiguration(
               kind: "com.example.app.coffee-caffeine",
               extensionBundleIdentifier: widgetExtensionBundleID
           )
           completionHandler(config)
       }
   }
   ```

4. Bundle multiple complications via `WidgetBundle`:

   ```swift
   @main
   struct ComplicationBundle: WidgetBundle {
       var body: some Widget {
           CaffeineComplication()
           TotalCupsComplication()
           CombinedComplication()
       }
   }
   ```

5. Remove the ClockKit target and code once every complication has migrated. Keep the migrator around for as long as the deployment target still covers users who installed the app while ClockKit was live.

### Budget changes

ClockKit negotiated a custom per-data-source budget with the system; WidgetKit gives up to 75 timeline reloads/day per complication, weighted by visibility on an active watch face. Complications on the active face trend toward the higher end; hidden ones get fewer. Design timelines with enough entries that the daily budget gives adequate freshness without depending on explicit reloads.

## From watch-only to watch+iOS companion

Adding an iOS companion to a watch-only app is one-way — there's no rolling back to watch-only after the iOS app ships:

1. Back up the project
2. Add a new iOS app target in Xcode
3. Signing & Capabilities → pick team, set bundle ID. **Critical**: the iOS bundle ID must be the prefix of the watchOS bundle ID (e.g. `com.yourco.coffee` vs `com.yourco.coffee.watchkitapp`)
4. General → Frameworks, Libraries, and Embedded Content → add the watchOS app as embedded content of the iOS app
5. Confirm the watchOS app's `WKCompanionAppBundleIdentifier` matches the iOS app's bundle ID
6. Optionally set `WKRunsIndependentlyOfCompanionApp = NO` if Xcode should auto-install the iOS app during watch-app runs

Result: an independent watchOS app with a companion iOS app — the recommended modern shape (see `platform-basics.md`). Remember to flip `WKWatchOnly` to `NO` on the Watch App target once the companion exists.

## Migration sequencing

Fix build architecture first (it's the submission hard gate), then project shape (so the rest builds cleanly against a single target), then UI (where the actual work is), then independence + complication migration (they share the widget-extension infrastructure), then submit:

1. Standard Architectures (64-bit/ARM64) on
2. Consolidate to single-target
3. Storyboards → SwiftUI (full or incremental)
4. Enable independent mode + direct APNs registration
5. ClockKit → WidgetKit (all complications, one release)
6. Submit on the watchOS 26 SDK

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| Migrating only some ClockKit complications to WidgetKit | Remaining ClockKit complications silently stop updating on every device | Migrate all of them in one release; use `CLKComplicationWidgetMigrator` to preserve watch-face customization |
| Consolidating a HealthKit dual-target app to single-target below watchOS 9.2 | HealthKit permissions no longer inherit from the iOS companion; users re-prompted | Keep dual-target if supporting watchOS 9.1 or earlier with HealthKit |
| Skipping `@WKApplicationDelegateAdaptor` after moving to a SwiftUI `App` | Remote notifications and workout recovery stop working | Attach the delegate; only drop it if no callback is actually needed |
| Building a new WidgetKit extension without `CLKComplicationWidgetMigrator` | User's watch-face customization disappears on update | Implement the migrator before shipping the WidgetKit replacement |
| iOS companion bundle ID isn't a prefix of the watchOS bundle ID | Watch app won't install from the iOS app | Rename so iOS is `com.yourco.app` and watchOS is `com.yourco.app.watchkitapp` |
| Leaving `WKHostingController` wrappers after every scene is SwiftUI | Storyboard infrastructure lingers, blocking full single-target consolidation | Delete the storyboard, hosting controllers, and WatchKit extension target once fully SwiftUI |
| Relying on `NavigationView` post-migration | Deprecation warnings; broken deep-link/state-restore paths | Use `NavigationStack` with `path: Binding<[T]>` — see `design-for-watchos.md` |
| Submitting on an older watchOS SDK after April 2026 | Rejected at App Store Connect | Audit `WATCHOS_DEPLOYMENT_TARGET` and SDK version before submission |
| Forgetting to flip `WKWatchOnly` to `NO` after adding an iOS companion | Watch target still claims watch-only distribution despite the new iOS app | Set `WKWatchOnly = NO` on the Watch App target |
