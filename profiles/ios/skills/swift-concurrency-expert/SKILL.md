---
name: swift-concurrency-expert
description: Swift Concurrency review and remediation for Swift 6.2+. Use when asked to review Swift Concurrency usage, improve concurrency compliance, or fix Swift concurrency compiler errors.
---

# Swift Concurrency Expert

## Overview

Review and fix Swift Concurrency issues in Swift 6.2+ codebases by applying actor isolation, Sendable safety, and modern concurrency patterns with minimal behavior changes.

## Workflow

### 1. Triage the issue

- Capture the exact compiler diagnostics and the offending symbol(s)
- Check project concurrency settings: Swift language version (6.2+), strict concurrency level
- Check if approachable concurrency (default actor isolation / main-actor-by-default) is enabled
- Identify the current actor context (`@MainActor`, `actor`, `nonisolated`)
- Confirm whether the code is UI-bound or intended to run off the main actor

### 2. Apply the smallest safe fix

Prefer edits that preserve existing behavior while satisfying data-race safety.

**Common fixes:**

| Issue | Fix |
|-------|-----|
| UI-bound types | Annotate the type or members with `@MainActor` |
| Protocol conformance on main actor types | Make conformance isolated: `extension Foo: @MainActor SomeProtocol` |
| Global/static state | Protect with `@MainActor` or move into an actor |
| Background work | Use `@concurrent` async function on a `nonisolated` type |
| Sendable errors | Prefer immutable/value types; add `Sendable` only when correct |

## Swift 6.2 Key Changes

### Default Actor Isolation

Swift 6.2 stays single-threaded by default until you choose to introduce concurrency:

```swift
// In Swift 6.2 with approachable concurrency enabled,
// this no longer produces a data race error
@MainActor
final class StickerModel {
    let photoProcessor = PhotoProcessor()

    func extractSticker(_ item: PhotosPickerItem) async throws -> Sticker? {
        guard let data = try await item.loadTransferable(type: Data.self) else {
            return nil
        }
        // Safe - runs on main actor by default
        return await photoProcessor.extractSticker(data: data, with: item.itemIdentifier)
    }
}
```

### Isolated Conformances

Conformances that need main actor state are now supported:

```swift
protocol Exportable {
    func export()
}

// Isolated conformance - safe because compiler ensures
// it's only used on the main actor
extension StickerModel: @MainActor Exportable {
    func export() {
        photoProcessor.exportAsPNG()
    }
}
```

### Protecting Global State

```swift
// Protect with @MainActor
@MainActor
final class StickerLibrary {
    static let shared: StickerLibrary = .init()
}

// Or enable main-actor-by-default mode for the whole project
```

### Offloading Work to Background

Use `@concurrent` to explicitly run code on the concurrent thread pool:

```swift
nonisolated struct PhotoProcessor {
    @concurrent
    func process(data: Data) async -> ProcessedPhoto? {
        // Runs on background thread
    }
}

// Caller adds await
processedPhotos[item.id] = await PhotoProcessor().process(data: data)
```

## Migration Checklist

1. Check Swift version in build settings (needs 6.2+)
2. Enable approachable concurrency features in build settings
3. Run Swift migration tooling: `swift.org/migration`
4. Fix remaining compiler errors using patterns above
5. Test thoroughly - concurrency bugs may surface at runtime

## Common Patterns

### UI-Bound Class
```swift
@MainActor
final class ViewModel {
    var items: [Item] = []

    func load() async {
        items = try await service.fetchItems()
    }
}
```

### Background Processing
```swift
nonisolated struct ImageProcessor {
    @concurrent
    static func resize(_ image: UIImage, to size: CGSize) async -> UIImage {
        // Heavy work runs off main actor
    }
}
```

### Actor for Shared Mutable State
```swift
actor Cache {
    private var storage: [String: Data] = [:]

    func get(_ key: String) -> Data? {
        storage[key]
    }

    func set(_ key: String, value: Data) {
        storage[key] = value
    }
}
```

## Build Settings

Enable in Xcode under Swift Compiler - Concurrency:
- `SWIFT_STRICT_CONCURRENCY` = complete
- Approachable concurrency features (Swift 6.2+)

Or in Package.swift:
```swift
swiftSettings: [
    .enableExperimentalFeature("StrictConcurrency")
]
```
