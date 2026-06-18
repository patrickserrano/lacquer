// Package tokens substitutes the fixed set of per-project placeholders into
// synced content. Values come from the manifest's [project] block plus a derived
// component prefix. Only these exact {{KEY}} literals are touched; GitHub Actions
// ${{ ... }} is never matched.
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
	Xcodeproj       = "{{XCODEPROJ}}"
	SwiftVersion    = "{{SWIFT_VERSION}}"
	ComponentPrefix = "{{COMPONENT_PREFIX}}"
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
	{ComponentPrefix, false},
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
func Values(p config.Project, prefix string) map[string]string {
	return map[string]string{
		ProjectName:     p.ProjectName,
		Scheme:          p.Scheme,
		BundleID:        p.BundleID,
		AscAppID:        p.AscAppID,
		Xcodeproj:       p.Xcodeproj,
		SwiftVersion:    p.SwiftVersion,
		ComponentPrefix: prefix,
	}
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
