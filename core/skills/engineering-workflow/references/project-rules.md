# Core engineering rules (all projects)

These rules are synced by the lacquer into every project's root `CLAUDE.md`. They
are stack-agnostic: they apply equally to iOS, web, Rust, and Go work. Stack
specifics (Swift, SwiftUI, Node, etc.) live in the matching profile body.

## Fundamental Rules

1. **Your job is to deliver code you have proven to work.** This is the #1 most important rule.

2. **A task is not finished unless the code compiles, the build succeeds, and tests are written and pass.** See rule #1 if you are unsure.

3. **Use atomic commits.** Each commit should represent a single logical change.

4. **Never push directly to main.** Always use a pull request.

5. **Always work in a git worktree.** Use `.worktrees/` as the worktree directory (e.g., `.worktrees/feature-name`).

6. **Use expert agents and orchestrate them to find the best solution.** When in doubt, stop and ask for input or clarification.

7. **NEVER disable linting rules without explicit user confirmation.** If code triggers a lint error, FIX THE CODE. Do not add `// swiftlint:disable`, `// swiftformat:disable`, `// biome-ignore`, `// eslint-disable`, `// deno-lint-ignore`, `@ts-ignore`/`@ts-expect-error`, or any similar suppression. If you truly believe a suppression is necessary, STOP and ask the user first.

   **Platform availability is not a suppression.** `@available(iOS 26, *)` and `#available` are Swift's availability system, and the iOS profile *requires* them — see the `swiftui-liquid-glass` skill, which is entirely about gating iOS 26 APIs with a fallback. What this rule bans is the *deprecation* spelling: marking your own declaration `@available(*, deprecated)` or `@available(*, unavailable)` to make a warning go away. Swift does not warn about a deprecated call made from inside a deprecated declaration, so that annotation silences the diagnostic at the call site exactly the way `// swiftlint:disable` does — the fix is to stop calling the deprecated API.

8. **NEVER bypass CI checks or use force flags without explicit user confirmation.** Do not use `--force`, `--force-with-lease`, `--no-verify`, `--admin` (bypasses branch protection on `gh pr merge`), or any other flags that bypass safety checks. Do not merge a PR with failing or pending required checks. If CI is failing, FIX THE ISSUE.

9. **Pre-existing failures are your failures.** If tests fail or builds break — even if the issue existed before your changes — it is your responsibility to fix it. If you genuinely cannot fix a pre-existing failure, STOP and ask for guidance rather than working around it.

10. **Always update related tests when modifying code.** Tests are not optional maintenance — they are part of the deliverable.

11. **NEVER invent a domain name.** Do not fabricate, guess, or placeholder-ify a URL, hostname, or email domain in code, docs, tests, fixtures, config, or anything you write or say. A domain that merely sounds plausible can be a real, live site you don't control — a support email, a redirect target, a fixture that ends up live, marketing copy pointing a real user at it are all real harm, not a cosmetic slip. Use `example.com`/`example.org`/`example.net` (reserved by IANA for exactly this) as a placeholder, or ask the user for the real one. This applies everywhere, not just user-facing surfaces — a "just for now" domain in a test fixture or a script has the same failure mode the moment it's copied somewhere real.

## Response Style

Match response length to what the task needs — calibrate, don't default to verbose.
Lead with the outcome (what happened, what you found) before supporting detail; skip
options you won't pursue, and don't restate context already established in the
session. This is the baseline for every response; `core/skills/caveman` is a
separate, user-invoked, much more aggressive compression style for when the user
explicitly asks for it — it doesn't substitute for calibrating normally the rest of
the time.

## Agent Delegation

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

Match the pattern to the scope of the work:

- **One task, sequential subtasks in this session** → `superpowers:subagent-driven-development` (fresh subagent per subtask, code review between each, one branch).
- **One artifact converging against a checkable bar** → `evaluator-optimizer`.
- **One strategic decision needing a second opinion** → `advisor-checkpoint`.
- **A batch of genuinely independent units (fleet-wide, multi-repo, overnight)** → `manager-loop`.
- **Several angles on the same problem that should challenge each other** (competing-hypothesis debugging, parallel review from different lenses) → agent teams (teammates message each other and self-coordinate on a shared task list), not a subagent — a subagent only reports back to you, it can't debate a peer.

When you do delegate, set the subagent's **effort** in the `Agent`/`Task` call's
model options — that is the only reasoning lever you actually control. Raise it
for hard, ambiguous, multi-file work rather than writing "think harder" into the
prompt: the `think` / `ultrathink` keyword-to-token-budget mapping belonged to the
prior manual extended-thinking API and does nothing on current models.

On any run long enough to report progress partway through, ground the report in
actual tool output — state only what you can point to evidence for from this
session, and say plainly when something is unverified, failing, or skipped, rather
than asserting it's done.

## Context Management

Mid-task compaction is the single strongest predictor of a failed session — work
that compacts before it finishes lands incomplete far more often than work that
doesn't. Manage context so it never happens mid-task:

- **One task per session.** Start a fresh session for a new task instead of
  extending a long, multi-day thread. Long threads accrue cost (repeated cache
  re-reads of bloated context) and hit compaction exactly when the work matters.
- **Hand off deliberately, before pressure forces it.** When a session is getting
  long, write the state down (a plan doc, PR description, or commit) and resume in
  a new session — don't let an automatic mid-task compaction decide what survives.
- **Offload exploration to subagents.** Broad searches and surveys should run in a
  subagent so their output, not their full transcript, lands in the main thread —
  this keeps the main context lean for the actual work.
- **Use `/compact` proactively** with preservation instructions, and **`/clear`
  between unrelated tasks** when this file provides sufficient context.
- **Front-load, don't rebuild, on resume.** Read the last plan/PR/commit and state
  the next action; do not reconstruct context by re-reading everything.
- **Two failed corrections means the context is the problem, not the next attempt.**
  If the same issue has been corrected twice in one session and is still wrong,
  stop retrying — `/clear` and restart with a prompt that incorporates what you
  learned. A clean session with a better prompt outperforms a long one carrying
  failed approaches.

## Papercuts Log

If `~/Developer/papercuts.md` exists, read it **first** when tooling fails in a way
that doesn't make sense — it is a machine-wide log of traps that already cost
sessions time, and the fix is usually one line away.

When you lose time to something new, append one line in this form:
`date · symptom · fix · project`. Put it under **Global** if it applies in any
repo, or under that project's heading if it is tied to one repo's setup.

The file only exists on the operator's machine, so its absence is normal (CI,
cloud sessions, a fresh clone). Nothing depends on it and no check requires it.

## Critical Review Pattern

For high-risk changes — anything touching **security or trust boundaries**,
**concurrency / data-race safety**, **authentication / authorization**, or
**data-integrity boundaries** — implement, then run a **separate adversarial
review of the diff before merging**: the bundled `/code-review` skill, or a
fresh agent/session given only the diff and no implementation context, prompted
to find regressions. This catches bug classes the implementer's own tests
miss. Reserve this for the categories above — bolting a review step onto
every task adds cost without benefit; current models already self-check.
