#!/usr/bin/env bash
# Run a documentation check unless [baseline.relax] documentation is active.
#
#   docs-hook.sh <command> [args...]
#
# The CI Docs job honours that relaxation; the local hooks did not, which made
# LOCAL STRICTER THAN CI — a project with a valid, unexpired relaxation passed
# CI and could not commit or push. Local and CI disagreeing is the defect this
# fleet keeps finding, and it is no better when local is the harsher of the two.
#
# An EXPIRED or malformed relaxation is not a skip. It fails here exactly as it
# fails in CI, because the whole point of a time-boxed exemption is that time
# runs out.
set -euo pipefail

state=$(scripts/docs-relaxation.sh .lacquer.toml 2>/dev/null || echo none)

case "$state" in
  relaxed)
    echo "docs: relaxed by [baseline.relax] in .lacquer.toml — skipping $1"
    exit 0
    ;;
  expired)
    echo "docs: the [baseline.relax] documentation entry in .lacquer.toml has EXPIRED." >&2
    echo "      Document the component, or extend the relaxation deliberately." >&2
    exit 1
    ;;
  malformed)
    echo "docs: the [baseline.relax] documentation entry is malformed — both until and reason are required." >&2
    exit 1
    ;;
esac

exec "$@"
