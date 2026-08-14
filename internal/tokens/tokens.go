// Package tokens substitutes the fixed set of per-project placeholders into
// synced content. Values come from the manifest's [project] block plus a derived
// component prefix. Only these exact {{KEY}} literals are touched; GitHub Actions
// ${{ ... }} is never matched.
package tokens

import (
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
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
	{WebBuildEnv, false}, // empty is valid and common: most projects need no build secrets
	{IOSProductCatalog, false},
}

// Prefix converts a component path to a path prefix: "." -> "", "ios" -> "ios/".
func Prefix(path string) string {
	if path == "." || path == "" {
		return ""
	}
	return path + "/"
}

// Values builds the substitution map from the [project] values plus the derived
// component prefix for the content being substituted.
//
// products is the project's shippable apps. Pass cfg.Products(), which is never
// empty — it synthesises a single entry from [project] when none is declared, so
// there is no "has products" branch anywhere downstream.
func Values(p config.Project, prefix string, products []config.Product) map[string]string {
	return map[string]string{
		ProjectName:       p.ProjectName,
		Scheme:            p.Scheme,
		BundleID:          p.BundleID,
		AscAppID:          p.AscAppID,
		Xcodeproj:         p.Xcodeproj,
		SwiftVersion:      p.SwiftVersion,
		GithubOrg:         p.GithubOrg,
		ComponentPrefix:   prefix,
		WebBuildEnv:       BuildEnvBlock(p.BuildEnv),
		IOSProductCatalog: ProductCatalog(products),
	}
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
