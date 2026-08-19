// Package tokens substitutes the fixed set of per-project placeholders into
// synced content. Values come from the manifest's [project] block plus a derived
// component prefix. Only these exact {{KEY}} literals are touched; GitHub Actions
// ${{ ... }} is never matched.
package tokens

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/patrickserrano/lacquer/internal/config"
	"github.com/patrickserrano/lacquer/internal/detect"
)

// Token names.
const (
	ProjectName     = "{{PROJECT_NAME}}"
	Scheme          = "{{SCHEME}}"
	BundleID        = "{{BUNDLE_ID}}"
	AscAppID        = "{{ASC_APP_ID}}"
	Xcodeproj       = "{{XCODEPROJ}}"
	SwiftVersion    = "{{SWIFT_VERSION}}"
	GithubOrg       = "{{GITHUB_ORG}}"
	ComponentPrefix = "{{COMPONENT_PREFIX}}"
	// ComponentToRoot is the inverse of ComponentPrefix: the relative path from
	// a component's own directory back UP to the repo root — "." at the root,
	// ".." one level down, "../.." two. ComponentPrefix points down and can only
	// be read from the repo root; this points up and can only be read from inside
	// the component. A config file that ships INTO the component dir and has to
	// name something at the repo root needs the second one, and until this token
	// existed there was no way to write it.
	//
	// It exists because of biome. profiles/web/config/biome.json sets
	// `vcs.useIgnoreFile: true`, and biome resolves that against biome.json's OWN
	// folder: with the config in `admin/` and the only .gitignore at the repo
	// root, biome does not merely skip the ignore file, it refuses to start —
	// "Biome couldn't find an ignore file in the following folder: …/admin",
	// then "Biome exited because the configuration resulted in errors". Every
	// fresh web component was lint-broken out of the box. `vcs.root` moves the
	// lookup to the repo root, and its value is exactly this token.
	//
	// The root layout is why this cannot be a hardcoded "..": a component AT the
	// repo root would point vcs.root one level ABOVE the repository, where biome
	// aborts with the same error against the parent directory (measured). "." is
	// the only correct value there.
	//
	// vcs.root is relative to the CONFIG FILE's directory, not to the working
	// directory. Those are the same on every shipped invocation — CI, lefthook
	// and `lacquer fix` all run biome FROM the component directory — so the two
	// readings are indistinguishable there, and getting it wrong would put the
	// value on the wrong thing the first time something ran from elsewhere. It
	// was measured apart on biome 2.5.5 by running from a directory BELOW the
	// config: config in `admin/`, cwd `admin/src/`, `root: ".."` looked in the
	// repo root and `root: "../.."` one above it. So the depth that decides this
	// value is the CONFIG's depth — which is exactly what the component prefix
	// records.
	//
	// (Under `--config-path` the resolution is not this simple; it was measured
	// and not characterised. No shipped invocation passes that flag; the only
	// caller that does is `lacquer doctor`, whose biome probes this fix repairs
	// — see profiles/web/doctor.toml.)
	ComponentToRoot = "{{COMPONENT_TO_ROOT}}"
	// WebBuildEnv expands to an entire job-level `env:` block, or to nothing.
	// Unlike every other token it is not a scalar spliced into a line: a project
	// with no [project].build_env must get NO env key at all, and one with
	// entries must get a correctly indented block. So it sits alone at column 0
	// on its own line in the template and owns its own indentation.
	//
	// That is the one place a template stops being parseable YAML on its own,
	// which is a real property to give up — it is what let this repo extract and
	// `bash -n` every run: block. TestRenderedWorkflowsAreValidYAML in
	// internal/shipped replaces it with something stronger: the RENDERED output
	// is parsed, for both the empty and populated cases. What ships is what gets
	// checked.
	WebBuildEnv = "{{WEB_BUILD_ENV}}"
	// IOSProductCatalog is every product as a single-line JSON array, embedded in
	// the release workflow's selection step. That step filters it by tag prefix
	// and emits the matrix, because GitHub does not expose the `matrix` context
	// to a job-level `if:` — a leg cannot skip itself, so the set has to be
	// decided before the matrix exists.
	IOSProductCatalog = "{{IOS_PRODUCT_CATALOG}}"
	// IOSProductChoices is the workflow_dispatch dropdown of releasable
	// products: `all` plus one entry per product.
	//
	// A dropdown rather than free text because this is where an operator picks
	// by hand, and a typo would otherwise reach the fail-closed branch and waste
	// a run. Like WebBuildEnv it stands alone at column 0 and owns its own
	// indentation, because product names contain spaces ("A Bible Verse Each Day
	// Paid") and each option has to be quoted on its own line.
	IOSProductChoices = "{{IOS_PRODUCT_CHOICES}}"
	// IOSProductSecrets expands to one release-time configuration step per
	// product that declares [[product]].secrets, or to nothing at all.
	//
	// One static step per product rather than a single step indexing
	// `secrets[matrix.product.…]`. GitHub does support dynamic context indexing,
	// but it makes the secret names invisible in the workflow — you could no
	// longer grep a repo to learn which secrets its release needs, and a secret
	// nobody knows to set is one that fails at release time. The names are the
	// documentation.
	//
	// Like WEB_BUILD_ENV it stands alone at column 0 and owns its indentation,
	// because the common case renders NOTHING and a leftover blank `- name:`
	// would be a syntax error.
	IOSProductSecrets = "{{IOS_PRODUCT_SECRETS}}"
	// IOSReleaseTags is the release workflow's push-tag filter, derived from the
	// products' tag prefixes.
	//
	// It has to be derived, not fixed at 'v*'. One project tags `steps-v1.2.3`
	// and `stepsfree-v1.2.3`, neither of which starts with `v` — under a fixed
	// filter, adopting this workflow would mean no tag ever starts a release.
	// Nothing errors and nothing runs, which is the worst way for a release
	// pipeline to break.
	IOSReleaseTags = "{{IOS_RELEASE_TAGS}}"
	// The IOS_CI_* family makes the iOS CI workflow build and test EVERY declared
	// [[product]] instead of the one scheme in [project].
	//
	// They exist as a family of small tokens, rather than one big block, because
	// of a constraint that dominates every other consideration here: TWELVE
	// projects declare no [[product]] at all, and each of these must render to
	// EXACTLY the text ci.yml carried before products reached it. Any difference
	// — a re-indented line, a stray blank line, a variable where a literal was —
	// is drift in twelve repositories at once, on the same day.
	//
	// So each token sits inline, at the one place its value differs, and the
	// single-product value is by construction the old spelling. The matrix legs
	// only appear when there is genuinely more than one product to build.
	//
	// TestIOSCISingleProductRenderIsUnchanged pins that: it renders the template
	// twice — once through these functions, once with each token replaced by its
	// documented legacy spelling — and requires the two to be byte-identical.
	//
	// IOSCIProductSuffix is appended to the `name:` of the matrixed jobs, so the
	// legs are distinguishable in the checks list. Empty for one product, where
	// GitHub would otherwise show "Test (Demo)" where it has always shown "Test".
	IOSCIProductSuffix = "{{IOS_CI_PRODUCT_SUFFIX}}"
	// IOSCIBuildStrategy and IOSCITestStrategy expand to the `strategy:` block
	// (plus the job-level `env:` hoisting the leg's values) for the build and
	// test jobs, or to nothing at all.
	//
	// They sit at the END of the preceding line — `timeout-minutes: 25{{…}}` —
	// and open with their own newline. A token alone on its own line would leave
	// a BLANK line behind when it renders empty, and a blank line is a byte
	// difference in twelve repositories.
	IOSCIBuildStrategy = "{{IOS_CI_BUILD_STRATEGY}}"
	IOSCITestStrategy  = "{{IOS_CI_TEST_STRATEGY}}"
	// IOSCIScheme is what `xcodebuild -scheme` is given: the literal scheme for
	// one product, the hoisted `$PRODUCT_SCHEME` for a matrix.
	IOSCIScheme = "{{IOS_CI_SCHEME}}"
	// IOSCIOnlyTesting is the `-only-testing:` argument list — one selector, or
	// two when the product declares a UI test target, plus one per entry in
	// extra_test_targets.
	IOSCIOnlyTesting = "{{IOS_CI_ONLY_TESTING}}"
	// IOSCIExtraTestSetup builds the `-only-testing:` arguments for a MATRIX
	// leg's extra_test_targets, as a bash array, before xcodebuild is invoked.
	//
	// An array rather than a bare string because an Xcode target name may contain
	// spaces ("A Bible Verse Each Day FreeTests" is a real one), so word-splitting
	// a joined value would pass two selectors that each match nothing — and
	// matching nothing exits 0. The legs' lists are carried through the matrix
	// newline-separated for the same reason: the target charset admits spaces and
	// forbids newlines, so the newline is the only separator that cannot occur
	// inside a value.
	//
	// Empty unless the project has a matrix AND some product declares extras, so
	// every project that predates this field renders the workflow it already had.
	IOSCIExtraTestSetup = "{{IOS_CI_EXTRA_TEST_SETUP}}"
	// IOSCIVerifySelectors is the whole "Verify Test Selectors Matched" step, or
	// nothing.
	//
	// `xcodebuild` exits 0 for an `-only-testing:` selector that matches no
	// tests. Every additional selector is therefore an additional way to convert
	// a maintained suite into a no-op and report it as a pass — the same silent
	// green this repository keeps finding. The step reads the result bundle back
	// and fails the job for any selector that produced no test bundle.
	//
	// It renders only for a product that declares extra_test_targets. Rendering
	// it for everyone would be the better guard and is not available: it would
	// change the workflow in every repository that has one, which is the one
	// thing this file is not allowed to do. Opting in hardens test_target and
	// ui_test_target too, since the step checks every selector the job passed.
	IOSCIVerifySelectors = "{{IOS_CI_VERIFY_SELECTORS}}"
	// IOSPrecommitOnlyTesting is the `-only-testing:` argument list for the
	// pre-commit `Swift Tests` hook.
	//
	// The hook hardcoded `-only-testing:{{PROJECT_NAME}}Tests`, so a project that
	// added a package suite would have it run in CI and not locally: the hook
	// passes, the PR fails, and the developer's fastest feedback loop is the one
	// covering least. That divergence is the trap, so the extras are emitted in
	// both places.
	//
	// The base selector stays PROJECT_NAME + "Tests" rather than becoming the
	// first product's test_target. That spelling is what every repository already
	// renders, and changing it here would rewrite the hook in all of them — a
	// separate defect for a separate change.
	IOSPrecommitOnlyTesting = "{{IOS_PRECOMMIT_ONLY_TESTING}}"
	// IOSCIAppTarget is the built product coverage is reported for, as it appears
	// in prose and in the step summary.
	IOSCIAppTarget = "{{IOS_CI_APP_TARGET}}"
	// IOSCICoverageJQ is the whole `jq` argument for the coverage query. It is a
	// token rather than a scalar inside the program because the program is in
	// SINGLE quotes, where a shell variable does not expand — the matrix form has
	// to pass the target through `--arg` instead, which changes the argument list
	// and not just a value inside it.
	IOSCICoverageJQ = "{{IOS_CI_COVERAGE_JQ}}"
	// IOSCIArtifactSuffix scopes the test-results artifact per product. Two legs
	// uploading `ios-test-results` would collide.
	IOSCIArtifactSuffix = "{{IOS_CI_ARTIFACT_SUFFIX}}"
	// IOSCISimSuffix and IOSCISimMatch scope the CI simulator per product.
	//
	// This is the sharp edge of putting a matrix on the test job. The simulator
	// is named `CI-iPhone-${GITHUB_REPOSITORY_ID}` and the job DELETES every
	// simulator matching that name before creating its own — a design that is
	// correct only while one job per repository exists at a time. Two matrix legs
	// on the same repository would share the name, and the second leg's cleanup
	// would delete the simulator the first is mid-test on. That failure is not
	// hypothetical: it is the same one the repository-scoping was introduced to
	// fix, reported as "the test runner crashed before establishing connection".
	//
	// IOSCISimMatch exists because the cleanup greps UNANCHORED. With products
	// named Steps and StepsFree — a real pair — `CI-iPhone-1-steps` is a
	// substring of `CI-iPhone-1-stepsfree`, so scoping the name alone would not
	// have fixed anything. Matching `"$SIM_NAME ("` pins the match to the end of
	// the name in `simctl list devices` output, where ` (` always follows it.
	IOSCISimSuffix = "{{IOS_CI_SIM_SUFFIX}}"
	IOSCISimMatch  = "{{IOS_CI_SIM_MATCH}}"
	// DependabotUpdates expands to the `updates:` list of .github/dependabot.yml:
	// one github-actions entry for the repo, plus one npm entry per web
	// component, each pointing at that component's directory.
	//
	// It has to be generated because `directory:` is per-component and Dependabot
	// has no glob for it — a hand-written file silently covers only the paths
	// somebody remembered, and a dependency manifest nobody watches is the thing
	// this is meant to prevent.
	DependabotUpdates = "{{DEPENDABOT_UPDATES}}"
)

