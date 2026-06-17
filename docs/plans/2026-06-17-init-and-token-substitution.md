# harness init + Token Substitution Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the harness usable to onboard a real project: `harness init` auto-detects components and writes a `.harness.toml`, and `harness sync` substitutes per-project placeholders ({{PROJECT_NAME}}, {{SCHEME}}, {{BUNDLE_ID}}, {{ASC_APP_ID}}) from the manifest, failing closed on any missing value.

**Architecture:** Substitution is sync-time and manifest-driven. `internal/config` gains a `[project]` block with the four values, each strictly validated (charset regex) so a crafted manifest can't inject into the CI YAML / pre-commit shell the values land in. `internal/tokens` maps manifest fields → placeholder literals and substitutes them. `sync` runs a token preflight across all region bodies + asset contents and aborts (writing nothing) if any registered placeholder lacks a value. `internal/detect` finds components by stack markers; `harness init` composes detect + derive + write-manifest + sync.

**Tech Stack:** Go 1.23 — build/test with `env -u GOROOT /opt/homebrew/bin/go`. Only `{{UPPERCASE_KEY}}` literals from a fixed registry are touched; `${{ ... }}` is never matched.

---

## Registry (the four placeholders)

| Manifest field (`[project]`) | Placeholder literal | Value charset (validated at config.Load) |
|---|---|---|
| `project_name` | `{{PROJECT_NAME}}` | `^[A-Za-z0-9][A-Za-z0-9 ._-]*$` |
| `scheme` | `{{SCHEME}}` | `^[A-Za-z0-9][A-Za-z0-9 ._-]*$` |
| `bundle_id` | `{{BUNDLE_ID}}` | `^[A-Za-z0-9][A-Za-z0-9.-]*$` |
| `asc_app_id` | `{{ASC_APP_ID}}` | `^[0-9]+$` |

A blank value is allowed in the manifest (init stubs them), but if the corresponding placeholder appears in to-be-synced content, sync fails closed. A NON-blank value must match its charset (rejects newlines, quotes, `$()`, backticks, YAML/JSON structure → no injection).

---

## Task 1: [project] fields + strict value validation

**Files:** Modify `internal/config/config.go`; Test `internal/config/config_test.go`.

**Step 1: Write failing tests** — append:

```go
func TestLoadProjectValues(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"rail\"\nproject_name=\"Rail\"\nscheme=\"Rail\"\nbundle_id=\"com.me.rail\"\nasc_app_id=\"6451234567\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Project
	if p.ProjectName != "Rail" || p.Scheme != "Rail" || p.BundleID != "com.me.rail" || p.AscAppID != "6451234567" {
		t.Errorf("project = %+v", p)
	}
}

func TestLoadAllowsBlankProjectValues(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nbundle_id=\"\"\n"); err != nil {
		t.Errorf("blank values must be allowed (init stubs them): %v", err)
	}
}

func TestLoadRejectsInjectionInProjectValues(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\nscheme=\"Rail\\n  evil: true\"\n",      // newline / YAML break
		"[project]\nname=\"x\"\nbundle_id=\"com.me.$(whoami)\"\n",       // shell sub
		"[project]\nname=\"x\"\nproject_name=\"Rail`id`\"\n",            // backtick
		"[project]\nname=\"x\"\nasc_app_id=\"12a34\"\n",                 // non-digit
		"[project]\nname=\"x\"\nscheme=\"a\\\"b\"\n",                    // quote
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected rejection for project value in:\n%s", data)
		}
	}
}
```

**Step 2:** `env -u GOROOT /opt/homebrew/bin/go test ./internal/config/...` → RED (fields undefined).

**Step 3: Implement** — extend `Project` and validate in `Load`:

```go
type Project struct {
	Name        string `toml:"name"`
	ProjectName string `toml:"project_name"`
	Scheme      string `toml:"scheme"`
	BundleID    string `toml:"bundle_id"`
	AscAppID    string `toml:"asc_app_id"`
}
```

