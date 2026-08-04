---
name: working-with-lacquer
description: >
  Use when working in a lacquer-managed project (one with a .lacquer.toml) and
  anything about that management comes up: the "No lacquer drift" CI job fails,
  `lacquer audit` exits 3/4/6, a lint config or CI workflow you edited reverts
  on the next sync, a hook or workflow needs changing, the project can't meet a
  baseline yet, a new stack (Swift package, backend, web app) is added to the
  repo, or a project needs onboarding.
---

# Working With Lacquer

Lacquer renders shared content — CLAUDE.md regions, skills, commands, lint
configs, git hooks, CI workflows — from one repo into every project. A project
declares its shape in `.lacquer.toml`; `lacquer sync` writes the content;
`lacquer audit` checks the project against it and is wired into CI.

The rule that explains every mechanism below: **a lacquer-managed file is
identical to the lacquer's copy, or it is excluded. There is no third state.**
A quietly-edited copy is how one project's pre-commit hook ends up weaker than
its CI while looking healthy.

## Is this file mine?

```sh
lacquer audit    # classifies every managed unit, lists the ones that aren't clean
lacquer status   # each CLAUDE region's stamped version vs the lacquer's latest
```

If `audit` lists it, the lacquer owns it. Typically: `.swiftlint.yml`,
`.swiftformat`, `biome.json`, `deno.jsonc`, `.pre-commit-config.yaml`,
`lefthook.yml`, `.github/workflows/<profile>-*.yml`, `.claude/skills/**`,
`.claude/commands/**`, and the `<!-- lacquer:… -->` regions inside `CLAUDE.md`
/ `AGENTS.md`. Text *outside* those markers in a CLAUDE.md is the project's and
is preserved.

## Exit codes

| Code | Means | Fix |
|---|---|---|
| 0 | clean | — |
| 1 | the command failed (bad manifest, I/O, missing profile) | read the message |
| 2 | usage error | — |
| 3 | a managed file was edited locally; sync would clobber it | see below |
| 4 | a project baseline is not met | meet it, or time-box a relaxation |
| 5 | `lacquer doctor`: a check proved it cannot fail | fix the check |
| 6 | a stack on disk is not declared in `.lacquer.toml` | `lacquer adopt` |

## Recipes

**Exit 3 — "a lacquer-managed file was edited in this project."**
Decide which is true, then do that one:

- *The change should apply everywhere* → make it in the lacquer repo, open a PR
  there, then re-sync this project. This is the default and usually the right
  answer.
- *This project genuinely owns the file* → add it to `[project].exclude` with a
  comment saying why. The lacquer then neither distributes nor tracks it.
- *The edit was accidental* → `lacquer sync` (or `sync --force` to take the
  lacquer's version over yours).

**Exit 6 — "this project runs a stack .lacquer.toml does not declare."**
A whole toolchain is ungated: no hooks, no CI, no CLAUDE region. Run
`lacquer adopt` — it re-detects and records the stack, additively, preserving
the manifest's comments — then `lacquer sync`. If the path is deliberately
unmanaged (a fixture tree, a scratch package), add it to `[project].exclude`
instead.

**Exit 4 — the baseline.** The standard lives in the lacquer
(`profiles/<p>/baseline.toml`) and is inherited, not restated. A project that
cannot comply *yet* time-boxes it in its own manifest — both fields required, an
expired entry is a hard failure:

```toml
[baseline.relax]
swift_version = { until = "2026-09-01", reason = "pre-Swift-6 audio engine, #142" }
```

Keys: `swift_version`, `warnings_as_errors`, `strict_concurrency`,
`documentation`, `pgtap`.

**A stack the lacquer has no profile for** (Rust, Go, a bare SwiftPM package)
is reported by `audit` on every run and gates nothing — that gap is the
lacquer's. Closing it means adding `profiles/<name>/` upstream. Until then,
nothing is enforcing that code; say so rather than treating the repo as covered.

**Starting a new project.** Name the stack before the code exists, so both
halves are gated from the first commit rather than whichever half gets written
first:

```sh
lacquer init --list-stacks
lacquer init --stack ios-supabase
lacquer sync --fix          # --fix also runs the profiles' autofixers over existing source
```

Every command that reads shipped content needs `LACQUER_ROOT` pointing at the
lacquer checkout (`LACQUER_ROOT=~/Developer/lacquer lacquer sync`).

## Three things that look like fixes and are not

- **Editing the managed file and moving on.** It silently diverges this project
  from every other one, and the next sync reverts it — so the work is lost *and*
  the divergence was real while it lasted.
- **Setting `profiles = []` to quiet a report.** It changes nothing about what
  is enforced; it only stops the report. One repo's Swift sat a month with no
  hooks, no CI, and 191 tests run by nothing, because the manifest said the
  component existed and named no profile.
- **A relaxation with no expiry, or `--force` to make audit stop complaining.**
  A relaxation that cannot expire is a redefinition of the standard. `--force`
  is for adopting the lacquer's version over a local edit, not for silencing a
  finding.
