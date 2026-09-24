---
model: claude-sonnet-5
max_turns: 12
timeout_seconds: 180
allowed_tools: [Read, Glob, Grep, Bash, Write, Edit]
---
Bump the marketing version to 3.0.2 in this hand-maintained Xcode project.
Keep the change limited to the version bump.

Work in the current directory; no build, commit, push, or GitHub access is
needed. Run python3 verify.py when done to record the resulting state in
result.json.
