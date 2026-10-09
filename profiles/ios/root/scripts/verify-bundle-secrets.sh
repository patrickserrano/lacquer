#!/usr/bin/env bash
# verify-bundle-secrets.sh — fail when any built bundle other than the app carries
# a secrets key in its Info.plist.
#
# Usage: scripts/verify-bundle-secrets.sh [--key NAME]... [--sources DIR] \
#          <secrets-template> <App.app> <dir>
#   --key NAME          a key the release writes beside the template's (the
#                       product's declared secrets); repeatable
#   --sources DIR       the component's source tree, searched for the Info.plist
#                       key names that carry each key (see below)
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
# The key list is DERIVED, so a key added to the template is checked without
# touching this script or the workflow:
#   - every setting the template defines, except the INFOPLIST_* build wiring;
#   - every --key;
#   - with --sources, every top-level Info.plist key in the source tree whose
#     string value references one of those as $(KEY) or ${KEY}. A plist key is
#     often not named like its setting (`RevenueCatAPIKey = $(REVENUECAT_API_KEY)`),
#     and a built plist carries only the resolved value under the plist's own
#     name, so the setting's name alone would find nothing. Apple's reserved
#     names (CFBundle…, NSCamera…, UIRequired…: a reserved prefix then a
#     capitalised word) are never taken this way: a template that happened to
#     define a build setting would otherwise mark a key every bundle carries.
#     SCREAMING_CASE names such as INSTABUG_TOKEN are not reserved.
#
# Every .app, .appex and .xctest under <dir>, at any depth, is a bundle. The app
# is the one named <App.app> that is not inside another bundle; a bundle of the
# same name embedded in something else is checked like any other.
#
# Positive controls, so a pass cannot mean "found nothing to check":
#   - the key list is non-empty;
#   - every bundle's Info.plist is a readable dictionary with a
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

usage() {
  echo "usage: $0 [--key NAME]... [--sources DIR] <secrets-template> <App.app> <dir>" >&2
  exit 2
}

extra=()
sources=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --key) [[ $# -ge 2 ]] || usage; extra+=("$2"); shift 2 ;;
    --sources) [[ $# -ge 2 ]] || usage; sources="$2"; shift 2 ;;
    --) shift; break ;;
    -*) usage ;;
    *) break ;;
  esac