Add package-level validators and call them in `Load` after decoding (before returning):

```go
var (
	nameVal     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)
	bundleVal   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	ascVal      = regexp.MustCompile(`^[0-9]+$`)
)

func validateProject(p Project) error {
	check := func(field, val string, re *regexp.Regexp) error {
		if val == "" {
			return nil // blank is allowed; sync fails closed if the token is used
		}
		if !re.MatchString(val) {
			return fmt.Errorf("invalid [project].%s value %q", field, val)
		}
		return nil
	}
	if err := check("project_name", p.ProjectName, nameVal); err != nil {
		return err
	}
	if err := check("scheme", p.Scheme, nameVal); err != nil {
		return err
	}
	if err := check("bundle_id", p.BundleID, bundleVal); err != nil {
		return err
	}
	if err := check("asc_app_id", p.AscAppID, ascVal); err != nil {
		return err
	}
	return nil
}
```

Call `if err := validateProject(cfg.Project); err != nil { return nil, err }` in `Load`.

**Step 4:** Tests pass. Full config suite green.

**Step 5: Commit** — `feat: add validated [project] values to .harness.toml`.

---

## Task 2: internal/tokens — registry + Substitute

**Files:** Create `internal/tokens/tokens.go`, `internal/tokens/tokens_test.go`.

**Step 1: Write failing test:**

```go
package tokens

import (
	"testing"

	"github.com/patrickserrano/harness/internal/config"
)

func TestSubstitute(t *testing.T) {
	p := config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "com.me.rail", AscAppID: "999"}
	in := "scheme: {{SCHEME}}\nid: {{BUNDLE_ID}}\nasc: {{ASC_APP_ID}}\nname: {{PROJECT_NAME}}\nga: ${{ github.ref }}\n"
	out, missing := Substitute(in, p)
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	want := "scheme: Rail\nid: com.me.rail\nasc: 999\nname: Rail\nga: ${{ github.ref }}\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSubstituteReportsMissing(t *testing.T) {
	p := config.Project{ProjectName: "Rail"} // scheme blank
	out, missing := Substitute("a {{SCHEME}} b {{PROJECT_NAME}}", p)
	if len(missing) != 1 || missing[0] != "{{SCHEME}}" {
		t.Fatalf("missing = %v, want [{{SCHEME}}]", missing)
	}
	// PROJECT_NAME still substituted; SCHEME left as-is.
	if out != "a {{SCHEME}} b Rail" {
		t.Fatalf("out = %q", out)
	}
}
```

**Step 2:** RED (undefined Substitute).

**Step 3: Implement** `internal/tokens/tokens.go`:

```go
// Package tokens substitutes the fixed set of per-project placeholders into
// synced content, using values from the manifest's [project] block. Only these
// exact {{KEY}} literals are touched; GitHub Actions ${{ ... }} is never matched.
package tokens

import (
	"strings"

	"github.com/patrickserrano/harness/internal/config"
)

// entry pairs a placeholder literal with the project value it draws from.
type entry struct {
	token string
	value func(config.Project) string
}

var registry = []entry{
	{"{{PROJECT_NAME}}", func(p config.Project) string { return p.ProjectName }},
	{"{{SCHEME}}", func(p config.Project) string { return p.Scheme }},
	{"{{BUNDLE_ID}}", func(p config.Project) string { return p.BundleID }},
	{"{{ASC_APP_ID}}", func(p config.Project) string { return p.AscAppID }},
}

// Substitute replaces each registered placeholder present in content with its
// project value. Any placeholder that appears but has a blank value is returned
// in missing (and left untouched in the output). Deduplicated, in registry order.
func Substitute(content string, p config.Project) (string, []string) {
	var missing []string
	for _, e := range registry {
		if !strings.Contains(content, e.token) {
			continue
		}
		val := e.value(p)
		if val == "" {
			missing = append(missing, e.token)
			continue
		}
		content = strings.ReplaceAll(content, e.token, val)
	}
	return content, missing
}
```

