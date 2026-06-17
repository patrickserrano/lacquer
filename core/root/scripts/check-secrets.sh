#!/usr/bin/env bash
# Pre-commit hook: scan staged changes for hardcoded secrets.
set -euo pipefail

# Collect staged files NUL-delimited so names with spaces (asset catalogs, group
# folders) are handled correctly. The while-read loop is portable to bash 3.2.
FILES=()
while IFS= read -r -d '' f; do
  FILES+=("$f")
done < <(git diff --cached --name-only --diff-filter=ACM -z |
  grep -zvE '(^scripts/check-secrets\.sh$|\.template$|\.example$|\.md$)' || true)
[ ${#FILES[@]} -eq 0 ] && exit 0

# -I skips binary files (avoids scanning images/compiled assets); -- ends option
# parsing so odd filenames aren't treated as flags; [[:space:]] for BSD grep.
MATCHES=$(grep -InE \
  '(api_key|apikey|api_secret|client_secret|password|access_token|auth_token)[[:space:]]*[:=][[:space:]]*["'"'"'][^"'"'"']{8,}' \
  -- "${FILES[@]}" 2>/dev/null | grep -viE 'placeholder|example|your_|xxxx|<.*>' || true)

if [ -n "$MATCHES" ]; then
  echo "Potential hardcoded secrets found in staged files:"
  echo "$MATCHES"
  echo ""
  echo "Move secrets to a gitignored config (e.g. Secrets.xcconfig) or use a placeholder."
  exit 1
fi
