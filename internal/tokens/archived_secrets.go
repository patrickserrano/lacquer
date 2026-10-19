package tokens

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/patrickserrano/lacquer/internal/config"
)

// archivedSecretsScript is the root asset every rendered step calls.
const archivedSecretsScript = "scripts/verify-archived-secrets.sh"

// ArchivedSecretsGateFrom is the first day (UTC) a declared secret whose
// Info.plist key cannot be found fails the release. Before it, the release
// prints the same finding as a named ::warning:: and goes on.
//
// The grace exists because the mapping is new: a project whose secret reaches
// the plist through a build setting of another name has nothing to declare
// until this check asks, and a routine sync must not turn its next release red
// before anyone has read the warning. A secret whose key IS found gets no grace:
// its archived value is checked from the first release.
//
// The one place the date lives; it is rendered into every step. A var only so
// tests can render either side of it.
var ArchivedSecretsGateFrom = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

// ReleaseArchivedSecrets renders, for each product declaring secrets, the step
// that reads the ARCHIVED app's Info.plist and fails when a declared key did
// not reach it with a real value; for a project declaring none, nothing, so its
// release workflow is byte-identical.
//
// write-release-config.sh checks a secret's value before the build. Nothing
// checked that the value reached the product: a plist template missing its
// entry, a custom INFOPLIST_KEY_* setting a generated plist drops, a setting
// pointing at a variable nothing defines. Each archives an app whose runtime key
// is empty or a placeholder, behind a green run.
//
// One static step per product, gated on its matrix leg, so each product checks
// its own keys in its own archive and the names stay greppable in the workflow.
// A product declaring no secrets gets no step: it declares no key to check.
func ReleaseArchivedSecrets(products []config.Product, prefix string) string {
	var b strings.Builder
	sources := strings.TrimSuffix(prefix, "/")
	if sources == "" {
		sources = "."
	}
	for _, p := range products {
		if len(p.Secrets) == 0 {
			continue
		}
		keys := make([]string, 0, len(p.Secrets))
		for k := range p.Secrets {
			keys = append(keys, k)
		}
		// Sorted, so the render is stable and a sync shows no drift.
		sort.Strings(keys)
		var specs strings.Builder
		for _, k := range keys {
			spec := k
			if pk, ok := p.SecretPlistKeys[k]; ok {
				spec = k + "=" + pk
			}
			fmt.Fprintf(&specs, " %q", spec)
		}
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "      - name: Verify build-time keys reached the archived Info.plist (%s)\n", p.Name)
		b.WriteString("        # Each declared key must be in the ARCHIVED app's Info.plist, non-empty and\n")
		b.WriteString("        # not its template placeholder, before anything is exported. The plist key\n")
		b.WriteString("        # is secret_plist_keys.<NAME>, or the source plist entry set to exactly\n")
		b.WriteString("        # $(NAME). Only key names are printed, never a value.\n")
		b.WriteString("        # GENERATED from [[product]].secrets; a product declaring none renders no step.\n")
		// Single quotes: a GitHub expression takes only single-quoted string
		// literals, and a literal quote inside is escaped by doubling it.
		fmt.Fprintf(&b, "        if: matrix.product.name == '%s'\n", strings.ReplaceAll(p.Name, "'", "''"))
		// The archive path is double-quoted rather than %q'd so $ARCHIVE_DIR and
		// $PRODUCT_NAME expand. Every other value is charset-validated at
		// config.Load and cannot carry a quote or a $.
		fmt.Fprintf(&b, "        run: %s --example %q --sources %q --unresolved-fails-from %s \"$ARCHIVE_DIR/$PRODUCT_NAME.xcarchive\" %q%s",
			archivedSecretsScript, prefix+p.SecretsTemplate(), sources, ArchivedSecretsGateFrom.Format("2006-01-02"), p.AppTargetName(), specs.String())
	}
	return b.String()
}
