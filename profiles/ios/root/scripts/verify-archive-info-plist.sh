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
# every gate stays green. Flare's committed xcconfig carries
# REVENUECAT_PUBLIC_SDK_KEY = REPLACE_ME_APPL_KEY; its pre-lacquer workflow had
# a check like this one, onboarding dropped it (flare #266), and its next
# release would have shipped the placeholder. kit and port-of-entry declare no
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
# That is done for the app AND every bundle it embeds — app extensions under
# PlugIns/ or Extensions/, a watch app under Watch/ and its own extensions —
# each against the source plist of the target that produced it. A bundle whose
# source cannot be found fails: "checked" and "never looked" must not print the
# same thing.
#
# References with no literal between them — `$(SCHEME)$(HOST)`, or
# `$(PRODUCT_NAME)$(SUFFIX)` — cannot be told apart in the built value, so such
# a run is judged as one value (empty, unexpanded, placeholder) and the line
# says so; no per-key shape or example rule applies to it.
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
#       ( --project <X.xcodeproj> --scheme <scheme>
#       | --info-plist <source> [--embedded-plist <Bundle.appex>=<source>]... )
#       [--example <Secrets.xcconfig.example>]... [KEY[=GLOB] ...]
#
# --project/--scheme resolve each bundle's source Info.plist the way the archive
# did: `xcodebuild -showBuildSettings` for the Release configuration, matched by
# FULL_PRODUCT_NAME, with `-alltargets` for any embedded bundle the scheme's own
# settings do not name. --info-plist and --embedded-plist name them directly.
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
embedded=()
keys=()
globs=()
while [ "$#" -gt 0 ]; do
	case "$1" in
	--archive | --project | --scheme | --info-plist | --embedded-plist | --example)
		[ "$#" -ge 2 ] || fail "$1 needs a value"
		case "$1" in
		--archive) archive=$2 ;;
		--project) project=$2 ;;
		--scheme) scheme=$2 ;;
		--info-plist) source_plist=$2 ;;
		--embedded-plist)
			case "$2" in
			?*=?*) embedded+=("$2") ;;
			*) fail "--embedded-plist takes BUNDLE=SOURCE, e.g. Widgets.appex=Widgets/Info.plist" ;;
			esac
			;;
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

# mapping.py OUT SETTINGS.json... — which source Info.plist each built product
# came from, keyed by FULL_PRODUCT_NAME. A later settings file only fills names
# the earlier ones lacked. A name two targets produce from DIFFERENT plists maps
# to null: guessing between them would check the wrong file.
cat >"$work/mapping.py" <<'PY'
import json, os, sys
out, files = sys.argv[1], sys.argv[2:]
mapping = {}
for path in files:
    with open(path) as f:
        text = f.read()
    # From the first line that opens the JSON array: xcodebuild can print a
    # notice ahead of it, and a stray line must not read as "no settings".
    start = text.find("\n[")
    entries = json.loads(text if text.startswith("[") or start < 0 else text[start + 1:])
    found = {}
    for e in entries:
        s = e.get("buildSettings", {})
        name = s.get("FULL_PRODUCT_NAME")
        if not name:
            continue
        plist = s.get("INFOPLIST_FILE", "")
        if plist and not os.path.isabs(plist):
            plist = os.path.join(s.get("PROJECT_DIR") or s.get("SRCROOT") or ".", plist)
        if name in found and found[name] != plist:
            found[name] = None
        else:
            found.setdefault(name, plist)
    for name, plist in found.items():
        mapping.setdefault(name, plist)
with open(out, "w") as f:
    json.dump(mapping, f)
PY

# explicit_mapping.py OUT NAME=PATH... — the same map, from --info-plist and
# --embedded-plist rather than from build settings.
cat >"$work/explicit_mapping.py" <<'PY'
import json, sys
mapping = {}
for pair in sys.argv[2:]:
    name, _, path = pair.partition("=")
    mapping[name] = path
with open(sys.argv[1], "w") as f:
    json.dump(mapping, f)
PY

# records.py APP MAPPING [--unmapped] — walk the app and every bundle embedded
# in it: app extensions (PlugIns/, Extensions/) and a watch app (Watch/), each
# of which has its own Info.plist and can reference build-time keys — a widget
# with its own Sentry DSN is an ordinary thing to ship.
#
# One record per (bundle, referenced setting), NUL-separated so no value can
# break the framing: bundle, setting, partners, plist path, status, value.
# status is:
#   ok         value is what the setting contributed
#   combined   the setting sits directly against other references (partners),
#              with no literal between them, so its own part cannot be told
#              apart; value is what the whole run contributed
#   absent     the source entry is not in the built plist
#   nonstring  the built value is not a string
#   shape      the built value does not have its source's shape
#   unmapped   no target in the build settings produces this bundle
#   ambiguous  two targets produce it, from different source plists
#   generated  its target has no INFOPLIST_FILE (nothing custom to check)
#   nosource   the mapped source plist is not on disk
#   noplist    the bundle has no Info.plist
#
# A composite source value — `https://$(HOST)/v1` — is matched against the
# built value with each run of references as one group, so a setting is judged
# on what IT contributed rather than on the whole string, which a literal
# prefix would always make non-empty.
#
# With --unmapped it prints only the names of bundles the map does not cover.
cat >"$work/records.py" <<'PY'
import glob, json, os, plistlib, re, sys

