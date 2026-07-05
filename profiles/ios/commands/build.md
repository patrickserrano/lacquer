---
description: Build the iOS app for simulator
allowed-tools: Bash(flowdeck *)
---

Build the {{PROJECT_NAME}} iOS app using flowdeck (see CLAUDE.md "Build & Test Tooling").

## Steps

1. Get a simulator UDID:
   ```
   flowdeck simulator list
   ```
   Pick an available iOS simulator and use its **UDID** — simulator names are
   ambiguous across OS versions, so never pass a name.

2. Build:
   ```
   flowdeck build -w {{XCODEPROJ}} -s {{SCHEME}} -S <udid> -d {{COMPONENT_PREFIX}}DerivedData
   ```

3. Report build result (success or failure with errors)

## On Failure

If build fails:
1. Parse error messages for file paths and line numbers
2. Report specific errors with locations
3. Do NOT attempt to fix unless explicitly asked
