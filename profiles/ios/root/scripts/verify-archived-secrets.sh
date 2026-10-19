#!/usr/bin/env bash
#
# verify-archived-secrets.sh — after the archive and before the export, fail the
# release when a declared build-time key did not reach the ARCHIVED app's
# Info.plist with a real value.
#
# Usage:
#
#   scripts/verify-archived-secrets.sh --example <template> [--sources DIR]
#       [--unresolved-fails-from YYYY-MM-DD] <App.xcarchive> <App.app>
#       NAME[=PlistKey] ...
#
#   --example    the committed `.example` template the release config is seeded
#                from; a value still equal to its line for NAME is a placeholder
#   --sources    the component's source tree, searched for the Info.plist key
#                that carries a NAME declared without one (see below)
#   --unresolved-fails-from
#                the first day (UTC) a NAME whose plist key cannot be found
#                fails; before it, the same finding is a named warning. Absent,
#                it fails from the start
#   NAME         a declared secret ([[product]].secrets), optionally with the
#                Info.plist key that carries it ([[product]].secret_plist_keys)
#
# WHY IT EXISTS. write-release-config.sh refuses a blank or placeholder VALUE
# when it writes the build-time config. Nothing checked that the value then
# REACHED the product. Between the two sits the project's own wiring: which
# xcconfig is the base configuration, which build setting the plist reads, the
# plist template itself. A custom INFOPLIST_KEY_* setting is silently dropped
# from a generated plist; a template can lose its entry; a setting can point at a
# variable nothing defines. Each one archives, signs and uploads an app whose
# runtime key is empty or a placeholder, and every check before this one passes.
#
# WHICH PLIST KEY CARRIES A NAME. A secret's name and its plist key are seldom
# the same (`RevenueCatAPIKey = $(REVENUECAT_API_KEY)`), and the archived plist
# holds only the resolved value under the plist's own name. So:
#   - NAME=PlistKey says it outright; that key is checked.
#   - otherwise every top-level key, in any .plist under --sources, whose string
#     value is EXACTLY $(NAME) or ${NAME} carries it, and each of those the
#     archived plist must hold. A value that only CONTAINS the reference
#     (`https://$(HOST)/x`) is not used: its resolved value is not the secret's,
#     so neither the placeholder test nor "empty" could be judged on it.
#   - a NAME with neither is UNRESOLVED. Nothing says where it should be, and a
#     guess that picked the wrong key would pass a release it should stop. It
#     fails (or, before --unresolved-fails-from, warns) and says what to declare.
#   - a NAME several source keys carry is checked under each one the archived
#     plist holds, and fails when it holds none of them: a key another target's
#     plist carries is not this app's to hold.
#
# A RESOLVED key fails when the archived plist lacks it, its value is not a
# string, is empty, holds a line break, or is still a placeholder — judged by
# secret-placeholders.awk, the same rules the writer applies.
#
# FAIL CLOSED. No archive, no app of that name in it, no Info.plist, a plist
# that does not parse or is not a dictionary, a missing template, python3 or the
# awk rules missing: each stops the release. None of them reads as "nothing to
# check".
#
# A VALUE IS NEVER PRINTED. Values are read by Python and handed to awk through
# a pipe; they never become a shell word, an argument or a log line, so not even
# `bash -x` can show one. Only NAMEs, plist keys and the rule broken are printed.
#
# Exit: 0 every resolved key is real · 1 a key failed · 2 a control failed or usage.
set -euo pipefail

me="verify-archived-secrets"

die() {
	echo "::error::$me: $1"
	exit 2
}

usage() {
	die "usage: $me --example <template> [--sources DIR] [--unresolved-fails-from YYYY-MM-DD] <App.xcarchive> <App.app> NAME[=PlistKey] ..."
}

example=""
sources=""
fails_from=""
while [ "$#" -gt 0 ]; do
	case "$1" in
	--example | --sources | --unresolved-fails-from)
		[ "$#" -ge 2 ] || usage
		case "$1" in
		--example) example=$2 ;;
		--sources) sources=$2 ;;
		--unresolved-fails-from) fails_from=$2 ;;
		esac
		shift 2
		;;
	--) shift; break ;;
	-*) usage ;;
	*) break ;;
	esac
done
[ "$#" -ge 3 ] || usage
archive=${1%/}
app_name=$2
shift 2

