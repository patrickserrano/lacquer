<!-- Generated: 2026-09-24 11:27:20 UTC -->
# Codex instructions (#415)

`internal/sync/sync.go` and `internal/audit/audit.go` read separate AGENTS sources
when `WantsAgentsMd` is true. Existing destination/region keys remain stable, so
three-way drift classification and project prose preservation still apply.
Missing sources fail before writes rather than falling back to the old mirror.

The shipped instruction budget is 10,000 bytes for core plus one profile,
including region markers. User-authored prose is outside that budget. Multiple
profiles in one directory add their bodies together.

## Root cause: five whys

1. Why was the Codex brief tool-inappropriate? It copied the other tool's bodies.
2. Why were those copied? The renderer changed only destination filenames.
3. Why did audit accept that? It independently reproduced the same mirror.
4. Why did tests not reject it? They required equality, rather than a tool-specific contract.
5. Why did guard coverage differ? Runtime configuration had no Codex-specific asset route.

Separate sources, contract tests and a tool-gated runtime asset route address
these causes. The task-level A/B experiment remains separate; byte counts and
guard probes do not establish improvement in model performance.

## Key files and workflows

| Surface | Source / verification |
|---|---|
| Universal rules | `core/AGENTS.core.md` |
| Exact stack commands | `profiles/{ios,web,supabase,marketing}/AGENTS.<profile>.md` |
| Guard assets and activation instructions | `core/codex/` |
| Tool gating, exclusion and lock participation | `internal/assets/assets.go` |
| Rendered budgets, forbidden terms, required commands, orphan checks | `internal/shipped/agents_test.go` |
| Executed hook decisions and safe controls | `internal/shipped/codex_guards_test.go` |
| Old lock migration, local edits and missing sources | `internal/sync/sync_test.go` |

Run `go test ./internal/shipped -run 'TestRenderedAgentsContract|TestCodexGuards' -v`
and `go test ./internal/sync`, then `go test ./...`. Tests render into scratch
projects, never into fleet repositories. Keep TMPDIR and GOCACHE inside the
worktree when verifying an isolated task. Put scratch repositories under a
`testdata/` directory (excluded by Go discovery and repository-wide source scans)
and set `GIT_CEILING_DIRECTORIES` to the scratch root so non-Git fixtures cannot
discover the enclosing worktree.

## Runtime contract and limits

Verified against `codex --version` (0.156.1), `codex --help`,
`codex features list` (hooks stable/enabled) and the
[official hook documentation](https://learn.chatgpt.com/docs/hooks).
The guard consumes `tool_input.command` for Bash and apply_patch and emits
`hookSpecificOutput.permissionDecision = "deny"` for `PreToolUse`.
Project trust plus individual hook review is required. See the shipped
`core/codex/README.md` for activation and uncovered surfaces; no trust setting is
written by lacquer. The tests execute the shipped command with documented event
payloads; they do not claim an end-to-end model session exercised the runtime.