done
[[ $# -eq 3 ]] || usage
template="$1"
app_name="$2"
products="${3%/}"

if ! command -v python3 >/dev/null 2>&1; then
  echo "::error::verify-bundle-secrets: python3 is needed to read property lists and is not on PATH" >&2
  exit 2
fi
if [[ ! -f "$template" ]]; then
  echo "::error::verify-bundle-secrets: no secrets template at $template; nothing says which keys to look for" >&2
  exit 2
fi
if [[ "$app_name" != *.app || "$app_name" == */* ]]; then
  echo "::error::verify-bundle-secrets: '$app_name' is not an app bundle's file name (expected Name.app)" >&2
  exit 2
fi
if [[ ! -d "$products" ]]; then
  echo "::error::verify-bundle-secrets: no directory at $products; nothing was built there to check" >&2
  exit 2
fi
if [[ -n "$sources" && ! -d "$sources" ]]; then
  echo "::error::verify-bundle-secrets: no source directory at $sources" >&2
  exit 2
fi
for k in ${extra[@]+"${extra[@]}"}; do
  if [[ ! "$k" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
    echo "::error::verify-bundle-secrets: --key '$k' is not a setting name" >&2
    exit 2
  fi
done

# `KEY = value` and `KEY[sdk=iphoneos*] = value`. INFOPLIST_* lines are how the
# keys are wired into the plist, not keys the plist carries.
settings=()
while IFS= read -r key; do
  settings+=("$key")
done < <({
  sed -nE 's/^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)([[:space:]]*\[[^]]*\])*[[:space:]]*=.*/\1/p' "$template"
  for k in ${extra[@]+"${extra[@]}"}; do echo "$k"; done
} | grep -v '^INFOPLIST_' | sort -u || true)
if [[ ${#settings[@]} -eq 0 ]]; then
  echo "::error::verify-bundle-secrets: no keys parsed from $template" >&2
  exit 2
fi

# Python reads every property list: plistlib takes XML and binary alike, a key
# is matched by NAME (plutil -extract reads a dotted name as a key path), and a
# value of any type counts.
py() { python3 -I -c "$1" "${@:2}"; }

# The source tree's Info.plist names for each setting: one name per line.
derived=()
if [[ -n "$sources" ]]; then
  while IFS= read -r name; do
    derived+=("$name")
  done < <(py '
import os, plistlib, re, sys
root, settings = sys.argv[1], set(sys.argv[2:])
reserved = re.compile(r"^(CF|NS|UI|LS|UT|MK|WK|GK|DT|AV|IN|ITS)[A-Z][a-z]|^(MinimumOSVersion|BuildMachineOSBuild)$")
ref = re.compile(r"\$[({]([A-Za-z_][A-Za-z0-9_]*)[)}]")
skip = {"DerivedData", "build", "Pods", "Carthage", "node_modules"}
out = set()
for d, dirs, files in os.walk(root):
    dirs[:] = [x for x in dirs if not x.startswith(".") and x not in skip
               and not x.endswith((".app", ".appex", ".xctest", ".xcarchive", ".dSYM"))]
    for f in files:
        if not f.endswith(".plist"):
            continue
        try:
            with open(os.path.join(d, f), "rb") as fh:
                p = plistlib.load(fh)
        except Exception:
            continue
        if not isinstance(p, dict):
            continue
        for k, v in p.items():
            if isinstance(v, str) and not reserved.search(k) and settings & set(ref.findall(v)):
                out.add(k)
print("\n".join(sorted(out)))
' "$sources" "${settings[@]}" | sed '/^$/d')
fi

keys=()
while IFS= read -r key; do
  keys+=("$key")
done < <(printf '%s\n' "${settings[@]}" ${derived[@]+"${derived[@]}"} | sort -u)
echo "secret keys (from $template${extra[@]+, --key}${sources:+, plists under $sources}): ${keys[*]}"

# Prints the keys a bundle's plist carries, space-separated, or fails when the
# plist is not a readable dictionary with a CFBundleIdentifier. Names only.
keys_in() {
  py '
import plistlib, sys
try:
    with open(sys.argv[1], "rb") as fh:
        p = plistlib.load(fh)
except Exception:
    sys.exit(3)
if not isinstance(p, dict) or not isinstance(p.get("CFBundleIdentifier"), str):
    sys.exit(3)
print(" ".join(k for k in sys.argv[2:] if k in p))
' "$1" "${keys[@]}"
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
  if ! found="$(keys_in "$plist")"; then
    echo "::error::verify-bundle-secrets: cannot read a CFBundleIdentifier from $rel's Info.plist; an unreadable bundle cannot be shown clean" >&2
    exit 2
  fi
  if [[ "$(basename "$bundle")" == "$app_name" ]] && ! nested "$rel"; then
    if [[ -z "$found" ]]; then
      echo "::error::verify-bundle-secrets: $rel carries none of the keys; the key list or the lookup is broken" >&2
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
  echo "::error::verify-bundle-secrets: no $app_name under $products; nothing proves the lookup works" >&2
  exit 2
fi
if [[ $leaks -gt 0 ]]; then
  echo "verify-bundle-secrets: FAIL, $leaks of $checked non-app bundles carry secrets keys (only $app_name may)"
  exit 1
fi
if [[ $checked -eq 0 ]]; then
  # Listed with a glob, not with the find above, so a scan that stopped seeing
  # bundles cannot also hide them here.
  for app in "${apps[@]}"; do
    for dir in PlugIns Extensions Watch AppClips; do
      for entry in "$app/$dir"/*; do
        if [[ -e "$entry" ]]; then
          echo "::error::verify-bundle-secrets: ${app#"$products"/}/$dir holds ${entry##*/}, but no bundle besides the app was checked; the scan is broken" >&2
          exit 2
        fi
      done
    done
  done
  echo "verify-bundle-secrets: $app_name embeds no other bundle and none was built beside it; nothing else can carry the keys"
  exit 0
fi
echo "verify-bundle-secrets: checked $checked non-app bundles, none carry secrets keys (${#apps[@]} $app_name allowed)"
