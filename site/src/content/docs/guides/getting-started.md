---
title: Getting started
description: Install lacquer and sync it into a project for the first time.
---

## Build the CLI

```sh
go build ./cmd/lacquer
```

## LACQUER_ROOT

Every command that reads shipped content (`sync`, `status`, `audit`, `version`)
resolves the lacquer checkout from the `LACQUER_ROOT` env var (default `.`):

```sh
LACQUER_ROOT=~/Developer/lacquer lacquer sync
```

If `LACQUER_ROOT` is unset and the current directory isn't a lacquer checkout
(no `VERSION` file / `profiles/` dir), those commands fail with an actionable
message rather than an opaque missing-file error.

## Onboard a new project

```sh
lacquer init                    # detect components, write a .lacquer.toml stub
lacquer onboard --org O         # init, then create a private GitHub repo under O
lacquer sync                    # render core + per-profile content into the project
```

`lacquer init` also writes a `docs/brief.md` stub — paste the real project brief
there before doing anything else. See [Agent rules](/lacquer/guides/agent-rules/) for the
docs taxonomy that flows from it (brief → PRD → PCD → plan).

## Updating a project already on lacquer

```sh
lacquer audit           # see what drifted; exit 3 means sync would overwrite a local edit
lacquer sync            # apply; refuses to clobber a locally-modified managed unit
lacquer sync --force    # adopt the lacquer version over a local change
```

Sync writes a `.lacquer.lock` baseline so `audit` can tell "the project edited
this" from "the lacquer moved on" and only blocks on the former.

## Third-party skills

`lacquer sync` distributes this repo's own skills (`core/skills/`,
`profiles/*/skills/`) — versioned and drift-audited, same as everything else
`sync` renders. Third-party skills (Apple framework references, etc.) are a
different concern: one global install shared across every project, kept up to
date by [`skills`](https://github.com/vercel-labs/skills), a real package
manager for agent skills, rather than something lacquer reimplements.

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
Swift imports — review and trim it, then:

```sh
lacquer skills   # installs exactly what's declared, project-scoped
```

This shells out to `npx skills add <source> -s <name> -p -y` per entry.
Idempotent — re-running only adds what's missing — and it flags any
*installed* skill no longer declared in the manifest (informational only;
nothing is auto-removed).

`skills` is deliberately separate from `sync`: `sync` stays fully offline and
deterministic, while `skills` is the one lacquer command that reaches the
network.

## Plugins (machine-level bootstrap)

Claude Code plugins (`superpowers`, `codex` for adversarial review via a real
Codex subprocess, `context7`, etc.) install once at **user** scope and are
shared across every project on a machine — a different shape of problem than
`[project].skills`, which is per-project. `core/bootstrap/plugins.toml` lists
the marketplaces and plugins this fleet relies on:

```sh
lacquer plugins
```

This shells out to `claude plugin marketplace add` / `claude plugin install`,
both confirmed idempotent — an already-configured marketplace or
already-installed plugin is a clean no-op, not an error. Only plugins
actually *enabled* on the reference machine are listed in the manifest; one
installed-but-disabled there is a deliberate choice, not silently
re-enabled on a fresh machine. This closes the same "bootstrap a machine
with none of this preconfigured" gap `[project].skills` closes for
per-project skills, just at the machine level instead.

## Profiles that ship

| Profile | Covers |
|---------|--------|
| `core` | Universal rules/skills/commands applied to every project. |
| `ios` | Swift/Xcode: SwiftLint/SwiftFormat, CI, TestFlight, skills; git hooks via `pre-commit`. |
| `web` | TypeScript + Biome + Vitest; CI + git hooks via `lefthook`. |
| `supabase` | Deno Edge Functions + Postgres/RLS; CI + git hooks via `lefthook`. |

A component detected as an unshipped stack (e.g. Rust/Go) is recorded in the
manifest with an empty profile list and a notice — it doesn't break `sync`.

See the full [command reference](/lacquer/reference/commands/) and [skills catalog](/lacquer/reference/skills/).
