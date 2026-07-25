# Design for watchOS

What's genuinely different about SwiftUI on watchOS — navigation primitives, toolbar placement, full-color backgrounds, and Always On. General SwiftUI state/layout/animation is covered by `swiftui-expert-skill` and `swiftui-liquid-glass`; this file only covers the watch-specific delta.

## Core principle

Design for two seconds of attention, vertical scrolling by default, fewest taps to the destination. Anything that needs a third tap probably belongs on iPhone instead.

## Pick the right navigation primitive

| Primitive | Use when | Avoid when |
|---|---|---|
| `TabView(.verticalPage)` | 2–5 peer views, mostly single-screen, Digital Crown is the primary navigation input | More than 5 peers; a real hierarchy |
| `NavigationSplitView` | Source list → detail (weather locations, chat threads, world clock) | One-off detail with no natural list pivot |
| `NavigationStack` | Arbitrary drill-down hierarchy, or more than 5 tabs | Flat peer views (`TabView` is simpler and correct there) |

### `TabView` — vertical paging

```swift
@Binding var selected: Item

var body: some View {
    TabView(selection: $selected) {
        ForEach(Item.allCases) { item in
            Text("\(item.title) tab")
        }
    }
    .tabViewStyle(.verticalPage)
}
```

The page indicator draws beside the Digital Crown automatically. Mixing single-screen pages with one long `ScrollView` is fine as long as the long page is last — the dot expands to reflect scroll position inside it.

### `NavigationSplitView` — source + detail

Shows one column at a time on watch, like an iPhone in portrait:

```swift
@Binding var selected: Item?

var body: some View {
    NavigationSplitView {
        List(selection: $selected) {
            ForEach(Item.allCases, id: \.self) { item in
                NavigationLink(item.rawValue.uppercased(), value: item)
            }
        }
        .containerBackground(.green.gradient, for: .navigation)
        .listStyle(.carousel)
    } detail: {
        DetailView(selected: $selected)
    }
}
```

**A trap that catches everyone once:** `List` unwraps `Optional<Selection>` when matching `id`; `TabView` compares the raw value against each child's `tag` and does *not* unwrap. If a `NavigationSplitView`'s detail is itself a `TabView`, wrap the tag values in `Optional(item)` so the types line up — otherwise tab selection silently never activates.

### `NavigationStack` — hierarchy

```swift
@State var stack = [Int]()

var body: some View {
    NavigationStack(path: $stack) {
        Text("Main page")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    NavigationLink(value: 2) {
                        Image(systemName: "chevron.right")
                    }
                }
            }
            .navigationDestination(for: Int.self) { value in
                Text("Second page")
            }
    }
}
```

Keep the stack shallow. Put a large title on the root view only — a view reached via a back button shouldn't also carry a title.

## Toolbar placement

The toolbar is the only chrome that survives scrolling:

| Placement | Capacity | Behavior |
|---|---|---|
| `.topBarLeading` | 1 button | The system may auto-insert a back/list icon here; only place your own when that slot is free |
| `.topBarTrailing` | 1 button | Most common single-action placement |
| `.bottomBar` | Up to 3 buttons | Make the center button prominent with `.controlSize(.large)` plus a capsule tint |
| `.primaryAction` | 1 button | Inline in a scrolling view; hidden until the user scrolls up, then free to rediscover |

```swift
.toolbar {
    ToolbarItem(placement: .topBarTrailing) {
        Button { /* action */ } label: { Image(systemName: "suit.club") }
    }
    ToolbarItemGroup(placement: .bottomBar) {
        Button { /* action */ } label: { Image(systemName: "suit.diamond") }
        Button { /* action */ } label: { Image(systemName: "star") }
            .controlSize(.large)
            .background(.red, in: Capsule())
        Button { /* action */ } label: { Image(systemName: "suit.spade") }
    }
}
```

## Full-color backgrounds

`.containerBackground(_:for:)` paints color behind the navigation bar, toolbar, and safe area — plain `.background(_:)` stops at the content bounds and leaves the chrome system-default. Views inside `NavigationStack`, `NavigationSplitView`, or `TabView` need the container variant because the container itself owns the chrome:

```swift
.containerBackground(.blue.gradient, for: .tabView)
.containerBackground(.green.gradient, for: .navigation)
```

Use color to carry information, not just decoration — branding on the first frame, emotional tone (calm blue for sleep, urgent orange for a finished timer), or state transitions (a timer flipping black → orange on completion).

## Matched geometry between pages

Pin `isSource` to whichever page is currently visible so a shared element (an icon, a hero image) flows between tabs instead of cross-fading:

