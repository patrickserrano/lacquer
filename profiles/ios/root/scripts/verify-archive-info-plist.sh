#!/usr/bin/env bash
#
# verify-archive-info-plist.sh — after the archive, before anything uploads,
# read the BUILT app's Info.plist and refuse the release if a build-time key
# reached it empty, unexpanded, or as a placeholder.
#
# WHY THIS EXISTS. write-release-config.sh checks a secret's VALUE: set,
# non-empty, the declared shape. It never checked that the value reached the
# product. Between the two sits the project's own xcconfig wiring — which file
# is the base configuration, whether it `#include?`s the written file, whether
# a committed xcconfig assigns the same key after it — and when that wiring is
# wrong the archive bakes in the committed placeholder or nothing at all while
# every gate stays green. Flare shipped REVENUECAT_PUBLIC_SDK_KEY =
# REPLACE_ME_APPL_KEY that way; its pre-lacquer workflow had a check like this
# one, and onboarding dropped it (flare #266). kit and port-of-entry declare no
# secrets at all and would archive with REVENUECAT_API_KEY, APTABASE_APP_KEY and
# SENTRY_DSN empty. That is lacquer#333's shape exactly: a passing state
# reachable without the checked thing having happened.
#
# So this reads the one artifact that cannot be wrong about what shipped.
#
# WHAT IT CHECKS. Every Info.plist value whose SOURCE was a `$(VAR)` or
# `${VAR}` build-setting reference — declared in .lacquer.toml or not — except
# Xcode's own standard settings (STANDARD_SETTINGS below). The mapping comes
# from the source Info.plist, never from key names: a key is checked because
# the project's plist references it, not because it looks like a secret.
#
#   * empty, still a literal $(VAR), or a known placeholder — see
#     secret-placeholders.sh, the one definition shared with the writer — fails
#   * a DECLARED key that does not match its secret_formats glob fails
#   * a declared key no Info.plist entry references is PRINTED, not failed:
#     some keys reach the code another way, and the log must show this check
#     did not cover them rather than imply that it did
#
# Undeclared keys fail on the same rules as declared ones. The measured fleet
# sweep behind that choice is in the pull request that added this script.
#
# It FAILS CLOSED. An archive, a plist or a build-settings query it cannot read
# stops the release; it never degrades to "nothing to check".
#
# A value is NEVER printed. Only key names, plist paths and the rule broken
# reach the log.
#
# Usage:
#
#   scripts/verify-archive-info-plist.sh --archive <App.xcarchive>
#       ( --project <X.xcodeproj> --scheme <scheme> | --info-plist <source> )
#       [--example <Secrets.xcconfig.example>]... [KEY[=GLOB] ...]
#
# --project/--scheme resolve the source Info.plist the way the archive did:
# `xcodebuild -showBuildSettings` for the Release configuration, the target
# whose product is the archived .app. --info-plist names it directly.
#
# KEY[=GLOB] are the product's declared [[product]].secrets and their
# secret_formats, in the writer's spelling.
set -euo pipefail

me="verify-archive-info-plist"
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=secret-placeholders.sh
. "$here/secret-placeholders.sh"

fail() {
	echo "::error::$me: $1"
	exit 1
}

# Xcode's own settings, which every app's Info.plist references and which are
# never a project's secret. An explicit list rather than a prefix rule: a
# project's own PRODUCT_API_KEY would slip under a `PRODUCT_*` guess. Anything
# not listed is checked, which is the safe direction to be wrong in — a
# non-secret custom setting with a real value passes the rules anyway.
STANDARD_SETTINGS="
AppIdentifierPrefix
CURRENT_PROJECT_VERSION
DEVELOPMENT_LANGUAGE
DEVELOPMENT_TEAM
EXECUTABLE_NAME
IPHONEOS_DEPLOYMENT_TARGET
MACOSX_DEPLOYMENT_TARGET
MARKETING_VERSION
PRODUCT_BUNDLE_IDENTIFIER
PRODUCT_BUNDLE_PACKAGE_TYPE
PRODUCT_MODULE_NAME
PRODUCT_NAME
TARGET_NAME
TeamIdentifierPrefix
TVOS_DEPLOYMENT_TARGET
WATCHOS_DEPLOYMENT_TARGET
XROS_DEPLOYMENT_TARGET
"

