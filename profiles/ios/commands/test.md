---
description: Run tests for the iOS app or specific module
argument-hint: <optional-module-name>
allowed-tools: mcp__XcodeBuildMCP__*
---

Run tests for {{PROJECT_NAME}}. Optionally specify a module name to filter tests.

## Arguments

- `$ARGUMENTS` - Optional test filter (e.g., "Player", "Auth", "Settings")

## Steps

1. Ensure session defaults are configured for XcodeBuildMCP:
   - projectPath: ios/{{PROJECT_NAME}}.xcodeproj
   - scheme: {{SCHEME}}
   - useLatestOS: true
   - suppressWarnings: true

2. Run tests:
   - If no argument: `mcp__XcodeBuildMCP__test_sim` (all tests)
   - If argument provided: run with filter for that module

3. Parse test results and report:
   - Total tests run
   - Passed/failed count
   - Failed test names with file locations

## On Failure

For each failing test:
1. Report the test name and failure reason
2. Include file path and line number
3. Suggest investigation approach (do NOT auto-fix)