```swift
NavigationStack {
    TabView(selection: $pageNumber) {
        VStack {
            Image(systemName: "books.vertical.fill")
                .matchedGeometryEffect(id: bookIcon, in: library, properties: .frame, isSource: pageNumber == 0)
            Text("Books")
        }
        .tag(0)

        VStack { BookList() }.tag(1)
    }
    .tabViewStyle(.verticalPage)
    .toolbar {
        ToolbarItem(placement: .topBarLeading) {
            Image(systemName: "books.vertical.fill")
                .matchedGeometryEffect(id: bookIcon, in: library, properties: .frame, isSource: pageNumber != 0)
        }
    }
}
```

## Materials and text hierarchy

Lean on system materials instead of hand-rolled blur/transparency: the system auto-adds vibrant fill behind buttons and list cells, sheets/full-screen covers get a thin material automatically, and the nav bar gets its own blur. Use `.foregroundStyle(.primary/.secondary/.tertiary/.quaternary)` for a four-step legibility hierarchy that holds over any background, and `.buttonStyle(.borderedProminent)` for prominent buttons — it's tuned correctly for Liquid Glass.

Apps built for watchOS 10+ pick up the watchOS 26 Liquid Glass toolbar/control refresh automatically; custom styles need an audit for legibility (see `modernization.md`).

## Always On — design for two brightness levels

Always On is on by default for apps compiled on watchOS 8+. The display dims, update cadence drops, and controls stay tappable so a tap wakes the app.

| State | Display behavior | Who controls timing |
|---|---|---|
| Active (interacting) | Full brightness, full update rate | User |
| Frontmost-inactive (wrist down) | Dimmed, reduced default cadence | User, via Settings → Wake Screen |
| Background with a session (workout, audio) | Dimmed, reduced frequency, app keeps running | App, while the session is active |
| Background suspended | Screen off unless another app is frontmost | — |

Respond to the two environment values that matter:

```swift
@Environment(\.isLuminanceReduced) private var isLuminanceReduced
@Environment(\.redactionReasons) private var redactionReasons

Text("Hello!")
    .opacity(isLuminanceReduced ? 0.5 : 1.0)

if !redactionReasons.contains(.privacy) {
    Text("Balance: \(balance)")
}
```

Auto-blur sensitive fields with `.privacySensitive()` — it activates whenever `redactionReasons` contains `.privacy`:

```swift
Text("Account Number:")
Text(accountNumber)
    .privacySensitive()
```

Always blur balances, account numbers, health readings. It's fine to keep showing content that's only mildly sensitive (messages, appointments) — users can disable Always On per app.

Match update cadence with `TimelineView`, or gate manually on `scenePhase`:

```swift
TimelineView(PeriodicTimelineSchedule(from: Date(), by: 1.0/60.0)) { context in
    switch context.cadence {
    case .live:     // up to 60 updates/sec
    case .seconds:  // ~1 update/sec
    case .minutes:  // ~1 update/min
    @unknown default: fatalError()
    }
}
```

```swift
@Environment(\.scenePhase) private var scenePhase

var body: some View {
    if scenePhase == .active {
        // animations, subsecond updates
    } else {
        // low-frequency representation — static icon, nearest-second time
    }
}
```

Preview both states — Xcode previews don't auto-dim, so simulate manually:

```swift
#Preview("Active") { ContentView() }

#Preview("Always On") {
    ContentView()
        .environment(\.isLuminanceReduced, true)
        .environment(\.redactionReasons, [.privacy])
}
```

Only set `WKSupportsAlwaysOnDisplay = false` in the Watch target's Info.plist when there's a legal/contractual reason to never render in the dimmed state — Apple Watch SE and Series 4 and earlier never show Always On regardless, so the flag is rarely the right call.

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| Using `NavigationView` in new code | Deprecation warnings, broken path binding, missing toolbar placements | Use `NavigationStack` with `path: Binding<[T]>` |
| More than 5 tabs in `TabView(.verticalPage)` | Users lose orientation; page dots become unreadable | Switch to `NavigationStack` or `NavigationSplitView` |
| Title on the detail view of a `NavigationSplitView` | Back button crowds the layout; detail feels boxed in | Omit the title — the detail should read at a glance |
| `.background(_:)` inside a `NavigationStack` | Color stops at content bounds; nav bar stays system-default | Use `.containerBackground(_:for: .navigation)` |
| `TabView` and `List` bound to the same selection without `Optional` wrapping | Tabs never activate | Wrap tags with `Optional(item)` to match `List`'s unwrapping behavior |
| Subsecond animations left running during Always On | Battery drain | Gate on `scenePhase == .active` or `TimelineView` cadence `.live` |
| Raw balances/health readings visible during Always On | Privacy exposure to a casual glance | `.privacySensitive()` or gate on `redactionReasons.contains(.privacy)` |
| Hand-rolled blur behind a custom nav bar | Looks fine on watchOS 10, breaks under Liquid Glass on watchOS 26 | Use the system toolbar and let it provide the blur |
