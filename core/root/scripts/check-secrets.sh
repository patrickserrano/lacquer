#!/usr/bin/env bash
# Pre-commit hook: scan staged changes for hardcoded secrets.
set -euo pipefail

FILES=$(git diff --cached --name-only --diff-filter=ACM | \
  grep -vE '(^scripts/check-secrets\.sh$|\.template$|\.example$|\.md$)' || true)
[ -z "$FILES" ] && exit 0

MATCHES=$(echo "$FILES" | xargs grep -nE \
  '(api_key|apikey|api_secret|client_secret|password|access_token|auth_token)\s*[:=]\s*["'"'"'][^"'"'"']{8,}' \
  2>/dev/null | grep -viE 'placeholder|example|your_|xxxx|<.*>' || true)

if [ -n "$MATCHES" ]; then
  echo "Potential hardcoded secrets found in staged files:"
  echo "$MATCHES"
  echo ""
  echo "Move secrets to a gitignored config (e.g. Secrets.xcconfig) or use a placeholder."
  exit 1
fi
