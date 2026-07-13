---
name: antigravity-rescue
description: >
  Use when stuck, wanting a second implementation or diagnosis pass, or
  handing a substantial task to Google's Antigravity CLI (agy) through a real
  subprocess -- a genuinely different agent harness, not just another model.
  Not relevant when already running under Antigravity CLI itself.
---

# Antigravity Rescue

A thin forwarding wrapper around Google's Antigravity CLI (`agy`). Your only
job is to forward the task and return its output. Do not inspect the repo,
read files, draft your own solution, or do any independent analysis — that
defeats the purpose of getting a second, independent opinion.

## When to use

- Proactively, when stuck: a recurring error, an approach that isn't
  converging, or a task that would benefit from a different agent harness's
  take. `agy` is a multi-vendor router (`agy models` lists Gemini, Claude, and
  GPT-OSS variants) — the value isn't necessarily a different underlying
  model, it's Google's own agent orchestration and tool-execution layer.
- Don't grab a task you can finish quickly yourself — this is a rescue
  mechanism, not a first resort.

## How to forward

```bash
scripts/antigravity-companion.sh task "<the task, verbatim>" [--model <name>] [--resume|--fresh] [--write|--read-only] [--timeout <duration>]
```

- Preserve the task text as-is; strip only the routing flags below before
  forwarding.
- **`--model`**: leave unset by default (agy picks its own default). Only
  pass a value on explicit request — check `agy models` for the current
  list; names span multiple vendors, not just Gemini.
- **`--write` / `--read-only`**: `--write` (default) runs with
  `--dangerously-skip-permissions` so the call completes without hanging on
  an interactive confirmation prompt. `--read-only` is **advisory, not
  enforced** — `agy` has no CLI-level read-only mode, so this only prepends
  an explicit "diagnosis only, do not modify files" instruction to the task
  text. Don't treat it as a hard guarantee the way Claude Code's
  `--permission-mode plan` is.
- **`--resume` / `--fresh`**: `--fresh` (default) starts a new Antigravity
  conversation. Use `--resume` (`agy -c`) only when continuing a prior
  rescue in the same repo.
- **`--timeout`**: defaults to `10m` (agy's own default is `5m`, tight for a
  substantial rescue task). Raise it further for a large ask.
- Return the script's output exactly as printed. No added commentary before
  or after it. If the call fails, surface the raw failure — don't paper over
  it or retry silently.

## Why `--add-dir` is non-negotiable

The companion script always passes `--add-dir "$(pwd)"`. Without it, `agy`
silently operates in its own internal scratch sandbox
(`~/.gemini/antigravity-cli/scratch`) instead of the real project —
confirmed live, not a hypothetical edge case. Never invoke `agy -p` directly
without an explicit workspace directory.
