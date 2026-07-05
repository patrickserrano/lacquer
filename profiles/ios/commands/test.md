---
description: Run tests for the iOS app or specific module
argument-hint: <optional-module-name>
allowed-tools: Bash(flowdeck *)
---

Run tests for {{PROJECT_NAME}} using flowdeck (see CLAUDE.md "Build & Test Tooling").

## Arguments

- `$ARGUMENTS` - Optional test filter (e.g., "Player", "Auth", "Settings")

## Steps

1. Get a simulator UDID:
   ```
   flowdeck simulator list
   ```
   Pick an available iOS simulator and use its **UDID** — simulator names are
   ambiguous across OS versions, so never pass a name.

2. Run tests:
   ```
   flowdeck test -w {{XCODEPROJ}} -s {{SCHEME}} -S <udid> -d {{COMPONENT_PREFIX}}DerivedData
   ```
   - If no argument: run the command above as-is (all tests).
   - If `$ARGUMENTS` is provided: flowdeck test supports test selection —
     consult the flowdeck skill for its test-filter flag and pass the module
     name through it. Do NOT guess or invent a flag name.

3. Parse test results and report:
   - Total tests run
   - Passed/failed count
   - Failed test names with file locations

## On Failure

For each failing test:
1. Report the test name and failure reason
2. Include file path and line number
3. Suggest investigation approach (do NOT auto-fix)
