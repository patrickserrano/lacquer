#!/usr/bin/env bash
# Forwards a rescue task to Google's Antigravity CLI (agy) -- a genuinely
# different agent harness, not just another model within this session.
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: antigravity-companion.sh task "<task text>" [options]

options:
  --model <name>   pass through to `agy --model` (run `agy models` to list; spans
                    Gemini, Claude, and GPT-OSS variants -- this is a multi-vendor
                    router, not a Gemini-only tool)
  --resume         continue the most recent Antigravity conversation (agy -c)
  --fresh          start a new conversation (default)
  --write          allow edits (--dangerously-skip-permissions) (default)
  --read-only      ask for diagnosis/review only -- prompt-compliance only, not
                    CLI-enforced: agy has no permission gating in headless (-p)
                    mode at all, with or without --dangerously-skip-permissions
                    (verified live -- see SKILL.md's Security section)
  --timeout <dur>  override the default 10m print-timeout (agy's own default is 5m)
EOF
  exit 2
}

[ "${1:-}" = "task" ] || usage
shift

task="${1:-}"
[ -n "$task" ] || usage
shift

model=""
resume_flag=""
skip_permissions="--dangerously-skip-permissions"
print_timeout="10m"

while [ $# -gt 0 ]; do
  case "$1" in
    --model) shift; model="${1:-}" ;;
    --resume) resume_flag="-c" ;;
    --fresh) resume_flag="" ;;
    --write) skip_permissions="--dangerously-skip-permissions" ;;
    --read-only) skip_permissions="" ;;
    --timeout) shift; print_timeout="${1:-}" ;;
    *) echo "antigravity-companion.sh: unknown flag: $1" >&2; usage ;;
  esac
  shift
done

case "$model" in
  -*) echo "antigravity-companion.sh: --model value must not start with '-': $model" >&2; exit 2 ;;
esac

if [ -z "$skip_permissions" ]; then
  task="Diagnosis/review only -- do not create, edit, or delete any files. $task"
fi

# --add-dir is not optional: without it, agy operates in its own internal
# scratch sandbox (~/.gemini/antigravity-cli/scratch), disconnected from the
# real project -- confirmed live, this is not a hypothetical edge case.
# It is a workspace default hint, not an access boundary: agy can still
# read/write/delete outside it in headless mode (verified live).
args=(--add-dir "$(pwd)" --print-timeout "$print_timeout")
[ -n "$model" ] && args+=(--model "$model")
[ -n "$resume_flag" ] && args+=("$resume_flag")
[ -n "$skip_permissions" ] && args+=("$skip_permissions")

# -p's value must be the argv token immediately following it -- confirmed
# live that inserting any other flag (even `--`) between -p and the task
# text breaks parsing, unlike claude/codex CLIs. Keep -p and $task last.
args+=(-p "$task")
exec agy "${args[@]}"
