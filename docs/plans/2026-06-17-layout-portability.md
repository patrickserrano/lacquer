# Layout Portability ({{COMPONENT_PREFIX}}) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the iOS profile work regardless of where the component lives (repo root like `rail`, or under `ios/` like the template) by replacing hardcoded `ios/` paths with a derived, per-profile `{{COMPONENT_PREFIX}}` token.

**Architecture:** `{{COMPONENT_PREFIX}}` is a derived token resolved per-asset/region from the path of the component that owns the asset's profile: `Prefix("ios") == "ios/"`, `Prefix(".") == ""`. The empty prefix collapses paths/filters/greps to whole-repo, which is correct for a root-layout project. Profile content uses the token in path contexts only; literal `ios` labels stay. v1 supports at most one component per profile (multi-stack like iOS+web is fine; two iOS apps errors clearly).

**Tech Stack:** Go 1.23 — build/test with `env -u GOROOT /opt/homebrew/bin/go`. `${{ ... }}` is never matched (only `{{KEY}}` registry literals).

---

## Token semantics

| Token | Source | Empty allowed? |
|---|---|---|
| `{{PROJECT_NAME}}`, `{{SCHEME}}`, `{{BUNDLE_ID}}`, `{{ASC_APP_ID}}` | `[project]` values | No — blank fails closed |
| `{{COMPONENT_PREFIX}}` | derived from owning component's path (`""` or `"<path>/"`) | **Yes** — `""` is valid (root layout) |

`{{COMPONENT_PREFIX}}` is only valid in profile content (it needs a component context); it must not appear in core content.

---

## Task 1: config — one-component-per-profile + injection-safe component paths

**Files:** Modify `internal/config/config.go`; Test `internal/config/config_test.go`.

**Step 1: Failing tests** — append:

```go
func TestLoadRejectsDuplicateProfile(t *testing.T) {
	data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"a\"\nprofiles=[\"ios\"]\n\n[[component]]\npath=\"b\"\nprofiles=[\"ios\"]\n"
	if _, err := loadString(t, data); err == nil {
		t.Fatal("expected error: two components declare profile ios")
	}
}

func TestLoadRejectsUnsafeComponentPath(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios;rm -rf\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios app\"\nprofiles=[\"ios\"]\n",   // space
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios$(x)\"\nprofiles=[\"ios\"]\n",
	}
	for _, d := range cases {
		if _, err := loadString(t, d); err == nil {
			t.Errorf("expected rejection for unsafe component path in:\n%s", d)
		}
	}
}

func TestLoadAllowsNestedAndRootComponentPaths(t *testing.T) {
	for _, p := range []string{".", "ios", "apps/ios-app"} {
		data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"" + p + "\"\nprofiles=[\"ios\"]\n"
		if _, err := loadString(t, data); err != nil {
			t.Errorf("path %q should be valid: %v", p, err)
		}
	}
}
```

**Step 2:** RED.

**Step 3: Implement** — tighten `validateComponentPath` to a safe charset and add a duplicate-profile check in `Load`.

Add a path charset (in addition to the existing empty/abs/`..` checks):

