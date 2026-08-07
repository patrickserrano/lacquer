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

# Provider-prefixed credentials. The assignment regex above only fires when the
# VARIABLE is named like a secret (api_key, password, access_token...). It says
# nothing about the value, so a real key bound to an ordinary name sails through:
#
#     anthropic_key_for_proxy=sk-ant-api03-...   <- not matched by the above
#
# That is not hypothetical. Seven live credentials were found in a dotfile in
# exactly that shape — sk-ant-api03-, sbp_, ghp_, perm- — every one bound to a
# name this scanner does not recognise.
#
# These patterns match the VALUE instead, so the variable name is irrelevant.
# Anchored on issuer prefixes with a length floor, which is what makes them
# specific enough not to fire on prose. Kept separate from the block above so a
# failure names which kind of thing was found.
PREFIXED=$(grep -InE \
  '(sk-ant-[a-z0-9]{4,}-[A-Za-z0-9_-]{16,}|sk-[A-Za-z0-9]{32,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|sbp_[a-f0-9]{16,}|AKIA[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----)' \
  -- "${FILES[@]}" 2>/dev/null | grep -viE 'placeholder|example|your_|xxxx|<.*>|REDACTED' || true)

if [ -n "$PREFIXED" ]; then
  echo "Provider credential found in staged files:"
  echo "$PREFIXED"
  echo ""
  echo "These are recognisable by issuer prefix (Anthropic, OpenAI, GitHub, Supabase,"
  echo "AWS, Slack, or a PEM private key). Move it to a gitignored config and ROTATE it —"
  echo "a key that reached the index should be treated as disclosed."
  exit 1
fi

if [ -n "$MATCHES" ]; then
  echo "Potential hardcoded secrets found in staged files:"
  echo "$MATCHES"
  echo ""
  echo "Move secrets to a gitignored config (e.g. Secrets.xcconfig) or use a placeholder."
  exit 1
fi
