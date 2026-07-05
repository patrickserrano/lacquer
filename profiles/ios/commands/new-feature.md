---
description: Scaffold a new feature module
argument-hint: <FeatureName>
allowed-tools: Write, Read, Glob
---

Create a new feature module following the project's architecture.

## Arguments

- `$ARGUMENTS` - Feature name in PascalCase (e.g., "Profile", "Settings")

## Locate the Features Directory

Do NOT assume the app's source directory is named after the project — layouts
vary (e.g. `App/`, `Sources/`). Find the existing `Features/` directory under
`{{COMPONENT_PREFIX}}` (Glob for `{{COMPONENT_PREFIX}}**/Features`, ignoring
build output like `DerivedData*` and `.build/`) and create the module there,
as a sibling of the existing feature folders. If no `Features/` directory
exists yet, create one alongside the app's other source folders under
`{{COMPONENT_PREFIX}}` (the directory that holds the app's Swift sources, next
to the `.xcodeproj`).

## Structure to Create

```
<features-dir>/$ARGUMENTS/
├── ${ARGUMENTS}View.swift           # Main SwiftUI view
├── ${ARGUMENTS}ViewModel.swift      # @Observable, @MainActor ViewModel
├── Components/                       # Feature-specific UI components
└── Models/                           # Feature-specific models (if needed)
```

## Template Requirements

### ViewModel
- Must use `@Observable` macro
- Must be `@MainActor` isolated
- Must use constructor injection for dependencies
- Use `any ProtocolName` for dependency types

### View
- Use `@State` for ViewModel
- Use extracted components for complex UI
- Keep under 100 lines

## Steps

1. Confirm feature name with user
2. Locate the `Features/` directory (see above), then create the directory structure
3. Generate ViewModel with placeholder dependencies
4. Generate main View with basic structure
5. Report created files

## Do NOT

- Create tests (user will request separately)
- Add to navigation (user will integrate)
- Create services/repositories (those go in Core/)
