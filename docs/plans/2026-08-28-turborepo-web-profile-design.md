# Turborepo-Aware Web Profile — Design

**Status:** Design validated via brainstorming; not yet planned/implemented.

**Origin:** pixelfoxstudio.com added `apps/admin` (a second Next.js app, Payload
CMS, in a pnpm workspace alongside the root site). Lacquer's `[[component]]`
model supports one component per profile (`internal/config/config.go`), so
`apps/admin` cannot be declared and is excluded (`.lacquer.toml`
`[project].exclude`, `until = "2026-11-30"`) — unmanaged: no synced hooks, CI,
or lint config, checks running only through ad hoc workspace scripts.

**Goal:** Fold multi-app orchestration into the app layer (Turborepo) instead
of building "multiple components per profile" in the lacquer. One `web`
component still maps to one `[[component]]` entry, pointed at the workspace
root; Turborepo fans a task out to every package inside it. This makes
`apps/admin` (and any future `apps/*`) reachable by the existing `.` component
without a lacquer schema change.

---

## Architecture

- A `web` component stays a single `[[component]]` entry at the workspace
  root (where `pnpm-workspace.yaml` lives).
- `turbo.json` is **project-authored, never synced**. Task graphs and outputs
  (`.next/**` vs `dist/**`, DB migrations, etc.) are genuinely per-project;
  the lacquer only needs task **names** to agree with what CI invokes
  (`lint`, `typecheck`, `test`, `build`).
- The synced CI workflow is **opt-in**: if `turbo.json` exists at the
  component root, run `turbo run <task>`; if not, fall back to today's flat
  `pnpm run <task>`. Single-app web projects (e.g. `rail-web`, today) are
  untouched until they choose to adopt turbo.
- `internal/config/config.go`'s one-component-per-profile rule is unchanged.
  This design makes it unnecessary for the web stack's multi-app case, not
  obsolete in general (a real second *stack*, e.g. a second unrelated
  service, still needs its own component).

## CI workflow (`profiles/web/workflows/ci.yml`)

Each of the four named `check` steps gains a turbo/no-turbo branch, keeping
today's step names so a failure still reads the same in the GitHub UI:

```yaml
- name: Lint
  run: |
    if [ -f turbo.json ]; then
      ./node_modules/.bin/turbo run lint
    else
      ./node_modules/.bin/biome ci --error-on-warnings .
    fi

- name: Type check
  run: |
    if [ -f turbo.json ]; then
      ./node_modules/.bin/turbo run typecheck
    else
      pnpm run typecheck
    fi

- name: Test (coverage)
  run: |
    if [ -f turbo.json ]; then
      ./node_modules/.bin/turbo run test
    else
      pnpm run test:coverage
    fi

- name: Build
  run: |
    if [ -f turbo.json ]; then
      ./node_modules/.bin/turbo run build
    else
      pnpm run build
    fi
```

`./node_modules/.bin/turbo`, never `pnpm dlx turbo` or `npx turbo` — this repo
bans resolver-dispatched tool invocation (`internal/shipped/shipped_test.go`'s
`TestShippedContentAvoidsBannedPatterns`, the "resolver-dispatched invocation
of a project dependency" entry): `dlx`/`npx` fall through to PATH or download
an unpinned version, so a project without `turbo` as a devDependency would get
a green step run by whatever version happened to be resolved, exactly the
`sleevetap`/biome incident that ban exists to prevent. Matches the `Biome`
step's own `./node_modules/.bin/biome` convention immediately below it.

All four steps still run inside `working-directory: "{{COMPONENT_PREFIX}}."`
— already the workspace root for a `web` component, so `{{COMPONENT_PREFIX}}`
needs no change. A package that doesn't define a given script (e.g.
`apps/admin` has no `lint` script yet) is silently skipped by turbo — normal
behavior, not a CI failure, so partial adoption inside a workspace doesn't
block.

`pnpm audit --audit-level=critical` and the `lacquer doctor --profile web`
step are unaffected: both already operate at the workspace root regardless of
package count.

## `doctor.toml` / `fix.toml` — no changes needed

Every probe in `profiles/web/doctor.toml` invokes `{component}/node_modules/.bin/biome`
and `{component}/node_modules/.bin/typedoc` directly, not through turbo or a
package script — they're proving the *shipped config* (biome.json,
typedoc.json) can reject known-bad input, independent of how many apps sit in
the workspace. `fix.toml`'s `biome check --write .` also runs directly at the
component root; biome's own recursion already reaches subdirectories like
`apps/admin/src/**`. Neither file needs a turbo-aware branch.

## Env vars / secrets

`[project].build_env` in `.lacquer.toml` already accepts an arbitrary flat
list of secret **names** (rendered into `{{WEB_BUILD_ENV}}`, values pulled
from GitHub `secrets`). No schema change: a project with multiple apps needing
different secrets (pixelfoxstudio.com's root `NEXT_PUBLIC_SANITY_*` vs.
`apps/admin`'s `CMS_DATABASE_URI`/`PAYLOAD_SECRET`/`R2_*`) just lists all of
them — they land in the same job's environment since one `turbo run build`
step builds every package together.

**Explicitly out of scope:** whether `apps/admin`'s build can succeed in CI
without a live Postgres instance (service container, or a build-time flag to
skip DB-dependent static generation). That's a pixelfoxstudio.com-side
implementation concern, not a lacquer design question.

---

## Rollout

**Lacquer repo:**
1. Add the turbo/no-turbo branches to `profiles/web/workflows/ci.yml`.
2. Document the convention in `profiles/web/CLAUDE.web.md`: a workspace with
   multiple apps should adopt Turborepo, with `turbo.json` task names
   `lint`/`typecheck`/`test`/`build` matching what CI invokes.
3. Version bump per the lacquer's normal process (profile content only — no
   `internal/config` change, no new token).

**pixelfoxstudio.com** (separate PR, after `lacquer sync` picks up the above):
1. Add `turbo` devDependency; add root `turbo.json` with `lint`/`typecheck`/
   `test`/`build` tasks (root package already has all four scripts).
2. Give `apps/admin` a `lint` script (currently missing) so turbo gates it
   rather than skipping it.
3. Add `CMS_DATABASE_URI`, `PAYLOAD_SECRET`, `R2_*` to `[project].build_env`.
4. Remove the `apps/admin` exclude entry in `.lacquer.toml` — once
   `turbo.json` exists at root, `apps/admin` is a workspace package reached
   by the existing `.` component, not a second component. This resolves what
   the exclusion's `until = "2026-11-30"` date was watching for.
5. Handle `apps/admin`'s CI-time Postgres dependency (own problem, per above).

**rail-web** stays on the no-turbo fallback path indefinitely, until it grows
a second app.

## Explicitly deferred / not built here

- Multiple components per profile in `internal/config/config.go` — this
  design makes it unneeded for the web stack's known case, but a real
  second *stack* in one project still needs it if it ever comes up.
- Turborepo remote caching (Vercel Remote Cache or self-hosted) — v1 gets
  turbo's local task-graph fan-out/ordering only; CI runners are ephemeral so
  remote caching would need its own token/secret plumbing, deferred until the
  opt-in mechanism itself is proven.
- A `doctor`/`audit` check that verifies workspace package scripts match
  `turbo.json` task names (catching e.g. a package with `build` but no
  `typecheck` silently never being typechecked) — worth having, not required
  for v1 since turbo's skip-if-missing behavior fails safe (under-runs, never
  reports false green).
