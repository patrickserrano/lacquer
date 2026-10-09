#!/usr/bin/env bash
# check-bundle-secrets.sh — fail when any built bundle other than the app carries
# a key from the secrets template in its Info.plist.
#
# Usage: scripts/check-bundle-secrets.sh <secrets-template> <App.app> <dir>
#   <secrets-template>  the committed xcconfig template, e.g. Secrets.xcconfig.example
#   <App.app>           the app bundle's file name: the one bundle allowed the keys
#   <dir>               a build products directory, or an archive's
#                       Products/Applications
#
# Why it exists. An app's service keys reach its Info.plist through a base
# xcconfig that sets INFOPLIST_FILE. When that config is applied at project level,
# every target inherits the line unless it overrides it, so a watch app, an
# extension or a test bundle ships a second copy of every key. Nothing fails: it
# builds, signs, uploads and passes review. This is what notices.
#
# The key list is DERIVED from the template: every setting it defines except the
# INFOPLIST_* build wiring. A key added to the template is checked without
# touching this script or the workflow.
#
# Every .app, .appex and .xctest under <dir>, at any depth, is a bundle. The app
# is the one named <App.app> that is not inside another bundle; a bundle of the
# same name embedded in something else is checked like any other.
#
# Positive controls, so a pass cannot mean "found nothing to check":
#   - the key list is non-empty;
#   - every bundle's Info.plist is readable, proved by reading its
#     CFBundleIdentifier (an unreadable plist would otherwise read as clean);
#   - the app is found AND carries at least one key (it does by design; if it
#     does not, the key list or the lookup is broken);
#   - at least one other bundle was checked, OR the app verifiably embeds
#     nothing: its PlugIns, Extensions, Watch and AppClips folders are absent or
#     empty. A plain app has nothing that could carry a copy; an app whose
#     folders hold something the scan did not check is a broken scan.
#
# Only key NAMES are printed, never a value.
#
# Exit: 0 clean · 1 a non-app bundle carries a key · 2 a control failed or usage.
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <secrets-template> <App.app> <dir>" >&2
  exit 2
fi
template="$1"
app_name="$2"
products="${3%/}"

if [[ ! -f "$template" ]]; then
  echo "::error::check-bundle-secrets: no secrets template at $template; nothing says which keys to look for" >&2
  exit 2
fi
if [[ "$app_name" != *.app || "$app_name" == */* ]]; then
  echo "::error::check-bundle-secrets: '$app_name' is not an app bundle's file name (expected Name.app)" >&2
  exit 2
fi
if [[ ! -d "$products" ]]; then
  echo "::error::check-bundle-secrets: no directory at $products; nothing was built there to check" >&2
  exit 2
fi

# `KEY = value` and `KEY[sdk=iphoneos*] = value`. INFOPLIST_* lines are how the
# keys are wired into the plist, not keys the plist carries. Key names are
# letters, digits and underscores, so plutil cannot read one as a key path.
keys=()
while IFS= read -r key; do
  keys+=("$key")
done < <(sed -nE 's/^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)([[:space:]]*\[[^]]*\])*[[:space:]]*=.*/\1/p' "$template" |
  grep -v '^INFOPLIST_' | sort -u || true)
if [[ ${#keys[@]} -eq 0 ]]; then
  echo "::error::check-bundle-secrets: no keys parsed from $template" >&2
  exit 2
fi
echo "secret keys (from $template): ${keys[*]}"

# Prints the keys present in a plist, space-separated. The value goes to
# /dev/null: a match is the exit status, never the output.
keys_in() {
  local plist="$1" key found=""
  for key in "${keys[@]}"; do
    if plutil -extract "$key" raw -o - "$plist" >/dev/null 2>&1; then
      found="$found $key"
    fi
  done
  echo "${found# }"
}

# Is this bundle inside another bundle? Judged on the path below <dir>, so an
# archive's own Products/Applications does not count.
nested() {
  local parent=""
  [[ "$1" == */* ]] && parent="${1%/*}"
  case "/$parent/" in
    *.app/* | *.appex/* | *.xctest/*) return 0 ;;
  esac
  return 1
}

list="$(mktemp)"
trap 'rm -f "$list"' EXIT
find "$products" \( -name '*.app' -o -name '*.appex' -o -name '*.xctest' \) -type d -print0 >"$list"

checked=0
leaks=0
apps=()
while IFS= read -r -d '' bundle; do
  rel="${bundle#"$products"/}"
  plist="$bundle/Info.plist"
  [[ -f "$plist" ]] || plist="$bundle/Contents/Info.plist"
  if ! plutil -extract CFBundleIdentifier raw -o - "$plist" >/dev/null 2>&1; then
    echo "::error::check-bundle-secrets: cannot read a CFBundleIdentifier from $rel's Info.plist; an unreadable bundle cannot be shown clean" >&2
    exit 2
  fi
  found="$(keys_in "$plist")"
  if [[ "$(basename "$bundle")" == "$app_name" ]] && ! nested "$rel"; then
    if [[ -z "$found" ]]; then
      echo "::error::check-bundle-secrets: $rel carries none of the keys; the key list or the lookup is broken" >&2
      exit 2
    fi
    apps+=("$bundle")
    echo "app (allowed): $rel"
    continue
  fi
  checked=$((checked + 1))
  if [[ -n "$found" ]]; then
    echo "::error::$rel carries secrets keys in its Info.plist: $found (only $app_name may)"
    leaks=$((leaks + 1))
  else
    echo "clean: $rel"
  fi
done <"$list"

if [[ ${#apps[@]} -eq 0 ]]; then
  echo "::error::check-bundle-secrets: no $app_name under $products; nothing proves the lookup works" >&2
  exit 2
fi
if [[ $leaks -gt 0 ]]; then
  echo "check-bundle-secrets: FAIL, $leaks of $checked non-app bundles carry secrets keys (only $app_name may)"
  exit 1
fi
if [[ $checked -eq 0 ]]; then
  # Listed with a glob, not with the find above, so a scan that stopped seeing
  # bundles cannot also hide them here.
  for app in "${apps[@]}"; do
    for dir in PlugIns Extensions Watch AppClips; do
      for entry in "$app/$dir"/*; do
        if [[ -e "$entry" ]]; then
          echo "::error::check-bundle-secrets: ${app#"$products"/}/$dir holds ${entry##*/}, but no bundle besides the app was checked; the scan is broken" >&2
          exit 2
        fi
      done
    done
  done
  echo "check-bundle-secrets: $app_name embeds no other bundle and none was built beside it; nothing else can carry the keys"
  exit 0
fi
echo "check-bundle-secrets: checked $checked non-app bundles, none carry secrets keys (${#apps[@]} $app_name allowed)"
