# Web profile rules

Synced into the `CLAUDE.md` of any component declaring the `web` profile. The
lacquer web stack is **TypeScript + Biome + Vitest + lefthook**, deployed to
Vercel/Cloudflare; the framework (Next.js / Vite / a Node API) is per-project.
Web jobs run on GitHub-hosted runners — there is no Apple toolchain here.

## Read the vendored framework docs first

If the component's framework ships its own docs inside the dependency (e.g. under
`node_modules/<framework>/dist/docs/`, as Next.js and some others do), read the
relevant guide there **before** writing code against it. The installed major may
carry breaking API changes versus your training data, and these fast-moving
frameworks routinely deprecate or rename APIs — trust the vendored copy over
memory and heed its deprecation notices.

## Required package.json scripts

The synced CI and git hooks assume these scripts exist — define them:

| Script | Does |
|--------|------|
| `typecheck` | `tsc --noEmit` |
| `test` | `vitest` |
| `test:coverage` | `vitest run --coverage` |
| `build` | the framework build (`next build`, `vite build`, `tsc`, …) |
| `docs` | `typedoc` (see [Documentation](#documentation-tsdoc--typedoc)) |

## TypeScript — extend the strict base

The lacquer syncs `tsconfig.base.json` (strictness flags only — no framework
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

`biome.json` is synced (format + lint). Run `./node_modules/.bin/biome check --write .`
locally; CI runs `./node_modules/.bin/biome ci --error-on-warnings .`.

**pnpm, not npm.** Every Node component in this fleet uses pnpm. Two things
have to be true in each `package.json`, and CI depends on both:

- a `packageManager` field, e.g. `"packageManager": "pnpm@11.17.0"`. This is the
  ONLY place the version is written. `pnpm/action-setup` reads it, and corepack
  reads it locally, so CI and your machine resolve the same tree by
  construction rather than by coincidence. A version pinned in the workflow as
  well would be a second source of truth, and the two drift silently.
- a committed `pnpm-lock.yaml`, and no `package-lock.json`. CI installs with
  `pnpm install --frozen-lockfile`, the `npm ci` equivalent: it refuses to run
  when the lockfile disagrees with `package.json` instead of quietly fixing it
  up, so a dependency change that was never locked fails the PR that made it.

Migrating an existing project is `pnpm import` (which reads `package-lock.json`
and preserves the resolved versions) followed by deleting the npm lockfile — not
a fresh `pnpm install`, which re-resolves every range and turns a package-manager
change into an unreviewed dependency bump.

Two things bite during that migration, both of them silent:

**Settings live in `pnpm-workspace.yaml`, not package.json's `pnpm` field.**
pnpm 11 stopped reading that field and ignores it with a warning, so a setting
left there does nothing while looking correct. That includes `overrides`.

**Install scripts are blocked by default**, which npm never did — every
postinstall in the tree ran silently there. pnpm fails the install with
ERR_PNPM_IGNORED_BUILDS and requires each one be named in
`onlyBuiltDependencies`. Grant it only where the package needs it (fetching a
platform binary, typically), and treat the list as a review step: each entry is
a dependency allowed arbitrary code execution on every developer machine and CI
runner. Note the state is cached in node_modules — after adding entries, a
re-run of `pnpm install` still reports them ignored until node_modules is
removed, which reads like the setting not working.

**Check `vercel.json`** (or whatever deploys the project) for a pinned
`installCommand`. pixelfoxstudio.com had `"installCommand": "npm ci"`, which
would have kept passing CI and failed every production deploy on the lockfile
that no longer exists.

**The local binary, never a downloading runner.** `npx <tool>` silently
downloads a version when the project has none installed, so a project that never
added `@biomejs/biome` gets a green lint step run by whatever npm served that
minute. One project was in exactly that state — `biome.json` synced, the
dependency in no `package.json`, the Biome step passing — and only
`lacquer doctor` noticed the check could not be running at all. `@biomejs/biome`
and `typedoc` must be real devDependencies.

pnpm makes this the default rather than a rule to remember: `pnpm exec` runs the
locally installed binary and FAILS when there is none. The downloading behaviour
moved to a separate verb, `pnpm dlx`, so it cannot happen by accident. pnpm's
non-hoisted `node_modules` reinforces it — a package that is not a declared
dependency has no `.bin` entry at all, where npm's flat layout could leave a
transitive's binary sitting there and resolving.

**`--error-on-warnings` is the whole gate.** Plain `biome ci` fails on
error-severity rules only, so every warning-severity rule prints and passes —
including the three this config sets deliberately
(`noExcessiveCognitiveComplexity`, `noArrayIndexKey`, `useSemanticElements`) and
the ones `recommended: true` brings, like `noNonNullAssertion`. Drop the flag and
a repo goes green with warnings outstanding; that was measured, not theorised.
The pre-commit hook carries the same flag so local and CI agree.

Fix the code. Never disable a rule inline without explicit user approval
(mirrors the core lint rule).

## Testing — Vitest

- Vitest with coverage; keep meaningful thresholds in `vitest.config.ts`
  (`coverage.thresholds`). The strict tier targets high coverage on logic
  (pure functions, API handlers) — don't chase 100% on glue/UI.
- Co-locate `*.test.ts` with the source, or under `src/**`. Test behaviour, not
  implementation. For React, prefer Testing Library + user-facing queries.
- E2E (Playwright) and accessibility (`@axe-core/playwright`) are project-opt-in;
  when present they run as their own CI job, still on a GitHub-hosted runner.

## Documentation (TSDoc + TypeDoc)

**Documentation is a requirement** — see core "Documentation" for the rule and
the relaxation mechanism. This section is the TypeScript half.

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
the build, prose is not.

The synced `typedoc.json` turns on `validation.notDocumented` (an export with no
comment fails), `invalidLink` (a dead `{@link}` fails), and
`treatValidationWarningsAsErrors`. **`entryPoints` is per-project** — point it at
the package's real public surface; the shipped default assumes a single barrel
file at `src/index.ts`, which is common but not universal.

Add a `docs` script so the local command matches CI:

```jsonc
{ "scripts": { "docs": "typedoc" } }
```

Note `excludeInternal: true` in the config: a symbol marked `@internal` is
omitted from the site *and* exempt from the documented-export rule, which is the
right escape for something exported only for testing or cross-module wiring.
Marking a genuinely public API `@internal` to dodge the rule is the TypeScript
spelling of disabling a lint rule — don't.

## Environment & secrets

- **Never commit a real `.env`.** Commit `.env.example` (and, when you want
  schema-validated env, a `.env.schema` checked with `dotenvx run -- ...`).
- Public values (e.g. `NEXT_PUBLIC_*`) vs. server-only secrets (service-role
  keys, API tokens) must be clearly separated; server secrets never reach the
  client bundle.
- A vendor REST secret (e.g. RevenueCat `sk_…`, a Supabase service-role key)
  lives in the deploy platform's env (Vercel/Cloudflare project settings) and in
  GitHub Actions secrets for CI — never in client code or a committed file.

### Secrets the BUILD needs

Some builds cannot run without env at build time — a Next.js app that statically
collects page data will throw during `pnpm run build` if its CMS vars are unset.
Name those secrets in `[project].build_env` and the synced CI `check` job
declares them:

```toml
[project]
build_env = ["NEXT_PUBLIC_SANITY_PROJECT_ID", "SANITY_API_READ_TOKEN"]
```

renders into `web-ci.yml` as:

```yaml
    env:
      NEXT_PUBLIC_SANITY_PROJECT_ID: ${{ secrets.NEXT_PUBLIC_SANITY_PROJECT_ID }}
      SANITY_API_READ_TOKEN: ${{ secrets.SANITY_API_READ_TOKEN }}
```

**Names only — never values.** The manifest is committed; the rendered form
reads each from `secrets`, so an unset secret is empty rather than baked into a
tracked file. Names are validated against the POSIX environment-name charset,
because they are interpolated into workflow YAML.

This exists because its absence was expensive. With no slot for five secret
names, one project's only escape was `[project].exclude` on the entire
`web-ci.yml` — opting out of the shared workflow to add five lines, then
hand-carrying a full copy that drifts every time the shared one changes. If you
find yourself excluding a whole managed file to add a few lines, the shared
asset is missing a seam; add the seam here rather than the exclusion there.

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

## Local Checks vs CI

Every CI gate and where it runs before push. See core "Local Checks Match CI" —
a new CI job adds a row here, and a hook never runs weaker than its CI twin.

| CI job / step | Local |
|---|---|
| `check` → `biome ci .` | pre-commit `biome` (`--write` on staged files, re-staged) |
| `check` → `pnpm run typecheck` | pre-commit `typecheck` |
| `check` → `pnpm run test:coverage` | pre-push `test` |
| `check` → `pnpm run build` | pre-push `build` |
| `check` → `pnpm audit` | pre-push `audit` (network, so not at commit time) |
| `Docs` → `./node_modules/.bin/typedoc` | pre-push `docs` |
| `No lacquer drift` | `lacquer audit` (exit 3) |

One deliberate asymmetry: pre-commit runs Biome over **staged files** while CI
runs it over the **whole component**. That is inherent to a staged-file hook — a
file you did not stage can still be broken — which is why CI is the authority and
the hook is the fast feedback, never the other way round.

## Git hooks & commits

`lefthook.yml` is synced — install once with `pnpm exec lefthook install`. It runs
Biome + typecheck + a secrets scan pre-commit (each scoped to the component via
lefthook's `root:`), coverage + build pre-push, and enforces **Conventional
Commits** via the shared `scripts/check-commit-msg.sh` (`type(scope): summary`).

**Git hooks in a mixed repo.** If this repo ALSO contains an iOS component, the
iOS profile syncs a `.pre-commit-config.yaml` and this profile syncs a
`lefthook.yml` — both write `.git/hooks`, and whichever `install`s last silently
wins. Don't install both. The iOS `pre-commit` framework should own `.git/hooks`;
the web checks always run in CI regardless, so rely on that. To keep them running
locally too, add them as `repo: local` hooks in the iOS `.pre-commit-config.yaml`
(e.g. an entry that runs `./node_modules/.bin/biome ci` scoped to the web component) rather than
installing lefthook alongside pre-commit.

## CI

`web-ci.yml` runs lint → typecheck → test (coverage) → build → dependency audit
on `ubuntu-latest`, path-gated to the component. The audit blocks on **critical**
advisories by default; tighten to `high` (and add `overrides` in pnpm-workspace.yaml for unfixable
transitives) per project.
