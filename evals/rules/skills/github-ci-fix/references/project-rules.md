## CI Hygiene

- Keep CI action/tool versions **consistent across all workflows** (one pin each for shared actions) — drift causes subtle job-to-job behavior differences.
- Update a branch from main before merging when it is behind; **after** updating, re-confirm the required checks re-ran green before merging (an update can drop a pending check).
- Never merge on partial signals: require every *required* check to pass and the merge state to be clean.

## CI round budget

Use `lacquer ci-round begin <N>` before pushing a follow-up to an open PR.
Failure-driven rounds need `--reason "<failed check and what changed>"`;
review-requested changes use `--review "<what was asked, and by whom>"` instead,
even on green CI. Both spend the same two-round budget (or the configured cap).
A push without `begin` spends an `unrecorded` round when `begin` or `status`
next observes it; it never refills the budget. Exception: a GitHub-created
update-branch merge is recorded as a neutral `update` entry, with no round spent
and no reset. The commit API must show committer email `noreply@github.com`,
exactly two parents, and the previous known head as one parent. Later observations
recognize that SHA without charging it; local merges and all other unknown heads
still spend an `unrecorded` round. Stop on exit 10 and surface the ACTION.
Only a human-authorized `lacquer ci-round reset <N> --reason "<why>"`
starts a fresh budget; never reset yourself to bypass the cap.
