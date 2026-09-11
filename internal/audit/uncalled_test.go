package audit_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/audit"
)

// A manifest for a single-product iOS project. The only thing that varies
// between the two spellings below is whether a [[product]] declares `secrets`,
// which is the sole input tokens.ProductSecrets consults when deciding whether
// to render a caller for scripts/write-release-config.sh at all.
const (
	uncalledManifestNoSecrets = `[project]
name = "Probe"
project_name = "Probe"
scheme = "Probe"
xcodeproj = "Probe.xcodeproj"
swift_version = "6"
bundle_id = "com.example.probe"
asc_app_id = "1000000001"
github_org = "example-org"
stack = "ios"
tools = ["claude"]

[[component]]
path = "."
profiles = ["ios"]
`
	uncalledManifestWithSecrets = uncalledManifestNoSecrets + `
[[product]]
name = "Probe"
scheme = "Probe"
bundle_id = "com.example.probe"
asc_app_id = "1000000001"
test_target = "ProbeTests"
app_target = "Probe.app"
secrets_file = "Config/Monetization.xcconfig"
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY" }
`
)

// uncalledLacquer builds a stub lacquer root out of `files` (paths relative to
// the lacquer root) plus the two files every plan needs, and a git-backed
// project carrying `manifest`.
//
// Nothing is synced. UncalledScripts asks what this manifest WOULD render, which
// is the whole point — a project that has never been synced, or whose checkout
// is stale, must get the same answer as one that is up to date.
func uncalledLacquer(t *testing.T, manifest string, files map[string]string) (lacquer, project string) {
	t.Helper()
	lacquer = t.TempDir()
	project = t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "1\n")
	writeFile(t, filepath.Join(lacquer, "core", "CLAUDE.core.md"), "CORE RULES\n")
	if _, ok := files["profiles/ios/CLAUDE.ios.md"]; !ok {
		writeFile(t, filepath.Join(lacquer, "profiles", "ios", "CLAUDE.ios.md"), "IOS RULES\n")
	}
	for rel, body := range files {
		writeFile(t, filepath.Join(lacquer, filepath.FromSlash(rel)), body)
	}
	writeFile(t, filepath.Join(project, ".lacquer.toml"), manifest)
	git(t, project, "init", "-q")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "init")
	return lacquer, project
}

// uncalledDests runs the check and returns the reported paths, failing on error.
func uncalledDests(t *testing.T, lacquer, project string) []string {
	t.Helper()
	found, err := audit.UncalledScripts(lacquer, project)
	if err != nil {
		t.Fatalf("UncalledScripts: %v", err)
	}
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Dest)
	}
	sort.Strings(out)
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The release workflow whose caller only exists after token substitution. This
// is release.yml's real shape: the invocation is not in the file, a token is.
const releaseWithSecretsToken = `name: release
on:
  push:
    tags: ["v*"]
jobs:
  release:
    steps:
      - uses: actions/checkout@v4
{{IOS_PRODUCT_SECRETS}}
      - run: xcodebuild archive
`

// baseScripts is the minimum shipped tree the token cases need.
func baseScripts() map[string]string {
	return map[string]string{
		"profiles/ios/workflows/release.yml":                releaseWithSecretsToken,
		"profiles/ios/root/scripts/write-release-config.sh": "#!/usr/bin/env bash\necho seeding\n",
	}
}

// THE HEADLINE CASE, and the one a grep cannot answer. tokens.ProductSecrets
// builds the `scripts/write-release-config.sh …` line as a Go string and
// substitutes it into release.yml at {{IOS_PRODUCT_SECRETS}} — but only for a
// [[product]] that declares `secrets`, and behind an `if:` on the matrix leg.
// The literal filename appears in NO workflow source anywhere in profiles/, so a
// grep across the lacquer reports every project with secrets as broken.
func TestATokenRenderedCallerCountsAsACaller(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestWithSecrets, baseScripts())
	if got := uncalledDests(t, lacquer, project); has(got, "scripts/write-release-config.sh") {
		t.Fatalf("a script whose caller is rendered by {{IOS_PRODUCT_SECRETS}} was reported as uncalled: %v", got)
	}
}

