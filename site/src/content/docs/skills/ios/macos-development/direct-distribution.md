---
title: "Direct Distribution (Developer ID, Outside the App Store)"
description: "Reference for the macos-development skill."
---

:::note
Generated from [`profiles/ios/skills/macos-development/references/direct-distribution.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/macos-development/references/direct-distribution.md) — edit that file, not this page.
:::

# Direct Distribution (Developer ID, Outside the App Store)

Code signing, notarization, packaging, and auto-updates for a macOS app
shipped directly (Developer ID) rather than through the Mac App Store. This
covers the signing/notarization/packaging *mechanics*, which apply the same
way regardless of how the app was built.

**If the project is pure SwiftPM (no `.xcodeproj`)**, `release-macos-spm-packaging`
already owns the full scaffold → build → package → sign → notarize pipeline
end to end, including the `notarytool store-credentials` one-time setup and
post-release verification (`codesign -dv`, `spctl -a -vvv -t exec`,
`stapler validate`) — use that skill directly rather than re-deriving its
scripts from the material here.

**If the project has an `.xcodeproj`/`.xcworkspace`**, this fleet does not
yet have an equivalent end-to-end recipe (`release.yml` only covers App
Store/TestFlight submission via the App Store Connect API) — a real,
open gap. The signing/notarization concepts below are project-shape-agnostic
and apply either way, but the missing piece is specifically the
`xcodebuild archive`/`-exportArchive` step that produces the `.app` to sign in
the first place. Don't invent a full Xcode-project pipeline speculatively —
confirm there's a real fleet project that needs it first, matching this
repo's existing precedent (`macos-ci-recipes` waited for two real
implementations before shipping).

## Never do these

- **`altool` for notarization** — rejected since November 1, 2023. Use
  `notarytool` (Xcode 13+) or the Notary REST API.
- **`codesign --deep`** — applies identical entitlements/options/identity to
  *every* nested component. XPC services, frameworks, and helpers often need
  different entitlements (or none) — Apple's own guidance calls this
  "`--deep` Considered Harmful."
- **`sudo codesign`** — codesign depends on user account info; running as
  root breaks identity lookup.
- **Signing outer-to-inner** — the outer signature hashes the inner
  signatures. Sign inside-out or the outer signature is invalidated.
- **Skipping Hardened Runtime** (`-o runtime`) — mandatory for notarization;
  omitting it gets the submission rejected outright.
- **Shipping without stapling** — Gatekeeper falls back to contacting Apple's
  servers to verify notarization; an offline user gets blocked.
- **Leaving `com.apple.security.get-task-allow`** in distribution
  entitlements — allows debugger attachment; notarization rejects it.

## Code signing

Sign inside-out:

| Order | Component |
|---|---|
| 1 | Dylibs (`Contents/Frameworks/*.dylib`) |
| 2 | Frameworks (`Contents/Frameworks/*.framework`) |
| 3 | XPC services (`Contents/XPCServices/*.xpc`) |
| 4 | Helpers/tools (`Contents/MacOS/helper-tool`) |
| 5 | App extensions (`Contents/PlugIns/*.appex`) |
| 6 | Main app bundle |

```bash
codesign -f -s "Developer ID Application: <Name> (<TeamID>)" --timestamp -o runtime \
  MyApp.app/Contents/Frameworks/SomeFramework.framework
# ... repeat inside-out for XPC services, helpers ...
codesign -f -s "Developer ID Application: <Name> (<TeamID>)" --timestamp -o runtime \
  --entitlements MyApp.entitlements MyApp.app   # entitlements: main executable only, never libraries

codesign -v -vvv --strict --deep MyApp.app       # verify (--deep is fine for *verification*, not signing)
codesign -d -vvv MyApp.app                       # signing details
```

| Flag | Purpose | Required when |
|---|---|---|
| `-s "Developer ID Application: ..."` | Signing identity | Always |
| `-f` | Force re-sign | Re-signing previously signed code |
| `--timestamp` | Secure timestamp from Apple | Always, for notarization |
| `-o runtime` | Hardened Runtime | Always, for notarization |
| `--entitlements path` | Apply entitlements | Main executable only |
| `-i com.example.tool` | Set identifier | Non-bundled executables only |

## Hardened Runtime

Required for notarization; protects against code injection, DLL hijacking,
memory tampering. Most apps need zero exceptions — add only what's genuinely
required:

| Exception | Entitlement | Use case |
|---|---|---|
| JIT compilation | `com.apple.security.cs.allow-jit` | JS/regex engines |
| Unsigned executable memory | `com.apple.security.cs.allow-unsigned-executable-memory` | Legacy code — avoid if possible |
| DYLD environment variables | `com.apple.security.cs.allow-dyld-environment-variables` | Plugin hosts, debugging tools |
| Disable library validation | `com.apple.security.cs.disable-library-validation` | Loading third-party frameworks/plugins |
| Debugging tool | `com.apple.security.cs.debugger` | Instruments-like tools |

Resource entitlements (camera, mic, location, contacts, calendar, photos,
Apple Events) are the same set documented in `sandbox-and-file-access.md` —
direct distribution doesn't change which entitlement string to use, only
that the App Sandbox entitlement itself is optional outside the App Store.

## Notarization

```bash
# one-time credential storage
xcrun notarytool store-credentials "AC_PASSWORD" --apple-id you@example.com --team-id YOURTEAMID
# or with an App Store Connect API key:
xcrun notarytool store-credentials "AC_APIKEY" --issuer ISSUER_UUID --key-id API_KEY_ID --key /path/AuthKey_XXXX.p8

xcrun notarytool submit MyApp.dmg --keychain-profile "AC_PASSWORD" --wait
xcrun notarytool log <submission-id> --keychain-profile "AC_PASSWORD"   # check even on success — warnings matter
```

| Format | Staple-able | Notes |
|---|---|---|
| `.dmg` (UDIF) | Yes | Must be a signed DMG |
| `.pkg` (flat) | Yes | Must be a signed installer package |
| `.zip` | No | Staple the `.app` before zipping |

| Failure | Cause | Fix |
|---|---|---|
| "signature does not include a secure timestamp" | Missing `--timestamp` | Re-sign with `--timestamp` |
| "executable does not have hardened runtime enabled" | Missing `-o runtime` | Re-sign with `-o runtime` |
| "signature of the binary is invalid" | Wrong signing order, or modified after signing | Re-sign inside-out; don't touch the bundle after |
| "requests the com.apple.security.get-task-allow entitlement" | Debug entitlement in a distribution build | Strip it from the distribution entitlements file |

```bash
xcrun stapler staple MyApp.dmg      # or staple MyApp.app before zipping
xcrun stapler validate MyApp.dmg
```

Stapling cache errors: `sudo killall -9 trustd && sudo rm /Library/Keychains/crls/valid.sqlite3`.

## Packaging

```bash
# DMG — drag-to-install, staple-able, best for user-facing distribution
hdiutil create -volname "MyApp" -srcfolder MyApp.app -ov -format UDZO MyApp.dmg
codesign -s "Developer ID Application: <Name> (<TeamID>)" --timestamp MyApp.dmg

# Zip — simplest, no stapling; best for Sparkle updates/CI artifacts
ditto -c -k --sequesterRsrc --keepParent MyApp.app MyApp.zip   # ditto preserves symlinks/resource forks — never use Finder's own zip

# Installer package — daemons, launch agents, privileged helpers, multi-component products
productbuild --component MyApp.app /Applications MyApp-unsigned.pkg
productsign --sign "Developer ID Installer: <Name> (<TeamID>)" MyApp-unsigned.pkg MyApp.pkg   # note: Installer identity, not Application
```

## Auto-updates with Sparkle

MIT-licensed, EdDSA signatures, sandboxing support, silent background
updates — the standard choice for directly distributed macOS apps.

```swift
import Sparkle

final class CheckForUpdatesViewModel: ObservableObject {
    @Published var canCheckForUpdates = false
    let updater: SPUUpdater

    init() {
        let controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil)
        updater = controller.updater
        updater.publisher(for: \.canCheckForUpdates).assign(to: &$canCheckForUpdates)
    }
    func checkForUpdates() { updater.checkForUpdates() }
}
```

Wire into `.commands { CommandGroup(after: .appInfo) { CheckForUpdatesView(viewModel: updaterViewModel) } }`.

Setup: add via SPM, generate EdDSA keys once (`./bin/generate_keys` — the
public key goes in Info.plist as `SUPublicEDKey`, the private key lives in
Keychain), and add `SUFeedURL` (appcast URL) to Info.plist. For sandboxed
apps, add `SUEnableInstallerLauncherService` to Info.plist plus the XPC
temporary-exception entitlement:

```xml
<key>com.apple.security.temporary-exception.mach-lookup.global-name</key>
<array>
    <string>$(PRODUCT_BUNDLE_IDENTIFIER)-spks</string>
    <string>$(PRODUCT_BUNDLE_IDENTIFIER)-spki</string>
</array>
```

Publishing: `ditto`-zip the app, `./bin/generate_appcast /path/to/updates_folder/`,
upload the archive + delta files + `appcast.xml`. If re-signing Sparkle
manually (not via Xcode Archive/Export), sign its XPC services and
`Autoupdate`/`Updater.app` inside-out same as any nested component — never
`--deep` on Sparkle, its XPC services have different signing requirements.

| Info.plist key | Default | Purpose |
|---|---|---|
| `SUFeedURL` | required | Appcast URL |
| `SUPublicEDKey` | required | EdDSA public key |
| `SUEnableAutomaticChecks` | prompts user | `YES` to skip the permission prompt |
| `SUAutomaticallyUpdate` | `NO` | Silent background updates |
| `SUScheduledCheckInterval` | 86400 (1 day) | Minimum 3600 (1 hour) |

## Troubleshooting

**Gatekeeper blocks**: test on a clean Mac/VM. Isolate from other issues by
removing the quarantine attribute (`xattr -d com.apple.quarantine MyApp.dmg`)
— if it *still* fails, the problem isn't Gatekeeper, it's signing/runtime.
`syspolicy_check distribution MyApp.app` on macOS 14+.

**Dangling load command paths** — by far the most common Gatekeeper failure.
`otool -L MyApp.app/Contents/MacOS/MyApp` and look for absolute paths
(`/usr/local/lib/...`, build-directory paths); fix with `install_name_tool`
or `@rpath`-relative paths.

**Unicode normalization in zips** — Finder's Archive Utility can convert
precomposed filenames to decomposed form, breaking signatures. Use `ditto`,
stick to ASCII filenames in the bundle.

**cdhash mismatch** — if Gatekeeper logs show `ticket not available:
2/2/<cdhash>`, compare `codesign -d -vvv MyApp.app`'s `CDHash=` against the
notary log's cdhash; a mismatch means you're testing a different binary than
you notarized.

```bash
log stream --predicate "sender == 'AppleMobileFileIntegrity' or sender == 'AppleSystemPolicy' or \
  process == 'amfid' or process == 'taskgated-helper' or process == 'syspolicyd'"
```

Always test from a clean download (has the quarantine attribute), never from
the build directory (doesn't).
