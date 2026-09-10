#!/usr/bin/env python3
"""
Fetch TestFlight beta feedback (tester screenshots + crashes) from the App Store
Connect API and file one deduplicated GitHub issue per new item.

Runs in CI on a GitHub-hosted runner (pure REST + `gh`, no Xcode). Env:
    APP_STORE_CONNECT_ISSUER_ID       - feedback API key issuer id
    APP_STORE_CONNECT_KEY_IDENTIFIER  - feedback API key id
    APP_STORE_CONNECT_PRIVATE_KEY     - feedback API key .p8 contents
    APP_ID                            - numeric App Store app id (not bundle id)
    GH_TOKEN, GH_REPO                 - provided by Actions for `gh`

Use a dedicated, least-privilege App Store Connect key for feedback — never the
release/signing key.

Trust boundary: feedback attributes (comment, tester email, device, OS, app
version) are written by arbitrary TestFlight testers and flow into GitHub issue
markdown. Every tester-controlled string passes through sanitize() before use so
HTML comment delimiters — and with them a forged tf-feedback-id dedup marker —
can never come from tester input.

Wrong field names must be loud, never blank. Every attribute this script reads
is named in the request's `fields[<type>]` parameter, and App Store Connect
rejects a name it does not recognise:

    HTTP 400 PARAMETER_ERROR.INVALID
    'creationDate' is not a valid field name

so a typo fails the run at the API boundary instead of yielding "" from a
`.get(key, "")` and printing an empty line in the issue. REQUIRED_ATTRS is the
second half of that guard: a key the API stopped returning raises
MissingAttribute for that submission rather than degrading to "". This is not
theoretical tidiness — it is the defect this script shipped with. `appVersion`
and `timestamp` are not attributes of either feedback resource (the app version
lives behind the `build` relationship; the submission time is `createdDate`), so
thirteen issues were filed carrying two silently blank fields.
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

BASE = "https://api.appstoreconnect.apple.com/v1"
LABEL = "testflight-feedback"

# Attributes and relationships requested per feedback resource, and the ones a
# response must carry. Sent verbatim as `fields[<resource type>]`, so every name
# here is validated by App Store Connect on every run (see the module docstring).
#
# `build` and `tester` are RELATIONSHIP names, listed because naming any subset
# of fields suppresses the whole `relationships` object otherwise — and without
# `relationships.build.data.id` per row there is no way to map a row to its
# entry in the flat, de-duplicated `included` array.
#
# OPTIONAL_ATTRS is a deliberate, documented list of things a submission may
# genuinely lack (a screenshot filed with no comment). Everything else missing
# is a bug in this script or a change in the API, and is treated as one.
FIELDS = {
    "screenshot": (
        "createdDate", "comment", "email", "deviceModel", "osVersion",
        "screenshots", "build", "tester",
    ),
    "crash": (
        "createdDate", "comment", "email", "deviceModel", "osVersion",
        "crashLog", "build", "tester",
    ),
}
REQUIRED_ATTRS = {
    "screenshot": ("createdDate", "deviceModel", "osVersion", "email", "screenshots"),
    "crash": ("createdDate", "deviceModel", "osVersion", "email"),
}
OPTIONAL_ATTRS = {"screenshot": ("comment",), "crash": ("comment",)}

RESOURCE_TYPE = {
    "screenshot": "betaFeedbackScreenshotSubmissions",
    "crash": "betaFeedbackCrashSubmissions",
}

# A crash log is a whole file. Kept well inside GitHub's 65536-character issue
# body limit, with room for everything else in the body.
CRASH_LOG_CHARS = 20000


class MissingAttribute(Exception):
    """An attribute this script requires was absent from the API response.

    Raised instead of substituting "" — the whole point of #477. Carries the
    submission id so the operator can look the one bad row up, and the keys
    that WERE present so a renamed attribute is obvious from the log alone.
    """

    def __init__(self, resource, submission_id, key, present):
        super().__init__(
            f"{resource} {submission_id}: expected attribute {key!r} is absent from "
            f"the API response. Present: {sorted(present)}. Either the request's "
            f"fields[] list and REQUIRED_ATTRS disagree, or App Store Connect "
            f"renamed or withdrew this attribute."
        )
        self.submission_id = submission_id
        self.key = key


def sanitize(text: str) -> str:
    """Strip HTML comment delimiters from tester-controlled text.

    With `<!--`/`-->` removed, an HTML comment in an issue body — in
    particular the hidden `tf-feedback-id` dedup marker — can only ever be
    produced by this script, never forged by a tester.

    Must strip to a FIXPOINT: a single replace pass can splice adjacent
    characters into a fresh delimiter (`<!<!----` -> `<!--`).
    """
    while "<!--" in text or "-->" in text:
        text = text.replace("<!--", "").replace("-->", "")
    return text


def _b64url(data: bytes) -> str:
    import base64
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode()


def make_jwt(issuer_id: str, key_id: str, private_key_pem: str) -> str:
    """ES256 JWT for App Store Connect, signed via openssl (stdlib only)."""
    import os as _os
    import tempfile

    header = _b64url(json.dumps({"alg": "ES256", "kid": key_id, "typ": "JWT"}).encode())
    now = int(time.time())
    payload = _b64url(json.dumps({
        "iss": issuer_id, "iat": now, "exp": now + 1200, "aud": "appstoreconnect-v1",
    }).encode())
    message = f"{header}.{payload}"

    with tempfile.NamedTemporaryFile(suffix=".p8", mode="w", delete=False) as f:
        f.write(private_key_pem)
        key_path = f.name
    try:
        # openssl emits a DER ECDSA signature; JWS ES256 wants raw r||s. Convert.
        der = subprocess.run(
            ["openssl", "dgst", "-sha256", "-sign", key_path],
            input=message.encode(), capture_output=True, check=True,
        ).stdout
        sig = _b64url(_der_to_raw_ecdsa(der))
    finally:
        _os.unlink(key_path)
    return f"{message}.{sig}"


def _der_to_raw_ecdsa(der: bytes) -> bytes:
    """Convert a DER-encoded ECDSA signature to the fixed 64-byte r||s JWS form."""
    # SEQUENCE { INTEGER r, INTEGER s }
    if der[0] != 0x30:
        raise ValueError("bad DER signature")
    idx = 2 if der[1] < 0x80 else 3 + (der[1] & 0x7F) - 1
    def read_int(i):
        if der[i] != 0x02:
            raise ValueError("bad DER integer")
        ln = der[i + 1]
        val = der[i + 2:i + 2 + ln]
        return val.lstrip(b"\x00").rjust(32, b"\x00"), i + 2 + ln
    r, idx = read_int(idx)
    s, _ = read_int(idx)
    return r + s


def _get(url: str, token: str) -> dict:
    req = urllib.request.Request(
        url, headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read())


def _paginate(url: str, token: str, params: dict, limit: int = 50) -> tuple:
    """Walk every page, returning (rows, included-by-(type, id)).

    `included` is a flat, de-duplicated array per page — a build shared by four
    submissions appears once — so it is merged into one lookup table keyed by
    (type, id) and resolved through each row's own relationship ids.
    """
    query = dict(params, limit=str(limit))
    # `safe` keeps `fields[builds]` and its comma-separated value readable in
    # logs; both characters are legal unencoded in a query string.
    nxt = f"{url}?{urllib.parse.urlencode(query, safe=',[]')}"
    rows, included = [], {}
    while nxt:
        page = _get(nxt, token)
        rows.extend(page.get("data", []))
        for res in page.get("included", []):
            included[(res.get("type"), res.get("id"))] = res
        # ASC's `next` link carries the original fields[]/include parameters.
        nxt = page.get("links", {}).get("next")
    return rows, included


def _attributes(kind: str, item: dict) -> dict:
    """The submission's attributes, with every required key proven present.

    A key that is present but null is legitimate absence (no comment, no paired
    watch) and passes through as None. A key that is *missing* means this
    script and the API disagree about the resource, which is exactly the
    failure #477 hid, so it raises.
    """
    attrs = item.get("attributes") or {}
    sid = item.get("id", "<no id>")
    for key in REQUIRED_ATTRS[kind]:
        if key not in attrs:
            raise MissingAttribute(RESOURCE_TYPE[kind], sid, key, attrs.keys())
    return attrs


def _related_id(item: dict, name: str):
    """The id a to-one relationship points at, or None if it is absent."""
    rel = (item.get("relationships") or {}).get(name) or {}
    data = rel.get("data") or {}
    return data.get("id")


def _related_link(item: dict, name: str):
    """A relationship's `links.related` URL, or None.

    Used for `crashLog`, which App Store Connect refuses to `include`
    ("The relationship 'crashLog' cannot be included") and which therefore has
    to be followed. Read from the response rather than assembled from a guessed
    path, so a URL layout change cannot silently produce a 404.
    """
    rel = (item.get("relationships") or {}).get(name) or {}
    return (rel.get("links") or {}).get("related")


def app_version(item: dict, included: dict, token: str, cache: dict):
    """Marketing version and build number for a submission, e.g. "1.3 (42)".

    Neither is an attribute of the feedback resource. `include=build` supplies
    the build, whose `version` is the BUILD NUMBER; the marketing version lives
    one hop further out on the build's `preReleaseVersion`, which ASC does not
    accept as a nested include (`'build.preReleaseVersion' is not a valid
    relationship name`), so it costs one extra request per distinct build —
    hence `cache`.

    Returns None when the submission has no build relationship at all, so the
    caller can omit the field instead of printing it blank.
    """
    build_id = _related_id(item, "build")
    if not build_id:
        return None
    build = included.get(("builds", build_id)) or {}
    number = (build.get("attributes") or {}).get("version")

    if build_id not in cache:
        cache[build_id] = None
        link = _related_link(build, "preReleaseVersion")
        if link:
            try:
                data = _get(link, token).get("data") or {}
                cache[build_id] = (data.get("attributes") or {}).get("version")
            except (urllib.error.HTTPError, urllib.error.URLError) as e:
                # Supplementary, so this degrades the field rather than the run
                # — but it says so out loud rather than rendering as blank.
                print(f"::warning::could not resolve the marketing version for build "
                      f"{build_id}: {e}", file=sys.stderr)
    marketing = cache[build_id]

    if marketing and number:
        return f"{marketing} ({number})"
    return marketing or (f"build {number}" if number else None)


def markdown_url(url) -> str:
    """An Apple-signed asset URL, vetted before it is put in markdown.

    A markdown image destination ends at the first unbalanced `)`, so a URL
    containing one would spill its tail into the rendered body as text; a
    `<` or a quote could close the surrounding construct entirely. Apple
    percent-encodes all of these, so this refuses such a URL rather than
    trusting that it always will.
    """
    if not isinstance(url, str) or not url.startswith("https://"):
        return ""
    if any(c in url for c in "()<>\"'` \t\r\n"):
        return ""
    return url


def crash_log_text(item: dict, token: str) -> str:
    """The crash log body for a crash submission, or "".

    `crashLog` is a relationship whose resource carries the log as inline
    `logText` — there is no downloadable asset and no `crashLog.url` attribute,
    which is what the previous `a.get("crashLog", {}).get("url", "")` was
    reaching for and why it always produced "".
    """
    link = _related_link(item, "crashLog")
    if not link:
        return ""
    try:
        data = _get(link, token).get("data") or {}
    except (urllib.error.HTTPError, urllib.error.URLError) as e:
        print(f"::warning::could not fetch the crash log for {item.get('id')}: {e}",
              file=sys.stderr)
        return ""
    return (data.get("attributes") or {}).get("logText") or ""


def screenshots(attrs: dict) -> list:
    """Every screenshot image on a submission as {url, width, height, expires}.

    Each image is a plain expiring signed URL plus its own `expirationDate` —
    not a templated ImageAsset — so there is nothing to substitute and nothing
    that stays valid. Images with an unusable URL are dropped here so the
    caller can report the count honestly.
    """
    out = []
    for shot in attrs.get("screenshots") or []:
        if not isinstance(shot, dict):
            continue
        url = markdown_url(shot.get("url"))
        if not url:
            continue
        out.append({
            "url": url,
            "width": shot.get("width"),
            "height": shot.get("height"),
            "expires": shot.get("expirationDate") or "unknown",
        })
    return out


def fetch(kind: str, app_id: str, token: str) -> tuple:
    """Every feedback submission of one kind, as (records, failures).

    A submission whose attributes do not match REQUIRED_ATTRS is collected in
    `failures` rather than aborting the batch: the run still files the rows it
    understood, and still ends non-zero so the failure cannot pass unnoticed.
    """
    params = {
        f"fields[{RESOURCE_TYPE[kind]}]": ",".join(FIELDS[kind]),
        "include": "build",
        "fields[builds]": "version,preReleaseVersion",
    }
    rows, included = _paginate(f"{BASE}/apps/{app_id}/{RESOURCE_TYPE[kind]}", token, params)

    out, failures, version_cache = [], [], {}
    for item in rows:
        try:
            attrs = _attributes(kind, item)
        except MissingAttribute as e:
            failures.append(e)
            continue
        record = {
            "kind": kind,
            "id": item.get("id", ""),
            # Tester-controlled fields are sanitized at ingestion, before any use.
            "tester": sanitize(attrs.get("email") or "unknown"),
            "device": sanitize(attrs.get("deviceModel") or ""),
            "os": sanitize(attrs.get("osVersion") or ""),
            "appVersion": app_version(item, included, token, version_cache),
            "createdDate": attrs.get("createdDate") or "",
            "comment": sanitize((attrs.get("comment") or "").strip()),
            "screenshots": screenshots(attrs) if kind == "screenshot" else [],
            "crashLog": sanitize(crash_log_text(item, token)) if kind == "crash" else "",
        }
        out.append(record)
    return out, failures


def dedup_marker(feedback_id: str) -> str:
    """The exact HTML-comment marker create_issue embeds in the issue body.

    sanitize() strips `<!--`/`-->` from tester text, so this full string can
    only ever be written by this script — never forged from tester input.
    """
    return f"<!-- tf-feedback-id: {feedback_id} -->"


def issue_exists(feedback_id: str) -> bool:
    """True if an issue already references this feedback id (dedup, stateless).

    Two-phase: GitHub search matches loose tokens anywhere in a body (a tester
    comment quoting an id would match), so the search only nominates candidates;
    dedup is decided by an exact-substring check for the full HTML-comment
    marker, which sanitize() guarantees tester text can never contain.
    """
    res = subprocess.run(
        # `--search=<id>` (not `--search <id>`) so an id starting with `-` can
        # never be parsed as a flag.
        ["gh", "issue", "list", "--state", "all", "--label", LABEL,
         f"--search={feedback_id}", "--json", "body"],
        capture_output=True, text=True, check=True)
    marker = dedup_marker(feedback_id)
    return any(marker in issue.get("body", "")
               for issue in json.loads(res.stdout or "[]"))


def issue_title(f: dict) -> str:
    """One-line issue title: kind, app version when known, device, tester.

    Built by joining only the parts that exist, so an unknown app version or a
    submission with no device model cannot leave a double space or a dangling
    separator in the title.
    """
    parts = [f"[TestFlight {f['kind']}]"]
    parts += [p for p in (f["appVersion"], f["device"], f["os"]) if p]
    return f"{' '.join(parts)} — {f['tester']}"


def issue_body(f: dict) -> str:
    """The issue body: every known field, and no line for an unknown one.

    A field the API did not supply is OMITTED rather than rendered as an empty
    value — a blank `**App version:**` reads as "this build has no version",
    which is how #477 stayed invisible for thirteen issues.
    """
    lines = ["Filed automatically from TestFlight beta feedback.", ""]
    for label, value in (
        ("Tester", f["tester"]),
        ("Device", f"{f['device']} ({f['os']})" if f["os"] else f["device"]),
        ("App version", f["appVersion"]),
        ("Submitted", f["createdDate"]),
    ):
        if value:
            lines.append(f"- **{label}:** {value}")
    body = "\n".join(lines) + "\n"

    if f["comment"]:
        # Blockquote EVERY line so no part of a multi-line tester comment escapes
        # the quote and reads as issue-author markdown.
        quoted = "\n".join(f"> {line}" for line in f["comment"].splitlines())
        body += f"\n**Comment:**\n\n{quoted}\n"

    shots = f["screenshots"]
    if shots:
        body += f"\n**Screenshot{'s' if len(shots) > 1 else ''}:**\n\n"
        for i, shot in enumerate(shots, 1):
            size = (f" ({shot['width']}×{shot['height']})"
                    if shot["width"] and shot["height"] else "")
            # Embedded so it renders in the issue, AND linked with its expiry
            # stated: these URLs are signed and time-limited, so a reader who
            # finds a dead link needs to know the screenshot expired rather
            # than concluding none was ever captured. App Store Connect mints a
            # fresh URL on every request, so the image itself is never lost —
            # the id below re-fetches it.
            body += f"![TestFlight screenshot {i}]({shot['url']})\n\n"
            body += (f"[Screenshot {i}]({shot['url']}){size} — Apple's signed link "
                     f"expires {shot['expires']}. Re-fetch a fresh one with "
                     f"`GET /v1/{RESOURCE_TYPE['screenshot']}/{f['id']}`.\n\n")
    elif f["kind"] == "screenshot":
        body += ("\n**Screenshot:** none returned by App Store Connect for this "
                 "submission.\n")

    if f["crashLog"]:
        log = f["crashLog"]
        truncated = len(log) > CRASH_LOG_CHARS
        shown = log[:CRASH_LOG_CHARS]
        body += "\n<details>\n<summary>Crash log</summary>\n\n```\n"
        body += shown + ("\n… truncated\n" if truncated else "\n")
        body += "```\n\n</details>\n"

    # Hidden marker the dedup verify pass matches on (see issue_exists).
    body += f"\n{dedup_marker(f['id'])}\n"
    return body


def create_issue(f: dict) -> None:
    """File one issue for one feedback submission."""
    subprocess.run(
        ["gh", "issue", "create", "--label", LABEL,
         "--title", issue_title(f), "--body", issue_body(f)],
        check=True)


def main() -> int:
    issuer = os.environ.get("APP_STORE_CONNECT_ISSUER_ID", "")
    key_id = os.environ.get("APP_STORE_CONNECT_KEY_IDENTIFIER", "")
    pkey = os.environ.get("APP_STORE_CONNECT_PRIVATE_KEY", "")
    app_id = os.environ.get("APP_ID", "")
    if not all([issuer, key_id, pkey, app_id]):
        print("Missing required env (APP_STORE_CONNECT_ISSUER_ID / _KEY_IDENTIFIER / "
              "_PRIVATE_KEY / APP_ID).", file=sys.stderr)
        return 1

    token = make_jwt(issuer, key_id, pkey)
    filed, broken = 0, 0
    for kind in ("screenshot", "crash"):
        try:
            items, failures = fetch(kind, app_id, token)
        except urllib.error.HTTPError as e:
            # A 400 PARAMETER_ERROR.INVALID here is the fields[] guard firing:
            # some name in FIELDS is not an attribute of this resource.
            print(f"::error::{kind}: HTTP {e.code}: {e.read().decode()}", file=sys.stderr)
            return 1
        for failure in failures:
            print(f"::error::{failure}", file=sys.stderr)
        broken += len(failures)
        print(f"{kind}: {len(items)} submission(s), {len(failures)} unreadable")
        for f in items:
            if not f["id"] or issue_exists(f["id"]):
                continue
            create_issue(f)
            filed += 1
    print(f"Filed {filed} new issue(s).")
    if broken:
        print(f"{broken} submission(s) could not be read — see the errors above.",
              file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
