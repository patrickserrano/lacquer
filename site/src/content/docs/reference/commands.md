---
title: Command reference
description: Every harness CLI subcommand.
---

| Command | Does |
|---------|------|
| `harness init` | Detect components, write a `.harness.toml` stub (and a `docs/brief.md` stub). |
| `harness onboard --org O [--no-repo]` | `init`, then create a private GitHub repo under `O` when the repo has no `origin`. |
| `harness sync [--force]` | Render core + per-profile content into the project (managed regions + whole-file assets). |
| `harness status` | Show each region's stamped version vs the harness's latest. |
| `harness audit` | Classify project drift; exit 3 if a sync would clobber a local change (usable as a CI gate). |
| `harness version` | Print the harness version. |

`harness --help` prints usage.

## Manifest shape

A project opts in via `.harness.toml` at its root:

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

[[component]]
path = "."
profiles = ["ios"]
```

`core` applies to every project regardless of `[[component]]` entries. A
component detected as an unshipped stack (e.g. Rust/Go) is recorded with an
empty profile list and a notice — it doesn't break `sync`.
