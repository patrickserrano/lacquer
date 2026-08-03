# lacquer

A Go CLI plus a set of profile templates that standardize how Claude Code works
across every project in `~/Developer`. The lacquer renders shared content —
`CLAUDE.md` rules, skills, commands, CI workflows, git hooks, tool configs — into
each project and tracks how far each project has drifted, so a lesson pinned once
propagates everywhere instead of being copy-pasted and left to rot.

A project opts in per **component** (a subdirectory) via a `.lacquer.toml`
manifest. Each component declares one or more **profiles**; `core` applies to
every project regardless.

## Commands

| Command | Does |
|---------|------|
| `lacquer init` | Detect components, write a `.lacquer.toml` stub (and a `docs/brief.md` stub). |
| `lacquer onboard --org O [--no-repo]` | `init`, then create a private GitHub repo under `O` when the repo has no `origin`. |
| `lacquer sync [--force] [--fix]` | Render core + per-profile content into the project (managed regions + whole-file assets); `--fix` then runs the autofixers. |
| `lacquer fix` | Run each profile's autofixers (formatter, `lint --fix`) over the project source. |
| `lacquer doctor` | Prove each check can fail: feed known-bad input and assert it's rejected (exit 5 if one can't). |
| `lacquer skills` | Install `[project].skills` entries via the [`skills` CLI](https://github.com/vercel-labs/skills). |
| `lacquer plugins` | Install `core/bootstrap/plugins.toml` (machine-level Claude Code plugins) via `claude plugin`. |
| `lacquer status` | Show each region's stamped version vs the lacquer's latest. |
| `lacquer audit` | Classify project drift; exit 3 if a sync would clobber a local change (usable as a CI gate). |
| `lacquer version` | Print the lacquer version. |

`lacquer --help` prints usage.

## LACQUER_ROOT

Every command that reads shipped content (`sync`, `status`, `audit`, `version`)
resolves the lacquer checkout from the `LACQUER_ROOT` env var (default `.`):

```sh
LACQUER_ROOT=~/Developer/lacquer lacquer sync
```

If `LACQUER_ROOT` is unset and the current directory isn't a lacquer checkout
(no `VERSION` file / `profiles/` dir), those commands fail with an actionable
message rather than an opaque missing-file error.

## Profiles that ship

- **`core`** — universal rules/skills/commands applied to every project.
- **`ios`** — Swift/Xcode: SwiftLint/SwiftFormat, CI, TestFlight, Skills; git
  hooks via `pre-commit`.
- **`web`** — TypeScript + Biome + Vitest; CI + git hooks via `lefthook`.
- **`supabase`** — Deno Edge Functions + Postgres/RLS; CI + git hooks via
  `lefthook`.

A component detected as an unshipped stack (e.g. Rust/Go) is recorded in the
manifest with an empty profile list and a notice — it doesn't break `sync`.

## Updating a project

```sh
lacquer audit    # see what drifted; exit 3 means sync would overwrite a local edit
lacquer sync     # apply; refuses to clobber a locally-modified managed unit
lacquer sync --force   # adopt the lacquer version over a local change
```

Sync writes a `.lacquer.lock` baseline so `audit` can tell "the project edited
this" from "the lacquer moved on" and only blocks on the former.

## Proving the checks work

Every serious defect found onboarding this fleet was the same shape: **a check
that ran, reported success, and verified nothing.**

- The editor hook called `swiftlint lint --path FILE`. `--path` had been removed
  from SwiftLint, so it errored on every write — and `2>/dev/null || true` ate
  the message. It linted nothing for months and looked healthy doing it.
- The DocC gate passed `DOCC_FLAGS=--warnings-as-errors`. That is a real build
  setting name and it is silently ignored; a deliberately broken symbol link
  still exited 0.
- The drift job ran `go build ./path` from a non-module directory. It failed in
  every repo, on every PR, for a reason unrelated to drift.

None was caught by running the check. CI already tells you whether a check
passes; it cannot tell you whether it *could* fail.

```sh
lacquer doctor
```

writes a **known-bad** fixture, runs the check against it, and asserts the check
**rejects** it. A probe that passes on broken input is reported as broken, with
the reason it exists. Exit 5 — distinct from `audit`'s 3 (drift) and 4
(baseline), so a caller can tell *"a check is broken"* from *"the project is
wrong"*.

```
proving each check can fail:
  ok    SwiftLint rejects a warning-severity violation under --strict
  ok    SwiftLint is invoked in a form it still accepts
  ok    SwiftFormat --lint rejects unformatted code
  ok    missing_docs rejects an undocumented declaration
  ok    the formatter and the linter agree on member order

5/5 checks proved they can fail.
```

Probes live in `profiles/<p>/doctor.toml`, and fixtures are written to a scratch
directory **outside** the project — a deliberately malformed file must never be
committable. A missing tool is a finding, not a skip: a check whose binary is
absent is not running. (The opposite of `lacquer fix`, where a missing tool
skips — an unfixed file is still caught by CI, an unverified one is not.)

## Adopting on an existing codebase

`lacquer sync` writes the configs; it does not touch your source. On a mature
app that means the newly-synced `.swiftlint.yml` finds everything at once — 509
violations in one app here, 290 in another — and roughly two thirds of that is
mechanical: member ordering, import sorting, trailing closures.

`--fix` pays that down before you ever look at it:

```sh
lacquer sync --fix     # sync, then run the profiles' autofixers
lacquer fix            # just the autofixers, any time
```

Measured on one app: **290 violations → 93**, with the whole `type_contents_order`
category (117) going to 1, because the synced `.swiftformat` enables
`organizeDeclarations` in `type` mode with a `--type-order` mirroring
`.swiftlint.yml`. What's left is judgement work — singletons, closure length,
layering — which is the right thing to be left with.

`--fix` is **opt-in on purpose.** Plain `sync` only ever writes lacquer-managed
files, and that contract is what makes `lacquer audit` able to say "you changed
this, the lacquer didn't". `--fix` deliberately breaks it by rewriting project
source, so it has to be asked for rather than discovered in a diff.

A fixer whose tool isn't installed is reported and skipped, never fatal — the
opposite of a *check*, which must block when it can't run. An unfixed file is
still caught by CI; a missing Homebrew formula shouldn't block adoption.

## Third-party skills

`lacquer sync` distributes this repo's own skills (`core/skills/`,
`profiles/*/skills/`) — that's a solved problem, versioned and drift-audited.
Third-party skills are a different concern: one global install shared across
every project, kept up to date by [`vercel-labs/skills`](https://github.com/vercel-labs/skills),
a real package manager for agent skills — not something lacquer reimplements.
The packages this fleet actually pulls in (with source links, and which is
suggested from which Swift import) are cataloged in [Skills
reference](https://patrickserrano.github.io/lacquer/reference/skills/#third-party-skills) —
currently [`dpearson2699/swift-ios-skills`](https://github.com/dpearson2699/swift-ios-skills)
(Apple framework references) and [`HunterHillegas/mac-assed-mac-app-skill`](https://github.com/HunterHillegas/mac-assed-mac-app-skill)
(AppKit/macOS conventions).

`[project].skills` in `.lacquer.toml` declares which packages *this* project
needs, mixing lacquer's own skills and third-party ones uniformly:

```toml
skills = [
  "patrickserrano/lacquer@security-review",
  "dpearson2699/swift-ios-skills@healthkit",
  "dpearson2699/swift-ios-skills@storekit",
]
```

`lacquer init` seeds this list automatically by scanning the project's actual
Swift imports (see `internal/skillsuggest`) — review and trim before running
`lacquer skills`, which installs exactly what's declared, project-scoped, via
`npx skills add <source> -s <name> -p -y`. Idempotent: re-running only adds
what's missing. It also flags any *installed* skill no longer declared in the
manifest (informational — nothing is auto-removed).

This is deliberately a separate command from `sync`: `sync` stays fully
offline and deterministic (its whole test suite depends on that), while
`skills` is the one command that reaches the network.

## Plugins (machine-level bootstrap)

Claude Code plugins install once at **user** scope and are shared across every
project on a machine — a different shape of problem than `[project].skills`,
which is per-project. `core/bootstrap/plugins.toml` lists the marketplaces and
six plugins this fleet relies on — `superpowers`, `codex` (adversarial review
via a real Codex subprocess), `context7`, `figma`, `security-guidance`, and
`telemetrydeck-analytics` — cataloged with source links in [Plugins
catalog](https://patrickserrano.github.io/lacquer/reference/plugins/).
`lacquer plugins` applies the manifest via `claude plugin marketplace add` /
`claude plugin install`, both confirmed idempotent (an already-configured
marketplace or already-installed plugin is a clean no-op). Only plugins
actually *enabled* on the reference machine are listed — one
installed-but-disabled there is a deliberate choice, not silently re-enabled
on a fresh machine.

```sh
lacquer plugins
```

This is how a fresh machine picks up the same plugin set an existing one
already has, closing the same "bootstrap a machine with none of this
preconfigured" gap that `[project].skills` closes for per-project skills.

## Installing

```sh
go install github.com/patrickserrano/lacquer/cmd/lacquer@latest
```

Or build from a checkout:

```sh
go build ./cmd/lacquer
```

Tagged releases (with prebuilt darwin/amd64 and darwin/arm64 binaries and
changelogs) are published automatically on [GitHub
Releases](https://github.com/patrickserrano/lacquer/releases) whenever
`VERSION` changes on `main`.

## Versioning

`VERSION` is semver and **machine-assigned — never edit it in a PR.** CI rejects
that, because two open PRs both bumping to the same number merge cleanly (both
sides make the identical change) and produce two different contents sharing one
version. Hand-picking also produced a permanent gap: `v0.69.0` does not exist,
because two bumps were missed and had to be corrected at once.

**Every merge with a material change releases.** [`version.yml`](.github/workflows/version.yml)
derives the next version from conventional commits with
[svu](https://github.com/caarlos0/svu) (pinned), writes `VERSION`, pushes, and
dispatches the release. `release.yml` tags `v<VERSION>` verbatim, so the file and
the tag cannot drift. There is no path filter — `VERSION` versions the whole
thing, CLI binary as much as synced content — and no loop, because a push made
with `GITHUB_TOKEN` does not trigger workflows.

| Commit | Bump |
|--------|------|
| `feat:` | minor |
| `fix:` | patch |
| `feat!:` / `BREAKING CHANGE:` | major |
| `docs:`, `chore:`, `ci:`, `refactor:`, `style:`, `test:` | **no release** |

A README or docs-site edit ships nothing to a project, so it mints no version.

**But prose under `core/` or `profiles/` is material** — projects consume it, so it
must release, and `docs:` would leave `lacquer status` telling them they're current
while the content moved. Commit those as `feat:`/`fix:` even though they read like
docs. CI enforces this rather than leaving it to discipline: a PR touching
`core/**` or `profiles/**` with a non-releasing type is rejected.

Because the repo squash-merges, **the PR title becomes the commit subject on
`main`** — so it is the PR title that CI validates, and the version is derived
from. A non-conventional title would otherwise compute no bump and silently
release nothing.

The PR **body** is load-bearing for the same reason, with a sharper edge: svu
decides "breaking" by substring-searching the commit body, not by parsing a
conventional footer — and that is the only check that reads the body at all
(`feat:`/`fix:` are matched against the subject alone). So *quoting* the marker
anywhere, even inside a code span or a table cell, declares a breaking change.
#89 proposed `1.0.0` off a `0.72.0` base because its description reproduced the
table above. CI now rejects a body carrying that marker unless the title also
declares it with `!`.

The project sits in `0.x` until a breaking change lands. `feat!:` /
`BREAKING CHANGE:` is the standardized, explicit way to declare one, so it
graduates the major — `0.x` → `1.0.0`, and `1.x` → `2.0.0` after that. If you need
to set a version by hand for any other reason, push it to `main` directly; the
workflow leaves a hand-set value alone.

The version is stamped into each managed region's marker so `lacquer status` can
report stamped-vs-latest. A project last synced before semver carries the old
integer form (`v70`); that reads as `0.70.0` and is re-stamped on its next sync.

## Docs

`docs/plans/` holds the design and build plans. The design doc
(`docs/plans/2026-06-15-lacquer-design.md`) carries an "Implementation status"
note distinguishing what's built from what's still aspirational.

## About

Built by [Patrick Serrano](https://patrickserrano.com), an iOS engineer
building apps under [PixelFox Studio](https://pixelfoxstudio.com). lacquer is
the internal tooling that keeps engineering practice consistent across the
whole fleet.
