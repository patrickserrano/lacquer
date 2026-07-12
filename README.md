# harness

A Go CLI plus a set of profile templates that standardize how Claude Code works
across every project in `~/Developer`. The harness renders shared content —
`CLAUDE.md` rules, skills, commands, CI workflows, git hooks, tool configs — into
each project and tracks how far each project has drifted, so a lesson pinned once
propagates everywhere instead of being copy-pasted and left to rot.

A project opts in per **component** (a subdirectory) via a `.harness.toml`
manifest. Each component declares one or more **profiles**; `core` applies to
every project regardless.

## Commands

| Command | Does |
|---------|------|
| `harness init` | Detect components, write a `.harness.toml` stub (and a `docs/brief.md` stub). |
| `harness onboard --org O [--no-repo]` | `init`, then create a private GitHub repo under `O` when the repo has no `origin`. |
| `harness sync [--force]` | Render core + per-profile content into the project (managed regions + whole-file assets). |
| `harness status` | Show each region's stamped version vs the harness's latest. |
| `harness audit` | Classify project drift; exit 3 if a sync would clobber a local change (usable as a CI gate). |
| `harness version` | Print the harness version. |

`harness --help` prints usage.

## HARNESS_ROOT

Every command that reads shipped content (`sync`, `status`, `audit`, `version`)
resolves the harness checkout from the `HARNESS_ROOT` env var (default `.`):

```sh
HARNESS_ROOT=~/Developer/harness harness sync
```

If `HARNESS_ROOT` is unset and the current directory isn't a harness checkout
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
harness audit    # see what drifted; exit 3 means sync would overwrite a local edit
harness sync     # apply; refuses to clobber a locally-modified managed unit
harness sync --force   # adopt the harness version over a local change
```

Sync writes a `.harness.lock` baseline so `audit` can tell "the project edited
this" from "the harness moved on" and only blocks on the former.

## Installing

```sh
go install github.com/patrickserrano/harness/cmd/harness@latest
```

Or build from a checkout:

```sh
go build ./cmd/harness
```

Tagged releases (with prebuilt darwin/amd64 and darwin/arm64 binaries and
changelogs) are published automatically on [GitHub
Releases](https://github.com/patrickserrano/harness/releases) whenever
`VERSION` changes on `main`.

## Docs

`docs/plans/` holds the design and build plans. The design doc
(`docs/plans/2026-06-15-harness-design.md`) carries an "Implementation status"
note distinguishing what's built from what's still aspirational.
