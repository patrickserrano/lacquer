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
"""
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

BASE = "https://api.appstoreconnect.apple.com/v1"
LABEL = "testflight-feedback"


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
        assert der[i] == 0x02
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


def _paginate(url: str, token: str, limit: int = 50) -> list:
    results, nxt = [], f"{url}?limit={limit}"
    while nxt:
        page = _get(nxt, token)
        results.extend(page.get("data", []))
        nxt = page.get("links", {}).get("next")
    return results


def fetch(kind: str, app_id: str, token: str) -> list:
    endpoint = {
        "screenshot": "betaFeedbackScreenshotSubmissions",
        "crash": "betaFeedbackCrashSubmissions",
    }[kind]
    out = []
    for item in _paginate(f"{BASE}/apps/{app_id}/{endpoint}", token):
        a = item.get("attributes", {})
        out.append({
            "kind": kind,
            "id": item.get("id", ""),
            "tester": a.get("testerEmail") or a.get("email", "unknown"),
            "device": a.get("deviceModel", ""),
            "os": a.get("osVersion", ""),
            "appVersion": a.get("appVersion", ""),
            "timestamp": a.get("timestamp", ""),
            "comment": (a.get("comment") or "").strip(),
            "crashLog": a.get("crashLog", {}).get("url", "") if kind == "crash" else "",
        })
    return out


def issue_exists(feedback_id: str) -> bool:
    """True if an issue already references this feedback id (dedup, stateless)."""
    res = subprocess.run(
        ["gh", "issue", "list", "--state", "all", "--label", LABEL,
         "--search", feedback_id, "--json", "number"],
        capture_output=True, text=True, check=True)
    return bool(json.loads(res.stdout or "[]"))


def create_issue(f: dict) -> None:
    title = f"[TestFlight {f['kind']}] {f['device']} {f['os']} — {f['tester']}"
    body = (
        f"Filed automatically from TestFlight beta feedback.\n\n"
        f"- **Tester:** {f['tester']}\n- **Device:** {f['device']} ({f['os']})\n"
        f"- **App version:** {f['appVersion']}\n- **When:** {f['timestamp']}\n"
    )
    if f["comment"]:
        body += f"\n**Comment:**\n\n> {f['comment']}\n"
    if f["crashLog"]:
        body += f"\n**Crash log:** {f['crashLog']}\n"
    # Hidden marker the dedup search matches on.
    body += f"\n<!-- tf-feedback-id: {f['id']} -->\n"
    subprocess.run(
        ["gh", "issue", "create", "--label", LABEL, "--title", title, "--body", body],
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
    filed = 0
    for kind in ("screenshot", "crash"):
        try:
            items = fetch(kind, app_id, token)
        except urllib.error.HTTPError as e:
            print(f"{kind}: HTTP {e.code}: {e.read().decode()}", file=sys.stderr)
            return 1
        print(f"{kind}: {len(items)} submission(s)")
        for f in items:
            if not f["id"] or issue_exists(f["id"]):
                continue
            create_issue(f)
            filed += 1
    print(f"Filed {filed} new issue(s).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
