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

## Documentation

**Documentation is a requirement** — see core "Documentation" for the rule and
the relaxation mechanism. Two surfaces here, checked differently.

### `_shared/` — the code other functions import

Every export in `supabase/functions/_shared/` carries a JSDoc comment. Checked by
the local pre-commit hook and published to
`https://<owner>.github.io/<repo>/db/` by `supabase-docs.yml`, which runs
nightly rather than on merge.

```ts
/**
 * Builds the standard JSON error response every function returns on failure.
 *
 * @param code - A stable, machine-readable code the client can branch on.
 * @param status - HTTP status; defaults to 400.
 */
export function errorResponse(code: string, status = 400): Response
```

Run it locally exactly as CI does:

```sh
deno doc --lint supabase/functions/_shared/*.ts
```

The check is scoped to `_shared/` deliberately. A function's own `index.ts` is an
HTTP entry point, not an API other code imports — holding it to an
export-documentation rule produces ceremony rather than understanding. Document
*that* in the handler's header comment: the method, the auth it requires, the
request and response shapes, and the failure codes.

### The schema — `comment on`, not a wiki

Postgres stores documentation in the database. Use it, in the same migration that
creates the object, so the comment ships and versions with the schema instead of
drifting in a doc nobody opens:

```sql
comment on table  public.sessions       is 'One recorded session per user. RLS: owner-scoped.';
comment on column public.sessions.ended_at is 'Null while the session is still running.';
```

This is the only documentation a client library, `supabase gen types`, or someone
reading the schema in Studio will actually see. A column whose meaning is not
obvious from its name — a nullable flag, a unit, an enum-like text field, a
denormalized counter — needs one.

## Secrets

- Server secrets (service-role key, `R2_*`, third-party keys) live in
  `supabase secrets set …` for deployed functions and in GitHub Actions secrets
  for CI — and in a **gitignored** `.env.local` for local dev. Commit `.env.example`.
- Never log a secret or return it in a response.

## Local Checks vs CI

Every CI gate and where it runs before push. See core "Local Checks Match CI" —
a new CI job adds a row here, and a hook never runs weaker than its CI twin.

| CI job / step | Local |
|---|---|
| `check` → `deno fmt --check` | pre-commit `fmt` |
| `check` → `deno lint` | pre-commit `lint` |
| `check` → `deno check` | pre-push `typecheck` |
| `check` → `deno test` | pre-push `test` |
| `Docs` → `deno doc --lint` | pre-commit `docs` |
| `DB Lint (Splinter)`, `DB Test (pgTAP)` | CI-only: both need a live Postgres. Run `supabase db lint` / `supabase test db` against `supabase start` when touching the schema. |
| `Deploy migrations` | CI-only, and only on merge to main — it is the step that touches production. |
| `No lacquer drift` | `lacquer audit` (exit 3) |

## Deploying migrations

**`supabase db push` runs on merge to `main`, behind the schema jobs.** A
migration cannot reach production without `DB Lint (Splinter)` and `DB Tests
(pgTAP)` having passed on it, which is why the deploy job lives in `ci.yml`
rather than a workflow of its own — `needs:` cannot reach across files.

Configure **either** a direct connection string, or the link route. The job
**skips with a warning** rather than failing when neither is present, so a
project that has not wired deployment up is not permanently red:

```sh
# Preferred: one secret, no linking step
gh secret set SUPABASE_DB_URL -R <owner>/<repo>          # Dashboard → Settings → Database

# Or the link route, which needs all three
gh secret set SUPABASE_ACCESS_TOKEN -R <owner>/<repo>    # supabase.com/dashboard/account/tokens
gh secret set SUPABASE_DB_PASSWORD -R <owner>/<repo>     # the database password
gh variable set SUPABASE_PROJECT_REF -R <owner>/<repo>   # the ref in your project URL
```

The project ref is a **variable, not a secret** — it is already public in your
project URL, and a variable is readable in the workflow log, which is what you
want when diagnosing a deploy that went to the wrong project.

`--project-ref` is **not** a flag on `db push`; it belongs to `link`, and push
names an already-linked project with `--linked`. Older CLI versions accepted it
on push, so a workflow copied from an older project fails with `Unrecognized
flag` on its first merge.

### When the push refuses: diverged history

`db push` refuses, rather than guessing, when it finds a local migration that
sorts **before** the last one already applied remotely:

