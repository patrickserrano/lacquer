#!/usr/bin/env bash
# check-model-defaults.sh
#
# Verifies that every non-optional stored property inside an @Model class
# declares an inline default value (= <expr>). SwiftData + CloudKit requires
# this: a missing default makes NSPersistentCloudKitContainer refuse to load the
# store at runtime (Code=134060) — a crash that unit tests on a non-CloudKit
# store won't catch. This pre-commit/CI guard catches it at author time.
#
# Usage: bash scripts/check-model-defaults.sh [source-dir]   (default: repo root)
# Exits 0 if all @Model classes comply, 1 on any violation.

set -euo pipefail

SOURCE_DIR="${1:-.}"
VIOLATIONS=0

# Find @Model Swift files, skipping build output and vendored dependency sources
# (whose models are not ours to fix).
mapfile -t MODEL_FILES < <(grep -rl "@Model" "$SOURCE_DIR" --include="*.swift" \
  --exclude-dir=DerivedData --exclude-dir='DerivedData-*' \
  --exclude-dir=.build --exclude-dir=Pods --exclude-dir=Carthage \
  --exclude-dir=.git 2>/dev/null || true)

if [ ${#MODEL_FILES[@]} -eq 0 ]; then
  echo "No @Model classes found under $SOURCE_DIR"
  exit 0
fi

for file in "${MODEL_FILES[@]}"; do
  in_model=0
  brace_depth=0
  line_num=0

  while IFS= read -r line; do
    line_num=$((line_num + 1))

    # @Model annotation on its own line opens model context for the next braces.
    if echo "$line" | grep -qE '^\s*@Model\s*$'; then
      in_model=1
      brace_depth=0
      continue
    fi

    if [ "$in_model" -eq 1 ]; then
      opens=$(echo "$line" | tr -cd '{' | wc -c)
      closes=$(echo "$line" | tr -cd '}' | wc -c)
      brace_depth=$((brace_depth + opens - closes))

      # Leave model scope when its outer brace closes.
      if [ "$brace_depth" -le 0 ] && [ "$opens" -eq 0 ] && echo "$line" | grep -q '}'; then
        in_model=0
        continue
      fi

      # Inspect only direct stored properties (depth 1 = top level of the model).
      if [ "$brace_depth" -eq 1 ]; then
        # var <name>: <NonOptionalType> ... with no trailing `?`, not computed
        # ({), not static/class, not a `let`.
        if echo "$line" | grep -qE '^\s*var [a-zA-Z_][a-zA-Z0-9_]*\s*:\s*[A-Za-z\[\(]' && \
           ! echo "$line" | grep -qE '\?' && \
           ! echo "$line" | grep -q '{' && \
           ! echo "$line" | grep -qE '^\s*(private\s+)?(static|class)\s+var' && \
           ! echo "$line" | grep -qE '^\s*let\s+'; then
          if ! echo "$line" | grep -q '='; then
            echo "VIOLATION $file:$line_num — non-optional stored property missing inline default:"
            echo "  $line"
            VIOLATIONS=$((VIOLATIONS + 1))
          fi
        fi
      fi
    fi
  done < "$file"
done

if [ "$VIOLATIONS" -gt 0 ]; then
  echo ""
  echo "Found $VIOLATIONS @Model stored propert$([ "$VIOLATIONS" -eq 1 ] && echo 'y' || echo 'ies') missing inline defaults."
  echo "SwiftData + CloudKit requires every non-optional @Model stored property to declare = <value>."
  exit 1
fi

echo "All @Model stored properties have inline defaults."
