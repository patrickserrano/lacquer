Core section references below now live in skills: Fundamental Rules in
`engineering-workflow`, Documentation in `project-documentation`, and Local
Checks Match CI in `working-with-lacquer`. Load the named skill when needed.

## Docs Taxonomy

A project starts from a **brief** at `docs/brief.md` — the pitch, scope, and
roadmap, the human-authored source of truth for what's being built. `lacquer
init` scaffolds a stub; paste the real brief there first. Feature work then flows
through three dated doc types named `YYYY-MM-DD-<feature>-<type>.md`:

- **Brief** (the product pitch, scope, roadmap — the source of truth) → `docs/brief.md`
- **PRD** (product requirements — the *what* and *why*) → `docs/prds/`
- **PCD** (product/component design — the *how*, UX + technical shape) → `docs/pcds/`
- **Plan** (bite-sized implementation tasks) → `docs/plans/`

Derive the PRD from the brief, then the PCD, then the Plan. Keep each artifact in
its dated file so history is auditable.

## Documentation

**Every declaration carries a doc comment, and the docs build clean.** This is a
baseline like warnings-as-errors, not a style preference. It is not a CI gate at
all any more, in two steps: the `Docs` job was dropped from every stack's
PR-blocking CI (a dedicated self-hosted Mac runner shared by the whole fleet
spent more time queued behind it than doing anything else), and the nightly
publish workflows that inherited the check were then removed too, because they
spent Actions minutes on a site nobody was reading. **The local hook is the only
thing that checks this now**, which is exactly why weakening it is the one way
the baseline stops existing.

Two halves, because neither implies the other:

1. **It exists.** Every declaration above `private` has a doc comment.
2. **It resolves.** The docs actually build: every symbol link points at
   something real, and the markup parses. A doc comment referring to a type that
   was renamed three refactors ago is worse than no comment — it is confidently
   wrong.

Each stack enforces this with its native toolchain, from that stack's own
pre-commit or pre-push hook:

| Stack | Checked with |
|-------|--------------|
| iOS / Swift | SwiftLint `missing_docs`. Half 1 only — see below |
| Web / TypeScript | TypeDoc `validation.notDocumented` + `invalidLink` |
| Supabase / Deno | `deno doc --lint` |

**iOS currently checks half 1 and not half 2.** `xcodebuild docbuild` ran from
the `Docs` job, never from a hook — a doc build on the shared Mac runner is the
one check in this table that costs minutes rather than seconds — so when the job
went, `scripts/build-docs.sh` was left with no caller and has now been unshipped
with it. Nothing resolves Swift symbol links today. Say so rather than letting
the table imply otherwise; a stack listed as checked when it is not is how a
baseline quietly stops being one.

**Write the comment for the reader who does not already know.** Say what the
thing is for and what a caller must know — preconditions, ownership, units,
what happens on failure. Do not restate the signature: `/// Sets the name.` on
`setName(_:)` costs a line and teaches nothing. If the only honest doc comment
is a restatement, that is a signal the name is doing its job and the *type* or
*module* is where the explanation belongs.

### Nothing publishes a docs site any more

**There is no hosted API documentation, for any stack.** No workflow writes a
`gh-pages` branch, no site is served, and `scripts/publish-docs.sh` is not
shipped.

**Unshipped means the lacquer stopped MANAGING it. It does not mean nothing
calls it, and it is not an instruction to delete it.** `scripts/build-docs.sh` is the live
example: in `dick-passport`, `flare`, `kit` and `skein`, `ios-docs.yml` runs it
in CI and `.pre-commit-config.yaml` runs it on every commit, so deleting it
breaks working pipelines.

**Before removing any unshipped file, grep for its path and its basename.** The
audit annotates each entry with what still references it — a file marked
`STILL REFERENCED by …` has a live caller, and one marked
`referenced by nothing tracked` is the safe case, though a caller that builds
the path dynamically will not be found by either check.

Only the publishing went — the doc comments are still required on every stack,
and the docs build still runs from the hook on web and Supabase.

**If you ever bring publishing back, do not turn GitHub Pages on for a private
project.** A Pages site is served publicly even when its repository is private —
that is the plan's behaviour, not a misconfiguration — so enabling it publishes
the API documentation, and with it the internal type and module names, to anyone
with the URL. Private Pages needs an Enterprise plan. The same goes for any deployer
that copies the internal tree onto a public edge network, such as Cloudflare
Workers.

**A project that cannot comply yet relaxes it — time-boxed, never open-ended.**
Same mechanism as every other baseline key, in the project's own `.lacquer.toml`:

```toml
[baseline.relax]
documentation = { until = "2026-11-01", reason = "legacy Core/, tracked in #212" }
```

Both fields are required, the checks still run and still report while relaxed,
and **an expired relaxation is a hard failure** — so the debt stays visible and
greppable instead of becoming policy by default. This is the one exception to
Fundamental Rule #7: it is a deliberate, dated, justified opt-out recorded in the
manifest, not an inline suppression hidden at the call site.
