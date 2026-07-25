---
title: Skills catalog
description: Every skill shipped by core and each profile, and when it fires.
---

Skills are Claude Code `SKILL.md` files synced into a project's `.claude/skills/`.
`core` skills apply everywhere; profile skills only reach projects with that
profile enabled. Source lives at `core/skills/<name>/SKILL.md` or
`profiles/<profile>/skills/<name>/SKILL.md` in the lacquer repo.

Every Source links somewhere: a skill's own file in this repo (authored here,
or with no further attributable external source), or the named upstream it
was vendored from (copied in, then adapted to this repo's conventions) — see
[Notes on vendored skills](#notes-on-vendored-skills) below each table for
anything a short link can't capture.

## core

`claudemd-review`, `handoff`, and `private-repo-search` are this repo's own
implementations of three commands from the [`dx` plugin](https://github.com/ykdojo/claude-code-tips)
(`ykdojo/claude-code-tips`) — written from scratch, not copied, so they follow
this repo's own conventions and install without the plugin's other five
commands, which don't fit this fleet (`/dx:gha` overlaps `github-ci-fix`;
`/dx:half-clone` needs to rewrite the live session transcript, not something
a `SKILL.md` can do; `/dx:reddit-fetch`/`/dx:hn-summarize`/`/dx:version-check`
are generic productivity, not fleet engineering). See `docs/references.md`.

| Skill | Fires when | Source |
|-------|-----------|--------|
| `advisor-checkpoint` | Consulting a stronger model mid-task for a second opinion — stuck, before committing to an approach, or before declaring a non-trivial task done. | [advisor-checkpoint](https://github.com/patrickserrano/lacquer/blob/main/core/skills/advisor-checkpoint/SKILL.md) |
| `antigravity-rescue` | Stuck, and wanting a second opinion from Google's Antigravity CLI (`agy`) via a real subprocess — forwards the task and returns its output verbatim. | [antigravity-rescue](https://github.com/patrickserrano/lacquer/blob/main/core/skills/antigravity-rescue/SKILL.md) |
| `caveman` | User says "caveman mode", "less tokens", "be brief", or invokes `/caveman` — token-efficient terse replies. | [caveman](https://github.com/patrickserrano/lacquer/blob/main/core/skills/caveman/SKILL.md) |
| `claude-rescue` | Running under Codex CLI, stuck, and wanting a second opinion from Claude Code via a real subprocess — forwards the task and returns its output verbatim. | [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc/blob/main/plugins/codex/agents/codex-rescue.md) (`codex-rescue`, reversed) |
| `claudemd-review` | Corrected on something a CLAUDE.md file should already have covered, explicitly asked to review CLAUDE.md, or after a session with a lot of back-and-forth — reviews this conversation and proposes a bounded edit. | [claudemd-review](https://github.com/patrickserrano/lacquer/blob/main/core/skills/claudemd-review/SKILL.md) — own version of the dx plugin's `/dx:review-claudemd` (see [notes below](#notes-on-vendored-skills)) |
| `evaluator-optimizer` | Generate a solution, evaluate against explicit pass/fail criteria, refine, repeat until it passes or a round cap is hit. | [evaluator-optimizer](https://github.com/patrickserrano/lacquer/blob/main/core/skills/evaluator-optimizer/SKILL.md) |
| `github-ci-fix` | PR checks fail, CI is red, or a GitHub Actions workflow breaks — inspects failing checks via `gh`, pulls logs, triages flaky-vs-real and scopes the breaking commit, scopes external checks, builds a fix plan. | [github-ci-fix](https://github.com/patrickserrano/lacquer/blob/main/core/skills/github-ci-fix/SKILL.md) — flakiness/breaking-commit triage folded in to cover the dx plugin's `/dx:gha` without a second competing skill |
| `github-issue-fix-flow` | Given a GitHub issue number: implement a fix, run builds/tests, commit with a closing message, push. | [github-issue-fix-flow](https://github.com/patrickserrano/lacquer/blob/main/core/skills/github-issue-fix-flow/SKILL.md) |
| `handoff` | Before a session ends, before a context-heavy interruption (SSH/tmux disconnect, low context), or a worktree is handed to another worker — writes progress, what worked/failed, and the next concrete step. | [handoff](https://github.com/patrickserrano/lacquer/blob/main/core/skills/handoff/SKILL.md) — own version of the dx plugin's `/dx:handoff` (see [notes below](#notes-on-vendored-skills)) |
| `manager-loop` | Running a large batch of independent work items (fleet-wide harvest, multi-repo sync-down, overnight backlog) as a persistent coordinator. | [manager-loop](https://github.com/patrickserrano/lacquer/blob/main/core/skills/manager-loop/SKILL.md) |
| `nameplate-attention` | Grabbing the human's attention (topmost message card + pulsating screen borders) before a password-manager prompt or whenever blocked on them. | [steipete/nameplate](https://github.com/steipete/nameplate/blob/main/skills/nameplate-attention/SKILL.md) |
| `private-repo-search` | Finding code or a past implementation across GitHub repos — including private ones — via `gh search code`, reaching further than grep/Glob can. | [private-repo-search](https://github.com/patrickserrano/lacquer/blob/main/core/skills/private-repo-search/SKILL.md) — own version of the dx plugin's `/dx:private-github-search` (see [notes below](#notes-on-vendored-skills)) |
| `security-review` | Adversarial security review of a branch diff, PR, or working tree before merge. | [security-review](https://github.com/patrickserrano/lacquer/blob/main/core/skills/security-review/SKILL.md) |
| `skill-authoring-standard` | Writing or reviewing a `SKILL.md` — the bar it must clear: tight trigger-oriented frontmatter, single responsibility, no padding. | [skill-authoring-standard](https://github.com/patrickserrano/lacquer/blob/main/core/skills/skill-authoring-standard/SKILL.md) |
| `skill-tuning-loop` | Empirically testing whether a skill needs an edit — mines session transcripts for friction (or audits against skill-authoring-standard when evidence is thin), proposes a bounded fix, validates against held-out cases before it ships. | [skill-tuning-loop](https://github.com/patrickserrano/lacquer/blob/main/core/skills/skill-tuning-loop/SKILL.md) |

## ios

Several skills in this profile are vendored from public, per-topic "Agent
Skill" repos by [AvdLee](https://github.com/AvdLee) (Antoine van der Lee —
SwiftLee); others are original to this repo. See the notes below the table.

| Skill | Fires when | Source |
|-------|-----------|--------|
| `app-store-screenshots` | Capturing App Store/TestFlight screenshots from the simulator at native resolution and uploading via helm-asc. | [app-store-screenshots](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/app-store-screenshots/SKILL.md) |
| `core-data-expert` | Setting up a Core Data stack, debugging threading/merge conflicts, planning a migration, integrating CloudKit sync, or diagnosing performance/memory issues. | [AvdLee/Core-Data-Agent-Skill](https://github.com/AvdLee/Core-Data-Agent-Skill) |
| `ios-debugger-agent` | Running an iOS app, interacting with the simulator UI, capturing logs, or diagnosing runtime behavior. | [ios-debugger-agent](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-debugger-agent/SKILL.md) |
| `ios-performance-battery-patterns` | Touching widgets, animations, networking configuration, or background/observer cleanup — battery and performance patterns. | [ios-performance-battery-patterns](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-performance-battery-patterns/SKILL.md) — split out of `CLAUDE.ios.md` to keep it under the ~200-line guideline |
| `ios-secrets-setup` | Wiring a new app-runtime service key (RevenueCat, Aptabase, …) into `Secrets.xcconfig`, `project.yml`, and `Info.plist`. | [ios-secrets-setup](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-secrets-setup/SKILL.md) — split out of `CLAUDE.ios.md` to keep it under the ~200-line guideline |
| `macos-ci-recipes` | Adding macOS build/test/lint CI to a project on the ios profile (macOS-only or hybrid with an iOS target). | [macos-ci-recipes](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-ci-recipes/SKILL.md); adapted from windsock's and mindmint's own CI (own fleet) |
| `macos-development` | AppKit interop/modernization, App Sandbox & file-access entitlements, menu bar commands, multi-window scenes, the Settings scene, ScreenCaptureKit, macOS-specific SwiftUI differences, notarized Developer ID distribution. | adapted from [CharlesWiltgen/Axiom](https://github.com/CharlesWiltgen/Axiom)'s `axiom-macos` skill (MIT); see `docs/references.md` |
| `native-app-profiling` | Profiling native macOS/iOS apps for CPU hotspots, hangs, and hitches via Instruments traces. | [native-app-profiling](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/native-app-profiling/SKILL.md) |
| `release-app-store-changelog` | Generating user-facing App Store "What's New" release notes from git history. | [release-app-store-changelog](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/release-app-store-changelog/SKILL.md) |
| `release-macos-spm-packaging` | Scaffolding, building, and packaging SwiftPM-based macOS apps without an Xcode project. | [release-macos-spm-packaging](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/release-macos-spm-packaging/SKILL.md); harvested from kit (own fleet) |
| `rocketsim` | Interacting with iOS Simulator apps via RocketSim — reading accessibility elements, tapping, swiping, typing, hardware buttons. | [rocketsim](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/rocketsim/SKILL.md) — original discovery/delegation logic, no vendored content (see [notes below](#notes-on-vendored-skills)) |
| `swift-concurrency` | Diagnosing data races, converting callbacks to async/await, actor isolation, `Sendable` conformance, Swift 6 migration. | [AvdLee/Swift-Concurrency-Agent-Skill](https://github.com/AvdLee/Swift-Concurrency-Agent-Skill) |
| `swift-testing-expert` | Writing or modernizing Swift Testing suites — `#expect`/`#require`, traits/tags, parameterized tests, XCTest migration. | [AvdLee/Swift-Testing-Agent-Skill](https://github.com/AvdLee/Swift-Testing-Agent-Skill) |
| `swift-testing-wait-until` | A test needs to wait for an async state change instead of a fixed delay — the `no_task_sleep_in_tests` lint rule bans arbitrary `Task.sleep`. | [swift-testing-wait-until](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/swift-testing-wait-until/SKILL.md) — split out of `CLAUDE.ios.md` to keep it under the ~200-line guideline |
| `swiftui-expert-skill` | Writing, reviewing, or improving SwiftUI code — state management, view composition, performance, Liquid Glass adoption; also analyzes Instruments `.trace` files. | [AvdLee/SwiftUI-Agent-Skill](https://github.com/AvdLee/SwiftUI-Agent-Skill) |
| `swiftui-liquid-glass` | Adopting or reviewing the iOS 26+ Liquid Glass API in SwiftUI. | split from [AvdLee/SwiftUI-Agent-Skill](https://github.com/AvdLee/SwiftUI-Agent-Skill)'s reference material |
| `swiftui-performance-audit` | Diagnosing slow rendering, janky scrolling, high CPU/memory, or excessive view updates in SwiftUI. | split from [AvdLee/SwiftUI-Agent-Skill](https://github.com/AvdLee/SwiftUI-Agent-Skill)'s reference material |
| `swiftui-ui-patterns` | Creating or refactoring SwiftUI UI, tab architecture, screen composition. | [swiftui-ui-patterns](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/swiftui-ui-patterns/SKILL.md) |
| `swiftui-view-refactor` | Cleaning up a SwiftUI view's structure, dependency injection, and Observation usage. | [swiftui-view-refactor](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/swiftui-view-refactor/SKILL.md) |
| `update-swiftui-apis` | Scanning Apple's SwiftUI docs for deprecated APIs and refreshing the swiftui-expert-skill with replacements (requires the Sosumi MCP). | [update-swiftui-apis](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/update-swiftui-apis/SKILL.md) |
| `url-validation-security` | Validating a user-provided or externally-sourced URL before it reaches `AVPlayer`, `URLSession`, or a `WKWebView`. | [url-validation-security](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/url-validation-security/SKILL.md) — split out of `CLAUDE.ios.md` to keep it under the ~200-line guideline |
| `watchos-development` | watchOS app structure, Watch Connectivity, complications/Smart Stack, controls & Live Activities on watch, background refresh/networking limits, WatchKit/ClockKit modernization. | adapted from [CharlesWiltgen/Axiom](https://github.com/CharlesWiltgen/Axiom)'s `axiom-watchos` skill (MIT); see `docs/references.md` |
| `xcode-build-benchmark` | Benchmarking clean/incremental Xcode builds with repeatable inputs and timestamped artifacts. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-build-fixer` | Applying an approved Xcode build optimization plan, then re-benchmarking. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-build-orchestrator` | End-to-end build optimization: benchmark, run specialist analyzers, prioritize, get approval, delegate fixes, re-benchmark. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-compilation-analyzer` | Analyzing Swift compile hotspots and type-checking cost from build timing summaries. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcode-project-analyzer` | Auditing Xcode project config, build settings, schemes, and script phases for build-time improvements. | [AvdLee/Xcode-Build-Optimization-Agent-Skill](https://github.com/AvdLee/Xcode-Build-Optimization-Agent-Skill) |
| `xcsym` | Symbolicating/triaging iOS/macOS crash reports (`.ips`, MetricKit, `.crash`, `.xccrashpoint`) — no other skill in this profile covers post-mortem crash analysis. | wraps [CharlesWiltgen/Axiom](https://github.com/CharlesWiltgen/Axiom)'s `tools/xcsym` CLI (MIT); see `docs/references.md` for why only this tool was pulled in, not the full Axiom plugin |

#### Notes on vendored skills

- `swift-concurrency` is also based on Antoine van der Lee's paid [Swift
  Concurrency Course](https://www.swiftconcurrencycourse.com) — the skill
  body itself only carries a plain-text attribution line, no link:
  commit `a228d797` deliberately scrubbed `swiftconcurrencycourse.com`/UTM
  links from the *synced* skill to avoid shipping course marketing
  fleet-wide. That decision stands here too — see the git history rather
  than expecting a link inside the skill.
- `rocketsim` also *integrates with* the third-party [RocketSim](https://www.rocketsim.app)
  app itself (paid, not bundled).
- **`rocketsim` used to be listed as adapted from `AvdLee/RocketSim-Agent-Skill`
  — no longer.** Checked 2026-07-13: that repo's `LICENSE` reads "Copyright
  (c) 2026 SwiftLee. All rights reserved... proprietary to SwiftLee," unlike
  the other AvdLee repos this profile genuinely does vendor MIT content from
  (`Core-Data-Agent-Skill`, `Swift-Concurrency-Agent-Skill`,
  `Swift-Testing-Agent-Skill`, `SwiftUI-Agent-Skill`,
  `Xcode-Build-Optimization-Agent-Skill`). Rather than seek permission or
  rewrite equivalent content, the skill was rewritten as this repo's own thin
  discovery/delegation wrapper: it locates the installed RocketSim.app and
  hands off to the Agent-Skill file *the app itself bundles*
  (`Contents/Resources/Agent-Skill/SKILL.md`) at runtime, rather than keeping
  any RocketSim usage content in this repo at all. No vendored third-party
  text remains in `rocketsim/SKILL.md` — see the skill file itself for why
  that's actually the better design (it can't drift out of sync with the
  installed app version) independent of the license question that prompted
  the rewrite.
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
([PolyForm Perimeter 1.0.0](https://polyformproject.org/licenses/perimeter/1.0.0),
not MIT/permissive — installed and used as distributed via `skills add`, not
copied into this repo, which is what keeps this within the license's terms)
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
