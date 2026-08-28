# Output Contract

Use this file when you need to branch on `helm-asc` machine output instead of terminal prose.

## Default output mode

- `helm-asc` defaults to human-readable text.
- Pass `--agent` for JSON in all agent flows.
- Omit `--agent` only when the user explicitly wants human-readable output.
- Keep commands as direct `helm-asc ... --agent` invocations. Avoid `jq`, shell pipelines, and command substitution in agent flows so the host can approve and replay a single CLI command cleanly.
- For large payloads, redirect the JSON to a file and inspect that file with the host's file-read tool instead of slicing it in the shell.

## Error envelope

Structured command failures use a stable JSON envelope:

```json
{
  "error": {
    "code": "VALIDATION",
    "message": "Human-readable summary",
    "field": "--locale",
    "suggestions": ["en-US"],
    "errors": [],
    "context": {}
  }
}
```

Known `CLIError` codes include:

- `UNKNOWN_LOCALE`
- `UNKNOWN_FIELD`
- `INVALID_URL`
- `FIELD_TOO_LONG`
- `NO_OP`
- `MISSING_REQUIRED`
- `ALREADY_EXISTS`
- `NOT_FOUND`
- `AUTH`
- `NETWORK`
- `PRO_REQUIRED`
- `DRY_RUN_BLOCKED`
- `VALIDATION`
- `FILE_ACCESS`

## Exit codes

- `0`: success
- `1`: partial failure
- `2`: validation
- `3`: auth
- `4`: network
- `5`: not found
- `6`: conflict
- `7`: pro required

## Status values inside successful JSON payloads

Many write and bulk-transfer commands also include a `status` field in the JSON body:

- `ok`
- `partial_failure`
- `noop`

Interpret them as follows:

- `ok`: the command completed as requested.
- `partial_failure`: some slices succeeded and some failed. This often appears in bulk localization, screenshot, preview, build-attach, or build-upload follow-on work.
- `noop`: the command intentionally changed nothing, usually because the action was cancelled or the requested patch was empty.

## Download artifact paths

Download commands for localization bundles, screenshots, and previews write to a sandbox-safe CLI artifacts directory when `--path` is omitted. Their successful JSON payloads include `rootPath`; use that path for subsequent file reads or upload commands.

Only pass `--path` when the user has provided an explicit destination or when you are intentionally reusing an existing editable directory.

## Upload staging paths

Run `helm-asc paths --agent` to discover Helm-visible directories. Use `uploadsInbox` for files or directories the agent creates before passing them to upload commands.

If a command returns `FILE_ACCESS`, changing directories does not grant access to the requested file. Use the error suggestions: stage the file under `uploadsInbox`, omit `--path` for downloads, or ask the user to grant access in Helm for the exact location.

## Partial failure behavior

When a command exits with code `1` or reports `status: "partial_failure"`:

- Inspect the per-locale, per-group, or per-item rows in the JSON payload.
- Keep successful IDs and do not rerun the whole workflow blindly.
- Retry only the failed locales, media groups, or follow-on steps.
- If the failures are validation-related, fix inputs before retrying.

## Retry, branch, or stop

- Retry on `NETWORK` when the request is idempotent or when the payload shows which slices failed.
- Branch to discovery commands on `NOT_FOUND` instead of guessing IDs.
- Branch to list or show commands on `ALREADY_EXISTS` or conflict-style outcomes.
- Stop for user input when a destructive command would require `--yes`, when multiple candidate apps or versions match, or when account intent is ambiguous.
- Stop and point the user to Helm Preferences when `helm-asc` cannot be resolved by the setup steps in `SKILL.md`.
- Do not treat `AUTH` as transient. Re-check `helm-asc auth list` or switch accounts explicitly.
- Do not treat `PRO_REQUIRED` as an auth, account-selection, or transient failure. Stop and tell the user Helm Pro is required for CLI commands; ask them to upgrade or restore purchases in Helm, then retry only after Helm reports active Pro status.
- Treat validation suggestions as candidate inputs, not instructions to mutate. If the target is ambiguous after applying suggestions, ask the user or run a narrower discovery command.
