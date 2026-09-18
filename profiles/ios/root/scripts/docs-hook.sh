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
#
# The script and the manifest are resolved from the REPOSITORY root. pre-commit
# runs this from there already, but the relaxation lives at the root whichever
# directory the hook is run from. They used to be read with
# `2>/dev/null || echo none`, which turned a missing script, a missing manifest
# or a failing script into "not relaxed" — a correctly dated relaxation silently
# ignored, and indistinguishable from "read the manifest, found nothing". A
# missing input is a broken install, so it fails and names the file (#387 fixed
# the same swallow in the web and supabase lefthook commands).
set -euo pipefail

if ! top=$(git rev-parse --show-toplevel); then
  echo "docs: not inside a git repository, so [baseline.relax] cannot be read." >&2
  exit 1
fi
for f in scripts/docs-relaxation.sh .lacquer.toml; do
  if [ ! -f "$top/$f" ]; then
    echo "docs: $f is missing from the repository root ($top), so [baseline.relax] cannot be read." >&2
    echo "      Every lacquer-managed repository has it; restore it (lacquer sync writes the script)." >&2
    exit 1
  fi
done
if ! state=$(cd "$top" && scripts/docs-relaxation.sh .lacquer.toml); then
  echo "docs: scripts/docs-relaxation.sh failed, so [baseline.relax] cannot be read." >&2
  exit 1
fi

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
