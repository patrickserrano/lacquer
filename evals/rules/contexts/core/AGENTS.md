# Engineering rules

- Read README.md, relevant docs/ files and nearby tests before changing code.
  Work only in the assigned worktree; preserve unrelated edits.
- Use atomic conventional commits and a pull request. Never push directly to main.
  Never force-push, including `--force` or `--force-with-lease` on git or gh.
  Never use `--no-verify` or `--admin` to bypass checks or branch protection.
  Never rebase a pushed branch; update it with `git fetch origin` then
  `git merge origin/main` in the assigned worktree.
- Never merge a PR marked "do not merge", or with failing or pending required
  checks. Merge only when authorized. After updating a branch, require new checks
  on its new head; a previous green SHA is not evidence for the current one.
- Wait on CI with `lacquer wait pr <N>`; never `gh pr checks --watch` or polling.
  Exit 0 is passed; 1 failed, 2 timed out, 3 no checks, 4 wait failed.
  Before a follow-up push run `lacquer ci-round begin <N>` with `--reason` for a
  failure fix or `--review` for requested changes. Stop on exit 10; never reset
  the round budget without human authorization.
- Never commit or log secrets. Commit examples, not real `.env`, `Secrets.xcconfig`,
  signing keys or credentials. Never put server/service-role keys in a client
  binary, public environment variable, bundle, response or log. Declare secret
  names rather than values in the manifest; use the configured secret store.
- Local checks must match CI strictness. Read `.github/workflows/` and the local
  check configuration; run their exact commands for the affected components.
  Treat warnings as errors. Do not suppress failures, hide stderr, add `|| true`,
  weaken checks or edit generated checks to get green. Fix shared configuration
  at its source or explicitly exclude project-owned files in `.lacquer.toml`.
- Add a regression test, prove it fails on the broken behavior, then passes with
  the fix. Build and run relevant tests. Zero selected tests, skipped checks and
  unreadable results are not success. Report commands, results and limitations.
- Keep build output inside the assigned worktree. Never kill a simulator or process
  this run did not create; record owned IDs and clean up only those resources.
- Preserve the PR number, branch, worktree, CI-round budget, verification evidence
  and next action across compaction. Resume from that state.
- In Codex, read `.codex/README.md` for guard activation and coverage limits.
  These rules still apply when runtime guards are inactive or cannot inspect a tool.
- Before briefing or starting work, run `lacquer decisions` and
  `lacquer decisions --fleet`: the operator's words, to quote verbatim in a brief.
