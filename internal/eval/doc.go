//go:build eval

// Package eval is lacquer's behaviour eval suite: does an agent (or lacquer
// itself, standing in for one) following core/CLAUDE.core.md and this repo's
// own CLAUDE.md reach the RIGHT CONCLUSION when handed a specific, real repo
// state — not "does the Go code compile" but "does the answer come out right."
//
// # Why this exists
//
// The self-learning layer this repo ships (CLAUDE.md rules, memory files,
// issue write-ups) is prose. Nothing enforces it, so nothing proves it changes
// behaviour. `lacquer audit` itself demonstrated the failure mode it exists to
// name: 38 orphaned workflow files across 13 repos, reported correctly, for ten
// releases, while exiting 0 the whole time. The thing genuinely untested here
// was never the Go — internal/{shipped,audit,baseline,...} already have
// thousands of lines of it — it was "given this output, does whoever reads it
// conclude the right thing." That is what this package grades.
//
// # Vehicle: why a Go test harness, not `claude plugin eval`
//
// `claude plugin eval` (see `claude plugin eval --help`) is real and enabled in
// this environment, and was evaluated first per the brief. It was rejected for
// this suite:
//
//   - It targets a PLUGIN — a directory with a plugin manifest that
//     `claude plugin eval` can load (skills, MCP servers, an eval dir).
//     `find . -iname '*.claude-plugin*' -o -iname plugin.json` turns up nothing
//     in this repository outside stale worktrees. lacquer is a Go CLI
//     (module github.com/patrickserrano/lacquer) that ships rule files into
//     OTHER repos; it is not itself packaged as a Claude Code plugin, and
//     standing one up purely to host this eval suite is a restructuring the
//     brief did not ask for and this PR does not attempt.
//   - Even where it fits, `claude plugin eval` grades by spawning real agent
//     conversations per case (three runs per case by default) and scoring the
//     transcript with LLM or scaffold graders. That is exactly right for "does
//     an agent, given free rein, reach a good outcome" — but wrong for THIS
//     job, which needs a binary, deterministic, CI-cheap answer to "is the
//     ground-truth signal an agent would rely on even PRESENT and correct."
//     Several of these scenarios (stale-root, comment-match) are precisely
//     about whether lacquer's own tool output carries the information a
//     careful reader needs — a property of the Go code, gradeable in
//     milliseconds without an LLM call, and one that a flaky, non-deterministic,
//     paid agent run would only make harder to trust in CI.
//
// So: a Go test harness, reusing the fixture patterns already proven in
// internal/shipped/e2e_test.go (copy a committed project fixture, git-init it,
// drive the real CLI or the real internal packages against it) and in
// cmd/lacquer/fixture_test.go (`fixtureProject`, `realLacquer` — the actual
// checkout, not a stub, because #118 was a bug detection could only see against
// this repo's own layout). Where a scenario's defect lives in cmd/lacquer's
// dispatch wiring (not in an internal package function), the scenario builds
// the real `lacquer` binary once (see buildLacquer in harness.go) and execs it,
// rather than reaching into cmd/lacquer's unexported `run()` — that keeps this
// package import-clean and tests the artifact a person actually runs.
//
// # Non-blocking, on purpose
//
// Every file in this package (test and non-test) carries the `eval` build tag.
// `go build ./...`, `go vet ./...` and `go test ./... -race` — the existing
// REQUIRED job in .github/workflows/ci.yml — silently exclude a package whose
// files are all tag-gated out (verified: a multi-package module with one
// all-tag-gated directory builds, vets and tests clean at exit 0 with that
// directory simply absent from the matched set). Nothing in ci.yml changed to
// ship this. A SEPARATE, explicitly non-blocking job runs
// `go test -tags eval -v ./internal/eval/...`; see the eval-suite job's
// `continue-on-error: true` and the PR body for what turning it into a required
// gate would additionally take.
//
// # Rule for every scenario in this package
//
// "A scenario whose setup fails must FAIL, never skip silently" — per the
// brief, this is the exact defect class under test, so a setup helper in this
// package must never call t.Skip. If a helper cannot get to a known state (no
// lacquer checkout found, git missing, etc.) it calls t.Fatal. There is
// precedent for the OTHER choice nearby — cmd/lacquer/fixture_test.go's
// realLacquer skips when not run from a checkout — and this package
// deliberately does not copy that part of the pattern.
//
// # Part 2 (content correctness) — extension point, not built here
//
// Part 1 above grades BEHAVIOUR: given a repo state, is the right verdict
// reached. Part 2 — deliberately out of scope for this PR beyond one worked
// example — grades CONTENT: given a rendered profile, does the PROJECT ITSELF
// pass its own gates (no orphans on disk, no stale references, every required
// check resolvable, warnings-as-errors actually set on every target rather
// than merely documented). See content_test.go for the one worked example
// (sync the ios profile into a fresh fixture and assert baseline.Run reports
// zero blocking findings — i.e. the warnings-as-errors gate the profile
// documents is the gate a freshly synced project actually has) and its
// "EXTENSION POINT" comment for what a follow-up task would add: one such
// assertion per profile per gate class, run against every archetype
// initcmd.Run can produce, not just one hand-picked fixture.
package eval
