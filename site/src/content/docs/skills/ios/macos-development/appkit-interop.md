---
title: "AppKit / SwiftUI Interoperability"
description: "Reference for the macos-development skill."
---

:::note
Generated from [`profiles/ios/skills/macos-development/references/appkit-interop.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-development/references/appkit-interop.md) — edit that file, not this page.
:::

# AppKit / SwiftUI Interoperability

## Direction first

Which framework is the host, and which is the guest — the bridging type
differs by direction, not just by which API you need.

```
SwiftUI hosts an AppKit view
  single NSView (text editor, custom control, map)      → NSViewRepresentable
  NSViewController with lifecycle (editor, media player) → NSViewControllerRepresentable

AppKit hosts SwiftUI
  needs a view controller (split item, sheet, popover)   → NSHostingController
  needs a raw view (collection cell, sidebar, subview)    → NSHostingView
```

Start with SwiftUI; drop to AppKit only when SwiftUI genuinely lacks the
capability:

| Need | SwiftUI | AppKit required for |
|---|---|---|
| File picking | `.fileImporter` | Directory selection, accessory views, iCloud conflict resolution |
| Toolbar | `.toolbar` | Item validation, custom views, overflow behavior |
| Drag destination | `.onDrop`, `Transferable` | `NSDraggingDestination` for legacy pasteboard types |
| Text editing | `TextEditor` | `NSTextView` for rich text, custom input, TextKit 2 |
| Menu bar | `CommandMenu`/`CommandGroup` | Dynamic menus, `NSHostingMenu`, `validateMenuItem` |
| Responder commands | `onCommand`, `copyable` | Custom selectors outside SwiftUI's command set |

## Automatic observation in AppKit — no SwiftUI required

AppKit tracks `@Observable` properties accessed inside certain methods and
redraws automatically — adopt this before hosting any SwiftUI, it replaces
manual `needsDisplay = true` fan-out:

```swift
@Observable @MainActor
final class ColorModel { var hue: Double = 0.6; var saturation = 1.0; var brightness = 1.0 }

class HueSliderCell: NSSliderCell {
    var model: ColorModel!
    override func drawKnob(_ knobRect: NSRect) {
        // AppKit tracks every @Observable property read here and redraws on change
        let color = NSColor(hue: model.hue, saturation: model.saturation, brightness: model.brightness, alpha: 1)
    }
}
```

Tracking methods: anything called as part of `NSView.draw(_:)` (including
`NSCell` draw methods like `drawKnob`/`drawBar`), plus `updateConstraints()`,
`layout()`, `updateLayer()`, and the `NSViewController` equivalents. Outside
those methods, use `withObservationTracking(_:onChange:)` instead.

On by default for apps built against the 2026 SDKs+; back-deploy to macOS 15
with the `NSObservationTrackingEnabled` Info.plist key.

## SwiftUI hosting AppKit (NSViewRepresentable)

Lifecycle: `makeCoordinator()` (once) → `makeNSView(context:)` → `updateNSView(_:context:)`
(every SwiftUI state change) → `dismantleNSView(_:coordinator:)` (optional cleanup).

```swift
struct ScriptEditorRepresentable: NSViewRepresentable {
    @Binding var sourceCode: String

    func makeCoordinator() -> Coordinator { Coordinator(parent: self) }

    func makeNSView(context: Context) -> ScriptEditorView {
        let editor = ScriptEditorView(frame: .zero)
        editor.delegate = context.coordinator
        return editor
    }

    func updateNSView(_ editor: ScriptEditorView, context: Context) {
        if editor.sourceCode != sourceCode { editor.sourceCode = sourceCode }  // guard, don't blindly reassign
        editor.isEditable = context.environment.isEnabled
        context.coordinator.parent = self   // refresh every time — coordinator persists across calls
    }

    class Coordinator: NSObject, ScriptEditorViewDelegate {
        var parent: ScriptEditorRepresentable
        init(parent: ScriptEditorRepresentable) { self.parent = parent }
        func sourceCodeDidChange(in view: ScriptEditorView) { parent.sourceCode = view.sourceCode }
    }
}
```

Key rules:
- **Guard every property write in `updateNSView`.** It's called on every
  SwiftUI state change; blindly reassigning a text/value property (e.g.
  `textView.string = text`) resets the insertion point and selection — the
  caret jumps to the top mid-typing.
- **`NSTextView` needs its scroll view.** A bare `NSTextView` has no
  enclosing scroll view and won't scroll/resize. Build it with
  `NSTextView.scrollableTextView()` (returns the configured `NSScrollView`)
  and return *that* from `makeNSView`; reach the text view via
  `scrollView.documentView as? NSTextView`.
- **Refresh the coordinator's `parent` reference every `updateNSView` call**
  — it's created once and persists, so a stale reference means writes to
  SwiftUI bindings silently go nowhere.
- **Never set frame/bounds inside `updateNSView`** — SwiftUI owns layout
  through its own constraint system; use `.frame()` on the representable.
- **Create the hosting/represented view once**, never per cell-reuse in a
  collection/table view — rebuilding the hierarchy every reuse causes scroll
  jank; update `rootView`/properties instead.

## AppKit gestures in SwiftUI (NSGestureRecognizerRepresentable, macOS 26+)

Bring an existing `NSGestureRecognizer` subclass into SwiftUI instead of
rewriting it as a SwiftUI `Gesture`:

```swift
struct ForceClickReset: NSGestureRecognizerRepresentable {
    var model: ColorModel
    func makeNSGestureRecognizer(context: Context) -> ForceClickGestureRecognizer { ForceClickGestureRecognizer() }
    func handleNSGestureRecognizerAction(_ recognizer: ForceClickGestureRecognizer, context: Context) {
        withAnimation { model.saturation = 1; model.brightness = 1 }
    }
}

HSBColorPicker(model: model).gesture(ForceClickReset(model: model))   // composes with existing SwiftUI gestures
```

macOS-only.

## AppKit hosting SwiftUI

**`NSHostingController`** — view controller contexts (split view item,
sheet, popover, modal window):

```swift
let sidebar = NSHostingController(rootView: SidebarView(model: selectionModel))
splitViewController.addSplitViewItem(NSSplitViewItem(viewController: sidebar))

viewController.presentAsSheet(NSHostingController(rootView: SheetContent()))
viewController.present(NSHostingController(rootView: PopoverContent()),
                        asPopoverRelativeTo: rect, of: view, preferredEdge: .maxY, behavior: .transient)
```

`NSHostingController` derives Auto Layout constraints from the SwiftUI view's
ideal/min/max sizes; customize (or disable, for performance or when
surrounding AppKit already handles layout) with
`controller.sizingOptions = [.minSize, .intrinsicContentSize, .maxSize]`.

**`NSHostingView`** — raw view contexts (collection/table cells). Create
once, reuse:

```swift
class ShortcutItemView: NSCollectionViewItem {
    private var hostingView: NSHostingView<ShortcutView>?
    func displayShortcut(_ shortcut: Shortcut) {
        let view = ShortcutView(shortcut: shortcut)
        if let hostingView { hostingView.rootView = view }          // reuse — SwiftUI diffs internally
        else {
            let newHosting = NSHostingView(rootView: view)
            self.view.addSubview(newHosting); setupConstraints(for: newHosting)
            hostingView = newHosting
        }
    }
}
```

**Shared state**: an `@Observable` model both sides read. AppKit reads it
inside an observation-tracking method (see above) or uses
`withObservationTracking(_:onChange:)` outside one; SwiftUI binds directly
with `@Bindable`. `@Observable` has no Combine `$property` publisher — that's
`@Published`/`ObservableObject` — observation tracking replaces the `.sink`
dance entirely.

## Responder chain and focus

SwiftUI views hosted in AppKit participate in the **same** responder chain —
they don't live in a separate world. When an `NSHostingView` has focus, it's
the first responder; selectors from menu items travel through the chain like
any AppKit view, intercepted by SwiftUI modifiers:

```swift
ScrollView { ... }
    .focusable()
    .copyable([selectedItem])                          // @autoclosure — pass the array, not a trailing closure
    .cuttable { [selectedItem] }                        // returns items to cut; remove them from your model too
    .pasteDestination(for: String.self) { strings in paste(strings) }   // label is `for:`, not `payloadType:`
    .onMoveCommand { direction in moveSelection(direction) }
    .onExitCommand { cancelOperation() }
    .onCommand(#selector(NSResponder.selectAll(_:))) { selectAllItems() }
```

| Modifier | Selector |
|---|---|
| `.copyable` | `copy:` |
| `.cuttable` | `cut:` |
| `.pasteDestination` | `paste:` |
| `.onCommand(#selector(...))` | any custom/standard selector |
| `.onMoveCommand` | arrow keys |
| `.onExitCommand` | Escape |

`.focusable()` is required for command receivers — without it, `onCommand`
and friends are silently ignored. Test with System Settings > Keyboard > Full
Keyboard Navigation both on and off; some controls are only focusable when
that setting is on.

## SwiftUI in the main menu (NSHostingMenu, macOS 14.4+)

```swift
let colorMenu = NSHostingMenu(rootView: ColorMenu(model: colorModel))
colorMenu.title = "Color"          // NSHostingMenu IS an NSMenu — configure as usual
let item = NSMenuItem(); item.submenu = colorMenu
mainMenu.addItem(item)
```

`Button` actions, `withAnimation`, and `Picker(selection:)` bindings all work
as in any SwiftUI view. For pure-SwiftUI menu construction (no AppKit main
menu involved), see `menus-and-commands.md`.

## SwiftUI scenes from AppKit (NSHostingSceneRepresentation, macOS 26+)

Add complete SwiftUI scenes (`MenuBarExtra`, `Settings`) to an existing
`NSApplicationDelegate` app without rewriting its lifecycle:

```swift
func applicationWillFinishLaunching(_ notification: Notification) {
    let scenes = NSHostingSceneRepresentation {
        LightMenuBarExtra(appModel: model)
        LightSettings(appModel: model)
    }
    NSApplication.shared.addSceneRepresentation(scenes)
    openSettingsAction = { scenes.environment.openSettings() }   // call from an AppKit menu item's @IBAction
}
```

A SwiftUI `MenuBarExtra` also handles the keyboard-navigation/session
bookkeeping that a raw `NSStatusItem` custom window needs manually — see
`appkit-modernization.md`'s expanded interface sessions if bridging isn't an
option.

## NSToolbar

`.toolbar` covers most needs; drop to `NSToolbar` for item validation
(`validateToolbarItem`), user customization (`allowsUserCustomization`),
centered item groups, custom item views beyond SwiftUI toolbar content, or
overflow control:

```swift
func toolbar(_ toolbar: NSToolbar, itemForIdentifier identifier: NSToolbarItem.Identifier,
             willBeInsertedIntoToolbar: Bool) -> NSToolbarItem? {
    let item = NSToolbarItem(itemIdentifier: identifier)
    item.view = NSHostingView(rootView: MyToolbarButton())   // wrap SwiftUI content in NSHostingView items
    return item
}
```

## NSOpenPanel vs .fileImporter

`.fileImporter` handles sandbox entitlements automatically and covers most
picking needs; drop to `NSOpenPanel` for `canChooseDirectories`, accessory
views, `canResolveUbiquitousConflicts`/`canDownloadUbiquitousContents`, or
delegate-based filtering beyond `UTType`. Full sandbox/bookmark handling for
both paths is in `sandbox-and-file-access.md`.

## Drag and drop

Prefer `Transferable` + `.draggable()`/`.dropDestination()` when both sides
are SwiftUI or the type is standard. When an `NSViewRepresentable`-wrapped
view needs to accept drops, implement `NSDraggingDestination` on the AppKit
view/coordinator as usual — you already own its creation. When SwiftUI needs
to accept drops from AppKit using legacy pasteboard types `Transferable`
doesn't cover, use `.onDrop(of:delegate:)` with `NSItemProvider`.

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| New `NSHostingView` per cell reuse | Scroll jank, high memory | Create once, update `rootView` |
| Setting frame/bounds in `updateNSView` | Layout corruption | Use SwiftUI `.frame()` |
| Stale coordinator reference | Writes to SwiftUI state ignored | Refresh `context.coordinator.parent` every `updateNSView` |
| Wrapping a bare `NSTextView` | No scrolling; caret jumps to top while typing | Build via `NSTextView.scrollableTextView()`; guard `string` writes |
| Redundant property sets in `updateNSView` | Unnecessary AppKit reloads | Compare before setting |
| `NSOpenPanel` for basic picks | Unnecessary complexity, sandbox friction | Use `.fileImporter` first |
| Manual event forwarding (`keyDown` override calling into SwiftUI) | Duplicated/broken input | Let the responder chain do it |
| Missing `.focusable()` on a command receiver | `onCommand` silently ignored | Add `.focusable()` |
