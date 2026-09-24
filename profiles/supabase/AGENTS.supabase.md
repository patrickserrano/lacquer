# Supabase / Deno

- From this component directory run `deno fmt --check`, `deno lint`,
  `deno check supabase/functions/*/index.ts` and
  `deno test --allow-all supabase/functions/`. Use Deno for Edge Functions.
- Start the local database with `supabase start`, apply migrations locally with
  `supabase db reset`, then run `supabase test db`. Never reset a remote database.
  Require real pgTAP assertions and tests proving cross-user access is denied;
  empty suites are not a pass. Local lefthook.yml checks map to
  `.github/workflows/supabase-ci.yml`; database integration needs the local stack.
- Enable RLS with explicit owner-scoped policies on every table. Authenticate
  callers and validate inputs before DB/storage access. Clients use anon + user
  JWT; service-role keys bypass RLS and must never reach clients or logs.
- Applied shared/remote migrations are append-only and forward-only. Never rewrite
  history or use `--include-all` to bypass unexplained migration divergence.
  Review the target and pending migrations before deploying.
