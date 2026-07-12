---
title: Getting started
description: Install harness and sync it into a project for the first time.
---

## Build the CLI

```sh
go build ./cmd/harness
```

## HARNESS_ROOT

Every command that reads shipped content (`sync`, `status`, `audit`, `version`)
resolves the harness checkout from the `HARNESS_ROOT` env var (default `.`):

```sh
HARNESS_ROOT=~/Developer/harness harness sync
```

If `HARNESS_ROOT` is unset and the current directory isn't a harness checkout
(no `VERSION` file / `profiles/` dir), those commands fail with an actionable
message rather than an opaque missing-file error.

## Onboard a new project

```sh
harness init                    # detect components, write a .harness.toml stub
harness onboard --org O         # init, then create a private GitHub repo under O
harness sync                    # render core + per-profile content into the project
```

`harness init` also writes a `docs/brief.md` stub — paste the real project brief
there before doing anything else. See [Agent rules](/guides/agent-rules/) for the
docs taxonomy that flows from it (brief → PRD → PCD → plan).

## Updating a project already on harness

```sh
harness audit           # see what drifted; exit 3 means sync would overwrite a local edit
harness sync            # apply; refuses to clobber a locally-modified managed unit
harness sync --force    # adopt the harness version over a local change
```

Sync writes a `.harness.lock` baseline so `audit` can tell "the project edited
this" from "the harness moved on" and only blocks on the former.

## Profiles that ship

| Profile | Covers |
|---------|--------|
| `core` | Universal rules/skills/commands applied to every project. |
| `ios` | Swift/Xcode: SwiftLint/SwiftFormat, CI, TestFlight, skills; git hooks via `pre-commit`. |
| `web` | TypeScript + Biome + Vitest; CI + git hooks via `lefthook`. |
| `supabase` | Deno Edge Functions + Postgres/RLS; CI + git hooks via `lefthook`. |

A component detected as an unshipped stack (e.g. Rust/Go) is recorded in the
manifest with an empty profile list and a notice — it doesn't break `sync`.

See the full [command reference](/reference/commands/) and [skills catalog](/reference/skills/).
