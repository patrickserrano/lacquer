# The Settings Scene

`Settings` is a macOS-only `Scene` type (macOS 11+) for the standard
Preferences window — SwiftUI wires up the Settings menu item and ⌘, for you;
you only supply the content view. It does **not** exist on iOS/iPadOS/
watchOS/tvOS — always gate it:

```swift
@main
struct MyApp: App {
    var body: some Scene {
        WindowGroup { ContentView() }
        #if os(macOS)
        Settings { SettingsView() }
        #endif
    }
}
```

If `SettingsView` itself references macOS-only types (`Settings`,
`SettingsLink`), wrap the view declaration in `#if os(macOS)` too — otherwise
the non-conditional view declaration fails to compile on iOS even though the
scene reference was guarded.

## Single-pane vs tabbed

Single pane for a handful of ungrouped toggles:

```swift
struct SettingsView: View {
    @AppStorage("showPreviews") private var showPreviews = true
    var body: some View {
        Form { Toggle("Show previews on hover", isOn: $showPreviews) }
            .formStyle(.grouped)
            .scenePadding()
            .frame(width: 450, height: 180)   // fixed — HIG: prefs windows are typically not resizable
    }
}
```

Tabbed for anything past ~5 preferences:

```swift
struct SettingsView: View {
    var body: some View {
        TabView {
            GeneralSettings().tabItem { Label("General", systemImage: "gear") }
            AppearanceSettings().tabItem { Label("Appearance", systemImage: "paintbrush") }
            AdvancedSettings().tabItem { Label("Advanced", systemImage: "slider.horizontal.3") }
        }
        .scenePadding()
        .frame(width: 450, height: 280)   // on the TabView, not each tab body — locks one size across tabs
    }
}
```

Every `Tab` needs a `systemImage:` — the HIG mandates icons on preference
tabs; text-only tabs read as unfinished.

**Per-tab sizing** (only when tabs legitimately need different sizes — the
window then animates between sizes on tab switch):

```swift
TabView {
    GeneralSettings().frame(width: 450, height: 200).tabItem { Label("General", systemImage: "gear") }
    LongPreferenceList().frame(width: 450, height: 500).tabItem { Label("Library", systemImage: "books.vertical") }
}
.scenePadding()
```

## SettingsLink and openSettings

`SettingsLink` opens the app's **own** `Settings { }` scene from anywhere in
the UI (onboarding, error states, inline help):

```swift
SettingsLink { Label("Open Settings", systemImage: "gear") }
```

`SettingsLink()` with no closure produces a system-styled "Settings…" button.
It does not exist on iOS. For a plain button instead of link semantics, use
`@Environment(\.openSettings)`'s `openSettings()` action.

## iOS/macOS adjacency — do not conflate these

"Open settings" means two different things, and `SettingsLink` only covers one:

| Goal | iOS | macOS |
|---|---|---|
| Open the app's own preferences | No `Settings` scene — show in-app UI | `SettingsLink` / `openSettings` |
| Re-enable a denied system permission (camera, mic, location) | `UIApplication.openSettingsURLString` → System Settings | `x-apple.systempreferences:` deep link → System Settings |

```swift
// iOS: system Settings, scrolled to this app's section
Button("Open Settings") {
    if let url = URL(string: UIApplication.openSettingsURLString) { openURL(url) }
}

// macOS: deep-link System Settings to a specific privacy pane
// swap the anchor: Privacy_Camera, Privacy_Microphone, Privacy_LocationServices, ...
if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Camera") {
    NSWorkspace.shared.open(url)
}
```

For a permission-denial CTA specifically (not general preferences), branch to
the System Settings deep link on **both** platforms — `SettingsLink` is never
correct there, since it only opens the app's own preferences window.

## Persistence with @AppStorage

```swift
enum PreferenceKey {
    static let showPreviews = "showPreviews"
    static let theme = "theme"
}

@AppStorage(PreferenceKey.showPreviews) private var showPreviews = true
```

Centralize keys in one enum. `@AppStorage("showPreviews")` in one file and
`@AppStorage("showPreview")` (typo) in another silently splits state — the
Settings UI updates one key, the rest of the app reads a different one, and
nothing errors. For preferences shared with an extension (Widget, Share
Extension), use `@AppStorage(_:store:)` with `UserDefaults(suiteName:)` — the
App Group container setup itself is in `sandbox-and-file-access.md`.

## Code review checklist

- [ ] `Settings { }` wrapped in `#if os(macOS)`
- [ ] `SettingsView` itself wrapped in `#if os(macOS)` if it references macOS-only APIs
- [ ] Root view has both `.scenePadding()` and a `.frame()` constraint
- [ ] Every `Tab`/`tabItem` uses `Label(_:systemImage:)`, never text-only
- [ ] `@AppStorage` keys come from a central enum, not inline string literals
- [ ] iOS code that opens system Settings uses `UIApplication.openSettingsURLString`, not `SettingsLink`
- [ ] No business logic, network calls, or heavy state inside `SettingsView`

## Common mistakes

| Symptom | Cause |
|---|---|
| `'Settings' is unavailable` on iOS | Missing `#if os(macOS)` |
| Settings menu item not in app menu | No `Settings { }` scene declared |
| Window opens tiny | No `.frame()` on the root view |
| Window resizes oddly | Frame uses `min`/`max` instead of fixed values |
| Tabs render without icons | `Text` used instead of `Label` for `.tabItem` |
| `@AppStorage` doesn't sync to another view | Different key strings (typo) — use a central enum |
| `SettingsLink` causes an iOS build failure | It's macOS-only; needs `#if os(macOS)` |