// The same lacquer, the same workflow source, a manifest declaring no secrets —
// and now the token expands to nothing and the script ships inert. This is the
// measured instance from lacquer#333: shipped, documented as running, never
// invoked.
func TestAScriptWhoseOnlyCallerWasNotRenderedIsReported(t *testing.T) {
	files := baseScripts()
	files["profiles/ios/CLAUDE.ios.md"] = "The release workflow then runs `scripts/write-release-config.sh`, which seeds the xcconfig.\n"
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, files)

	found, err := audit.UncalledScripts(lacquer, project)
	if err != nil {
		t.Fatalf("UncalledScripts: %v", err)
	}
	var hit *audit.UncalledScript
	for i := range found {
		if found[i].Dest == "scripts/write-release-config.sh" {
			hit = &found[i]
		}
	}
	if hit == nil {
		t.Fatalf("a shipped script with no rendered caller was not reported: %+v", found)
	}
	if !hit.Confirmed() {
		t.Errorf("a complete sweep was reported as unconfirmable: %q", hit.Unconfirmable)
	}
	if len(hit.Documented) == 0 {
		t.Error("the lacquer's own CLAUDE.md region says this script runs and the report does not say so — " +
			"the documentation lie is the expensive half of this finding, not a footnote")
	}
	out := audit.FormatUncalledScripts(found)
	for _, want := range []string{"scripts/write-release-config.sh", "DOCUMENTED AS RUNNING", "CLAUDE.md#ios"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

// THE #319 SHAPE, first form. scripts/docs-relaxation.sh has no caller in any
// workflow or hook config on an iOS project; its caller is a SIBLING SCRIPT,
// scripts/docs-hook.sh, which .pre-commit-config.yaml runs. A check that only
// searched non-script units would report a code path that runs on every commit
// in eight repositories as dead.
func TestASiblingScriptCallerCountsThroughTheChain(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/root/.pre-commit-config.yaml":    "repos:\n  - hooks:\n      - entry: scripts/docs-hook.sh swiftlint-docs\n",
		"profiles/ios/root/scripts/docs-hook.sh":       "#!/usr/bin/env bash\nstate=$(scripts/docs-relaxation.sh .lacquer.toml || echo none)\n",
		"profiles/ios/root/scripts/docs-relaxation.sh": "#!/usr/bin/env bash\necho none\n",
	})
	for _, s := range []string{"scripts/docs-hook.sh", "scripts/docs-relaxation.sh"} {
		if got := uncalledDests(t, lacquer, project); has(got, s) {
			t.Errorf("%s is reached through a sibling script and was reported as uncalled: %v", s, got)
		}
	}
}

// The other half of the closure. Reachability is transitive from ROOTS that are
// never scripts, so two abandoned scripts that call each other cannot vouch for
// one another — which is what "any script counts as a caller" would have done.
func TestTwoDeadScriptsDoNotVouchForEachOther(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/root/scripts/alpha.sh": "#!/usr/bin/env bash\nscripts/beta.sh\n",
		"profiles/ios/root/scripts/beta.sh":  "#!/usr/bin/env bash\nscripts/alpha.sh\n",
	})
	got := uncalledDests(t, lacquer, project)
	for _, s := range []string{"scripts/alpha.sh", "scripts/beta.sh"} {
		if !has(got, s) {
			t.Errorf("%s is called only by another uncalled script and was treated as live: %v", s, got)
		}
	}
}

// profiles/ios/workflows/ci.yml carries the line "Reads the manifest directly
// rather than via docs-relaxation.sh". Counting a comment would mark that script
// called on every iOS project forever and mask the day its real caller went
// away — the finding silently becoming unreachable, which is the defect class.
func TestACommentNamingAScriptIsNotACaller(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/workflows/ci.yml": "name: ci\non:\n  pull_request:\njobs:\n  build:\n    steps:\n" +
			"      # Reads the manifest directly rather than via scripts/docs-relaxation.sh.\n" +
			"      - run: echo hi\n",
		"profiles/ios/root/scripts/docs-relaxation.sh": "#!/usr/bin/env bash\necho none\n",
	})
	if got := uncalledDests(t, lacquer, project); !has(got, "scripts/docs-relaxation.sh") {
		t.Fatalf("a script named only in a YAML comment was treated as called: %v", got)
	}
}

