# {{XCODEPROJ}} Token + Layout Detection Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Model the `.xcodeproj` path independently from the component/config dir so projects like Queueify (configs at `ios/`, xcodeproj nested at `ios/Queueify/Queueify.xcodeproj`) onboard correctly. Add an `{{XCODEPROJ}}` token + smarter detection.

**Architecture:** `{{COMPONENT_PREFIX}}` keeps meaning "the config/lint/DerivedData dir prefix" (derived from `component.path`). A new `[project].xcodeproj` value drives a new required `{{XCODEPROJ}}` token (the full repo-relative path to the `.xcodeproj`). `detect` finds the xcodeproj path AND picks the component as the dir holding `.swiftlint.yml`/`.swiftformat` (falling back to the xcodeproj's parent). Workflows/CLAUDE use `{{XCODEPROJ}}` for `-project`/SPM-cache paths; `{{COMPONENT_PREFIX}}` stays for configs/DerivedData/lint scope.

**Tech Stack:** Go 1.23 — build/test with `env -u GOROOT /opt/homebrew/bin/go`.

---

## Layouts this must handle

| Project | component (config dir) | xcodeproj |
|---|---|---|
| template | `ios` | `ios/MyApp.xcodeproj` |
| rail | `.` | `Rail.xcodeproj` |
| Queueify | `ios` | `ios/Queueify/Queueify.xcodeproj` |

`{{XCODEPROJ}}` = the xcodeproj column verbatim; `{{COMPONENT_PREFIX}}` = `Prefix(component)`.

---

## Task 1: [project].xcodeproj value + validation

**Files:** `internal/config/config.go`, `internal/config/config_test.go`.

**Step 1: Failing tests** — append:

```go
func TestLoadXcodeproj(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"q\"\nxcodeproj=\"ios/Queueify/Queueify.xcodeproj\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Xcodeproj != "ios/Queueify/Queueify.xcodeproj" {
		t.Errorf("xcodeproj = %q", cfg.Project.Xcodeproj)
	}
}

func TestLoadRejectsUnsafeXcodeproj(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\nxcodeproj=\"/abs/App.xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"../escape/App.xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/$(x).xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/App.xcodeproj; rm -rf\"\n",
	}
	for _, d := range cases {
		if _, err := loadString(t, d); err == nil {
			t.Errorf("expected rejection for xcodeproj in:\n%s", d)
		}
	}
}

func TestLoadAllowsBlankXcodeproj(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\n"); err != nil {
		t.Errorf("blank xcodeproj must be allowed: %v", err)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** — add `Xcodeproj string `toml:"xcodeproj"`` to `Project`. Validate in `validateProject`: blank allowed; non-blank must be a relative, non-escaping, safe path ending in `.xcodeproj`. Reuse the path rules:

```go
func validateXcodeproj(p string) error {
	if p == "" {
		return nil
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("[project].xcodeproj %q must be relative", p)
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("[project].xcodeproj %q escapes the project root", p)
	}
	if !componentPathVal.MatchString(filepath.ToSlash(clean)) || !strings.HasSuffix(clean, ".xcodeproj") {
		return fmt.Errorf("[project].xcodeproj %q is not a valid .xcodeproj path", p)
	}
	return nil
}
```

Note `componentPathVal` currently forbids leading `-` per segment and allows `.`/`_`/`-`/alnum and `/`; a `.xcodeproj` segment like `Queueify.xcodeproj` matches (`.` allowed). Call `validateXcodeproj(cfg.Project.Xcodeproj)` in `validateProject`.

**Step 4:** GREEN; full config suite. **Step 5: Commit** — `feat: add validated [project].xcodeproj path`.

---

## Task 2: {{XCODEPROJ}} token

**Files:** `internal/tokens/tokens.go`, `internal/tokens/tokens_test.go`.

**Step 1: Failing test** — append:

```go
func TestSubstituteXcodeproj(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "b", AscAppID: "9", Xcodeproj: "ios/Queueify/Queueify.xcodeproj"}, "ios/")
	out, missing := Substitute("-project {{XCODEPROJ}}\nlint: {{COMPONENT_PREFIX}}.swiftlint.yml", vals)
	if len(missing) != 0 {
		t.Fatalf("missing: %v", missing)
	}
	if out != "-project ios/Queueify/Queueify.xcodeproj\nlint: ios/.swiftlint.yml" {
		t.Fatalf("out: %q", out)
	}
}

