# Project Baseline Design

**Goal:** Give lacquer a way to *assert* project-level invariants — Swift language
mode, warnings-as-errors, strict concurrency — instead of merely observing and
propagating whatever a project already happens to declare.

**Status:** Design approved 2026-07-26. Implementation follows in this branch.

---

## Why this exists

throughline was onboarded onto lacquer on 2026-07-13 and has been building in
Swift 5 language mode ever since, accruing Swift 6 violations as warnings while
CI reported green. Its `ios/Throughline.xcodeproj/project.pbxproj` declares
`SWIFT_VERSION = 5.0` in all 12 build configurations, while simultaneously
setting `SWIFT_APPROACHABLE_CONCURRENCY = YES` and
`SWIFT_DEFAULT_ACTOR_ISOLATION = MainActor` — the Swift 6 ergonomics switched on
underneath a language mode where every diagnostic they produce is a warning.

Five whys:

1. **Why is throughline on Swift 5?** The pbxproj said `5.0` at scaffold time and
   nothing ever changed it.
2. **Why did nothing change it?** `swift_version` is a *detected observation* in
   lacquer, not an asserted standard. Its only consumer in the entire harness is
   `profiles/ios/config/.swiftformat:1` → `--swiftversion {{SWIFT_VERSION}}`. It
   configures a formatter. Nothing validates it.
3. **Why detection-only?** `internal/detect/detect.go:19` scrapes `SWIFT_VERSION`
   only from an XcodeGen `project.yml`. throughline has no `project.yml`, so
   detection returned empty; because `internal/tokens/tokens.go:39` marks the
   token *required* (fail-closed), `sync` refused to run until a value was
   hand-filled. The value that silenced the error was the one already in the
   pbxproj. **The fail-closed check demanded a number, not a correct number, and
   so laundered the wrong answer into the manifest.**
4. **Why didn't the audit path catch it?** The three skills that audit build
   settings are explicitly gagged on precisely this class of setting —
   `xcode-project-analyzer`, `xcode-build-fixer`, and `xcode-build-orchestrator`
   all carry *"Do not flag language-migration settings like
   `SWIFT_STRICT_CONCURRENCY` or `SWIFT_UPCOMING_FEATURE_*` — those are developer
   adoption choices unrelated to build speed."* Meanwhile `lacquer audit` only
   classifies template drift. No auditor's job included this question.
5. **Root cause:** lacquer has three buckets — content it renders, tokens it
   substitutes, drift it detects — and **no concept of an asserted project
   baseline**. Language mode landed in the token bucket, which is descriptive by
   design. A slot that asks "tell me what you are" can never say "you're wrong."

**Second root cause, same shape:** `core/CLAUDE.core.md:137` has a "Warnings as
Errors" section instructing zero-warning builds, but the harness contains no
`SWIFT_TREAT_WARNINGS_AS_ERRORS` and no compiler-warning CI gate anywhere. The
only mechanical enforcement is SwiftLint `--strict` and SwiftFormat `--lint`.
Everywhere else this harness pairs a principle with a mechanism — it even guards
against "SwiftLint matched no files and silently passed." Here the mechanism was
never built, so the principle degraded into a suggestion addressed to an agent.

## Rejected alternatives

- **lacquer auto-writes the settings into the pbxproj.** Self-healing, but makes
  lacquer a pbxproj mutator: a 7000-line generated file, 12 build configs, no
  round-trip parser in Go today, and a corruption risk on every sync.
- **A managed `Baseline.xcconfig` synced asset.** Elegant — existing
  region/lock/audit machinery would cover drift for free — but base-config
  chaining is manual and fiddly (throughline already chains
  `Secrets.xcconfig`), and a pbxproj setting silently overrides an xcconfig.
- **CI-only command-line overrides.** One-line change, but local Xcode would
  still build Swift 5 clean and errors would appear only in CI. That dev/CI
  divergence is the failure mode being fixed, not a fix for it.
- **A per-project `[baseline]` block written by `lacquer init`.** Rejected
  because init derives from what the project already declares, so a Swift 5
  project would get a Swift 5 baseline and pass its own audit — the original bug
  in a new costume.
- **A hardcoded Go constant with no escape hatch.** The first project with a
  legitimate reason to lag would delete the CI job, removing the check for
  everyone.

## Architecture

### The standard is harness-owned

`profiles/ios/baseline.toml` holds the asserted standard. It is harness-side
policy, **not a synced asset** — modeled on `core/bootstrap/plugins.toml`, which
already establishes this shape. Per-profile, so a future `macos` profile can
differ and `web`/`supabase` simply have no `baseline.toml` and run no checks.

```toml
[baseline]
swift_version      = "6"
warnings_as_errors = true
strict_concurrency = "complete"
```

Every project inherits this by default, which is what makes a fresh
`lacquer init` unable to scaffold Swift 5 ever again.

