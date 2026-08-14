// Package tokens substitutes the fixed set of per-project placeholders into
// synced content. Values come from the manifest's [project] block plus a derived
// component prefix. Only these exact {{KEY}} literals are touched; GitHub Actions
// ${{ ... }} is never matched.
package tokens

import (
	"fmt"
	"sort"
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
	{IOSProductChoices, false},
	{IOSProductSecrets, false},
	{IOSReleaseTags, false},
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
		IOSProductChoices: ProductChoices(products),
		IOSProductSecrets: ProductSecrets(products),
		IOSReleaseTags:    ReleaseTags(products),
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
