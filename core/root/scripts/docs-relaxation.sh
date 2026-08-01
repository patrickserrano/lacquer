#!/usr/bin/env bash
# Read this project's documentation relaxation from .lacquer.toml and print its
# state on stdout: `none`, `relaxed`, `expired`, or `malformed`.
#
#   docs-relaxation.sh [manifest] [today]
#
# The documentation standard is strict — every declaration carries a doc comment
# and the docs build clean — so a project that genuinely cannot comply yet needs
# the same escape hatch every other baseline key has: time-boxed and justified,
# never open-ended.
#
#   [baseline.relax]
#   documentation = { until = "2026-11-01", reason = "legacy Core/, #212" }
#
# `expired` is deliberately NOT the same as `none`: an expired relaxation is a
# hard failure, so debt cannot quietly become permanent policy by being ignored
# after its date passes.
#
# WHY SHELL AND NOT A TOML PARSER: this runs on a self-hosted macOS runner whose
# system python3 is 3.9 — tomllib landed in 3.11 — and adding a parser install to
# every docs job to read one line is not worth it. The shape accepted here is the
# inline-table form that lacquer itself documents and writes; anything else
# reports `malformed` rather than being silently treated as absent, because
# "silently absent" is exactly how a typo becomes an invisible exemption.
set -euo pipefail

MANIFEST="${1:-.lacquer.toml}"
TODAY="${2:-$(date -u +%Y-%m-%d)}"

if [ ! -f "$MANIFEST" ] || ! grep -q '^\[baseline\.relax\]' "$MANIFEST"; then
  echo none
  exit 0
fi

# The line within the [baseline.relax] table, stopping at the next table header
# so a `documentation = ...` key in some other section cannot be mistaken for one.
line=$(awk '
  /^\[baseline\.relax\][[:space:]]*$/ { inblock = 1; next }
  /^\[/                               { inblock = 0 }
  inblock && /^[[:space:]]*documentation[[:space:]]*=/ { print; exit }
' "$MANIFEST")

[ -n "$line" ] || { echo none; exit 0; }

until_date=$(printf '%s' "$line" | sed -n 's/.*until[[:space:]]*=[[:space:]]*"\([0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\}\)".*/\1/p')
reason=$(printf '%s' "$line" | sed -n 's/.*reason[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p')

# Both fields are required. A relaxation with no expiry is permanent policy, and
# one with no reason is undocumented debt — neither is allowed to pass as valid.
if [ -z "$until_date" ] || [ -z "$reason" ]; then
  echo malformed
  exit 0
fi

# String comparison is correct for zero-padded ISO dates and needs no date(1)
# flags, which differ between BSD (macOS runners) and GNU (ubuntu runners).
if [ "$TODAY" \> "$until_date" ]; then
  echo expired
else
  echo relaxed
fi
