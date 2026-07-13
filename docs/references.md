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
