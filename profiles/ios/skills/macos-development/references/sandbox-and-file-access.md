# App Sandbox and File Access

## The #1 cause of "works on my machine"

**Xcode debug builds do not sandbox your app by default.** `FileManager`
calls succeed on any path, network works without entitlements, hardware
access works without prompts — none of that proves anything about a release
build. Verify sandboxing is actually active before trusting a test:

1. **Activity Monitor**: View > Columns > Sandbox; your app should show "Yes".
2. **Enable in debug**: Signing & Capabilities > add "App Sandbox" capability
   (adds `com.apple.security.app-sandbox`).
3. **Test the release/archived build** — it's sandboxed even if debug isn't.
4. **TestFlight builds are always sandboxed** — best pre-release validation.

The sandbox is kernel-level access control: your app's container
(`~/Library/Containers/<bundle-id>/`), files the user explicitly grants
(open/save panels, drag-and-drop), and whatever entitlements declare. No
error handling works around a denial — the operation just fails. Hardcoded
paths (`/Users/name/Library/...`) point outside the container and get denied;
always resolve paths via `FileManager`.

## File access decision tree

```
File inside your app's container?           → read/write directly
User picks a file interactively?
  SwiftUI                                    → .fileImporter / .fileExporter
  AppKit                                     → NSOpenPanel / NSSavePanel
Need the same file again next launch?        → security-scoped bookmark (below)
Need Downloads/Pictures/Music/Movies?        → the specific folder entitlement
Need to share files between your own apps?   → App Group container
Need arbitrary file access?                  → user must grant Full Disk Access
                                                 in System Settings (not requestable in code)
```

```swift
// SwiftUI
.fileImporter(isPresented: $showImporter, allowedContentTypes: [.plainText, .pdf]) { result in
    switch result {
    case .success(let urls):
        guard let url = urls.first else { return }
        defer { url.stopAccessingSecurityScopedResource() }  // access already started
        let data = try? Data(contentsOf: url)
        saveBookmark(for: url)   // if needed later — bookmark it NOW, before access ends
    case .failure(let error):
        logger.error("File import failed: \(error.localizedDescription)")
    }
}

// AppKit
let panel = NSOpenPanel()
panel.allowedContentTypes = [.plainText]
panel.begin { response in
    guard response == .OK, let url = panel.url else { return }
    defer { url.stopAccessingSecurityScopedResource() }
    let data = try? Data(contentsOf: url)
}
```

URLs from open/save panels, `.fileImporter`/`.fileExporter`, and Dock
drag-and-drop all arrive with security-scoped access **already started** —
call `stopAccessingSecurityScopedResource()` when done, but never
`startAccessing`. `.fileImporter` URLs are temporary: to use a file later or
after relaunch, create the bookmark inside the completion handler, before
access ends — saving the raw URL for later use fails silently once scope ends.

## Security-scoped bookmarks

The pattern developers get wrong most often. Requires the bookmark
entitlement — `com.apple.security.files.bookmarks.app-scope` for app-scoped
bookmarks, `com.apple.security.files.bookmarks.document-scope` for
document-relative ones. If `bookmarkData(options: .withSecurityScope)`
throws, or a resolved bookmark's `startAccessingSecurityScopedResource()`
always returns `false` despite valid data, check this entitlement before
debugging further (recent macOS is sometimes lenient without it, but Apple
still documents it as required).

```swift
// 1. Create — immediately, while you have access (e.g. inside the fileImporter/panel callback)
func saveBookmark(for url: URL) {
    do {
        let bookmarkData = try url.bookmarkData(options: .withSecurityScope,
                                                 includingResourceValuesForKeys: nil, relativeTo: nil)
        try bookmarkData.write(to: bookmarkStorageURL)   // a file in your container, NOT UserDefaults
    } catch { logger.error("Failed to create bookmark for \(url.path): \(error)") }
}
// read-only future access: add .securityScopeAllowOnlyReadAccess to the options set

// 2. Resolve — on next launch
func resolveBookmark() -> URL? {
    guard let data = try? Data(contentsOf: bookmarkStorageURL) else { return nil }
    var isStale = false
    guard let url = try? URL(resolvingBookmarkData: data, options: .withSecurityScope,
                              relativeTo: nil, bookmarkDataIsStale: &isStale) else { return nil }
    if isStale { saveBookmark(for: url) }   // refresh immediately — don't defer this
    return url
}

// 3. Access — resolved bookmarks do NOT auto-start access; you must start AND stop
func readBookmarkedFile() -> Data? {
    guard let url = resolveBookmark(),
          url.startAccessingSecurityScopedResource() else { return nil }
    defer { url.stopAccessingSecurityScopedResource() }
    return try? Data(contentsOf: url)
}
```

| Source | Access auto-started? | Must call start? | Must call stop? |
|---|---|---|---|
| `NSOpenPanel`/`NSSavePanel` | Yes | No | Yes |
| `.fileImporter`/`.fileExporter` | Yes | No | Yes |
| Dock drag-and-drop | Yes | No | Yes |
| Resolved security-scoped bookmark | No | **Yes** | Yes |

