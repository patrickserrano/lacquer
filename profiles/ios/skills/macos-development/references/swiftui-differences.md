# macOS SwiftUI Differences

macOS is not "iPhone on a bigger screen." Three structural differences drive
almost every macOS-specific decision below:

1. **Multi-window.** Users run several windows simultaneously, each with
   independent state. Use `@SceneStorage` for per-window state (sidebar
   expansion, column visibility, selection) — a global singleton object means
   selecting something in one window changes another.
2. **Focus-driven.** The focused window determines which menu commands apply.
   Commands target the front-most window via `focusedSceneValue`, not a
   global model — see `menus-and-commands.md`.
3. **Keyboard-first.** Every action needs a menu-bar path with a shortcut; a
   toolbar-only button is unreachable for keyboard-only users. The toolbar is
   a convenience subset of what the menu bar already offers, never the only
   path to an action.

Skipping any of these three is what makes an app feel like a ported iPad app.

## Table vs List

Use `Table` when there are 2+ sortable text/numeric columns and users benefit
from sorting, resizing, or reordering columns. Use `List` for visual content
(images, complex cells) or single-column data — `List` has no column headers,
sort, or per-column resize.

```swift
struct PlantTable: View {
    @State private var plants: [Plant]
    @State private var selection: Set<Plant.ID> = []
    @State private var sortOrder: [KeyPathComparator<Plant>] = [.init(\.name, order: .forward)]

    var body: some View {
        Table(plants, selection: $selection, sortOrder: $sortOrder) {
            TableColumn("Name", value: \.name)
            TableColumn("Days to Maturity", value: \.daysToMaturity) { Text($0.daysToMaturity, format: .number) }
            TableColumn("Date Planted") { Text($0.datePlanted, format: .date) }
        }
        .onChange(of: sortOrder) { _, newOrder in plants.sort(using: newOrder) }
    }
}
```

`Table` renders the sort indicator in the header automatically, but **you**
must sort the data in `onChange(of: sortOrder)` — nothing does it for you.

**Column customization** persists order/visibility per window:

```swift
@SceneStorage("plantTableColumns") private var columnCustomization: TableColumnCustomization<Plant>

Table(plants, selection: $selection, columnCustomization: $columnCustomization) {
    TableColumn("Name", value: \.name).customizationID("name")
    // every column needs a stable, unique .customizationID or customization silently no-ops
}
```

**Hierarchical rows**: `DisclosureTableRow` for parent-child data, nested
with disclosure triangles like Finder's list view.

**Platform behavior**: macOS and iPadOS-regular show all columns with
resizable headers; iPadOS-compact/iPhone collapse to the first column only,
headers hidden — design the first column to be meaningful on its own.

**Styling**: `.tableStyle(.bordered)` (macOS only, visible borders),
`.tableColumnHeaders(.hidden)` for small data sets.

## NavigationSplitView

On macOS this renders as a true multi-column layout with resizable dividers,
all columns visible simultaneously (unlike the drill-down behavior common on
iPhone).

```swift
NavigationSplitView {
    List(gardens, selection: $selectedGarden) { Label($0.name, systemImage: "leaf") }
        .navigationSplitViewColumnWidth(min: 180, ideal: 220, max: 300)
} content: {
    if let garden = selectedGarden { PlantTable(garden: garden) }
    else { ContentUnavailableView("Select a Garden", systemImage: "leaf") }
} detail: {
    if let plant = selectedPlant { PlantDetailView(plant: plant) }
    else { ContentUnavailableView("Select a Plant", systemImage: "leaf.circle") }
}
```

`columnVisibility` options: `.all`, `.doubleColumn` (content+detail),
`.detailOnly`, `.automatic`. Note macOS always shows the content column
regardless of the visibility setting. Sidebar toggles via View menu once
`SidebarCommands()` is added; each window's column visibility persists
independently via `@SceneStorage`.

## Inspector

The macOS-native way to show detail for the current selection: a trailing
column alongside content, non-modal — not a sheet, not a separate window.

| Need | Use |
|---|---|
| Editable detail for current selection | Inspector |
| One-time confirmation/input | Sheet (alert/dialog) |
| Brief contextual info | Popover |
| Separate editing window | `Window` via `openWindow` |

```swift
ContentView(selection: $selectedItem)
    .inspector(isPresented: $inspectorPresented) {
        if let item = selectedItem { ItemInspector(item: item) }
        else { Text("No Selection").foregroundStyle(.secondary) }
    }
    .inspectorColumnWidth(min: 200, ideal: 280, max: 400)
```

Add `InspectorCommands()` alongside `SidebarCommands()` in `.commands` or
there's no keyboard shortcut (Control-Command-I) or menu item to toggle it.

Sheets are modal and block the main content — using one for selection detail
that macOS users expect to edit alongside the list is the most common
sheet-vs-Inspector mistake ported from iOS.

## Focus and keyboard