// THE #319 SHAPE, second form, and the one that fix was actually about. The
// project excluded the managed hook config and runs the same script from a
// workflow of its own. The managed unit does not do the work; the work happens.
// Reporting this project would be a false positive on a correct setup.
func TestAProjectOwnedCallerCountsAsACaller(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/root/scripts/check-secrets.sh": "#!/usr/bin/env bash\nexit 0\n",
	})
	writeFile(t, filepath.Join(project, "Makefile"), "audit:\n\tbash ./check-secrets.sh\n")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "makefile")

	if got := uncalledDests(t, lacquer, project); has(got, "scripts/check-secrets.sh") {
		t.Fatalf("a script the project's own Makefile runs — under the bare basename, not the shipped "+
			"path — was reported as uncalled: %v", got)
	}
}

// THE #319 SHAPE, third form: an invocation that is not a path at all.
// .github/scripts/test_fetch_testflight_feedback.py reaches its subject as
// `import fetch_testflight_feedback as tf` — no extension, no directory. A
// module imported by a script a hook runs is exercised, not dead weight.
func TestAPythonSiblingImportCountsAsACaller(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/root/.pre-commit-config.yaml": "repos:\n  - hooks:\n      - entry: python3 .github/scripts/runner.py\n",
		// Deliberately never spells the filename: only the module name appears.
		"profiles/ios/root/.github/scripts/runner.py": "import sys\nimport helper\n\nhelper.go()\n",
		"profiles/ios/root/.github/scripts/helper.py": "def go():\n    pass\n",
	})
	if got := uncalledDests(t, lacquer, project); has(got, ".github/scripts/helper.py") {
		t.Fatalf("a module imported by a script the hooks run was reported as uncalled: %v", got)
	}
}

// Documentation describes; it does not run. A shipped .md naming the script is
// exactly the state that made write-release-config.sh survive review, so it must
// annotate the finding rather than suppress it.
func TestProseNamingAScriptIsNotACaller(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		// A real caller surface, so this is a CONFIRMED finding rather than an
		// unsearchable project — otherwise the assertion below would hold for
		// the wrong reason.
		"profiles/ios/workflows/ci.yml":     "name: ci\non:\n  pull_request:\njobs:\n  build:\n    steps:\n      - run: echo hi\n",
		"profiles/ios/root/docs/release.md": "CI runs `scripts/ship.sh` on every tag.\n",
		"profiles/ios/root/scripts/ship.sh": "#!/usr/bin/env bash\nexit 0\n",
	})
	found, err := audit.UncalledScripts(lacquer, project)
	if err != nil {
		t.Fatalf("UncalledScripts: %v", err)
	}
	if len(found) != 1 || found[0].Dest != "scripts/ship.sh" {
		t.Fatalf("a script only its documentation mentions was treated as called: %+v", found)
	}
	if !found[0].Confirmed() {
		t.Fatalf("want a confirmed finding, got %q", found[0].Unconfirmable)
	}
	if !has(found[0].Documented, "docs/release.md") {
		t.Errorf("the document that claims this script runs is not named in the finding: %+v", found[0])
	}
}

// The other way the sweep can come up empty: the plan renders nothing that could
// hold a caller and the project owns nothing either. Nothing was searched, so
// nothing was learned — and "no caller found" after searching nowhere is the
// same vacuous pass as `jq 'all(.bucket != "pending")'` over `[]`, instance 4 in
// lacquer#333.
func TestNothingToSearchIsUnconfirmableRatherThanUncalled(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/root/scripts/ship.sh": "#!/usr/bin/env bash\nexit 0\n",
	})
	found, err := audit.UncalledScripts(lacquer, project)
	if err != nil {
		t.Fatalf("UncalledScripts: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("want the script reported, got %+v", found)
	}
	if found[0].Confirmed() {
		t.Fatal("a sweep with no caller surface at all reported a confirmed finding")
	}
}

