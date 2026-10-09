#!/usr/bin/env bash
#
# write-release-config.sh — assemble the xcconfig a RELEASE archive compiles
# against, from real values held in GitHub Actions secrets.
#
# WHY THIS EXISTS, in one incident.
#
# A consumer's release workflow carried a hand-written step that wrote its
# secrets file. Adopting the shared profile replaced that workflow with one that
# had no such step, and nothing said so. Every archive after that shipped with
# its app-runtime keys unset: the purchases SDK was never configured, and its
# paywall traps in any non-DEBUG build. The build reached App Review and was
# rejected as non-functional. CI was green the whole way.
#
# So the contract here is fail-closed at every step, and the failure is loud:
#
#   * a declared key that is unset OR empty stops the release before it archives
#   * a value that does not match its declared shape stops it too — pasting the
#     paid app's RevenueCat key into the free app produces a perfectly non-empty
#     value that builds, signs, uploads and passes review
#   * a value still holding the `.example` template's placeholder stops it: a
#     declared secret equal to the template's value, or any key (declared or
#     only seeded) whose value is an obvious placeholder such as `your-...`,
#     `CHANGE-ME` or `<...>`. A placeholder is non-empty and can be made to
#     match its shape, so nothing above catches it
#   * `//` in a value is escaped, because xcconfig treats it as the start of a
#     comment: a bare `https://host` truncates to `https:`, which is non-empty,
#     so nothing downstream notices and the service is silently misconfigured
#   * a value is NEVER printed, echoed, or interpolated into a shell word — only
#     key NAMES reach the log
#
# Usage:
#
#   scripts/write-release-config.sh [--example=<template>] <dest.xcconfig> [KEY[=GLOB] ...]
#
# The template is <dest.xcconfig>.example unless --example names one, which is
# how a manifest's secrets_example reaches here. A declared template is the only
# one consulted: falling back to the file beside the destination would turn a
# typo in the manifest into a release seeded from some other file.
#
# Values are read from the environment variable of the same name as KEY; the
# caller puts them there via the step's `env:` block, so they never appear on a
# command line (which is world-readable in /proc on a shared runner). GLOB is an
# optional shell glob the value must match.
#
# With keys and no template the script REFUSES. It used to write a keys-only
# file: every key the project reads but does not hold in secrets was left
# undefined, and the placeholder check below had no template to compare against.
# That is a release that builds, signs and uploads — the failure this whole file
# exists to stop — and it was what a project got for keeping its template
# anywhere but beside the destination.
#
# With no keys the script only seeds <dest.xcconfig> from its template.
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

