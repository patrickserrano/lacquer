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
  local file="$1" attempt delay=15 http rc api_url headers
  local waited=0 wait_for retry_after retry_seconds
  headers="$RUNNER_TEMP/$file.headers"
  for attempt in 1 2 3 4 5; do
    rm -f "$headers" || return 1
    if http=$(curl -fsSL --connect-timeout 5 --max-time 10 -w '%{http_code}' \
        --dump-header "$headers" \
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
      403)
        # Redirect headers must not make a final permission denial retryable.
        if ! awk '
          /^HTTP\// { limited=0 }
          { line=tolower($0); sub(/\r$/, "", line) }
          line ~ /^x-ratelimit-remaining:[[:space:]]*0[[:space:]]*$/ { limited=1 }
          line ~ /^retry-after:/ { limited=1 }
          END { exit !limited }
        ' "$headers"; then
          echo "::error::Release download refused (HTTP 403, no rate-limit signal)."
          return 1
        fi ;;
      4??) echo "::error::Release download refused (HTTP $http)."; return 1 ;;
    esac
    if [ "$attempt" -lt 5 ]; then
      wait_for=$delay
      retry_after=""
      if [ -f "$headers" ]; then
        retry_after=$(awk '
          /^HTTP\// { value="" }
          tolower($0) ~ /^retry-after:/ {
            sub(/^[^:]*:[[:space:]]*/, ""); sub(/[[:space:]]+$/, ""); value=$0
          }
          END { print value }
        ' "$headers")
      fi
      if [ -n "$retry_after" ]; then
        # Retry-After is seconds or an HTTP date. Cap before shell arithmetic
        # so an enormous server value cannot overflow or extend the budget.
        if retry_seconds=$(perl -MTime::Piece -e '
          my $value=shift; my $wait;
          if ($value =~ /^[0-9]+$/) {
            $value =~ s/^0+(?=[0-9])//;
            $wait=length($value)>3 ? 226 : 0+$value;
          } else {
            eval { $wait=Time::Piece->strptime($value, "%a, %d %b %Y %H:%M:%S GMT")->epoch-time };
            exit 1 if $@;
          }
          $wait=0 if $wait<0;
          print $wait>225 ? 226 : $wait;
        ' "$retry_after"); then
          if [ "$retry_seconds" -gt "$wait_for" ]; then wait_for=$retry_seconds; fi
        fi
      fi
      if [ "$wait_for" -gt "$((225 - waited))" ]; then
        echo "::notice::Retry-After exceeds the remaining public retry budget; using authenticated fallback."
        break
      fi
      sleep "$wait_for" || return 1
      waited=$((waited + wait_for))
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