// A skill's script is invoked by an agent reading the skill's own SKILL.md, so
// prose IS the caller there. Reporting them would put a dozen working scripts in
// front of the one real finding on every project.
func TestSkillPackageScriptsAreOutOfScope(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/skills/build-tuning/SKILL.md":           "Run the analyzer.\n",
		"profiles/ios/skills/build-tuning/scripts/analyze.py": "print('hi')\n",
	})
	if got := uncalledDests(t, lacquer, project); len(got) != 0 {
		t.Fatalf("skill-package scripts were reported: %v", got)
	}
}

// "Found nothing" must not read as "found nothing wrong". With no git there is
// no list of project-owned files, so half the sweep did not happen — and a
// project-owned caller is exactly what #319 proved exists. Saying "uncalled"
// here would be this tool committing the defect it detects.
func TestWithoutGitTheAnswerIsUnconfirmableRatherThanClean(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		// A rendered workflow, so the managed half of the sweep succeeds and the
		// only thing missing is the project's own files. Without it the check
		// would be unconfirmable for a second reason and this test would pass
		// whatever the git path did.
		"profiles/ios/workflows/ci.yml":     "name: ci\non:\n  pull_request:\njobs:\n  build:\n    steps:\n      - run: echo hi\n",
		"profiles/ios/root/scripts/ship.sh": "#!/usr/bin/env bash\nexit 0\n",
	})
	if err := os.RemoveAll(filepath.Join(project, ".git")); err != nil {
		t.Fatal(err)
	}
	found, err := audit.UncalledScripts(lacquer, project)
	if err != nil {
		t.Fatalf("UncalledScripts: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("want the script reported, got %+v", found)
	}
	if found[0].Confirmed() {
		t.Fatal("a sweep that could not read the project's own files reported a confirmed finding — " +
			"an unverified all-clear (or an unverified accusation) is the thing this detector is for")
	}
	if out := audit.FormatUncalledScripts(found); !strings.Contains(out, "UNCONFIRMABLE") {
		t.Errorf("the report does not distinguish an unfinished search from an answer:\n%s", out)
	}
}

// A declaration is not a caller. .gitignore naming a script's path, or
// .lacquer.toml mentioning it, states a fact about the file; neither runs it.
// Counting one would mark a dead script live and — because .gitignore is itself
// a managed region the lacquer writes — could do it fleet-wide from one line.
func TestADeclarationNamingAScriptIsNotACaller(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/root/scripts/ship.sh": "#!/usr/bin/env bash\nexit 0\n",
	})
	writeFile(t, filepath.Join(project, ".gitignore"), "scripts/ship.sh.log\nscripts/ship.sh\n")
	git(t, project, "add", "-A")
	git(t, project, "commit", "-q", "-m", "ignore")

	if got := uncalledDests(t, lacquer, project); !has(got, "scripts/ship.sh") {
		t.Fatalf("a script named only by .gitignore was treated as called: %v", got)
	}
}

// A project with no scripts in its plan has nothing to say, and silence must not
// cost a line of output. Most of the fleet's non-iOS projects are this.
func TestAPlanWithNoScriptsReportsNothing(t *testing.T) {
	lacquer, project := uncalledLacquer(t, uncalledManifestNoSecrets, map[string]string{
		"profiles/ios/workflows/ci.yml": "name: ci\non:\n  pull_request:\njobs: {}\n",
	})
	found, err := audit.UncalledScripts(lacquer, project)
	if err != nil {
		t.Fatalf("UncalledScripts: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("reported findings for a project shipping no scripts: %+v", found)
	}
	if out := audit.FormatUncalledScripts(found); out != "" {
		t.Errorf("empty report printed a section:\n%s", out)
	}
}
