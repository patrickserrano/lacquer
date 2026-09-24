<!-- Generated: 2026-09-24 11:27:20 UTC -->
# Repository guards

`.codex/hooks.json` registers `.codex/hooks/lacquer-guard.py` for Codex
`PreToolUse`. Requires Python 3, Git and Codex hook support (checked with installed
CLI 0.156.1). The command resolves from the Git root, including in subdirectories.

Trust the project, then use Codex `/hooks` to review and trust the installed
hook definition. Changed definitions need review again; syncing files does not
activate untrusted hooks. Never bypass trust review to make this configuration
appear active. Check the displayed source and enabled status before relying on it.

The guard denies visible git/gh force and bypass flags in Bash commands and
protected project, workspace, interface and entitlement paths in `apply_patch`,
including rename destinations. It returns a structured denial, not an approval
request. For an authorized entitlement change, the operator must arrange the edit;
do not disable the guard yourself.

These are guardrails, not a sandbox. Shell-script contents, aliases, commands
constructed at runtime, file writes through Bash or MCP, later `write_stdin`
input and specialized tool paths are not fully inspected. Missing Python, disabled
or untrusted hooks and runtime errors can prevent enforcement. The lexical shell
check is conservative and can reject quoted examples mentioning forbidden flags.
All AGENTS.md rules still apply on those surfaces; never route around a denial.

Reference: [OpenAI hook contract](https://learn.chatgpt.com/docs/hooks).