[ -n "$example" ] || die "--example names no template; nothing says what a placeholder looks like"
case "$app_name" in
*/* | .app | ..*) die "'$app_name' is not an app bundle's file name (expected Name.app)" ;;
*.app) ;;
*) die "'$app_name' is not an app bundle's file name (expected Name.app)" ;;
esac
if [ -n "$fails_from" ] && ! [[ "$fails_from" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
	die "--unresolved-fails-from '$fails_from' is not a YYYY-MM-DD date"
fi
names=()
for spec in "$@"; do
	name=${spec%%=*}
	case "$name" in
	'' | [!A-Za-z_]* | *[!A-Za-z0-9_]*) die "invalid key $(printf '%q' "$name")" ;;
	esac
	case "$spec" in
	*=*)
		case "${spec#*=}" in
		'' | *[!A-Za-z0-9_.-]*) die "$name: invalid Info.plist key $(printf '%q' "${spec#*=}")" ;;
		esac
		;;
	esac
	names+=("$name")
done

command -v python3 >/dev/null 2>&1 || die "python3 is needed to read property lists and is not on PATH"
placeholder_awk="$(dirname "${BASH_SOURCE[0]}")/secret-placeholders.awk"
[ -f "$placeholder_awk" ] || die "$placeholder_awk is missing, so a value still holding its placeholder could not be caught"
[ -f "$example" ] || die "no template at $example; a value equal to its placeholder could not be caught"
[ -z "$sources" ] || [ -d "$sources" ] || die "no source directory at $sources"
[ -d "$archive" ] || die "no archive at $archive; nothing was archived to check"
app="$archive/Products/Applications/$app_name"
[ -d "$app" ] || die "the archive holds no $app_name under Products/Applications; the app that ships cannot be checked"
plist="$app/Info.plist"
[ -f "$plist" ] || die "$app_name in the archive has no Info.plist"

report=$(mktemp)
trap 'rm -f "$report"' EXIT

# Python reads the archived plist (XML or binary) and the source tree, writes
# one report line per finding to $report — NAMEs and plist keys only — and
# writes `NAME = value` for every value it could read to stdout, which is the
# pipe into the placeholder rules. `//` is escaped as `/$()/` exactly as the
# writer stores it, so the rules read the same value from both.
#
# Report lines, tab-separated:
#   read       NAME  KEY[,KEY]   the keys whose value went down the pipe
#   fail       NAME  KEY  reason
#   unresolved NAME
#   fatal      reason
# shellcheck disable=SC2016 # a Python program; $(NAME) is its text, not shell
read_plist='
import os, plistlib, sys
plist, sources, report = sys.argv[1], sys.argv[2], sys.argv[3]
specs = sys.argv[4:]
out = open(report, "w")
def emit(*f):
    out.write("\t".join(f) + "\n")
try:
    with open(plist, "rb") as fh:
        info = plistlib.load(fh)
except Exception:
    emit("fatal", "the archived Info.plist does not parse as a property list")
    sys.exit(3)
if not isinstance(info, dict) or not isinstance(info.get("CFBundleIdentifier"), str):
    emit("fatal", "the archived Info.plist is not a dictionary with a CFBundleIdentifier")
    sys.exit(3)

skip = {"DerivedData", "build", "Pods", "Carthage", "node_modules"}
carriers = {}
if sources:
    for d, dirs, files in os.walk(sources):
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
                if isinstance(v, str):
                    carriers.setdefault(v.strip(), set()).add(k)

def esc(v):
    while "//" in v:
        i = v.index("//")
        v = v[:i] + "/$()/" + v[i + 2:]
    return v

for spec in specs:
    name, _, key = spec.partition("=")
    if key:
        keys = [key]
    else:
        found = carriers.get("$(%s)" % name, set()) | carriers.get("${%s}" % name, set())
        if not found:
            emit("unresolved", name)
            continue
        held = sorted(k for k in found if k in info)
        if not held:
            emit("fail", name, ",".join(sorted(found)), "is not in the archived Info.plist")
            continue
        keys = held
    good = []
    for k in keys:
        if k not in info:
            emit("fail", name, k, "is not in the archived Info.plist")
        elif not isinstance(info[k], str):
            emit("fail", name, k, "is not a string in the archived Info.plist")
        elif not info[k].strip():
            emit("fail", name, k, "is empty in the archived Info.plist")
        elif "\n" in info[k] or "\r" in info[k]:
            emit("fail", name, k, "holds a line break, which no build setting produces")
        else:
            good.append(k)
            sys.stdout.write("%s = %s\n" % (name, esc(info[k])))
    if good:
        emit("read", name, ",".join(good))
'

status=0
placeholders=$(
	set -o pipefail
	python3 -I -c "$read_plist" "$plist" "$sources" "$report" "$@" |
		XCCONFIG_DECLARED="${names[*]}" XCCONFIG_TEMPLATE="$example" awk -f "$placeholder_awk"
) || status=$?

if [ "$status" -ne 0 ]; then
	reason=$(awk -F '\t' '$1 == "fatal" { print $2; exit }' "$report")
	[ -n "$reason" ] || reason="reading the archived Info.plist of $app_name failed (status $status)"
	die "$reason; an unread plist cannot be shown to carry its keys"
fi

today=$(date -u +%Y-%m-%d)
failed=0
bad=" $placeholders "
read_keys=()
while IFS=$'\t' read -r kind name key reason; do
	case "$kind" in
	read) read_keys+=("$name ($key)") ;;
	fail)
		echo "::error::$me: $name: Info.plist key $key $reason of $app_name. The app would ship with that service unconfigured."
		bad="$bad$name "
		failed=1
		;;
	unresolved)
		msg="$name: no Info.plist key is known to carry it. No source plist sets a key to exactly \$($name), so this check cannot tell where it should be. Declare secret_plist_keys.$name = \"<Info.plist key>\" in .lacquer.toml"
		if [ -n "$fails_from" ] && [[ "$today" < "$fails_from" ]]; then
			echo "::warning::$me: $msg. NOT CHECKED; this fails releases from $fails_from."
		else
			echo "::error::$me: $msg."
			failed=1
		fi
		;;
	esac
done <"$report"

for name in $placeholders; do
	key=$(awk -F '\t' -v n="$name" '$1 == "read" && $2 == n { print $3; exit }' "$report")
	echo "::error::$me: $name: Info.plist key $key in the archived $app_name still holds a placeholder. Set the real value in its secret."
	failed=1
done

# A NAME is real only when every key it was read from passed every rule.
checked=()
for entry in ${read_keys[@]+"${read_keys[@]}"}; do
	case "$bad" in
	*" ${entry%% *} "*) ;;
	*) checked+=("$entry") ;;
	esac
done
if [ "${#checked[@]}" -gt 0 ]; then
	echo "$me: real values in the archived $app_name for: ${checked[*]}"
fi
if [ "$failed" -ne 0 ]; then
	echo "$me: FAIL. Only key names are printed, never a value."
	exit 1
fi
echo "$me: checked ${#checked[@]} of ${#names[@]} declared key(s) in the archived $app_name"
