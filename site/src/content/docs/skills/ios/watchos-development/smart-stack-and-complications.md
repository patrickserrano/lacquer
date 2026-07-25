---
title: "Smart Stack and Complications"
description: "Reference for the watchos-development skill."
---

:::note
Generated from [`profiles/ios/skills/watchos-development/references/smart-stack-and-complications.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/watchos-development/references/smart-stack-and-complications.md) — edit that file, not this page.
:::

# Smart Stack and Complications

Complications, widgets, Live Activities, and controls all compete for the same Smart Stack real estate on watchOS 26. Pick the surface by primary purpose, then use RelevanceKit so the system surfaces content at the right moment.

| Primary purpose | Surface |
|---|---|
| A quick action (toggle a setting, trigger a device) | Control — see `controls-and-live-activities.md` |
| Info visible throughout the day (weather, next event) | Widget (timeline or relevant) |
| A bounded event (flight, sports match) | Live Activity — see `controls-and-live-activities.md` |

## Complication surfaces

| Widget family | Placement | Typical content |
|---|---|---|
| `accessoryCircular` | Corner, sub-dial, Modular Compact | One metric (steps, battery, next event time) |
| `accessoryRectangular` | Modular large, Infograph rectangular | Two to three lines, optional icon |
| `accessoryInline` | Inline band above/below the face | Single system-tinted line |
| `accessoryCorner` | Corner of Infograph face **only** | Curved text + gauge |
| `AccessoryWidgetGroup` | Wraps three circular views under one label | Bundled multi-metric complication |

Build these with the standard WidgetKit shape — `StaticConfiguration`/`AppIntentConfiguration`, a `TimelineProvider`/`AppIntentTimelineProvider`, and a SwiftUI view. The migration rule that matters: the instant a WidgetKit complication is offered, the system stops calling ClockKit's `CLKComplicationDataSource` methods entirely. Migrate every ClockKit complication to WidgetKit in a single release — a partial migration silently breaks the ones left behind. Full migration mechanics live in `modernization.md`.

## Smart Stack basics

Rotating the Digital Crown above the watch face reveals the Smart Stack, which the system populates by predicted relevance. Two ways to signal relevance:

1. **Timeline widget + `RelevanceConfiguration`** — compute entries as usual, and attach a relevance hint per entry.
2. **Relevant widget** (watchOS 26) — a configuration type that generates entries on demand when a `RelevantContext` matches; multiple instances can appear at once.

Use the relevant widget when several instances of the same widget could matter simultaneously (three overlapping calendar events, two upcoming flights). Use the timeline widget for steady content (hourly weather, step count).

## RelevanceKit

Only `RelevantContext` and its `.date(interval:kind:)` / `.location(category:)` overloads live in RelevanceKit and are new in watchOS 26. `WidgetRelevance<Configuration>` and `WidgetRelevanceAttribute<Configuration>` are **WidgetKit** types that have existed since iOS 18 / watchOS 11 — don't conflate the two frameworks. You wrap the watchOS-26 `RelevantContext` cases in the older WidgetKit `WidgetRelevance` container.

```swift
func relevance() async -> WidgetRelevance<Void> {
    guard let context = RelevantContext.location(category: .beach) else {
        return WidgetRelevance<Void>([])
    }
    return WidgetRelevance([WidgetRelevanceAttribute(context: context)])
}
```

`RelevantContext.location(category:)` returns `nil` for unsupported categories — guard it, don't force-unwrap.

## Relevant widgets — the watchOS 26 pattern

Think of it as a multi-card timeline widget:

| Relevant-widget type | Role | Timeline-widget analog |
|---|---|---|
| `RelevanceEntry` | Data for one card | `TimelineEntry` |
| `RelevanceEntriesProvider` | Builds entries, declares relevance | `TimelineProvider` |
| `RelevanceConfiguration` | Glues provider + view into a `Widget` | `StaticConfiguration` |

```swift
struct BeachEventRelevanceProvider: RelevanceEntriesProvider {
    let store: BeachEventStore

    func relevance() async -> WidgetRelevance<BeachEventConfigurationIntent> {
        let attributes = store.upcomingEvents().map { event in
            WidgetRelevanceAttribute(
                configuration: BeachEventConfigurationIntent(event: event),
                context: .date(interval: event.dateInterval, kind: .default)
            )
        }
        return WidgetRelevance(attributes)
    }

    func entry(configuration: BeachEventConfigurationIntent, context: Context) async throws -> BeachEventRelevanceEntry {
        context.isPreview ? .previewEntry : BeachEventRelevanceEntry(event: configuration.event)
    }

    func placeholder(context: Context) -> BeachEventRelevanceEntry { .placeholderEntry }
}

struct BeachEventWidget: Widget {
    private let store = BeachEventStore.shared

    var body: some WidgetConfiguration {
        RelevanceConfiguration(kind: "BeachEventWidget", provider: BeachEventRelevanceProvider(store: store)) { entry in
            BeachWidgetView(entry: entry)
        }
        .configurationDisplayName("Beach Events")
        .description("Events at the beach")
    }
}
```

`relevance()` returns one `WidgetRelevanceAttribute` per card; `entry(configuration:context:)` resolves a single attribute into its card entry (branch on `context.isPreview`); `placeholder(context:)` covers the loading skeleton.

## Deduplicating timeline + relevant widgets

If a user has both a timeline widget and a matching relevant widget, the system can show two cards for the same event. Associate them so the relevant widget's cards replace the timeline card:

```swift
RelevanceConfiguration(kind: "BeachEventWidget", provider: provider) { entry in
    BeachWidgetView(entry: entry)
}
.associatedKind(WidgetKinds.beachEventsTimeline)
```

## Configurable widgets and controls (watchOS 26)

Return an empty recommendations array to mark a widget as user-configurable on the watch face and Smart Stack:

```swift
func recommendations() -> [AppIntentRecommendation<BeachConfigurationIntent>] {
    if #available(watchOS 26, *) {
        return []  // empty signals user-configurable
    } else {
        return recommendedBeaches  // pre-26: return real preconfigured options
    }
}
```

Controls use `AppIntentControlConfiguration` + `AppIntentControlValueProvider` — see `controls-and-live-activities.md` for the full shape.

## Workout-app Smart Stack suggestions

Free promotion if the workout data is accurate: specify the correct `HKWorkoutActivityType`, record real (not approximate or front-loaded) start/end times, and attach route data via `HKWorkoutRouteBuilder` when applicable. The system uses that to predict when to suggest launching the app. Workout recording specifics live in the `healthkit` skill.

## Widget push updates via APNs (watchOS 26)

Widgets can now receive dedicated APNs push updates on all WidgetKit-supporting platforms, including watch. Use pushes when data changes unpredictably (score change, incoming message) — the Watch Connectivity complication-transfer budget (50/day, see `watch-connectivity.md`) is the wrong tool for high-frequency updates.

## Previewing relevant widgets

Three preview levels for different stages of development:

```swift
// Layout only, across sizes
#Preview("Entries") { BeachEventWidget() } relevanceEntries: {
    BeachEventRelevanceEntry.previewShorebirds
    BeachEventRelevanceEntry.previewMeditation
}

