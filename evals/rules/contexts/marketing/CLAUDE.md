# Core engineering rules (all projects)

## Git and workspace safety

- Work in a git worktree under `.worktrees/`; keep build output in that worktree.
- Before briefing or starting work, run `lacquer decisions` and `lacquer decisions --fleet`; quote the operator's words verbatim.
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

# Marketing profile rules

Synced into the `CLAUDE.md` of any component declaring the `marketing` profile.
Unlike `ios`, `web`, and `supabase`, `marketing` is never auto-detected — there is
no marketing "stack" on disk to find. Add it to a component's `profiles` in
`.lacquer.toml` deliberately, when marketing/growth work is actually in scope for
that component (an agency, a growth team, a founder running their own GTM).

This profile ships skills only: no CI workflow, no git hooks, no lint config, no
`doctor.toml`/`baseline.toml`/`fix.toml`. There is nothing here to build, lint, or
test, so it adds no gate and no CI job — just a shelf of skills Claude reaches for
by description match when a marketing task comes up.

## What's in here

~50 skills covering the marketing/growth surface: paid ads and ad creative,
SEO (technical, AI-search, programmatic, schema), copywriting and copy editing,
conversion funnels (signup, onboarding, paywalls, popups, CRO), lifecycle
(email, SMS), growth channels (social, PR, influencer, community, referrals,
events, co-marketing), pricing/offers, and planning (marketing-plan,
marketing-ideas, marketing-council, customer-research, competitor research).
Each skill's own `SKILL.md` frontmatter describes exactly when to reach for it —
that routing lives in the skill, not here.

Vendored from an upstream marketing-skills package (Corey Haines); see
`skills/LICENSE-upstream-marketingskills`.

## Where to start

If this is the first marketing skill invoked in a project, start with
**product-marketing**: it builds `.agents/product-marketing.md`, the positioning
and audience context every other marketing skill in this profile reads before
doing its own work. Skipping it means every skill re-derives (or guesses at)
the same context independently.

For an open-ended "what should we do" question, **marketing-council** or
**marketing-ideas** are the entry points; for a specific channel or asset, go
straight to the matching skill (ads, copywriting, seo-audit, and so on) — each
one's frontmatter cross-references the adjacent skills it hands off to.
