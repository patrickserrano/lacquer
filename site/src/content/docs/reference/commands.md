---
title: Command reference
description: Every lacquer CLI subcommand.
---

| Command | Does |
|---------|------|
| `lacquer init` | Detect components, write a `.lacquer.toml` stub (and a `docs/brief.md` stub). |
| `lacquer onboard --org O [--no-repo]` | `init`, then create a private GitHub repo under `O` when the repo has no `origin`. |
| `lacquer sync [--force]` | Render core + per-profile content into the project (managed regions + whole-file assets). |
| `lacquer skills` | Install `[project].skills` entries via the [`skills` CLI](https://github.com/vercel-labs/skills). See [Third-party skills](/lacquer/guides/getting-started/#third-party-skills). |
| `lacquer plugins` | Install `core/bootstrap/plugins.toml` (machine-level Claude Code plugins) via `claude plugin`. See [Plugins](/lacquer/guides/getting-started/#plugins-machine-level-bootstrap). |
| `lacquer status` | Show each region's stamped version vs the lacquer's latest. |
| `lacquer audit` | Classify project drift; exit 3 if a sync would clobber a local change (usable as a CI gate). |
| `lacquer version` | Print the lacquer version. |

`lacquer --help` prints usage.

## Manifest shape

A project opts in via `.lacquer.toml` at its root:

```toml
[project]
name = "my-app"
project_name = "MyApp"
scheme = "MyApp"
bundle_id = "com.example.myapp"
asc_app_id = "0000000000"
xcodeproj = "MyApp.xcodeproj"
swift_version = "6.0"
github_org = "my-org"
tools = []
exclude = []
skills = ["dpearson2699/swift-ios-skills@healthkit"]

[[component]]
path = "."
profiles = ["ios"]
```

`core` applies to every project regardless of `[[component]]` entries. A
component detected as an unshipped stack (e.g. Rust/Go) is recorded with an
empty profile list and a notice — it doesn't break `sync`.

`skills` entries are `"<owner>/<repo>@<skill-name>"` strings, installed by
`lacquer skills` — see [Third-party
skills](/lacquer/guides/getting-started/#third-party-skills).

### Multiple products from one repo

A repo that ships more than one App Store app — a paid app and a free or lite
sibling built from the same source — declares each as a `[[product]]`:

```toml
[[product]]
name = "MyApp"
scheme = "MyApp"
bundle_id = "com.example.myapp"
asc_app_id = "0000000000"
tag_prefix = "myapp"

[[product]]
name = "MyApp Lite"
scheme = "MyAppLite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "1111111111"
tag_prefix = "myapplite"
```

Declaring none is the normal case: `[project]` is then treated as the single
product, and every tag releases it.

`tag_prefix` decides which product a tag releases. `myapp-v2.1.0` releases
MyApp and leaves the Lite app alone. **One tag must release exactly one
product.** An App Store version train closes permanently once its version
reaches `READY_FOR_SALE`, so a tag that fanned out to both apps would push the
already-shipped one at a closed train and fail with error 90186 — every time,
for the life of that version.

A tag matching no product's prefix fails the release rather than guessing;
guessing signs a product with another app's credentials. A product with a blank
`tag_prefix` matches any tag, which is what makes the single-product case work
unchanged.

A `workflow_dispatch` run picks its product from a dropdown, defaulting to
`all`. Scoping matters there too: a dispatch of `all` after one app has shipped
a version hits the same closed train. Naming an unknown product fails rather
than falling back to everything.
