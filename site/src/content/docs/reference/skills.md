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
| `exploring-an-idea` | The very start of a new product idea — before a PRD, PCD, repo, or name exists, or when someone asks for a PRD/PCD from scratch. | [exploring-an-idea](https://github.com/patrickserrano/lacquer/blob/main/core/skills/exploring-an-idea/SKILL.md) |
| `github-ci-fix` | PR checks fail, CI is red, or a GitHub Actions workflow breaks — inspects failing checks via `gh`, pulls logs, triages flaky-vs-real and scopes the breaking commit, scopes external checks, builds a fix plan. | [github-ci-fix](https://github.com/patrickserrano/lacquer/blob/main/core/skills/github-ci-fix/SKILL.md) — flakiness/breaking-commit triage folded in to cover the dx plugin's `/dx:gha` without a second competing skill |
| `github-issue-fix-flow` | Given a GitHub issue number: implement a fix, run builds/tests, commit with a closing message, push. | [github-issue-fix-flow](https://github.com/patrickserrano/lacquer/blob/main/core/skills/github-issue-fix-flow/SKILL.md) |
| `handoff` | Before a session ends, before a context-heavy interruption (SSH/tmux disconnect, low context), or a worktree is handed to another worker — writes progress, what worked/failed, and the next concrete step. | [handoff](https://github.com/patrickserrano/lacquer/blob/main/core/skills/handoff/SKILL.md) — own version of the dx plugin's `/dx:handoff` (see [notes below](#notes-on-vendored-skills)) |
| `manager-loop` | Running a large batch of independent work items (fleet-wide harvest, multi-repo sync-down, overnight backlog) as a persistent coordinator. | [manager-loop](https://github.com/patrickserrano/lacquer/blob/main/core/skills/manager-loop/SKILL.md) |
| `nameplate-attention` | Grabbing the human's attention (topmost message card + pulsating screen borders) before a password-manager prompt or whenever blocked on them. | [steipete/nameplate](https://github.com/steipete/nameplate/blob/main/skills/nameplate-attention/SKILL.md) |
| `private-repo-search` | Finding code or a past implementation across GitHub repos — including private ones — via `gh search code`, reaching further than grep/Glob can. | [private-repo-search](https://github.com/patrickserrano/lacquer/blob/main/core/skills/private-repo-search/SKILL.md) — own version of the dx plugin's `/dx:private-github-search` (see [notes below](#notes-on-vendored-skills)) |
| `security-review` | Adversarial security review of a branch diff, PR, or working tree before merge. | [security-review](https://github.com/patrickserrano/lacquer/blob/main/core/skills/security-review/SKILL.md) |
| `skill-authoring-standard` | Writing or reviewing a `SKILL.md` — the bar it must clear: tight trigger-oriented frontmatter, single responsibility, no padding. | [skill-authoring-standard](https://github.com/patrickserrano/lacquer/blob/main/core/skills/skill-authoring-standard/SKILL.md) |
| `skill-tuning-loop` | Empirically testing whether a skill needs an edit — mines session transcripts for friction (or audits against skill-authoring-standard when evidence is thin), proposes a bounded fix, validates against held-out cases before it ships. | [skill-tuning-loop](https://github.com/patrickserrano/lacquer/blob/main/core/skills/skill-tuning-loop/SKILL.md) |
| `working-with-lacquer` | Working in a lacquer-managed project and the management itself comes up — `No lacquer drift` fails, `lacquer audit` exits 3/4/6, a synced config reverts on the next sync, or a baseline can't be met yet. | [working-with-lacquer](https://github.com/patrickserrano/lacquer/blob/main/core/skills/working-with-lacquer/SKILL.md) |

### core — marketing

Vendored wholesale from [coreyhaines31/marketingskills](https://github.com/coreyhaines31/marketingskills)
(MIT; the upstream licence is kept at `core/skills/LICENSE-upstream-marketingskills`).
They sit in `core` rather than a profile because they apply to any project with
a public-facing app or site, not just an iOS one. Two were edited on the way in
rather than copied blind: `video` used `npm install`, which the web profile
bans in favour of `pnpm` against a committed lockfile, and `cold-email` used
`{{FirstName}}`-shaped mail-merge placeholders that collide with lacquer's own
`{{TOKEN}}` substitution syntax and made `sync` correctly refuse to render them.

| Skill | Fires when |
|-------|-----------|
| `ab-testing` | Planning, designing, or implementing an A/B test, split test, or a growth experimentation program. |
| `ad-creative` | Generating or scaling ad creative — headlines, descriptions, primary text, full variations — for any paid platform. |
| `ads` | Paid advertising campaigns on Google, Meta, LinkedIn, X, or another ad platform; PPC, ROAS, CPA, retargeting. |
| `ai-seo` | Optimizing content to be cited by LLMs and appear in AI-generated answers (AEO / GEO / LLMO). |
| `analytics` | Setting up, improving, or auditing analytics and measurement — GA4, event tracking, UTMs, GTM. |
| `aso` | Auditing or optimizing an App Store or Google Play listing. |
| `attribution` | Working out which marketing actually drives revenue, choosing an attribution model, or reconciling numbers that disagree across tools. |
| `churn-prevention` | Reducing churn — cancellation flows, save offers, dunning, failed-payment recovery, retention. |
| `co-marketing` | Finding co-marketing partners and planning joint or cross-promotional campaigns. |
| `cold-email` | Writing B2B cold outreach emails and follow-up sequences. |
| `community-marketing` | Building or growing a Discord/Slack/forum community and turning it into word-of-mouth. |
| `competitor-profiling` | Researching and profiling competitors from their URLs. |
| `competitors` | Building competitor comparison, "vs", and alternative pages. |
| `content-strategy` | Deciding what content to create — topic clusters, blog strategy, content planning. |
| `copy-editing` | Editing, reviewing, or refreshing existing marketing copy. |
| `copywriting` | Writing or rewriting marketing copy for a page — homepage, landing, pricing, feature, about. |
| `cro` | Increasing conversions on a marketing page or a form. |
| `customer-research` | Running or synthesizing customer research — interviews, surveys, support tickets, ICP. |
| `directory-submissions` | Submitting a product to startup / SaaS / AI directories for backlinks and discovery. |
| `emails` | Building an email sequence, drip campaign, or lifecycle flow. |
| `events` | Planning, running, sponsoring, or speaking at events, and getting pipeline out of them. |
| `free-tools` | Planning or building a free tool as marketing (engineering as marketing). |
| `image` | Creating or optimizing marketing images — blog heroes, social graphics, mockups, brand assets. |
| `influencer-marketing` | Running influencer, creator, or ambassador partnerships, including disclosure compliance. |
| `launch` | Planning a product launch, feature announcement, or release strategy — Product Hunt, beta, waitlist. |
| `lead-magnets` | Creating or optimizing a lead magnet for email capture. |
| `marketing-council` | Wanting several expert perspectives on one marketing question — a simulated board of legendary marketers. |
| `marketing-ideas` | Needing marketing, growth, or promotion ideas for a software product. |
| `marketing-loops` | Setting up a recurring, self-running marketing workflow an agent runs on a cadence. |
| `marketing-plan` | Needing a full marketing or go-to-market plan — AARRR, 90-day, 12-month. |
| `marketing-psychology` | Applying behavioral science, mental models, or persuasion principles to marketing. |
| `offers` | Designing or improving the offer itself — value framing, bonuses, guarantees, naming, payment structure. |
| `onboarding` | Optimizing post-signup onboarding, activation, first-run experience, time-to-value. |
| `paywalls` | Creating or optimizing in-app paywalls, upgrade screens, upsells, feature gates. |
| `popups` | Creating or optimizing popups, modals, slide-ins, or banners for conversion. |
| `pricing` | Pricing decisions, packaging, and monetization strategy — tiers, freemium, trials, willingness to pay. |
| `product-marketing` | Creating or updating the product marketing context document — positioning, ICP, target audience. |
| `programmatic-seo` | Creating SEO pages at scale from templates and data. |
| `prospecting` | Finding, qualifying, and building a prospect list. |
| `public-relations` | Earned media, press coverage, journalist outreach, media strategy. |
| `referrals` | Creating, optimizing, or analyzing a referral, affiliate, or word-of-mouth program. |
| `revops` | Revenue operations — lead lifecycle, scoring, routing, marketing-to-sales handoff, pipeline stages. |
| `sales-enablement` | Sales collateral — pitch decks, one-pagers, objection handling, demo scripts. |
| `schema` | Adding or fixing schema markup and structured data — JSON-LD, rich snippets. |
| `seo-audit` | Auditing or diagnosing SEO issues — technical SEO, on-page, a traffic drop. |
| `signup` | Optimizing signup, registration, and trial activation flows. |
| `site-architecture` | Planning page hierarchy, navigation, URL structure, and internal linking. |
| `sms` | Planning or optimizing SMS/MMS marketing — welcome, abandoned cart, win-back, transactional. |
| `social` | Creating, scheduling, or optimizing social content, and social listening / engagement triage. |
| `video` | Producing video content with AI tools or programmatic frameworks (Remotion, Veo, Sora, Runway, …). |

## ios

Several skills in this profile are vendored from public, per-topic "Agent
Skill" repos by [AvdLee](https://github.com/AvdLee) (Antoine van der Lee —
SwiftLee); others are original to this repo. See the notes below the table.

| Skill | Fires when | Source |
|-------|-----------|--------|
| `app-store-screenshots` | Capturing App Store/TestFlight screenshots from the simulator at native resolution and uploading via helm-asc. | [app-store-screenshots](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/app-store-screenshots/SKILL.md) |
| `core-data-expert` | Setting up a Core Data stack, debugging threading/merge conflicts, planning a migration, integrating CloudKit sync, or diagnosing performance/memory issues. | [AvdLee/Core-Data-Agent-Skill](https://github.com/AvdLee/Core-Data-Agent-Skill) |
| `helm-asc` | Driving App Store Connect through the Helm CLI — app discovery, versions, localizations, screenshots, TestFlight groups, builds, IAPs and subscriptions, review replies and submissions. | [helm-asc](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/helm-asc/SKILL.md) — copied from the already-working local skill rather than reconstructed from vendor docs (see [notes below](#notes-on-vendored-skills)) |
| `ios-debugger-agent` | Running an iOS app, interacting with the simulator UI, capturing logs, or diagnosing runtime behavior. | [ios-debugger-agent](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-debugger-agent/SKILL.md) |
| `ios-performance-battery-patterns` | Touching widgets, animations, networking configuration, or background/observer cleanup — battery and performance patterns. | [ios-performance-battery-patterns](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-performance-battery-patterns/SKILL.md) — split out of `CLAUDE.ios.md` to keep it under the ~200-line guideline |
| `ios-secrets-setup` | Wiring a new app-runtime service key (RevenueCat, Aptabase, …) into `Secrets.xcconfig`, `project.yml`, and `Info.plist`. | [ios-secrets-setup](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/ios-secrets-setup/SKILL.md) — split out of `CLAUDE.ios.md` to keep it under the ~200-line guideline |
| `macos-ci-recipes` | Adding macOS build/test/lint CI to a project on the ios profile (macOS-only or hybrid with an iOS target). | [macos-ci-recipes](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-ci-recipes/SKILL.md); adapted from windsock's and mindmint's own CI (own fleet) |
| `macos-development` | AppKit interop/modernization, App Sandbox & file-access entitlements, menu bar commands, multi-window scenes, the Settings scene, ScreenCaptureKit, macOS-specific SwiftUI differences, notarized Developer ID distribution. | adapted from [CharlesWiltgen/Axiom](https://github.com/CharlesWiltgen/Axiom)'s `axiom-macos` skill (MIT); see `docs/references.md` |
| `native-app-profiling` | Profiling native macOS/iOS apps for CPU hotspots, hangs, and hitches via Instruments traces. | [native-app-profiling](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/native-app-profiling/SKILL.md) |
| `release-app-store-changelog` | Generating user-facing App Store "What's New" release notes from git history. | [release-app-store-changelog](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/release-app-store-changelog/SKILL.md) |
| `release-macos-spm-packaging` | Scaffolding, building, and packaging SwiftPM-based macOS apps without an Xcode project. | [release-macos-spm-packaging](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/release-macos-spm-packaging/SKILL.md); harvested from kit (own fleet) |
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
| `xcode-disk-cleanup` | Low disk space on a Mac that builds — auditing DerivedData, simulator runtimes and devices, archives, dSYMs, DeviceSupport and stale caches against evidence of what still uses them, then cleaning behind an approval gate. | [AvdLee/Xcode-Disk-Cleanup-Agent-Skill](https://github.com/AvdLee/Xcode-Disk-Cleanup-Agent-Skill) |
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
- `helm-asc` documents the third-party Helm app's `helm-asc` CLI, which is paid
  and not bundled. It was copied verbatim from the already-installed,
  already-working local skill rather than reconstructed from vendor
  documentation, so it describes what the operator's licensed copy actually
  does.
- `swiftui-liquid-glass` additionally cites Apple's WWDC25 session 323 for
  one design note.
- **A `rocketsim` skill used to ship here and no longer does.** It was
  consolidated onto FlowDeck along with the rest of this profile's
  simulator/build-verification surface — see [Build & test tooling
  (flowdeck)](/lacquer/guides/ios-rules/#build--test-tooling-flowdeck).

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
