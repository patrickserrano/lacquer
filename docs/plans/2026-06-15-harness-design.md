<!-- Generated: 2026-06-15 -->

# Developer Harness — Design

A central system that standardizes how Claude Code works across every project in
`~/developer`, lets projects learn from each other, and makes onboarding a new
project (or a new component within a project) a single command.

## Problem

Today the `ios-template` repo is a **fork-once** template: a new project copies it,
runs `FIRST_RUN.md`, and immediately begins to drift. Hard-won lessons are scattered
across three places — the template's `CLAUDE.md`, the global `~/.claude/CLAUDE.md`,
and the auto-memory `MEMORY.md` — and a lesson learned in one project (e.g. an
AVAudioEngine teardown fix in `journalcast`) never reaches the template, let alone
`rail` or `frequency`. There is no bidirectional loop. Dependency and tooling
upgrades are handled by hand, per project.

The harness closes that loop: **learn locally → harvest up → sync down everywhere.**

## Decisions

| Decision | Choice |
|----------|--------|
| Distribution | Central `harness/` repo + `harness` CLI as the single source of truth |
| Harvest (project → harness) | PR-gated promotion |
| Sync boundary in mixed files | Managed-region markers |
| Scope | All stacks, layered: universal `core` + per-stack profiles |
| Opt-in unit | **Component** (a subdirectory), declared in `.harness.toml` |
| `ios-template` fate | Absorbed: its CLAUDE.md → core + ios profile; skills/CI → ios profile; FIRST_RUN → `harness init` |
| App-dependency upgrades | Scaffolded Renovate config extending a shared harness preset |
| Tooling-version upgrades | Harness-pinned, propagated by sync |

## Core insight: a project is a set of components

A project is **not** one stack. `journalcast` = `ios/` (ios) + `dashboard/` (web).
Daily bread = `ios/` (ios) + `proxy/` (web). The harness operates on **components**,
each declaring its own profiles. `core` applies to every project regardless.

```toml
# journalcast/.harness.toml
[project]
name = "journalcast"

[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "dashboard"
profiles = ["web"]
```

## Repository structure

```
harness/
├── core/                      # applies to EVERY project, every stack
│   ├── CLAUDE.core.md         # git hygiene, atomic commits, PR discipline,
│   │                          #   worktree rules, "pre-existing failures are
│   │                          #   yours", harvest/sync usage
│   ├── skills/                # stack-agnostic skills
│   └── commands/              # e.g. /sync-worktree
├── profiles/
│   ├── ios/
│   │   ├── CLAUDE.ios.md       # @Observable/NavigationStack rules, iOS 26
│   │   │                       #   gotchas, battery patterns, URL validator…
│   │   ├── skills/             # the 23 Xcode/Swift/SwiftUI skills
│   │   ├── commands/           # /build /test /lint-fix /new-feature
│   │   ├── workflows/          # ios CI, release, dead-code, audit
│   │   ├── config/             # .swiftlint.yml, .swiftformat, .periphery.yml
│   │   ├── Brewfile            # pinned dev tooling (swiftlint, swiftformat…)
│   │   └── renovate.json5      # ios app-dependency upgrade preset
│   ├── web/                    # (seed later: lint, vercel CI, ts rules)
│   ├── rust/
│   └── go/
├── templates/
│   └── ios-scaffold/           # the empty Xcode project + FIRST_RUN content
├── renovate-base.json5         # shared Renovate policy extended by all components
├── registry.json               # fleet view: projects, components, synced versions
└── bin/harness                 # the CLI: init · new · scaffold · sync · status · harvest · upgrade
```

**Layering rule:** a component always gets `core` + each profile it names. `core`
owns universal discipline; profiles own stack specifics. Nothing iOS-flavored leaks
into `core`.

## Asset placement (by consumer expectations)

`harness sync` walks each component and places that profile's assets into the
component's directory, while `core` lands once at the repo root. Placement differs
by asset type because the tools that consume them have fixed expectations:

| Asset | Where it lands | Why |
|-------|---------------|-----|
| `core` rules | root `CLAUDE.md` managed-region | loaded everywhere in the repo |
| profile rules | `<component>/CLAUDE.md` managed-region | Claude Code auto-loads nested CLAUDE.md in that subtree |
| skills + commands | root `.claude/` (union of core + all profiles) | selected by description-relevance; iOS skills don't fire on web work |
| CI workflows | root `.github/workflows/`, **path-gated** + stack-prefixed (`ios-ci.yml`, `web-ci.yml`) | GitHub requires workflows at root; path-gating runs each only when its component changes |
| stack config (`.swiftlint.yml`, `eslint`…) | `<component>/` | the linter looks beside the code |
| Renovate config | root, scoped per component path | one bot config covers all components |

## Versioning

The harness repo is tagged (`v1`, `v2`…). Each managed-region marker records the
version it was written from:

```
<!-- harness:ios:start v4 -->
…shared iOS rules…
<!-- harness:ios:end -->
```

`harness status` diffs a component's stamped version against the harness's latest
tag, so behind-the-times projects are visible at a glance; `registry.json`
aggregates this across the fleet.

