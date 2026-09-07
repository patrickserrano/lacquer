#!/usr/bin/env bash
#
# write-release-config.sh — assemble the xcconfig a RELEASE archive compiles
# against, from real values held in GitHub Actions secrets.
#
# WHY THIS EXISTS, in one incident.
#
# Rail's release workflow carried a hand-written "Setup Secrets" step. Lacquer's
# onboarding replaced that workflow with the shared profile's, which had no such
# step, and nothing said so. Every archive after that shipped with its
# app-runtime keys unset: `Purchases.configure` never ran, and RevenueCatUI's
# paywall calls `fatalError("Purchases has not been configured.")` in any
# non-DEBUG build. That shipped as 1.1.0 (200) and was rejected under App Store
# Review Guideline 2.1(a). The build was green the whole way.
#
# So the contract here is fail-closed at every step, and the failure is loud:
#
#   * a declared key that is unset OR empty stops the release before it archives
#   * a value that does not match its declared shape stops it too — pasting the
#     paid app's RevenueCat key into the free app produces a perfectly non-empty
#     value that builds, signs, uploads and passes review
#   * `//` in a value is escaped, because xcconfig treats it as the start of a
#     comment: a bare `https://host` truncates to `https:`, which is non-empty,
#     so nothing downstream notices and the service is silently misconfigured
#   * a value is NEVER printed, echoed, or interpolated into a shell word — only
#     key NAMES reach the log
#
# Usage:
#
#   scripts/write-release-config.sh <dest.xcconfig> [KEY[=GLOB] ...]
#
# Values are read from the environment variable of the same name as KEY; the
# caller puts them there via the step's `env:` block, so they never appear on a
# command line (which is world-readable in /proc on a shared runner). GLOB is an
# optional shell glob the value must match.
#
# With no keys the script only seeds <dest.xcconfig> from <dest.xcconfig>.example.
# That is the sibling-product case: a paid variant that reads the same base
# configuration file as its free sibling but declares no secrets of its own
# still needs the file to exist, or `xcodebuild archive` fails before compiling.
#
set -euo pipefail

me="write-release-config"

fail() {
	echo "::error::$me: $1"
	exit 1
}

dest=${1:-}
[ -n "$dest" ] || fail "no destination xcconfig given (usage: $me <dest.xcconfig> [KEY[=GLOB] ...])"
shift

# The file holds live credentials on a self-hosted runner whose disk outlives
# the job, so it is never world- or group-readable, not even for the instant
# between creation and chmod.
umask 077

mkdir -p "$(dirname "$dest")"

example="$dest.example"
if [ -f "$example" ]; then
	# Seed from the committed template FIRST. The xcconfig is the Xcode target's
	# base configuration file, so it must carry every key the project references
	# — not only the ones held in secrets. Writing just the declared keys leaves
	# the rest undefined, which is the same silent-empty failure one layer down.
	cp "$example" "$dest"
elif [ "$#" -eq 0 ]; then
	fail "$example does not exist and no keys were given — there is nothing to write"
else
	: >"$dest"
fi
chmod 600 "$dest"

# xcconfig_escape rewrites every `//` as `/$()/`. `$()` is xcconfig's
# empty-substitution and expands to nothing at build time, so the value the
# compiler sees is unchanged while the file contains no comment marker.
#
# Pure parameter expansion, never sed or awk on the value: a secret containing
# `&`, `\1` or a backslash is DATA here, and both of those tools would treat it
# as syntax and corrupt it silently. The loop is needed for `///` — a single
# global replace leaves the third slash paired with the one it just inserted.
xcconfig_escape() {
	local v=$1
	while [ "${v#*//}" != "$v" ]; do
		v="${v%%//*}/\$()/${v#*//}"
	done
	printf '%s' "$v"
}

