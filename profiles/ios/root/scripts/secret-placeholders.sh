# shellcheck shell=bash
#
# secret-placeholders.sh — the ONE definition of "this value is a placeholder,
# not a real key", sourced by both release-time guards:
#
#   * scripts/write-release-config.sh, BEFORE it writes a declared secret — the
#     fast check, which fails a release in seconds
#   * scripts/verify-archive-info-plist.sh, AFTER the archive — the backstop,
#     which reads what actually reached the built app
#
# They share this file so they cannot drift. A rule added to one and not the
# other is a value the writer refuses and the archive check waves through, or
# the reverse, and nobody would notice until a release shipped with it.
#
# WHY A PLACEHOLDER IS WORSE THAN A MISSING VALUE. Every one of these is
# non-empty, so every accessor that only checks for blank passes it through.
# Flare's REVENUECAT_PUBLIC_SDK_KEY = REPLACE_ME_APPL_KEY builds, signs, uploads
# and passes review; the app then configures RevenueCat with a key that does not
# exist, and nothing looks wrong until the subscriptions do not arrive.
#
# Sourcing this file defines functions and nothing else. It never prints a
# value — callers get a REASON, which names the rule and not the input.
#
# bash 3.2-compatible on purpose: that is /bin/bash on the macOS release runner,
# so no ${v,,}, no associative arrays, no mapfile.

# placeholder_reason VALUE [EXAMPLE_VALUE]
#
# Prints why VALUE is not a real key and returns 0, or prints nothing and
# returns 1 when VALUE looks real. EXAMPLE_VALUE is the same key's value in the
# committed *.xcconfig.example, already normalised by example_value; a value
# equal to it is the template shipped verbatim.
placeholder_reason() {
	local v=$1 example=${2-} trimmed restore reason=""

	# Whitespace-only is empty: an xcconfig value is trimmed by Xcode, so
	# `KEY =   ` reaches the app as "".
	trimmed=${v#"${v%%[![:space:]]*}"}
	trimmed=${trimmed%"${trimmed##*[![:space:]]}"}
	if [ -z "$trimmed" ]; then
		echo "is empty"
		return 0
	fi

	# A `$(` or `${` in a value is either a reference Xcode never expanded (in
	# the archive) or one it WILL expand (in a secret about to be written to an
	# xcconfig, which substitutes it and silently changes the key). The writer
	# could escape it as `$$(` — measured: that reaches the built Info.plist as
	# a literal `$(` — but the archive check then could not tell that literal
	# from a reference Xcode failed to expand, so such a key could never pass
	# the backstop. Refusing it at both ends is the only consistent answer.
	case "$v" in
	*'$('* | *'${'*)
		echo 'contains a $(…) or ${…} build-setting reference'
		return 0
		;;
	esac

	restore=$(shopt -p nocasematch)
	shopt -s nocasematch
	case "$trimmed" in
	*REPLACE_ME* | *REPLACE-ME*) reason="is a REPLACE_ME placeholder" ;;
	your_* | your-* | *://your_* | *://your-*) reason="is a your_…/your-… placeholder" ;;
	appl_xxxx*) reason="is the appl_xxxx… RevenueCat placeholder" ;;
	esac
	$restore
	if [ -n "$reason" ]; then
		echo "$reason"
		return 0
	fi

	# The profile's Secrets.xcconfig.example ships A-DEV-0000000000; the same
	# all-zero id under any region prefix is the same placeholder.
	if [[ $trimmed =~ ^A-[A-Z]+-0+$ ]]; then
		echo "is the all-zero Aptabase placeholder"
		return 0
	fi
	# The example's Sentry DSN: an all-zero public key. Real DSN keys are hex
	# and never all zeros.
	if [[ $trimmed =~ ^[A-Za-z][A-Za-z0-9+.-]*://0+@ ]]; then
		echo "is the all-zero Sentry DSN placeholder"
		return 0
	fi
	# A URL husk: a scheme with no host — `https://`, `https:/`, `http:`. It is
	# what `https:/$()/$(EMPTY)` resolves to when the variable after the escape
	# is empty, and it is non-empty, so an emptiness check passes it.
	if [[ $trimmed =~ ^[A-Za-z][A-Za-z0-9+.-]*:/*$ ]]; then
		echo "is a URL with no host (a scheme-only husk)"
		return 0
	fi

	if [ -n "$example" ] && [ "$trimmed" = "$example" ]; then
		echo "equals the committed .example placeholder for this key"
		return 0
	fi
	return 1
}

# example_value FILE KEY
#
# Prints KEY's value in the xcconfig FILE as the BUILT app would see it: the
# trailing `//` comment dropped, the `$()` empty-substitution escape removed
# (`https:/$()/host` is `https://host` once built), surrounding space trimmed.
# Prints nothing when the file or the key is absent. KEY is an identifier the
# callers have already validated, so it is safe inside the awk regex.
example_value() {
	local file=$1 key=$2
	[ -f "$file" ] || return 0
	awk -v k="$key" '
		$0 ~ ("^[ \t]*" k "[ \t]*=") {
			v = $0
			sub(/^[^=]*=/, "", v)
			c = index(v, "//")
			if (c > 0) v = substr(v, 1, c - 1)
			gsub(/\$\(\)/, "", v)
			sub(/^[ \t]+/, "", v)
			sub(/[ \t\r]+$/, "", v)
			print v
			exit
		}
	' "$file"
}
