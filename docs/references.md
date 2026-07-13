# External references

Running log of external articles/discussions that informed a harness
decision, or that surfaced a real gap not yet acted on. Not a synced
convention — this is the lacquer repo's own research trail, alongside
`docs/plans/`.

Each entry: source, date found, what it covers, what (if anything) was
adopted, and what's still open.

---

## Building/shipping Mac and iOS apps without opening Xcode

- **Source**: [scottwillsey.com](https://scottwillsey.com/building-and-shipping-mac-and-ios-apps-without-ever-opening-xcode/), discussed on [Hacker News](https://news.ycombinator.com/item?id=48896665)
- **Found**: 2026-07-13
- **Covers**: `xcodebuild`/`xcodegen`/`xcrun notarytool`/`xcrun stapler`/`codesign`/`spctl`/`devicectl` as a full CLI-only Mac/iOS build-sign-notarize-ship pipeline, driven end to end by Claude Code through a plain non-interactive shell.
- **Adopted**: `profiles/ios/skills/release-macos-spm-packaging/SKILL.md` was missing the `xcrun notarytool store-credentials` one-time setup step and post-release verification (`codesign -dv --verbose=4`, `spctl -a -vvv -t exec`, `xcrun stapler validate`) — added both, plus the "needs full Xcode.app, not just Command Line Tools" caveat (also raised independently in the HN thread).
- **Still open, not acted on** (no current fleet evidence, so not built speculatively — matches the harness's own `macos-ci-recipes` precedent of waiting for real usage before shipping a recipe):
  - A Developer-ID-notarized **direct distribution** pipeline (outside the App Store) for a macOS app that **does** have an Xcode project. `release-macos-spm-packaging` only covers the no-`.xcodeproj` SwiftPM path; `release.yml` only covers App Store/TestFlight submission via the App Store Connect API. Nothing currently covers "Xcode-project macOS app, shipped as a notarized `.zip`/`.dmg` outside the App Store." Revisit if/when a fleet project actually needs this.
  - The article's XcodeGen pattern (gitignore `.xcodeproj`, regenerate from `project.yml` via `xcodegen generate` on every build) is **not** this repo's current model — the harness assumes a committed `.xcodeproj` (`internal/detect`, the `{{XCODEPROJ}}` token, the `.claude/settings.json` hook blocking `.pbxproj` edits). `project.yml` is already referenced in `CLAUDE.ios.md`'s Secrets section, but as a committed input file, not a gitignored-and-regenerated one. Switching models would be a real architectural change, not a docs tweak — don't do it opportunistically.
- **HN thread gotchas worth remembering** (not yet acted on anywhere):
  - Running an AI agent with broad Mac/filesystem access is a real, repeatedly-raised risk (cited example: a home directory including SSH keys getting uploaded by an agent). Mitigations discussed: a separate restricted user, a VM/container (Tart, VirtualBuddy, Apple containers), Secure Enclave-backed SSH keys (Secretive.dev). Directly relevant to how this repo already reasons about subprocess-invoking rescue skills (see `core/skills/antigravity-rescue/SKILL.md`'s Security section) — worth keeping in mind if a similar sandboxing question comes up again.
  - XcodeGen has known reliability gaps on complex projects (widgets, watchOS, notification extensions) — another reason not to switch this repo's model opportunistically.
  - Sideloaded/ad-hoc-signed apps expire weekly/biweekly and need re-signing — not relevant to this fleet's TestFlight/App-Store-based distribution today, but worth knowing if that ever changes.

---

## Agentic device-automation / Apple-dev toolkits (Axiom, argent, agent-device)

- **Sources**:
  - [Axiom](https://charleswiltgen.github.io/Axiom/) ([tools page](https://charleswiltgen.github.io/Axiom/tools/)) — Charles Wiltgen
  - [software-mansion/argent](https://github.com/software-mansion/argent)
  - [callstack/agent-device](https://github.com/callstack/agent-device)
- **Found**: 2026-07-13
- **Covers**:
  - **Axiom** — a large (158 discipline + 80 reference + 26 diagnostic skills, 41 agents, 15 commands), MIT-licensed, Claude-Code/Codex-oriented collection specifically for modern Apple OS dev (Swift 6, SwiftUI, Liquid Glass, Apple Intelligence). Ships four bundled CLI tools: `xclog` (console capture as JSON), `xcprof` (Instruments trace capture/analysis, wraps `xctrace`), `xcsym` (crash symbolication — `.ips`/MetricKit/`.crash`/`.xccrashpoint`, automatic dSYM discovery), `xcui` (simulator UI/accessibility automation, delegates to AXe for HID input). Still in preview (v27.0.0-beta.22).
  - **argent** (Software Mansion) and **agent-device** (Callstack) — both cross-platform (iOS + Android, some TV/desktop/web) agentic device-control toolkits aimed at AI coding agents; both are primarily React Native-tooling vendors and both tools are RN/Hermes-aware (React DevTools profiling, RN element trees) alongside native support. Different packaging: argent is an MCP server (`npx @swmansion/argent init`); agent-device is a standalone npm CLI with an optional MCP server. Both explicitly position themselves against Appium/Detox/Maestro as "AI-agent-native" rather than traditional automation frameworks.
- **Assessment, not yet acted on**:
  - **argent / agent-device**: likely **not** a good fit for this fleet as-is — this fleet is 100% native SwiftUI/Swift, no React Native anywhere, and both tools' core differentiator (RN/Hermes awareness, Android/TV/Electron breadth) is dead weight here. `flowdeck` (already the mandated tool for all Apple-platform work per `CLAUDE.ios.md`) and `rocketsim` (already a skill) cover the same native-simulator-automation ground more directly. Not recommending adoption unless the fleet ever picks up a cross-platform (RN/Flutter) project.
  - **Axiom**: a genuinely different case — worth a real look before deciding, not dismissed. Two angles, not yet investigated in depth:
    1. **Overlap with the existing `profiles/ios/skills/` set.** Several current skills (`core-data-expert`, `swift-concurrency`, `swift-testing-expert`, `swiftui-expert-skill`, the `xcode-build-*` family) were themselves traced this session to AvdLee's individual per-topic repos (see the skills catalog's Source column) — Axiom may cover the same ground at a different (larger, maintained-as-one-project) grain, which could mean either useful expansion or duplicate content to reconcile.
    2. **`xcsym` (crash symbolication)** looks like a genuinely new capability with no current equivalent anywhere in this repo — `native-app-profiling` covers Instruments/performance, but nothing here handles `.ips`/MetricKit crash report symbolication and triage today.
  - Given the size (~320 items) and that it's still in beta, this deserves a deliberate look (probably a dedicated research/comparison pass) rather than a speculative pull-in — flagging here so it doesn't get lost, not building anything yet.
