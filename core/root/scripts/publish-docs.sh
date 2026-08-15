#!/usr/bin/env bash
# Publish one stack's generated documentation into its own subdirectory of the
# `gh-pages` branch, then regenerate the root index from whatever subdirectories
# are present.
#
#   publish-docs.sh <subdir> <built-site-dir>
#
# WHY A BRANCH AND NOT actions/deploy-pages: deploy-pages replaces the *entire*
# site on every deploy. A repo with an iOS component and a web component has two
# docs workflows, and under deploy-pages whichever ran last would silently
# delete the other's docs. Writing only our own subdirectory composes instead:
# each stack owns one directory and never touches its siblings.
#
# The callers all share one `concurrency: group: docs-publish` so two stacks
# never push to gh-pages at the same time. The push is still retried below,
# because a group serializes runs of the *workflows that opt in*, and nothing
# stops a human pushing to gh-pages by hand.
#
# Requires: GH_TOKEN (or GITHUB_TOKEN) with contents:write, and GITHUB_REPOSITORY.
set -euo pipefail

SUBDIR="${1:?usage: publish-docs.sh <subdir> <built-site-dir>}"
SITE="${2:?usage: publish-docs.sh <subdir> <built-site-dir>}"

TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
: "${TOKEN:?GH_TOKEN or GITHUB_TOKEN must be set}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"

# Refuse a subdir that could escape the checkout. This value comes from a
# workflow, not a user, but it is interpolated into `rm -rf` below.
case "$SUBDIR" in
  */*|.*|"") echo "publish-docs: invalid subdir '$SUBDIR' (one path element, no leading dot)" >&2; exit 1 ;;
esac

if [ ! -d "$SITE" ]; then
  echo "publish-docs: '$SITE' is not a directory — nothing was built" >&2
  exit 1
fi
# A docs generator that "succeeds" while emitting nothing would otherwise
# publish an empty directory over a good one.
if [ -z "$(find "$SITE" -type f -print -quit)" ]; then
  echo "publish-docs: '$SITE' contains no files — refusing to publish an empty site" >&2
  exit 1
fi

SITE=$(cd "$SITE" && pwd)
# PUBLISH_DOCS_REMOTE is a test seam: it lets the script be exercised end to end
# against a local bare repo instead of GitHub. Workflows never set it.
REMOTE="${PUBLISH_DOCS_REMOTE:-https://x-access-token:${TOKEN}@github.com/${GITHUB_REPOSITORY}.git}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# First publish in a repo has no gh-pages branch yet: start an orphan.
if ! git clone --depth 1 --branch gh-pages --quiet "$REMOTE" "$WORK" 2>/dev/null; then
  echo "No gh-pages branch yet — creating one."
  git clone --depth 1 --quiet "$REMOTE" "$WORK"
  git -C "$WORK" checkout --quiet --orphan gh-pages
  git -C "$WORK" rm -rqf . >/dev/null 2>&1 || true
fi

rm -rf "${WORK:?}/${SUBDIR}"
mkdir -p "$WORK/$SUBDIR"
cp -R "$SITE/." "$WORK/$SUBDIR/"

# DocC archives contain directories whose names begin with an underscore, which
# GitHub Pages' Jekyll pipeline drops. Without this the published site 404s on
# its own assets.
touch "$WORK/.nojekyll"

# Rebuild the index from what is actually on the branch, so a stack that has not
# published yet is absent rather than listed as a broken link, and a stack that
# publishes later appears without anyone editing this.
{
  echo '<!doctype html>'
  echo '<meta charset="utf-8">'
  echo '<meta name="viewport" content="width=device-width,initial-scale=1">'
  printf '<title>%s — documentation</title>\n' "${GITHUB_REPOSITORY#*/}"
  echo '<style>'
  echo '  :root{color-scheme:light dark}'
  echo '  body{font:16px/1.6 ui-sans-serif,system-ui,-apple-system,sans-serif;'
  echo '       max-width:34rem;margin:12vh auto;padding:0 1.5rem}'
  echo '  h1{font-size:1.5rem;margin:0 0 1.5rem}'
  echo '  ul{list-style:none;padding:0} li{margin:.5rem 0}'
  echo '  a{text-decoration:none} a:hover{text-decoration:underline}'
  echo '  code{font-size:.9em;opacity:.7}'
  echo '</style>'
  printf '<h1>%s</h1>\n<ul>\n' "${GITHUB_REPOSITORY#*/}"
  for d in "$WORK"/*/; do
    name=$(basename "$d")
    [ "$name" = ".git" ] && continue
    printf '  <li><a href="./%s/">%s</a></li>\n' "$name" "$name"
  done
  echo '</ul>'
} > "$WORK/index.html"

# Record WHICH source commit these docs were built from, so a scheduled run can
# tell whether a rebuild is needed at all. The callers pass the last commit that
# touched their component, not GITHUB_SHA: HEAD moves on every unrelated change,
# so comparing against it would rebuild nightly regardless.
#
# Written inside the published subdir so it travels with the docs it describes,
# and so a stack that stops publishing takes its marker with it.
#
# BEFORE `git add -A`, and deliberately so. Written after it, the marker is never
# staged and never committed. It also has to be able to produce a commit ON ITS
# OWN: when the source moved but the generated docs are byte-identical, the
# "no changes" exit below would leave the marker pointing at the older commit and
# the scheduled build would rebuild the same output every night forever.
if [ -n "${DOCS_SOURCE_SHA:-}" ]; then
  printf '%s\n' "$DOCS_SOURCE_SHA" > "$WORK/$SUBDIR/.docs-source"
fi

git -C "$WORK" add -A
if git -C "$WORK" diff --cached --quiet; then
  echo "No documentation changes to publish."
  exit 0
fi

git -C "$WORK" \
  -c user.name="github-actions[bot]" \
  -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
  commit --quiet -m "docs: publish ${SUBDIR} docs for ${GITHUB_SHA:-HEAD}"

for attempt in 1 2 3; do
  if git -C "$WORK" push --quiet origin gh-pages; then
    echo "Published ${SUBDIR}/ to gh-pages (attempt ${attempt})."
    exit 0
  fi
  echo "Push rejected; rebasing onto gh-pages and retrying."
  git -C "$WORK" pull --rebase --quiet origin gh-pages
done

echo "publish-docs: could not push gh-pages after 3 attempts" >&2
exit 1
