# Web / TypeScript

- Read the installed framework's vendored docs before using its APIs.
- From this component directory, use its package.json `packageManager` and lock:
  `pnpm install --frozen-lockfile` for declared pnpm, otherwise `npm ci`.
  Run `./node_modules/.bin/biome ci --error-on-warnings .`, then the package
  scripts `typecheck`, `test:coverage`, `build` and `docs` using that manager
  (for example `pnpm run typecheck` or `npm run typecheck`).
- Local checks in the repository's lefthook.yml map to `.github/workflows/web-ci.yml`.
  Use project-pinned binaries, strict TypeScript and warnings-as-errors. Prove
  every intended workspace package, including the root app, was checked; an empty
  task selection or stale cache is not verification.
- Commit `.env.example`, never real `.env`. Keep server credentials out of public
  environment variables and client bundles. Authenticate server actions, validate
  external input and enforce authorization at data access boundaries.