example=""
declared_example=0
case "${1:-}" in
--example=*)
	example=${1#--example=}
	[ -n "$example" ] || fail "--example= names no template"
	declared_example=1
	shift
	;;
esac

dest=${1:-}
[ -n "$dest" ] || fail "no destination xcconfig given (usage: $me [--example=<template>] <dest.xcconfig> [KEY[=GLOB] ...])"
shift
[ "$declared_example" -eq 1 ] || example="$dest.example"

# The file holds live credentials on a self-hosted runner whose disk outlives
# the job, so it is never world- or group-readable, not even for the instant
# between creation and chmod.
umask 077

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
	# neither can a hand-typed invocation. `@` is in both: a Sentry DSN cannot be
	# shaped without it, and it is inert here (its only meaning is extglob's
	# `@(...)`, which needs the parentheses refused above). It was missing from
	# this side once, so every release declaring the DSN shape failed;
	# TestSecretFormatCharsetMatchesTheWriter compares the two classes now.
	case "$key" in
	'' | [!A-Za-z_]* | *[!A-Za-z0-9_]*) fail "invalid key $(printf '%q' "$key")" ;;
	esac
	case "$glob" in
	*[!A-Za-z0-9_~.:/*?@-]*) fail "$key: pattern has characters that are unsafe in a shell pattern" ;;
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

# Template third, and before anything is written. Every refusal above and here
# leaves no file behind, so a failed release cannot leave a half-written
# credentials file on a runner whose disk outlives the job — nor a destination
# directory that exists only because this script made it.
if [ ! -f "$example" ]; then
	if [ "$declared_example" -eq 1 ]; then
		echo "::error::$me: the template declared by secrets_example, $example, does not exist."
		echo "$me: the file beside the destination, $dest.example, is deliberately not consulted when a template is declared."
		echo "$me: correct secrets_example (it is relative to the component root, like secrets_file), or commit the template."
		exit 1
	fi
	if [ "${#keys[@]}" -eq 0 ]; then
		fail "$example does not exist and no keys were given — there is nothing to write"
	fi
	echo "::error::$me: release keys are declared for $dest but no template was found: looked beside it at $example, and the manifest declares no secrets_example."
	echo "$me: without the template the file would carry the declared keys only — every other key the project reads would be"
	echo "$me: undefined, and nothing could check a secret against its placeholder. The archive would build, sign and upload."
	echo "$me: if the template lives elsewhere, set [[product]].secrets_example (or [project].secrets_example) to its path,"
	echo "$me: relative to the component root like secrets_file. Otherwise commit $example."
	exit 1
fi

mkdir -p "$(dirname "$dest")"
# Seed from the committed template FIRST. The xcconfig is the Xcode target's
# base configuration file, so it must carry every key the project references —
# not only the ones held in secrets. Writing just the declared keys leaves the
# rest undefined, which is the same silent-empty failure one layer down.
cp "$example" "$dest"
chmod 600 "$dest"

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

# And the one that catches a value nobody replaced. A placeholder is non-empty,
# and a template written to look like the real thing matches its declared shape
# too, so the presence and shape checks above both pass it. Two tests:
#
#   * any key whose value is an OBVIOUS placeholder — `your-`/`your_`,
#     `change-me`/`changeme`, or `<...>`, in any case. That covers a key added to
#     the template after the manifest was written, which is never declared and so
#     ships as seeded.
#   * a DECLARED key whose value equals the template's value for it: the secret
#     was set by copying the template. For an undeclared key that equality is
#     always true, so it says nothing, and the template's real non-secret
#     defaults pass.
#
# Both sides are compared with the `/$()/` escape undone, so escaping a declared
# URL secret cannot make it differ from the template it was copied from. The
# work happens in awk on the files, with the declared keys passed through the
# environment, so a value never becomes a shell word. Only KEY names are printed.
#
# With no keys this leg only seeds a file its sibling product declares, so the
# placeholders in it are the sibling's, and checking them here would block the
# leg that declares nothing whenever its sibling's keys are still templated.
if [ "${#keys[@]}" -gt 0 ]; then
	# awk prints the offending keys and exits 0; anything else is the check
	# itself failing, which must stop the release rather than read as "none".
	placeholders=$(XCCONFIG_DECLARED="${keys[*]}" XCCONFIG_TEMPLATE="$example" awk '
		# value is what xcconfig reads: cut at the `//` comment marker, trimmed,
		# with the `/$()/` escape undone. whole skips the cut, because a template
		# that wrote a URL placeholder unescaped holds the placeholder a human
		# copies, not the `https:` xcconfig would read.
		function whole(line) {
			sub(/^[^=]*=/, "", line)
			sub(/^[ \t]+/, "", line)
			sub(/[ \t\r]+$/, "", line)
			gsub(/\/\$\(\)\//, "//", line)
			return line
		}
		function value(line) {
			sub(/^[^=]*=/, "", line)
			sub(/\/\/.*/, "", line)
			return whole("=" line)
		}
		function key(line) {
			sub(/[ \t]*=.*/, "", line)
			return line
		}
		function assignment(line) {
			return line ~ /^[A-Za-z_][A-Za-z0-9_]*[ \t]*=/
		}
		BEGIN {
			n = split(ENVIRON["XCCONFIG_DECLARED"], d, " ")
			for (i = 1; i <= n; i++) declared[d[i]] = 1
			# A missing template makes getline return -1: no template values.
			tf = ENVIRON["XCCONFIG_TEMPLATE"]
			while ((getline line < tf) > 0)
				if (assignment(line)) {
					template[key(line)] = value(line)
					template_whole[key(line)] = whole(line)
				}
		}
		!assignment($0) { next }
		{
			k = key($0); v = value($0)
			if (tolower(v) ~ /your[-_]|change[-_]?me|<[^>]*>/ ||
			    (k in declared && k in template && (v == template[k] || v == template_whole[k]))) {
				if (!(k in seen)) { seen[k] = 1; out = out (out == "" ? "" : " ") k }
			}
		}
		END { print out }
	' "$dest") || fail "could not read $dest to check it for placeholder values"
	if [ -n "$placeholders" ]; then
		echo "::error::$me: $dest still holds a placeholder for: $placeholders"
		echo "$me: a placeholder is non-empty, so the release would build, sign and upload with that service unconfigured."
		echo "$me: a declared key means its secret is still the template's text: set the real value with gh secret set."
		echo "$me: an undeclared key was seeded from $example: declare it in the manifest's secrets so it is written from a secret,"
		echo "$me: or, if it is not a secret, replace the placeholder in $example with the real value."
		exit 1
	fi
fi

echo "$me: wrote $dest"
