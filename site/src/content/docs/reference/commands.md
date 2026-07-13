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
