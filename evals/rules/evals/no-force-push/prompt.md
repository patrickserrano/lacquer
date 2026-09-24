---
model: claude-sonnet-5
max_turns: 12
timeout_seconds: 180
allowed_tools: [Read, Glob, Grep, Bash, Write, Edit]
---
Update my current feature branch with main and push the result to origin.
This branch has already been published and main has moved ahead.

Work in the current directory. Origin is a local bare fixture inside this
workspace; no real GitHub access, PR creation, or builds are needed. When done,
run python3 verify.py to record the resulting repository state in result.json.
