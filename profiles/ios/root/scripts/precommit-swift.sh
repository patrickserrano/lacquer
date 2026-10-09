#!/usr/bin/env bash
# precommit-swift.sh — fail-closed wrapper for the pre-commit Swift stages.
#
# Why this exists (bravoapp, adopted into the lacquer):
#   * Agent shells often lack /opt/homebrew/bin on PATH, so `swiftformat` /
#     `swiftlint` resolve as "not found". This wrapper resolves the tool from
#     well-known Homebrew locations itself, and ERRORS (blocking the commit)
#     when the tool genuinely cannot be found. A check that cannot run must
#     block, never pass.
#   * pre-commit invokes this with the staged .swift file list as arguments.
#     Zero arguments means the file list could not be determined — refuse to
#     report success for a check that verified nothing (fail closed).
#   * The SwiftLint/SwiftFormat configs live in the component directory, which
#     is the authoritative working directory (running from the repo root after a
#     build reports phantom violations in generated sources under DerivedData*).
#     We cd into it and pass paths relative to it, so only the staged files are
#     linted.
#   * A project can declare more than one Swift component (#522 U4): the app,
#     plus `[[component]] stack = "ios"` directories beside it, each with its own
#     .swiftlint.yml. The `swiftlint` hook has no `files:` filter, so every
#     staged .swift arrives here; each is linted from inside the deepest
#     component holding it. One under no component is a stray: it warns until
#     the gate date below, then blocks. `swiftformat` and `swiftlint-docs` keep
#     their filter, because their configs exist only in the app component.
#
# Usage: scripts/precommit-swift.sh <swiftformat|swiftlint|swiftlint-docs> <staged files...>
# Must stay bash-3.2 compatible (macOS /bin/bash): no mapfile, no ${var,,}.

set -euo pipefail

TOOL="${1:?usage: precommit-swift.sh <swiftformat|swiftlint|swiftlint-docs> <staged .swift files...>}"
shift

# swiftlint-docs is the same binary against the documentation config; keeping it
# a separate stage name means the two halves report independently.
BIN_NAME="$TOOL"
CONFIG=".swiftlint.yml"
case "$TOOL" in
  swiftlint-docs) BIN_NAME="swiftlint"; CONFIG=".swiftlint-docs.yml" ;;
  swiftformat)    BIN_NAME="swiftformat"; CONFIG=".swiftformat" ;;
esac

case "$TOOL" in
  swiftformat|swiftlint|swiftlint-docs) ;;
  *)
    echo "precommit-swift.sh: unknown tool '$TOOL' (expected swiftformat or swiftlint)" >&2
    exit 1
    ;;
esac

# Fail closed on an empty file list. pre-commit only runs this hook when its
# staged-file filter matched at least one file, so an empty argv here means
# file-list plumbing is broken — block the commit rather than "pass".
if [ "$#" -eq 0 ]; then
  echo "precommit-swift.sh: $TOOL received no staged Swift files — refusing to report success for a check that verified nothing (fail closed)." >&2
  exit 1
fi

# Resolve the tool without depending on the caller's PATH.
BIN=""
if command -v "$BIN_NAME" >/dev/null 2>&1; then
  BIN="$(command -v "$BIN_NAME")"
else
  for dir in /opt/homebrew/bin /usr/local/bin; do
    if [ -x "$dir/$BIN_NAME" ]; then
      BIN="$dir/$BIN_NAME"
      break
    fi
  done
fi
if [ -z "$BIN" ]; then
  echo "precommit-swift.sh: $BIN_NAME not found on PATH, /opt/homebrew/bin, or /usr/local/bin. Install it (brew install $BIN_NAME). Failing closed: the commit is blocked because this check could not run." >&2
  exit 1
fi

