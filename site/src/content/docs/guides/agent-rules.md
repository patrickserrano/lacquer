---
title: Agent rules
description: The engineering rules synced into every lacquered project's CLAUDE.md.
---

# Core engineering rules (all projects)

## Git and workspace safety

- Work in a git worktree under `.worktrees/`; keep build output in that worktree.
- Never push directly to main. Use atomic commits and a pull request.
- Never force-push or rebase a pushed branch. Do not bypass hooks, CI or branch
  protection (`--force`, `--force-with-lease`, `--no-verify`, `--admin`).
- Never merge with failing or pending required checks. After updating a branch,
  verify the required checks ran again and passed on its new head.
- Never commit or log secrets. Commit examples, not credentials; sensitive
  server keys never belong in a client binary or bundle.

## Verification

- Deliver code proven to work: compile/build, update related tests, and run them.
  Pre-existing failures need fixing or explicit guidance, not a workaround.
- **prove the check can fail**: feed known-bad input or mutate the implementation,
  confirm a named test rejects it, restore it, then confirm the test passes.
  A skipped check, zero selected tests, or an unreadable result is not a pass.
- Treat compiler/linter warnings as errors. Fix the code; never suppress warnings,
  weaken a hook, hide stderr or add `|| true` to a gate without user approval.
- Local checks match CI strictness; a new gate needs its local counterpart or an
  explicit CI-only rationale. Keep managed files identical or explicitly excluded
  through `.lacquer.toml`; never silently fork a generated check.
- Report only what this session's tool output proves; label unverified or skipped work.

## CI and compaction

- Wait with `lacquer wait pr <N>`, not polling or `gh pr checks --watch`.
  Exit 0 means passed; 1 failed, 2 timed out, 3 no checks, 4 wait failed.
  Only 0 is green. The `github-ci-fix` skill carries the recovery procedure.
- Before a follow-up push: `lacquer ci-round begin <N>` with `--reason` for a
  failure fix or `--review` for requested changes. Both spend the two-round budget
  (or configured cap). Unrecorded pushes spend it too; stop on exit 10 and surface
  the ACTION. Never reset the budget without human authorization.
- Across auto-compaction preserve the PR number, branch, worktree path, CI-round
  state (spent/remaining and latest result), verification evidence and next action.
  Resume from that state; do not reset the budget or switch worktrees.

## On-demand procedures

Load the skill matching the task; its references retain the full procedures:

- `engineering-workflow`: implementation/review, delegation, context handoff,
  response style and the optional machine-local papercuts log.
- `project-documentation`: brief → PRD → PCD → plan, doc comments and docs checks.
- `working-with-lacquer`: audit/sync, exclusions, baseline relaxations,
  dependency-update refusals and retirement.
- `github-ci-fix`: failed checks, CI hygiene and round accounting details.
