#!/usr/bin/env bash
# swiftformat-pinned.sh — run (or locate) the SwiftFormat this project pins.
#
# Why this exists: Homebrew has no versioned swiftformat formula, so a routine
# `brew upgrade` on a shared runner moved every iOS project to a release with new
# default rules, and Lint failed on unchanged main branches. CI, the pre-commit
# hook, the editor hook, `lacquer fix` and `lacquer doctor` all go through this
# one script, so they cannot disagree about which SwiftFormat they are running.
#
# What it does:
#   * Installs the pinned release's swiftformat.zip into a per-version cache
#     directory, verifies the DOWNLOAD against the pinned sha256 before it is
#     unpacked, and refuses on a mismatch. Nothing is run that was not verified.
#   * Reuses that directory only while the zip and the binary both still match
#     the checksums recorded at install. A cache entry that changed is not trusted.
#   * Asks the binary for its version and refuses unless it is the pinned one:
#       SwiftFormat: runner binary reports X, repo pins Y
#   * Never falls back to a `swiftformat` on PATH or in Homebrew. A check that
#     cannot run the pinned version fails; it does not run a different one.
#
# It uses only curl, unzip and shasum from /usr/bin, so it works from an agent
# shell that lacks /opt/homebrew/bin.
#
# Usage:
#   scripts/swiftformat-pinned.sh --path          print the verified binary's path
#   scripts/swiftformat-pinned.sh <args...>       run it with <args> (exec)
#
# The pin below is rendered from [project].swiftformat_version in .lacquer.toml
# (default: the version the lacquer ships). The SWIFTFORMAT_PIN_* variables exist
# so CI can state the pin on the step that installs it and so tests can serve a
# release from a local file; the checksum is verified whichever URL is used.
#
# Must stay bash-3.2 compatible (macOS /bin/bash).

set -euo pipefail

PIN_VERSION="${SWIFTFORMAT_PIN_VERSION:-{{IOS_SWIFTFORMAT_VERSION}}}"
PIN_SHA256="${SWIFTFORMAT_PIN_SHA256:-{{IOS_SWIFTFORMAT_SHA256}}}"
URL="${SWIFTFORMAT_PIN_URL:-https://github.com/nicklockwood/SwiftFormat/releases/download/$PIN_VERSION/swiftformat.zip}"

CACHE_ROOT="${SWIFTFORMAT_PIN_CACHE:-${RUNNER_TOOL_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/lacquer}}"
DIR="$CACHE_ROOT/swiftformat/$PIN_VERSION"
BIN="$DIR/swiftformat"

# GitHub renders ::error:: as an annotation; a terminal gets a plain prefix.
fail() {
  if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
    echo "::error::$*" >&2
  else
    echo "error: $*" >&2
  fi
  exit 1
}

sha_of() { shasum -a 256 "$1" | cut -d' ' -f1; }

# A cached install is trusted only if both recorded checksums still hold.
cache_is_good() {
  [ -x "$BIN" ] && [ -f "$DIR/swiftformat.zip" ] && [ -f "$DIR/swiftformat.sha256" ] || return 1
  [ "$(sha_of "$DIR/swiftformat.zip")" = "$PIN_SHA256" ] || return 1
  [ "$(sha_of "$BIN")" = "$(cat "$DIR/swiftformat.sha256")" ] || return 1
}

install() {
  local tmp
  mkdir -p "$CACHE_ROOT/swiftformat"
  tmp="$(mktemp -d "$CACHE_ROOT/swiftformat/.install.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT
  if ! curl -fsSL --retry 3 --connect-timeout 20 -o "$tmp/swiftformat.zip" "$URL"; then
    fail "SwiftFormat: could not download $URL"
  fi
  local got
  got="$(sha_of "$tmp/swiftformat.zip")"
  if [ "$got" != "$PIN_SHA256" ]; then
    fail "SwiftFormat: sha256 mismatch for $PIN_VERSION: downloaded $got, repo pins $PIN_SHA256. Refusing to run an unverified binary."
  fi
  unzip -q -o "$tmp/swiftformat.zip" -d "$tmp" swiftformat || fail "SwiftFormat: $URL does not contain a swiftformat binary"
  chmod +x "$tmp/swiftformat"
  sha_of "$tmp/swiftformat" > "$tmp/swiftformat.sha256"
  # Replace any stale or corrupt entry. mv of a directory is atomic, so a
  # concurrent job sees either the whole install or none of it.
  rm -rf "$DIR"
  mkdir -p "$(dirname "$DIR")"
  if ! mv "$tmp" "$DIR" 2>/dev/null; then
    cache_is_good || fail "SwiftFormat: could not place the verified install at $DIR"
  fi
  trap - EXIT
}

if ! cache_is_good; then
  install
fi

have="$("$BIN" --version 2>/dev/null || true)"
if [ "$have" != "$PIN_VERSION" ]; then
  fail "SwiftFormat: runner binary reports ${have:-nothing}, repo pins $PIN_VERSION"
fi

if [ "${1:-}" = "--path" ] && [ "$#" -eq 1 ]; then
  printf '%s\n' "$BIN"
  exit 0
fi
exec "$BIN" "$@"