```go
// componentPathVal allows "." and slash-separated segments of safe chars only,
// because component.path is substituted into CI/shell via {{COMPONENT_PREFIX}}.
var componentPathVal = regexp.MustCompile(`^(\.|[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*)$`)
```

In `validateComponentPath`, after the existing abs/`..` checks, add:

```go
	if !componentPathVal.MatchString(clean) {
		return fmt.Errorf("component path %q contains unsafe characters", p)
	}
```

(Use `clean` from `filepath.Clean(p)`; note a clean path won't have a trailing slash.)

In `Load`, after the per-component validation loop, add a duplicate-profile check:

```go
	seenProfile := map[string]string{} // profile -> component path
	for _, c := range cfg.Components {
		for _, p := range c.Profiles {
			if prev, ok := seenProfile[p]; ok {
				return nil, fmt.Errorf("profile %q is declared by two components (%q and %q); one component per profile is supported", p, prev, c.Path)
			}
			seenProfile[p] = c.Path
		}
	}
```

**Step 4:** GREEN; full config suite. **Step 5: Commit** — `feat: one component per profile + injection-safe component paths`.

---

## Task 2: tokens — value-map substitution + Prefix helper + empty-valid COMPONENT_PREFIX

**Files:** Modify `internal/tokens/tokens.go`, `internal/tokens/tokens_test.go`.

**Step 1: Failing tests** — replace the existing tests' direct `config.Project` calls and add prefix cases. New API: `Substitute(content string, vals map[string]string) (string, []string)` plus `Prefix(path string) string` and `Values(p config.Project, prefix string) map[string]string`.

```go
func TestPrefix(t *testing.T) {
	if Prefix(".") != "" {
		t.Errorf("Prefix(\".\") = %q, want empty", Prefix("."))
	}
	if Prefix("ios") != "ios/" {
		t.Errorf("Prefix(\"ios\") = %q, want ios/", Prefix("ios"))
	}
	if Prefix("apps/ios-app") != "apps/ios-app/" {
		t.Errorf("Prefix nested = %q", Prefix("apps/ios-app"))
	}
}

func TestSubstituteValues(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "com.me.rail", AscAppID: "9"}, "ios/")
	in := "p: {{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj\nf: '{{COMPONENT_PREFIX}}**'\nga: ${{ github.ref }}\n"
	out, missing := Substitute(in, vals)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	want := "p: ios/Rail.xcodeproj\nf: 'ios/**'\nga: ${{ github.ref }}\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSubstituteEmptyPrefixIsValid(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Rail", Scheme: "Rail", BundleID: "b", AscAppID: "9"}, "") // root layout
	out, missing := Substitute("f: '{{COMPONENT_PREFIX}}**'\nd: {{COMPONENT_PREFIX}}DerivedData\n", vals)
	if len(missing) != 0 {
		t.Fatalf("empty prefix must not be 'missing': %v", missing)
	}
	if out != "f: '**'\nd: DerivedData\n" {
		t.Fatalf("got: %q", out)
	}
}

func TestSubstituteReportsMissingProjectValue(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Rail"}, "ios/") // scheme blank
	_, missing := Substitute("{{SCHEME}} {{PROJECT_NAME}}", vals)
	if len(missing) != 1 || missing[0] != "{{SCHEME}}" {
		t.Fatalf("missing = %v", missing)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** `internal/tokens/tokens.go`:

```go
package tokens

import (
	"strings"

	"github.com/patrickserrano/harness/internal/config"
)

// Token names.
const (
	ProjectName     = "{{PROJECT_NAME}}"
	Scheme          = "{{SCHEME}}"
	BundleID        = "{{BUNDLE_ID}}"
	AscAppID        = "{{ASC_APP_ID}}"
	ComponentPrefix = "{{COMPONENT_PREFIX}}"
)

// entry is a registered token and whether a non-empty value is required (so an
// empty value with the token present in content is a fail-closed "missing").
type entry struct {
	token        string
	requireValue bool
}

var registry = []entry{
	{ProjectName, true},
	{Scheme, true},
	{BundleID, true},
	{AscAppID, true},
	{ComponentPrefix, false}, // "" is a valid value (root layout)
}

// Prefix converts a component path to a path prefix: "." -> "", "ios" -> "ios/".
func Prefix(path string) string {
	if path == "." || path == "" {
		return ""
	}
	return path + "/"
}

// Values builds the substitution map from the [project] values plus the derived
// component prefix.
func Values(p config.Project, prefix string) map[string]string {
	return map[string]string{
		ProjectName:     p.ProjectName,
		Scheme:          p.Scheme,
		BundleID:        p.BundleID,
		AscAppID:        p.AscAppID,
		ComponentPrefix: prefix,
	}
}

// Substitute replaces each registered token present in content with its value
// from vals. A token that requires a value but is empty is returned in missing
// and left untouched. Only registered {{KEY}} literals are touched; ${{ ... }}
// is never matched.
func Substitute(content string, vals map[string]string) (string, []string) {
	var missing []string
	for _, e := range registry {
		if !strings.Contains(content, e.token) {
			continue
		}
		v := vals[e.token]
		if v == "" && e.requireValue {
			missing = append(missing, e.token)
			continue
		}
		content = strings.ReplaceAll(content, e.token, v)
	}
	return content, missing
}
```

**Step 4:** GREEN. **Step 5: Commit** — `feat: value-map token substitution with derived component prefix`.

---

## Task 3: assets — per-asset prefix in Plan; Copy/MissingTokens use vals

**Files:** Modify `internal/assets/assets.go`, `internal/assets/assets_test.go`.

**Step 1: Failing test** — append:

```go
func TestPlanRecordsComponentPrefix(t *testing.T) {
	h := t.TempDir()
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "web", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "core", "skills", "g.md"), "x")
	cfg := &config.Config{Components: []config.Component{
		{Path: ".", Profiles: []string{"ios"}},
		{Path: "dashboard", Profiles: []string{"web"}},
	}}
	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pre := map[string]string{}
	for _, a := range got {
		pre[a.Dest] = a.Prefix
	}
	if pre[filepath.Join(".github", "workflows", "ios-ci.yml")] != "" {
		t.Errorf("ios (root) prefix should be empty, got %q", pre[filepath.Join(".github", "workflows", "ios-ci.yml")])
	}
	if pre[filepath.Join(".github", "workflows", "web-ci.yml")] != "dashboard/" {
		t.Errorf("web prefix = %q, want dashboard/", pre[filepath.Join(".github", "workflows", "web-ci.yml")])
	}
	if pre[filepath.Join(".claude", "skills", "g.md")] != "" {
		t.Errorf("core asset prefix must be empty, got %q", pre[filepath.Join(".claude", "skills", "g.md")])
	}
}
```

**Step 2:** RED (`Asset.Prefix` undefined).

**Step 3: Implement:**
- Add `Prefix string` to `Asset`.
- In `Plan`, build `profileDir := map[string]string{}` from `cfg.Components` (profile -> component path; safe because config guarantees one component per profile). For each profile asset, set `Prefix: tokens.Prefix(profileDir[p])`. Core assets get `Prefix: ""`.
- The `add` closure needs the prefix; thread it through (e.g. `add(src, dest, prefix)`), or set it where each `add` is called. Simplest: change `add` to take a prefix arg; core calls pass `""`, profile calls pass `tokens.Prefix(profileDir[p])`.
- Change `Copy(projectRoot string, plan []Asset, proj config.Project)` → `Copy(projectRoot string, plan []Asset, proj config.Project)` but build per-asset vals: `vals := tokens.Values(proj, a.Prefix)` inside the loop, then `tokens.Substitute(string(data), vals)`.
- Change `MissingTokens(plan, proj)` to also use per-asset prefix: `tokens.Substitute(string(data), tokens.Values(proj, a.Prefix))`.

(`Copy`/`MissingTokens` keep the `proj config.Project` param; they build the vals per asset using `a.Prefix`. Update call sites/tests that pass `config.Project{}` — they still compile; assets without tokens are unaffected.)

**Step 4:** GREEN; assets suite. **Step 5: Commit** — `feat: resolve per-profile component prefix for each asset`.

---

## Task 4: sync — per-region prefix; build vals from project + prefix

**Files:** Modify `internal/sync/sync.go`, `internal/sync/sync_test.go`.

**Step 1: Failing test** — append a root-layout substitution test:

```go
func TestSyncRootLayoutEmptyPrefix(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "1\n")
	writeFile(t, filepath.Join(harness, "core", "CLAUDE.core.md"), "CORE")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "CLAUDE.ios.md"), "IOS")
	writeFile(t, filepath.Join(harness, "profiles", "ios", "workflows", "ci.yml"), "lint: {{COMPONENT_PREFIX}}.swiftlint.yml\nf: '{{COMPONENT_PREFIX}}**'\n")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// component at root (".") and all 4 project values present
	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"x\"\nproject_name=\"Rail\"\nscheme=\"Rail\"\nbundle_id=\"com.me.rail\"\nasc_app_id=\"9\"\n\n[[component]]\npath=\".\"\nprofiles=[\"ios\"]\n")

	if _, err := Run(harness, project); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(project, ".github", "workflows", "ios-ci.yml"))
	if string(got) != "lint: .swiftlint.yml\nf: '**'\n" {
		t.Errorf("root-layout prefix not applied:\n%q", got)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** — in `Run`:
- When gathering region bodies, record each region's component path so its prefix is known. Core region prefix = `""`. For a profile region, prefix = `tokens.Prefix(c.Path)`.
- Token preflight: for each region, `tokens.Substitute(body, tokens.Values(cfg.Project, regionPrefix))`; collect missing. Keep `assets.MissingTokens(plan, cfg.Project)` (now per-asset prefix-aware).
- Writes: substitute each region body with its prefix before `mergeInto`; `assets.Copy(projectRoot, plan, cfg.Project)` unchanged (it now resolves per-asset prefix internally).

Add a `prefix` field to the `regionWrite` struct.

**Step 4:** GREEN; full suite. **Step 5: Commit** — `feat: apply per-region component prefix during sync`.

---

## Task 5: Rewrite iOS profile content to use {{COMPONENT_PREFIX}}

**Files:** Modify `profiles/ios/workflows/*.yml` and `profiles/ios/CLAUDE.ios.md`.

Replace **path-context** `ios/` and `ios` with `{{COMPONENT_PREFIX}}` forms. Leave literal labels (`runs-on: [self-hosted, macos, ios, dedicated]`, `group: ios-release`, `--workflow=ios-ci.yml`/`ios-release.yml`, `Apply labels: ios, quality`).

Concrete substitutions (apply across `ci.yml`, `release.yml`, `dead-code.yml`, `quality-review.yml`; `cleanup-ci.yml` has only workflow-filename refs — leave it):
- Path filter `- 'ios/**'` → `- '{{COMPONENT_PREFIX}}**'`
- Changed-file greps: `'^(ios/|\.github/workflows/ios-ci\.yml$)'` → `'^({{COMPONENT_PREFIX}}|\.github/workflows/ios-ci\.yml$)'`; `'^ios/.*/(Views|App)/'` → `'^{{COMPONENT_PREFIX}}.*/(Views|App)/'`
- `swiftlint --strict --config ios/.swiftlint.yml ios/` → `... --config {{COMPONENT_PREFIX}}.swiftlint.yml {{COMPONENT_PREFIX}}.`
- `swiftformat --lint --config ios/.swiftformat ios/` → `... --config {{COMPONENT_PREFIX}}.swiftformat {{COMPONENT_PREFIX}}.`
- `ios/DerivedData...`, `ios/{{PROJECT_NAME}}.xcodeproj`, `ios/build/...`, `ios/ExportOptions.plist`, `ios/periphery-report.json`, `hashFiles('ios/...')`, `find ios/{{PROJECT_NAME}}/Features` → replace the leading `ios/` with `{{COMPONENT_PREFIX}}`
- `cd ios` → `cd {{COMPONENT_PREFIX}}.` (the trailing `.` keeps it valid when the prefix is empty)
- In `CLAUDE.ios.md`, the flowdeck examples `ios/<YourApp>.xcodeproj`, `-d ios/DerivedData`, project-structure `ios/<YourApp>/` → `{{COMPONENT_PREFIX}}<YourApp>.xcodeproj` etc. (the body is now substituted at sync time).

**Verification within this task:**
```bash
H=/Users/patrickserrano/Developer/harness
# No bare path-context ios/ should remain (allow the literal label/group/workflow refs)
grep -rnE '\bios/' "$H/profiles/ios/workflows/" "$H/profiles/ios/CLAUDE.ios.md" || echo "no bare ios/ path contexts remain (good)"
# spot the literal labels that SHOULD remain
grep -rn 'workflow=ios-ci.yml\|group: ios-release\|macos, ios' "$H/profiles/ios/workflows/" | head
```
Expect: no bare `ios/` path contexts; the literal label/group/workflow-name refs still present.

**Commit** — `feat(content): parameterize iOS profile paths with {{COMPONENT_PREFIX}}`.

---

## Task 6: End-to-end verification + v5 bump

**Step 1:** Build. Verify three layouts produce correct paths.

```bash
H=/Users/patrickserrano/Developer/harness
env -u GOROOT /opt/homebrew/bin/go build -o "$H/bin/harness" ./cmd/harness

probe() { # $1 = component path
  tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
  if [ "$1" = "." ]; then proj="."; else proj="$1"; fi
  printf '[project]\nname="p"\nproject_name="Rail"\nscheme="Rail"\nbundle_id="com.me.rail"\nasc_app_id="9"\n\n[[component]]\npath="%s"\nprofiles=["ios"]\n' "$proj" > "$tmp/.harness.toml"
  ( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync >/dev/null )
  echo "--- component=$1: ios-ci.yml lint line + path filter ---"
  grep -nE 'swiftlint --strict|COMPONENT_PREFIX|^\s+- |DerivedData' "$tmp/.github/workflows/ios-ci.yml" | grep -iE 'swiftlint|\*\*|DerivedData' | head -4
  grep -c '{{' "$tmp/.github/workflows/ios-ci.yml" | sed 's/^/harness tokens remaining: /'
  rm -rf "$tmp"
}
probe "ios"   # template layout -> ios/.swiftlint.yml, 'ios/**', ios/DerivedData
probe "."     # root layout    -> .swiftlint.yml, '**', DerivedData
```

Expect: `ios` layout shows `ios/`-prefixed paths and `'ios/**'`; root layout shows unprefixed paths and `'**'`; **0** harness tokens remaining in both.

**Step 2:** Multi-stack + duplicate-profile checks.

```bash
H=/Users/patrickserrano/Developer/harness
# duplicate profile must error
tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
printf '[project]\nname="p"\nproject_name="A"\nscheme="A"\nbundle_id="b"\nasc_app_id="9"\n\n[[component]]\npath="a"\nprofiles=["ios"]\n\n[[component]]\npath="b"\nprofiles=["ios"]\n' > "$tmp/.harness.toml"
( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync ) && echo "UNEXPECTED ok" || echo "(expected: duplicate-profile error)"
rm -rf "$tmp"
```

**Step 3:** `env -u GOROOT /opt/homebrew/bin/go test ./... && go vet ./...`.

**Step 4:** `printf '5\n' > VERSION`; commit `feat: bump harness to v5 (layout portability via {{COMPONENT_PREFIX}})`.

---

## Task 7: Security audit (gate)

**Step 1:** `/security-review` on `origin/main..HEAD`.

**Step 2:** Threat-model:
- **Injection via component.path -> {{COMPONENT_PREFIX}} -> CI YAML/shell.** Confirm `componentPathVal` rejects spaces, `;`, `$()`, backticks, quotes, newlines, and that combined with the abs/`..` checks no path can break out or inject. Try to defeat the regex (anchoring, embedded newline — same RE2 reasoning as the project-value audit).
- **Empty-prefix correctness:** confirm `{{COMPONENT_PREFIX}}=""` can't produce a path that escapes (e.g. `cd {{COMPONENT_PREFIX}}.` -> `cd .`, never `cd ` or `cd /`).
- **Token preflight still fail-closed** for the 4 project values; COMPONENT_PREFIX empty is intentionally not "missing".
- **One-per-profile** enforced (no ambiguous prefix).

**Step 3:** Fix findings ≥ 8 (test-first); re-run until clean. **Step 4:** Commit fixes.

---

## Done — what this milestone delivers

The iOS profile is layout-agnostic: a root-layout project (rail) and a template-layout project both get correct, fully-substituted CI/config, and multi-stack repos (iOS + web admin) resolve each profile's paths independently. Unblocks onboarding rail.

## Known limitations (PR)

- **One component per profile** (multi-stack fine; two same-stack apps errors — per-app workflows deferred).
- Onboarding a mature project still **overwrites** its diverged configs (reviewed in that project's PR diff).

## Next

Re-attempt **rail onboarding** end-to-end on a branch (now unblocked); then retire `ios-template`; then `scaffold`/`new`, `harvest`, Renovate.
