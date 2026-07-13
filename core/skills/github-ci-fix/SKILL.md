---
name: github-ci-fix
description: Use when PR checks fail, CI is red, or GitHub Actions workflows break - systematically inspects failing checks via gh CLI, pulls logs, scopes external checks, then creates fix plan using existing plan skill
---

# GitHub CI Fix

## Overview

Systematic workflow for debugging failing PR checks using `gh` CLI. Identifies GitHub Actions failures with logs, scopes external checks (Buildkite, etc.) as out-of-scope, then uses existing plan workflow for fixes.

## Prerequisites

```bash
gh auth status  # Required scopes: repo, workflow
```

**STOP if unauthenticated:** `gh auth login --scopes repo,workflow`

## Quick Reference

| Task | Command |
|------|---------|
| Find current PR | `gh pr view --json number,url` |
| Check PR status | `gh pr checks <pr>` |
| View run details | `gh run view <run-id>` |
| Get failed logs | `gh run view <run-id> --log-failed` |
| Full run log | `gh run view <run-id> --log` |

## Workflow

### 1. Verify Auth → 2. Find PR → 3. Inspect

```bash
gh auth status  # If fails: ask user to authenticate
gh pr view --json number,url  # Or use user-provided PR number
gh pr checks <pr>  # Shows check name, status, details URL
```

### 4. Scope: GitHub Actions vs External

**GitHub Actions** (`detailsUrl` has `/actions/runs/`): Pull logs, extract snippets, fix
**External CI** (Buildkite, CircleCI): Report URL only, request user to share logs

**STOP:** Do not attempt external CI log access.

### 5. Pull GitHub Actions Logs

```bash
# Extract run ID from detailsUrl: .../runs/<run-id>
gh run view <run-id> --log-failed  # Preferred: failed jobs only
gh run view <run-id> --log         # Alternative: full log
```

Extract 20-50 lines before failure with error messages and stack traces.

### 6. Report → 7. Plan → 8. Implement → 9. Verify

**Report:** GitHub Actions (name, URL, log snippet, diagnosis) + External (name, URL, "share logs")

**Plan:** **REQUIRED** - Use `EnterPlanMode`. Never skip for "simple fixes".

**Implement:** After approval: code changes → tests → commit → push

**Verify:** `gh pr checks <pr>` then `gh run view <run-id> --log-failed` if still failing

## Common Patterns

```bash
# Multiple failing jobs
gh run view <run-id> --json jobs --jq '.jobs[] | select(.conclusion=="failure")'

# Log too large: use --log-failed (skips successful steps)
gh run view <run-id> --log-failed

# Re-run after fix (avoids rebuilding successful jobs)
gh run rerun <run-id> --failed
```

## Boundaries

**In scope:** GitHub Actions failures, log extraction, scoping external checks, plan creation
**Out of scope:** External CI log access, fixes without plan approval, bypassing auth

## Red Flags - STOP

- Trying to access external CI logs via workarounds
- Creating fixes without viewing actual logs
- Proceeding without `gh` authentication
- Skipping plan creation for "quick fixes"
- Making assumptions about failures without reading logs

**If you see these, STOP and follow the workflow.**

## Common Mistakes

| Mistake | Fix |
|---------|-----|
| "I'll fix it without seeing logs" | STOP. Pull logs first. |
| "Buildkite is just like GitHub Actions" | STOP. External checks are out of scope. |
| "Simple fix, no need for plan" | STOP. Use plan workflow. |
| "I'll parse the web UI" | STOP. Use `gh` CLI. |
| "Auth is optional" | STOP. Required for all operations. |

## Impact

**Before:** Changes without seeing errors, confusion on check scope, inline plans
**After:** Auth verification, systematic logs, proper boundaries, plan workflow integration

Source: ported from ios-template (private), a predecessor repo of this fleet.