archive="" project="" scheme="" source_plist=""
examples=()
keys=()
globs=()
while [ "$#" -gt 0 ]; do
	case "$1" in
	--archive | --project | --scheme | --info-plist | --example)
		[ "$#" -ge 2 ] || fail "$1 needs a value"
		case "$1" in
		--archive) archive=$2 ;;
		--project) project=$2 ;;
		--scheme) scheme=$2 ;;
		--info-plist) source_plist=$2 ;;
		--example) examples+=("$2") ;;
		esac
		shift 2
		;;
	--*) fail "unknown option $1" ;;
	*)
		spec=$1
		shift
		key=${spec%%=*}
		glob=""
		case "$spec" in
		*=*) glob=${spec#*=} ;;
		esac
		# The same charsets write-release-config.sh enforces, for the same
		# reason: the glob is used UNQUOTED as a case pattern.
		case "$key" in
		'' | [!A-Za-z_]* | *[!A-Za-z0-9_]*) fail "invalid key $(printf '%q' "$key")" ;;
		esac
		case "$glob" in
		*[!A-Za-z0-9_~.:/*?@-]*) fail "$key: pattern has characters that are unsafe in a shell pattern" ;;
		esac
		keys+=("$key")
		globs+=("$glob")
		;;
	esac
done

[ -n "$archive" ] || fail "no --archive given"
if [ -z "$source_plist" ] && { [ -z "$project" ] || [ -z "$scheme" ]; }; then
	fail "give --info-plist, or both --project and --scheme to resolve it"
fi
command -v python3 >/dev/null 2>&1 || fail "python3 is required to read the archived Info.plist and was not found"

[ -d "$archive" ] || fail "no archive at $archive — the archive step did not produce one, so there is nothing to verify and the release cannot proceed"

work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/$me.XXXXXX")
# The records file holds the archived VALUES, so it is private and removed on
# every exit path.
chmod 700 "$work"
trap 'rm -rf "$work"' EXIT

# The Python helpers are written to files rather than run as heredocs inside
# $(...): bash 3.2, which is /bin/bash on the macOS runner, misparses a heredoc
# inside a command substitution when its body holds quotes or parentheses.
cat >"$work/app_path.py" <<'PY'
import plistlib, sys
try:
    with open(sys.argv[1], "rb") as f:
        p = plistlib.load(f)
    print(p["ApplicationProperties"]["ApplicationPath"])
except Exception as e:
    sys.stderr.write("%s: %s\n" % (type(e).__name__, e))
    sys.exit(1)
PY

cat >"$work/source_plist.py" <<'PY'
import json, os, sys
settings, app = sys.argv[1], sys.argv[2]
try:
    # From the first line that opens the JSON array: xcodebuild can print a
    # notice ahead of it, and a stray line must not read as "no settings".
    with open(settings) as f:
        text = f.read()
    start = text.find("\n[")
    entries = json.loads(text if text.startswith("[") or start < 0 else text[start + 1:])
except Exception as e:
    sys.stderr.write("unreadable build settings: %s\n" % e)
    sys.exit(2)
hits = [e.get("buildSettings", {}) for e in entries
        if e.get("buildSettings", {}).get("FULL_PRODUCT_NAME") == app]
if len(hits) != 1:
    sys.stderr.write("%d targets build %s\n" % (len(hits), app))
    sys.exit(3)
s = hits[0]
plist = s.get("INFOPLIST_FILE", "")
if plist and not os.path.isabs(plist):
    plist = os.path.join(s.get("PROJECT_DIR") or s.get("SRCROOT") or ".", plist)
print(plist)
PY

# One record per (Info.plist path, referenced setting), NUL-separated so no
# value can break the framing: setting, path, status, value. status is `ok`
# (value is what the setting contributed), `absent` (the source entry is not in
# the archived plist), `nonstring`, or `shape` (the archived value does not have
# its source's shape, so the setting's part of it cannot be isolated).
#
# A composite source value — `https://$(HOST)/v1` — is matched against the
# archived value with each reference as a group, so each setting is checked on
# what IT contributed rather than on the whole string, which a literal prefix
# would always make non-empty.
cat >"$work/records.py" <<'PY'
import os, plistlib, re, sys

REF = re.compile(r"\$\(([A-Za-z_][A-Za-z0-9_]*)(?::[^)]*)?\)|\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}")
standard = set(os.environ["STANDARD_SETTINGS"].split())

def load(path):
    with open(path, "rb") as f:
        return plistlib.load(f)

src, built = load(sys.argv[1]), load(sys.argv[2])
out = sys.stdout.buffer

def emit(var, path, status, value=""):
    for field in (var, path, status, value):
        out.write(field.encode("utf-8") + b"\0")

MISSING = object()

def walk(s, b, path):
    if isinstance(s, dict):
        for k in sorted(s):
            child = b.get(k, MISSING) if isinstance(b, dict) else MISSING
            walk(s[k], child, "%s.%s" % (path, k) if path else k)
    elif isinstance(s, list):
        for i, item in enumerate(s):
            child = b[i] if isinstance(b, list) and i < len(b) else MISSING
            walk(item, child, "%s[%d]" % (path, i))
    elif isinstance(s, str):
        refs = list(REF.finditer(s))
        names = [m.group(1) or m.group(2) for m in refs]
        checked = [n for n in names if n not in standard]
        if not checked:
            return
        if b is MISSING:
            for n in checked:
                emit(n, path, "absent")
            return
        if not isinstance(b, str):
            for n in checked:
                emit(n, path, "nonstring")
            return
        pattern, last = "", 0
        for m in refs:
            pattern += re.escape(s[last:m.start()]) + "(.*?)"
            last = m.end()
        pattern += re.escape(s[last:])
        got = re.fullmatch(pattern, b, re.S)
        for i, n in enumerate(names):
            if n in standard:
                continue
            if got is None:
                emit(n, path, "shape")
            else:
                emit(n, path, "ok", got.group(i + 1))

walk(src, built, "")
PY

# Where the app is inside the archive. The archive's own Info.plist records it
# (ApplicationProperties.ApplicationPath); guessing from a directory listing
# would pick an arbitrary .app if an archive ever held two.
app_rel=$(python3 "$work/app_path.py" "$archive/Info.plist") ||
	fail "cannot read ApplicationProperties.ApplicationPath from $archive/Info.plist — not an application archive, or unreadable"
app="$archive/Products/$app_rel"
# An iOS bundle is flat; a macOS one keeps its plist under Contents/.
archived_plist="$app/Info.plist"
if [ -f "$app/Contents/Info.plist" ]; then
	archived_plist="$app/Contents/Info.plist"
fi
[ -f "$archived_plist" ] || fail "the archive names $app_rel but $archived_plist does not exist"
app_name=$(basename "$app")

if [ -z "$source_plist" ]; then
	# The build settings the archive was built with, for the target whose
	# product IS the archived app. Release, because that is what `xcodebuild
	# archive` builds; a Debug lookup could resolve a different plist.
	if ! xcodebuild -showBuildSettings -json -project "$project" -scheme "$scheme" \
		-configuration Release >"$work/settings.json" 2>"$work/settings.err"; then
		tail -20 "$work/settings.err" >&2 || true
		fail "xcodebuild -showBuildSettings failed for scheme $scheme — cannot find the source Info.plist, so the archive cannot be verified"
	fi
	source_plist=$(python3 "$work/source_plist.py" "$work/settings.json" "$app_name") ||
		fail "the build settings for scheme $scheme do not identify exactly one target producing $app_name"
	if [ -z "$source_plist" ]; then
		# Generated entirely from INFOPLIST_KEY_* settings, which only carry
		# Apple's own keys — so nothing in it can reference a custom setting.
		# A warning, not a quiet line: one fleet project sets INFOPLIST_FILE
		# only inside its gitignored Secrets.xcconfig, so when that file is
		# absent the app is built from a generated plist, its keys never reach
		# it, and this check has nothing to read.
		echo "::warning::$me: $app_name has no INFOPLIST_FILE in its Release build settings, so its Info.plist is generated and references no custom build setting — nothing here could be checked. If INFOPLIST_FILE is meant to come from a secrets xcconfig, that file did not reach this build."
		source_plist=/dev/null
	fi
fi
if [ "$source_plist" != /dev/null ] && [ ! -f "$source_plist" ]; then
	fail "source Info.plist $source_plist does not exist"
fi

if [ "$source_plist" = /dev/null ]; then
	: >"$work/records"
elif ! STANDARD_SETTINGS=$STANDARD_SETTINGS python3 "$work/records.py" "$source_plist" "$archived_plist" >"$work/records"; then
	fail "cannot read $source_plist or $archived_plist as a property list"
fi

declared_index() {
	local want=$1 i=0 k
	for k in "${keys[@]+"${keys[@]}"}"; do
		if [ "$k" = "$want" ]; then
			echo "$i"
			return 0
		fi
		i=$((i + 1))
	done
	return 1
}

example_for() {
	local key=$1 f v
	for f in "${examples[@]+"${examples[@]}"}"; do
		v=$(example_value "$f" "$key")
		if [ -n "$v" ]; then
			printf '%s' "$v"
			return 0
		fi
	done
}

for f in "${examples[@]+"${examples[@]}"}"; do
	[ -f "$f" ] || echo "$me: no $f in this checkout — the equals-the-example rule has nothing to compare against for it"
done

failures=0
checked=0
covered=" "
while IFS= read -r -d '' var && IFS= read -r -d '' path && IFS= read -r -d '' status && IFS= read -r -d '' value; do
	checked=$((checked + 1))
	covered="$covered$var "
	kind=undeclared
	glob=""
	if idx=$(declared_index "$var"); then
		kind=declared
		glob=${globs[$idx]}
	fi

	reason=""
	case "$status" in
	absent) reason="is referenced by the source Info.plist at $path but that entry is missing from the archived one — the source resolved here is not the plist that was built" ;;
	nonstring) reason="reached the archived Info.plist at $path as a non-string value" ;;
	shape) reason="cannot be isolated: the archived value at $path does not have its source's shape" ;;
	ok)
		if r=$(placeholder_reason "$value" "$(example_for "$var")"); then
			reason="$r in the archived Info.plist ($path)"
		elif [ -n "$glob" ]; then
			# shellcheck disable=SC2254 # the glob is deliberately unquoted, and validated above
			case "$value" in
			$glob) ;;
			*) reason="does not match its declared shape $glob in the archived Info.plist ($path)" ;;
			esac
		fi
		;;
	*) fail "internal: unknown record status for $var" ;;
	esac

	if [ -z "$reason" ]; then
		echo "$me: ok — $var ($kind) reached $app_name/Info.plist at $path"
		continue
	fi
	failures=$((failures + 1))
	if [ "$kind" = declared ]; then
		echo "::error::$me: $var $reason"
	else
		echo "::error::$me: $var (undeclared build-time key) $reason — declare it in .lacquer.toml, under [project].secrets for a single app or [[product]].secrets, as $var = \"<name of the GitHub secret holding its value>\", so the release writes the real value. A flag meant to be off is written NO or 0, never left empty."
	fi
done <"$work/records"

for k in "${keys[@]+"${keys[@]}"}"; do
	case "$covered" in
	*" $k "*) ;;
	*) echo "$me: NOT COVERED — declared key $k is referenced by no Info.plist entry, so this check cannot see whether it reached the app" ;;
	esac
done

echo "$me: checked $checked build-setting reference(s) in $app_name/Info.plist; $failures failed"
if [ "$failures" -gt 0 ]; then
	echo "$me: the archive carries keys that are empty, unexpanded or placeholders. It would build, sign, upload and"
	echo "$me: pass review as a non-functional app, so the release stops here, before anything is uploaded."
	exit 1
fi