**Step 4:** GREEN. **Step 5: Commit** — `feat: token substitution registry for per-project placeholders`.

---

## Task 3: Wire token preflight + substitution into sync (fail-closed)

**Files:** Modify `internal/sync/sync.go`; Test `internal/sync/sync_test.go`.

Substitution applies to region bodies AND asset contents. A token preflight runs first: gather every (placeholder, file) where a registered token appears with a blank value; if any, abort before writing.

**Step 1: Write failing tests** — append:

```go
func TestSyncSubstitutesTokens(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	// an asset containing a placeholder
	writeFile(t, filepath.Join(harness, "profiles", "ios", "root", ".x.yml"), "scheme: {{SCHEME}}\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\nscheme=\"Rail\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(harness, project); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, ".x.yml"))
	if string(got) != "scheme: Rail\n" {
		t.Errorf("token not substituted: %q", got)
	}
}

func TestSyncFailsClosedOnMissingToken(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "root", ".x.yml"), "scheme: {{SCHEME}}\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// scheme blank -> placeholder present with no value
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")

	_, err := Run(harness, project)
	if err == nil {
		t.Fatal("expected fail-closed error for missing {{SCHEME}} value")
	}
	if !strings.Contains(err.Error(), "SCHEME") {
		t.Errorf("error should name the missing token: %v", err)
	}
	// Nothing should have been written (preflight aborts).
	if _, err := os.Stat(filepath.Join(project, ".x.yml")); err == nil {
		t.Error(".x.yml was written despite missing token (not fail-closed)")
	}
}
```

**Step 2:** RED (substitution not wired; both fail).

**Step 3: Implement** — in `sync.Run`:
- Load now has `cfg.Project`. After `assets.Plan` and BEFORE `assets.Copy`, run a token preflight over: each region body (coreBody, each profile body) and each asset's source content. Collect missing as `"<token> (<dest/region>)"`. If any, return an error listing them.
- Pass `cfg.Project` into substitution: substitute region bodies before `mergeInto`, and substitute asset contents before write.

Concretely:
1. Add a helper in `sync` that reads an asset's bytes and substitutes; but `assets.Copy` reads+writes internally. To keep `assets` cohesive, add substitution there: change `assets.Copy(projectRoot, plan)` → `assets.Copy(projectRoot, plan, proj config.Project)`, and inside the write loop, after `data, err := os.ReadFile(a.Src)`, do `s, missing := tokens.Substitute(string(data), proj)`; if missing, that's a programming error if preflight ran — but to be safe, the preflight is the authority. Implement the preflight as a new `assets.MissingTokens(plan, proj) []string` that scans every asset source, plus the region bodies scanned in `sync`. Then `assets.Copy` substitutes (trusting preflight already validated).
2. In `sync.Run`: build the list of region bodies; run `tokens.Substitute` on each to collect missing; also call `assets.MissingTokens`; if combined missing non-empty → error. Else substitute region bodies (use the substituted string in `mergeInto`) and call `assets.Copy(projectRoot, plan, cfg.Project)`.

Implement `assets.MissingTokens`:

```go
// MissingTokens returns "<token> (<dest>)" for every registered placeholder that
// appears in an asset's source with no project value. Used by sync's fail-closed
// preflight before any write.
func MissingTokens(plan []Asset, proj config.Project) ([]string, error) {
	var out []string
	for _, a := range plan {
		data, err := os.ReadFile(a.Src)
		if err != nil {
			return nil, fmt.Errorf("read asset %s: %w", a.Src, err)
		}
		_, missing := tokens.Substitute(string(data), proj)
		for _, m := range missing {
			out = append(out, fmt.Sprintf("%s (%s)", m, a.Dest))
		}
	}
	return out, nil
}
```

