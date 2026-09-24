<!-- Generated: 2026-09-24 09:10:45 UTC -->
# CLAUDE.md diet (#453)

The always-loaded rules now contain safety invariants, verification requirements,
CI-round accounting, compaction state and a short skill index. Procedures and
incident evidence remain available in 12 on-demand skill references. No workflow
template or runtime renderer changed.

## Root cause (Five Whys)

1. Why was session context large? Every task loaded the rendered root/profile rules.
2. Why were those rules large? They included release recipes, configuration examples,
   troubleshooting and incident rationale as well as universal invariants.
3. Why did unrelated tasks load recipes? Both categories shared the same managed regions.
4. Why did existing checks permit this? The line ceilings pinned the existing large
   totals (1,534 rootapp and 2,234 multistack), rather than a small session budget.
5. Why would trimming alone recur? A ceiling prevents growth but does not establish
   a destination for new procedures. The fix separates always-needed rules from
   discoverable skills and tightens both rendered-file and total managed budgets.

This is an architectural explanation supported by the templates and tests, not a
claim about past authors' decisions. All 62 original sections (453 paragraphs/blocks) were compared against their
new references to verify paragraph preservation; the one changed intra-document
link now points to its destination skill.

## Measurements

Physical rendered lines include markers and blank lines. A nested component's
loaded count includes root CLAUDE.md. Mirrors are checked but not double-counted.
These are fixture measurements, not claims about project-owned prose in the fleet.

| Fixture / context | Before | After |
| --- | ---: | ---: |
| rootapp / iOS (one file) | 1535 | 106 |
| multistack / iOS (root + ios) | 1534 | 105 |
| multistack / web (root + admin) | 845 | 69 |
| multistack / Supabase (root + server) | 683 | 69 |
| spmpackage / core-only | 414 | 53 |

Managed-region ceilings in `internal/shipped/claude_ratchet_test.go` count regions
without inter-region spacing: rootapp 1534 → 105; multistack 2234 → 137;
duoapp 1534 → 105; spmpackage 414 → 53.

## Section destinations

Each row includes every subsection beneath the named original sections. References
are linked by their skill's `SKILL.md`; the compact rules name the matching skills.

