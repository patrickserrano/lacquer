---
title: Skills catalog
description: Every skill shipped by core and each profile, and when it fires.
---

Skills are Claude Code `SKILL.md` files synced into a project's `.claude/skills/`.
`core` skills apply everywhere; profile skills only reach projects with that
profile enabled. Source lives at `core/skills/<name>/SKILL.md` or
`profiles/<profile>/skills/<name>/SKILL.md` in the harness repo.

## core

| Skill | Fires when |
|-------|-----------|
| `advisor-checkpoint` | Consulting a stronger model mid-task for a second opinion — stuck, before committing to an approach, or before declaring a non-trivial task done. |
| `caveman` | User says "caveman mode", "less tokens", "be brief", or invokes `/caveman` — token-efficient terse replies. |
| `evaluator-optimizer` | Generate a solution, evaluate against explicit pass/fail criteria, refine, repeat until it passes or a round cap is hit. |
| `github-ci-fix` | PR checks fail, CI is red, or a GitHub Actions workflow breaks — inspects failing checks via `gh`, pulls logs, scopes external checks, builds a fix plan. |
| `github-issue-fix-flow` | Given a GitHub issue number: implement a fix, run builds/tests, commit with a closing message, push. |
| `manager-loop` | Running a large batch of independent work items (fleet-wide harvest, multi-repo sync-down, overnight backlog) as a persistent coordinator. |
| `nameplate-attention` | Grabbing the human's attention (topmost message card + pulsating screen borders) before a password-manager prompt or whenever blocked on them. |
| `security-review` | Adversarial security review of a branch diff, PR, or working tree before merge. |
| `skill-authoring-standard` | Writing or reviewing a `SKILL.md` — the bar it must clear: tight trigger-oriented frontmatter, single responsibility, no padding. |

## ios

| Skill | Fires when |
|-------|-----------|
| `app-store-screenshots` | Capturing App Store/TestFlight screenshots from the simulator at native resolution and uploading via helm-asc. |
| `core-data-expert` | Setting up a Core Data stack, debugging threading/merge conflicts, planning a migration, integrating CloudKit sync, or diagnosing performance/memory issues. |
| `ios-debugger-agent` | Running an iOS app, interacting with the simulator UI, capturing logs, or diagnosing runtime behavior. |
| `macos-ci-recipes` | Adding macOS build/test/lint CI to a project on the ios profile (macOS-only or hybrid with an iOS target). |
| `native-app-profiling` | Profiling native macOS/iOS apps for CPU hotspots, hangs, and hitches via Instruments traces. |
| `release-app-store-changelog` | Generating user-facing App Store "What's New" release notes from git history. |
| `release-macos-spm-packaging` | Scaffolding, building, and packaging SwiftPM-based macOS apps without an Xcode project. |
| `rocketsim` | Interacting with iOS Simulator apps via RocketSim — reading accessibility elements, tapping, swiping, typing, hardware buttons. |
| `swift-concurrency` | Diagnosing data races, converting callbacks to async/await, actor isolation, `Sendable` conformance, Swift 6 migration. |
| `swift-testing-expert` | Writing or modernizing Swift Testing suites — `#expect`/`#require`, traits/tags, parameterized tests, XCTest migration. |
| `swiftui-expert-skill` | Writing, reviewing, or improving SwiftUI code — state management, view composition, performance, Liquid Glass adoption; also analyzes Instruments `.trace` files. |
| `swiftui-liquid-glass` | Adopting or reviewing the iOS 26+ Liquid Glass API in SwiftUI. |
| `swiftui-performance-audit` | Diagnosing slow rendering, janky scrolling, high CPU/memory, or excessive view updates in SwiftUI. |
| `swiftui-ui-patterns` | Creating or refactoring SwiftUI UI, tab architecture, screen composition. |
| `swiftui-view-refactor` | Cleaning up a SwiftUI view's structure, dependency injection, and Observation usage. |
| `update-swiftui-apis` | Scanning Apple's SwiftUI docs for deprecated APIs and refreshing the swiftui-expert-skill with replacements (requires the Sosumi MCP). |
| `xcode-build-benchmark` | Benchmarking clean/incremental Xcode builds with repeatable inputs and timestamped artifacts. |
| `xcode-build-fixer` | Applying an approved Xcode build optimization plan, then re-benchmarking. |
| `xcode-build-orchestrator` | End-to-end build optimization: benchmark, run specialist analyzers, prioritize, get approval, delegate fixes, re-benchmark. |
| `xcode-compilation-analyzer` | Analyzing Swift compile hotspots and type-checking cost from build timing summaries. |
| `xcode-project-analyzer` | Auditing Xcode project config, build settings, schemes, and script phases for build-time improvements. |

## supabase

| Skill | Fires when |
|-------|-----------|
| `supabase-postgres-best-practices` | Writing, reviewing, or optimizing Postgres queries, schema, indexing, connection pooling, RLS policies, or locking. |
