# Supabase / Deno profile rules

Synced into the `CLAUDE.md` of any component declaring the `supabase` profile — a
Supabase backend: Postgres schema + RLS in `supabase/migrations/`, seed data in
`supabase/seed/`, and **Deno** Edge Functions in `supabase/functions/`. The
runtime is Deno, **not** Node — there is no `package.json`/`npm` here. This runs
on GitHub-hosted runners (no Apple toolchain).

## Tooling — Deno, not npm

- Format / lint / type-check / test with the Deno toolchain, never npm:
  `deno fmt`, `deno lint`, `deno check`, `deno test --allow-all`. A synced
  `deno.jsonc` holds the fmt/lint config and `deno task` shortcuts.
- **Pin every remote import to an exact version** — `https://deno.land/std@0.168.0/…`,
  `https://esm.sh/@supabase/supabase-js@2.39.0`. Never import an unpinned URL; a
  moving dependency breaks reproducibility and is a supply-chain risk.
- Prefer the `supabase` CLI for everything local: `supabase start` (boots
  Postgres + Studio + the functions runtime), `supabase db reset` (recreate +
  migrate + seed), `supabase functions serve`, `supabase gen types typescript`.

## Migrations (`supabase/migrations/`)

- **Idempotent**: `create table if not exists`, `create or replace function`,
  `drop … if exists`. A migration must be safe to re-run.
- **`enable row level security` immediately** on every new table — in the same
  migration that creates it. A table without RLS is a data leak.
- **Explicit foreign-key `on delete`** behavior (`cascade` / `set null` /
  `restrict`) — never rely on the default.
- **Seed data lives in `supabase/seed/`, never in a migration.** Migrations are
  schema; seeds are data.
- Migrations are **forward-only and append-only** once applied to a shared/remote
  DB — never edit a migration that has shipped; write a new one.

## Row-Level Security (the security boundary)

- **Every table has RLS enabled with explicit policies.** Default-deny: no policy
  means no access.
- Published/public rows are readable by `anon`; **all user data is owner-scoped**
  (`auth.uid() = user_id`). Writes are owner-scoped too.
- The **service-role key bypasses RLS** — it is server-only (Edge Functions,
  CI). It must NEVER reach a client or a committed file. Clients use the `anon`
  key + the user's JWT.

## Edge Functions (`supabase/functions/`, Deno)

- **Route shared logic through `_shared/`** (e.g. `r2.ts`, `errors.ts`) — reuse
  the helpers, don't reinvent CORS / error shapes / storage signing per function.
- **Auth on every endpoint**: verify the caller's JWT before doing work; gate
  premium/subscription features explicitly. Return the shared error shapes.
- **Standard CORS headers** on every response (including `OPTIONS` preflight).
- **Signed, expiring URLs** for all object storage (R2 / Supabase Storage) — never
  hand out a public or long-lived URL. Apply per-user rate limits where relevant.
- Validate and narrow every input (auth header, body, params) at the top of the
  handler before touching the DB or storage.

## Secrets

- Server secrets (service-role key, `R2_*`, third-party keys) live in
  `supabase secrets set …` for deployed functions and in GitHub Actions secrets
  for CI — and in a **gitignored** `.env.local` for local dev. Commit `.env.example`.
- Never log a secret or return it in a response.

## Git hooks & commits

`lefthook.yml` is synced — install once with `npx lefthook install` (or
`brew install lefthook`). It runs `deno fmt --check` + `deno lint` (scoped to the
component via lefthook's `root:`) and a secrets scan pre-commit, and enforces
**Conventional Commits** via the shared `scripts/check-commit-msg.sh`.

**Git hooks in a mixed repo.** If this repo ALSO contains an iOS component, the
iOS profile syncs a `.pre-commit-config.yaml` and this profile syncs a
`lefthook.yml` — both write `.git/hooks`, and whichever `install`s last silently
wins. Don't install both. The iOS `pre-commit` framework should own `.git/hooks`;
the Supabase checks always run in CI regardless, so rely on that. To keep them
running locally too, add them as `repo: local` hooks in the iOS
`.pre-commit-config.yaml` (e.g. an entry that runs `deno fmt --check`/`deno lint`
scoped to the supabase component) rather than installing lefthook alongside
pre-commit.

## Testing & CI

- `deno test --allow-all` for Edge Function logic; keep `_shared/` helpers unit-
  tested. The synced `supabase-ci.yml` runs `deno fmt --check`, `deno lint`,
  `deno check`, and `deno test` on `ubuntu-latest`.
- See the **supabase-postgres-best-practices** skill for schema design, indexing,
  RLS performance, and query patterns.