### Projects may relax, never redefine

`.lacquer.toml` gains relaxations only:

```toml
[baseline.relax]
swift_version = { until = "2026-09-01", reason = "pre-Swift-6 audio engine, tracked in #142" }
```

Both `until` and `reason` are required. An expired `until` is a hard violation
with its own message, so debt cannot quietly become permanent policy. Config
validation **rejects unknown relax keys** — otherwise a typo (`swift_verison`)
becomes a silent permanent exemption, reproducing this entire bug in a new
costume. Absence of a relaxation means the standard applies.

### Types

```go
type Spec struct {
    SwiftVersion      string `toml:"swift_version"`
    WarningsAsErrors  bool   `toml:"warnings_as_errors"`
    StrictConcurrency string `toml:"strict_concurrency"`
}

type Relax struct {
    Until  string `toml:"until"`  // YYYY-MM-DD, required
    Reason string `toml:"reason"` // required, non-empty
}

type Finding struct {
    Key, Want, Got string // Got == "<unset>" when undeclared
    Configs, Total int    // "declared in 8 of 12 build configurations"
    Status         Status // ok | violation | relaxed | expired
    Relax          *Relax
}
```

### Reading what the project declares

`internal/baseline` reads declared settings **without Xcode**, so `lacquer audit`
stays fast and portable:

- Scan `project.pbxproj` for `SWIFT_VERSION = <v>;` per `XCBuildConfiguration`,
  counting matches against the total build-config count. This is what surfaces
  throughline's "12 configs" and, more importantly, catches the nastier partial
  case: set in Debug, unset in Release.
- When a `project.yml` exists it takes precedence — the pbxproj is generated from
  it, so the pbxproj is a build artifact there.
- A setting can also legitimately arrive via an xcconfig that lacquer cannot
  resolve, so before reporting something unset, grep `*.xcconfig` within the
  component. This keeps the check from crying wolf on xcconfig-driven projects.

This is deliberately a *different fidelity* from the CI check below. lacquer sees
what is declared; CI sees what is effective. Two independent checks at different
fidelity is the same defense-in-depth shape `ci.yml` already uses for the
local-SPM-cache / `actions/cache`-validate pair.

## Enforcement points

| Surface | Behavior |
| --- | --- |
| `lacquer audit` | New `baseline:` section; **exit 4** on violation. Exit 3 (would-clobber) keeps precedence when both fire — data loss outranks policy. |
| `lacquer status` | Reports the baseline informationally; no exit change. |
| `lacquer init` | **Stops deriving `swift_version` from the project.** Writes the Spec's value and warns when the project's declared value disagrees. Detection becomes a diagnostic, not the source of truth. This is the direct fix for why throughline's manifest ever said `5.0`. |
| `{{SWIFT_VERSION}}` | Populated from the Spec, not from detection. **Consequence:** on throughline's next sync, `.swiftformat` flips `--swiftversion 5.0` → `6`. Correct, but a real change to a file that project does not exclude. |
| `ci.yml` | New `baseline` job on the self-hosted Mac asserting *effective* values via `xcodebuild -showBuildSettings`. Same fork guard and `changes.outputs.code` gate as `lint`. **Must be added to the `CI OK` aggregator's `needs[]` and its result loop** (`ci.yml:687`) or it will not be a required check. |

## Prose repairs

So that principle and mechanism are paired the way they are everywhere else here:

- `core/CLAUDE.core.md:137` §Warnings as Errors — cite the mechanism now backing it.
- `profiles/ios/CLAUDE.ios.md:200` — assert Swift 6 language mode rather than
  describing *"if the app target sets…"*.
- Narrow the gag clause in the three auditor skills from "do not flag
  language-migration settings" to "not a build-*performance* finding — report
  under Baseline instead." The clause was defensible as written and became
  "not a finding at all."
- Regenerate `site/` via `site/scripts/generate-skill-docs.mjs` rather than
  hand-editing the mirrors.
- Bump `VERSION`.

## Testing

TDD, table-driven to match the existing `*_test.go` style:

- Spec load: present, absent (profile with no baseline), malformed.
- pbxproj read: all configs, partial configs, unset, xcconfig-supplied,
  `project.yml` precedence.
- Relaxations: valid, expired, unknown key, missing reason, unparseable date.
- Audit exit-code precedence: clobber+violation → 3, violation alone → 4.

## Out of scope

- **throughline is not fixed by this branch.** It stays in Swift 5 until the new
  `audit` is run against it and its pbxproj is corrected — deliberately, since
  flipping language mode will surface a backlog of real errors that wants its own
  change.
- `internal/detect`'s `project.yml` scrape is repurposed as the init-time
  warning, not otherwise reworked.
- No pbxproj mutation, now or as a follow-up, without a separate design.
