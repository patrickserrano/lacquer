---
name: performance-audit
description: Audit and improve SwiftUI runtime performance. Use for requests to diagnose slow rendering, janky scrolling, high CPU/memory usage, excessive view updates, or layout thrash in SwiftUI apps.
---

# SwiftUI Performance Audit

## Overview

Audit SwiftUI view performance from instrumentation and baselining to root-cause analysis and concrete remediation steps.

## Workflow Decision Tree

1. **If user provides code**: Start with Code-First Review
2. **If user only describes symptoms**: Ask for minimal code/context, then Code-First Review
3. **If code review is inconclusive**: Guide user to profile with Instruments

## 1. Code-First Review

### Collect
- Target view/feature code
- Data flow: state, environment, observable models
- Symptoms and reproduction steps

### Focus On
- View invalidation storms from broad state changes
- Unstable identity in lists (`id` churn, `UUID()` per render)
- Heavy work in `body` (formatting, sorting, image decoding)
- Layout thrash (deep stacks, `GeometryReader`, preference chains)
- Large images without downsampling
- Over-animated hierarchies (implicit animations on large trees)

### Provide
- Likely root causes with code references
- Suggested fixes and refactors
- Minimal repro or instrumentation suggestion if needed

## 2. Guide User to Profile

If code review is inconclusive, explain how to collect data:

1. Use SwiftUI template in Instruments (Release build)
2. Reproduce the exact interaction (scroll, navigation, animation)
3. Capture SwiftUI timeline and Time Profiler
4. Export or screenshot relevant lanes and call tree

Ask for:
- Trace export or screenshots
- Device/OS/build configuration

## 3. Common Code Smells (and Fixes)

### Expensive formatters in `body`

**Bad:**
```swift
var body: some View {
    let formatter = NumberFormatter()  // Slow allocation every render
    Text(formatter.string(from: value))
}
```

**Good:**
```swift
final class Formatters {
    static let number = NumberFormatter()
}

var body: some View {
    Text(Formatters.number.string(from: value))
}
```

### Computed properties with heavy work

**Bad:**
```swift
var filtered: [Item] {
    items.filter { $0.isEnabled }  // Runs every body eval
}
```

**Good:**
```swift
@State private var filtered: [Item] = []

.onChange(of: items) {
    filtered = items.filter { $0.isEnabled }
}
```

### Sorting/filtering in ForEach

**Bad:**
```swift
ForEach(items.sorted(by: sortRule)) { item in
    Row(item)
}
```

**Good:**
```swift
let sortedItems = items.sorted(by: sortRule)  // Compute once
ForEach(sortedItems) { item in
    Row(item)
}
```

### Unstable identity

**Bad:**
```swift
ForEach(items, id: \.self) { item in  // \.self may not be stable
    Row(item)
}
```

**Good:**
```swift
ForEach(items, id: \.stableID) { item in
    Row(item)
}
```

### Image decoding on main thread

**Bad:**
```swift
Image(uiImage: UIImage(data: data)!)
```

**Good:**
```swift
// Decode/downsample off main thread, cache the result
@State private var image: UIImage?

.task {
    image = await ImageLoader.load(data: data, targetSize: size)
}
```

### Broad dependencies in observable models

**Bad:**
```swift
@Observable class Model {
    var items: [Item] = []
}

var body: some View {
    Row(isFavorite: model.items.contains(item))  // Entire array dependency
}
```

**Good:**
```swift
// Granular view models or per-item state to reduce update fan-out
```

## 4. Remediation Strategies

| Issue | Fix |
|-------|-----|
| Broad state changes | Narrow scope with `@State`/`@Observable` closer to leaves |
| Unstable identities | Use stable, unique IDs for `ForEach` |
| Heavy work in body | Precompute, cache, move to `@State` |
| Expensive subtrees | Use `equatable()` or value wrappers |
| Large images | Downsample before rendering |
| Layout complexity | Reduce nesting, use fixed sizing where possible |

## 5. Verify

Ask user to re-run same capture and compare with baseline:
- CPU usage
- Frame drops
- Memory peak

## Output Format

Provide:
1. Metrics table (before/after if available)
2. Top issues (ordered by impact)
3. Proposed fixes with estimated effort

## Profiling Commands

```bash
# Build for profiling
xcodebuild -scheme MyApp -configuration Release -destination 'platform=iOS Simulator,name=iPhone 15 Pro' build

# Open in Instruments
open -a Instruments
```

## Instruments Checklist

- [ ] Use Release build (not Debug)
- [ ] Select SwiftUI template
- [ ] Reproduce exact problematic interaction
- [ ] Look at SwiftUI timeline for body evaluations
- [ ] Check Time Profiler for hot spots
- [ ] Note frame rate drops in Animation timeline