Update `Copy` to substitute: signature `Copy(projectRoot string, plan []Asset, proj config.Project) error`; in the write loop replace `data` with the substituted bytes (`sub, _ := tokens.Substitute(string(data), proj); ... os.WriteFile(target, []byte(sub), mode)`). Update the existing `Copy` call site and tests accordingly (existing asset tests pass `config.Project{}`; with no tokens in their fixtures, missing is empty and behavior is unchanged).

In `sync.Run`, replace the region writes so the body is substituted, and add the preflight. Keep the `len(plan) > 0` guard. The token preflight must run regardless of plan length if any region body has a token (region bodies are de-tokenized today, so normally none).

**Step 4:** Tests pass; full suite green; update any broken call sites (assets tests calling `Copy`).

**Step 5: Commit** — `feat: substitute project tokens during sync, fail closed on missing values`.

---

## Task 4: internal/detect — component detection

**Files:** Create `internal/detect/detect.go`, `internal/detect/detect_test.go`.

**Step 1: Write failing test:**

```go
package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestComponents(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Rail.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "dashboard", "package.json"))
	mk(t, filepath.Join(root, "cli", "Cargo.toml"))

	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{} // path -> profile
	for _, c := range comps {
		got[c.Path] = c.Profiles[0]
	}
	if got["ios"] != "ios" || got["dashboard"] != "web" || got["cli"] != "rust" {
		t.Errorf("components = %+v", comps)
	}
	if derived.ProjectName != "Rail" || derived.Scheme != "Rail" {
		t.Errorf("derived = %+v", derived)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** `internal/detect/detect.go` — walk the root (bounded depth, e.g. ≤ 2) for markers: a `*.xcodeproj` dir → `ios` component at its parent (relative to root); `package.json` → `web`; `Cargo.toml` → `rust`; `go.mod` → `go`. The component `Path` is the marker's directory relative to root (`"."` normalized away if at root). Derive `ProjectName`/`Scheme` from the first `.xcodeproj` basename (strip extension). Return `([]config.Component, config.Project, error)`. Skip `.git`, `.worktrees`, `node_modules`, `DerivedData`, `.build`. Dedupe components by path.

**Step 4:** GREEN. **Step 5: Commit** — `feat: detect components by stack markers`.

---

## Task 5: harness init command

**Files:** Modify `cmd/harness/main.go`; add `internal/initcmd/initcmd.go` + test.

**Step 1: Write failing test** (`internal/initcmd/initcmd_test.go`) — `Run(projectRoot)` writes `.harness.toml` from detection, refuses to clobber an existing one:

```go
func TestInitWritesManifest(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Rail.xcodeproj", "project.pbxproj")) // helper as in detect
	if err := Run(root); err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".harness.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"[project]", "project_name = \"Rail\"", "scheme = \"Rail\"", "[[component]]", "path = \"ios\"", "profiles = [\"ios\"]"} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
}

