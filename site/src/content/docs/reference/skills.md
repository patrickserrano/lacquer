---
title: Skills catalog
description: Every skill shipped by core and each profile, and when it fires.
---

Skills are Claude Code `SKILL.md` files synced into a project's `.claude/skills/`.
`core` skills apply everywhere; profile skills only reach projects with that
profile enabled. Source lives at `core/skills/<name>/SKILL.md` or
`profiles/<profile>/skills/<name>/SKILL.md` in the lacquer repo.

Skills marked **original** were authored directly in this repo. Everything
else was vendored (copied in, then adapted to this repo's conventions) from a
named upstream — see [Notes on vendored skills](#notes-on-vendored-skills)
below each table for anything a short link can't capture.

## core

| Skill | Fires when | Source |
|-------|-----------|--------|
| `advisor-checkpoint` | Consulting a stronger model mid-task for a second opinion — stuck, before committing to an approach, or before declaring a non-trivial task done. | original |
| `caveman` | User says "caveman mode", "less tokens", "be brief", or invokes `/caveman` — token-efficient terse replies. | ported from ios-template (archived); now [caveman](https://github.com/patrickserrano/lacquer/blob/main/core/skills/caveman/SKILL.md) |
| `claude-rescue` | Running under Codex CLI, stuck, and wanting a second opinion from Claude Code via a real subprocess — forwards the task and returns its output verbatim. | [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc/blob/main/plugins/codex/agents/codex-rescue.md) (`codex-rescue`, reversed) |
| `evaluator-optimizer` | Generate a solution, evaluate against explicit pass/fail criteria, refine, repeat until it passes or a round cap is hit. | original |
| `github-ci-fix` | PR checks fail, CI is red, or a GitHub Actions workflow breaks — inspects failing checks via `gh`, pulls logs, scopes external checks, builds a fix plan. | ported from ios-template (archived); now [github-ci-fix](https://github.com/patrickserrano/lacquer/blob/main/core/skills/github-ci-fix/SKILL.md) |
| `github-issue-fix-flow` | Given a GitHub issue number: implement a fix, run builds/tests, commit with a closing message, push. | ported from ios-template (archived); now [github-issue-fix-flow](https://github.com/patrickserrano/lacquer/blob/main/core/skills/github-issue-fix-flow/SKILL.md) |
| `manager-loop` | Running a large batch of independent work items (fleet-wide harvest, multi-repo sync-down, overnight backlog) as a persistent coordinator. | original |
| `nameplate-attention` | Grabbing the human's attention (topmost message card + pulsating screen borders) before a password-manager prompt or whenever blocked on them. | [steipete/nameplate](https://github.com/steipete/nameplate/blob/main/skills/nameplate-attention/SKILL.md) |
| `security-review` | Adversarial security review of a branch diff, PR, or working tree before merge. | original |
| `skill-authoring-standard` | Writing or reviewing a `SKILL.md` — the bar it must clear: tight trigger-oriented frontmatter, single responsibility, no padding. | original |

## ios

Most of this profile was absorbed from `patrickserrano/ios-template` (a
private, now-archived predecessor repo) in one migration; several of *those*
skills turned out to themselves be vendored from public, per-topic "Agent
Skill" repos by
[AvdLee](https://github.com/AvdLee) (Antoine van der Lee — SwiftLee). See the
notes below the table.

| Skill | Fires when | Source |
|-------|-----------|--------|
| `app-store-screenshots` | Capturing App Store/TestFlight screenshots from the simulator at native resolution and uploading via helm-asc. | ported from ios-template (archived); now [app-store-screenshots](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/app-store-screenshots/SKILL.md) |
| `core-data-expert` | Setting up a Core Data stack, debugging threading/merge conflicts, planning a migration, integrating CloudKit sync, or diagnosing performance/memory issues. | [AvdLee/Core-Data-Agent-Skill](https://github.com/AvdLee/Core-Data-Agent-Skill) |
| `ios-debugger-agent` | Running an iOS app, interacting with the simulator UI, capturing logs, or diagnosing runtime behavior. | ported from ios-template (archived); now [ios-debugger-agent](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-debugger-agent/SKILL.md) |
| `macos-ci-recipes` | Adding macOS build/test/lint CI to a project on the ios profile (macOS-only or hybrid with an iOS target). | adapted from windsock's and mindmint's own CI (own fleet) |
| `native-app-profiling` | Profiling native macOS/iOS apps for CPU hotspots, hangs, and hitches via Instruments traces. | ported from ios-template (archived); now [native-app-profiling](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/native-app-profiling/SKILL.md) |
| `release-app-store-changelog` | Generating user-facing App Store "What's New" release notes from git history. | ported from ios-template (archived); now [release-app-store-changelog](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/release-app-store-changelog/SKILL.md) |
| `release-macos-spm-packaging` | Scaffolding, building, and packaging SwiftPM-based macOS apps without an Xcode project. | harvested from kit (own fleet) |
| `rocketsim` | Interacting with iOS Simulator apps via RocketSim — reading accessibility elements, tapping, swiping, typing, hardware buttons. | [AvdLee/RocketSim-Agent-Skill](https://github.com/AvdLee/RocketSim-Agent-Skill) |
| `swift-concurrency` | Diagnosing data races, converting callbacks to async/await, actor isolation, `Sendable` conformance, Swift 6 migration. | [AvdLee/Swift-Concurrency-Agent-Skill](https://github.com/AvdLee/Swift-Concurrency-Agent-Skill) |
| `swift-testing-expert` | Writing or modernizing Swift Testing suites — `#expect`/`#require`, traits/tags, parameterized tests, XCTest migration. | [AvdLee/Swift-Testing-Agent-Skill](https://github.com/AvdLee/Swift-Testing-Agent-Skill) |
| `swiftui-expert-skill` | Writing, reviewing, or improving SwiftUI code — state management, view composition, performance, Liquid Glass adoption; also analyzes Instruments `.trace` files. | [AvdLee/SwiftUI-Agent-Skill](https://github.com/AvdLee/SwiftUI-Agent-Skill) |
| `swiftui-liquid-glass` | Adopting or reviewing the iOS 26+ Liquid Glass API in SwiftUI. | split from [AvdLee/SwiftUI-Agent-Skill](https://github.com/AvdLee/SwiftUI-Agent-Skill)'s reference material |
| `swiftui-performance-audit` | Diagnosing slow rendering, janky scrolling, high CPU/memory, or excessive view updates in SwiftUI. | split from [AvdLee/SwiftUI-Agent-Skill](https://github.com/AvdLee/SwiftUI-Agent-Skill)'s reference material |
| `swiftui-ui-patterns` | Creating or refactoring SwiftUI UI, tab architecture, screen composition. | ported from ios-template (archived); now [swiftui-ui-patterns](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/swiftui-ui-patterns/SKILL.md) |
| `swiftui-view-refactor` | Cleaning up a SwiftUI view's structure, dependency injection, and Observation usage. | ported from ios-template (archived); now [swiftui-view-refactor](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/swiftui-view-refactor/SKILL.md) |
| `update-swiftui-apis` | Scanning Apple's SwiftUI docs for deprecated APIs and refreshing the swiftui-expert-skill with replacements (requires the Sosumi MCP). | ported from ios-template (archived); now [update-swiftui-apis](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/update-swiftui-apis/SKILL.md) |
| `xcode-build-benchmark` | Benchmarking clean/incremental Xcode builds with repeatable inputs and timestamped artifacts. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-build-fixer` | Applying an approved Xcode build optimization plan, then re-benchmarking. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-build-orchestrator` | End-to-end build optimization: benchmark, run specialist analyzers, prioritize, get approval, delegate fixes, re-benchmark. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-compilation-analyzer` | Analyzing Swift compile hotspots and type-checking cost from build timing summaries. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-project-analyzer` | Auditing Xcode project config, build settings, schemes, and script phases for build-time improvements. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |

#### Notes on vendored skills

- `swift-concurrency` is also based on Antoine van der Lee's paid [Swift
  Concurrency Course](https://www.swiftconcurrencycourse.com) — the skill
  body itself only carries a plain-text attribution line, no link:
  commit `a228d797` deliberately scrubbed `swiftconcurrencycourse.com`/UTM
  links from the *synced* skill to avoid shipping course marketing
  fleet-wide. That decision stands here too — see the git history rather
  than expecting a link inside the skill.
- `rocketsim` also *integrates with* the third-party [RocketSim](https://www.rocketsim.app)
  app itself (paid, not bundled) — distinct from the skill's own source above.
- `swiftui-liquid-glass` additionally cites Apple's WWDC25 session 323 for
  one design note.

## supabase

| Skill | Fires when | Source |
|-------|-----------|--------|
| `supabase-postgres-best-practices` | Writing, reviewing, or optimizing Postgres queries, schema, indexing, connection pooling, RLS policies, or locking. | [supabase/agent-skills](https://github.com/supabase/agent-skills/tree/main/skills/supabase-postgres-best-practices) (official, MIT) |

## Third-party skills

Not this repo's own skills — installed per-project via `[project].skills` in
`.lacquer.toml` and `lacquer skills` (see the [README](https://github.com/patrickserrano/lacquer#third-party-skills)
for the mechanism). Listed here so the packages this fleet actually pulls in
have a source link.

**[dpearson2699/swift-ios-skills](https://github.com/dpearson2699/swift-ios-skills)**
— Apple-framework reference skills. `lacquer init` suggests these automatically
from a component's Swift imports (see `internal/skillsuggest`); this table is
that same mapping.

| Skill | Swift import(s) |
|-------|-----------------|
| [`activitykit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/activitykit) | `ActivityKit` |
| [`app-intents`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/app-intents) | `AppIntents` |
| [`apple-on-device-ai`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/apple-on-device-ai) | `FoundationModels` |
| [`authentication`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/authentication) | `AuthenticationServices`, `LocalAuthentication` |
| [`avkit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/avkit) | `AVKit` |
| [`background-processing`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/background-processing) | `BackgroundTasks` |
| [`cloudkit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/cloudkit) | `CloudKit` |
| [`cryptokit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/cryptokit) | `CryptoKit` |
| [`device-integrity`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/device-integrity) | `DeviceCheck` |
| [`healthkit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/healthkit) | `HealthKit` |
| [`mapkit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/mapkit) | `MapKit` |
| [`musickit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/musickit) | `MusicKit` |
| [`pdfkit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/pdfkit) | `PDFKit` |
| [`photokit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/photokit) | `PhotosUI` |
| [`push-notifications`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/push-notifications) | `UserNotifications` |
| [`realitykit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/realitykit) | `RealityKit` |
| [`storekit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/storekit) | `StoreKit` |
| [`swift-charts`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/swift-charts) | `Charts` |
| [`swift-testing`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/swift-testing) | `Testing` |
| [`swiftdata`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/swiftdata) | `SwiftData` |
| [`vision-framework`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/vision-framework) | `Vision`, `VisionKit` |
| [`widgetkit`](https://github.com/dpearson2699/swift-ios-skills/tree/main/skills/widgetkit) | `WidgetKit` |

**[HunterHillegas/mac-assed-mac-app-skill](https://github.com/HunterHillegas/mac-assed-mac-app-skill)**
— AppKit/macOS app conventions. Not import-suggested (no single Swift import
signals a macOS app); declared explicitly where needed (e.g. windsock).
