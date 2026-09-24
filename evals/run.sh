#!/bin/bash
# A single-arm grader pilot. The measured experiment is deliberately separate.
set -euo pipefail
cd "$(dirname "$0")/.."
if [ "$#" -gt 1 ]; then
  echo 'usage: bash evals/run.sh [case-glob]' >&2
  exit 2
fi
scratch="$PWD/testdata/.rule-eval-work"
mkdir -p "$scratch/tmp" "$scratch/go-cache" "$scratch/go-mod"
export TMPDIR="$scratch/tmp"
export GOCACHE="$scratch/go-cache"
export GOMODCACHE="$scratch/go-mod"
export GIT_CEILING_DIRECTORIES="$scratch"
export PYTHONDONTWRITEBYTECODE=1
export DISABLE_AUTOUPDATER=1
export CLAUDE_CONFIG_DIR="${CLAUDE_CONFIG_DIR:-$scratch/claude-config}"
# Fail on stale generated context before a model call costs anything.
go test ./evals ./internal/shipped -run TestRuleEval -count=1
# Reserve one of three pilot invocations before starting the CLI, including
# interrupted attempts. No paid init, retries, or measured arm are hidden here.
run_dir=''
for n in 1 2 3; do
  if mkdir "$scratch/pilot-$n" 2>/dev/null; then
    run_dir="$scratch/pilot-$n"
    break
  fi
done
if [ -z "$run_dir" ]; then
  echo 'Three pilot invocations already reserved; stop for PM review.' >&2
  exit 2
fi
set +e
"${CLAUDE_BIN:-claude}" plugin eval "$PWD/evals/rules" \
  --model claude-sonnet-5 --ablation none --runs 1 --max-cost-usd 1.5 \
  --no-publish --scaffold --trust-plugin --case "${1:-*}" \
  --output-dir "$run_dir" --json "$run_dir/result.json" \
  --allow-tools Bash Write Edit 2>&1 | tee "$run_dir/run.log"
code=${PIPESTATUS[0]}
set -e
printf '%s\n' "$code" > "$run_dir/exit-code.txt"
echo "Pilot exit $code; reported cost and any partial results are in $run_dir."
exit "$code"