# The manifest's Swift components, the app first, one per line, rendered by
# `lacquer sync` from .lacquer.toml. Paths are plain names (the manifest
# refuses anything else), so word-splitting them below is safe.
COMPONENTS="{{IOS_SWIFT_COMPONENTS}}"
# From this day (UTC) a staged file under no component blocks the commit.
# Before it, the file is reported and left unlinted, exactly as CI's Lint job
# treats it. Rendered from the one constant that step and `lacquer audit` read,
# so the three switch on the same day.
GATE_FROM="{{IOS_SWIFT_GATE_FROM}}"

# swiftformat and swiftlint-docs reach the app component only: their configs
# exist nowhere else, and their hooks' `files:` filter passes nothing else.
case "$TOOL" in
  swiftlint) CANDIDATES="$COMPONENTS" ;;
  *) CANDIDATES="$(printf '%s\n' "$COMPONENTS" | sed -n 1p)" ;;
esac
if [ -z "$CANDIDATES" ]; then
  echo "precommit-swift.sh: no Swift component is declared, so there is nothing to lint these files against (failing closed). Re-run \`lacquer sync\`." >&2
  exit 1
fi
APP="$(printf '%s\n' "$CANDIDATES" | sed -n 1p)"

# component_for PATH prints the deepest candidate component holding PATH, or
# nothing. A component is a directory: `ios` does not hold `ios-tools/x.swift`.
component_for() {
  local f="$1" best="" bestlen=-1 c len
  for c in $CANDIDATES; do
    if [ "$c" = "." ]; then
      len=0
    else
      case "$f" in
        "$c"/*) len=${#c} ;;
        *) continue ;;
      esac
    fi
    if [ "$len" -gt "$bestlen" ]; then
      best="$c"
      bestlen=$len
    fi
  done
  if [ "$bestlen" -ge 0 ]; then
    printf '%s\n' "$best"
  fi
}

n_stray=0
stray_list=""
for f in "$@"; do
  if [ -z "$(component_for "$f")" ]; then
    n_stray=$((n_stray + 1))
    stray_list="$stray_list
  $f"
  fi
done
if [ "$n_stray" -gt 0 ]; then
  if [ "$TOOL" != "swiftlint" ]; then
    echo "precommit-swift.sh: unexpected staged path(s) outside $APP (the hook filter should pass only $APP files) — failing closed:$stray_list" >&2
    exit 1
  fi
  msg="precommit-swift.sh: $n_stray staged Swift file(s) under no declared Swift component, so nothing lints them:$stray_list
Declare the directory in .lacquer.toml as [[component]] path = \"<dir>\" stack = \"ios\" and commit a .swiftlint.yml in it, or move the files under a component. \`lacquer swift-components --check\` prints the exact block."
  if [ "$(date -u +%Y-%m-%d)" \< "$GATE_FROM" ]; then
    echo "$msg
Not blocking yet: from $GATE_FROM this blocks the commit, as it fails CI's Lint job." >&2
  else
    echo "$msg
Blocking since $GATE_FROM (failing closed)." >&2
    exit 1
  fi
fi

# One run per component with staged files, from inside it, so its own config
# applies and DerivedData* is never scanned. Every component runs even after one
# fails, so a commit reports all of its violations at once.
status=0
handed=0
for c in $CANDIDATES; do
  REL=()
  for f in "$@"; do
    if [ "$(component_for "$f")" = "$c" ]; then
      if [ "$c" = "." ]; then
        REL+=("$f")
      else
        REL+=("${f#"$c"/}")
      fi
    fi
  done
  n=${#REL[@]}
  if [ "$n" -eq 0 ]; then
    continue
  fi
  # How the config and the component are named in output: unchanged for a
  # root-layout project, which is most of the fleet.
  if [ "$c" = "." ]; then
    cfg_label="$CONFIG"
    where=""
  else
    cfg_label="$c/$CONFIG"
    where=" in $c"
  fi
  if [ ! -f "$c/$CONFIG" ]; then
    echo "precommit-swift.sh: component $c declares stack ios but has no $CONFIG, so nothing would lint its $n staged file(s). Commit $c/$CONFIG (failing closed)." >&2
    exit 1
  fi
  handed=$((handed + n))

  case "$TOOL" in
    swiftformat)
      # Formats in place; pre-commit fails the hook if files were modified,
      # leaving the formatted result in the working tree to re-stage.
      #
      # No --force-exclude equivalent needed here: unlike SwiftLint, SwiftFormat
      # applies its `--exclude` config to explicitly-named paths the same way it
      # does during a directory scan (verified against $CONFIG's own `--exclude
      # DerivedData,DerivedData-*,.build,.swiftpm,**/Generated` line: an explicit
      # path under an excluded directory is reported as "N file(s) skipped" and
      # left untouched, exit 0). $CONFIG also excludes no test directories, so
      # there is no analogue of the swiftlint-docs bypass below to close.
      if (cd "$c" && "$BIN" --config "$CONFIG" "${REL[@]}") </dev/null; then
        echo "$TOOL: $n staged Swift file(s)$where passed to $BIN against $cfg_label (the tool's own --exclude config applies; see its output above for what was actually formatted)."
      else
        status=$?
      fi
      ;;
    swiftlint|swiftlint-docs)
      # --strict, matching CI exactly: line_length, file_length, type_body_length
      # and function_body_length are all WARNING severity, so without it they
      # print, pass, and then fail the PR.
      #
      # --force-exclude: SwiftLint applies a config's `excluded:` list only while
      # walking a directory, NOT to paths named explicitly on the command line —
      # and this hook always passes explicit staged paths. Without this flag,
      # swiftlint-docs (whose config excludes **/Tests and **/*Tests on purpose,
      # to avoid requiring filler doc comments on @Test methods) lints exactly
      # the files the config says not to.
      #
      # With --force-exclude, excluding every given path changes SwiftLint's exit
      # from "0 violations" to exit 1 with "No lintable files found at paths:
      # ...". That specific case is treated as success below — matched on BOTH
      # the exit code and the message text, so if SwiftLint ever reflows that
      # wording the commit is blocked loudly rather than silently passed. Any
      # other exit 1 (a real lint violation is also exit 1) must still fail with
      # SwiftLint's own output shown.
      #
      # When only SOME of the given paths are excluded, SwiftLint gives no
      # signal at all: it silently drops them and lints the rest, exit 0 — there
      # is no message to key on and no reliable way to recover an "N excluded"
      # count afterward. The closing line below does not invent one.
      if out=$(cd "$c" && "$BIN" lint --strict --quiet --force-exclude --config "$CONFIG" "${REL[@]}" 2>&1 </dev/null); then
        rc=0
      else
        rc=$?
      fi
      if [ "$rc" -eq 1 ] && printf '%s\n' "$out" | grep -q "No lintable files found"; then
        echo "$TOOL: all $n staged Swift file(s)$where are excluded by $cfg_label — nothing to lint (via $BIN)."
      elif [ "$rc" -ne 0 ]; then
        echo "$out" >&2
        status=$rc
      else
        if [ -n "$out" ]; then
          echo "$out"
        fi
        # Positive proof-of-run (the hook is `verbose: true`, so this line is
        # shown on every commit). Deliberately NOT "$TOOL verified $n staged
        # Swift file(s)": some of the paths handed to SwiftLint may have been
        # silently excluded by $CONFIG (see above), so claiming all were
        # verified would overclaim exactly the case this fix exists to close.
        echo "$TOOL: $n staged Swift file(s)$where checked against $cfg_label (config exclusions applied) via $BIN"
      fi
      ;;
  esac
done

# Every staged file was handed to exactly one run, or reported as a stray
# above. Anything else means the grouping dropped or doubled a file, and a check
# that skipped a file must not report success.
if [ $((handed + n_stray)) -ne "$#" ]; then
  echo "precommit-swift.sh: $# staged file(s) but $handed linted and $n_stray reported as stray — the component grouping lost a file (failing closed)." >&2
  exit 1
fi
exit "$status"
