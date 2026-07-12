---
title: Agent rules
description: The engineering rules synced into every harnessed project's CLAUDE.md.
---

:::note
This page mirrors `core/CLAUDE.core.md` — the stack-agnostic rules the harness
syncs into every project's root `CLAUDE.md`. It applies equally to iOS, web,
Supabase, and any other stack; stack specifics live in the matching profile body.
:::

## Fundamental rules

1. **Your job is to deliver code you have proven to work.** This is the #1 most important rule.
2. **A task is not finished unless the code compiles, the build succeeds, and tests are written and pass.** See rule #1 if you are unsure.
3. **Use atomic commits.** Each commit should represent a single logical change.
4. **Never push directly to main.** Always use a pull request.
5. **Always work in a git worktree.** Use `.worktrees/` as the worktree directory (e.g., `.worktrees/feature-name`).
6. **Use expert agents and orchestrate them to find the best solution.** When in doubt, stop and ask for input or clarification.
7. **NEVER disable linting rules without explicit user confirmation.** If code triggers a lint error, fix the code. Do not add `swiftlint:disable`, `swiftformat:disable`, `biome-ignore`, `eslint-disable`, `deno-lint-ignore`, `@ts-ignore`/`@ts-expect-error`, `@available`, or any similar suppression. If a suppression genuinely seems necessary, stop and ask first.
8. **NEVER bypass CI checks or use force flags without explicit user confirmation.** No `--force`, `--force-with-lease`, `--no-verify`, `--admin` (bypasses branch protection on `gh pr merge`), or similar. Don't merge a PR with failing or pending required checks — fix the issue.
9. **Pre-existing failures are your failures.** If tests fail or builds break — even if the issue predates your changes — it's your responsibility to fix it. If you genuinely can't, stop and ask for guidance rather than working around it.
10. **Always update related tests when modifying code.** Tests are part of the deliverable, not optional maintenance.

## Extended thinking

| Keyword | Token budget | Use for |
|---------|-------------|---------|
| `think` | ~4K | Simple planning, quick decisions |
| `think hard` | ~10K | Feature implementation, debugging |
| `think harder` | ~16K | Complex architecture, tricky bugs |
| `ultrathink` | ~32K | Major architecture decisions, critical debugging |

## Context management

Mid-task compaction is the single strongest predictor of a failed session — work
that compacts before it finishes lands incomplete far more often than work that
doesn't. Manage context so it never happens mid-task:

- **One task per session.** Start a fresh session for a new task instead of extending a long, multi-day thread.
- **Hand off deliberately, before pressure forces it.** Write the state down (a plan doc, PR description, or commit) and resume in a new session.
- **Offload exploration to subagents.** Broad searches and surveys should run in a subagent so only their output lands in the main thread.
- **Use `/compact` proactively** with preservation instructions, and **`/clear` between unrelated tasks** when this file provides sufficient context.
- **Front-load, don't rebuild, on resume.** Read the last plan/PR/commit and state the next action instead of reconstructing context by re-reading everything.

## Docs taxonomy

A project starts from a **brief** at `docs/brief.md` — the pitch, scope, and
roadmap, the human-authored source of truth for what's being built. `harness
init` scaffolds a stub; paste the real brief there first. Feature work then flows
through three dated doc types named `YYYY-MM-DD-<feature>-<type>.md`:

| Type | Answers | Location |
|------|---------|----------|
| Brief | The product pitch, scope, roadmap — source of truth | `docs/brief.md` |
| PRD | The *what* and *why* | `docs/prds/` |
| PCD | The *how* — UX + technical shape | `docs/pcds/` |
| Plan | Bite-sized implementation tasks | `docs/plans/` |

Derive the PRD from the brief, then the PCD, then the plan. Keep each artifact in
its dated file so history stays auditable.

## Critical review pattern

For high-risk changes — anything touching **security or trust boundaries**,
**concurrency / data-race safety**, **authentication / authorization**, or
**data-integrity boundaries** — implement, then run a **separate adversarial
review of the diff before merging**: a fresh agent or session given only the
diff, no implementation context, prompted to find regressions. This catches bug
classes the implementer's own tests miss.

## CI hygiene

- Keep CI action/tool versions **consistent across all workflows** — drift causes subtle job-to-job behavior differences.
- Update a branch from main before merging when it's behind; **after** updating, re-confirm the required checks re-ran green (an update can drop a pending check).
- Never merge on partial signals: require every *required* check to pass and the merge state to be clean.

## Warnings as errors

Treat compiler and linter warnings as errors — ship zero-warning builds. Don't
suppress a warning; fix the code (see rule 7).
