#!/usr/bin/env bash
# Pre-commit hook: scan staged changes for hardcoded secrets.
set -euo pipefail

# Hard rule: Secrets.xcconfig holds real service keys (RevenueCat, Aptabase, …)
# and must stay gitignored — never committed. The pattern scan below can't catch
# it (xcconfig values are unquoted), so refuse it by name. The committed
# Secrets.xcconfig.example template is unaffected.
STAGED_SECRETS=$(git diff --cached --name-only --diff-filter=ACM |
  grep -E '(^|/)Secrets\.xcconfig$' || true)
if [ -n "$STAGED_SECRETS" ]; then
  echo "Refusing to commit Secrets.xcconfig — it holds real service keys and must stay gitignored:"
  echo "$STAGED_SECRETS"
  echo ""
  echo "Add 'Secrets.xcconfig' to .gitignore; commit Secrets.xcconfig.example (the template) instead."
  exit 1
fi

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
