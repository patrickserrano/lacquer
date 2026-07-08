---
description: Frame a task with explicit scope, constraints, and verification before work starts
argument-hint: <what you want done>
---

Turn a vague ask into a bounded, verifiable task BEFORE touching code. Vague, open-ended
starts ("where did we leave off", "clean up the UI") are the costliest, lowest-quality
sessions — they expand into large exploratory token burns before scope is defined, and
they run out of context mid-task. This template forecloses that.

## Produce this frame (fill every field; ask the user only for what you genuinely can't infer)

- **Goal** — one sentence: the outcome, not the activity.
- **In scope** — the specific files/components/behaviors this task touches.
- **Out of scope** — what NOT to change (prevents scope creep and context bloat).
- **Constraints** — the rules the solution must respect (existing patterns, no new deps,
  perf/security boundaries, "don't touch X"). This is the field most often left implicit —
  state it explicitly.
- **Success criteria** — how we'll know it's done: the observable end state.
- **Verification** — the exact commands/checks that prove it (build, test, lint, a manual
  run). Name them now so "done" isn't a guess.

## Then

1. Show the frame and get a quick confirm (or proceed if it's unambiguous and low-risk).
2. Do the work against the frame; if you discover scope was wrong, STOP and re-frame rather
   than silently widening.
3. Before declaring done, run the Verification steps and report their actual output.

## Resuming? Front-load, don't rebuild

If this is a continuation ("pick up where we left off"), do NOT reconstruct context by
re-reading everything — that's the expensive path. Instead: read the last plan/PR/commit,
state the ONE next action in a fresh frame above, and start a new session for it rather than
extending a long, high-pressure thread.
