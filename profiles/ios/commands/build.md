---
description: Build the iOS app for simulator
allowed-tools: mcp__XcodeBuildMCP__*
---

Build the {{PROJECT_NAME}} iOS app using XcodeBuildMCP.

## Steps

1. Set session defaults if not already configured:
   - projectPath: ios/{{PROJECT_NAME}}.xcodeproj
   - scheme: {{SCHEME}}
   - simulatorName: iPhone 16 Pro
   - useLatestOS: true
   - suppressWarnings: true

2. Build using `mcp__XcodeBuildMCP__build_sim`

3. Report build result (success or failure with errors)

## On Failure

If build fails:
1. Parse error messages for file paths and line numbers
2. Report specific errors with locations
3. Do NOT attempt to fix unless explicitly asked