```swift
.onDeleteCommand { deleteSelectedItems() }              // Delete key, when focused
.onExitCommand { clearSelection() }                      // Escape, when focused
.onCommand(#selector(NSResponder.selectAll(_:))) { selectAllItems() }
```

These only fire when the view (or a descendant) has focus — matches AppKit's
responder chain. `Table(items, selection:) { }.focusable()` is required for
the table to receive keyboard focus at all; without it, `onDeleteCommand` and
friends never fire.

For routing values from the focused window to the menu bar
(`focusedSceneValue`/`@FocusedValue`/`@FocusedBinding`), see
`menus-and-commands.md` — the mechanism is identical whether the consumer is
a `Commands` struct or a focus-dependent view modifier.

## Toolbars

```swift
WindowGroup { ContentView() }.windowToolbarStyle(.unified)          // scene-level, preferred
NavigationSplitView { }.presentedWindowToolbarStyle(.unifiedCompact) // view-level
```

| Style | Appearance | When |
|---|---|---|
| `.unified` | Title and toolbar merged | Most apps (default) |
| `.unifiedCompact` | Merged, reduced height | Utility windows/panels |
| `.expanded` | Toolbar below title bar | Apps with many toolbar items |

```swift
.toolbar {
    ToolbarItem(placement: .navigation) { Button("Back", systemImage: "chevron.left") { goBack() } }
    ToolbarItem(placement: .primaryAction) { Button("Add", systemImage: "plus") { addItem() } }
    ToolbarItem(placement: .secondaryAction) { Button("Filter", systemImage: "line.3.horizontal.decrease") { toggleFilter() } }
}
```

Every toolbar action needs a menu-bar equivalent (`ToolbarCommands()` plus
your own `CommandMenu`/`CommandGroup`) — the menu bar is the canonical,
keyboard-reachable location for every action; the toolbar is a shortcut to a
subset of it.

## Known SwiftUI-on-macOS gaps (AppKit escape hatches)

These are durable as of the current SDK — SwiftUI on macOS still can't
express several interactions Mac users expect. Knowing where the wall is
saves time fighting the framework instead of bridging past it (see
`appkit-interop.md` for the bridging patterns themselves).

| Gap | Why | Workaround |
|---|---|---|
| Selected-but-not-focused row dimming | `List` styles selection while focused but exposes no hook for the *unfocused* window state (only `List` inherits this from `NSTableView`; nothing outside `List` gets a hook at all) | Build a custom list (`ScrollView` + `LazyVStack`, `.focusable()`), bridge an `NSViewRepresentable` observing `didBecomeKeyNotification`/`didResignKeyNotification` or `NSApp.isActive` to drive the emphasized-vs-unemphasized fill |
| Context-menu-open row highlight | Only `List` inherits the `NSTableView` right-click highlight; impossible in a custom container in pure SwiftUI | Use `List` when this matters, or wrap `NSMenu` + `NSTableView` via `NSViewRepresentable` |
| Drag-session visibility for the drag *source* | SwiftUI gives no visibility into a live drag session unless you're also the drop target, across all three DnD generations (`onDrag`/`onDrop` → `Transferable` → `DropSession`) | Bridge the draggable view via `NSViewRepresentable` to observe `NSDraggingSource` session begin/move/end |
| Arrow-key list navigation on iPadOS | `.onMoveCommand` is `@available(iOS, unavailable)` — doesn't compile on iOS/iPadOS at all, even with a hardware keyboard | `#if os(macOS)` around `.onMoveCommand`; separate iPad-specific path |
| Search field + arrow-key list navigation together | A focused SwiftUI `TextField` consumes arrow keys, so simultaneous field-typing + list navigation (a decade-old AppKit pattern) isn't possible in pure SwiftUI | Wrap `NSSearchField`/`NSTextField` via `NSViewRepresentable`, forward `moveUp:`/`moveDown:` to the list's selection |
| Toolbar item placement precision | `.primaryAction`/`.secondaryAction`/`.navigation` behave inconsistently across platforms, especially assembled across a multi-pane view | Keep toolbar construction in one place; drop to `NSToolbar`/`NSToolbarDelegate` for exact ordering/overflow control |

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| `focusedValue` instead of `focusedSceneValue` for menu commands | Menu actions only work when one specific field has focus | Use `focusedSceneValue` |
| Not sorting in `onChange(of: sortOrder)` | Header shows sort state, rows don't reorder | Sort the data yourself |
| Missing `.customizationID` on Table columns | Column customization silently does nothing | Unique stable ID per column |
| `NavigationStack` where `NavigationSplitView` fits | Feels like an iPad app, drill-in instead of columns | Use `NavigationSplitView` for sidebar-content-detail |
| No `@SceneStorage` for per-window state | Second window duplicates the first window's state | Use `@SceneStorage`, not a global object |
| Table's first column not meaningful alone | Cryptic single-column layout on compact widths | Design the first column as the primary identifier |