```
Found local migration files to be inserted before the last migration on remote database.
Rerun the command with --include-all flag to apply these migrations: …
```

That almost always means someone applied a migration **by hand in the SQL
editor** — the objects exist, but no row was written to
`supabase_migrations.schema_migrations`, so the CLI cannot tell the difference
between "already applied" and "skipped".

**Do not reach for `--include-all`, and never put it in the workflow.** It
reorders a production schema silently. Look at the divergence first:

```sh
supabase migration list --linked                  # local vs remote, side by side
supabase db push --linked --dry-run               # what would be applied
supabase migration repair --status applied <ver>  # objects exist: record it, don't re-run
supabase migration repair --status reverted <ver> # objects do NOT exist: let push apply it
```

Confirm which case you are in before repairing — dump the remote schema and
look for the objects (`supabase db dump --linked --schema public`) rather than
assuming. Marking something applied when it is not leaves production missing
the objects with nothing left to tell you.

**Watch for this when onboarding an existing project onto the lacquer.** Rail
had this exact job, hand-rolled inside its own `ci.yml`, and onboarding retired
that file wholesale — the deploy went with it while its secret stayed
configured, so nothing looked broken and migrations silently stopped shipping.
That is the failure this job in the profile exists to prevent: if it is here, a
future sync restores it instead of dropping it.

**Edge Functions are deliberately not deployed by CI.** `db push` applies a
reviewed, ordered, append-only migration set; `functions deploy` swaps running
code. A project may reasonably want the first automatic and the second
deliberate, so run `supabase functions deploy <name>` yourself, or add the step
if you want it on merge.

`deno fmt --check` and `deno lint` are **not** scoped to staged files — they check
everything in `deno.jsonc`'s `include` (`supabase/functions/`). So the first
commit after these hooks start running reports every unformatted file in the
tree, not just the one you touched. That is the gate working, not a
misconfiguration: run `deno fmt` once to bring the tree into line, commit that on
its own, and it stays quiet after.

## Git hooks & commits

`lefthook.yml` is synced — install once with `pnpm exec lefthook install` (or
`brew install lefthook`). It runs `deno fmt --check` + `deno lint` (scoped to the
component via lefthook's `root:`) and a secrets scan pre-commit, and enforces
**Conventional Commits** via the shared `scripts/check-commit-msg.sh`.

**Git hooks in a web + Supabase repo.** Both profiles ship a `lefthook.yml` to
the repository root, so `lacquer sync` MERGES them into one file rather than
letting one silently win. Every command keeps the `root:` of the component it
came from; a command both profiles ship identically (the secrets scan, the
commit-message check) appears once; a command they ship differently keeps both,
suffixed with its profile — the pre-push `test` becomes `test-supabase` and
`test-web`. Run one of them by its suffixed name (`lefthook run pre-push
--commands test-supabase`). Don't hand-edit the merged file: the next sync
rewrites it, and `lacquer audit` reports the edit as drift.

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
- CI also **checks the schema, not just the functions**: `supabase db lint
  --level warning` (Splinter — flags missing-RLS / security-definer issues) and
  `supabase test db` (pgTAP against `supabase/tests/*.sql`). Write pgTAP tests
  that assert RLS actually denies cross-user access — the lint catches a
  *missing* policy, a test catches a *wrong* one.
- **An empty `supabase/tests/` fails the `DB Tests (pgTAP)` job.** `supabase test
  db` exits 0 over a matchless glob, so a project with no tests would otherwise
  boot a full Postgres stack and report green having asserted nothing — the
  check that cannot fail, in the exact form the core rules warn about. A project
  that genuinely has no tests yet takes the same time-boxed escape hatch as
  every other baseline, and the expiry is enforced:

  ```toml
  [baseline.relax]
  pgtap = { until = "2026-11-01", reason = "new project, RLS tests tracked in #12" }
  ```

  Write the tests rather than the relaxation where you can. Assertions worth
  reaching for first: that `anon` cannot read another user's rows, that a
  `security definer` RPC deletes only the caller's row and not a neighbour's,
  and that a table's *effective* read works — a policy with no matching `GRANT`
  passes review and fails 42501 in production. Note that `TRUNCATE` is not
  filtered by RLS at all, so a table locked down at the row level can still be
  emptied in one statement by any role holding the default grant.
- See the **supabase-postgres-best-practices** skill for schema design, indexing,
  RLS performance, and query patterns.
