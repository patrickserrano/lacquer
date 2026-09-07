package assets

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/patrickserrano/lacquer/internal/config"
)

// secretRef matches a GitHub Actions secret reference. `secrets` is the only
// context that can name a credential, and the name is on the identifier charset
// GitHub enforces for secret names.
var secretRef = regexp.MustCompile(`\$\{\{\s*secrets\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// GITHUB_TOKEN is minted by Actions for every run. A workflow that stops
// mentioning it has lost nothing a person has to go and provision, so it is not
// a drop worth refusing a sync over.
const autoProvisionedSecret = "GITHUB_TOKEN"

func secretNames(content []byte) map[string]bool {
	out := map[string]bool{}
	for _, m := range secretRef.FindAllSubmatch(content, -1) {
		name := string(m[1])
		if name == autoProvisionedSecret {
			continue
		}
		out[name] = true
	}
	return out
}

// isWorkflow reports whether dest is a GitHub Actions workflow. Nothing else in
// a synced tree can read a secret, so nothing else is worth reading twice.
func isWorkflow(dest string) bool {
	dest = filepath.ToSlash(dest)
	if path.Dir(dest) != ".github/workflows" {
		return false
	}
	ext := path.Ext(dest)
	return ext == ".yml" || ext == ".yaml"
}

// checkSecretDrop refuses a sync that would remove a secret the project's
// current workflow reads and the incoming one does not.
//
// This guard exists because of a shipped App Store rejection, and because the
// guard that should have caught it structurally could not.
//
// Rail's release workflow carried a hand-written "Setup Secrets" step that wrote
// six app-runtime keys into an xcconfig. Lacquer's onboarding replaced that file
// with the shared profile's release workflow, which had no such step. Nothing
// failed: the archive built, signed, uploaded, and reached App Review with every
// key unset. RevenueCatUI's paywall calls `fatalError("Purchases has not been
// configured.")` in any non-DEBUG build, and 1.1.0 (200) was rejected under
// Guideline 2.1(a). CI was green from the first commit to the rejection.
//
// The audit's clobber guard (see internal/audit) is the obvious place for this
// and cannot do it: it compares the file against the LOCK baseline, and at
// onboarding there is no baseline — the file predates the lacquer entirely. So
// the one sync where the whole of a project's local knowledge is discarded is
// exactly the sync that guard cannot see.
//
// The check is a NAME-set comparison rather than a content diff on purpose. It
// says nothing about how a workflow uses a secret, only that a credential
// somebody had to go and provision is about to stop being read. That is
// mechanical, has no false-negative story, and the two ways out of it are both
// deliberate acts a reviewer can see: declare the keys in `[[product]].secrets`
// so the shared workflow writes them, or exclude the path with a reason and an
// expiry.
//
// It is NOT bypassed by --force. --force means "take the lacquer's version of a
// file I have edited", which is a judgement about content; this is a judgement
// about credentials disappearing from a release, and the answer to it is never
// "run it again with a flag".
func checkSecretDrop(plan []Asset, cfg *config.Config, targets []string) error {
	type finding struct {
		dest    string
		dropped []string
	}
	var findings []finding

	// Names the project has explicitly retired, each with a required reason.
	// Subtracted rather than reported: the guard exists to stop a credential
	// disappearing UNNOTICED, and an entry here is the opposite of unnoticed.
	retired := map[string]bool{}
	for _, r := range cfg.Project.RetiredSecrets {
		retired[r.Name] = true
	}

	for i, a := range plan {
		if !isWorkflow(a.Dest) {
			continue
		}
		existing, err := os.ReadFile(targets[i])
		if err != nil {
			// A workflow that does not exist yet drops nothing. Any other read
			// error is left to the write phase to report against the same path.
			continue
		}
		incoming, _, err := Render(a, cfg)
		if err != nil {
			return err
		}
		have, want := secretNames(existing), secretNames(incoming)
		var dropped []string
		for name := range have {
			if !want[name] && !retired[name] {
				dropped = append(dropped, name)
			}
		}
		if len(dropped) > 0 {
			sort.Strings(dropped)
			findings = append(findings, finding{dest: a.Dest, dropped: dropped})
		}
	}
	if len(findings) == 0 {
		return nil
	}

	sort.Slice(findings, func(i, j int) bool { return findings[i].dest < findings[j].dest })
	var b strings.Builder
	b.WriteString("refusing to sync: this would stop these workflows reading secrets they read today.\n")
	b.WriteString("A release built without its runtime keys does not fail — it signs, uploads and reaches\n")
	b.WriteString("App Review as a non-functional app. Rail's 1.1.0 was rejected under Guideline 2.1(a)\n")
	b.WriteString("exactly this way, from exactly this sync, with every check green.\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "\n  %s\n    %s\n", f.dest, strings.Join(f.dropped, ", "))
	}
	b.WriteString("\nEither declare them in .lacquer.toml so the shared workflow writes them:\n")
	b.WriteString("\n  [[product]]\n  secrets = { KEY = \"GITHUB_SECRET_NAME\" }\n")
	b.WriteString("\nor, if the credential is genuinely obsolete, retire it by name:\n")
	b.WriteString("\n  [project]\n  retired_secrets = [{ name = \"…\", reason = \"…\" }]\n")
	b.WriteString("\nor keep the local file, with a reason and an expiry:\n")
	b.WriteString("\n  exclude = [{ path = \"…\", reason = \"…\", until = \"YYYY-MM-DD\" }]\n")
	b.WriteString("\n--force does not lift this. It means \"take the lacquer's version of a file I edited\",\nwhich is a judgement about content; this one is about credentials leaving a release.")
	return fmt.Errorf("%s", b.String())
}