func TestSubstituteReportsMissingXcodeproj(t *testing.T) {
	vals := Values(config.Project{ProjectName: "Q", Scheme: "Q", BundleID: "b", AscAppID: "9"}, "ios/") // xcodeproj blank
	_, missing := Substitute("-project {{XCODEPROJ}}", vals)
	if len(missing) != 1 || missing[0] != "{{XCODEPROJ}}" {
		t.Fatalf("missing = %v", missing)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** — add `Xcodeproj = "{{XCODEPROJ}}"` const; add `{Xcodeproj, true}` to `registry`; add `Xcodeproj: p.Xcodeproj` to the `Values` map.

**Step 4:** GREEN. **Step 5: Commit** — `feat: add {{XCODEPROJ}} token`.

---

## Task 3: detect — find xcodeproj path + config-dir component

**Files:** `internal/detect/detect.go`, `internal/detect/detect_test.go`.

**Step 1: Failing test** — append (Queueify-shaped):

```go
func TestComponentsConfigDirAndXcodeproj(t *testing.T) {
	root := t.TempDir()
	// xcodeproj nested under ios/Queueify, configs at ios/
	mk(t, filepath.Join(root, "ios", "Queueify", "Queueify.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))

	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Fatalf("component should be the config dir 'ios', got %+v", comps)
	}
	if derived.Xcodeproj != "ios/Queueify/Queueify.xcodeproj" {
		t.Errorf("xcodeproj = %q, want ios/Queueify/Queueify.xcodeproj", derived.Xcodeproj)
	}
	if derived.ProjectName != "Queueify" {
		t.Errorf("project_name = %q", derived.ProjectName)
	}
}

func TestComponentsXcodeprojParentFallback(t *testing.T) {
	root := t.TempDir()
	// no separate config dir -> component is the xcodeproj's parent
	mk(t, filepath.Join(root, "ios", "App.xcodeproj", "project.pbxproj"))
	comps, derived, err := Components(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 || comps[0].Path != "ios" {
		t.Errorf("component = %+v, want ios", comps)
	}
	if derived.Xcodeproj != "ios/App.xcodeproj" {
		t.Errorf("xcodeproj = %q", derived.Xcodeproj)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** — extend the iOS branch in `Components`:
- When an `*.xcodeproj` dir is found, record its full repo-relative path into `derived.Xcodeproj` (first one wins, like project_name), using `filepath.ToSlash`.
- For the iOS component path, prefer the directory that contains `.swiftlint.yml`/`.swiftformat`/`.periphery.yml` if one exists at or above the xcodeproj within the repo; else fall back to the xcodeproj's parent. Simplest robust approach: during the walk, also note any dir containing a swift config file as `iosConfigDir`. After the walk, if `iosConfigDir` is set and the xcodeproj path is under it (or equal), use `iosConfigDir` as the ios component path; else use the xcodeproj's parent.

Concretely: track `iosXcodeprojDir` (parent of the .xcodeproj) and `iosConfigDir` (dir of the first `.swiftlint.yml`/`.swiftformat` seen). After the walk, set the ios component path = `iosConfigDir` if non-empty AND `iosXcodeprojDir` is within it; otherwise `iosXcodeprojDir`. Keep the existing skipDirs and the SkipDir-after-.xcodeproj (so we still find configs that sit ABOVE the xcodeproj — note configs are visited before descending into Queueify/ only if `ios/` is walked first; WalkDir visits `ios/.swiftlint.yml` before `ios/Queueify/` since lexical and files/dirs ordering — verify in test; if ordering is an issue, do two passes or don't SkipDir the xcodeproj for config-finding).

> Implementation note: to avoid walk-ordering fragility, collect ALL xcodeproj paths and ALL swift-config dirs during the walk, then resolve the ios component once after. Don't rely on visit order.

Return `derived` with `ProjectName`, `Scheme`, `Xcodeproj` set.

**Step 4:** GREEN (both new tests + existing detect tests). **Step 5: Commit** — `feat: detect xcodeproj path and config-dir component`.

---

## Task 4: init writes xcodeproj

**Files:** `internal/initcmd/initcmd.go`, `internal/initcmd/initcmd_test.go`.

**Step 1: Failing test** — extend `TestInitWritesManifest` (or add one) to assert the manifest includes `xcodeproj = "..."` for a Queueify-shaped tree, and that the emitted manifest loads.

```go
func TestInitWritesXcodeproj(t *testing.T) {
	root := t.TempDir()
	mk(t, filepath.Join(root, "ios", "Queueify", "Queueify.xcodeproj", "project.pbxproj"))
	mk(t, filepath.Join(root, "ios", ".swiftlint.yml"))
	if _, err := Run(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, ".harness.toml"))
	s := string(data)
	for _, want := range []string{`xcodeproj = "ios/Queueify/Queueify.xcodeproj"`, `path = "ios"`} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q:\n%s", want, s)
		}
	}
	if _, err := config.Load(filepath.Join(root, ".harness.toml")); err != nil {
		t.Errorf("generated manifest does not load: %v", err)
	}
}
```

**Step 2:** RED.

**Step 3: Implement** — in `initcmd.Run`, emit `xcodeproj = %q` (from `derived.Xcodeproj`) in the `[project]` block (after scheme). Leave it blank `""` if detection found none.

**Step 4:** GREEN. **Step 5: Commit** — `feat: init writes detected xcodeproj path`.

---

## Task 5: Rewrite iOS content to use {{XCODEPROJ}}

**Files:** `profiles/ios/workflows/*.yml`, `profiles/ios/CLAUDE.ios.md`.

Replace every `.xcodeproj` reference built from `{{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj` with `{{XCODEPROJ}}`:
- `ci.yml`: the build `-project {{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj` → `-project {{XCODEPROJ}}`; the SPM-cache `hashFiles('{{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj/project.xcworkspace/...')` → `hashFiles('{{XCODEPROJ}}/project.xcworkspace/...')` (both cache steps).
- `release.yml`: `-project {{COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj` → `-project {{XCODEPROJ}}`. Leave archive/build/export paths as `{{COMPONENT_PREFIX}}build/...` (the build dir is under the component dir — correct).
- `CLAUDE.ios.md`: flowdeck `-w {{COMPONENT_PREFIX}}<YourApp>.xcodeproj` → `-w {{XCODEPROJ}}`.

Keep `{{COMPONENT_PREFIX}}` for `.swiftlint.yml`, `.swiftformat`, `DerivedData`, lint scope, `build/`, `ExportOptions.plist`, `periphery-report.json`, path filters, greps.

**Verify within task:**
```bash
H=/Users/patrickserrano/Developer/harness
grep -rn 'COMPONENT_PREFIX}}{{PROJECT_NAME}}.xcodeproj\|COMPONENT_PREFIX}}<YourApp>.xcodeproj' "$H/profiles/ios/" && echo "STILL CONFLATED (fix)" || echo "no conflated xcodeproj refs (good)"
grep -rn 'XCODEPROJ' "$H/profiles/ios/workflows/" | head
ruby -ryaml -e "%w[ci release dead-code quality-review dependency-audit cleanup-ci].each{|f| YAML.load_file(\"$H/profiles/ios/workflows/#{f}.yml\"); puts \"#{f}: valid\"}"
```

**Commit** — `feat(content): use {{XCODEPROJ}} for -project / SPM-cache paths`.

---

## Task 6: End-to-end verification + v6 bump

**Step 1:** Build. Verify the three real layouts render correct, valid, token-free YAML.

```bash
H=/Users/patrickserrano/Developer/harness
env -u GOROOT /opt/homebrew/bin/go build -o "$H/bin/harness" ./cmd/harness

check() { # $1=component  $2=xcodeproj  $3=label
  tmp=$(mktemp -d); ( cd "$tmp" && git init -q )
  printf '[project]\nname="p"\nproject_name="App"\nscheme="App"\nbundle_id="com.me.app"\nasc_app_id="9"\nxcodeproj="%s"\n\n[[component]]\npath="%s"\nprofiles=["ios"]\n' "$2" "$1" > "$tmp/.harness.toml"
  ( cd "$tmp" && HARNESS_ROOT="$H" "$H/bin/harness" sync >/dev/null )
  echo "--- $3 (component=$1 xcodeproj=$2) ---"
  grep -nE '\-project |swiftlint --strict|derivedDataPath|hashFiles' "$tmp/.github/workflows/ios-ci.yml" "$tmp/.github/workflows/ios-release.yml" 2>/dev/null | grep -iE 'project |swiftlint|DerivedData|hashFiles' | head -5
  echo -n "tokens remaining (0): "; grep -rho '{{[A-Z_]*}}' "$tmp/.github/workflows/" | sort -u | tr '\n' ' '; echo
  echo -n "ci.yml valid YAML? "; ruby -ryaml -e "YAML.load_file('$tmp/.github/workflows/ios-ci.yml'); puts 'yes'" 2>&1 | tail -1
  rm -rf "$tmp"
}
check "ios" "ios/App.xcodeproj"               "TEMPLATE"
check "."   "App.xcodeproj"                    "ROOT (rail-style)"
check "ios" "ios/Queueify/Queueify.xcodeproj"  "NESTED (Queueify-style)"
```

Expect: NESTED shows `-project ios/Queueify/Queueify.xcodeproj` AND `--config ios/.swiftlint.yml`/`derivedDataPath ios/DerivedData`; all three render 0 tokens and valid YAML.

**Step 2:** `env -u GOROOT /opt/homebrew/bin/go test ./... && go vet ./...`.

**Step 3:** `printf '6\n' > VERSION`; commit `feat: bump harness to v6 ({{XCODEPROJ}} + config-dir detection)`.

---

## Task 7: Security audit (gate)

**Step 1:** `/security-review` on `origin/main..HEAD`.

**Step 2:** Threat-model:
- **Injection via [project].xcodeproj -> {{XCODEPROJ}} -> CI `-project`/`hashFiles`.** Confirm `validateXcodeproj` rejects abs, `..`, shell metacharacters, spaces, newlines (RE2 `$`), and requires a `.xcodeproj` suffix. Try to defeat it.
- **detect** can't emit an escaping/abs xcodeproj or component path (paths are repo-relative under root; re-validated by config on the next load).
- Fail-closed: `{{XCODEPROJ}}` blank-but-present aborts sync.
- Prior guards unchanged.

**Step 3:** Fix ≥8 findings (test-first). **Step 4:** Commit.

---

## Done — what this milestone delivers

The harness models the xcodeproj path independently from the config/lint dir, so the three real layouts (template, rail, Queueify) all onboard with correct CI. Unblocks Queueify onboarding.

## Next

Onboard Queueify end-to-end on a branch (PR for review); then retire `ios-template`; then `scaffold`/`new`, `harvest`, Renovate.
