---
name: swift-testing-wait-until
description: >
  Use when a test needs to wait for an async state change instead of a fixed
  delay — the `no_task_sleep_in_tests` lint rule bans arbitrary `Task.sleep`
  in tests because it causes flaky failures. Provides the `waitUntil` polling
  helper to add to a test target instead.
---

# `waitUntil`: No `Task.sleep` in Tests

Wait for the actual state change instead of a fixed delay. Add this
`TestHelpers.swift` to your test target (the lint rules already exclude
`*TestHelpers.swift`):

```swift
import Foundation

/// Thrown by `waitUntil` when the condition never became true within the timeout.
struct WaitTimeoutError: Error, CustomStringConvertible {
    let timeout: Duration
    var description: String { "waitUntil timed out after \(timeout)" }
}

/// Polls `condition` until true, throwing on timeout. Runs on the caller's actor
/// (via `#isolation`) so the closure may read `@MainActor` fixtures safely.
func waitUntil(
    timeout: Duration = .seconds(2),
    pollInterval: Duration = .milliseconds(10),
    isolation _: isolated (any Actor)? = #isolation,
    _ condition: () -> Bool
) async throws {
    let clock = ContinuousClock()
    let deadline = clock.now.advanced(by: timeout)
    while !condition() {
        guard clock.now < deadline else { throw WaitTimeoutError(timeout: timeout) }
        try await Task.sleep(for: pollInterval)
    }
}
```

Usage: `try await waitUntil { viewModel.isLoaded }` instead of
`try await Task.sleep(for: .seconds(1))`.