// entry is a registered token and whether a non-empty value is required. A
// required token present in content with an empty value is a fail-closed
// "missing"; ComponentPrefix is not required (empty is valid for a root layout).
type entry struct {
	token        string
	requireValue bool
}

var registry = []entry{
	{ProjectName, true},
	{Scheme, true},
	{BundleID, true},
	{AscAppID, true},
	{Xcodeproj, true},
	{SwiftVersion, true},
	{GithubOrg, false}, // empty is valid: a project may not have a repo/org yet
	{ComponentPrefix, false},
	// Required, unlike ComponentPrefix. The two are not symmetric: the empty
	// prefix is the correct rendering for a root layout, whereas ToRoot never
	// returns "" for any valid component path — the root case is ".". An empty
	// value here could only mean the derivation broke, and the failure it would
	// ship is silent: `"root": ""` sends biome's ignore-file lookup back to the
	// config's own folder, which is the exact abort this token exists to prevent.
	{ComponentToRoot, true},
	{WebBuildEnv, false}, // empty is valid and common: most projects need no build secrets
	{IOSProductCatalog, false},
	{IOSProductChoices, false},
	{IOSProductSecrets, false},
	{IOSReleaseTags, false},
	{IOSCIProductSuffix, false},
	{IOSCIBuildStrategy, false},
	{IOSCITestStrategy, false},
	// Required, exactly as {{SCHEME}} and {{PROJECT_NAME}} were before these
	// replaced them in ci.yml. A manifest with a blank scheme or project_name
	// must keep failing the sync loudly; it must not quietly render
	// `-only-testing:Tests` against a scheme nobody named.
	{IOSCIScheme, true},
	{IOSCIOnlyTesting, true},
	// Required for the same reason: the hook's selector list is never legitimately
	// empty, and an empty one would render `xcodebuild test` with no
	// `-only-testing:` at all — which runs a DIFFERENT (larger) set than CI and
	// would look like the hook simply got slower.
	{IOSPrecommitOnlyTesting, true},
	// Not required: empty is the correct and overwhelmingly common rendering —
	// no extra selectors declared, so no array to build and nothing to verify.
	{IOSCIExtraTestSetup, false},
	{IOSCIVerifySelectors, false},
	{IOSCIAppTarget, true},
	{IOSCICoverageJQ, true},
	{IOSCIArtifactSuffix, false},
	{IOSCISimSuffix, false},
	{IOSCISimMatch, false},
	{DependabotUpdates, false},
}