func TestInitRefusesExistingManifest(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".harness.toml"), []byte("[project]\n"), 0o644)
	if err := Run(root); err == nil {
		t.Fatal("expected init to refuse clobbering an existing .harness.toml")
	}
}
```

**Step 2:** RED.

**Step 3: Implement** `internal/initcmd/initcmd.go`:
- If `.harness.toml` exists → error (refuse to clobber).
- `comps, derived, _ := detect.Components(root)`.
- Render a `.harness.toml` string: a `[project]` block with `name` (derive from dir or project_name), `project_name`/`scheme` (derived, may be blank), and blank `bundle_id`/`asc_app_id` lines; then one `[[component]]` per detected component. Write it.
- Print a short summary to stdout (what was detected, which values still need filling). Do NOT run sync from within `Run` (keep it testable); the CLI wrapper prints next-step guidance ("fill blank [project] values, then run `harness sync`").

Wire into `cmd/harness/main.go`: add `case "init":` calling `initcmd.Run(projectRoot)`, printing the summary; update usage.

**Step 4:** GREEN; full suite. **Step 5: Commit** — `feat: harness init detects components and writes .harness.toml`.

---

## Task 6: End-to-end verification

**Step 1:** Build. Init a throwaway repo, fill the values, sync.

```bash
H=/Users/patrickserrano/Developer/harness
env -u GOROOT /opt/homebrew/bin/go build -o "$H/bin/harness" ./cmd/harness
tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
mkdir -p "$tmp/ios/Rail.xcodeproj"; printf '//\n' > "$tmp/ios/Rail.xcodeproj/project.pbxproj"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" init )
echo "--- generated manifest ---"; cat "$tmp/.harness.toml"
echo "--- sync with blank values should FAIL closed ---"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync ) || echo "(expected non-zero)"
# fill the blanks
python3 - "$tmp/.harness.toml" <<'PY'
import sys,re
p=sys.argv[1]; s=open(p).read()
s=s.replace('bundle_id = ""','bundle_id = "com.me.rail"').replace('asc_app_id = ""','asc_app_id = "6451234567"')
open(p,'w').write(s)
PY
echo "--- sync after filling should succeed ---"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync )
echo "--- a synced workflow should have real values, no {{...}} ---"
grep -l '{{' "$tmp/.github/workflows/"*.yml && echo "TOKENS REMAIN (bad)" || echo "no harness tokens remain (good)"
grep -c '${{' "$tmp/.github/workflows/ios-ci.yml"  # GitHub Actions expressions preserved (>0)
rm -rf "$tmp"
```

Expected: init writes a manifest with derived `project_name`/`scheme` and blank `bundle_id`/`asc_app_id`; the first sync fails closed naming the missing values; after filling, sync succeeds and the workflows contain real values with **no** `{{...}}` harness tokens but intact `${{ github.* }}` expressions.

**Step 2:** `env -u GOROOT /opt/homebrew/bin/go test ./... && go vet ./...`.

**Step 3: Commit** any doc/version touch if needed (no VERSION bump required unless content changed — init/tokens are engine features; bump VERSION to 4 since substitution changes how existing content is delivered). `printf '4\n' > VERSION`; commit `feat: bump harness to v4 (init + token substitution)`.

---

## Task 7: Security audit (gate — required before finishing)

**Step 1:** Run `/security-review` on `origin/main..HEAD`.

**Step 2:** Threat-model the substitution + init:
- **Injection via manifest values:** confirm `config.validateProject` rejects newlines, quotes, `$()`, backticks, and YAML/JSON structural chars for every value, so a crafted `.harness.toml` (e.g. from a cloned repo) cannot inject a step into a synced CI workflow or a command into the pre-commit shell. Try to defeat each regex.
- **Token preflight completeness:** confirm sync truly writes nothing when a token is missing (fail-closed), and that substitution can't partially write before the preflight (preflight precedes all writes).
- **Substitution scope:** confirm only the 4 registered literals are replaced and `${{ ... }}` / other braces are never matched or corrupted.
- **detect / init:** confirm init refuses to clobber an existing manifest; detection walks bounded depth and ignores `.git` etc.; no path-traversal in detected component paths (they come from the filesystem under root, but confirm a symlinked marker dir can't produce a component path escaping root — reuse the same confinement mindset).

**Step 3:** Fix every finding ≥ 8 (test-first). Re-run until clean.

**Step 4: Commit** any fixes.

---

## Done — what this milestone delivers

A real project can be onboarded: `harness init` writes a `.harness.toml` from detection, the operator fills the per-project values, and `harness sync` delivers fully-substituted, project-specific tooling — failing closed if anything is unfilled, and rejecting injection via validated values.

## Known limitations (document in PR)

- **Detection heuristics** are best-effort (bounded depth, marker-based); the operator reviews the generated manifest.
- **No `scaffold`/`new`** yet (init onboards *existing* repos only).

## Next plans

1. Onboard a real project (e.g. `rail`) end-to-end; retire `ios-template`.
2. `scaffold`/`new`, then `harvest` (the learn-from-each-other loop), then Renovate.
