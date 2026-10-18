#!/usr/bin/env bash
# The "Public names (private denylist)" step, kept out of ci.yml so its decision
# logic can be driven from a Go test without GitHub.
#
# Inputs, all from the environment (the secret never appears in argv or in
# interpolated `run:` text):
#   ACTOR        github.actor. Not github.triggering_actor: a re-run uses the
#                privileges of github.actor, so a human re-running a Dependabot
#                PR's workflow still has no Actions secrets, and the skip below
#                must still apply.
#   DENYLIST     the secret's value; empty when the secret is not readable.
#   RUNNER_TEMP  where the list is written (mode 600) for the test to read.
#
# Outcomes:
#   DENYLIST empty, ACTOR is dependabot[bot]  -> notice, exit 0 (cannot read the secret)
#   DENYLIST empty, anyone else               -> ::error:: naming the secret, exit 1
#   DENYLIST set                              -> run the scan; it must report PASS
#                                                and a non-zero scanned-file count
set -euo pipefail

if [ -z "${DENYLIST:-}" ]; then
  if [ "${ACTOR:-}" = "dependabot[bot]" ]; then
    echo "skipped: no denylist secret (Dependabot-triggered runs cannot read Actions secrets)"
    echo "::notice::skipped: no denylist secret (Dependabot-triggered run)"
    exit 0
  fi
  echo "::error::The LACQUER_PUBLIC_DENYLIST secret is missing or empty, so the public-names check could not scan anything. Only Dependabot-triggered runs may skip it. Restore the secret."
  exit 1
fi

umask 077
list="$RUNNER_TEMP/denylist.txt"
printf '%s\n' "$DENYLIST" > "$list"
chmod 600 "$list"
out="$RUNNER_TEMP/denylist-test.log"
LACQUER_PUBLIC_DENYLIST="$list" LACQUER_PUBLIC_DENYLIST_REQUIRED=1 \
  go test ./internal/shipped -run '^TestShippedContentNamesNoPrivateConsumer$' -count=1 -v | tee "$out"
if ! grep -q -- '--- PASS: TestShippedContentNamesNoPrivateConsumer' "$out"; then
  echo "::error::The denylist test did not report PASS (skipped, or matched no test). A run that holds the secret must run the check."
  exit 1
fi
if ! grep -Eq -- 'public-names: scanned [1-9][0-9]* tracked files' "$out"; then
  echo "::error::The denylist test passed without reporting a non-zero scanned-file count. A check that cannot show it scanned something is not a check."
  exit 1
fi
