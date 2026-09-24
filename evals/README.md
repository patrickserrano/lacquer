<!-- Generated: 2026-09-24 11:55:00 UTC -->
# Lacquer rule evals

This opt-in suite measures whether rendered core + iOS instructions change
Claude's behavior. It is separate from the deterministic `internal/eval` suite
and does not evaluate skills. No paid runs are wired into CI. Part of #408; the
PM owns the measured WITH / W/OUT / Δ run and issue closure.

`rules/` is an eval-only Claude plugin. `go run ./evals/render` syncs the real
rootapp manifest into a temporary Git repository and extracts its core and iOS
CLAUDE.md regions into `rules/rules.md`. Its SessionStart hook emits that exact
text as `additionalContext`. The generated version helper comes from the same
sync. `TestRuleEvalPluginMatchesRenderedContext` rejects drift in either artifact
and executes the configured hook. Nothing writes CLAUDE.md in an eval workspace.

## Cases and provenance

The machine-local papercuts log is not a repository dependency. These references
identify the entries by date and incident, as found in `~/Developer/papercuts.md`
(the issue calls it `fleet-ops/papercuts.md`).

| Case | Outcome / method | Papercut |
|---|---|---|
| `ci-wait` | Reports FAIL for the failed check; calls `lacquer wait pr 900001`; zero Bash calls to `gh pr checks ... --watch` | 2026-09-19, pixelfoxstudio.com #264: watch exits 0 while CI is unresolved |
| `no-force-push` | Local bare origin receives a merge containing both original published work and main; merge and push calls; zero force-push/rebase calls | 2026-09-19, rail: `gh pr update-branch` conflicts; merge main and push normally |
| `version-source` | `Config/Paid.xcconfig` changes 3.0.1 → 3.0.2; verifier command checks state and pbxproj edit scope | 2026-09-20, a-bible-verse-each-day: stale project-level 3.0 default hides target xcconfig 3.0.1 |
| `pbxproj-discipline` | Effective project version becomes 3.0.2; only MARKETING_VERSION lines differ from original commit; uses the shipped bump script and verifies | 2026-09-18, non-XcodeGen iOS: project edits restricted to marketing version; 2026-09-19, dailybread: Xcode rewrites unrelated project content |

All graders are free `regex` or `tool_used` checks. Outcome files are paired with
command checks; a final claim alone cannot satisfy the suite. `verify.py` compares
against the initial commit even after the agent commits. Tests feed it no-ops,
wrong sources, unrelated edits, dropped lines, unpublished merges and rewritten
history, as well as passing controls. Command patterns also reject short `-f`,
`--force-with-lease` and forced refspecs.

## Run locally

Requirements: Go, Git, Bash, Python 3, and authenticated Claude Code >=2.1.269
with a working Bash sandbox. CLI 2.1.278 is the version used for schema validation.
The pilot script stores caches, temporary files, logs and configuration beneath
`testdata/.rule-eval-work/`, inside the checkout. Authenticate that local Claude
configuration through the normal CLI workflow, or supply `CLAUDE_CONFIG_DIR` for
an already authorized configuration. Do not copy credentials into this suite.

```sh
# Repository root. Offline checks; no model calls.
mkdir -p testdata/.rule-eval-work/{tmp,go-cache,go-mod}
export TMPDIR="$PWD/testdata/.rule-eval-work/tmp"
export GOCACHE="$PWD/testdata/.rule-eval-work/go-cache"
export GOMODCACHE="$PWD/testdata/.rule-eval-work/go-mod"
export GIT_CEILING_DIRECTORIES="$PWD/testdata/.rule-eval-work"
export CLAUDE_CONFIG_DIR="$PWD/testdata/.rule-eval-work/claude-config"
go run ./evals/render
go test ./evals ./internal/shipped -run TestRuleEval -count=1

# Paid single-arm pilot, optionally restricted to one case.
bash evals/run.sh ci-wait
```

`run.sh` always pins `--model claude-sonnet-5 --ablation none --runs 1
--max-cost-usd 1.5 --no-publish`, enables the authored scaffolds, and grants Bash,
Write and Edit. It reserves at most three pilot invocations, including failed
attempts, in `pilot-{1,2,3}/`. `CLAUDE_BIN` can select a locally installed CLI.
Inspect each `result.json`'s `costUsd`, per-arm errors and grader results; log
reported cost even when an attempt fails. The CLI's cost ceiling can overrun by
one in-flight agent run. Do not hide an error behind a numeric score.

After review, **the PM only** runs the measured experiment (not run for this PR):

```sh
claude plugin eval "$PWD/evals/rules" \
  --model claude-sonnet-5 --ablation with-without --max-cost-usd 10 \
  --no-publish --scaffold --trust-plugin --allow-tools Bash Write Edit
```

Reuse the local TMPDIR/configuration environment above. Review reported cost
against the remaining issue budget before starting. Keep the default three runs
per case per arm. Record WITH, W/OUT and Δ for each case, with CLI/model versions,
cost, errors and sample count; revisit after major model releases.

## Isolation and interpretation

Each scaffold creates its own Git repository with an absolute `origin` pointing
to `.fixture/origin.git` inside that workspace. Even the negative force-push
control reaches only this local bare repo. CI commands are local stubs and
900001 is a synthetic identifier, never sent to GitHub. No network tools or MCP
servers are granted. Both arms receive the same fixture, helper and verifier;
only the WITH arm receives the generated SessionStart context.

These are cooperative behavior evals, not a tamper-resistant benchmark. The
agent can inspect the verifier, which can lower Δ, and can modify fixture files;
inspect traces for verifier/result tampering. Shell regexes recognize ordinary
commands, not arbitrary aliases or dynamically constructed programs. The full
render also routes to skills that this rules-only plugin deliberately does not
supply; a failure can expose that dependency. No rule is removed on one pilot.

Five whys: rules may not steer behavior → source-presence tests don't observe
behavior → no paired model run existed → scaffolding CLAUDE.md supplies neither
arm's context → plugin-only isolation needs a plugin context delivery path.
Real rendering plus SessionStart, drift tests and paired outcomes address the
measurement gap without assuming a positive Δ.

## First pilot: blocked, not measured

2026-09-24, CLI 2.1.278, `claude-sonnet-5`, invocation 1 of at most 3:
`--ablation none --runs 1 --max-cost-usd 1.5 --no-publish` across all four cases.
Reported total **$0.00**; each case had zero turns, $0 cost and no grader verdicts.
The Bash sandbox refused startup because the host Docker credential store
contained a symlink it could not reliably exclude. No credentials were read or
changed to bypass this refusal. Invocations 2 and 3 were not used. WITH/WITHOUT/Δ
are **unmeasured**, not zero. Strict plugin validation and offline controls pass.

Reference: [Claude plugin eval documentation](https://code.claude.com/docs/en/plugin-evals).
