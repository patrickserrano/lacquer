# Core engineering rules (all projects)

These rules are synced by the harness into every project's root `CLAUDE.md`. They
are stack-agnostic: they apply equally to iOS, web, Rust, and Go work. Stack
specifics (Swift, SwiftUI, Node, etc.) live in the matching profile body.

## Fundamental Rules

1. **Your job is to deliver code you have proven to work.** This is the #1 most important rule.

2. **A task is not finished unless the code compiles, the build succeeds, and tests are written and pass.** See rule #1 if you are unsure.

3. **Use atomic commits.** Each commit should represent a single logical change.

4. **Never push directly to main.** Always use a pull request.

5. **Always work in a git worktree.** Use `.worktrees/` as the worktree directory (e.g., `.worktrees/feature-name`).

6. **Use expert agents and orchestrate them to find the best solution.** When in doubt, stop and ask for input or clarification.

7. **NEVER disable linting rules without explicit user confirmation.** If code triggers a lint error, FIX THE CODE. Do not add `disable` directives or any similar escape hatches. If you believe a disable comment is truly necessary, STOP and ask the user first.

8. **NEVER bypass CI checks or use force flags without explicit user confirmation.** Do not use `--force`, `--no-verify`, or any other flags that bypass safety checks. If CI is failing, FIX THE ISSUE.

9. **Pre-existing failures are your failures.** If tests fail or builds break — even if the issue existed before your changes — it is your responsibility to fix it.

10. **Always update related tests when modifying code.** Tests are not optional maintenance — they are part of the deliverable.

## Extended Thinking

| Keyword | Token Budget | Use For |
|---------|-------------|---------|
| `think` | ~4K tokens | Simple planning, quick decisions |
| `think hard` | ~10K tokens | Feature implementation, debugging |
| `think harder` | ~16K tokens | Complex architecture, tricky bugs |
| `ultrathink` | ~32K tokens | Major architecture decisions, critical debugging |

## Context Management

- **Use `/compact` proactively** with preservation instructions.
- **Use `/clear` between unrelated tasks** when this file provides sufficient context.

## Docs Taxonomy

A project starts from a **brief** at `docs/brief.md` — the pitch, scope, and
roadmap, the human-authored source of truth for what's being built. `harness
init` scaffolds a stub; paste the real brief there first. Feature work then flows
through three dated doc types named `YYYY-MM-DD-<feature>-<type>.md`:

- **Brief** (the product pitch, scope, roadmap — the source of truth) → `docs/brief.md`
- **PRD** (product requirements — the *what* and *why*) → `docs/prds/`
- **PCD** (product/component design — the *how*, UX + technical shape) → `docs/pcds/`
- **Plan** (bite-sized implementation tasks) → `docs/plans/`

Derive the PRD from the brief, then the PCD, then the Plan. Keep each artifact in
its dated file so history is auditable.

## Critical Review Pattern

For high-risk changes — anything touching **security or trust boundaries**,
**concurrency / data-race safety**, **authentication / authorization**, or
**data-integrity boundaries** — implement, then run a **separate adversarial
review of the diff before merging**: a fresh agent or session given only the
diff, no implementation context, prompted to find regressions. This catches bug
classes the implementer's own tests miss.

## CI Hygiene

- Keep CI action/tool versions **consistent across all workflows** (one pin each for shared actions) — drift causes subtle job-to-job behavior differences.
- Update a branch from main before merging when it is behind; **after** updating, re-confirm the required checks re-ran green before merging (an update can drop a pending check).
- Never merge on partial signals: require every *required* check to pass and the merge state to be clean.

## Warnings as Errors

Treat compiler and linter warnings as errors — ship zero-warning builds. Don't
suppress a warning; fix the code (see Fundamental Rule #7).
