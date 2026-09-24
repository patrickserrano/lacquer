Core section references below now live in skills: Fundamental Rules in
`engineering-workflow`, Documentation in `project-documentation`, and Local
Checks Match CI in `working-with-lacquer`. Load the named skill when needed.

## Local Checks Match CI

**A local hook runs the same command, with the same strictness, as the CI job it
stands in for.** A hook that is weaker than CI is worse than no hook: it reports
green, you push, and CI fails on something the hook already had in its hands.

Two rules follow, and both are mechanical rather than aspirational:

1. **Never weaken a hook to make it pass, and never hide its errors.** No
   `|| true`, no dropping `--strict`, no `2>/dev/null`, no `continue-on-error`.
   Those turn a gate into a log line. If a check is too slow for pre-commit, move
   it to pre-push — don't defang it.

   Suppressing stderr is the most dangerous of these, because it hides the
   *tool* failing, not just the code. A lacquer editor hook invoked SwiftLint
   with an option that had been removed; it errored on every write for months,
   `2>/dev/null || true` ate the message, and it read as a clean pass the whole
   time. A hook that cannot fail and cannot complain is not a hook.
2. **Adding a CI gate means adding its local counterpart in the same change**,
   or deciding out loud that it belongs only in CI (a full archive, a database
   lint needing a live server). Each profile's rules carry a table of every CI
   job and where it runs locally; a new job adds a row.

**Lacquer-managed files are identical or excluded — there is no third state.**
(The `working-with-lacquer` skill carries the full manifest reference — exit
codes, exclusions, relaxations, retirement, fleet sweeps. Load it when you are
actually resolving one of these, rather than working from what follows.)
The files that carry these checks (`.pre-commit-config.yaml`, `lefthook.yml`,
the CI workflows, the lint configs) are rendered from the lacquer, so editing one
in a project silently diverges it from every other project. The `No lacquer
drift` CI job runs `lacquer audit` and fails on exit 3 when a managed file was
edited locally — and on exit 6 when the project runs a **stack** the manifest
never declared, which is the same failure one level up: a whole toolchain with
no hooks, no CI, and no CLAUDE region, reported by nothing. Run `lacquer adopt`
to record it. If a project genuinely owns a file, say so:

```toml
[project]
exclude = [
  # Permanent: a real, ongoing difference between this project and the fleet.
  { path = "lefthook.yml", reason = "monorepo runs hooks from the workspace root" },
  # Temporary: debt with a term. Past `until`, audit fails with exit 4.
  { path = ".github/workflows/ios-ci.yml", reason = "local xcresult fix pending upstream", until = "2026-10-01" },
]
```

The lacquer then neither distributes nor tracks it. That is a real, supported
choice; a quietly-edited copy is not. **The `reason` is a field, not a comment** —
`audit` reports every exclusion that lacks one, and `until` is what separates
"we differ" from "we haven't got to it yet". Omit `until` only when no future
date could make the exclusion wrong; an invented date you renew forever teaches
the next reader that dates in this file mean nothing. An exclusion that stops
matching anything the lacquer ships is reported as stale so it can be deleted,
because dead config reads exactly like a live decision. This job is what turns "someone's hook
drifted six months ago" into a failing check on the PR that does it.

> A project that has never been synced has no `.lacquer.lock`, so drift cannot
> be attributed and nothing can block. The job warns instead of reporting a pass
> — run `lacquer sync` to establish the baseline.

### Refusing a dependency update

**`.github/dependabot.yml` offers every update; the only thing ever withheld is
one that cannot be merged at all.** Volume is managed by grouping — minor and
patch arrive as one PR per ecosystem, majors stay individual — because grouping
changes how many PRs carry the updates, not which updates are offered.

Some updates genuinely cannot be taken. A docs generator whose newest release
peers at the previous major of its compiler doesn't produce a noisy PR; it
produces a hard crash before any work happens, on every upstream release,
forever. Say so in the component that has the problem:

```toml
[[component]]
path = "admin"
stack = "web"
dependabot_ignore = [
  { dependency = "typedoc", versions = ["0.29.x"], reason = "crashes on the compiler's new major — upstream issue 1234", until = "2026-11-30" },
]
```

That renders into the generated `.github/dependabot.yml` as a real Dependabot
`ignore` rule, carrying the reason and the date as comments so the next reader
of that file doesn't have to go find the manifest.

**Every field is required, and there is no permanent form.** This is the one
place the rules are stricter than `[project].exclude`, which does permit an
undated entry: a macOS-only app really does differ from the fleet forever, but no
incompatibility does. It ends when upstream ships, when the pin is dropped, or
when the project accepts the breakage — and without a date nobody ever asks which
happened. Past `until`, `audit` fails with exit 4, exactly like an expired
exclusion. An ignore naming a dependency the component doesn't actually declare
is reported as stale, the same way an exclusion that suppresses nothing is.

There is deliberately **no `update_types` field**, though Dependabot has one, and
wildcards in the dependency name are rejected. Those are how an ignore quietly
becomes a volume control — one line hiding every minor and patch in an ecosystem,
forever. Name the versions that are broken. If what you want is fewer PRs, the
lever is grouping, and it's already pulled.

### Retiring a project

**A project that is no longer worth investing in is retired, not abandoned.**
Abandoning it leaves a repository that silently rots out of the fleet while its
nightly jobs keep running and keep billing. Retiring it says so in the manifest:

```toml
[project]
retired = { since = "2026-08-18", reason = "not a viable app" }
```

Retired means **stop the spend, stay consistent.** The lacquer keeps syncing
everything that holds the repo to the fleet's shape — PR-triggered CI, lint and
format configs, `CLAUDE.md` / `AGENTS.md`, `.gitignore`, `.gitattributes`, hooks,
skills — so the project still audits clean and can be picked back up. What it
stops shipping is everything that costs money or attention **on a schedule**:
every workflow whose `on:` block carries a `schedule:` trigger, and
`.github/dependabot.yml`.

That set is derived from each workflow's **content**, never from a list of
filenames — a filename list is correct right up until someone adds the next
scheduled workflow, and then silently is not. A workflow that declares
`workflow_dispatch:` beside `schedule:` still goes: the dispatch entry is a
convenience, the cron is the point.

Both fields are required and a malformed entry is a hard error, for the same
reason `[baseline.relax]` needs both: `retired = true` records that someone
retired the project and not why, and six months later why is the only thing
anyone wants to know. Unlike a relaxation or a dated exclusion, **retirement has
no `until`** — it is not debt with a term, it is a decision already made. An
expiry would either be rubber-stamped forever or would quietly un-retire a dead
project and turn its cron jobs back on.

`lacquer status` and `lacquer audit` both lead with the retirement and its date,
so a retired project is never read as a healthy one. Neither deletes anything:
files already in the repo stay until someone removes them by hand.

## Warnings as Errors

Treat compiler and linter warnings as errors — ship zero-warning builds. Don't
suppress a warning; fix the code (see Fundamental Rule #7).

This is mechanically enforced, not left to judgement: the project baseline
(`lacquer audit`, plus the stack's CI `Baseline` job) requires warnings-as-errors
in **every** build configuration and fails the build when it is missing from any
of them. Setting it on the main target and leaving it off the tests or extensions
reports as a violation with the ratio, not as a pass. If a project genuinely
cannot comply yet, add a time-boxed `[baseline.relax]` entry to `.lacquer.toml`
with a reason — an expired relaxation is a hard failure, so the debt stays
visible rather than becoming policy by default.
