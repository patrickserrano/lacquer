## Battery & Performance Patterns

Apply these whenever touching widgets, animations, networking, or background
work — see the `ios-performance-battery-patterns` skill for the concrete
patterns (Timeline entry limits, animation cleanup, Low Power Mode guards,
constrained-network config, observer/task cleanup under `@Observable`).

## Swift 6 Concurrency & Default Actor Isolation

**Swift 6 language mode is the baseline, in every build configuration — not just the app target.** `SWIFT_VERSION = 6` and `SWIFT_TREAT_WARNINGS_AS_ERRORS = YES` are asserted by the lacquer and checked two ways: `lacquer audit` reads the pbxproj statically across every configuration, and the CI `Baseline` job reads the effective settings via `xcodebuild -showBuildSettings`. Below Swift 6, data-race diagnostics are warnings rather than errors, so violations accrue invisibly until the migration has to happen as one large risky change. A target left behind (tests, widget, watch app) reports as a coverage ratio like `4/12`, not as a pass. Genuine exceptions go in `[baseline.relax]` with a reason and an expiry.

If the app target sets `SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor` (approachable concurrency), classes without an explicit isolation annotation — **including services** — are implicitly `@MainActor`.

- `await urlSession.data(for:)` still does its network I/O **off** the main thread; the suspension yields. Only the synchronous work around it (e.g. JSON decoding) runs on the main actor — fine at small payload sizes.
- If a method does **heavy synchronous work** (large decode, image processing, crypto), mark that method (or the type) `nonisolated` / `@concurrent` **deliberately** so it runs off-main.
- **NEVER** reach for `@unchecked Sendable` or `nonisolated(unsafe)` to silence a diagnostic. Fix the root cause: make the type a value type, isolate it to an actor, or make stored state immutable.

## iOS 26 API Gotchas

- **Mini-player / bottom accessory:** the shipping API is `.tabViewBottomAccessory { ... }` — **not** `.tabViewAccessory`.
- **Tab-bar morphing search:** declare the search tab with `Tab(role: .search)` and use `.searchable(text:prompt:)` with automatic placement. `SearchFieldPlacement.tabBar` **does not exist** in the iOS 26 SDK.
- **Naming:** name your tab enum `AppTab` (or similar) — a type named `Tab` shadows SwiftUI's `Tab` builder struct and breaks the `TabView` content.

## URL Validation Security Posture

Validate every user-provided URL before it reaches `AVPlayer`, `URLSession`,
or a `WKWebView` — see the `url-validation-security` skill for the
positive-allowlist validator and where to apply it.

### Spotlight must never index build output

Every directory that receives build output carries an empty
`.metadata_never_index` file, and it is created **before** the build that
populates it — not after, or the tree is indexed once on the way in.

That covers `DerivedData`, any `DerivedData-*` variant, `WatchDerivedData`,
`~/Library/Developer/Xcode/DerivedData`, `~/Library/Developer/Xcode/Products`
and `~/Library/Developer/CoreSimulator/Devices`.

Measured on the dedicated runner, 2026-09-11: **~215GB of regenerable build
output was being indexed** — 133G of simulator devices, 59G of shared
DerivedData, 22G across ten per-project trees — with `mdbulkimport` running a
full reindex and load average at **99**. Excluding it took `mds*` from 34% CPU
across nine processes down to 12%.

The managed CI workflow does this for the paths it owns. A script, skill or
local build that points `-derivedDataPath` somewhere new owns the marker for
that path:

```sh
mkdir -p "$DD" && : > "$DD/.metadata_never_index"
```

Two reasons this keeps coming back rather than staying fixed. The output is
**regenerable**, so it gets deleted and recreated constantly and a one-time
exclusion does not survive — the marker has to be created at the same moment
the directory is. And the symptom does not name its cause: a machine at load 99
looks like too many builds, not like a search index quietly walking a hundred
gigabytes of object files.

## Verifying UI in the Simulator

Read the screen with `rocketsim elements --agent-mode nav` (or `act` when you
need values and enabled-state) and act on it by element id. Measured on a real
screen: **738 bytes, about 184 tokens, for a fourteen-element snapshot.** That is
cheap enough to take one before and after every step, which is what makes
batching several interactions into one round trip practical — and it is a much
better default than a screenshot-and-read loop, which costs orders of magnitude
more for a less precise answer.

**The agent view is the default; a screenshot is the fallback, not a co-equal
option.** In order:

1. **Read the tree** — `rocketsim elements --agent-mode`, above. Reach for this
   first, every time.
2. **Where RocketSim is not installed**, `flowdeck ui simulator screen --tree
   --json` returns the same kind of text without capturing an image. An
   alternative, not a second default.
3. **Fall back to a screenshot** when the question is genuinely visual — layout
   and spacing, contrast, comparing against a design mockup — or when the tree
   cannot express the answer.

