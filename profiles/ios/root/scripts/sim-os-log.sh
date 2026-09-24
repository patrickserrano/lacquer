#!/usr/bin/env bash
# sim-os-log.sh
#
# Reads os_log from ONE simulator you name, read-only, over a bounded window.
#
# Why this exists: flowdeck's log stream carries stdout only, so the diagnostics
# SwiftUI and CoreData write to os_log never reach it. This is the sanctioned,
# read-only exception for reading them from the simulator YOUR run owns.
#
# Usage: scripts/sim-os-log.sh <udid> [--last 5m] [--predicate '<pred>' | --subsystem <id>] [--style compact]
#
#   <udid>         the run's own simulator (get it from `flowdeck simulator list`).
#                  Never `booted`, a name, or a device you did not create/choose.
#   --last N       window, e.g. 90s, 5m, 2h, 1d. Always applied; default 5m.
#   --predicate P  an os_log predicate, passed to `log show` as one argument.
#   --subsystem S  shorthand for --predicate 'subsystem == "S"'.
#   --style S      compact (default), default, syslog, json, ndjson.
#
# It runs exactly one command, `xcrun simctl spawn <udid> log show ...`, and prints
# it to stderr before running it (stdout stays pure log output) so a PR can
# disclose the read. It refuses everything else — see die() calls below.
# Exit status is `log show`'s own; a refusal exits 2.

set -uo pipefail

die() {
  echo "sim-os-log: refused: $*" >&2
  echo "usage: sim-os-log.sh <udid> [--last 5m] [--predicate '<pred>' | --subsystem <id>] [--style compact]" >&2
  exit 2
}

udid="${1-}"
[ "$#" -gt 0 ] && shift

[ -n "$udid" ] || die "missing udid — pass the run's own simulator UDID (flowdeck simulator list)"
[ "$udid" != "booted" ] || die "'booted' names whichever simulator the host has up, not the one this run owns — pass its udid"
# A UUID and nothing else: this is what keeps a flag, a name, or a subcommand
# from ever reaching `simctl` in the udid position.
[[ "$udid" =~ ^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$ ]] \
  || die "'$udid' is not a udid — expected 8-4-4-4-12 hex, from flowdeck simulator list"
udid="$(printf '%s' "$udid" | tr '[:lower:]' '[:upper:]')"

last="5m"
style="compact"
predicate=""
subsystem=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --last)
      [ "$#" -ge 2 ] || die "--last needs a window, e.g. 5m"
      [[ "$2" =~ ^[1-9][0-9]*[smhd]$ ]] || die "--last '$2' is not a bounded window — use a number and one of s, m, h, d (e.g. 5m)"
      last="$2"; shift 2 ;;
    --predicate)
      [ "$#" -ge 2 ] || die "--predicate needs a value"
      [ -n "$2" ] || die "--predicate must not be empty"
      predicate="$2"; shift 2 ;;
    --subsystem)
      [ "$#" -ge 2 ] || die "--subsystem needs a value"
      [[ "$2" =~ ^[A-Za-z0-9._-]+$ ]] || die "--subsystem '$2' is not a subsystem identifier"
      subsystem="$2"; shift 2 ;;
    --style)
      [ "$#" -ge 2 ] || die "--style needs a value"
      case "$2" in
        compact|default|syslog|json|ndjson) style="$2"; shift 2 ;;
        *) die "--style '$2' is not one of compact, default, syslog, json, ndjson" ;;
      esac ;;
    *) die "'$1' is not an accepted argument — this script only reads: <udid> [--last] [--predicate|--subsystem] [--style]" ;;
  esac
done

if [ -n "$predicate" ] && [ -n "$subsystem" ]; then
  die "--predicate and --subsystem together is ambiguous — give one (--subsystem is shorthand for a predicate)"
fi
[ -z "$subsystem" ] || predicate="subsystem == \"$subsystem\""

# The device must be one THIS user's CoreSimulator knows. The udid is already
# validated as hex-and-dashes, so it is safe inside the pattern.
devices="$(xcrun simctl list devices -j)" || die "could not list simulators (xcrun simctl list devices -j failed)"
# A here-string, not a pipe: under pipefail, grep -q exiting on its first match
# can SIGPIPE a printf still writing a large device list and read as "not found".
grep -Eiq "\"udid\"[[:space:]]*:[[:space:]]*\"$udid\"" <<<"$devices" \
  || die "$udid is not a simulator on this machine for this user (xcrun simctl list devices -j)"

cmd=(xcrun simctl spawn "$udid" log show --last "$last" --style "$style" --info)
[ -z "$predicate" ] || cmd+=(--predicate "$predicate")

printf 'sim-os-log: running:' >&2
printf ' %q' "${cmd[@]}" >&2
printf '\n' >&2

"${cmd[@]}"