REF = re.compile(r"\$\(([A-Za-z_][A-Za-z0-9_]*)(?::[^)]*)?\)|\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}")
standard = set(os.environ.get("STANDARD_SETTINGS", "").split())
app, mapping_file = sys.argv[1], sys.argv[2]
unmapped_only = "--unmapped" in sys.argv[3:]
with open(mapping_file) as f:
    mapping = json.load(f)
out = sys.stdout.buffer

def bundles(path, label):
    yield path, label
    for sub in ("PlugIns", "Extensions", "Contents/PlugIns", "Contents/Extensions"):
        for b in sorted(glob.glob(os.path.join(glob.escape(path), sub, "*.appex"))):
            yield from bundles(b, "%s/%s/%s" % (label, sub, os.path.basename(b)))
    for b in sorted(glob.glob(os.path.join(glob.escape(path), "Watch", "*.app"))):
        yield from bundles(b, "%s/Watch/%s" % (label, os.path.basename(b)))

def plist_of(bundle):
    for p in (os.path.join(bundle, "Contents", "Info.plist"), os.path.join(bundle, "Info.plist")):
        if os.path.isfile(p):
            return p
    return None

def emit(bundle, var, status, path="", partners="", value=""):
    for field in (bundle, var, partners, path, status, value):
        out.write(field.encode("utf-8") + b"\0")

MISSING = object()

def walk(label, s, b, path):
    if isinstance(s, dict):
        for k in sorted(s):
            child = b.get(k, MISSING) if isinstance(b, dict) else MISSING
            walk(label, s[k], child, "%s.%s" % (path, k) if path else k)
    elif isinstance(s, list):
        for i, item in enumerate(s):
            child = b[i] if isinstance(b, list) and i < len(b) else MISSING
            walk(label, item, child, "%s[%d]" % (path, i))
    elif isinstance(s, str):
        refs = list(REF.finditer(s))
        # Runs of references with no literal between them: `$(A)$(B)` is ONE
        # run. Lazy groups side by side would hand A nothing and B everything.
        runs, lits, last = [], [], 0
        for m in refs:
            if runs and m.start() == last:
                runs[-1].append(m.group(1) or m.group(2))
            else:
                lits.append(s[last:m.start()])
                runs.append([m.group(1) or m.group(2)])
            last = m.end()
        lits.append(s[last:])
        if not any(n not in standard for run in runs for n in run):
            return
        def each(status, value=None):
            for run in runs:
                for n in run:
                    if n in standard:
                        continue
                    partners = " ".join(x for x in run if x != n)
                    st = status
                    if status == "ok" and len(run) > 1:
                        st = "combined"
                    emit(label, n, st, path, partners, "" if value is None else value[runs.index(run)])
        if b is MISSING:
            return each("absent")
        if not isinstance(b, str):
            return each("nonstring")
        pattern = "".join(re.escape(lit) + "(.*?)" for lit in lits[:-1]) + re.escape(lits[-1])
        got = re.fullmatch(pattern, b, re.S)
        if got is None:
            return each("shape")
        each("ok", got.groups())

for bundle, label in bundles(app, os.path.basename(app)):
    name = os.path.basename(bundle)
    if name not in mapping:
        if unmapped_only:
            print(name)
        else:
            emit(label, "", "unmapped")
        continue
    if unmapped_only:
        continue
    source = mapping[name]
    if source is None:
        emit(label, "", "ambiguous")
        continue
    if source == "":
        emit(label, "", "generated")
        continue
    if not os.path.isfile(source):
        emit(label, "", "nosource", source)
        continue
    built = plist_of(bundle)
    if built is None:
        emit(label, "", "noplist")
        continue
    with open(source, "rb") as f:
        src = plistlib.load(f)
    with open(built, "rb") as f:
        walk(label, src, plistlib.load(f), "")
PY

# Where the app is inside the archive. The archive's own Info.plist records it
# (ApplicationProperties.ApplicationPath); guessing from a directory listing
# would pick an arbitrary .app if an archive ever held two.
app_rel=$(python3 "$work/app_path.py" "$archive/Info.plist") ||
	fail "cannot read ApplicationProperties.ApplicationPath from $archive/Info.plist — not an application archive, or unreadable"
app="$archive/Products/$app_rel"
[ -d "$app" ] || fail "the archive names $app_rel but $app does not exist"
app_name=$(basename "$app")

if [ -n "$source_plist" ]; then
	python3 "$work/explicit_mapping.py" "$work/mapping.json" "$app_name=$source_plist" \
		"${embedded[@]+"${embedded[@]}"}" || fail "cannot record the source plists given"
