package tokens

import (
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

// bundleSecretsScript is the root asset every rendered step calls. It is a
// root asset, so it sits at scripts/ whatever the component layout.
//
// verify-, not check-: the consumer whose repo-owned script this generalises
// keeps it at scripts/check-bundle-secrets.sh, with a different argument list,
// called from a workflow it has excluded. A shipped file at that path makes its
// sync refuse, and taking the lacquer's copy would break that workflow. Under
// its own name the two coexist until the consumer adopts the rendered CI and
// deletes its copy.
const bundleSecretsScript = "scripts/verify-bundle-secrets.sh"

// Where a bundle-secrets step runs, and so which built products it reads.
type bundleSecretsSite int

const (
	// The Build (Release) job: the shipped product, the app and everything it
	// embeds, built for a device.
	bundleSecretsBuild bundleSecretsSite = iota
	// The Test job: the same targets plus the test bundles, which are built
	// only there. The job deletes DerivedData/Build before it builds, so every
	// product under it is from this commit.
	bundleSecretsTest
	// The release, after the archive: what is about to be signed and uploaded.
	bundleSecretsRelease
)

// CIBundleSecretsBuild, CIBundleSecretsTest and ReleaseBundleSecrets render the
// step that fails when a bundle other than the app carries a key from the
// product's secrets template, or nothing.
//
// The leak this catches is silent everywhere else. The keys reach the app's
// Info.plist through a base xcconfig whose INFOPLIST_FILE line every target
// inherits when the config is applied at project level, so a watch app, an
// extension or a test bundle that does not override it ships a second copy of
// every key. It builds, signs, uploads and passes review.
//
// A step renders for each product that declares secrets, because only then is
// there a resolved template (config.Product.SecretsTemplate, the same lookup
// the release writer uses) to derive the key list from. A project declaring
// none renders nothing, which keeps its workflows byte-identical.
//
// A product that declares no secrets but shares a sibling's base configuration
// gets no step: nothing names which template its keys come from.
func CIBundleSecretsBuild(products []config.Product, prefix string) string {
	return bundleSecretsSteps(products, prefix, bundleSecretsBuild)
}

// CIBundleSecretsTest is CIBundleSecretsBuild for the Test job.
func CIBundleSecretsTest(products []config.Product, prefix string) string {
	return bundleSecretsSteps(products, prefix, bundleSecretsTest)
}

// ReleaseBundleSecrets is CIBundleSecretsBuild for the release, after the
// archive.
func ReleaseBundleSecrets(products []config.Product, prefix string) string {
	return bundleSecretsSteps(products, prefix, bundleSecretsRelease)
}

func bundleSecretsSteps(products []config.Product, prefix string, site bundleSecretsSite) string {
	var b strings.Builder
	for _, p := range products {
		if len(p.Secrets) == 0 {
			continue
		}
		app := p.AppTargetName()
		var name, dir, what string
		switch site {
		case bundleSecretsBuild:
			name = "Check non-app bundles for secrets keys"
			dir = prefix + "DerivedData/Build/Products/Release-iphoneos"
			what = "the shipped product: every bundle the app embeds"
		case bundleSecretsTest:
			name = "Check test and embedded bundles for secrets keys"
			dir = prefix + "DerivedData/Build/Products"
			what = "everything this job built, test bundles included"
		case bundleSecretsRelease:
			name = "Check archived bundles for secrets keys"
			dir = "$ARCHIVE_DIR/$PRODUCT_NAME.xcarchive/Products/Applications"
			what = "the archive about to be signed and uploaded"
		}
		// The release is always a matrix, so its steps always say which leg they
		// belong to. CI has a matrix only for more than one product.
		gated := site == bundleSecretsRelease || multi(products)
		b.WriteString("\n\n")
		if gated {
			fmt.Fprintf(&b, "      - name: %s (%s)\n", name, p.Name)
		} else {
			fmt.Fprintf(&b, "      - name: %s\n", name)
		}
		fmt.Fprintf(&b, "        # Only %s may carry the keys %s defines. A target\n", app, prefix+p.SecretsTemplate())
		b.WriteString("        # that inherits the app's Info.plist wiring ships a second copy of every\n")
		fmt.Fprintf(&b, "        # key, and nothing else fails. Scans %s.\n", what)
		b.WriteString("        # GENERATED from [[product]].secrets; a product declaring none renders no step.\n")
		if gated {
			// Single quotes: a GitHub expression takes only single-quoted string
			// literals, and a literal quote inside is escaped by doubling it.
			fmt.Fprintf(&b, "        if: matrix.product.name == '%s'\n", strings.ReplaceAll(p.Name, "'", "''"))
		}
		// The directory is double-quoted rather than %q'd so the release's
		// $ARCHIVE_DIR and $PRODUCT_NAME expand. Every other value is
		// charset-validated at config.Load and cannot carry a quote or a $.
		fmt.Fprintf(&b, "        run: %s %q %q \"%s\"", bundleSecretsScript, prefix+p.SecretsTemplate(), app, dir)
	}
	return b.String()
}
