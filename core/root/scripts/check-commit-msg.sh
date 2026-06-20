#!/usr/bin/env bash
# commit-msg hook: enforce Conventional Commits — `type(optional scope)!: summary`.
# Run by pre-commit's commit-msg stage with the message file as $1.
set -euo pipefail

MSG_FILE="${1:?usage: check-commit-msg.sh <commit-msg-file>}"

# Subject = first non-blank, non-comment line.
SUBJECT=$(grep -vE '^[[:space:]]*#' "$MSG_FILE" | grep -vE '^[[:space:]]*$' | head -1 || true)

# Let git's own machine-generated subjects through untouched.
case "$SUBJECT" in
  Merge\ *|Revert\ *|fixup!\ *|squash!\ *|amend!\ *) exit 0 ;;
esac

if ! printf '%s' "$SUBJECT" | grep -qE '^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-zA-Z0-9 ._/-]+\))?!?: .+'; then
  echo "Commit message must follow Conventional Commits:"
  echo "  <type>(<optional scope>): <summary>"
  echo "  types: feat fix docs style refactor perf test build ci chore revert"
  echo ""
  echo "Got: $SUBJECT"
  exit 1
fi
