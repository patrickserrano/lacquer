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
#
# `--diff-filter=ACM` includes a staged git submodule (mode 160000, a gitlink):
# the path appears as an ordinary "A"/"M" entry with no marker distinguishing
# it from a regular file. On disk that path is a directory, so `grep -oIHnE`
# on it exits 2 ("Is a directory") — the scanner reporting it "could not run"
# on the first commit that adds any submodule. `[ -f "$f" ]` is the correct
# filter, not `[ -d "$f" ]` negated: it also skips a path that was staged and
# then deleted before commit, which likewise no longer exists as a regular file.
FILES=()
while IFS= read -r -d '' f; do
  [ -f "$f" ] && FILES+=("$f")
done < <(git diff --cached --name-only --diff-filter=ACM -z |
  grep -zvE '(^scripts/check-secrets\.sh$|\.template$|\.example$|\.md$)' || true)
[ ${#FILES[@]} -eq 0 ] && exit 0

# Placeholder terms, applied to the MATCHED VALUE and never to the whole grep
# output line. Two other things share that line: the rest of the source line,
# and grep's own "path:lineno:" prefix. Testing against those is how the
# previous version failed open twice over — a real sk-ant-… key was discarded
# because it happened to sit inside a <meta> tag (the old filter carried a bare
# `<.*>`, which any JSX/SVG/XML/HTML-comment line satisfies), and again because
# the file lived under an examples/ directory, since `example` matched the path
# prefix rather than the credential.
#
# `<` is a placeholder tell only because it CANNOT occur in a real match: the
# provider patterns below are [A-Za-z0-9_-] throughout, and the PEM header has
# no angle bracket. So `password="<redacted>"` is dropped while a live key
# inside markup is still reported.
PLACEHOLDER='your_|your-|xxxx|placeholder|redacted|example|<'

# grep exits 0 when it matched, 1 when it did not, and >=2 when it could not
# run at all. Only the first two are answers. The third — an unreadable file, a
# staged changeset long enough to blow the argument list (E2BIG), a pattern some
# platform's grep rejects — is the scanner failing, and it must be loud.
#
# `2>/dev/null … || true` collapsed all three into "clean": stderr discarded,
# status cleared, empty output read as "no secrets found". A commit could then
# pass a gate that never executed, with nothing printed to say so. The project
# rule this violates is stated in CLAUDE.md — a hook that cannot fail and cannot
# complain is not a hook.
#
# Sets SCAN_OUT rather than echoing, because `exit 1` inside a command
# substitution exits only the subshell — the failure would be swallowed exactly
# like the one being fixed.
SCAN_OUT=""
scan() {
  local pattern=$1 rc err
  err=$(mktemp)
  set +e
  # -H forces the filename even when exactly one file is staged; without it a
  # single-file commit reports a bare "12:sk-ant-…" and the reader is left to
  # guess which file to go clean up.
  SCAN_OUT=$(grep -oIHnE "$pattern" -- "${FILES[@]}" 2>"$err")
  rc=$?
  set -e
  if [ "$rc" -ge 2 ]; then
    echo "check-secrets: the scan could not run (grep exit $rc) — refusing to report a pass." >&2
    cat "$err" >&2
    rm -f "$err"
    exit 1
  fi
  rm -f "$err"
  # Strip grep's "path:lineno:" prefix to get the match on its own, test the
  # placeholder terms against that, and print the original line so the report
  # still says where it came from. The strip is greedy so it survives colons in
  # a filename; no pattern below can match a colon, so it cannot over-strip.
  SCAN_OUT=$(printf '%s\n' "$SCAN_OUT" | awk -v bad="$PLACEHOLDER" '
    NF {
      tok = $0
      sub(/^.*:[0-9]+:/, "", tok)
      if (tolower(tok) !~ bad) print
    }')
}

# The assignment scan: fires when the VARIABLE is named like a secret.
scan '(api_key|apikey|api_secret|client_secret|password|access_token|auth_token)[[:space:]]*[:=][[:space:]]*["'"'"'][^"'"'"']{8,}'
MATCHES=$SCAN_OUT

# Provider-prefixed credentials. The assignment regex above says nothing about
# the value, so a real key bound to an ordinary name sails through:
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
scan '(sk-ant-[a-z0-9]{4,}-[A-Za-z0-9_-]{16,}|sk-[A-Za-z0-9]{32,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|sbp_[a-f0-9]{16,}|AKIA[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----)'
PREFIXED=$SCAN_OUT

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
