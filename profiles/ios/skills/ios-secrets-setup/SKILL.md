---
name: ios-secrets-setup
description: >
  Use when wiring a new app-runtime service key (RevenueCat, Aptabase, …)
  into a project — setting up `Secrets.xcconfig`, surfacing a key through
  `project.yml` into `Info.plist`, and reading it at runtime. Distinct from
  CI/server secrets, which live in GitHub Actions secrets and never touch an
  xcconfig — see the project's `CLAUDE.md` Secrets section for that split.
---

# iOS App-Runtime Secrets Setup

App-runtime keys (RevenueCat, Aptabase, …) live in a gitignored
`Secrets.xcconfig`, never in source or the committed `project.yml`. The
lacquer syncs a `Secrets.xcconfig.example` template into the component dir.

1. **Copy & ignore:** `cp Secrets.xcconfig.example Secrets.xcconfig`, fill in
   real values, and add `Secrets.xcconfig` to `.gitignore`. The example is
   committed; the real file never is. (The committed `project.yml` must also
   stay key-free.)
2. **Wire into the build (`project.yml`):** point the target's configs at the
   xcconfig and surface each key into `Info.plist`:
   ```yaml
   targets:
     <App>:
       configFiles:
         Debug: Secrets.xcconfig
         Release: Secrets.xcconfig
       info:
         path: App/Info.plist
         properties:
           REVENUECAT_API_KEY: $(REVENUECAT_API_KEY)
           APTABASE_APP_KEY: $(APTABASE_APP_KEY)
   ```
3. **Read at runtime** from the Info dictionary — fail loud if a required key
   is blank rather than shipping a broken SDK init:
   ```swift
   enum Secrets {
       static func required(_ key: String) -> String {
           guard let v = Bundle.main.object(forInfoDictionaryKey: key) as? String,
                 !v.isEmpty else {
               fatalError("Missing \(key) — copy Secrets.xcconfig.example to Secrets.xcconfig and fill it in")
           }
           return v
       }
       static var revenueCatAPIKey: String { required("REVENUECAT_API_KEY") }
       static var aptabaseAppKey: String { required("APTABASE_APP_KEY") }
   }
   ```

`Secrets.xcconfig` values are **build-time** — they are baked into the
binary, so treat them as obfuscated, not secret. A truly sensitive secret
belongs on a server, never in the app.

> **RevenueCat ships two different keys — do not confuse them.** The
> `REVENUECAT_API_KEY` above is the **public SDK key** (`appl_…`), safe to
> compile into the app. RevenueCat's **REST API** uses a separate **secret
> key** (`sk_…`) that grants full account access — it must **never** go in
> `Secrets.xcconfig` or the binary. That's a CI/server secret
> (`REVENUECAT_REST_API_KEY`), set via `gh secret set` per the project's
> `CLAUDE.md` Secrets section.
