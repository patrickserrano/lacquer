#!/usr/bin/env bash
# Deploy the composed documentation site to Cloudflare Workers Static Assets.
#
#   deploy-docs-cloudflare.sh
#
# This does NOT build anything. `gh-pages` is already the assembly point — each
# stack's docs workflow writes its own subdirectory there and the root index is
# regenerated from what is present (see publish-docs.sh) — so the composed tree
# is exactly what should be served. Cloudflare is a second *target* for that same
# artifact, not a second pipeline.
#
# WHY WORKERS AND NOT PAGES: Cloudflare's own guidance is that Pages continues to
# be supported but "all investment, optimizations, and feature work" now goes to
# Workers, which serves static assets and costs the same. New projects should not
# be started on Pages.
#
# WHY NOT CLOUDFLARE DROP: Drop (July 2026) uploads a folder or zip with no
# account and is the obvious-looking fit, but it is drag-and-drop in the
# dashboard with no CLI or API, and the deployment expires after one hour unless
# a human claims it. It is a preview-sharing tool, not a publishing target.
#
# THE `<repo>/` NESTING IS LOAD-BEARING. DocC bakes DOCC_HOSTING_BASE_PATH into
# every asset URL at build time, and the iOS docs build sets it to `<repo>/swift`
# because GitHub Pages serves a project site under /<repo>/. Serving the same
# archive at the root of a Worker would leave every CSS and JS request pointing
# at /<repo>/swift/... and 404ing. Nesting the composed tree under <repo>/ here
# means ONE build serves both targets identically. A root redirect covers the
# resulting bare-domain visit.
#
# Requires: CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID, GITHUB_REPOSITORY, and
# GH_TOKEN (or GITHUB_TOKEN) to read the branch.
set -euo pipefail

: "${CLOUDFLARE_API_TOKEN:?CLOUDFLARE_API_TOKEN must be set}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID must be set}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"

TOKEN="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
: "${TOKEN:?GH_TOKEN or GITHUB_TOKEN must be set}"

# Pinned, not @latest: this runs with a token that can deploy Workers on the
# account, so an unpinned dependency would execute whatever was published most
# recently. Same rule every action in this repo follows. Bump deliberately.
WRANGLER_VERSION="4.118.0"

REPO="${GITHUB_REPOSITORY#*/}"
REMOTE="${PUBLISH_DOCS_REMOTE:-https://x-access-token:${TOKEN}@github.com/${GITHUB_REPOSITORY}.git}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

if ! git clone --depth 1 --branch gh-pages --quiet "$REMOTE" "$WORK/pages" 2>/dev/null; then
  echo "No gh-pages branch yet — nothing has been published, so there is nothing to deploy."
  exit 0
fi
rm -rf "$WORK/pages/.git"

if [ -z "$(find "$WORK/pages" -type f -print -quit)" ]; then
  echo "deploy-docs-cloudflare: gh-pages is empty — refusing to deploy an empty site" >&2
  exit 1
fi

mkdir -p "$WORK/_site/$REPO"
cp -R "$WORK/pages/." "$WORK/_site/$REPO/"

# A visitor to the bare Worker domain would otherwise get a 404, because
# everything real lives one level down. Meta-refresh rather than a redirect
# Worker so the deployment stays assets-only — no script, no compatibility
# surface, nothing to keep up to date.
cat > "$WORK/_site/index.html" <<EOF
<!doctype html>
<meta charset="utf-8">
<meta http-equiv="refresh" content="0; url=./${REPO}/">
<title>${REPO} — documentation</title>
<a href="./${REPO}/">Documentation</a>
EOF

# A project that needs a custom domain, a route, or headers commits its own
# config; otherwise this generated one is all an assets-only Worker needs.
if [ -f "docs.wrangler.jsonc" ]; then
  echo "Using the project's committed docs.wrangler.jsonc."
  cp docs.wrangler.jsonc "$WORK/wrangler.jsonc"
else
  cat > "$WORK/wrangler.jsonc" <<EOF
{
  "name": "${REPO}-docs",
  "compatibility_date": "2026-01-01",
  "assets": {
    "directory": "./_site",
    "not_found_handling": "404-page"
  }
}
EOF
fi

# DEPLOY_DOCS_DRY_RUN=1 stages and validates everything, then stops without
# uploading. Useful for checking what would ship before pointing a custom domain
# at it, and it is how this script is exercised without a Cloudflare account.
args=(deploy)
if [ "${DEPLOY_DOCS_DRY_RUN:-}" = "1" ]; then
  args+=(--dry-run)
  echo "DRY RUN — staging and validating only."
fi

echo "Deploying ${REPO} docs to Cloudflare Workers..."
(cd "$WORK" && npx --yes "wrangler@${WRANGLER_VERSION}" "${args[@]}")
