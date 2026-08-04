# Archetypes

An archetype is the baseline toolset for a kind of app: the components and
profiles a project of that shape has, before any of the code exists.

```
lacquer init --list-stacks
lacquer init --stack ios-supabase
```

## Why this exists

Detection can only see what is already on disk, which makes it useless at the
one moment the stack decision is actually being made — while the idea is still a
brief and a PCD. So the stack got decided in prose, in a document, and then
`lacquer init` re-derived it from whichever half happened to be written first.

Needledrop is the case: the PCD described an iOS app with a web backend, the
repo was bootstrapped during a TypeScript-only spike, and detection correctly
recorded `profiles = ["web"]`. The Swift arrived the next day. Nothing re-ran
detection, so Swift had no hooks, no CI, and no CLAUDE region for a month — 191
tests run by nothing at any gate.

`--stack` closes that by letting the PCD's answer *be* the manifest's answer.
`internal/detect.Drift` closes the other half, for projects that grow a stack
after onboarding anyway.

## Choosing one

Name the archetype in the brief/PCD under **Stack**, then pass it to `init`.
An archetype is a starting point, not a cage: components detection actually
finds always win, and the manifest is yours to edit afterwards.

| Stack | Shape |
| --- | --- |
| `ios` | A native iOS app, nothing else. |
| `ios-supabase` | A native iOS app with a Supabase backend beside it. |
| `ios-web` | A native iOS app plus a separate web surface (marketing site, dashboard). |
| `ios-web-supabase` | A native iOS app, a web surface, and a Supabase backend. |
| `web` | A web app on its own. |
| `web-supabase` | A web app with a Supabase backend. |

## Adding one

Drop a `<name>.toml` in this directory:

```toml
description = "One line, shown by `lacquer init --list-stacks`."

[[component]]
path = "."
profiles = ["ios"]
```

Rules the loader enforces: the name is lowercase-kebab, at least one component
is required, and every profile named must be one the lacquer actually ships
(`profiles/<p>/CLAUDE.<p>.md`) — `lacquer init` reports any that do not.
