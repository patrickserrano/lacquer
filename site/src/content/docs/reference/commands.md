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

`optional_workflows` opts into a workflow the lacquer ships but does not install
by default, named without its `.yml`:

```toml
optional_workflows = ["testflight-feedback"]
```

The default-off set exists because a workflow needing credentials nobody has
doesn't fail loudly — it fails **daily and quietly**. `testflight-feedback` wants
`APP_STORE_CONNECT_FEEDBACK_ISSUER_ID` and two siblings; no project in this fleet
had them, so it was red on every scheduled run in every repo that received it. A
scheduled job nobody can satisfy is worse than a missing feature: it trains
people to ignore red.

A name with no matching file is an error, not a silent no-op.

### Retired projects

`retired` marks a project that is no longer worth investing in but is not being
deleted:

```toml
[project]
retired = { since = "2026-08-18", reason = "not a viable app" }
```

Retired means **stop the spend, stay consistent.** `sync` keeps shipping
everything that holds the repo to the fleet's shape — PR-triggered CI, lint and
format configs, `CLAUDE.md` / `AGENTS.md`, `.gitignore`, hooks, skills — so the
project still audits clean and can be picked back up. It stops shipping
everything that costs money or attention **on a schedule**:

| Dropped | Kept |
|---------|------|
| Any workflow whose `on:` block has a `schedule:` trigger (`*-docs.yml`, `ios-cleanup-ci.yml`, `ios-dependency-audit.yml`, `ios-quality-review.yml`, `supabase-health.yml`, …) | `*-ci.yml`, `web-dependency-review.yml`, `web-env-validation.yml`, `ios-claude.yml`, `ios-issue-deduplication.yml`, `ios-release.yml` |
| `.github/dependabot.yml` | every non-workflow asset |

"Is scheduled" is read from each workflow's **content**, not from a list of
filenames — a filename list silently misses the next scheduled workflow someone
adds. A workflow declaring `workflow_dispatch:` beside `schedule:` is still
dropped: the dispatch entry is a convenience, the cron is what runs unattended.

Both fields are required and a malformed entry fails the load. `retired = true`
records that someone retired the project and not why, and six months later why is
the only thing anyone wants to know. Unlike `[baseline.relax]` and the dated form
of `exclude`, retirement takes **no `until`** — it is not debt with a term, and an
expiry would either be rubber-stamped forever or quietly turn a dead project's
cron jobs back on.

`lacquer status` and `lacquer audit` both lead with the retirement and its date,
and `audit` still exits 0: the dropped assets stop being managed units, so they
read as neither drift nor missing. Nothing is deleted — files already in the repo
stay until someone removes them by hand.

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

`tag_prefix` is required once a project declares more than one product — a blank
prefix means "every tag releases this", which is right with one product and
incoherent with two. It also **derives the release workflow's push-tag filter**:
a repo whose products are prefixed `steps-v` and `stepsfree-v` triggers on those
patterns, not on `v*`. A project declaring no products keeps the historical
`v*`.

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

### Per-product CI targets

The iOS CI workflow builds and tests **every** declared product — one
`Build (Release)` leg and one `Test` leg each, with `fail-fast: false` so one
product failing does not cancel the other's run. Four optional fields describe
the test leg:

```toml
[[product]]
name = "MyApp Lite"
scheme = "MyAppLite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "1111111111"
tag_prefix = "myapplite"
test_target = "MyAppLiteTests"   # defaults to "<name>Tests"
ui_test_target = ""              # blank = no UI tests for this variant
extra_test_targets = ["CoreKitTests"]  # local package suites to run as well
app_target = "MyApp.app"         # coverage target; defaults to "<name>.app"
```

`extra_test_targets` adds `-only-testing:` selectors for suites the app's own
bundle does not contain — typically a local Swift package's test target, which
is otherwise run by nothing while xcodebuild still exits 0. Declaring any turns
on a CI step that reads the result bundle back and fails the job for a selector
that matched no tests; blank and repeated entries are rejected at load, because
both render a selector that runs nothing. The pre-commit `Swift Tests` hook runs
the same extras.

`app_target` is declared, not derived: a scheme and the product it builds
genuinely differ in real projects, and a wrong target selects no coverage row at
all — which reports 0.0% rather than failing.

Each leg's CI simulator and test-results artifact are scoped by a slug derived
from the product name, so two legs on one runner cannot delete each other's
simulator or collide on an artifact name. Two products whose names reduce to the
same slug are rejected at load.

**A project declaring no products renders the CI workflow byte-for-byte as it
did before products existed** — the matrix machinery expands to nothing.

### Release-time secrets

A product that needs real values at release — monetization SDK keys, ad unit
IDs — maps each xcconfig key to the GitHub secret holding it:

```toml
[[product]]
name = "MyApp Lite"
scheme = "MyAppLite"
bundle_id = "com.example.myapp.lite"
asc_app_id = "1111111111"
tag_prefix = "myapplite"
secrets_file = "Config/Monetization.xcconfig"   # optional, defaults to Secrets.xcconfig
secrets = { REVENUECAT_API_KEY = "LITE_REVENUECAT_KEY", ADMOB_APP_ID = "LITE_ADMOB_APP_ID" }
secret_formats = { REVENUECAT_API_KEY = "appl_*", ADMOB_APP_ID = "ca-app-pub-*~*" }
```

`secret_formats` optionally constrains a value's **shape**, as a shell glob
checked at release time. Non-empty is not the same as correct: the two ways
these keys actually go wrong — pasting another app's key, or leaving Google's
public test AdMob ID in place — both produce a perfectly non-empty value that
builds, signs, uploads and passes review, then serves the wrong ads to real
users. A mismatch fails the release without echoing the value.

The manifest holds the secret's **name**; the value stays in GitHub. `lacquer`
rejects a value that looks like a real credential, because this file is
committed.

The release writes those values into the xcconfig on that product's matrix leg
only, and **fails if any of them is unset or empty**. It does not fall back to
the placeholder seeding `ci.yml` does: CI seeds placeholders because tests must
run without production keys, whereas a release doing the same would sign and
ship an IPA wired to `appl_xxxxxxxx`, with nothing looking wrong until the
revenue didn't arrive. A product that declares no secrets renders no step, which
is the normal case.

A `workflow_dispatch` run picks its product from a dropdown, defaulting to
`all`. Scoping matters there too: a dispatch of `all` after one app has shipped
a version hits the same closed train. Naming an unknown product fails rather
than falling back to everything.