| Moved source sections | Destination |
| --- | --- |
| `core/CLAUDE.core.md`: Profile introduction; Fundamental Rules; Response Style; Agent Delegation; Context Management; Papercuts Log; Critical Review Pattern | [`core/skills/engineering-workflow/references/project-rules.md`](../../core/skills/engineering-workflow/references/project-rules.md) |
| `core/CLAUDE.core.md`: Docs Taxonomy; Documentation | [`core/skills/project-documentation/references/project-rules.md`](../../core/skills/project-documentation/references/project-rules.md) |
| `core/CLAUDE.core.md`: Local Checks Match CI; Warnings as Errors | [`core/skills/working-with-lacquer/references/project-rules.md`](../../core/skills/working-with-lacquer/references/project-rules.md) |
| `core/CLAUDE.core.md`: CI Hygiene; CI round budget | [`core/skills/github-ci-fix/references/project-rules.md`](../../core/skills/github-ci-fix/references/project-rules.md) |
| `profiles/ios/CLAUDE.ios.md`: Profile introduction; Xcode-Specific Prohibitions; Architecture; SwiftData + CloudKit; Testing; Documentation (DocC); Premium / Subscription Gating (if monetized) | [`profiles/ios/skills/ios-project-development/references/project-rules.md`](../../profiles/ios/skills/ios-project-development/references/project-rules.md) |
| `profiles/ios/CLAUDE.ios.md`: App Store Requirements; Release archives go to the archive volume, not the repository; App Store Connect accepts a binary before it lists it | [`profiles/ios/skills/ios-release-guide/references/project-rules.md`](../../profiles/ios/skills/ios-release-guide/references/project-rules.md) |
| `profiles/ios/CLAUDE.ios.md`: Shipping more than one app from one repository; CI Runners; Editor hooks (.claude/settings.json); Local Checks vs CI | [`profiles/ios/skills/ios-ci-configuration/references/project-rules.md`](../../profiles/ios/skills/ios-ci-configuration/references/project-rules.md) |
| `profiles/ios/CLAUDE.ios.md`: Secrets & Service Keys | [`profiles/ios/skills/ios-secrets-setup/references/project-rules.md`](../../profiles/ios/skills/ios-secrets-setup/references/project-rules.md) |
| `profiles/ios/CLAUDE.ios.md`: Build data stays in the worktree; Build & Test Tooling (flowdeck); Test Timeout Rule | [`profiles/ios/skills/ios-build-verification/references/project-rules.md`](../../profiles/ios/skills/ios-build-verification/references/project-rules.md) |
| `profiles/ios/CLAUDE.ios.md`: Battery & Performance Patterns; Swift 6 Concurrency & Default Actor Isolation; iOS 26 API Gotchas; URL Validation Security Posture; Verifying UI in the Simulator; Accessibility & Design-Token Contrast (WCAG 1.4.11) | [`profiles/ios/skills/ios-ui-verification/references/project-rules.md`](../../profiles/ios/skills/ios-ui-verification/references/project-rules.md) |
| `profiles/web/CLAUDE.web.md`: Profile introduction; Read the vendored framework docs first; Required package.json scripts; TypeScript — extend the strict base; Code quality — Biome; Testing — Vitest; Documentation (TSDoc + TypeDoc); Environment & secrets; Security; Accessibility; Local Checks vs CI; Git hooks & commits; CI; Monorepos — Turborepo | [`profiles/web/skills/web-development-guide/references/project-rules.md`](../../profiles/web/skills/web-development-guide/references/project-rules.md) |
| `profiles/supabase/CLAUDE.supabase.md`: Profile introduction; Tooling — Deno, not npm; Migrations (`supabase/migrations/`); Row-Level Security (the security boundary); Edge Functions (`supabase/functions/`, Deno); Documentation; Secrets; Local Checks vs CI; Deploying migrations; Git hooks & commits; Testing & CI | [`profiles/supabase/skills/supabase-development-guide/references/project-rules.md`](../../profiles/supabase/skills/supabase-development-guide/references/project-rules.md) |

## Verification and maintenance

- `TestRenderedClaudeContextBudget` failed before the move on all five contexts
  (414–1535 lines versus 300), then passed. It also checks CLAUDE/AGENTS equality
  and the core safety/compaction contract.
- `TestMovedClaudeGuidanceIsDelivered` checks all moved sections in the rendered
  Claude, Codex and Antigravity skill trees, entrypoint links, name/description
  discovery, equal tool copies and absence of unresolved template tokens.
- `TestRenderedIOSRetainsSafetyRules` keeps project-file, DerivedData, secret and
  build-output protections in both root and nested iOS contexts.
- The papercuts test now checks the delivered on-demand reference, including the
  optional nature of the machine-local log. Prose-only site mirrors must contain
  the complete compact source; empty mirrors cannot silently pass.
- `go test ./...` passes. The documented CLI entrypoint is `./cmd/lacquer`.
  Running its `doctor` command against a freshly synced rootapp fixture passes
  20/20 core+iOS probes. The unverified-root development override is explicit.
- Mutation controls: replacing the force-push/rebase prohibition fails
  `TestRenderedClaudeContextBudget`; replacing the iOS build reference with an
  empty stub fails `TestMovedClaudeGuidanceIsDelivered/ios-build-verification`.
  Both files were restored before the final passing run.
- All 12 affected skills pass the skill-creator frontmatter validator.

Keep local verification output under ignored `bin/testdata/`: set `GOCACHE`,
`GOMODCACHE` and `TMPDIR` there, and set `GIT_CEILING_DIRECTORIES` to that TMPDIR.
This prevents nominally non-Git test directories inheriting the parent worktree;
`testdata` also keeps build churn out of the repository's source-scanning tests.

When adding guidance, put procedures in the matching skill and keep safety
invariants inline. Lower the managed ceilings whenever content shrinks; retain
negative controls and rendered mirror/skill checks when changing routing.