// Prefix converts a component path to a path prefix: "." -> "", "ios" -> "ios/".
func Prefix(path string) string {
	if path == "." || path == "" {
		return ""
	}
	return path + "/"
}

// ToRoot inverts Prefix: given a component PREFIX ("", "ios/", "apps/web/") it
// returns the relative path from that component's directory back up to the repo
// root — ".", "..", "../..".
//
// It takes the prefix rather than the raw component path because that is what
// every substitution site already carries (assets.Asset.Prefix, regionWrite.
// prefix); deriving both values from the same input keeps them from drifting
// apart on a layout the other one handled.
//
// "." for the root case, not "": callers splice this into a config value where
// the empty string means something else entirely (see the ComponentToRoot
// comment), and "." is what a path-relative-to-itself is written as.
//
// Segment counting is safe here because config.validateComponentPath has
// already rejected absolute paths, "..", and anything that is not a plain
// slash-separated name — so a prefix is always N literal segments deep.
func ToRoot(prefix string) string {
	p := strings.Trim(prefix, "/")
	if p == "" || p == "." {
		return "."
	}
	return strings.TrimSuffix(strings.Repeat("../", strings.Count(p, "/")+1), "/")
}

// Values builds the substitution map from the [project] values plus the derived
// component prefix for the content being substituted.
//
// products is the project's shippable apps. Pass cfg.Products(), which is never
// empty — it synthesises a single entry from [project] when none is declared, so
// there is no "has products" branch anywhere downstream.
func Values(cfg *config.Config, prefix string) map[string]string {
	p := cfg.Project
	products := cfg.Products()
	return map[string]string{
		ProjectName:       p.ProjectName,
		Scheme:            p.Scheme,
		BundleID:          p.BundleID,
		AscAppID:          p.AscAppID,
		Xcodeproj:         p.Xcodeproj,
		SwiftVersion:      p.SwiftVersion,
		GithubOrg:         p.GithubOrg,
		ComponentPrefix:   prefix,
		ComponentToRoot:   ToRoot(prefix),
		WebBuildEnv:       BuildEnvBlock(p.BuildEnv),
		IOSProductCatalog: ProductCatalog(products),
		IOSProductChoices: ProductChoices(products),
		IOSProductSecrets: ProductSecrets(products),
		IOSReleaseTags:    ReleaseTags(products),

		IOSCIProductSuffix:   CIProductSuffix(products),
		IOSCIBuildStrategy:   CIBuildStrategy(products),
		IOSCITestStrategy:    CITestStrategy(products),
		IOSCIScheme:          CIScheme(products),
		IOSCIOnlyTesting:     CIOnlyTesting(products),
		IOSCIExtraTestSetup:  CIExtraTestSetup(products),
		IOSCIVerifySelectors: CIVerifySelectors(products),

		IOSPrecommitOnlyTesting: PrecommitOnlyTesting(p.ProjectName, products),

		IOSCIAppTarget:      CIAppTarget(products),
		IOSCICoverageJQ:     CICoverageJQ(products),
		IOSCIArtifactSuffix: CIArtifactSuffix(products),
		IOSCISimSuffix:      CISimSuffix(products),
		IOSCISimMatch:       CISimMatch(products),

		DependabotUpdates: dependabotUpdates(cfg),
	}
}