// Provider + relevance — verify entry generation
#Preview("Provider and Relevance") { BeachEventWidget() } relevanceProvider: {
    BeachEventRelevanceProvider(store: .preview)
} relevance: {
    let configurations: [BeachEventConfigurationIntent] = [.previewSurfing, .previewMeditation, .previewWalk]
    return WidgetRelevance(configurations.map {
        WidgetRelevanceAttribute(configuration: $0, context: .date($0.event.startDate, kind: .default))
    })
}

// Full provider — final pass
#Preview("Provider") { BeachEventWidget() } relevanceProvider: {
    BeachEventRelevanceProvider(store: .preview)
}
```

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| Force-unwrapping `RelevantContext.location(category:)` | Crash on devices where the category is unsupported | `guard let` and return an empty `WidgetRelevance` |
| Shipping a WidgetKit complication while other complications still rely on ClockKit callbacks | Remaining ClockKit complications silently stop updating | Migrate every complication in the same release — see `modernization.md` |
| Missing `associatedKind(_:)` on a relevant widget that overlaps a timeline widget | Duplicate cards for the same event | Call `.associatedKind(timelineWidgetKind)` |
| Returning non-empty recommendations when the widget is meant to be watchOS-26-configurable | Users see preconfigured options instead of the configuration UI | Wrap in `if #available(watchOS 26, *)` and return `[]` |
| Using `transferCurrentComplicationUserInfo` as a high-frequency refresh path | Silent throttling past the 50/day budget | Reserve it for user-visible-change moments; use APNs widget push for frequency |
| `accessoryCorner` on a non-Infograph face | No card appears; budget wasted | It's Infograph-only — offer the other accessory families too |
| Skipping `context.isPreview` in a relevant widget's `entry(configuration:context:)` | Preview sheet shows placeholder or live user data instead of a preview entry | Branch explicitly on `context.isPreview` |
| Empty or stale `relevance()` attributes | Widget never appears in the Smart Stack even when it should | Populate an attribute for every event that should surface, not just the next one |
| Skipping `HKWorkoutRouteBuilder` for outdoor workouts | App isn't suggested for the user's routine | Attach route data on runs, walks, cycling |
