# Supabase / Deno profile rules

- Server secrets and service-role keys never reach clients, tracked files, logs
  or responses. The service-role key bypasses RLS; clients use anon + user JWT.
- Enable RLS with explicit policies on every table; user reads and writes are
  owner-scoped. Authenticate callers and validate inputs before DB/storage access.
- Applied shared/remote migrations are forward-only and append-only. Never
  rewrite history or blindly use `--include-all` to bypass migration divergence.
- Use Deno for Edge Functions. A green check must exercise real assertions:
  prove RLS denies cross-user access, and never accept an empty pgTAP suite.
- Read `supabase-development-guide` for migrations, Deno tooling, Edge Functions,
  docs, secrets, hooks, pgTAP and deployment/recovery procedures. Read
  `supabase-postgres-best-practices` for schema, indexing and query performance.
