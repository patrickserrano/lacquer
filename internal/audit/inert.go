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
	// SecretsFile is the path the product declared, which nothing writes.
	SecretsFile string
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

	// Read EVERY workflow, not just the managed release. The first version of
	// this asked whether ios-release.yml contained the literal string
	// "Write release configuration" — the managed step's name — and that was
	// wrong in the direction that matters.
	//
	// a-bible-verse-each-day declares secrets_file = "Config/Monetization.xcconfig"
	// and excludes ios-release.yml, so the managed step is genuinely absent. But
	// its project-owned release carries a hand-rolled step, "Create protected
	// runtime configuration", that reads all three secrets, FAILS CLOSED on any
	// unset (`: "${VAR:?Missing VAR}"`), and writes exactly that file. The
	// project is not exposed at all. Keying on the managed step's NAME reported
	// a correct setup as broken — a false positive on the one project the check
	// was built for.
	//
	// The question is not "does the managed step exist" but "does anything write
	// the file the product declared". That is what the release actually depends
	// on, and it is agnostic about who writes it.
	workflows := map[string]string{}
	wfDir := filepath.Join(projectRoot, ".github", "workflows")
	if entries, err := os.ReadDir(wfDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(wfDir, e.Name())); err == nil {
				workflows[e.Name()] = string(b)
			}
		}
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
		// Anything that names the declared file is a writer as far as this check
		// is concerned. Over-accepting on purpose: telling a project its
		// working setup is broken is far more expensive than staying quiet about
		// a file merely mentioned somewhere, because the first teaches people
		// the finding is noise.
		written := false
		for _, body := range workflows {
			if strings.Contains(body, p.SecretsPath()) {
				written = true
				break
			}
		}
		if written {
			continue
		}
		keys := make([]string, 0, len(p.Secrets))
		for k := range p.Secrets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out = append(out, InertSecrets{
			Product:     p.Name,
			Keys:        keys,
			Workflow:    releaseWorkflowFor,
			SecretsFile: p.SecretsPath(),
			Excluded:    excluded,
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
		fmt.Fprintf(&b, "    Nothing in .github/workflows writes %s.\n", f.SecretsFile)
		if f.Excluded {
			fmt.Fprintf(&b, "    %s is EXCLUDED in .lacquer.toml, so the managed step that would write it is never rendered.\n", f.Workflow)
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
