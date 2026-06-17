---
description: Update current worktree from main branch
allowed-tools: Bash(git *)
---

Safely update the current worktree with latest changes from main.

## Prerequisites

- Must be in a git worktree (not the main working directory)
- Working directory should be clean (no uncommitted changes)

## Steps

1. Verify we're in a worktree: `git worktree list`
2. Check for uncommitted changes: `git status --porcelain`
3. If changes exist, warn user and STOP
4. Fetch latest: `git fetch origin main`
5. Rebase current branch onto main: `git rebase origin/main`
6. Report result

## On Conflict

If rebase conflicts occur:
1. Report conflicting files
2. Abort rebase: `git rebase --abort`
3. Suggest manual resolution or merge strategy

## Safety

- Never force push
- Never modify main branch
- Always preserve local commits
