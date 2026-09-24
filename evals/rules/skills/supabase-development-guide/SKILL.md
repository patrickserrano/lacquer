---
name: supabase-development-guide
description: Write Supabase migrations, RLS policies or Edge Functions; configure Deno checks, pgTAP tests, secrets or deployment.
---

# Supabase Development Guide

## Project conventions

Read [references/project-rules.md](references/project-rules.md) for the relevant
section when handling this task. Read only what applies; examples do not
authorize releases, deployments or changes outside the user's scope.
Always-loaded safety rules still apply.

- Profile scope
- Tooling — Deno, not npm
- Migrations (`supabase/migrations/`)
- Row-Level Security (the security boundary)
- Edge Functions (`supabase/functions/`, Deno)
- Documentation
- Secrets
- Local Checks vs CI
- Deploying migrations
- Git hooks & commits
- Testing & CI