// multi reports whether the iOS CI workflow needs a matrix at all.
//
// One product — declared or synthesised from [project] — renders the flat,
// pre-matrix workflow. That is not an optimisation; it is the requirement. The
// twelve projects that declare nothing must receive a byte-identical file.
func multi(products []config.Product) bool { return len(products) > 1 }

// CIProductSuffix distinguishes the matrix legs in the checks list.
func CIProductSuffix(products []config.Product) string {
	if !multi(products) {
		return ""
	}
	return " ${{ matrix.product.name }}"
}

// CIBuildStrategy renders the Build (Release) job's matrix, or nothing.
func CIBuildStrategy(products []config.Product) string {
	if !multi(products) {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n    # One leg per shippable app. This block is GENERATED from the manifest's")
	b.WriteString("\n    # [[product]] entries and is absent entirely for a project that declares")
	b.WriteString("\n    # none — which is what lets one workflow serve both without a second code")
	b.WriteString("\n    # path, and why forking it to build a free variant is no longer necessary.")
	b.WriteString(ciMatrixHeader())
	for _, p := range products {
		fmt.Fprintf(&b, "\n          - name: %q", p.Name)
		fmt.Fprintf(&b, "\n            scheme: %q", p.Scheme)
	}
	b.WriteString("\n    env:")
	// Hoisted once, exactly as the release workflow does it: shell steps read
	// $PRODUCT_*, and `with:` / `name:` blocks read ${{ matrix.product.* }}
	// directly because job env is not in scope there.
	b.WriteString("\n      PRODUCT_SCHEME: ${{ matrix.product.scheme }}")
	return b.String()
}

// CITestStrategy renders the Test job's matrix, or nothing.
//
// It carries more per-leg values than the build matrix because a paid and a free
// variant do not merely build from different schemes — they compile different
// test bundles and produce differently-named .app targets. Running the paid
// product's test target against the free scheme is not a failure; it is a green
// run over a suite that never touched the code that shipped, which is the exact
// silent pass this change exists to prevent.
func CITestStrategy(products []config.Product) string {
	if !multi(products) {
		return ""
	}
	hasExtras := anyExtra(products)
	var b strings.Builder
	b.WriteString("\n    # One leg per shippable app, GENERATED from [[product]]. A paid and a free")
	b.WriteString("\n    # variant compile DIFFERENT test bundles, so testing one target is not a")
	b.WriteString("\n    # partial result — it is a green run over a suite that never covered the")
	b.WriteString("\n    # code the other app ships.")
	b.WriteString("\n    #")
	b.WriteString("\n    # PRODUCT_SLUG below also scopes this leg's simulator name and its uploaded")
	b.WriteString("\n    # results. Two legs sharing either one would have the second delete the")
	b.WriteString("\n    # simulator the first is mid-test on, or fail its artifact upload.")
	b.WriteString(ciMatrixHeader())
	for _, p := range products {
		fmt.Fprintf(&b, "\n          - name: %q", p.Name)
		fmt.Fprintf(&b, "\n            scheme: %q", p.Scheme)
		fmt.Fprintf(&b, "\n            test_target: %q", p.TestTargetName())
		// Always emitted, blank included: a key missing from one leg and present
		// on another makes `matrix.product.ui_test_target` undefined there, and
		// the job env would render an empty value anyway. Stating it keeps the
		// legs the same shape and the intent readable.
		fmt.Fprintf(&b, "\n            ui_test_target: %q", p.UITestTarget)
		// Only when some product declares extras, so a project that predates the
		// field keeps the exact legs it already renders. Emitted for EVERY leg
		// once any leg needs it, blank included, for the reason above: a key
		// present on one leg and absent on another makes the expression undefined
		// where it is missing.
		//
		// Newline-separated inside a double-quoted YAML scalar (Go's %q writes the
		// separator as \n, which YAML unescapes). Target names may contain spaces
		// and may not contain newlines, so this is the one separator that cannot
		// appear inside a value and split a target in half.
		if hasExtras {
			fmt.Fprintf(&b, "\n            extra_test_targets: %q", strings.Join(p.ExtraTestTargets, "\n"))
		}
		fmt.Fprintf(&b, "\n            app_target: %q", p.AppTargetName())
		fmt.Fprintf(&b, "\n            artifact: %q", p.Slug())
	}
	b.WriteString("\n    env:")
	b.WriteString("\n      PRODUCT_SCHEME: ${{ matrix.product.scheme }}")
	b.WriteString("\n      PRODUCT_SLUG: ${{ matrix.product.artifact }}")
	b.WriteString("\n      TEST_TARGET: ${{ matrix.product.test_target }}")
	b.WriteString("\n      UI_TEST_TARGET: ${{ matrix.product.ui_test_target }}")
	if hasExtras {
		b.WriteString("\n      EXTRA_TEST_TARGETS: ${{ matrix.product.extra_test_targets }}")
	}
	b.WriteString("\n      APP_TARGET: ${{ matrix.product.app_target }}")
	return b.String()
}

// anyExtra reports whether any product declares extra `-only-testing:`
// selectors.
//
// Every renderer below branches on it rather than on "does this product have
// extras", because the matrix legs and the job env must have the same SHAPE as
// each other — and because a project that declares none has to receive the
// workflow it already has, byte for byte.
func anyExtra(products []config.Product) bool {
	for _, p := range products {
		if len(p.ExtraTestTargets) > 0 {
			return true
		}
	}
	return false
}

// ciMatrixHeader is the part every matrixed CI job shares.
//
// fail-fast stays false for the reason it is false in the release workflow: one
// product failing must not cancel the other's run. A cancelled sibling reports
// as `cancelled`, which the CI OK aggregator treats as a failure — so a single
// real failure in the paid app would present as both apps being broken, and the
// free app's genuine result would never have been computed.
func ciMatrixHeader() string {
	return "\n    strategy:" +
		"\n      # One product failing must not cancel the other's run: they are" +
		"\n      # separate apps with separate outcomes, and a cancelled sibling" +
		"\n      # reports no result at all." +
		"\n      fail-fast: false" +
		"\n      matrix:" +
		"\n        product:"
}

// CIScheme is the value of `xcodebuild -scheme`, already inside quotes in the
// template.
func CIScheme(products []config.Product) string {
	if multi(products) {
		return "$PRODUCT_SCHEME"
	}
	return products[0].Scheme
}

// CIOnlyTesting renders the `-only-testing:` arguments.
//
// The matrix form uses `${UI_TEST_TARGET:+…}` rather than a GitHub expression so
// a blank UI target contributes no argument at all. An empty `-only-testing:`
// selector is not ignored by xcodebuild — it matches nothing, and a test run
// that selects nothing exits 0.
//
// The single-product form inlines the extras as literals; the matrix form
// appends the array CIExtraTestSetup built, and only when there is one, so a
// project declaring no extras renders the argument list it already renders.
func CIOnlyTesting(products []config.Product) string {
	if multi(products) {
		out := `"-only-testing:$TEST_TARGET" ${UI_TEST_TARGET:+"-only-testing:$UI_TEST_TARGET"}`
		if anyExtra(products) {
			// Quoted array expansion, so a target name with a space stays one
			// argument. Empty on a leg that declares no extras, which adds no
			// argument rather than an empty one.
			out += ` "${EXTRA_ONLY_TESTING[@]}"`
		}
		return out
	}
	p := products[0]
	name := p.TestTargetName()
	if name == "" {
		return "" // fail closed: the token is required, so sync reports it missing
	}
	out := fmt.Sprintf("%q", "-only-testing:"+name)
	if p.UITestTarget != "" {
		out += " " + fmt.Sprintf("%q", "-only-testing:"+p.UITestTarget)
	}
	for _, t := range p.ExtraTestTargets {
		out += " " + fmt.Sprintf("%q", "-only-testing:"+t)
	}
	return out
}

// CIExtraTestSetup builds the matrix leg's extra selectors into a bash array.
//
// Only the matrix form needs it: with one product the selectors are literals in
// the command line already. Empty otherwise, including for a matrix where no
// product declares extras.
func CIExtraTestSetup(products []config.Product) string {
	if !multi(products) || !anyExtra(products) {
		return ""
	}
	return "\n" +
		"\n          # This leg's extra `-only-testing:` selectors, newline-separated in" +
		"\n          # EXTRA_TEST_TARGETS. Built as an ARRAY because an Xcode target name may" +
		"\n          # contain spaces, and a joined string would word-split into selectors that" +
		"\n          # each match nothing — which xcodebuild reports as success." +
		"\n          EXTRA_ONLY_TESTING=()" +
		"\n          while IFS= read -r extra_target; do" +
		"\n            [ -n \"$extra_target\" ] || continue" +
		"\n            EXTRA_ONLY_TESTING+=(\"-only-testing:$extra_target\")" +
		"\n          done <<< \"$EXTRA_TEST_TARGETS\""
}

// CIVerifySelectors renders the step that proves the selectors this job passed
// actually selected something.
//
// This is the defence the extra selectors are not safe without. xcodebuild does
// not fail on an `-only-testing:` selector that matches no tests: it runs the
// rest, exits 0, and the check goes green over a suite that did not execute. A
// stale target name, a renamed package, a typo — each produces a passing run
// that covers less than the day before, and nothing in the output says so.
//
// So the step reads the result bundle back and requires every selector to appear
// among the bundles that ran. It fails CLOSED: an unreadable bundle, a missing
// tool or a changed schema all leave the executed-bundle list empty, every
// selector unmatched, and the job red. "I could not tell" is not a pass.
//
// Empty for a project with no extras, which is what keeps every existing
// repository's workflow byte-identical.
func CIVerifySelectors(products []config.Product) string {
	if !anyExtra(products) {
		return ""
	}
	// The heredoc body: literal names with one product, the hoisted env values
	// with a matrix. It is an UNQUOTED heredoc either way — target names are
	// charset-validated to letters, digits, space, dot, underscore and dash, so
	// a literal name cannot contain anything the shell would expand.
	var body string
	if multi(products) {
		body = "\n          $TEST_TARGET" +
			"\n          $UI_TEST_TARGET" +
			"\n          $EXTRA_TEST_TARGETS"
	} else {
		for _, sel := range products[0].TestSelectors() {
			body += "\n          " + sel
		}
	}
	return "\n" +
		"\n      - name: Verify Test Selectors Matched" +
		"\n        # `xcodebuild` exits 0 when an `-only-testing:` selector matches NOTHING." +
		"\n        # Every selector is therefore a way to turn a maintained suite into a" +
		"\n        # no-op and still report a pass — a renamed package, a stale target, a" +
		"\n        # typo. This step reads the results back and fails the job for any" +
		"\n        # selector that produced no test bundle, so \"it ran\" is checked rather" +
		"\n        # than assumed." +
		"\n        #" +
		"\n        # GENERATED from [[product]].extra_test_targets. A project declaring none" +
		"\n        # does not render this step at all." +
		"\n        if: always() && hashFiles('TestResults.xcresult') != ''" +
		"\n        run: |" +
		"\n          set -o pipefail" +
		"\n          # The bundles that actually executed, one name per line. `.xctest` is" +
		"\n          # stripped because a selector names the TARGET, not the built bundle." +
		"\n          #" +
		"\n          # No `|| true` anywhere: if this cannot be read the list is empty, every" +
		"\n          # selector below is unmatched, and the job fails. That is deliberate —" +
		"\n          # a verification that cannot verify must not report success." +
		"\n          RAN=$(xcrun xcresulttool get test-results tests --path TestResults.xcresult \\" +
		"\n            | jq -r '.. | objects | select(.nodeType? == \"Unit test bundle\" or .nodeType? == \"UI test bundle\") | .name' \\" +
		"\n            | sed 's/\\.xctest$//')" +
		"\n          MISSED=\"\"" +
		"\n          while IFS= read -r selector; do" +
		"\n            [ -n \"$selector\" ] || continue" +
		"\n            printf '%s\\n' \"$RAN\" | grep -Fxq \"$selector\" || MISSED=\"${MISSED}${MISSED:+, }$selector\"" +
		"\n          done <<SELECTORS" +
		body +
		"\n          SELECTORS" +
		"\n          if [ -n \"$MISSED\" ]; then" +
		"\n            echo \"::error::These -only-testing: selectors matched no tests: $MISSED. xcodebuild exits 0 for a selector that matches nothing, so those suites did not run and this job would otherwise be green.\"" +
		"\n            echo \"Test bundles that did run:\"" +
		"\n            printf '%s\\n' \"$RAN\"" +
		"\n            exit 1" +
		"\n          fi" +
		"\n          echo \"Every -only-testing: selector matched a test bundle that ran.\""
}

// PrecommitOnlyTesting is the `-only-testing:` list the pre-commit Swift Tests
// hook runs.
//
// The hook and CI must select the same suites. They did not: the hook hardcoded
// the app's own bundle, so a project adding a package suite would have it run in
// CI and never locally — the fast loop covering strictly less than the slow one,
// which is the wrong way round and shows up as a PR that fails on something
// pre-commit just approved.
//
// The extras are the UNION across products, deduplicated in declaration order.
// The hook runs [project].scheme, one scheme, so there is no product to pick;
// and a selector naming a suite that scheme does not build costs nothing here,
// because xcodebuild ignores it. That asymmetry is only safe locally — CI, where
// ignoring it would be a false green, has the verification step instead.
func PrecommitOnlyTesting(projectName string, products []config.Product) string {
	if projectName == "" {
		return "" // fail closed: the token is required, so sync reports it missing
	}
	// The base is PROJECT_NAME + "Tests", which is what the hook already
	// rendered in every repository. Changing it to the product's test_target
	// would be a fix to a different bug and would rewrite the hook everywhere.
	out := fmt.Sprintf("%q", "-only-testing:"+projectName+"Tests")
	seen := map[string]bool{projectName + "Tests": true}
	for _, p := range products {
		for _, t := range p.ExtraTestTargets {
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			out += " " + fmt.Sprintf("%q", "-only-testing:"+t)
		}
	}
	return out
}

// CIAppTarget is the built product coverage is reported for.
func CIAppTarget(products []config.Product) string {
	if multi(products) {
		return "$APP_TARGET"
	}
	return products[0].AppTargetName()
}

// CICoverageJQ is the full `jq` argument list for the coverage query.
//
// The single-product form inlines the target into the program, which is what
// shipped and is safe: the value is charset-validated and the program is single-
// quoted. The matrix form cannot do that — a shell variable does not expand
// inside single quotes — so it passes the name in with `--arg` instead, which is
// also the form that would survive a target name containing a quote if the
// charset ever widened.
func CICoverageJQ(products []config.Product) string {
	if multi(products) {
		return `--arg app "$APP_TARGET" '.targets[] | select(.name == $app) | .lineCoverage * 100'`
	}
	app := products[0].AppTargetName()
	if app == "" {
		return "" // fail closed, as above
	}
	return fmt.Sprintf(`'.targets[] | select(.name == %q) | .lineCoverage * 100'`, app)
}

// CIArtifactSuffix scopes the uploaded test results per product.
func CIArtifactSuffix(products []config.Product) string {
	if !multi(products) {
		return ""
	}
	// `with:` cannot read job env, so this reads the matrix directly.
	return "-${{ matrix.product.artifact }}"
}

// CISimSuffix scopes the CI simulator's NAME per product, and CISimMatch scopes
// what the stale-simulator cleanup matches. See the token declarations for why
// the second one is not redundant.
func CISimSuffix(products []config.Product) string {
	if !multi(products) {
		return ""
	}
	return "-${PRODUCT_SLUG}"
}

// CISimMatch anchors the cleanup's grep to the end of the simulator name.
func CISimMatch(products []config.Product) string {
	if !multi(products) {
		return ""
	}
	// `simctl list devices` prints "    <name> (<udid>) (<state>)", so " (" is
	// always what follows a name and never appears inside a slug.
	return " ("
}

// ProductCatalog renders every product as one line of JSON.
//
// Hand-built rather than encoding/json because the output must be a single line
// with no surprises inside a YAML scalar, and every value is already charset-
// validated at config.Load — no quote, backslash or newline can reach here.
func ProductCatalog(products []config.Product) string {
	var b strings.Builder
	b.WriteString("[")
	for i, p := range products {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"name":%q,"scheme":%q,"bundle_id":%q,"asc_app_id":%q,"tag_prefix":%q}`,
			p.Name, p.Scheme, p.BundleID, p.AscAppID, p.TagPrefix)
	}
	b.WriteString("]")
	return b.String()
}

// BuildEnvBlock renders [project].build_env as a job-level YAML env block, or
// "" when there is nothing to declare.
//
// Names only — the values are read from `secrets` at run time, so nothing
// sensitive is ever written into a synced file. Names are charset-validated at
// config.Load (POSIX env-name only), which is what keeps this interpolation from
// being able to inject YAML structure.
//
// Indentation is baked in rather than inherited, because the token stands alone
// at column 0: four spaces for the `env:` key (job level) and six for entries.
func BuildEnvBlock(names []string) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("    env:")
	for _, n := range names {
		fmt.Fprintf(&b, "\n      %s: ${{ secrets.%s }}", n, n)
	}
	return b.String()
}

// Substitute replaces each registered token present in content with its value
// from vals. A required token that is empty is returned in missing and left
// untouched in the output (deduplicated, in registry order).
//
// Values are validated upstream (config.Load forbids "{"/"}"/newlines/quotes/
// shell metacharacters in [project] values and component paths), so a substituted
// value cannot re-trigger a token or inject structure. That validation is the
// security boundary — keep it.
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

// ProductChoices renders the dispatch dropdown's options list, indented to sit
// under `options:` in the workflow's inputs block.
func ProductChoices(products []config.Product) string {
	var b strings.Builder
	b.WriteString("          - all")
	for _, p := range products {
		fmt.Fprintf(&b, "\n          - %q", p.Name)
	}
	return b.String()
}

// ProductSecrets renders the release-time configuration steps: for each product
// declaring secrets, a step gated on that product's matrix leg which writes the
// real values into its xcconfig.
//
// Release deliberately does not fall back to the placeholder seeding ci.yml
// does. CI seeds placeholders because tests must run without production keys; a
// release doing the same would sign and ship an IPA wired to `appl_xxxxxxxx`,
// and nothing would look wrong until the revenue did not arrive.
func ProductSecrets(products []config.Product) string {
	var b strings.Builder
	for _, p := range products {
		if len(p.Secrets) == 0 {
			continue
		}
		// Sorted: a map's iteration order is random, and an unstable render
		// would rewrite the workflow on every sync and show as permanent drift.
		keys := make([]string, 0, len(p.Secrets))
		for k := range p.Secrets {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "      - name: Write release configuration (%s)\n", p.Name)
		// Gated on the matrix leg. A product's keys must never be written into
		// another product's build: shipping the free app's ad unit IDs inside
		// the paid app is not a build failure, it is a bad release.
		// Single quotes: a GitHub expression takes single-quoted string literals
		// only, and `== "Free"` is a syntax error there, not a failed match. A
		// literal quote inside is escaped by doubling it.
		fmt.Fprintf(&b, "        if: matrix.product.name == '%s'\n", strings.ReplaceAll(p.Name, "'", "''"))
		b.WriteString("        env:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "          %s: ${{ secrets.%s }}\n", k, p.Secrets[k])
		}
		b.WriteString("        run: |\n")
		b.WriteString("          set -euo pipefail\n")
		// The file holds live credentials on a self-hosted runner whose disk
		// outlives the job.
		b.WriteString("          umask 077\n")
		for _, k := range keys {
			// Fail on an unset OR empty secret. An unset secret expands to the
			// empty string, and an empty xcconfig value is not an error to
			// xcodebuild — it would build, sign, upload, and be wrong.
			fmt.Fprintf(&b, "          : \"${%s:?%s is not set — it is required to release %s}\"\n", k, p.Secrets[k], p.Name)
		}
		for _, k := range keys {
			pattern, ok := p.SecretFormats[k]
			if !ok {
				continue
			}
			// Non-empty is not the same as correct. Pasting the paid app's key
			// into the free app, or shipping Google's public test AdMob ID, both
			// produce a perfectly non-empty value that builds, signs, uploads
			// and passes review.
			fmt.Fprintf(&b, "          case \"$%s\" in\n", k)
			fmt.Fprintf(&b, "            %s) ;;\n", pattern)
			fmt.Fprintf(&b, "            *) echo \"::error::%s (from secret %s) does not match %s — releasing %s with it would ship the wrong key\"; exit 1 ;;\n",
				k, p.Secrets[k], pattern, p.Name)
			b.WriteString("          esac\n")
		}
		fmt.Fprintf(&b, "          mkdir -p \"$(dirname %q)\"\n", p.SecretsPath())
		b.WriteString("          {\n")
		for _, k := range keys {
			// printf, not echo: a value containing a backslash would be
			// interpreted by some echo implementations.
			fmt.Fprintf(&b, "            printf '%%s = %%s\\n' %q \"$%s\"\n", k, k)
		}
		fmt.Fprintf(&b, "          } > %q\n", p.SecretsPath())
	}
	return b.String()
}

// ReleaseTags renders the push-tag filter: one pattern per product prefix, or
// the historical 'v*' when nothing declares one.
func ReleaseTags(products []config.Product) string {
	var pats []string
	for _, p := range products {
		if p.TagPrefix != "" {
			pats = append(pats, p.TagPrefix+"*")
		}
	}
	if len(pats) == 0 {
		// The single-product case, and every project that predates products.
		return "      - 'v*'"
	}
	sort.Strings(pats)
	var b strings.Builder
	for i, pat := range pats {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "      - '%s'", pat)
	}
	return b.String()
}

// dependabotUpdates renders one Dependabot entry per ecosystem present in the
// project: github-actions for the repo itself, plus npm for each web component
// and swift for each ios one that has a Swift manifest Dependabot can read.
//
// Swift used to be impossible here. Dependabot's swift ecosystem required a
// top-level Package.swift, and every iOS project in this fleet declares its SPM
// dependencies inside the Xcode project instead. As of 2026-03-31 it discovers
// Package.resolved inside .xcodeproj/.xcworkspace bundles and reads version
// rules from project.pbxproj, which is exactly the layout these projects use:
//
//	Steps.xcodeproj/project.xcworkspace/xcshareddata/swiftpm/Package.resolved
//
// Swift is also the ONLY ecosystem here whose directory cannot be derived from
// the manifest. This function used to emit a swift entry for every ios component
// at that component's own directory, reasoning that a project with no SPM
// dependencies has nothing for Dependabot to find and the entry is therefore
// harmless. That was wrong twice over, and both halves were failing daily in
// production:
//
//   - Dependabot does not no-op, it ABORTS THE JOB — "Error during file fetching;
//     aborting: Repo must contain a Package.swift configuration file or an
//     .xcodeproj/.xcworkspace directory with a Package.resolved file" — taking the
//     repo's github-actions updates down with it. Queueify and rail, twice each.
//   - The manifest is not always at the component root. windsock has one, at
//     WindsockKit/Package.resolved, while its entry pointed at "/".
//
// So the directory is resolved against the repository (see
// detect.IndexSwiftManifests) instead of assumed, and a component with nothing
// readable gets NO swift entry. A missing entry silently stops watching
// dependencies and a wrong one fails a job every day; both are bad, and the
// second one is the one that also takes the other ecosystems with it.
func dependabotUpdates(cfg *config.Config) string {
	// ignore is "" for every component that declares none, and the format string
	// below is otherwise byte-for-byte what it was before ignores existed. That
	// is a requirement, not tidiness: every project in the fleet declares no
	// ignores, so a stray newline here is a diff in every repository at once.
	entry := func(ecosystem, dir, ignore string) string {
		return fmt.Sprintf(`  - package-ecosystem: %s
    directory: %q
    schedule:
      interval: daily
    open-pull-requests-limit: 20
%s    groups:
      # Minor and patch updates arrive as ONE pull request per ecosystem.
      #
      # Nothing is hidden: grouping changes how many PRs carry the updates, not
      # which updates are offered. That is why it is the lever for VOLUME, and
      # why the ignore block above — when a component declares one — is not: an
      # ignore names updates that cannot be merged at all, and carries a reason
      # and an expiry precisely so it can never do this job.
      #
      # It exists because the first daily run opened roughly forty pull requests
      # across the fleet, and this fleet builds iOS on ONE self-hosted Mac. Forty
      # PRs is forty full builds queued behind each other, which starved the CI
      # and releases that actually needed the runner.
      #
      # MAJORS stay separate, deliberately. They are the ones that break: this
      # fleet lost Dead Code Analysis in seven repositories to a
      # download-artifact v7.0.1 that never existed. A major buried in a batch
      # of twenty is a major nobody read.
      routine:
        patterns:
          - "*"
        update-types:
          - minor
          - patch
`, ecosystem, dir, ignore)
	}

	var b strings.Builder
	// Every repo has workflows, and pinned action SHAs are the dependency most
	// likely to rot unnoticed: nothing fails when one goes stale.
	//
	// No ignore block, and there is no way to write one: dependabot_ignore lives
	// on a component, and this entry belongs to no component. That is a real
	// limit rather than an oversight — an action that breaks a repo breaks it in
	// the workflow, where the fix is a version bump somebody makes, not a
	// third-party peer-dependency conflict nobody in the repo can resolve.
	b.WriteString(entry("github-actions", "/", ""))

	// One entry per component, at that component's directory: Dependabot has no
	// glob for `directory`, so a manifest outside a listed path is simply never
	// looked at. kit keeps its project at Kit/Kit.xcodeproj, so "/" would find
	// nothing.
	//
	// Keyed on the component's STACK, falling back to its profiles. Those are
	// different questions — `stack` is what the component IS, `profiles` is what
	// the lacquer manages there — and rendering from profiles alone meant a
	// component with hand-written CI got NO dependency coverage. One project had
	// a Next.js app with 36 npm dependencies watched by nothing for exactly that
	// reason, and the gap was invisible because the config file it should have
	// appeared in looked complete.
	swift := swiftManifests(cfg.Root)
	for _, c := range cfg.Components {
		seen := map[string]bool{}
		// Rendered once per component and shared by every entry that component
		// produces. A component can yield more than one — a swift component with
		// two bundled manifests gets an entry per directory — and the ignore
		// belongs to the component's dependencies, not to one of its paths.
		ignore := dependabotIgnores(c.DependabotIgnore)
		emit := func(stack string) {
			eco, ok := config.StackEcosystem[stack]
			// A stack with no ecosystem is detectable but has no manifest
			// Dependabot supports — supabase's config.toml is configuration,
			// not a lockfile. Emitting an entry there would be a promise the
			// tool cannot keep.
			if !ok || seen[eco] {
				return
			}
			seen[eco] = true
			// swift is the one ecosystem whose directory is a fact about the
			// repository rather than about the manifest, so it is looked up. Zero
			// directories means zero entries.
			if eco == "swift" {
				for _, d := range swift.DependabotDirs(c.Path) {
					b.WriteString(entry(eco, dependabotDir(d), ignore))
				}
				return
			}
			b.WriteString(entry(eco, dependabotDir(c.Path), ignore))
		}
		if c.Stack != "" {
			emit(c.Stack)
		}
		for _, p := range c.Profiles {
			emit(p)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// dependabotIgnores renders a component's declared ignores as Dependabot's
// `ignore` list, or "" when it declares none.
//
// The key names are Dependabot's and are not negotiable: `dependency-name` and
// `versions`, both hyphenated, inside a list under `ignore`. Getting one wrong
// costs nothing visible — Dependabot does not reject an unknown key inside an
// ignore entry, so the file parses, looks configured, and withholds nothing,
// which is this repository's signature failure mode. TestDependabotIgnoreUses
// DependabotsSchema in internal/shipped asserts the rendered keys rather than
// trusting this comment.
//
// `update-types` is absent by construction, not by omission: config has no field
// for it. It is the key that turns an ignore into volume control, and volume is
// what `groups` below is for.
//
// The reason and the expiry are rendered as COMMENTS. They are not Dependabot
// syntax and it never sees them; they are for the person reading
// .github/dependabot.yml and asking why an update stopped arriving. Sending them
// to go read .lacquer.toml instead is how the reasons ended up in TOML comments
// nothing could report in the first place.
func dependabotIgnores(ignores []config.DependabotIgnore) string {
	if len(ignores) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("    # Updates this component cannot take, declared in .lacquer.toml.\n")
	b.WriteString("    #\n")
	b.WriteString("    # Each one is a known incompatibility with a name, a reason and a review\n")
	b.WriteString("    # date — not a volume control. `lacquer audit` fails once a date passes,\n")
	b.WriteString("    # so an ignore that outlives its reason comes back rather than persisting.\n")
	b.WriteString("    ignore:\n")
	for _, ig := range ignores {
		// Single-line: a reason with an embedded newline would break out of the
		// comment and into the YAML. TOML permits one via a multi-line string.
		fmt.Fprintf(&b, "      # %s\n", strings.Join(strings.Fields(ig.Reason), " "))
		fmt.Fprintf(&b, "      # Review by %s.\n", ig.Until)
		fmt.Fprintf(&b, "      - dependency-name: %q\n", ig.Dependency)
		b.WriteString("        versions:\n")
		for _, v := range ig.Versions {
			// Quoted, always. "7.x" survives unquoted but ">=2.0" does not — a
			// leading `>` opens a YAML folded scalar — and the difference between
			// those two is exactly the kind of thing that renders a file which
			// parses into something other than what was written.
			fmt.Fprintf(&b, "          - %q\n", v)
		}
	}
	return b.String()
}

// dependabotDir spells a component path as a Dependabot `directory` value: "."
// and "" are the repo root, "/".
func dependabotDir(compPath string) string {
	if compPath == "" || compPath == "." {
		return "/"
	}
	return "/" + strings.TrimSuffix(compPath, "/")
}

// swiftManifests resolves root's Swift manifests, memoised per root for the life
// of the process.
//
// Memoised because Values is called once per managed unit — `lacquer audit`
// re-renders every region and every asset, so a single run asks this question
// around a hundred times, and each answer costs a `git ls-files`. Caching it is
// safe for the reason the cache is keyed the way it is: the answer is a property
// of one repository's index, and nothing the lacquer does writes a Package.swift
// or a Package.resolved — sync writes CLAUDE.md, workflows and configs. A run
// that could invalidate this does not exist.
//
// An unknown root (a Config built in memory rather than loaded) and a root that
// is not a git repository both yield the empty index, which renders no swift
// entry. Nothing is silently mis-pointed; something is silently absent, which is
// the direction to fail in, and sync refuses to write assets outside a git
// repository anyway.
func swiftManifests(root string) detect.SwiftManifestIndex {
	if root == "" {
		return detect.SwiftManifestIndex{}
	}
	swiftMu.Lock()
	defer swiftMu.Unlock()
	if ix, ok := swiftCache[root]; ok {
		return ix
	}
	// A git failure is indistinguishable here from "no manifests", and Values has
	// no error path to report it on. Fail closed: no entry.
	ix, _ := detect.IndexSwiftManifests(root)
	swiftCache[root] = ix
	return ix
}

var (
	swiftMu    sync.Mutex
	swiftCache = map[string]detect.SwiftManifestIndex{}
)
