package tokens

import "strings"

// LacquerReleaseDownload supplies the same bounded download function to every
// workflow installer. Callers retain their pinned tag and checksum verification.
const LacquerReleaseDownload = "{{LACQUER_RELEASE_DOWNLOAD}}"

func releaseDownloadScript() string {
	return "          " + strings.ReplaceAll(strings.TrimSpace(releaseDownloadShell), "\n", "\n          ")
}

const releaseDownloadShell = `
# Five public attempts: at most 50s of transfer and 225s of backoff per file.
# Never retry a checksum failure: callers verify once, after both downloads.
download_release_file() {
  local file="$1" attempt delay=15 http rc api_url
  for attempt in 1 2 3 4 5; do
    if http=$(curl -fsSL --connect-timeout 5 --max-time 10 -w '%{http_code}' \
        -o "$RUNNER_TEMP/$file.part" "$base/$file"); then
      mv -f "$RUNNER_TEMP/$file.part" "$RUNNER_TEMP/$file" || return 1
      return 0
    else
      rc=$?
    fi
    echo "::warning::Downloading $file attempt $attempt/5 failed (HTTP $http, curl exit $rc)."
    # A bad pin or filename must fail immediately, without an API fallback.
    case "$http" in
      404) echo "::error::Release asset $file was not found at $LACQUER_TAG (HTTP 404)."; return 1 ;;
      429) ;;
      4??) echo "::error::Release download refused (HTTP $http)."; return 1 ;;
    esac
    if [ "$attempt" -lt 5 ]; then
      sleep "$delay" || return 1
      delay=$((delay * 2))
    fi
  done
  # Discard partial/stale files before the authenticated path writes its result.
  rm -f "$RUNNER_TEMP/$file.part" "$RUNNER_TEMP/$file" || return 1
  echo "::notice::Public download exhausted for $file; trying the authenticated release API."
  if [ -z "${GH_TOKEN:-}" ]; then
    echo "::error::GH_TOKEN is missing; the authenticated download cannot run."
    return 1
  fi
  if command -v gh >/dev/null 2>&1; then
    if perl -e 'alarm shift; exec @ARGV' 30 gh release download "$LACQUER_TAG" \
        -R patrickserrano/lacquer -p "$file" -D "$RUNNER_TEMP" --clobber; then
      return 0
    fi
  else
    # Hosted images include gh; self-hosted runners may not. Use the same API
    # with curl and Python's standard JSON parser, without installing a tool.
    if ! curl -fsSL --connect-timeout 5 --max-time 15 \
        -H "Authorization: Bearer $GH_TOKEN" -H 'Accept: application/vnd.github+json' \
        -o "$RUNNER_TEMP/release.json" \
        "https://api.github.com/repos/patrickserrano/lacquer/releases/tags/$LACQUER_TAG"; then
      echo "::error::Authenticated release metadata download failed."
      return 1
    fi
    if ! api_url=$(python3 -c 'import json, sys
assets = json.load(open(sys.argv[1]))["assets"]
matches = [a["url"] for a in assets if a["name"] == sys.argv[2]]
if len(matches) != 1:
    sys.exit("Expected exactly one release asset: " + sys.argv[2])
print(matches[0])' "$RUNNER_TEMP/release.json" "$file"); then
      return 1
    fi
    if curl -fsSL --connect-timeout 5 --max-time 15 \
        -H "Authorization: Bearer $GH_TOKEN" -H 'Accept: application/octet-stream' \
        -o "$RUNNER_TEMP/$file.part" "$api_url"; then
      mv -f "$RUNNER_TEMP/$file.part" "$RUNNER_TEMP/$file" || return 1
      return 0
    fi
  fi
  echo "::error::Authenticated release download failed for $file."
  return 1
}
`
