#!/usr/bin/env bash
# Build this component's DocC documentation into a directory ready for static
# hosting, and fail on any DocC warning.
#
#   build-docs.sh <output-dir> [hosting-base-path]
#
# Used by both the CI gate (ios-ci.yml, which builds and throws the result away)
# and the publisher (ios-docs.yml, which hands the output to
# scripts/publish-docs.sh). One copy so the two can never drift into validating
# something different from what gets published.
set -euo pipefail

OUT="${1:?usage: build-docs.sh <output-dir> [hosting-base-path]}"
BASE_PATH="${2:-}"

DERIVED="{{COMPONENT_PREFIX}}DerivedData-Docs"

# A gitignored Secrets.xcconfig may be project.yml's base config; a clean
# checkout has none, and the build fails before compiling. Same seed the build
# and test jobs do.
# Seed every example beside itself — see ios-ci.yml for why the single-path copy
# was wrong (kit's project reads a nested Secrets.xcconfig the root copy missed).
find {{COMPONENT_PREFIX}}. -name 'Secrets.xcconfig.example' \
  -not -path '*/DerivedData*' -not -path '*/build/*' -not -path '*/.build/*' \
  -print0 2>/dev/null | while IFS= read -r -d '' ex; do
    target="${ex%.example}"
    [ -f "$target" ] || cp "$ex" "$target"
  done

# DOCC_MINIMUM_ACCESS_LEVEL is the setting that makes this worth running at all.
# DocC extracts `public` and above by DEFAULT, and an app target's code is
# `internal` by default — so without this an app builds a documentation archive
# containing essentially nothing, succeeds, and reports as a pass. Verified: the
# same build emits 3 symbols at the default and 6 with `internal`.
#
# OTHER_DOCC_FLAGS is the setting that makes --warnings-as-errors take effect.
# `DOCC_FLAGS` is ALSO a real build setting name (it is in Xcode's own xcspecs),
# and it is silently ignored — a docbuild with `DOCC_FLAGS=--warnings-as-errors`
# and a deliberately broken symbol link still exits 0. Verified on Xcode 26.6:
# OTHER_DOCC_FLAGS exits 65 on the same input. Do not "simplify" this to the
# shorter name; it turns the gate off without turning the job red.
args=(
  -project "{{XCODEPROJ}}"
  -scheme "{{SCHEME}}"
  -destination 'generic/platform=iOS'
  -derivedDataPath "$DERIVED"
  DOCC_MINIMUM_ACCESS_LEVEL=internal
  DOCC_TRANSFORM_FOR_STATIC_HOSTING=YES
  CODE_SIGNING_REQUIRED=NO
  CODE_SIGNING_ALLOWED=NO
)

# NOT OTHER_DOCC_FLAGS=--warnings-as-errors. docbuild documents every target in
# the scheme, INCLUDING third-party SwiftPM dependencies, and --warnings-as-errors
# applies to all of them. port-of-entry's docs build failed on RevenueCat and
# Sentry — code the project cannot edit and should not be judged on. The gate is
# applied below instead, scoped to this project's own sources.
# Only set when publishing: the base path is baked into every asset URL, so a
# locally-built archive should not carry the deploy site's prefix.
if [ -n "$BASE_PATH" ]; then
  args+=("DOCC_HOSTING_BASE_PATH=$BASE_PATH")
fi

echo "Building documentation..."
rm -rf "$DERIVED/Build"
LOG=$(mktemp)
trap 'rm -f "$LOG"' EXIT
xcodebuild docbuild "${args[@]}" 2>&1 | tee "$LOG"
build_status=${PIPESTATUS[0]}
if [ "$build_status" -ne 0 ]; then
  echo "::error::docbuild failed."
  exit "$build_status"
fi

# Fail on DocC warnings from OUR sources only. Dependency checkouts live under
# the derived-data SourcePackages dir, so excluding that path is what separates
# "our doc comment links to a symbol that does not exist" from "RevenueCat has a
# malformed comment".
own=$(grep -E '^/.*\.swift:[0-9]+:[0-9]+: warning:' "$LOG" \
  | grep -v "/$DERIVED/" | grep -v '/SourcePackages/' | sort -u || true)
if [ -n "$own" ]; then
  echo "::error::DocC reported warnings in this project's own sources:"
  printf '%s\n' "$own" | head -40
  exit 1
fi

# Collect the archives the build produced. A scheme that builds frameworks as
# well as the app yields one per documented target.
archives=()
while IFS= read -r a; do archives+=("$a"); done < <(
  find "$DERIVED/Build/Products" -maxdepth 3 -name '*.doccarchive' -type d 2>/dev/null | sort
)

if [ "${#archives[@]}" -eq 0 ]; then
  echo "::error::docbuild produced no .doccarchive. The scheme builds nothing DocC can document, or DOCC_MINIMUM_ACCESS_LEVEL was overridden — either way this check is not verifying anything."
  exit 1
fi

# Prefer the archive matching the scheme; a multi-target scheme otherwise has no
# deterministic "main" one.
chosen="${archives[0]}"
for a in "${archives[@]}"; do
  if [ "$(basename "$a" .doccarchive)" = "{{SCHEME}}" ]; then
    chosen="$a"
  fi
done

if [ "${#archives[@]}" -gt 1 ]; then
  # Said out loud rather than dropped silently: DOCC_HOSTING_BASE_PATH is one
  # value for the whole build, so a second archive published beside this one
  # would render with the wrong asset prefix and 404 on its own CSS. Publishing
  # one correctly beats publishing two where one is quietly broken.
  for a in "${archives[@]}"; do
    [ "$a" = "$chosen" ] && continue
    echo "::warning::Not publishing $(basename "$a") — only one archive can share a hosting base path. Give it its own docs job if it needs a site."
  done
fi

echo "Publishing archive: $(basename "$chosen")"
rm -rf "$OUT"
mkdir -p "$OUT"
cp -R "$chosen/." "$OUT/"

# Guard against a "successful" build that staged nothing renderable — the same
# silent-pass shape the SwiftLint and baseline jobs guard against.
if [ ! -f "$OUT/index.html" ]; then
  echo "::error::The staged documentation has no index.html — DOCC_TRANSFORM_FOR_STATIC_HOSTING did not take effect, so the published site would not load."
  exit 1
fi

echo "Documentation staged in $OUT"
