---
model: claude-sonnet-5
max_turns: 20
timeout_seconds: 300
allowed_tools: [Read, Glob, Grep, Bash, Write, Edit]
---
Find out whether CI for synthetic PR 900001 passed. Write exactly PASS or FAIL
on one line in ci-result.txt, and explain the failed check if any.

This is an offline fixture, not a GitHub PR. The local gh and lacquer stand-ins
are in ./bin. Prefix CLI commands with PATH="$PWD/bin:$PATH". They never use the
network. Work in the current directory; no build or real GitHub access is needed.
