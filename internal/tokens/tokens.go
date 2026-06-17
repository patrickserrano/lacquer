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
// in missing (and left untouched in the output), deduplicated in registry order.
//
// A substituted value could in principle re-trigger a later registered token,
// but config.Load's [project] validators forbid "{"/"}" (and newlines, quotes,
// shell metacharacters) in values, so no loaded value can carry a token literal
// or inject structure. That validation is the security boundary here — keep it.
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
