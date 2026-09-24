# lacquer

A Go CLI plus a set of profile templates that standardize how Claude Code works
across every project in `~/Developer`. The lacquer renders shared content —
`CLAUDE.md` rules, skills, commands, CI workflows, git hooks, tool configs, the
credential rules in `.gitignore` — into
each project and tracks how far each project has drifted, so a lesson pinned once
propagates everywhere instead of being copy-pasted and left to rot.

A project opts in per **component** (a subdirectory) via a `.lacquer.toml`
manifest. Each component declares one or more **profiles**; `core` applies to
every project regardless.

## Commands

| Command | Does |
|---------|------|
| `lacquer init [--stack S]` | Detect components, write a `.lacquer.toml` stub (and a `docs/brief.md` stub). `--stack` also declares the components the project doesn't have *yet*. |
| `lacquer init --list-stacks` | Print the archetypes `--stack` accepts. |
| `lacquer onboard --org O [--no-repo]` | `init`, then create a private GitHub repo under `O` when the repo has no `origin`. |
| `lacquer adopt` | Re-detect, and record any stack that appeared since `init` into `.lacquer.toml`. Only ever adds. |
| `lacquer sync [--force] [--fix]` | Render core + per-profile content into the project (managed regions + whole-file assets); `--fix` then runs the autofixers. |
| `lacquer fix` | Run each profile's autofixers (formatter, `lint --fix`) over the project source. |
| `lacquer doctor` | Prove each check can fail: feed known-bad input and assert it's rejected (exit 5 if one can't). |
| `lacquer skills` | Install `[project].skills` entries via the [`skills` CLI](https://github.com/vercel-labs/skills). |
| `lacquer plugins` | Install `core/bootstrap/plugins.toml` (machine-level Claude Code plugins) via `claude plugin`. |
| `lacquer status` | Show each region's stamped version vs the lacquer's latest. |
| `lacquer audit` | Classify project drift; exit 3 if a sync would clobber a local change, 4 on a baseline violation, 6 on an undeclared stack (usable as a CI gate). |
| `lacquer wait pr <N>` | Block until every check on a PR is terminal (no tokens while it sleeps). Exit 0 none failed, 1 a check failed, 2 timed out, 3 no checks — an empty check list is never a pass. The sanctioned way to wait for CI. |
| `lacquer version` | Print labeled content and build versions, plus the resolved content root path. |

`lacquer --help` prints usage.

## Dispatch model and effort

`console dispatch` starts ICs on **sonnet** by default. Set top-level
`ic_model = "opus"` and/or `ic_effort = "low"` in the fleet roster to change
fleet-wide defaults. `console dispatch-role` instead uses `model`/`effort` in
its `[[role]]` entry; absent settings inherit Claude's defaults.

Both commands accept `--model M` and `--effort E`, overriding each configured
value independently in bg and tmux modes. For example:

```sh
lacquer console --roster fleet.toml --sessions sessions.jsonl --mode bg dispatch app "implement the unit" --model opus --effort low
```

With `--sessions`, JSONL records keep the requested model and effort, including
failed launches. The dashboard and `console watch` show those settings, and
watchdog relaunches retain explicit recorded values. Empty values (including
old records) display `inherited/unknown`; they do not imply Sonnet. These are
**requested settings**, not transcript verification of the service's actual
model. Verify that separately from the session's own transcript when auditing
model usage. Dry runs print the selected flags and write no session record.

## Dispatch worktree preparation

Before an agent starts in a new, assigned, or resumed worktree, console writes
`.metadata_never_index` at its root to exclude build churn from Spotlight.
The marker is ignored through the user's global `core.excludesFile`, or Git's
default `$XDG_CONFIG_HOME/git/ignore` (`~/.config/git/ignore` without XDG).
Console preserves existing entries and appends the marker rule once; it never
adds it to project `.gitignore` files. An unwritable global file or a repository
ignore override that exposes the marker prevents launch with an error.
Dry runs write neither markers nor exclusions. Existing worktree branches and
source files are preserved. Console does not remove old worktrees.

## LACQUER_ROOT

Every command that reads shipped content resolves `LACQUER_ROOT` (default `.`)
and prints its absolute root path, tag or branch, and commit to **stderr**.
The root must be a clean Git checkout, detached at a tag matching `VERSION`.
Branches, dirty trees (including untracked files), missing Git, unknown commits,
and failed Git inspection are refused. Verification is local; it does not fetch
or claim that a pinned release is the newest release.

```sh
LACQUER_ROOT=~/.local/share/lacquer/content lacquer status
```

For deliberate development against a working checkout, explicitly opt in:

```sh
LACQUER_ALLOW_UNVERIFIED_ROOT=1 LACQUER_ROOT="$PWD" go run ./cmd/lacquer status
```

Every invocation that uses this override warns that its output is **UNREVIEWED**.
The separate `LACQUER_ALLOW_STALE_BINARY` override still governs `sync` when the
binary and content versions differ. Neither override proves a release pin.

`status` labels version-marker drift `stamp-behind` and checks whether managed
content still matches; `audit` classifies content drift. A release affecting only
another profile can leave a project's stamps behind while its content matches.

If the root has no `VERSION` file or `profiles/` directory, commands fail with an
actionable message before trying to read content.

## Profiles that ship

- **`core`** — universal rules/skills/commands applied to every project.
- **`ios`** — Swift/Xcode: SwiftLint/SwiftFormat, CI, TestFlight, Skills; git
  hooks via `pre-commit`.
- **`web`** — TypeScript + Biome + Vitest; CI + git hooks via `lefthook`.
- **`supabase`** — Deno Edge Functions + Postgres/RLS; CI + git hooks via
  `lefthook`.
- **`marketing`** — no CI, no hooks, skills only: ~50 marketing/growth skills
  (ads, SEO, copywriting, funnels, lifecycle, pricing, planning). Never
  auto-detected — there is no marketing "stack" on disk to find, so add it to a
  component's `profiles` deliberately when marketing work is in scope.

A component detected as an unshipped stack (e.g. Rust/Go, or a bare SwiftPM
package) is recorded in the manifest with an empty profile list and a notice —
it doesn't break `sync`, and `audit` keeps reporting it so the gap stays
visible.

## Declaring the stack before the code exists

Detection can only see what is already on disk, which makes it useless at the
one moment the stack is actually being decided — while the idea is still a brief
and a PCD. So name the stack there, as an **archetype**, and hand it to `init`:

```sh
lacquer init --list-stacks
lacquer init --stack ios-supabase
```

`--stack` declares the components a project of that kind has, including the ones
that do not exist yet, so both halves are gated from the first commit. Detected
components always win where the two disagree — the archetype only fills gaps.
See [`archetypes/`](archetypes/).

Projects that grow a stack *after* onboarding are the other half of the problem.
`sync` and `audit` now re-run detection every time, and:

- a stack the lacquer ships a profile for **blocks** — run `lacquer adopt` to
  record it, or add the path to `[project].exclude` to keep it unmanaged;
- a stack no profile covers is **reported on every run and gates nothing** —
  that gap is the lacquer's, not the project's.

Detection used to run exactly once, at `init`, and never again. One repo
bootstrapped as TypeScript-only during a spike, grew a Swift package the next
day, and a year later still declared `profiles = ["web"]`: no hooks, no CI, 191
tests run by nothing at any gate. Another declared its iOS app but not the
Supabase backend or the admin web app sitting beside it. Neither ever produced
an error, because nothing ever asked.

## Updating a project

```sh
lacquer audit    # see what drifted; exit 3 means sync would overwrite a local edit
lacquer sync     # apply; refuses to clobber a locally-modified managed unit
lacquer sync --force   # adopt the lacquer version over a local change
```

`fleet` uses the same blocking policy as `audit`, including still-present
orphans, and names those orphans in its report. The uncalled-script report
recognizes scripts named in rendered `CLAUDE.md` instructions as agent entry
points (including their script helpers). Removing that documentation exposes an
otherwise uncalled script again; other Markdown files do not grant exemptions.
This checks wiring or documented use, not proof that a script executed.

Sync writes a `.lacquer.lock` baseline so `audit` can tell "the project edited
this" from "the lacquer moved on". With an existing lock, sync also refuses
`untracked-conflict` units: lacquer now ships a path (or managed region) where
the project already has differing content, but no lock entry records ownership.
Review it, then use `--force` to take lacquer's content or exclude/disown the
unit. First sync, with no lock at all, still adopts existing content and prints
which units it replaced. Identical content is accepted without a clobber warning;
uncommitted asset changes remain protected even with `--force`.

## Regeneration drift and dead relaxations

`audit` and `fleet` report build-setting names present in a tracked `.xcodeproj`
but absent after `xcodegen generate`, and the reverse, by target/configuration
and project/configuration. A sibling `project.yml` opts the project into this
comparison. It compares the current checkout, including local edits, in a scratch
copy; it does not replace the original project. Value-only changes and effective
xcconfig values are outside this presence check (the baseline checker still
resolves its own settings).

XcodeGen and macOS `plutil` are required. Missing tools, unreadable input, failed
generation, and unsupported inputs such as symlinks or generation hooks report `NOT CHECKED`; they
never stand in for a clean comparison. Findings are report-only. The iOS Mac lint
job runs `lacquer audit --xcodegen-only` before doctor, using the same pinned
release, so CI can report this even though the Linux drift job lacks XcodeGen.

A `[baseline.relax]` entry whose baseline passes in every checked component is
reported as a **dead relaxation** to remove, including strict concurrency implied
by Swift 6. Like stale exclusions, this notice does not change the exit code.
An unknown baseline or a key enforced only by CI (`documentation`, `pgtap`) is
reported as `relaxation NOT CHECKED`, never assumed live or dead.

## Proving the checks work

Every serious defect found onboarding this fleet was the same shape: **a check
that ran, reported success, and verified nothing.**

- The editor hook called `swiftlint lint --path FILE`. `--path` had been removed
  from SwiftLint, so it errored on every write — and `2>/dev/null || true` ate
  the message. It linted nothing for months and looked healthy doing it.
- The DocC gate passed `DOCC_FLAGS=--warnings-as-errors`. That is a real build
  setting name and it is silently ignored; a deliberately broken symbol link
  still exited 0.
- The drift job ran `go build ./path` from a non-module directory. It failed in
  every repo, on every PR, for a reason unrelated to drift.

None was caught by running the check. CI already tells you whether a check
passes; it cannot tell you whether it *could* fail.

```sh
lacquer doctor
```

writes a **known-bad** fixture, runs the check against it, and asserts the check
**rejects** it. A probe that passes on broken input is reported as broken, with
the reason it exists. Exit 5 — distinct from `audit`'s 3 (drift) and 4
(baseline), so a caller can tell *"a check is broken"* from *"the project is
wrong"*.

```
proving each check can fail:
  ok    SwiftLint rejects a warning-severity violation under --strict
  ok    SwiftLint is invoked in a form it still accepts
  ok    SwiftFormat --lint rejects unformatted code
  ok    missing_docs rejects an undocumented declaration
  ok    the formatter and the linter agree on member order

5/5 checks proved they can fail.
```

Probes live in `profiles/<p>/doctor.toml`, and fixtures are written to a scratch
directory **outside** the project — a deliberately malformed file must never be
committable. A missing tool is a finding, not a skip: a check whose binary is
absent is not running. (The opposite of `lacquer fix`, where a missing tool
skips — an unfixed file is still caught by CI, an unverified one is not.)

## Adopting on an existing codebase

`lacquer sync` writes the configs; it does not touch your source. On a mature
app that means the newly-synced `.swiftlint.yml` finds everything at once — 509
violations in one app here, 290 in another — and roughly two thirds of that is
mechanical: member ordering, import sorting, trailing closures.

`--fix` pays that down before you ever look at it:

```sh
lacquer sync --fix     # sync, then run the profiles' autofixers
lacquer fix            # just the autofixers, any time
```

Measured on one app: **290 violations → 93**, with the whole `type_contents_order`
category (117) going to 1, because the synced `.swiftformat` enables
`organizeDeclarations` in `type` mode with a `--type-order` mirroring
`.swiftlint.yml`. What's left is judgement work — singletons, closure length,
layering — which is the right thing to be left with.

`--fix` is **opt-in on purpose.** Plain `sync` only ever writes lacquer-managed
files, and that contract is what makes `lacquer audit` able to say "you changed
this, the lacquer didn't". `--fix` deliberately breaks it by rewriting project
source, so it has to be asked for rather than discovered in a diff.

A fixer whose tool isn't installed is reported and skipped, never fatal — the
opposite of a *check*, which must block when it can't run. An unfixed file is
still caught by CI; a missing Homebrew formula shouldn't block adoption.

## Third-party skills

`lacquer sync` distributes this repo's own skills (`core/skills/`,
`profiles/*/skills/`) — that's a solved problem, versioned and drift-audited.
Third-party skills are a different concern: one global install shared across
every project, kept up to date by [`vercel-labs/skills`](https://github.com/vercel-labs/skills),
a real package manager for agent skills — not something lacquer reimplements.
The packages this fleet actually pulls in (with source links, and which is
suggested from which Swift import) are cataloged in [Skills
reference](https://patrickserrano.github.io/lacquer/reference/skills/#third-party-skills) —
currently [`dpearson2699/swift-ios-skills`](https://github.com/dpearson2699/swift-ios-skills)
(Apple framework references) and [`HunterHillegas/mac-assed-mac-app-skill`](https://github.com/HunterHillegas/mac-assed-mac-app-skill)
(AppKit/macOS conventions).

`[project].skills` in `.lacquer.toml` declares which packages *this* project
needs, mixing lacquer's own skills and third-party ones uniformly:

```toml
skills = [
  "patrickserrano/lacquer@security-review",
  "dpearson2699/swift-ios-skills@healthkit",
  "dpearson2699/swift-ios-skills@storekit",
]
```

`lacquer init` seeds this list automatically by scanning the project's actual
Swift imports (see `internal/skillsuggest`) — review and trim before running
`lacquer skills`, which installs exactly what's declared, project-scoped, via
`npx skills add <source> -s <name> -p -y`. Before invoking the installer, lacquer
refuses entries whose destination contains tracked files (including clean files
and local deletions), preserving project-owned skills while continuing with other
entries. It also flags any *installed* skill no longer declared in the manifest
(informational — nothing is auto-removed).

`sync` reads `skills-lock.json` offline and only reminds you about declared skills
missing from that record. An unreadable or malformed lock file produces a warning.

This is deliberately a separate command from `sync`: `sync` stays fully
offline and deterministic (its whole test suite depends on that), while
`skills` is the one command that reaches the network.

## Plugins (machine-level bootstrap)

Claude Code plugins install once at **user** scope and are shared across every
project on a machine — a different shape of problem than `[project].skills`,
which is per-project. `core/bootstrap/plugins.toml` lists the marketplaces and
six plugins this fleet relies on — `superpowers`, `codex` (adversarial review
via a real Codex subprocess), `context7`, `figma`, `security-guidance`, and
`telemetrydeck-analytics` — cataloged with source links in [Plugins
catalog](https://patrickserrano.github.io/lacquer/reference/plugins/).
`lacquer plugins` applies the manifest via `claude plugin marketplace add` /
`claude plugin install`, both confirmed idempotent (an already-configured
marketplace or already-installed plugin is a clean no-op). Only plugins
actually *enabled* on the reference machine are listed — one
installed-but-disabled there is a deliberate choice, not silently re-enabled
on a fresh machine.

```sh
lacquer plugins
```

This is how a fresh machine picks up the same plugin set an existing one
already has, closing the same "bootstrap a machine with none of this
preconfigured" gap that `[project].skills` closes for per-project skills.

## Installing

```sh
go install github.com/patrickserrano/lacquer/cmd/lacquer@latest
```

Or build from a checkout:

```sh
go build ./cmd/lacquer
```

Tagged releases (with prebuilt darwin/amd64 and darwin/arm64 binaries and
changelogs) are published automatically on [GitHub
Releases](https://github.com/patrickserrano/lacquer/releases) whenever
`VERSION` changes on `main`.

## Versioning

`VERSION` is semver and **machine-assigned — never edit it in a PR.** CI rejects
that, because two open PRs both bumping to the same number merge cleanly (both
sides make the identical change) and produce two different contents sharing one
version. Hand-picking also produced a permanent gap: `v0.69.0` does not exist,
because two bumps were missed and had to be corrected at once.

**Every merge with a material change releases.** [`version.yml`](.github/workflows/version.yml)
derives the next version from conventional commits with
[svu](https://github.com/caarlos0/svu) (pinned), writes `VERSION`, pushes, and
dispatches the release. `release.yml` tags `v<VERSION>` verbatim, so the file and
the tag cannot drift. There is no path filter — `VERSION` versions the whole
thing, CLI binary as much as synced content — and no loop, because a push made
with `GITHUB_TOKEN` does not trigger workflows.

| Commit | Bump |
|--------|------|
| `feat:` | minor |
| `fix:` | patch |
| `feat!:` / `BREAKING CHANGE:` | major |
| `docs:`, `chore:`, `ci:`, `refactor:`, `style:`, `test:` | **no release** |

A README or docs-site edit ships nothing to a project, so it mints no version.

**But prose under `core/` or `profiles/` is material** — projects consume it, so it
must release, and `docs:` would leave `lacquer status` telling them they're current
while the content moved. Commit those as `feat:`/`fix:` even though they read like
docs. CI enforces this rather than leaving it to discipline: a PR touching
`core/**` or `profiles/**` with a non-releasing type is rejected.

Because the repo squash-merges, **the PR title becomes the commit subject on
`main`** — so it is the PR title that CI validates, and the version is derived
from. A non-conventional title would otherwise compute no bump and silently
release nothing.

The PR **body** is load-bearing for the same reason, with a sharper edge: svu
decides "breaking" by substring-searching the commit body, not by parsing a
conventional footer — and that is the only check that reads the body at all
(`feat:`/`fix:` are matched against the subject alone). So *quoting* the marker
anywhere, even inside a code span or a table cell, declares a breaking change.
#89 proposed `1.0.0` off a `0.72.0` base because its description reproduced the
table above. CI now rejects a body carrying that marker unless the title also
declares it with `!`.

The project sits in `0.x` until a breaking change lands. `feat!:` /
`BREAKING CHANGE:` is the standardized, explicit way to declare one, so it
graduates the major — `0.x` → `1.0.0`, and `1.x` → `2.0.0` after that. If you need
to set a version by hand for any other reason, push it to `main` directly; the
workflow leaves a hand-set value alone.

The version is stamped into each managed region's marker so `lacquer status` can
report stamped-vs-latest. A project last synced before semver carries the old
integer form (`v70`); that reads as `0.70.0` and is re-stamped on its next sync.

## Managed regions

A **region** is a block the lacquer owns inside a file the project also owns.
Everything outside the markers is left untouched, which is what lets a project
keep its own content in the same file.

| File | Marker key | Comment form |
|------|-----------|--------------|
| `CLAUDE.md`, `AGENTS.md` (root) | `core` | `<!-- ... -->` |
| `<component>/CLAUDE.md`, `<component>/AGENTS.md` | the profile name | `<!-- ... -->` |
| `.gitignore` (root) | `gitignore` | `# ...` |
| `.gitattributes` (root) | `gitattributes` | `# ...` |

The `.gitignore` region carries the ignore rules that must not be a per-project
decision: App Store Connect keys (`*.p8`), signing material, `Secrets.xcconfig`,
`.env` and friends — with the committed templates (`Secrets.xcconfig.example`,
`.env.example`, `.env.schema`) re-included. It also names the third-party skill
trees installed from `[project].skills`, one by one, so the skills the lacquer
syncs into the same directories stay tracked and auditable. `skills-lock.json`
is ignored as local installation state.

Everything else in a project's `.gitignore` — `DerivedData/`, build outputs,
per-project junk — stays project-owned and survives every sync.

The `.gitattributes` region marks the agent-skill directories
(`.claude/skills`, `.codex/skills`, `.agents/skills`, whichever
`[project].tools` enables) as `linguist-vendored`. The lacquer ships ~207KB of
Python skill tooling into each of them, so a Swift repo taking the ios profile
picks up half a megabyte of code it did not write — and GitHub counted every
byte of it toward the language bar. One fleet repo read as more Python than
Swift. `linguist-vendored` suppresses the stats and collapses those paths in a
diff without untracking them, so `lacquer audit` can still see drift in a synced
skill.

This too is a region rather than a file, and for a sharper reason than
`.gitignore`: three fleet repos already keep real content in `.gitattributes` —
Git LFS filters, line-ending normalization, their own linguist overrides.
Shipping a whole file would have broken LFS.

## Docs

`docs/plans/` holds the design and build plans. The design doc
(`docs/plans/2026-06-15-lacquer-design.md`) carries an "Implementation status"
note distinguishing what's built from what's still aspirational.

## About

Built by [Patrick Serrano](https://patrickserrano.com), an iOS engineer
building apps under [PixelFox Studio](https://pixelfoxstudio.com). lacquer is
the internal tooling that keeps engineering practice consistent across the
whole fleet.