Every unbalanced `start`/`stop` pair leaks a kernel resource; enough leaks
and *all* file access stops working until the app relaunches — always pair
with `defer`. Store bookmark data (can be several KB) in a dedicated file in
your container, never `UserDefaults` (size limits, not designed for blobs).

**Document-relative bookmarks** — for a project file referencing sibling
files (an IDE referencing source files): pass `relativeTo: projectDocumentURL`
instead of `nil`. Any process with access to the parent document can resolve
these.

## Entitlements

**Core**: `com.apple.security.app-sandbox` — required for the Mac App Store;
without further entitlements, an app can only access its own container.

| File access | Grants |
|---|---|
| `com.apple.security.files.user-selected.read-only` | Read user-picked files |
| `com.apple.security.files.user-selected.read-write` | Read/write user-picked files |
| `com.apple.security.files.user-selected.executable` | Write executables to user-selected locations |
| `com.apple.security.files.bookmarks.app-scope` | Create/resolve app-scoped bookmarks (persist across launches) |
| `com.apple.security.files.bookmarks.document-scope` | Create/resolve document-scoped bookmarks |
| `com.apple.security.files.downloads.read-only` / `.read-write` | Downloads folder |
| `com.apple.security.files.pictures.read-only` / `.read-write` | Pictures folder |
| `com.apple.security.files.music.read-only` / `.read-write` | Music folder |
| `com.apple.security.files.movies.read-only` / `.read-write` | Movies folder |
| `com.apple.security.files.all` | All files (rarely approved for App Store) |

| Other | Grants |
|---|---|
| `com.apple.security.network.client` / `.server` | Outgoing / incoming network |
| `com.apple.security.device.camera` | Camera |
| `com.apple.security.device.audio-input` | Microphone |
| `com.apple.security.device.usb` | USB devices |
| `com.apple.security.print` | Printing |
| `com.apple.security.personal-information.addressbook` / `.calendars` / `.location` | Contacts / Calendar / Location |
| `com.apple.security.application-groups` | Shared container between your own apps |

Even with an entitlement declared, the user still sees a runtime permission
prompt for camera/mic/location/etc.

**Temporary exceptions** (`com.apple.security.temporary-exception.*`) grant
broader access for migrating legacy apps — App Review expects justification
and a removal path. Avoid entirely in new apps; use standard entitlements +
user-interaction-based access instead. Principle of least privilege: start
with `files.user-selected.read-write` and add only what a feature actually
needs — `files.all` invites App Review scrutiny.

## Diagnosing violations

1. **Console.app** — filter Subsystem `com.apple.sandbox.reporting`,
   Category `violation`, Type Error. Shows the denied operation and a stack
   trace.
2. **Confirm it's actually the sandbox** — POSIX permissions and TCC/Privacy
   can also deny operations; a true sandbox violation always shows
   `com.apple.sandbox.reporting` in Console.
3. **Check entitlements** against what the operation requires.
4. **Check for a stale bookmark** — file moved/renamed; check
   `bookmarkDataIsStale`.
5. **Check code signing identity** — macOS 14+ ties sandbox containers to the
   signature; a debug vs. release identity mismatch triggers prompts/denials.

```bash
log stream --predicate "subsystem == 'com.apple.sandbox.reporting'" --level debug
```

| Message | Cause | Fix |
|---|---|---|
| `deny(1) file-read-data` | Reading outside the sandbox | Open panel, or add entitlement |
| `deny(1) file-write-data` | Writing outside the sandbox | Save panel, or add entitlement |
| `deny(1) network-outbound` | No outgoing entitlement | Add `network.client` |
| `deny(1) network-inbound` | No listening entitlement | Add `network.server` |
| `deny(1) mach-lookup` | IPC with a system service | May need a temporary exception, or a redesign |

## Common mistakes

| Mistake | Symptom | Fix |
|---|---|---|
| Never testing under the sandbox | Works in debug, breaks in release/TestFlight | Enable sandbox in debug, or test the release build |
| Not calling `stopAccessingSecurityScopedResource()` | File access stops working after many cycles | `defer` every `start` with a `stop` |
| Bookmarks in `UserDefaults` | Corrupted/lost bookmark data | Store in a dedicated file in the container |
| Not checking `bookmarkDataIsStale` | Access fails after a file moves/renames | Check the flag, recreate when stale |
| Not calling `startAccessing` on a *resolved* bookmark | Denied despite a valid bookmark | Resolved bookmarks need an explicit `startAccessing` |
| Hardcoding `~/Library/...` paths | Denied under sandbox | Use `FileManager.urls(for:in:)` |
| Requesting `files.all` unnecessarily | App Review pushback | `files.user-selected.read-write` + bookmarks |
| Missing `network.client` | All requests fail silently | Add the entitlement |
