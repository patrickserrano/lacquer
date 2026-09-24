# Web profile rules

- Never commit a real `.env`; commit `.env.example`. Keep server credentials out
  of public env variables and client bundles. Manifest build env entries are
  secret names, never values.
- Run project-pinned check binaries through `./node_modules/.bin/<tool>`;
  resolver/global fallbacks can pass without the declared dependency installed.
  Keep Biome's `--error-on-warnings`, strict TypeScript and local/CI parity.
- A green task must cover the intended packages, including a workspace-root app;
  prove the check can fail rather than trusting zero failures over zero work.
- Read `web-development-guide` for framework docs, package scripts, pnpm,
  TypeScript, Biome, Vitest, TypeDoc, security/accessibility, hooks, build secrets
  and Turborepo task/cache configuration before changing those surfaces.