# set_key replaces KEY's line in the file, or appends one if the template has
# none. The value travels through the environment into awk's ENVIRON rather than
# `-v`, which interprets backslash escapes and would turn a `\n` inside a secret
# into a newline — splitting the value across two xcconfig lines.
set_key() {
	local file=$1
	XCCONFIG_KEY=$2
	XCCONFIG_VALUE=$3
	export XCCONFIG_KEY XCCONFIG_VALUE
	awk '
		BEGIN { k = ENVIRON["XCCONFIG_KEY"]; v = ENVIRON["XCCONFIG_VALUE"]; done = 0 }
		!done && $0 ~ ("^" k "[ \t]*=") { print k " = " v; done = 1; next }
		{ print }
		END { if (!done) print k " = " v }
	' "$file" >"$file.tmp"
	mv "$file.tmp" "$file"
	unset XCCONFIG_KEY XCCONFIG_VALUE
}

keys=()
globs=()
for spec in "$@"; do
	key=${spec%%=*}
	glob=""
	case "$spec" in
	*=*) glob=${spec#*=} ;;
	esac
	# The key is spliced into an awk regex and printed to the left of `=`; the
	# glob is used UNQUOTED as a `case` pattern, where `(`, `)`, `|` and `&`
	# change the parse. Both charsets match what internal/config validates a
	# manifest against, so a manifest cannot inject shell into a release — and
	# neither can a hand-typed invocation.
	case "$key" in
	'' | [!A-Za-z_]* | *[!A-Za-z0-9_]*) fail "invalid key $(printf '%q' "$key")" ;;
	esac
	case "$glob" in
	*[!A-Za-z0-9_~.:/*?-]*) fail "$key: pattern has characters that are unsafe in a shell pattern" ;;
	esac
	keys+=("$key")
	globs+=("$glob")
done

# Presence first, and ALL of them at once. Failing on the first missing key
# turns provisioning a release into one round trip per secret, each costing a
# whole run to discover.
missing=()
for key in "${keys[@]+"${keys[@]}"}"; do
	if [ -z "${!key-}" ]; then
		missing+=("$key")
	fi
done
if [ "${#missing[@]}" -gt 0 ]; then
	echo "::error::$me: required release secret(s) unset or empty: ${missing[*]}"
	echo "$me: a release built without them archives with those keys unset. That is not a build"
	echo "$me: failure — it signs, uploads and reaches App Review as a non-functional app."
	echo "$me: set them with: gh secret set <NAME> -R <owner>/<repo>"
	exit 1
fi

# Shape second. Non-empty is not the same as correct: another app's key and
# Google's public test AdMob id are both perfectly non-empty.
i=0
for key in "${keys[@]+"${keys[@]}"}"; do
	glob=${globs[$i]}
	i=$((i + 1))
	[ -n "$glob" ] || continue
	# shellcheck disable=SC2254 # the glob is deliberately unquoted, and validated above
	case "${!key}" in
	$glob) ;;
	*) fail "$key does not match its declared shape $glob — releasing with it would ship the wrong key" ;;
	esac
done

for key in "${keys[@]+"${keys[@]}"}"; do
	raw=${!key}
	escaped=$(xcconfig_escape "$raw")
	set_key "$dest" "$key" "$escaped"
	if [ "$escaped" != "$raw" ]; then
		echo "$me: set $key in $dest (// escaped as /\$()/)"
	else
		echo "$me: set $key in $dest"
	fi
done

chmod 600 "$dest"

# Last line of defence, and the one that catches a key the template carried but
# nobody declared. A value the compiler will truncate at `//` is worse than a
# missing one: it is non-empty, so every accessor that only checks for blank
# passes it through. Only the KEY is named — never the value.
if bad=$(grep -nE '^[A-Za-z_][A-Za-z0-9_]*[ \t]*=[ \t]*[A-Za-z][A-Za-z0-9+.-]*://' "$dest" | sed -E 's/^([0-9]+):([A-Za-z_][A-Za-z0-9_]*).*/line \1: \2/'); then
	echo "::error::$me: $dest carries an unescaped '://'. xcconfig treats '//' as a comment, so that value truncates at it and the service is silently misconfigured."
	echo "$me: affected — $bad"
	echo "$me: use the /\$()/ escape (https:/\$()/host) in $example, or declare the key in [[product]].secrets so this script escapes it."
	exit 1
fi

echo "$me: wrote $dest"
