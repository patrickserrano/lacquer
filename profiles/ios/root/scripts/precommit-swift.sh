#!/usr/bin/env bash
# precommit-swift.sh — fail-closed wrapper for the pre-commit Swift stages.
#
# Why this exists (momfriend, adopted into the lacquer):
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

# The hook's `files:` filter already restricts staged paths to the component;
# make them relative to it so the tools run from their config's directory and
# never scan DerivedData*. PREFIX is empty for a root-layout project, in which
# case every path is already relative and the loop is a no-op.
PREFIX="{{COMPONENT_PREFIX}}"
REL=()
for f in "$@"; do
  case "$f" in
    "$PREFIX"*) REL+=("${f#"$PREFIX"}") ;;
    *)
      echo "precommit-swift.sh: unexpected staged path '$f' (the hook filter should pass only ${PREFIX:-repo-root} files) — failing closed." >&2
      exit 1
      ;;
  esac
done

# Explicit `if`, not `[ -n ... ] && cd ...`: under `set -e` the exit status of a
# failing AND-list is easy to reason about wrongly, and a root-layout project
# takes the false branch on every commit.
if [ -n "$PREFIX" ]; then
  cd "$PREFIX"
fi

case "$TOOL" in
  swiftformat)
    # Formats in place; pre-commit fails the hook if files were modified,
    # leaving the formatted result in the working tree to re-stage.
    "$BIN" --config "$CONFIG" "${REL[@]}"
    ;;
  swiftlint|swiftlint-docs)
    # --strict, matching CI exactly: line_length, file_length, type_body_length
    # and function_body_length are all WARNING severity, so without it they
    # print, pass, and then fail the PR.
    "$BIN" lint --strict --quiet --config "$CONFIG" "${REL[@]}"
    ;;
esac

# Positive proof-of-run (the hook is `verbose: true`, so this line is shown on
# every commit): confirms the stage executed and how many files it verified.
echo "$TOOL verified $# staged Swift file(s) via $BIN"
