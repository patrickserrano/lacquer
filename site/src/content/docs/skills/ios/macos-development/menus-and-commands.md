---
title: "Menus and Commands"
description: "Reference for the macos-development skill."
---

:::note
Generated from [`profiles/ios/skills/macos-development/references/menus-and-commands.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-development/references/menus-and-commands.md) — edit that file, not this page.
:::

# Menus and Commands

## Mental model

The menu bar is a **routing system**, not a set of buttons: one shared menu
bar, potentially many windows, and commands have no direct reference to any
specific window's state. `focusedSceneValue` is the bridge — it exposes
values from whichever window is currently focused so the menu bar (and any
`Commands` struct) can read them. This is the single most common mistake
iOS-first developers make on macOS: reaching for `@State` from inside a
`Commands` struct, which has no window to read state from.

```
Menu Bar (one, shared) → Commands struct (reads focused values) → @FocusedValue
                                                                         │
                                                                 .focusedSceneValue
                                                                         │
                                                              Focused window (publishes state)
```

## Where commands live

`.commands` is a **scene**-level modifier — it goes on `WindowGroup`/`Window`
in the `App` body, never on a `View`. Putting `.commands { }` on a view is a
compile error (the modifier doesn't exist there).

```swift
@main
struct GardenApp: App {
    var body: some Scene {
        WindowGroup { ContentView() }
            .commands {
                SidebarCommands()   // system-provided
                PlantCommands()     // custom
            }
    }
}
```

## CommandMenu vs CommandGroup

**`CommandMenu`** creates a new top-level menu, positioned between View and
Window per the HIG. Use short, one-word titles.

```swift
struct PlantCommands: Commands {
    @FocusedBinding(\.garden) var garden
    @FocusedValue(\.selectedPlants) var selectedPlants

    var body: some Commands {
        CommandMenu("Plants") {
            Button("Water Selected") { garden?.water(selectedPlants ?? []) }
                .keyboardShortcut("w", modifiers: [.command, .shift])
                .disabled(selectedPlants?.isEmpty ?? true)
        }
    }
}
```

**`CommandGroup`** extends or replaces an existing system menu via placement:

```swift
CommandGroup(before: .newItem) {
    Button("New Plant") { garden?.addPlant() }
        .keyboardShortcut("n", modifiers: [.command])
}
CommandGroup(replacing: .undoRedo) { EmptyView() }   // e.g. app has no undo
```

| Placement | Menu | Use for |
|---|---|---|
| `.newItem` | File | New-item actions |
| `.saveItem` | File | Save |
| `.importExport` | File | Import/export |
| `.printItem` | File | Print |
| `.pasteboard` | Edit | Clipboard |
| `.undoRedo` | Edit | Undo/redo |
| `.textEditing` | Edit | Text manipulation |
| `.sidebar` | View | Sidebar visibility |
| `.toolbar` | View | Toolbar commands |
| `.windowList` | Window | Window management |
| `.help` | Help | Help content |
| `.appSettings` | App | Settings/preferences |

System-provided groups wire up standard functionality without custom code:
`SidebarCommands()`, `InspectorCommands()`, `ToolbarCommands()`,
`TextEditingCommands()`, `TextFormattingCommands()`.

Suppress a WindowGroup's automatic File-menu entry with `.commandsRemoved()`
on the scene (see `windows.md`).

For AppKit-built menus specifically, `NSMenuItem.preferredImageVisibility`
(`.automatic` / `.visible` / `.hidden`, macOS 27) declares whether an item's
image should show — `.automatic` lets the system override even `.visible`.

## Keyboard shortcuts

```swift
Button("Refresh") { refresh() }.keyboardShortcut("r", modifiers: [.command])
Button("Delete") { delete() }.keyboardShortcut(.delete, modifiers: [.command])
```

Support standard shortcuts for standard actions (⌘C, ⌘V, ⌘S). Only invent a
custom shortcut when there's no standard one — check first, since a custom
shortcut can silently shadow a system one.

## Context menus

Secondary click (Control-click / right-click). Per the HIG: a small number of
frequently used actions related to the clicked item, applied consistently
across all instances of that item type.

```swift
ArticleContent(article: article)
    .contextMenu {
        Button("Open in New Window") { openWindow(value: article.id) }
        Divider()
        Button("Delete", role: .destructive) { deleteArticle(article) }
    }
```

## Focus-based command routing

Two sides: **publish** from the view, **read** from the commands.

**1. Define the key** with `@Entry` (iOS 17+/macOS 14+):

```swift
extension FocusedValues {
    @Entry var document: Document?
    @Entry var garden: Binding<Garden>?   // Binding for read-write access
}
```

**2. Publish** — `.focusedSceneValue` exposes a value whenever *any part* of
that scene's window has focus, not just the specific publishing view:

```swift
Table(garden.plants, selection: $selection) { ... }
    .focusedSceneValue(\.garden, $garden)
    .focusedSceneValue(\.selectedItems, selection)
```

**3. Read**:

```swift
struct GardenCommands: Commands {
    @FocusedBinding(\.garden) var garden          // read-write
    @FocusedValue(\.selectedItems) var selection  // read-only

    var body: some Commands {
        CommandMenu("Garden") {
            Button("Water Selected") { garden?.water(selection ?? []) }
                .disabled(selection?.isEmpty ?? true)
        }
    }
}
```

| Modifier | Scope | Use when |
|---|---|---|
| `.focusedSceneValue` | Entire window/scene | Value represents window-level state (document, selection) — **default to this** |
| `.focusedValue` | Individual focused view | Value is only meaningful when one specific view has keyboard focus (e.g. text field state) |

`focusedValue` publishing breaks the moment the user clicks a toolbar button
or sidebar item outside that view — the value goes `nil` and disables the
menu item. That's almost never what a window-level command wants.

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| `.commands` on a `View` | Compile error | Move to scene level (`WindowGroup`/`Window`) |
| No `.focusedSceneValue` published | Menu items permanently disabled | Publish the value from a view in the focused scene |
| `focusedValue` used for window-level state | Menu disables when clicking toolbar/sidebar | Switch to `focusedSceneValue` |
| `@FocusedValue` with no matching `@Entry` | Compile error or always-nil value | Define the key in `extension FocusedValues` |
| Custom shortcut shadows a system one | System shortcut silently overridden | Check standard shortcuts before assigning custom ones |
| Menu title too long | Truncated, cluttered menu bar | Short, one-word `CommandMenu` titles |
| Hiding unavailable items instead of disabling | Users can't discover what's possible | Disable, don't hide, per HIG |
