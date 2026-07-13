---
title: Plugins catalog
description: Machine-level Claude Code plugins this fleet relies on, and where each comes from.
---

Unlike skills (per-project, synced by `lacquer sync`), Claude Code plugins
install once at **user** scope and are shared across every project on a
machine. `core/bootstrap/plugins.toml` lists the marketplaces and plugins this
fleet relies on; `lacquer plugins` applies it via `claude plugin marketplace
add` / `claude plugin install`. See the [README](https://github.com/patrickserrano/lacquer#plugins-machine-level-bootstrap)
for the mechanism — this page is the catalog of what's actually installed and
where each plugin comes from.

Only plugins actually *enabled* on the reference machine are listed — one
installed-but-disabled there is a deliberate choice, not silently re-enabled
on a fresh machine.

| Plugin | What it's for | Source |
|--------|---------------|--------|
| `superpowers` | TDD, brainstorming, writing-plans, code-reviewer, git-worktree workflows. | [obra/superpowers](https://github.com/obra/superpowers) |
| `codex` | `/review`, `/adversarial-review`, `/rescue` — a live Codex subprocess for a genuine second opinion, distinct from Claude's own self-review. | [openai/codex-plugin-cc](https://github.com/openai/codex-plugin-cc/tree/main/plugins/codex) |
| `context7` | Up-to-date library/framework docs, fetched on demand. | [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official/tree/main/external_plugins/context7) |
| `figma` | Figma design-to-code / code-to-design bridge. | [figma/mcp-server-guide](https://github.com/figma/mcp-server-guide) |
| `security-guidance` | Injection/secrets/workflow-security guidance surfaced on relevant edits. | [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official/tree/main/plugins/security-guidance) |
| `telemetrydeck-analytics` | Session/usage analytics — ask questions in plain English, get real queries. | [agenkin/telemetrydeck-analytics](https://github.com/agenkin/telemetrydeck-analytics) |

Each plugin belongs to a marketplace (the index `claude plugin marketplace add`
points at); `context7` and `security-guidance` both live inside
`anthropics/claude-plugins-official`, one marketplace with many plugins —
`superpowers`, `codex`, `figma`, and `telemetrydeck-analytics` each have their
own dedicated marketplace/repo.
