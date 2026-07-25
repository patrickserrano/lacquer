# Window Management

## Scene types

Pick the right one first — getting it wrong costs a real refactor later.

| Scene | Instances | Use for | Menu integration |
|---|---|---|---|
| `WindowGroup` | Multiple | Primary app content, data-driven detail windows | File > New Window |
| `Window` | Single | Singleton auxiliary window (activity monitor, console) | Window menu item |
| `UtilityWindow` (macOS 15+) | Single | Floating panel (inspector, formatter) — stays above main windows | View menu toggle |
| `MenuBarExtra` | Single | Menu bar utility, standalone or companion | System menu bar |
| `Settings` | Single | App preferences | App menu > Settings (auto) |
| `DocumentGroup` | Multiple | Document-based apps with file I/O | File > New/Open |

Only `WindowGroup`, `Window`, `UtilityWindow`, `MenuBarExtra`, and `Settings`
exist on macOS as listed; none of `Window`/`UtilityWindow`/`MenuBarExtra`/
`Settings` exist on iOS/iPadOS.

## Pitfalls that cost the most time

- **`WindowGroup` for a window that should be a singleton** — allows Cmd+N
  duplicates of state that isn't per-window. Use `Window`; it gets a Window-menu
  item to show/focus the existing instance instead of creating a new one.
- **`Window` as the app's *primary* scene** — the app quits when that window
  closes. Use `WindowGroup` for the primary scene.
- **Passing a struct as an `openWindow(value:)` presentation value** — value
  types get copied, so edits in the new window don't reflect back, and the
  value is persisted for state restoration (large objects slow launch). Pass
  an identifier (`item.id`) and resolve from your model store. Presentation
  values must conform to both `Hashable` and `Codable`.
- **Missing `.commandsRemoved()` on a data-driven `WindowGroup`** — every
  `WindowGroup` adds a "New [Title] Window" item to the File menu by default,
  even for windows meant to open only programmatically.
- **Expecting `.defaultSize` to always apply** — it only applies when no
  saved window state exists. Once a user resizes, SwiftUI persists their
  choice and ignores `defaultSize` on later launches. This is correct
  behavior, not a bug to work around.

## Opening and dismissing

```swift
@Environment(\.openWindow) private var openWindow
openWindow(id: "activity")            // by scene id
openWindow(value: book.id)            // data-driven; focuses an existing window for this value, else creates one

@Environment(\.dismissWindow) private var dismissWindow
dismissWindow(id: "auxiliary")

@Environment(\.dismiss) private var dismiss   // current window
```

Data-driven form needs a matching `WindowGroup(for:)`:

```swift
WindowGroup("Book Details", for: Book.ID.self) { $bookId in
    BookDetail(id: $bookId)
}
.commandsRemoved()   // programmatic-only, no File-menu entry
```

## Sizing and placement

```swift
.defaultSize(width: 400, height: 800)     // first launch / no saved state only
.defaultPosition(.topTrailing)            // screen-relative, locale-aware leading/trailing
.defaultWindowPlacement { content, context in
    let bounds = context.defaultDisplay.visibleRect
    let size = content.sizeThatFits(.unspecified)
    return WindowPlacement(
        CGPoint(x: bounds.midX - size.width / 2, y: bounds.maxY - size.height - 20),
        size: size)
}
.windowResizability(.contentSize)   // .automatic | .contentSize | .contentMinSize
```

`.contentSize` constrains window size to the content's min/max frame
(`.frame(minWidth:maxWidth:minHeight:maxHeight:)` on the root view).

## Window and toolbar style

```swift
.windowStyle(.hiddenTitleBar)       // .automatic | .hiddenTitleBar | .titleBar
.windowToolbarStyle(.unified)       // .automatic | .expanded | .unified | .unified(showsTitle:false) | .unifiedCompact
```

## Patterns

**Auxiliary singleton window:**

```swift
Window("Activity", id: "activity") { ActivityView() }
    #if os(macOS)
    .defaultPosition(.topTrailing)
    .defaultSize(width: 400, height: 600)
    .keyboardShortcut("0", modifiers: [.option, .command])
    #endif
```

**MenuBarExtra — standalone utility.** Set `LSUIElement = true` in Info.plist
to hide the Dock icon for menu-bar-only apps:

```swift
MenuBarExtra("Status", systemImage: "gauge.medium") { StatusMenu() }
```

For richer content than a simple menu, `.menuBarExtraStyle(.window)`.

**UtilityWindow (macOS 15+)** automatically floats above main windows,
receives `FocusedValues` from the focused main scene, adds a View-menu
show/hide toggle, hides when the app loses focus, and dismisses on Escape.
Use `.commandsRemoved()` + `WindowVisibilityToggle` for custom menu placement.

**DocumentGroup** gives a document app its whole shell for free: File >
New/Open/Save/Save As/Revert, the title-bar document menu (rename, move,
duplicate), window tabs, and per-document state restoration.

```swift
DocumentGroup(newDocument: ScriptDocument()) { configuration in
    EditorView(document: configuration.$document)
}
```

- Don't set frame autosave names on document windows — AppKit/SwiftUI manage
  per-document restoration.
- `DocumentGroupLaunchScene` is iOS/iPadOS/visionOS only — on the Mac,
  documents launch through the standard open panel and File menu; there's no
  launch scene to customize.

**Settings with tabs** (full pattern in `settings.md`):

```swift
#if os(macOS)
Settings {
    TabView {
        GeneralSettings().tabItem { Label("General", systemImage: "gear") }
        AccountSettings().tabItem { Label("Account", systemImage: "person") }
    }
    .scenePadding()
    .frame(width: 450, height: 300)
}
#endif
```

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| `WindowGroup` for a singleton window | Duplicate windows via Cmd+N | Use `Window` |
| Missing `.commandsRemoved()` | Unwanted File menu item | Add it to programmatic-only `WindowGroup`s |
| Struct passed to `openWindow(value:)` | Edits don't sync between windows | Pass an ID, resolve from the model store |
| Presentation value not `Codable` | Compile error, or no state restoration | Conform to both `Hashable` and `Codable` |
| Assuming `defaultSize` applies after resize | It doesn't — expected | Not a bug |
| `Window` used as primary scene | App quits when closed | Use `WindowGroup` |
| MenuBarExtra not visible | Missing `LSUIElement`, or wrong style | Check Info.plist; `.menuBarExtraStyle(.window)` for rich content |
| Plain `Window` used for a floating panel | Doesn't float above main windows | Use `UtilityWindow` (macOS 15+) |
