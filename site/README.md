# harness docs

[Astro Starlight](https://starlight.astro.build) site with the
[Flexoki theme](https://delucis.github.io/starlight-theme-flexoki/), documenting
the harness CLI, its agent rules, and its skill catalog for both humans and
Claude Code sessions.

Content lives in `src/content/docs/`; pages are `.md`/`.mdx` files routed by
filename. Regenerate the reference pages from `../README.md`,
`../core/CLAUDE.core.md`, and `../core/skills/`/`../profiles/*/skills/` when
those change upstream — this site does not read them dynamically.

## Commands

| Command | Action |
|---------|--------|
| `npm install` | Install dependencies |
| `npm run dev` | Local dev server at `localhost:4321/harness/` |
| `npm run build` | Build to `./dist/` |
| `npm run preview` | Preview the production build locally |

Deploys via `.github/workflows/docs.yml` on push to `main` (GitHub Pages must be
enabled in repo settings, source: GitHub Actions).
