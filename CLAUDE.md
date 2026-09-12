# Working on the lacquer

This file governs work on **this repository**. The rule files it *ships* —
`core/CLAUDE.core.md` and `profiles/*/CLAUDE.*.md` — govern the projects that
receive them, and are mirrored to the site by `internal/docsmirror`. This one is
not shipped and has no mirror.

## The doctor principle applies inward

README's *"Proving the checks work"* states it plainly, about the checks this
tool ships:

> CI already tells you whether a check passes; it cannot tell you whether it
> *could* fail.

That is why `lacquer doctor` writes a known-bad fixture and asserts the check
**rejects** it. The principle is correct and it was not being applied to the code
that produces those checks. Three defects from 2026-09-11, each of which passed a
green test suite:

- A detector keyed on a managed step's **name** rather than on whether the
  declared secrets file was written. A project with a correct hand-rolled step
  under a different name was reported as exposing App Store credentials. It
  shipped, and was described to a human as a live risk before anyone re-read it.
- The warnings-as-errors gate resolved an xcconfig by walking the component
  directory. A lexical walk reaches `.claude/worktrees/...` before `Config/`, so
  an abandoned worktree's stale copy won. It reported **twelve violations against
  a fully compliant project** and would have refused every sync of it.
- The same gate refused a declared-but-absent xcodeproj. One project sits in that
  state deliberately, documented in its own manifest, because it is pre-code. The
  gate would have blocked the projects least able to act on it.

None was caught by review. Every one was caught by running the thing against
reality. Three rules follow, in yield order.

## 1. A detector does not merge without a fleet dry-run in the pull request

Run it against every managed repository, and paste the output into the PR
under a `## Fleet dry-run` heading. CI rejects a pull request that changes a
detector package without one.

The warnings-as-errors gate had a green suite and two defects. Both surfaced the
moment it ran against all fifteen iOS projects instead of against fixtures —
before them it would have blocked three repositories, after them it blocked none.
The step-name detector above shipped a false positive *because this was skipped*.

A dry-run costs one command. It is the highest-yield check in this document.

## 2. A guard is not tested until a mutation has failed a *named* test

Break the implementation, confirm a specific named test fails, restore, and
record which mutation broke which test.

This is not ceremony. It has found something nearly every time it has been run:

- Four tests written for an iOS fix passed against the **broken** implementation.
  One shared a fixture with the value it asserted; one had optional chaining
  flatten `Bool??`; one read the host app's `Bundle.main`; one survived a target
  losing its asset catalog.
- Of eight mutations against the promotable-bumps detector, **three survived the
  first pass**. The most serious: dropping the action-name check made it report
  `actions/setup-node` for an `evil/setup-node` line — a supply-chain swap
  presented as a routine version bump.
- Of nineteen against the protection checker, two earlier rounds caught nothing
  and forced better fixtures before they bit.

A test that passes against a broken implementation is not a test. It is the
defect class in issue #333 wearing a test's clothes.

## 3. Anything infrastructural proves on one repository before the fleet

Runner labels, workflow topology, anything that renders into every managed repo.
A pull request that changes `profiles/*/workflows/` names the repository and
run it was proven on under a `## Proven on` heading; CI rejects it otherwise.

`blacksmith-2vcpu-ubuntu-2404-arm` shipped fleet-wide having been proven on zero
repositories. No runner is ever assigned to it on the account that mattered, and
the failure mode is the worst available: the job does not go red, it **queues**.
A required check that never resolves reads as *"CI is slow today"* rather than as
breakage, so it blocks every merge while looking healthy, and every repository
inherits it silently at its next sync with no symptom until someone opens a pull
request.

It shipped carrying a release note that said it was untested in production. The
note was accurate and prevented nothing. **A written caveat is not a gate.**

## Read the output of your own commands

A meaningful share of the cost above was not product defects but misread tool
output. These are specific to this machine and they recur:

- **Compound commands report the LAST command's status.** `go build ./... | head
  && echo ok` prints `ok` after a failed build; `cmd | tail; echo $?` reports
  `tail`'s status. Use an explicit `if`, not `&&`, whenever the answer matters.
- **`cp`, `mv` and `rm` are aliased to `-i`.** An overwrite silently prompts and
  does nothing, so a "restore" can leave a stale file in place — that corrupted a
  profile workflow here. Use `command cp -f`, and verify the restore.
- **zsh does not word-split unquoted variables.** `probe $repos` passes one
  argument, so a fleet sweep silently checked nothing and reported zero problems.
  Use an array.
- **Do not read indentation off piped output.** `sed 's/^/  /'` adds two spaces;
  anchors copied from it fail to match. Measure the file.
- **A flag after a script name is passed to the script.** `pnpm run lint
  --silent` hands `--silent` to biome, which rejects it — inventing three
  failures that were then investigated as real.

## What not to add

More review stages. The step-name false positive survived careful reading by two
parties and shipped anyway. The leverage here is measurement and mutation, not
more eyes.

## The question underneath all of it

Every expensive failure in this repository has been **a state indistinguishable
from working**: a skipped required check that satisfies branch protection, a job
queued forever on a label no runner matches, a selector matching zero tests and
exiting 0, a sync reporting success while writing stale content, a test asserting
nothing.

So the useful question about any check, including the ones in this repository, is
not *does it pass* but:

> Can this distinguish "verified and passing" from "never ran"?

If it cannot, it is not a check. Issue #333 tracks the corpus.