## Sync (harness → project), per component

1. Resolve `core` + the component's profiles.
2. **Whole-file assets** (skills, commands, configs, Renovate preset, CI workflows) → overwrite. Local edits here are an anti-pattern; harvest exists so you never need to fork them locally.
3. **Managed-region assets** (`CLAUDE.md`) → rewrite only between markers; project-owned text is never touched.
4. If a whole-file asset has **uncommitted local changes**, stop and report rather than clobber. Sync is never destructive to unsaved work.
5. Re-stamp versions; update `registry.json`.

## Harvest (project → harness), PR-gated

1. `harness harvest` scans the project's `MEMORY.md` and the *project-owned* parts of `CLAUDE.md` for candidate learnings.
2. You classify each: universal (`core`) vs stack-specific (a profile) vs purely local (stays put).
3. It branches the harness repo, drops each promoted learning into the right layer, and opens a PR.
4. On merge + tag, the next `harness sync` in every other project picks it up.

This is the only path by which the shared base grows — keeping it high-signal.

## Dependency upgrades — two hands-off tracks

1. **Harness-pinned tooling** (SwiftLint/Format versions, GitHub Action SHAs,
   pre-commit revs, eslint-config). These live in profile-owned files (`Brewfile`,
   workflow YAML, `.pre-commit-config.yaml`) and are whole-file synced. Bump once
   in the harness → `sync` propagates the identical pin everywhere. This enforces
   the "consistent Action versions across all workflows" rule mechanically instead
   of by vigilance.

2. **App dependencies** (SPM/npm/cargo/go). Each component gets a Renovate config
   that `extends` the shared `harness//renovate-base` preset, scaffolded at sync
   time and path-scoped per component. Grouped, scheduled upgrade PRs arrive
   automatically; CI gates them; patch-level can auto-merge. Because Renovate
   watches the `extends` reference, improving the base preset propagates to every
   project with no `sync` needed — a second, independent propagation channel.
   `harness upgrade [--all]` remains the manual escape hatch for an immediate bump.

## CLI surface

```
harness init                 # adopt existing repo: detect components, write
                             #   .harness.toml, first sync (wraps old FIRST_RUN)
harness new <name> --with ios,web    # brand-new multi-component repo
harness scaffold <stack> <path>      # add a component to an existing repo
                                     #   (e.g. harness scaffold web dashboard)
harness sync [--component X]         # pull core+profiles; managed-region safe
harness status                       # which components are behind which version
harness harvest                      # open PR promoting local learnings upward
harness upgrade [--all]              # on-demand dep bump fallback (build+test+PR)
```

## Rollout (high level)

1. Stand up the harness repo skeleton (`core/`, `profiles/ios/`, `bin/harness`).
2. Absorb `ios-template`: split its `CLAUDE.md` into `core/CLAUDE.core.md` +
   `profiles/ios/CLAUDE.ios.md`; move the 23 skills, commands, CI workflows, and
   config files into `profiles/ios/`; convert `FIRST_RUN.md` into `harness init`.
3. Implement the CLI: `init`, `sync`, `status` first (the read/apply loop), then
   `harvest`, then `scaffold`/`new`, then `upgrade`.
4. Onboard one existing iOS project (e.g. `rail`) end-to-end as the proof.
5. Seed `web`/`rust`/`go` profiles as real components demand them (YAGNI).
6. Retire the standalone `ios-template` repo once `profiles/ios` is authoritative.

## Resolved implementation decisions

- **`bin/harness` is written in Go** — single static binary, zero runtime deps,
  travels cleanly to CI runners; strong TOML/JSON parsing and file-merge ergonomics
  for the managed-region logic. Built with `go build -o bin/harness ./cmd/harness`.
- **`registry.json` is regenerated, not committed** — `harness status` scans sibling
  directories for `.harness.toml` + stamped marker versions. A committed registry is
  a second source of truth that goes stale; the fleet *is* whatever is on disk.
- **Renovate auto-merge policy** — auto-merge patch-level and pinned dev-dependency
  updates only, once CI is green; minor/major updates open a PR for review.
  Conservative by default, loosenable per-stack later.

## Security discipline (every step)

The harness reads untrusted inputs (a project's `.harness.toml`, existing
`CLAUDE.md` files, files inside cloned repos) and writes into many repos — and its
synced `CLAUDE.md` content becomes trusted LLM instructions. A vulnerability here
propagates across the whole fleet. So security is a **per-step gate**, not a final
pass:

1. **Per-commit:** the automated commit security review stays enabled; treat its
   findings as blocking.
2. **Per-plan:** every implementation plan ends with a **Security audit** task
   before the branch is finished.
3. **Per-PR:** run `/security-review` on the branch diff and resolve every finding
   at confidence ≥ 8 before merge.
4. **Bypasses count:** a fix that only partially closes a class (e.g. a symlink
   guard that checks the final path element but not parent dirs) is a finding until
   the whole class is closed. Path handling that touches the filesystem must confine
   the *resolved* path within the project root (see `internal/safepath`).
