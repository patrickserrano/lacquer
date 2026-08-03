---
title: Agent rules
description: The engineering rules synced into every lacquered project's CLAUDE.md.
---

:::note
This page mirrors `core/CLAUDE.core.md` — the stack-agnostic rules the lacquer
syncs into every project's root `CLAUDE.md`. It applies equally to iOS, web,
Supabase, and any other stack; stack specifics live in the matching profile
body — see [iOS rules](/lacquer/guides/ios-rules/), [Web
rules](/lacquer/guides/web-rules/), and [Supabase
rules](/lacquer/guides/supabase-rules/).
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

## Response style

Match response length to what the task needs — calibrate, don't default to verbose.
Lead with the outcome before supporting detail; skip options you won't pursue, and
don't restate context already established in the session. This is the baseline for
every response; `core/skills/caveman` is a separate, user-invoked, much more
aggressive compression style for when the user explicitly asks for it — it doesn't
substitute for calibrating normally the rest of the time.

## Effort and thinking

Current-generation Claude models run with adaptive thinking on by default. The old
`think` / `think hard` / `think harder` / `ultrathink` keyword-to-token-budget
mapping is from the prior manual extended-thinking API and no longer reflects how
these models scale reasoning — Claude Sonnet 5 rejects a manual `budget_tokens`
outright. The lever that matters now is **effort** (`low` / `medium` / `high` /
`xhigh` / `max`), set via `/config`, an `Agent`/`Task` call's model options, or a
`Workflow` script's `opts.effort`:

| Effort | Use for |
|--------|---------|
| `high` | Default for most work; leave it unless you have a reason to move |
| `xhigh` | The hardest coding and agentic tasks — what you'd have reached for `ultrathink` for previously |
| `medium` / `low` | Cost- and latency-sensitive work where quality holds at the lower tier |

If a task is under-thinking at its current effort, raise the effort level rather
than adding a "think harder" instruction to the prompt — that no longer does
anything on current models.

## Agent delegation

**A skill is not a subagent.** They are separate tools with separate name spaces,
and this fleet's names collide badly: skills named `*-expert`, `*-pro`,
`*-analyzer`, `*-orchestrator` — and one literally named `ios-debugger-agent` —
read exactly like the agents in `.claude/agents/`, which use the same `-expert`
and `-engineer` suffixes. You cannot tell which is which from the name.

Passing a skill name as a Task `subagent_type` fails with
`Agent type '<name>' not found`. Before delegating by name, check which it is:

- Listed in `.claude/agents/` → `Task` with that `subagent_type`.
- Listed in `.claude/skills/` (or `.agents/skills/`, `.codex/skills/`) → the
  **Skill** tool. Never a `subagent_type`.

Concretely: `swift-testing-expert`, `core-data-expert`, `swiftui-expert-skill`,
`ios-debugger-agent`, and the `xcode-*` family are **skills**. The agent for
Swift work is `ios-swift-engineer`; for tests it is `test-automation-engineer`.
When in doubt, invoke it as a skill — a wrong Skill call is a no-op, a wrong
Task call is a hard error.

Delegate genuinely independent, sizeable work — not everything. A subagent adds
latency and cost; reserve it for tracks large enough that parallelizing or isolating
context actually pays for itself. Don't spin one up to double-check work you already
verified, and don't delegate a task you can finish yourself in a handful of tool
calls.

| Scope | Pattern |
|-------|---------|
| One task, sequential subtasks in this session | `superpowers:subagent-driven-development` |
| One artifact converging against a checkable bar | `evaluator-optimizer` |
| One strategic decision needing a second opinion | `advisor-checkpoint` |
| A batch of genuinely independent units (fleet-wide, multi-repo, overnight) | `manager-loop` |
| Several angles on the same problem that should challenge each other (competing-hypothesis debugging, parallel review from different lenses) | Agent teams — teammates message each other and self-coordinate; a subagent only reports back, it can't debate a peer |

On any run long enough to report progress partway through, ground the report in
actual tool output — state only what you can point to evidence for from this
session, and say plainly when something is unverified, failing, or skipped, rather
than asserting it's done.

## Context management

Mid-task compaction is the single strongest predictor of a failed session — work
that compacts before it finishes lands incomplete far more often than work that
doesn't. Manage context so it never happens mid-task:

