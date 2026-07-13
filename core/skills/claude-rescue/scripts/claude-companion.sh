#!/usr/bin/env bash
# Forwards a rescue task to the Claude Code CLI. Mirrors the codex-rescue
# plugin's codex-companion.mjs, direction reversed: Codex -> Claude.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: claude-companion.sh task "<task text>" [options]

options:
  --background   start as a background agent, return immediately
  --wait         run in the foreground and print the result (default)
  --model <name> pass through to `claude --model` (e.g. opus, sonnet, fable)
  --resume       continue the most recent Claude Code session in this repo
  --fresh        start a new session (default)
  --write        allow edits (--permission-mode acceptEdits) (default)
  --read-only    diagnosis/review only, no edits (--permission-mode plan)
EOF
  exit 2
}

[ "${1:-}" = "task" ] || usage
shift

task="${1:-}"
[ -n "$task" ] || usage
shift

mode="wait"
model=""
resume_flag=""
permission_mode="acceptEdits"

while [ $# -gt 0 ]; do
  case "$1" in
    --background) mode="background" ;;
    --wait) mode="wait" ;;
    --model) shift; model="${1:-}" ;;
    --resume) resume_flag="-c" ;;
    --fresh) resume_flag="" ;;
    --write) permission_mode="acceptEdits" ;;
    --read-only) permission_mode="plan" ;;
    *) echo "claude-companion.sh: unknown flag: $1" >&2; usage ;;
  esac
  shift
done

case "$model" in
  -*) echo "claude-companion.sh: --model value must not start with '-': $model" >&2; exit 2 ;;
esac

# `claude` re-parses the value of -p/--background as an option if it starts
# with '-', so the task text is appended last, after a `--` that terminates
# option parsing -- this is the only thing standing between task text and
# argument injection (e.g. a task starting with "--dangerously-skip-permissions").
if [ "$mode" = "background" ]; then
  args=(--background --permission-mode "$permission_mode")
  [ -n "$model" ] && args+=(--model "$model")
  [ -n "$resume_flag" ] && args+=("$resume_flag")
  args+=(-- "$task")
  exec claude "${args[@]}"
fi

args=(-p --permission-mode "$permission_mode" --output-format text)
[ -n "$model" ] && args+=(--model "$model")
[ -n "$resume_flag" ] && args+=("$resume_flag")
args+=(-- "$task")
exec claude "${args[@]}"
