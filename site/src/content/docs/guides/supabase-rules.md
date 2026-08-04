---
title: Supabase rules
description: Postgres/Deno-specific rules synced into any component declaring the supabase profile.
---

:::note
This page mirrors `profiles/supabase/CLAUDE.supabase.md` — the rules the
lacquer syncs into the `CLAUDE.md` of any component declaring the `supabase`
profile, on top of the stack-agnostic [core rules](/lacquer/guides/agent-rules/).
:::

This profile targets a Supabase backend: Postgres schema + RLS in `supabase/migrations/`, seed data in `supabase/seed/`, and **Deno** Edge Functions in `supabase/functions/`. The runtime is Deno, not Node — there's no `package.json`/`npm` here. This runs on GitHub-hosted runners (no Apple toolchain).

## Tooling — Deno, not npm

- Format / lint / type-check / test with the Deno toolchain, never npm: `deno fmt`, `deno lint`, `deno check`, `deno test --allow-all`. A synced `deno.jsonc` holds the fmt/lint config and `deno task` shortcuts.
- **Pin every remote import to an exact version** — `https://deno.land/std@0.168.0/…`, `https://esm.sh/@supabase/supabase-js@2.39.0`. Never import an unpinned URL; a moving dependency breaks reproducibility and is a supply-chain risk.
- Prefer the `supabase` CLI for everything local: `supabase start` (boots Postgres + Studio + the functions runtime), `supabase db reset` (recreate + migrate + seed), `supabase functions serve`, `supabase gen types typescript`.

## Migrations (`supabase/migrations/`)

- **Idempotent**: `create table if not exists`, `create or replace function`, `drop … if exists`. A migration must be safe to re-run.
- **`enable row level security` immediately** on every new table — in the same migration that creates it. A table without RLS is a data leak.
- **Explicit foreign-key `on delete`** behavior (`cascade` / `set null` / `restrict`) — never rely on the default.
- **Seed data lives in `supabase/seed/`, never in a migration.** Migrations are schema; seeds are data.
- Migrations are forward-only and append-only once applied to a shared/remote DB — never edit a migration that has shipped; write a new one.

## Row-Level Security (the security boundary)

- **Every table has RLS enabled with explicit policies.** Default-deny: no policy means no access.
- Published/public rows are readable by `anon`; all user data is owner-scoped (`auth.uid() = user_id`). Writes are owner-scoped too.
- The **service-role key bypasses RLS** — it's server-only (Edge Functions, CI). It must never reach a client or a committed file. Clients use the `anon` key + the user's JWT.

## Edge Functions (`supabase/functions/`, Deno)

- **Route shared logic through `_shared/`** (e.g. `r2.ts`, `errors.ts`) — reuse the helpers, don't reinvent CORS / error shapes / storage signing per function.
- **Auth on every endpoint**: verify the caller's JWT before doing work; gate premium/subscription features explicitly. Return the shared error shapes.
- **Standard CORS headers** on every response (including `OPTIONS` preflight).
- **Signed, expiring URLs** for all object storage (R2 / Supabase Storage) — never hand out a public or long-lived URL. Apply per-user rate limits where relevant.
- Validate and narrow every input (auth header, body, params) at the top of the handler before touching the DB or storage.

## Documentation

Documentation is a requirement — see [core documentation
rules](/lacquer/guides/agent-rules/#documentation). Two surfaces here, checked
differently.

**`_shared/` — the code other functions import.** Every export in
`supabase/functions/_shared/` carries a JSDoc comment, checked by the `Docs` job
in `supabase-ci.yml` and published to `https://<owner>.github.io/<repo>/db/`.
Run it locally exactly as CI does:

```sh
deno doc --lint supabase/functions/_shared/*.ts
```

The check is scoped to `_shared/` deliberately: a function's own `index.ts` is an
HTTP entry point, not an API other code imports, so an export-documentation rule
there produces ceremony rather than understanding. Document that in the handler's
header comment — the method, the auth it requires, the request and response
shapes, and the failure codes.

**The schema — `comment on`, not a wiki.** Postgres stores documentation in the
database; use it, in the same migration that creates the object, so the comment
ships and versions with the schema:

```sql
comment on table  public.sessions          is 'One recorded session per user. RLS: owner-scoped.';
comment on column public.sessions.ended_at is 'Null while the session is still running.';
```

This is the only documentation a client library, `supabase gen types`, or someone
reading the schema in Studio will actually see.

## Secrets

- Server secrets (service-role key, `R2_*`, third-party keys) live in `supabase secrets set …` for deployed functions and in GitHub Actions secrets for CI — and in a gitignored `.env.local` for local dev. Commit `.env.example`.
- Never log a secret or return it in a response.

## Git hooks & commits

`lefthook.yml` is synced — install once with `npx lefthook install` (or `brew install lefthook`). It runs `deno fmt --check` + `deno lint` (scoped to the component via lefthook's `root:`) and a secrets scan pre-commit, and enforces Conventional Commits via the shared `scripts/check-commit-msg.sh`.

:::caution[Git hooks in a mixed repo]
If this repo also contains an iOS component, the iOS profile syncs a `.pre-commit-config.yaml` and this profile syncs a `lefthook.yml` — both write `.git/hooks`, and whichever `install`s last silently wins. Don't install both. The iOS `pre-commit` framework should own `.git/hooks`; the Supabase checks always run in CI regardless, so rely on that. To keep them running locally too, add them as `repo: local` hooks in the iOS `.pre-commit-config.yaml` (e.g. an entry that runs `deno fmt --check`/`deno lint` scoped to the supabase component) rather than installing lefthook alongside pre-commit.
:::

## Testing & CI

- `deno test --allow-all` for Edge Function logic; keep `_shared/` helpers unit-tested. The synced `supabase-ci.yml` runs `deno fmt --check`, `deno lint`, `deno check`, and `deno test` on `ubuntu-latest`.
- CI also checks the schema, not just the functions: `supabase db lint --level warning` (Splinter — flags missing-RLS / security-definer issues) and `supabase test db` (pgTAP against `supabase/tests/*.sql`). Write pgTAP tests that assert RLS actually denies cross-user access — the lint catches a *missing* policy, a test catches a *wrong* one.
- An empty `supabase/tests/` **fails** the `DB Tests (pgTAP)` job. `supabase test db` exits 0 over a matchless glob, so without that check the job boots a full Postgres stack and reports green having asserted nothing. A project with no tests yet takes the standard time-boxed escape hatch, and the expiry is enforced: `pgtap = { until = "YYYY-MM-DD", reason = "..." }` under `[baseline.relax]`.
- Remember that `TRUNCATE` is **not** filtered by RLS. A table with RLS on and no policies still permits `TRUNCATE` from any role holding the default grant, so lock the grant down as well as the policy — and assert it, since no policy can express it.
- See the `supabase-postgres-best-practices` skill (in the [skills catalog](/lacquer/reference/skills/#supabase)) for schema design, indexing, RLS performance, and query patterns.
