---
title: "Controls and Live Activities on Apple Watch"
description: "Reference for the watchos-development skill."
---

:::note
Generated from [`profiles/ios/skills/watchos-development/references/controls-and-live-activities.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/watchos-development/references/controls-and-live-activities.md) — edit that file, not this page.
:::

# Controls and Live Activities on Apple Watch

## Core principle

Controls execute actions, widgets display information, Live Activities track bounded events — pick the surface by that distinction, not by habit:

| The user wants to... | Use |
|---|---|
| Change a setting or toggle a device | Control (`ControlWidgetToggle`) |
| Trigger an action without opening the app | Control (`ControlWidgetButton`) |
| Open the app at a specific screen | Control with `OpenIntent` (iPhone-only, see below) |
| Glance at info throughout the day | Widget — see `smart-stack-and-complications.md` |
| Follow an event with a start and end | Live Activity |

## Controls on Apple Watch (watchOS 26)

Controls can land in Control Center, the Smart Stack, the Apple Watch Ultra Action button, and Double Tap. Two ways a control reaches the watch:

| Setup | Where the action runs | Watch app required |
|---|---|---|
| Control ships in the iPhone app only | iPhone (relays to the companion) | No — appears on the watch automatically |
| Control ships in the Watch app | Apple Watch | Yes |

Controls whose action foregrounds the iPhone app — e.g. an `OpenIntent` opening a specific screen — are filtered out of watch placement, because the action would have to run on iPhone but the user tapped from the watch. Use `OpenIntent` only on iPhone-targeted controls; give watch-visible controls a non-foregrounding `AppIntent`.

## Anatomy of a control

Three pieces: a `ControlWidget` type, a `StaticControlConfiguration` (non-configurable) or `AppIntentControlConfiguration` (configurable) body, and the `AppIntent`/`OpenIntent`/`SetValueIntent` that runs on tap. Add `.displayName(_:)` and `.description(_:)` so the gallery entry is legible.

### Toggle (on/off with state)

```swift
struct TimerToggle: ControlWidget {
    static let kind: String = "com.example.MyApp.TimerToggle"

    var body: some ControlWidgetConfiguration {
        StaticControlConfiguration(kind: Self.kind, provider: Provider()) { value in
            ControlWidgetToggle(
                "Productivity Timer",
                isOn: value,
                action: ToggleTimerIntent(),
                valueLabel: { isOn in Label(isOn ? "Running" : "Stopped", systemImage: "timer") }
            )
        }
        .displayName("Productivity Timer")
        .description("Start and stop a productivity timer.")
    }
}

extension TimerToggle {
    struct Provider: ControlValueProvider {
        var previewValue: Bool { false }
        func currentValue() async throws -> Bool { TimerService.shared.isRunning }
    }
}

struct ToggleTimerIntent: SetValueIntent {
    static var title: LocalizedStringResource = "Productivity Timer"

    @Parameter(title: "Timer is running")
    var value: Bool

    func perform() async throws -> some IntentResult {
        TimerService.shared.setRunning(value)
        return .result()
    }
}
```

**`value` is system-managed** — don't set it manually inside `perform()`. The system populates it with the desired new state; your job is to mutate the underlying model to match, not to compute the toggle's own displayed value.

### Button (fire-and-forget)

```swift
struct PerformActionButton: ControlWidget {
    var body: some ControlWidgetConfiguration {
        StaticControlConfiguration(kind: "com.example.myApp.performActionButton") {
            ControlWidgetButton(action: PerformAction()) {
                Label("Perform Action", systemImage: "checkmark.circle")
            }
        }
        .displayName("Perform Action")
        .description("An example control that performs an action.")
    }
}

struct PerformAction: AppIntent {
    static let title: LocalizedStringResource = "Perform action"

    func perform() async throws -> some IntentResult {
        MyService.shared.doWork()
        return .result()
    }
}
```

### Configurable control (watchOS 26)

For controls where the user picks the target (which timer, which light, which beach), use `AppIntentControlConfiguration` + `AppIntentControlValueProvider`:

```swift
struct ConfigurableMeditationControl: ControlWidget {
    var body: some ControlWidgetConfiguration {
        AppIntentControlConfiguration(kind: WidgetKinds.configurableMeditationControl, provider: Provider()) { value in
            ControlWidgetToggle(
                "Ocean Meditation",
                isOn: value.isActive,
                action: StartMeditationIntent(configuration: value.configuration),
                valueLabel: { _ in Label("Meditate", systemImage: "leaf") }
            )
        }
        .displayName("Ocean Meditation")
        .description("Meditation with optional ocean sounds.")
        .promptsForUserConfiguration()
    }
}

extension ConfigurableMeditationControl {
    struct Provider: AppIntentControlValueProvider {
        func previewValue(configuration: TimerConfiguration) -> Value {
            Value(configuration: configuration, isActive: false)
        }
        func currentValue(configuration: TimerConfiguration) async throws -> Value {
            Value(configuration: configuration, isActive: MeditationService.shared.isActive(for: configuration))
        }
    }

    struct Value {
        let configuration: TimerConfiguration
        let isActive: Bool
    }
}
```

`.promptsForUserConfiguration()` tells the system to surface configuration UI when the user adds the control.

## Bundle every control with existing widgets

```swift
@main
struct MyControlsAndWidgetsBundle: WidgetBundle {
    var body: some Widget {
        PerformActionButton()
        TimerToggle()
        ConfigurableMeditationControl()
        MyAppTimelineWidget()
    }
}
```

Order here sets gallery order — lead with the most useful control. A separate `WidgetBundle` for controls means they simply won't appear in the gallery alongside the rest.

## Opening the app from a control (iPhone-only)

```swift
struct LaunchAppIntent: OpenIntent {
    static var title: LocalizedStringResource = "Launch App"

    @Parameter(title: "Target")
    var target: LaunchAppEnum
}

enum LaunchAppEnum: String, AppEnum {
    case timer
    case history

    static var typeDisplayRepresentation = TypeDisplayRepresentation("Productivity Timer's app screens")
    static var caseDisplayRepresentations = [
        LaunchAppEnum.timer: DisplayRepresentation("Timer"),
        LaunchAppEnum.history: DisplayRepresentation("History")
    ]
}
```

The intent's target membership must include both the app and the widget extension — if only the extension carries it, the app can't be opened. And remember the watch filter: this intent type is inherently iPhone-only territory since it foregrounds the app.

## Double Tap (watchOS 11+)

Double Tap binds to the frontmost surface's primary action automatically — a control's wired `ControlWidgetToggle`/`ControlWidgetButton` action needs no extra work. On a regular view, opt the primary button in with `.handGestureShortcut(.primaryAction)`. Users can disable Double Tap globally, so the primary action must remain reachable by a normal tap too.

## Live Activities on Apple Watch

Live Activities are authored once on iOS with ActivityKit + WidgetKit; the watch only displays them — it never starts its own. They surface at the top of the Smart Stack on a paired watch automatically. What you control on the iOS side also drives the watch presentation:

- The Dynamic Island presentations (compact, minimal, expanded) you author for iOS are what surfaces on watch — test each on a paired device.
- The watch defaults to the **minimal** presentation when multiple Live Activities are active, so design and test minimal first, not last.

### `ActivityAttributes` shape

One per activity kind — static data on the outer struct, dynamic data on `ContentState`:

```swift
import ActivityKit

struct OrderAttributes: ActivityAttributes {
    struct ContentState: Codable & Hashable {
        let estimatedArrival: Date
        let driverName: String
        let statusMessage: String
    }

    let orderID: String
    let restaurantName: String
}
```

Set `NSSupportsLiveActivities = YES` in the iOS app's Info.plist, and include "Include Live Activity" when creating the widget extension. Full ActivityKit API surface — `Activity.request`, push-token updates, lifecycle — lives in the `activitykit` skill; this covers only the watch-visible constraints.

### Constraints that also apply on watch

| Constraint | Value |
|---|---|
| Maximum active duration | 8 hours (system auto-ends); Lock Screen persists up to 4 more hours (12 total) |
| Max payload (static + dynamic combined) | 4 KB |
| Network/location access from the Live Activity view | None — update only via ActivityKit or APNs |
| Image resolution | Must be ≤ the presentation size; oversized images fail to start the activity |

The 4 KB cap bites fast with descriptive strings (ETA text, status messages) — keep payloads tight and fetch rich detail from the companion app on tap. Updates arrive via ActivityKit push tokens over APNs; the same rule as widgets applies — pushes propagate to the watch without any Watch Connectivity involvement.

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| `OpenIntent` on a control expected to appear on watch | Control missing from the watch controls gallery | Watch-visible controls can't foreground the iPhone app — use a non-Open `AppIntent`, keep `OpenIntent` for iPhone-only variants |
| Manually setting `value` inside a `SetValueIntent`'s `perform()` | Control state drifts or jitters between taps | Treat `value` as read-only input; mutate the model to match it |
| Reading/writing control state through an in-process `.shared` singleton | Toggle drifts or appears to do nothing | The control runs in the widget extension's process, a different one from the app — persist state in an App Group and call `ControlCenter.shared.reloadControls(ofKind:)` after mutation |
| App Intent target membership set only on the widget extension | Control appears but the intent fails silently | Add the intent file to both the app and the extension target |
| Shipping a Live Activity without testing the minimal presentation | Illegible or truncated Smart Stack card | Design and test minimal first — it's the watch's default when multiple activities compete |
| Live Activity image sized for the Dynamic Island leading slot | Activity fails to start on watch's smaller minimal presentation | Provide presentation-sized assets |
| Network or location access inside a Live Activity view | Stale renders or crashes | Update only through the ActivityKit API or APNs |
| Static + dynamic payload over 4 KB | Update rejected; state frozen | Trim strings and numeric precision; fetch rich data from the app instead |
| Missing `NSSupportsLiveActivities` in Info.plist | `Activity.request(attributes:...)` throws; nothing appears | Set the key to `YES` on the iOS app target |
| Assuming Double Tap is always available | Primary action unreachable for users who disabled it or have older hardware | Keep a tappable equivalent — Double Tap is an accelerator, not the only path |
| Controls placed in a separate `WidgetBundle` from existing widgets | Controls don't appear in the gallery | Bundle everything under one `@main WidgetBundle` |
