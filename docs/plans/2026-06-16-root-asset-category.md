# Root-File / Script Asset Category Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `root/` asset category so `harness sync` can distribute repo-root files and scripts (Brewfile, pre-commit config, MCP config, helper scripts) with their executable bit preserved — then migrate the deferred ios-template root files and verify end-to-end.

**Architecture:** Extend `internal/assets` with one new placement rule: any file under `core/root/**` or `profiles/<p>/root/**` copies to the project root at the same relative path (preserving subdirectories like `scripts/` and `.claude/scripts/`), root-union deduped exactly like skills. Fix `assets.Copy` to preserve the source file's executable bit (resolving the earlier 0o644 limitation) so synced scripts stay runnable. No new package.

**Tech Stack:** Go 1.23 — build/test with `env -u GOROOT /opt/homebrew/bin/go`. Reference: `docs/plans/2026-06-15-harness-design.md`, prior asset-sync plan.

---

## Scope boundary

**In scope:** new `root/` placement rule in `assets.Plan`; executable-bit preservation in `assets.Copy`; migrate `Brewfile`, `.pre-commit-config.yaml`, `.mcp.json`, `.claude/scripts/allow_mcp.js` (iOS) and `scripts/check-secrets.sh` (core); bump VERSION to 3; end-to-end verify; security gate.

**Placement rule added:**

| Harness source | Project destination |
|----------------|---------------------|
| `core/root/<path>` | `<path>` (at project root) |
| `profiles/<p>/root/<path>` | `<path>` (at project root) |

So `profiles/ios/root/Brewfile` → `<root>/Brewfile`, `profiles/ios/root/.claude/scripts/allow_mcp.js` → `<root>/.claude/scripts/allow_mcp.js`, `core/root/scripts/check-secrets.sh` → `<root>/scripts/check-secrets.sh`.

**Deferred (not this milestone):** `harness init`, token substitution (the 4 real `{{...}}` workflow/pre-commit placeholders still sync verbatim), `AGENTS.md` (duplicates the core CLAUDE rules), `FIRST_RUN.md` (becomes `init`), `README.md` (per-project).

**Known overwrite caveat to document:** root files are whole-file synced (overwrite). `.mcp.json` especially may be project-customized; the git guard protects uncommitted edits, but a committed custom `.mcp.json` would be overwritten on sync. No JSON-merge in scope.

---

## Task 1: Enumerate the `root/` category in assets.Plan

**Files:** Modify `internal/assets/assets.go`; Test `internal/assets/assets_test.go`.

**Step 1: Write the failing test** — append to `assets_test.go`:

```go
func TestPlanRootCategory(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "core", "root", "scripts", "check-secrets.sh"), "#!/bin/sh\n")
	write(t, filepath.Join(h, "profiles", "ios", "root", "Brewfile"), "brew 'x'\n")
	write(t, filepath.Join(h, "profiles", "ios", "root", ".claude", "scripts", "allow_mcp.js"), "//x\n")

	cfg := &config.Config{Components: []config.Component{{Path: "ios", Profiles: []string{"ios"}}}}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	dests := map[string]bool{}
	for _, a := range got {
		dests[a.Dest] = true
	}
	for _, want := range []string{
		filepath.Join("scripts", "check-secrets.sh"),
		"Brewfile",
		filepath.Join(".claude", "scripts", "allow_mcp.js"),
	} {
		if !dests[want] {
			t.Errorf("missing root asset dest %q; got %v", want, dests)
		}
	}
}
```

**Step 2:** Run `env -u GOROOT /opt/homebrew/bin/go test ./internal/assets/... -run RootCategory`; confirm RED (dests absent).

**Step 3: Implement** — in `assets.Plan`, after the existing core skills/commands block, add a core root walk; and inside the per-profile loop, add a profile root walk. Both map `rel` → `rel` (project root, verbatim):

```go
	// core: root tree -> project root (verbatim relative paths)
	if err := walkInto(filepath.Join(harnessRoot, "core", "root"),
		func(src, rel string) { add(src, rel) }); err != nil {
		return nil, err
	}
```

and within `for _, p := range profiles {` after the workflows block:

```go
		// profile root tree -> project root (verbatim relative paths)
		if err := walkInto(filepath.Join(base, "root"),
			func(src, rel string) { add(src, rel) }); err != nil {
			return nil, err
		}
```

**Step 4:** Run the test; confirm PASS. Run full assets suite.

**Step 5: Commit** — `git add internal/assets/`; message `feat: enumerate root/ asset category (repo-root files + scripts)`.

---

## Task 2: Preserve the executable bit in assets.Copy

**Files:** Modify `internal/assets/assets.go`; Test `internal/assets/assets_test.go`.

**Step 1: Write the failing test** — append:

```go
func TestCopyPreservesExecutableBit(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	gitInit(t, project)
	// an executable source script and a non-exec source file
	exe := filepath.Join(h, "core", "root", "scripts", "hook.sh")
	write(t, exe, "#!/bin/sh\necho hi\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(h, "core", "root", "Brewfile"), "brew 'x'\n")

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	fi, err := os.Stat(filepath.Join(project, "scripts", "hook.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o100 == 0 {
		t.Errorf("script lost its executable bit: mode=%v", fi.Mode())
	}
	bf, err := os.Stat(filepath.Join(project, "Brewfile"))
	if err != nil {
		t.Fatal(err)
	}
	if bf.Mode()&0o111 != 0 {
		t.Errorf("non-exec file gained an executable bit: mode=%v", bf.Mode())
	}
}
```

**Step 2:** Run `... -run PreservesExecutableBit`; confirm RED (script lost exec bit — Copy hardcodes 0o644).

