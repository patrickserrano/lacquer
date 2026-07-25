---
title: "url-validation-security"
description: "Use when validating a user-provided or externally-sourced URL before it reaches `AVPlayer`, `URLSession`, or a `WKWebView` — building a positive- allowlist URL validator, or reviewing existing networking/media code for missing URL validation."
---

:::note
Generated from [`profiles/ios/skills/url-validation-security/SKILL.md`](https://github.com/patrickserrano/lacquer/blob/main/profiles/ios/skills/url-validation-security/SKILL.md) — edit that file, not this page.
:::


# URL Validation Security Posture

Validate **every** user-provided URL through a positive-allowlist validator
before it reaches `AVPlayer`, `URLSession`, or a `WKWebView`. Validate at
**both** the manager and service boundaries (the duplication is intentional
defense-in-depth). Known limitation: homograph / IDN look-alike hosts are not
detected.

The validator parses once via `URLComponents` and asserts: http/https scheme
only, non-empty host, no userinfo (credentials), a UTF-8 **byte-length** cap,
and rejection of C0 controls / DEL / literal & percent-encoded null bytes. The
dangerous-scheme denylist is redundant belt-and-suspenders.

```swift
enum SecureURLValidator {
    /// Returns true only when the URL satisfies every required property.
    /// Known limitation: homograph / IDN look-alike hosts are not detected.
    nonisolated static func validate(_ urlString: String) -> Bool {
        guard !urlString.isEmpty else { return false }
        guard urlString.utf8.count <= 2048 else { return false }
        guard !urlString.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7F }) else { return false }
        guard !urlString.contains("\0"), !urlString.lowercased().contains("%00") else { return false }
        let dangerous = ["javascript:", "data:", "file:", "vbscript:"]
        guard !dangerous.contains(where: { urlString.lowercased().hasPrefix($0) }) else { return false }
        guard let components = URLComponents(string: urlString) else { return false }
        guard let scheme = components.scheme?.lowercased(), ["http", "https"].contains(scheme) else { return false }
        guard components.user == nil, components.password == nil else { return false }
        guard let host = components.host, !host.isEmpty else { return false }
        return true
    }
}
```
