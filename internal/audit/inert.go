package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

// InertSecrets is a product whose declared `secrets` nothing will ever write.
//
// `[[product]].secrets` is consumed in exactly one place: the release workflow's
// rendered "Write release configuration" step. If that workflow is excluded, or
// simply is not in the project, the declaration is inert — it reads as
// configured, it survives review, and it produces nothing.
//
// a-bible-verse-each-day is the live case. It declares three keys with
// `secret_formats` shape checks:
//
//	secrets = { REVENUECAT_PUBLIC_SDK_KEY = ..., ADMOB_APPLICATION_ID = ..., ... }
//	secret_formats = { REVENUECAT_PUBLIC_SDK_KEY = "appl_*", ... }
//
// and excludes `.github/workflows/ios-release.yml`. Its releases archive with
// every one of those keys undefined, and nothing anywhere says so. The
// declaration is the strongest available evidence that somebody INTENDED the
// keys to be written, which is what makes the silence worth breaking.
//
// Deliberately not the same finding as a shadow release workflow
// (internal/shadow): that one fires when a managed release exists ALONGSIDE
// another, this one when the managed release is gone and a declaration is left
// pointing at nothing.
type InertSecrets struct {
	// Product is the [[product]] name, or the project name for a single-product
	// repository.
	Product string
	// Keys are the declared secret names, sorted.
	Keys []string
	// Workflow is the release workflow that would have consumed them.
	Workflow string
	// Excluded is true when .lacquer.toml excludes that workflow, false when it
	// is simply absent. The distinction is the whole remedy: an exclusion is a
	// decision to revisit, an absence is a sync away.
	Excluded bool
}

// releaseWorkflowFor names the workflow whose rendered step consumes
// [[product]].secrets. Only the iOS profile has one today.
const releaseWorkflowFor = ".github/workflows/ios-release.yml"

// InertSecretDeclarations returns every product declaring secrets that nothing
// in this project will write.
func InertSecretDeclarations(projectRoot string, cfg *config.Config) []InertSecrets {
	if cfg == nil {
		return nil
	}
	products := cfg.Products()
	if len(products) == 0 {
		return nil
	}

	// Present AND carrying the step. A project can hold a stale, project-owned
	// copy of the file under the managed name; what matters is whether anything
	// in it writes the config, not whether the path exists.
	consumed := false
	if b, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(releaseWorkflowFor))); err == nil {
		consumed = strings.Contains(string(b), "Write release configuration")
	}
	if consumed {
		return nil
	}

	excluded := false
	for _, e := range cfg.Project.Exclude {
		if strings.Contains(e.Path, "ios-release.yml") {
			excluded = true
			break
		}
	}

	var out []InertSecrets
	for _, p := range products {
		if len(p.Secrets) == 0 {
			continue
		}
		keys := make([]string, 0, len(p.Secrets))
		for k := range p.Secrets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, InertSecrets{
			Product:  p.Name,
			Keys:     keys,
			Workflow: releaseWorkflowFor,
			Excluded: excluded,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Product < out[j].Product })
	return out
}

// FormatInertSecrets renders the report, or "" when there is nothing to say.
func FormatInertSecrets(fs []InertSecrets) string {
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\ndeclared secrets that nothing will write:\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "  [[product]] %s declares %d secret(s): %s\n", f.Product, len(f.Keys), strings.Join(f.Keys, ", "))
		if f.Excluded {
			fmt.Fprintf(&b, "    %s is EXCLUDED in .lacquer.toml, so the step that writes them is never rendered.\n", f.Workflow)
		} else {
			fmt.Fprintf(&b, "    %s has no \"Write release configuration\" step, so nothing writes them.\n", f.Workflow)
		}
	}
	b.WriteString("A release built here archives with every one of those keys UNDEFINED. The declaration\n" +
		"reads as configured and produces nothing — which is worse than declaring none, because it\n" +
		"is the evidence somebody intended them to be written.\n" +
		"Either re-adopt the managed release workflow, or write the keys from whatever workflow you\n" +
		"release from — scripts/write-release-config.sh is already synced here and fails closed on\n" +
		"an unset or wrong-shaped value. If the keys genuinely are not needed, remove the\n" +
		"declaration so it stops claiming otherwise.\n")
	return b.String()
}
