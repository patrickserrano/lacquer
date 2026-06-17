# Absorb ios-template Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Fill the harness `core/` and `profiles/ios/` (and seed `profiles/web/`) with the real content currently in `~/developer/ios-template`, so `harness sync` distributes genuine rules, skills, commands, CI, and configs — then verify it end-to-end and retire the standalone template's authority.

**Architecture:** This is content migration, not new Go code. The classification below is the design decision (already approved). The CLAUDE.md split is judgment-heavy: the iOS body must be **de-tokenized** (no `{{PROJECT_NAME}}`-style placeholders — shared profile content is identical across all iOS projects), and project-identity tables are **not** migrated (they remain each project's own, outside managed regions). After migration, bump `VERSION` and verify a full sync into a throwaway git project.

**Tech Stack:** No code changes expected. Build/verify with `env -u GOROOT /opt/homebrew/bin/go`. Source of truth for content: `~/developer/ios-template`.

---

## Classification (the approved split)

**`core/` (universal, every stack):**
- `core/CLAUDE.core.md` — Fundamental Rules 1–10, Extended Thinking table, context-management basics (`/compact`, `/clear`), Docs taxonomy (PRD/PCD/Plan), CI hygiene, the critical-review/adversarial-diff pattern, warnings-as-errors. De-flowdecked (flowdeck specifics → iOS).
- `core/skills/`: `caveman`, `github-ci-fix`, `github-issue-fix-flow`
- `core/commands/`: `sync-worktree.md`

**`profiles/ios/` (Swift/Xcode/SwiftUI):**
- `profiles/ios/CLAUDE.ios.md` — Xcode prohibitions, App Store reqs, flowdeck build/test tooling, Architecture (View→ViewModel→Service), Swift Testing, Swift 6 concurrency, iOS 26 gotchas, battery patterns, URL validator, accessibility/design tokens, subscription gating. **De-tokenized to generic phrasing.**
- `profiles/ios/skills/`: the 20 iOS skills (`app-store-screenshots`, `core-data-expert`, `ios-debugger-agent`, `native-app-profiling`, `release-app-store-changelog`, `rocketsim`, `swift-concurrency`, `swift-concurrency-expert`, `swift-testing-expert`, `swiftui-expert-skill`, `swiftui-liquid-glass`, `swiftui-performance-audit`, `swiftui-ui-patterns`, `swiftui-view-refactor`, `update-swiftui-apis`, `xcode-build-benchmark`, `xcode-build-fixer`, `xcode-build-orchestrator`, `xcode-compilation-analyzer`, `xcode-project-analyzer`)
- `profiles/ios/commands/`: `build.md`, `lint-fix.md`, `test.md`, `new-feature.md`
- `profiles/ios/workflows/` (stored WITHOUT an `ios-` prefix; sync re-prefixes → `ios-<file>`): `ci.yml`, `release.yml`, `dead-code.yml`, `cleanup-ci.yml`, `dependency-audit.yml`, `quality-review.yml`
- `profiles/ios/config/`: `.swiftlint.yml`, `.swiftformat`, `.periphery.yml`

**`profiles/web/` (seed the web profile):**
- `profiles/web/skills/`: `supabase-postgres-best-practices` (database guidance — neither universal nor iOS; seeds the web/backend profile the dashboards/proxies will use)

**Deferred (the sync engine has no asset category for these yet — note as a known limitation, do NOT migrate now):**
`Brewfile`, `.pre-commit-config.yaml`, `.mcp.json`, `.claude/scripts/allow_mcp.js`, `scripts/check-secrets.sh`, `AGENTS.md`, `FIRST_RUN.md`, `README.md`. These need a future "root-level files + scripts" asset category. The `FIRST_RUN.md` flow becomes `harness init` in a later milestone.

---

## Task 1: Migrate core skills + commands

**Step 1:** Copy the three core skills and the one core command from the template into the harness.

```bash
T=/Users/patrickserrano/Developer/ios-template
H=/Users/patrickserrano/Developer/harness
mkdir -p "$H/core/skills" "$H/core/commands"
for s in caveman github-ci-fix github-issue-fix-flow; do
  cp -R "$T/.claude/skills/$s" "$H/core/skills/$s"
done
cp "$T/.claude/commands/sync-worktree.md" "$H/core/commands/sync-worktree.md"
```

**Step 2:** Verify the three skills and one command landed, each skill has a `SKILL.md`.

```bash
ls "$H/core/skills" && ls "$H/core/commands"
find "$H/core/skills" -name SKILL.md | wc -l   # expect 3
```

**Step 3:** Spot-check that the copied core skills carry no `{{...}}` tokens or iOS-only assumptions.

```bash
grep -rl '{{' "$H/core/skills" "$H/core/commands" || echo "no tokens (good)"
```

If any token or hard iOS assumption appears in a "core" skill, STOP and reclassify it to `profiles/ios` instead — core must be stack-agnostic.

**Step 4: Commit**

```bash
git add core/skills core/commands
git commit -m "feat(content): migrate core skills and commands from ios-template"
```

---

## Task 2: Migrate iOS skills + commands

**Step 1:** Copy the 20 iOS skills and 4 iOS commands.

```bash
T=/Users/patrickserrano/Developer/ios-template
H=/Users/patrickserrano/Developer/harness
mkdir -p "$H/profiles/ios/skills" "$H/profiles/ios/commands"
for s in app-store-screenshots core-data-expert ios-debugger-agent native-app-profiling \
         release-app-store-changelog rocketsim swift-concurrency swift-concurrency-expert \
         swift-testing-expert swiftui-expert-skill swiftui-liquid-glass swiftui-performance-audit \
         swiftui-ui-patterns swiftui-view-refactor update-swiftui-apis xcode-build-benchmark \
         xcode-build-fixer xcode-build-orchestrator xcode-compilation-analyzer xcode-project-analyzer; do
  cp -R "$T/.claude/skills/$s" "$H/profiles/ios/skills/$s"
done
for c in build lint-fix test new-feature; do
  cp "$T/.claude/commands/$c.md" "$H/profiles/ios/commands/$c.md"
done
```

**Step 2:** Verify counts.

```bash
ls "$H/profiles/ios/skills" | wc -l     # expect 20
ls "$H/profiles/ios/commands" | wc -l   # expect 4
find "$H/profiles/ios/skills" -name SKILL.md | wc -l  # expect 20
```

**Step 3: Commit**

```bash
git add profiles/ios/skills profiles/ios/commands
git commit -m "feat(content): migrate iOS skills and commands from ios-template"
```

---

## Task 3: Migrate iOS workflows + configs; seed web profile

**Step 1:** Copy workflows (renamed to drop redundant `ios-` prefixes; sync re-prefixes), configs, and the one web skill.

```bash
T=/Users/patrickserrano/Developer/ios-template
H=/Users/patrickserrano/Developer/harness
mkdir -p "$H/profiles/ios/workflows" "$H/profiles/ios/config" "$H/profiles/web/skills"
cp "$T/.github/workflows/ios-ci.yml"                      "$H/profiles/ios/workflows/ci.yml"
cp "$T/.github/workflows/ios-release.yml"                 "$H/profiles/ios/workflows/release.yml"
cp "$T/.github/workflows/dead-code.yml"                   "$H/profiles/ios/workflows/dead-code.yml"
cp "$T/.github/workflows/cleanup-ci.yml"                  "$H/profiles/ios/workflows/cleanup-ci.yml"
cp "$T/.github/workflows/scheduled-dependency-audit.yml"  "$H/profiles/ios/workflows/dependency-audit.yml"
cp "$T/.github/workflows/scheduled-quality-review.yml"    "$H/profiles/ios/workflows/quality-review.yml"
cp "$T/ios/.swiftlint.yml"  "$H/profiles/ios/config/.swiftlint.yml"
cp "$T/ios/.swiftformat"    "$H/profiles/ios/config/.swiftformat"
cp "$T/ios/.periphery.yml"  "$H/profiles/ios/config/.periphery.yml"
cp -R "$T/.claude/skills/supabase-postgres-best-practices" "$H/profiles/web/skills/supabase-postgres-best-practices"
```

**Step 2:** Verify.

```bash
ls "$H/profiles/ios/workflows"   # 6 files
ls -a "$H/profiles/ios/config"   # 3 dotfiles
ls "$H/profiles/web/skills"      # supabase-postgres-best-practices
```

**Step 3:** Scan workflows for `{{...}}` tokens. Workflow YAML legitimately may reference repo-specific values; if tokens exist, note them — they will need handling when `init`/`scaffold` lands, but for now they sync verbatim. Record any found in the commit message.

```bash
grep -rl '{{' "$H/profiles/ios/workflows" || echo "no tokens"
```

**Step 4: Commit**

```bash
git add profiles/ios/workflows profiles/ios/config profiles/web/skills
git commit -m "feat(content): migrate iOS workflows/configs and seed web profile"
```

---

## Task 4: Split CLAUDE.md into core + iOS bodies (judgment-heavy)

This replaces the placeholder `core/CLAUDE.core.md` and `profiles/ios/CLAUDE.ios.md` with real content split from the template's `CLAUDE.md` (`~/developer/ios-template/CLAUDE.md`).

**Rules for the split:**
- **`core/CLAUDE.core.md`** gets only stack-agnostic content: Fundamental Rules 1–10, Extended Thinking table, context-management basics, Docs taxonomy, CI hygiene, the critical-review pattern (re-phrased so its examples aren't audio/Swift-specific), warnings-as-errors-as-a-principle.
- **`profiles/ios/CLAUDE.ios.md`** gets everything Swift/Xcode/SwiftUI/Apple.
- **De-tokenize the iOS body:** replace `{{PROJECT_NAME}}`, `{{SCHEME}}`, `{{BUNDLE_ID}}`, `{{DEPLOYMENT_TARGET}}`, etc. with generic phrasing ("your app's scheme", "the project's `.xcodeproj`"). The shared profile body must read correctly for ANY iOS project.
- **Do NOT migrate** the Project Identity table or any per-project values — those live in each project's own CLAUDE.md (handled by `init`/`scaffold` later).
- Neither body may contain the literal harness marker strings (`<!-- harness:...:start/end -->`) — `region.Merge` rejects a body containing its own markers.

**Step 1:** Read `~/developer/ios-template/CLAUDE.md` in full and draft the two bodies per the rules above. Write them to `core/CLAUDE.core.md` and `profiles/ios/CLAUDE.ios.md` (overwriting the `CORE RULES` / `IOS RULES` placeholders).

**Step 2:** Verify no tokens or markers leaked.

```bash
H=/Users/patrickserrano/Developer/harness
grep -n '{{' "$H/core/CLAUDE.core.md" "$H/profiles/ios/CLAUDE.ios.md" && echo "TOKENS REMAIN — fix" || echo "no tokens (good)"
grep -n 'harness:.*:start\|harness:.*:end' "$H/core/CLAUDE.core.md" "$H/profiles/ios/CLAUDE.ios.md" && echo "MARKER LITERAL — fix" || echo "no markers (good)"
```

Both checks must report the "good" branch.

**Step 3:** Sanity-read both bodies top to bottom. The core body must make sense for a Rust or web project (no Swift assumptions). The iOS body must make sense for an arbitrary iOS app (no project-specific names).

**Step 4: Commit**

```bash
git add core/CLAUDE.core.md profiles/ios/CLAUDE.ios.md
git commit -m "feat(content): split ios-template CLAUDE.md into core + ios profile bodies"
```

---

## Task 5: Bump VERSION and verify end-to-end sync

**Step 1:** Bump the harness version (content changed, so projects are now behind).

```bash
H=/Users/patrickserrano/Developer/harness
printf '2\n' > "$H/VERSION"
```

**Step 2:** Build and run a full end-to-end sync into a throwaway git project with an iOS component.

```bash
env -u GOROOT /opt/homebrew/bin/go build -o "$H/bin/harness" ./cmd/harness
tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
printf '[project]\nname="probe"\n\n[[component]]\npath="ios"\nprofiles=["ios"]\n' > "$tmp/.harness.toml"
printf '# probe\n\nlocal note\n' > "$tmp/CLAUDE.md"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync )
echo "--- root CLAUDE.md head ---"; head -20 "$tmp/CLAUDE.md"
echo "--- ios CLAUDE.md present? ---"; test -f "$tmp/ios/CLAUDE.md" && echo yes
echo "--- skills synced ---"; ls "$tmp/.claude/skills" | wc -l   # core(3) + ios(20) = 23
echo "--- commands synced ---"; ls "$tmp/.claude/commands"       # sync-worktree + build/lint-fix/test/new-feature = 5
echo "--- workflows synced ---"; ls "$tmp/.github/workflows"     # 6 ios-*.yml
echo "--- ios configs ---"; ls -a "$tmp/ios" | grep -E '\.(swiftlint|swiftformat|periphery)'
echo "--- status ---"; ( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" status )
rm -rf "$tmp"
```

Expected: root `CLAUDE.md` keeps `local note` and gains a `harness:core` region with real rules; `ios/CLAUDE.md` has a `harness:ios` region; 23 skills, 5 commands, 6 `ios-*` workflows, 3 iOS configs present; status shows core+ios `ok` at v2. Note: a `web`-only sync would also pull the supabase skill, but this probe uses an `ios` component.

**Step 3:** Run the Go suite to confirm no code regressed.

```bash
env -u GOROOT /opt/homebrew/bin/go test ./... && env -u GOROOT /opt/homebrew/bin/go vet ./...
```

**Step 4: Commit**

```bash
git add VERSION
git commit -m "feat: bump harness to v2 (ios-template content absorbed)"
```

---

## Task 6: Security audit (gate — required before finishing)

Per the harness security discipline, do NOT finish until this passes.

**Step 1:** Run `/security-review` on the branch diff (`origin/main..HEAD`). This diff is overwhelmingly migrated content (skills/workflows/configs) plus the CLAUDE.md split — review with an eye for:
- **Migrated workflow YAML:** any `pull_request_target`, `workflow_run`, or untrusted-input → secret-exfiltration patterns; any script step interpolating untrusted PR data; any over-broad `permissions:`. These are real CI supply-chain risks the harness will now propagate to every iOS project, so a flaw here is fleet-wide.
- **Migrated scripts inside skills** (e.g. `rocketsim`, profiling skills run shell) — flag any that execute untrusted input.
- **Secrets:** confirm no real secrets/keys were copied from the template (the template used ASC secrets via GitHub secrets, not in-repo — verify none leaked into the migrated files).
- The CLAUDE.md bodies are documentation — not executable — but confirm no marker-literal or token leaked (Task 4 Step 2 already gates this).

**Step 2:** Resolve every finding at confidence ≥ 8. For migrated-workflow findings, fix the workflow in `profiles/ios/workflows/` (the harness is now the source of truth). Re-run until clean. Record accepted residual risk in the PR description.

**Step 3: Commit any fixes**

```bash
git add -A
git commit -m "fix: address security audit findings in absorbed content"
```

---

## Done — what this milestone delivers

`harness sync` now distributes the real iOS toolchain (rules, 20 skills, 4 commands, 6 workflows, 3 configs) and universal core (rules, 3 skills, 1 command), with the web profile seeded. The standalone `ios-template` is no longer the source of truth for agent tooling.

## Known limitations (document in PR)

- **Root-level assets not yet migrated:** `Brewfile`, `.pre-commit-config.yaml`, `.mcp.json`, `allow_mcp.js`, `check-secrets.sh`, `AGENTS.md` — the sync engine has no root-file/script asset category yet.
- **Workflow tokens sync verbatim:** any `{{...}}` in workflow YAML is not substituted (no `init`/`scaffold` yet).
- **`ios-template` repo not retired here:** leave it in place until at least one real project is synced from the harness; retire in a follow-up.

## Next plans

1. **Root-file + script asset category** (Brewfile, pre-commit, mcp, scripts) + `init`.
2. **`scaffold`/`new`**, then **`harvest`**, then **Renovate**.
3. Onboard a real iOS project (e.g. `rail`) from the harness as the proof, then retire `ios-template`.
