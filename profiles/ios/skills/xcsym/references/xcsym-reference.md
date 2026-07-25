# xcsym Reference

Full subcommand, output-schema, exit-code, and pattern-tag reference for `xcsym`. See the parent `SKILL.md` for the everyday `crash` workflow — reach for this file for anything beyond that.

## Subcommands

All report subcommands (`crash`, `verify`, `resolve`, `find-dsym`, `list-dsyms`) accept `--human` for prose output; default is compact single-line JSON. `anonymize` reproduces the `.ips` wire format instead of a report, so it has no `--human`. Flags and positional arguments can be given in either order (`xcsym crash --format=standard <file>` and `xcsym crash <file> --format=standard` are equivalent).

- **`crash <file>`** — full pipeline: parse → discover dSYMs → symbolicate → categorize → emit JSON.
  - `--format=summary|standard|full` — output size tier (see below)
  - `--from-metrickit` — force MetricKit JSON parsing
  - `--dsym <path>` / `--dsym-paths <paths>` — explicit dSYM location(s), skips discovery
  - `--no-symbolicate` / `--no-cache` / `--no-spotlight` — disable pipeline stages
  - `--output <path>` — write JSON to a file instead of stdout
  - `--filter` / `--prefer-locally-symbolicated` — `.xccrashpoint` bundle handling (default: most recent `Filter_*`, raw `.crash` not `LocallySymbolicated/`)
  - Accepts stdin
- **`verify <file>`** — per-image UUID/arch match diagnostics only; exit-code semantics differ from `crash` (see code 7 below).
- **`resolve --dsym <path> --address <addr>`** — single-address resolution via `atos` against an explicit dSYM.
- **`find-dsym --uuid <uuid>`** — UUID-driven dSYM lookup across the full discovery chain.
- **`list-dsyms [--source archives|derived-data|downloads|toolchain|frameworks|env|all]`** — inventory of discoverable dSYMs by source.
- **`anonymize <file>`** — scrubs bundle IDs, process names, user paths, IPs, device names, account IDs, and foreign UUIDs found in freeform strings. Preserves dSYM UUIDs, thread names, library identifiers, and structural fields, so the scrubbed file still symbolicates — safe to commit as a test fixture.

## Output schema (`crash`)

Top-level JSON: `tool`, `version`, `format`, `input`, `crash`, `images`, `images_summary`, `warnings`, `size_warning`, plus `pattern_tag`, `pattern_confidence`, `pattern_rule_id`, `pattern_reason`, `crashed_thread`. `images` breaks down into matched/mismatched/missing.

## Format tiers

| Tier | Target size | Warns past |
|------|-------------|------------|
| `summary` | ~2 KB | 4 KB |
| `standard` (default) | ~12 KB | 50 KB |
| `full` | — | 100 KB |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Clean |
| 1 | Usage error |
| 2 | Input or main dSYM missing |
| 3 | UUID mismatch |
| 4 | Arch mismatch |
| 5 | Tool error |
| 6 | Timeout |
| 7 | Partial match (`crash` and `verify` diverge on exact semantics here) |
| 8 | Output error |

## Pattern tag catalog

19 tags, each with a rule ID and confidence level in the JSON output: `swift_forced_unwrap`, `swift_concurrency_violation`, `swift_fatal_error`, `zombie_or_heap_corruption`, `stack_overflow`, `bad_memory_access`, `illegal_instruction`, `exc_guard`, `objc_exception`, `main_thread_checker_violation`, `abort`, `watchdog_termination`, `user_force_quit`, `background_task_expired`, `data_protection_violation`, `code_signing_killed`, `jetsam_oom`, `cpu_resource_fatal`, `swiftui_update_loop`, `unclassified`.

## dSYM discovery order

`crash`/`find-dsym` search in this order, stopping at the first match: explicit-by-UUID → explicit paths (`--dsym-paths`) → cache → Spotlight → Archives → DerivedData → Frameworks → Downloads → Toolchain → env paths (`XCSYM_DSYM_PATHS`). `XCSYM_FRAMEWORK_SCAN_TIMEOUT` controls how long the Frameworks-scan step waits.

## Hang and unsupported-input routing

`xcsym` intentionally rejects hang reports (`bug_type=298` `.ips` files) rather than mis-symbolicating them — it returns a JSON reject shape (`hang_report`) so an agent can route on the error field instead of scraping stderr. Same pattern for `empty_bundle` (an `.xccrashpoint` with no usable `.crash` inside) and `unsupported_format`. There is no hang-diagnostics equivalent in this fleet yet — treat a `hang_report` rejection as "not this tool's job" and fall back to manual `.ips` inspection.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Exit 2, `images.missing` non-empty | dSYM not discoverable | `xcsym list-dsyms` to see what's present; `xcsym find-dsym --uuid <uuid>` for a specific image; pass `--dsym`/`--dsym-paths` explicitly if it lives somewhere non-standard |
| Exit 3 | dSYM found but UUID doesn't match the binary in the crash | Wrong archive/build — find the matching Archive or re-export the dSYM for the exact build that crashed |
| Exit 4 | Arch mismatch (e.g. dSYM built for a different slice) | Rebuild or re-export a universal/matching-arch dSYM |
| Exit 7 on `crash` vs `verify` | Partial symbolication — some images matched, some didn't | Check `images.mismatched`/`images.missing`; treat as "usable but incomplete," not a full failure |
| `hang_report` JSON reject | Input is a hang (`bug_type=298`), not a crash | Not `xcsym`'s job by design — inspect manually |
| `unsupported_format` / `empty_bundle` | Input isn't a recognized crash format, or an `.xccrashpoint` had no `.crash` under the selected filter | Re-check the source file; for `.xccrashpoint`, try `--filter` to pick a different build |
