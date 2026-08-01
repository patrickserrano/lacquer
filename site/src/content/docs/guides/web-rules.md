---
title: Web rules
description: TypeScript/Biome/Vitest-specific rules synced into any component declaring the web profile.
---

:::note
This page mirrors `profiles/web/CLAUDE.web.md` — the rules the lacquer syncs
into the `CLAUDE.md` of any component declaring the `web` profile, on top of
the stack-agnostic [core rules](/lacquer/guides/agent-rules/).
:::

The lacquer web stack is **TypeScript + Biome + Vitest + lefthook**, deployed to
Vercel/Cloudflare; the framework (Next.js / Vite / a Node API) is per-project.
Web jobs run on GitHub-hosted runners — there's no Apple toolchain here.

## Read the vendored framework docs first

If the component's framework ships its own docs inside the dependency (e.g. under `node_modules/<framework>/dist/docs/`, as Next.js and some others do), read the relevant guide there before writing code against it. The installed major may carry breaking API changes versus your training data, and these fast-moving frameworks routinely deprecate or rename APIs — trust the vendored copy over memory and heed its deprecation notices.

## Required package.json scripts

The synced CI and git hooks assume these scripts exist — define them:

| Script | Does |
|--------|------|
| `typecheck` | `tsc --noEmit` |
| `test` | `vitest` |
| `test:coverage` | `vitest run --coverage` |
| `build` | the framework build (`next build`, `vite build`, `tsc`, …) |

## TypeScript — extend the strict base

The lacquer syncs `tsconfig.base.json` (strictness flags only — no framework wiring, because web stacks are heterogeneous). Your project's `tsconfig.json` extends it and adds the framework-specific bits:

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

Never relax a base flag (`strict`, `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `noUnusedLocals/Parameters`). Fix the code. Never `// @ts-ignore` — use `// @ts-expect-error` with a reason, or fix the type.

## Code quality — Biome

`biome.json` is synced (format + lint). Run `npx biome check --write .` locally; CI runs `npx biome ci .` (no writes, fails on any issue). `noExplicitAny` and `noArrayIndexKey` are warnings — treat them as errors and fix the code; never disable a rule inline without explicit user approval (mirrors the [core lint rule](/lacquer/guides/agent-rules/#fundamental-rules)).

## Testing — Vitest

- Vitest with coverage; keep meaningful thresholds in `vitest.config.ts` (`coverage.thresholds`). The strict tier targets high coverage on logic (pure functions, API handlers) — don't chase 100% on glue/UI.
- Co-locate `*.test.ts` with the source, or under `src/**`. Test behaviour, not implementation. For React, prefer Testing Library + user-facing queries.
- E2E (Playwright) and accessibility (`@axe-core/playwright`) are project-opt-in; when present they run as their own CI job, still on a GitHub-hosted runner.

## Documentation (TSDoc + TypeDoc)

Documentation is a requirement — see [core documentation
rules](/lacquer/guides/agent-rules/#documentation) for the standard and the
relaxation mechanism. This is the TypeScript half.

Every exported symbol carries a TSDoc comment, and every link in one resolves.
Both are checked by the `Docs` job in `web-ci.yml` and published to
`https://<owner>.github.io/<repo>/api/` by `web-docs.yml` on merge.

```ts
/**
 * Loads the user's saved sessions, newest first.
 *
 * @param limit - Maximum number of sessions to return; omit for all of them.
 * @returns The sessions, or an empty array when the user has none.
 * @throws {@link UnauthorizedError} when the session token has expired.
 */
export async function loadSessions(limit?: number): Promise<Session[]>
```

Use `{@link Symbol}` rather than a plain-text type name — a link is checked by
the build, prose isn't.

The synced `typedoc.json` turns on `validation.notDocumented`, `invalidLink`, and
`treatValidationWarningsAsErrors`. `entryPoints` is per-project: point it at the
package's real public surface; the shipped default assumes a single barrel file
at `src/index.ts`.

`excludeInternal: true` means a symbol marked `@internal` is omitted from the
site *and* exempt from the documented-export rule — the right escape for
something exported only for testing or cross-module wiring. Marking a genuinely
public API `@internal` to dodge the rule is the TypeScript spelling of disabling
a lint rule.

## Environment & secrets

- **Never commit a real `.env`.** Commit `.env.example` (and, when you want schema-validated env, a `.env.schema` checked with `dotenvx run -- ...`).
- Public values (e.g. `NEXT_PUBLIC_*`) vs. server-only secrets (service-role keys, API tokens) must be clearly separated; server secrets never reach the client bundle.
- A vendor REST secret (e.g. RevenueCat `sk_…`, a Supabase service-role key) lives in the deploy platform's env (Vercel/Cloudflare project settings) and in GitHub Actions secrets for CI — never in client code or a committed file.

## Security

- Set HTTP security headers at the edge (`vercel.json` `headers`): `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, a Content-Security Policy where feasible; constrain CORS to known origins (never reflect `*` with credentials).
- Validate and narrow every external input at the boundary (Zod or equivalent); never trust query/body/header shape. No `dangerouslySetInnerHTML` with unsanitised content (Biome warns — heed it).
- Pin and scope deploy/API tokens to least privilege.

## Accessibility

Ship semantic HTML (Biome's `useSemanticElements`); every interactive control is keyboard-reachable with a visible focus ring and an accessible name; meaning is never carried by colour alone. Target WCAG 2.1 AA.

## Git hooks & commits

`lefthook.yml` is synced — install once with `npx lefthook install`. It runs Biome + typecheck + a secrets scan pre-commit (each scoped to the component via lefthook's `root:`), coverage + build pre-push, and enforces Conventional Commits via the shared `scripts/check-commit-msg.sh` (`type(scope): summary`).

:::caution[Git hooks in a mixed repo]
If this repo also contains an iOS component, the iOS profile syncs a `.pre-commit-config.yaml` and this profile syncs a `lefthook.yml` — both write `.git/hooks`, and whichever `install`s last silently wins. Don't install both. The iOS `pre-commit` framework should own `.git/hooks`; the web checks always run in CI regardless, so rely on that. To keep them running locally too, add them as `repo: local` hooks in the iOS `.pre-commit-config.yaml` (e.g. an entry that runs `npx biome ci` scoped to the web component) rather than installing lefthook alongside pre-commit.
:::

## CI

`web-ci.yml` runs lint → typecheck → test (coverage) → build → dependency audit on `ubuntu-latest`, path-gated to the component. The audit blocks on critical advisories by default; tighten to `high` (and add npm `overrides` for unfixable transitives) per project.