- **One task per session.** Start a fresh session for a new task instead of extending a long, multi-day thread.
- **Hand off deliberately, before pressure forces it.** Write the state down (a plan doc, PR description, or commit) and resume in a new session.
- **Offload exploration to subagents.** Broad searches and surveys should run in a subagent so only their output lands in the main thread.
- **Use `/compact` proactively** with preservation instructions, and **`/clear` between unrelated tasks** when this file provides sufficient context.
- **Front-load, don't rebuild, on resume.** Read the last plan/PR/commit and state the next action instead of reconstructing context by re-reading everything.
- **Two failed corrections means the context is the problem, not the next attempt.** If the same issue has been corrected twice in one session and is still wrong, stop retrying — `/clear` and restart with a prompt that incorporates what you learned. A clean session with a better prompt outperforms a long one carrying failed approaches.

## Docs taxonomy

A project starts from a **brief** at `docs/brief.md` — the pitch, scope, and
roadmap, the human-authored source of truth for what's being built. `lacquer
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
review of the diff before merging**: the bundled `/code-review` skill, or a
fresh agent/session given only the diff and no implementation context, prompted
to find regressions. This catches bug classes the implementer's own tests
miss. Reserve this for the categories above — bolting a review step onto
every task adds cost without benefit; current models already self-check.

## CI hygiene

- Keep CI action/tool versions **consistent across all workflows** — drift causes subtle job-to-job behavior differences.
- Update a branch from main before merging when it's behind; **after** updating, re-confirm the required checks re-ran green (an update can drop a pending check).
- Never merge on partial signals: require every *required* check to pass and the merge state to be clean.

## Documentation

Every declaration carries a doc comment, and the docs build clean. This is a
baseline like warnings-as-errors, not a style preference — the `Docs` job in
every stack's CI checks it and it blocks the merge.

Two halves, because neither implies the other:

1. **It exists.** Every declaration above `private` has a doc comment.
2. **It resolves.** The docs actually build: every symbol link points at
   something real, and the markup parses. A doc comment referring to a type
   renamed three refactors ago is worse than no comment — it's confidently wrong.

Each stack uses its native toolchain, and each publishes to its own
subdirectory of the project's GitHub Pages, so a repo with several components
gets one site per stack rather than one stack overwriting another:

| Stack | Checked with | Published at |
|-------|--------------|--------------|
| iOS / Swift | SwiftLint `missing_docs` + `xcodebuild docbuild` | `/swift/` |
| Web / TypeScript | TypeDoc `validation.notDocumented` + `invalidLink` | `/api/` |
| Supabase / Deno | `deno doc --lint` | `/db/` |

Write the comment for the reader who doesn't already know: what the thing is
for, and what a caller must know — preconditions, ownership, units, what happens
on failure. Don't restate the signature. If the only honest doc comment is a
restatement, the name is doing its job and the *type* or *module* is where the
explanation belongs.

### Where the sites are hosted

GitHub Pages is the default and needs no secrets. Each stack's docs workflow
writes its own subdirectory of a `gh-pages` branch and the root index is
regenerated from whatever is present, so `gh-pages` *is* the composed site and
adding a stack later needs no coordination. Turn it on once per repo:
Settings → Pages → Source → *Deploy from a branch* → `gh-pages` / (root).

Cloudflare is an optional second target for that same tree. Set
`CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` and `docs-cloudflare.yml`
deploys `gh-pages` to Workers Static Assets after each publish; leave them unset
and it skips with a notice.

- **Workers, not Pages.** Pages is still supported, but Cloudflare has said all
  new investment goes to Workers, which serves static assets at the same cost.
- **Not Cloudflare Drop.** Drop is the obvious-looking fit — upload a folder, no
  account — but it's drag-and-drop in the dashboard with no CLI or API, and the
  deployment expires after an hour unless a human claims it. It's for sharing a
  preview, not for publishing.
- **The site is served under `/<repo>/`,** matching GitHub Pages exactly. DocC
  bakes its hosting base path into every asset URL at build time, so serving the
  same archive at a domain root would 404 on its own CSS. Nesting means one build
  serves both targets; the bare domain redirects.

A project that can't comply yet relaxes it — time-boxed, never open-ended, in
its own `.lacquer.toml`:

```toml
[baseline.relax]
documentation = { until = "2026-11-01", reason = "legacy Core/, tracked in #212" }
```

Both fields are required, the checks still run and report while relaxed, and an
expired relaxation is a hard failure — so the debt stays visible instead of
becoming policy by default.

## Warnings as errors

Treat compiler and linter warnings as errors — ship zero-warning builds. Don't
suppress a warning; fix the code (see rule 7).
