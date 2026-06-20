# Web profile rules

Synced into the `CLAUDE.md` of any component declaring the `web` profile. The
harness web stack is **TypeScript + Biome + Vitest + lefthook**, deployed to
Vercel/Cloudflare; the framework (Next.js / Vite / a Node API) is per-project.
Web jobs run on GitHub-hosted runners — there is no Apple toolchain here.

## Required package.json scripts

The synced CI and git hooks assume these scripts exist — define them:

| Script | Does |
|--------|------|
| `typecheck` | `tsc --noEmit` |
| `test` | `vitest` |
| `test:coverage` | `vitest run --coverage` |
| `build` | the framework build (`next build`, `vite build`, `tsc`, …) |

## TypeScript — extend the strict base

The harness syncs `tsconfig.base.json` (strictness flags only — no framework
wiring, because web stacks are heterogeneous). Your project's `tsconfig.json`
**extends** it and adds the framework-specific bits:

```jsonc
{
  "extends": "./tsconfig.base.json",
  "compilerOptions": {
    "lib": ["dom", "dom.iterable", "esnext"],
    "module": "esnext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",          // or "preserve" for Next.js
    "noEmit": true,
    "paths": { "@/*": ["./src/*"] }
  }
}
```

Never relax a base flag (`strict`, `noUncheckedIndexedAccess`,
`exactOptionalPropertyTypes`, `noUnusedLocals/Parameters`). Fix the code. Never
`// @ts-ignore` — use `// @ts-expect-error` with a reason, or fix the type.

## Code quality — Biome

`biome.json` is synced (format + lint). Run `npx biome check --write .` locally;
CI runs `npx biome ci .` (no writes, fails on any issue). `noExplicitAny` and
`noArrayIndexKey` are warnings — treat them as errors and fix the code; never
disable a rule inline without explicit user approval (mirrors the core lint rule).

## Testing — Vitest

- Vitest with coverage; keep meaningful thresholds in `vitest.config.ts`
  (`coverage.thresholds`). The strict tier targets high coverage on logic
  (pure functions, API handlers) — don't chase 100% on glue/UI.
- Co-locate `*.test.ts` with the source, or under `src/**`. Test behaviour, not
  implementation. For React, prefer Testing Library + user-facing queries.
- E2E (Playwright) and accessibility (`@axe-core/playwright`) are project-opt-in;
  when present they run as their own CI job, still on a GitHub-hosted runner.

## Environment & secrets

- **Never commit a real `.env`.** Commit `.env.example` (and, when you want
  schema-validated env, a `.env.schema` checked with `dotenvx run -- ...`).
- Public values (e.g. `NEXT_PUBLIC_*`) vs. server-only secrets (service-role
  keys, API tokens) must be clearly separated; server secrets never reach the
  client bundle.
- A vendor REST secret (e.g. RevenueCat `sk_…`, a Supabase service-role key)
  lives in the deploy platform's env (Vercel/Cloudflare project settings) and in
  GitHub Actions secrets for CI — never in client code or a committed file.

## Security

- Set HTTP security headers at the edge (`vercel.json` `headers`):
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, a Content-Security
  Policy where feasible; constrain CORS to known origins (never reflect `*` with
  credentials).
- Validate and narrow every external input at the boundary (Zod or equivalent);
  never trust query/body/header shape. No `dangerouslySetInnerHTML` with
  unsanitised content (Biome warns — heed it).
- Pin and scope deploy/API tokens to least privilege.

## Accessibility

Ship semantic HTML (Biome's `useSemanticElements`); every interactive control is
keyboard-reachable with a visible focus ring and an accessible name; meaning is
never carried by colour alone. Target WCAG 2.1 AA.

## Git hooks & commits

`lefthook.yml` is synced — install once with `npx lefthook install`. It runs
Biome + typecheck pre-commit, coverage + build pre-push, and enforces
**Conventional Commits** via the shared `scripts/check-commit-msg.sh`
(`type(scope): summary`).

## CI

`web-ci.yml` runs lint → typecheck → test (coverage) → build → dependency audit
on `ubuntu-latest`, path-gated to the component. The audit blocks on **critical**
advisories by default; tighten to `high` (and add npm `overrides` for unfixable
transitives) per project.