This is not RocketSim over FlowDeck: FlowDeck stays mandatory for build, run,
test, logs and simulator management, and this rule governs only how an agent
reads and drives the UI. A skill whose deliverable is an actual image (App Store
screenshots, a recorded animation) still captures one — it just does not read
a screenshot to answer a question the tree already answers.

The Simulator itself is worth watching, just not by the agent: `rocketsim
preview` streams it to a local browser page (the CLI's `context.preview_url`)
so a human can follow along live. That is for the person, not the model — it
costs the agent no tokens, and it is why the agent has no need to relay
screenshots just so someone else can see progress.

Element ids are ephemeral: they are stable **within one snapshot** and not across
them. Re-snapshot before acting on an id you did not just read.

### `element_disabled` does not mean the element is disabled

```
$ rocketsim interact tap --id 23        # "Sync with iCloud"
error: element_disabled
        "The matched element did not change state after tapping."
```

That element was reported `enabled` in the snapshot immediately before, and it is
not broken. It is a control that deliberately never changes state: tapping it
presents a sheet. The error CODE is a misnomer; the MESSAGE is accurate, and the
message is the part to read.

This matters because the obvious recoveries are both wrong. Waiting for it to
become enabled waits forever, and looking for "a valid target" sends you hunting
an element that does not exist.

**The recovery is `interact activate`**, which performs an accessibility press
rather than a HID touch:

```
$ rocketsim interact activate --id 23
ok: true   screen_changed: true     # the sheet appears; the toggle stays at 0
```

Verified against Shelf Life's iCloud sync toggle on 2026-09-11 with RocketSim
16.4.6: `tap` returns `element_disabled`, `activate` presents the sheet, and the
checkbox value is unchanged at `0` throughout because changing it was never what
the control did.

Reach for `activate` whenever a tap reports no state change on an element the
snapshot says is enabled. Reach for `tap` when you specifically need a real touch
— hit-testing behaviour, gesture recognisers, anything where the accessibility
press would bypass what you are testing.

### The snapshot reports accessibility defects, and they are findings

A snapshot can carry a `!perception` row:

```
!perception|zero_size_elements_omitted|12 omitted|Elements with accessibility
content but zero-size frames were omitted.
```

Twelve elements carrying accessibility content that VoiceOver can reach and
nothing can hit-test. That is a real accessibility defect in the app, surfaced
for free by a tool you were using for something else — treat it as a finding
rather than as snapshot noise. `!ambiguous` rows are the same shape: two elements
a selector cannot tell apart, which is usually a missing accessibility
identifier.

## Accessibility & Design-Token Contrast (WCAG 1.4.11)

Audit **non-text** contrast, not just text. Ship two distinct boundary tokens and use them for their intended roles:

- `controlBorder` — **≥ 3:1 against its own background**, which lands near white @ 40% on a dark ground — for the boundary of an **interactive** control (button outline, text-field border, selected chip).
- a decorative hairline — ~white @ 8% — for dividers and separators that carry no meaning.

**The ratio is the requirement; the percentage is only a starting point.** White at a
fixed opacity does not have a fixed contrast — it composites against whatever sits
behind it, so one token passes on one ground and fails on another. Across the dark
backgrounds this fleet actually uses, the opacity that first reaches 3:1 ranges from
**33% to 37.5%**, and 40% is the lowest round value clearing every one of them:

| ground | @ 30% | @ 36% | @ 40% |
|---|---|---|---|
| pure black `#000000` | 2.45 | 3.14 | 3.66 |
| Apple dark `#1C1C1E` | 2.71 | 3.34 | 3.80 |
| `#2C2C2E` | 2.62 | 3.16 | 3.54 |
| `#3A3A3C` | 2.47 | **2.92** | 3.25 |

This rule used to read "~white @ 30% opacity, **≥ 3:1**", and those two halves cannot
both hold: 30% fails on every ground above. A project that followed the percentage got
a border failing the ratio the same sentence demanded. That is exactly what happened in
Rail — the design system never defined the token at all, so a view hand-rolled
`white.opacity(0.30)` straight from this line and shipped 2.68:1.

Note the failure is worst on the *lighter* dark grounds, not the darkest: at 36% pure
black passes and `#3A3A3C` does not. Picking a percentage by eye on one screen is how
this goes wrong.

So **measure each token against the surface it actually sits on, and assert it in a
test.** A ratio written only in a doc comment cannot fail, and two of Rail's were
wrong for months.

Other rules:
- Use a **saturated** `controlAccent` for controls that sit against a **white system thumb** (e.g. `Toggle`). A near-white accent fails ~3:1 against the white thumb and reads as "off" to low-vision users.
- Selection states must be **non-color-redundant**: show a checkmark / icon, not just a colored ring or tint, so the state survives color-blindness and grayscale.