else
	# The build settings the archive was built with. Release, because that is
	# what `xcodebuild archive` builds; a Debug lookup could resolve a
	# different plist.
	if ! xcodebuild -showBuildSettings -json -project "$project" -scheme "$scheme" \
		-configuration Release >"$work/settings.json" 2>"$work/settings.err"; then
		tail -20 "$work/settings.err" >&2 || true
		fail "xcodebuild -showBuildSettings failed for scheme $scheme — cannot find the source Info.plist, so the archive cannot be verified"
	fi
	python3 "$work/mapping.py" "$work/mapping.json" "$work/settings.json" ||
		fail "cannot read the build settings for scheme $scheme"
	# A scheme usually lists only the app, and an extension it embeds is built
	# as a dependency. When the scheme's settings do not name every bundle in
	# the archive, ask for every target in the project too.
	unmapped=$(STANDARD_SETTINGS=$STANDARD_SETTINGS python3 "$work/records.py" "$app" "$work/mapping.json" --unmapped) ||
		fail "cannot list the bundles in $app"
	if [ -n "$unmapped" ]; then
		if ! xcodebuild -showBuildSettings -json -project "$project" -alltargets \
			-configuration Release >"$work/all.json" 2>"$work/all.err"; then
			tail -20 "$work/all.err" >&2 || true
			fail "xcodebuild -showBuildSettings -alltargets failed — the embedded bundles' source plists cannot be found, so the archive cannot be verified"
		fi
		python3 "$work/mapping.py" "$work/mapping.json" "$work/settings.json" "$work/all.json" ||
			fail "cannot read the build settings for every target in $project"
	fi
fi

if ! STANDARD_SETTINGS=$STANDARD_SETTINGS python3 "$work/records.py" "$app" "$work/mapping.json" >"$work/records"; then
	fail "cannot read a source or built Info.plist in $app_name as a property list"
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
bundles_seen=0
covered=" "
while IFS= read -r -d '' bundle && IFS= read -r -d '' var && IFS= read -r -d '' partners &&
	IFS= read -r -d '' path && IFS= read -r -d '' status && IFS= read -r -d '' value; do
	# Per-bundle statuses carry no setting.
	case "$status" in
	generated)
		if [ "$bundle" = "$app_name" ]; then
			# A warning, not a quiet line: one fleet project sets INFOPLIST_FILE
			# only inside its gitignored Secrets.xcconfig, so when that file is
			# absent the app is built from a generated plist, its keys never
			# reach it, and this check has nothing to read.
			echo "::warning::$me: $app_name has no INFOPLIST_FILE in its Release build settings, so its Info.plist is generated and references no custom build setting — nothing here could be checked. If INFOPLIST_FILE is meant to come from a secrets xcconfig, that file did not reach this build."
		else
			echo "$me: $bundle has a generated Info.plist — no custom build setting to check"
		fi
		continue
		;;
	unmapped | ambiguous | nosource | noplist)
		failures=$((failures + 1))
		case "$status" in
		unmapped) echo "::error::$me: $bundle is in the archive but no target in the build settings produces it — its source Info.plist is unknown, so its build-time keys cannot be verified" ;;
		ambiguous) echo "::error::$me: more than one target produces $(basename "$bundle"), from different source Info.plists — cannot tell which one $bundle was built from" ;;
		nosource) echo "::error::$me: $bundle's source Info.plist $path does not exist" ;;
		noplist) echo "::error::$me: $bundle has no Info.plist in the archive" ;;
		esac
		continue
		;;
	esac

	checked=$((checked + 1))
	covered="$covered$var "
	kind=undeclared
	glob=""
	if idx=$(declared_index "$var"); then
		kind=declared
		glob=${globs[$idx]}
	fi
	where="$bundle/Info.plist at $path"
	note=""
	if [ "$status" = combined ]; then
		note=" (checked together with $partners: adjacent references cannot be told apart, so no shape or example check applies)"
	fi

	reason=""
	case "$status" in
	absent) reason="is referenced by the source Info.plist of $bundle at $path but that entry is missing from the built one — the source resolved here is not the plist that was built" ;;
	nonstring) reason="reached $where as a non-string value" ;;
	shape) reason="cannot be isolated: the built value in $where does not have its source's shape" ;;
	combined)
		if r=$(placeholder_reason "$value"); then
			reason="$r in $where$note"
		fi
		;;
	ok)
		if r=$(placeholder_reason "$value" "$(example_for "$var")"); then
			reason="$r in $where"
		elif [ -n "$glob" ]; then
			# shellcheck disable=SC2254 # the glob is deliberately unquoted, and validated above
			case "$value" in
			$glob) ;;
			*) reason="does not match its declared shape $glob in $where" ;;
			esac
		fi
		;;
	*) fail "internal: unknown record status for $var" ;;
	esac

	if [ -z "$reason" ]; then
		echo "$me: ok — $var ($kind) reached $where$note"
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

echo "$me: checked $checked build-setting reference(s) across $app_name and the bundles it embeds; $failures failed"
if [ "$failures" -gt 0 ]; then
	echo "$me: the archive carries keys that are empty, unexpanded or placeholders, or bundles this check could not read."
	echo "$me: It would build, sign, upload and pass review as a non-functional app, so the release stops here, before anything is uploaded."
	exit 1
fi