**Step 3: Implement** — in `assets.Copy`'s write phase, choose the mode from the source and chmod after write:

```go
		mode := os.FileMode(0o644)
		if info, err := os.Stat(a.Src); err == nil && info.Mode()&0o100 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return err
		}
		// WriteFile only applies mode on create; enforce it for overwrites too.
		if err := os.Chmod(target, mode); err != nil {
			return err
		}
```

(Replace the existing `os.WriteFile(target, data, 0o644)` line with the block above.)

**Step 4:** Run the test; confirm PASS. Run full suite + vet + gofmt.

**Step 5: Commit** — `git add internal/assets/`; message `feat: preserve executable bit when copying assets`.

---

## Task 3: Migrate the deferred root files

**Step 1:** Copy from the template into the harness root category.

```bash
T=/Users/patrickserrano/Developer/ios-template
H=/Users/patrickserrano/Developer/harness
mkdir -p "$H/core/root/scripts" "$H/profiles/ios/root/.claude/scripts"
cp "$T/scripts/check-secrets.sh"            "$H/core/root/scripts/check-secrets.sh"
cp "$T/Brewfile"                            "$H/profiles/ios/root/Brewfile"
cp "$T/.pre-commit-config.yaml"             "$H/profiles/ios/root/.pre-commit-config.yaml"
cp "$T/.mcp.json"                           "$H/profiles/ios/root/.mcp.json"
cp "$T/.claude/scripts/allow_mcp.js"        "$H/profiles/ios/root/.claude/scripts/allow_mcp.js"
chmod +x "$H/core/root/scripts/check-secrets.sh" "$H/profiles/ios/root/.claude/scripts/allow_mcp.js"
```

**Step 2:** Verify presence + exec bits.

```bash
ls -la "$H/core/root/scripts/check-secrets.sh" "$H/profiles/ios/root/.claude/scripts/allow_mcp.js" | awk '{print $1,$NF}'
ls "$H/profiles/ios/root"
```

Confirm the two scripts are `-rwxr-xr-x`.

**Step 3: Commit** — `git add core/root profiles/ios/root`; message `feat(content): migrate Brewfile/pre-commit/mcp/scripts into root asset category`.

---

## Task 4: Bump VERSION and verify end-to-end

**Step 1:** `printf '3\n' > VERSION`.

**Step 2:** Build and sync into a throwaway git project; confirm the root files land and scripts are executable.

```bash
H=/Users/patrickserrano/Developer/harness
env -u GOROOT /opt/homebrew/bin/go build -o "$H/bin/harness" ./cmd/harness
tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
printf '[project]\nname="probe"\n\n[[component]]\npath="ios"\nprofiles=["ios"]\n' > "$tmp/.harness.toml"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync )
echo "--- root files ---"; ls -la "$tmp/Brewfile" "$tmp/.pre-commit-config.yaml" "$tmp/.mcp.json" 2>&1 | awk '{print $1,$NF}'
echo "--- scripts (must be executable) ---"; ls -la "$tmp/scripts/check-secrets.sh" "$tmp/.claude/scripts/allow_mcp.js" 2>&1 | awk '{print $1,$NF}'
rm -rf "$tmp"
```

Expected: Brewfile/.pre-commit/.mcp.json present at root; both scripts `-rwxr-xr-x`.

**Step 3:** `env -u GOROOT /opt/homebrew/bin/go test ./... && env -u GOROOT /opt/homebrew/bin/go vet ./...`.

**Step 4: Commit** — `git add VERSION`; message `feat: bump harness to v3 (root asset category + tooling files)`.

---

## Task 5: Security audit (gate — required before finishing)

**Step 1:** Run `/security-review` on `origin/main..HEAD`.

**Step 2:** Threat-model the new surface:
- **Executable-bit preservation** now lets the harness sync **runnable scripts** into every project (`check-secrets.sh`, `allow_mcp.js`). Review both scripts for unsafe behavior: command injection, `eval`, unquoted expansions, network calls, anything that runs untrusted input. `check-secrets.sh` runs as a pre-commit hook; `allow_mcp.js` runs at session start (it clicks the Xcode MCP allow dialog via Accessibility) — confirm it does only that and reads no untrusted input.
- **`.mcp.json`** — confirm it only configures the intended MCP server(s) and embeds no secret/token.
- **`.pre-commit-config.yaml`** — confirm hooks point at trusted repos/refs and the local secret-scan hook is wired safely.
- Re-confirm the root-category copy still goes through `safepath.Resolve` + the symlink guard + the dirty preflight (it reuses `assets.Copy`, so it should — verify no bypass for the new category).

**Step 3:** Fix every finding at confidence ≥ 8 (test-first). Re-run until clean.

**Step 4: Commit** any fixes — `fix: address security audit findings for root asset category`.

---

## Done — what this milestone delivers

`harness sync` now distributes repo-root tooling (Brewfile, pre-commit, MCP config, secret-scan + MCP-allow scripts) with executable bits intact, completing the asset coverage. Only `init`/scaffold-era concerns (token substitution, onboarding) remain before a real project can be fully driven from the harness.

## Known limitations (document in PR)

- **Whole-file overwrite for root files** (no merge); `.mcp.json` customizations would be overwritten (git guard still protects uncommitted edits).
- **Tokens in `.pre-commit-config.yaml`** sync verbatim until token substitution lands.
- **`AGENTS.md` deferred** (redundant with core CLAUDE rules); `README.md` stays per-project.

## Next plans

1. **`harness init`** — detect components, write `.harness.toml`, first sync, token substitution for the 4 real placeholders; replaces `FIRST_RUN.md`.
2. Onboard a real project (e.g. `rail`) from the harness; then `scaffold`/`new`, `harvest`, Renovate.
