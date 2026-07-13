---
name: release-app-store-changelog
description: Create user-facing App Store release notes from git history. Use when asked to generate release changelog, App Store "What's New" text, or release notes based on git tags.
---

# App Store Changelog

## Overview

Generate comprehensive, user-facing changelog from git history since the last tag, then translate commits into clear App Store release notes.

## Workflow

### 1) Collect Changes

Get commits since the last tag:

```bash
# Find the last tag
git describe --tags --abbrev=0

# List commits since last tag
git log $(git describe --tags --abbrev=0)..HEAD --oneline

# Or with more detail
git log $(git describe --tags --abbrev=0)..HEAD --pretty=format:"%h %s" --no-merges
```

If comparing specific refs:
```bash
git log v1.2.3..HEAD --oneline --no-merges
```

If no tags exist:
```bash
git log --oneline --no-merges -50  # Last 50 commits
```

### 2) Triage for User Impact

Scan commits and identify user-visible changes:

**Include:**
- New features
- UI changes
- Behavior changes
- Bug fixes users would notice
- Performance improvements with visible impact

**Exclude:**
- Refactors
- Dependency bumps
- CI changes
- Developer tooling
- Internal logging
- Analytics changes (unless affecting user privacy/behavior)

Group changes by theme:
- **New**: New features and capabilities
- **Improved**: Enhancements to existing features
- **Fixed**: Bug fixes

### 3) Draft App Store Notes

Write short, benefit-focused bullets:

- Use clear verbs and plain language
- Avoid internal jargon, ticket IDs, file paths
- Prefer 5-10 bullets unless user specifies different length
- Each bullet: one sentence, starts with a verb

**Language guidance:**
- Translate technical terms to user-facing descriptions
- Avoid: "API", "refactor", "nil", "crash log", "dependency"
- Use: "Improved", "Added", "Fixed", "Updated"
- Keep tense consistent (present or past)

**Examples:**
- "Added account switching from the profile menu."
- "Improved timeline loading speed on slow connections."
- "Fixed media attachments not opening in full screen."

### 4) Validate

- [ ] Every bullet maps to a real change in the range
- [ ] No duplicate bullets describing the same change
- [ ] No internal jargon or file paths
- [ ] Final list fits App Store text limits (if provided)

## Output Format

```
What's New

• Added [feature description]
• Improved [enhancement description]
• Fixed [bug fix description]
```

Or with sections:

```
What's New in [Version]

New
• [Feature 1]
• [Feature 2]

Improved
• [Enhancement 1]

Fixed
• [Bug fix 1]
• [Bug fix 2]
```

## Quick Commands

```bash
# Full workflow: commits since last tag with files changed
git log $(git describe --tags --abbrev=0)..HEAD --stat --no-merges

# Just commit messages
git log $(git describe --tags --abbrev=0)..HEAD --pretty=format:"- %s" --no-merges

# List all tags
git tag -l --sort=-v:refname

# Compare two specific tags
git log v1.1.0..v1.2.0 --oneline --no-merges
```

## Tips

- If a change is ambiguous, ask for clarification
- Drop changes that are clearly internal-only
- When in doubt, describe as "small improvement" only if user-visible
- Respect storefront character limits if provided

Source: ported from ios-template (now archived), a predecessor repo of this fleet.
