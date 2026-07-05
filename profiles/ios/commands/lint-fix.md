---
description: Run all formatters and linters with auto-fix
allowed-tools: Bash(swiftformat *), Bash(swiftlint *)
---

Run all code quality tools to format and lint the iOS codebase.

## Steps

1. Run SwiftFormat: `swiftformat --config {{COMPONENT_PREFIX}}.swiftformat {{COMPONENT_PREFIX}}.`
2. Run SwiftLint auto-correct: `swiftlint --fix --config {{COMPONENT_PREFIX}}.swiftlint.yml {{COMPONENT_PREFIX}}.`
3. Run SwiftLint check: `swiftlint --strict --config {{COMPONENT_PREFIX}}.swiftlint.yml {{COMPONENT_PREFIX}}.`

## Expected Output

Report:
- Files modified by formatters
- Remaining lint warnings (if any)
- Final pass/fail status

## Important

- Do NOT modify any files manually
- If lint errors cannot be auto-fixed, report them for manual review
- NEVER add disable comments to suppress warnings
