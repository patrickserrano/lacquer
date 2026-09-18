package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/tokens"
)

// These fixtures are shaped like real fleet repositories, and the managed
// workflows in them are the profile's own templates, rendered — not
// transcriptions. The defect they pin was invisible precisely because the
// managed ios-ci.yml's placeholder seed was never in any fixture: every
// earlier test gave the audit a release workflow and nothing else, so nothing
// ever asked what CI's
//
//	cp "Secrets.xcconfig.example" "$scheme_dir/Secrets.xcconfig"
//
// does to a substring match on "Secrets.xcconfig". It satisfies it, on every
// iOS repository in the fleet, which made the audit blind on exactly the repos
// it exists for.

// managedCI is profiles/ios/workflows/ci.yml as a repository with the given
// component prefix receives it. Read from the template so that a change to the
// seed step is re-tested here rather than frozen in a copy.
func managedCI(t *testing.T, prefix string) string {
	t.Helper()
	b, err := os.ReadFile("../../profiles/ios/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(b), "{{COMPONENT_PREFIX}}", prefix)
	// Guard the fixture itself: if the seed step moved or was renamed, these
	// tests would go on passing while exercising nothing.
	if !strings.Contains(body, `cp "`+prefix+`Secrets.xcconfig.example" "$scheme_dir/Secrets.xcconfig"`) {
		t.Fatal("fixture: the managed CI no longer carries the placeholder seed these tests are about")
	}
	return body
}

// managedRelease is profiles/ios/workflows/release.yml with its secrets step
// rendered by the real renderer. With no product declaring secrets that renders
// nothing — the state of kit, port-of-entry and multimeter, whose releases
// write no Secrets.xcconfig at all.
func managedRelease(t *testing.T, secretsStep string) string {
	t.Helper()
	b, err := os.ReadFile("../../profiles/ios/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), tokens.IOSProductSecrets) {
		t.Fatalf("fixture: release.yml no longer carries %s", tokens.IOSProductSecrets)
	}
	return strings.ReplaceAll(string(b), tokens.IOSProductSecrets, secretsStep)
}

func writeWorkflows(t *testing.T, dir string, workflows map[string]string) {
	t.Helper()
	wf := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range workflows {
		if err := os.WriteFile(filepath.Join(wf, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Declared secrets, the managed CI seeding placeholders, and a release that
// writes nothing: the audit must fire. Before this fix it was silent on every
// one of these, because CI's seed counted as the release writing the file.
func TestCIPlaceholderSeedIsNotTheReleaseWritingSecrets(t *testing.T) {
	cases := []struct {
		name     string
		prefix   string
		manifest string
	}{
		{
			// flare's shape before its release wrote the key: a REPLACE_ME
			// RevenueCat placeholder shipped while this audit said nothing.
			name:   "flare",
			prefix: "Flare/",
			manifest: `
[project]
name = "Flare"
project_name = "Flare"
scheme = "Flare"

[[product]]
name = "Flare"
scheme = "Flare"
bundle_id = "com.example.Flare"
asc_app_id = "1234567890"
secrets = { REVENUECAT_PUBLIC_SDK_KEY = "REVENUECAT_PUBLIC_SDK_KEY" }
secret_formats = { REVENUECAT_PUBLIC_SDK_KEY = "appl_*" }
`,
		},
		{
			name:   "kit",
			prefix: "",
			manifest: `
[project]
name = "kit"
project_name = "Kit"
scheme = "Kit"
secrets = { REVENUECAT_API_KEY = "KIT_REVENUECAT_API_KEY" }
`,
		},
		{
			name:   "port-of-entry",
			prefix: "",
			manifest: `
[project]
name = "dick-passport"
project_name = "PortOfEntry"
scheme = "PortOfEntry"

[[product]]
name = "PortOfEntry"
scheme = "PortOfEntry"
bundle_id = "com.example.PortOfEntry"
asc_app_id = "1234567890"
secrets = { REVENUECAT_API_KEY = "PORT_OF_ENTRY_REVENUECAT_API_KEY" }
`,
		},
		{
			name:   "multimeter",
			prefix: "ios/",
			manifest: `
[project]
name = "multimeter"
project_name = "Multimeter"
scheme = "Multimeter"
secrets = { SENTRY_DSN = "MULTIMETER_SENTRY_DSN" }
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeWorkflows(t, dir, map[string]string{
				"ios-ci.yml":      managedCI(t, tc.prefix),
				"ios-release.yml": managedRelease(t, ""),
			})
			fs := InertSecretDeclarations(dir, loadManifest(t, dir, tc.manifest))
			if len(fs) != 1 {
				t.Fatalf("declared secrets that only CI's placeholder seed touches were treated as written "+
					"by the release: %+v", fs)
			}
		})
	}
}

// The other direction, and the one that matters more: every real writer in the
// fleet must still count, with the managed CI seed sitting beside it. Each of
// these is transcribed from the repository named, because the shapes are the
// point — a redirect after a multi-line sed, an awk-to-tmp-then-mv, a seed
// followed by a real write, the managed writer called from a project-owned
// workflow.
func TestEveryFleetReleaseWriterStillCounts(t *testing.T) {
	cases := []struct {
		name      string
		manifest  string
		workflows func(t *testing.T) map[string]string
	}{
		{
			// Two products, one declaring secrets: the managed release renders a
			// seed-only call for the sibling and a keyed call for the declarer.
			name: "steps",
			manifest: `
[project]
name = "Steps"
project_name = "Steps"
scheme = "Steps"

[[product]]
name = "Steps"
scheme = "Steps"
tag_prefix = "steps-v"
bundle_id = "com.example.Steps"
asc_app_id = "1234567890"

[[product]]
name = "Steps Free"
scheme = "StepsFree"
tag_prefix = "steps-free-v"
bundle_id = "com.example.StepsFree"
asc_app_id = "1234567890"
secrets = { REVENUECAT_API_KEY = "STEPS_FREE_REVENUECAT_PUBLIC_API_KEY" }
secret_formats = { REVENUECAT_API_KEY = "appl_*" }
`,
			workflows: func(t *testing.T) map[string]string {
				return map[string]string{"ios-ci.yml": managedCI(t, "")}
			},
		},
		{
			// Managed release EXCLUDED; the project's own release writes the
			// declared file with a redirect for one leg and seeds it for the
			// other. Its UI-test workflow also seeds it from the example.
			name: "a-bible-verse-each-day",
			manifest: `
[project]
name = "a-bible-verse-each-day"
project_name = "ABibleVerseEachDay"
scheme = "ABibleVerseEachDay"
exclude = [{ path = ".github/workflows/ios-release.yml", reason = "project-owned release" }]

[[product]]
name = "A Bible Verse Each Day"
scheme = "ABibleVerseEachDay"
bundle_id = "com.example.ABibleVerseEachDay"
asc_app_id = "1234567890"
secrets_file = "Config/Monetization.xcconfig"
secrets = { REVENUECAT_PUBLIC_SDK_KEY = "REVENUECAT_PUBLIC_SDK_KEY", ADMOB_APPLICATION_ID = "ADMOB_APPLICATION_ID" }
`,
			workflows: func(t *testing.T) map[string]string {
				return map[string]string{
					"ios-ci.yml": managedCI(t, ""),
					"ios-release.yml": `jobs:
  release:
    steps:
      - name: Create protected runtime configuration
        env:
          REVENUECAT_PUBLIC_SDK_KEY: ${{ secrets.REVENUECAT_PUBLIC_SDK_KEY }}
          ADMOB_APPLICATION_ID: ${{ secrets.ADMOB_APPLICATION_ID }}
        run: |
          set -euo pipefail
          umask 077
          if [ "${{ matrix.slug }}" = "free" ]; then
            : "${REVENUECAT_PUBLIC_SDK_KEY:?Missing REVENUECAT_PUBLIC_SDK_KEY}"
            : "${ADMOB_APPLICATION_ID:?Missing ADMOB_APPLICATION_ID}"
            {
              printf 'REVENUECAT_PUBLIC_SDK_KEY = %s\n' "$REVENUECAT_PUBLIC_SDK_KEY"
              printf 'ADMOB_APPLICATION_ID = %s\n' "$ADMOB_APPLICATION_ID"
            } > "Config/Monetization.xcconfig"
          else
            cp "Config/Monetization.xcconfig.example" "Config/Monetization.xcconfig"
          fi
`,
					"ios-ui-tests.yml": `jobs:
  ui:
    steps:
      - run: |
          cp "Config/Monetization.xcconfig.example" "Config/Monetization.xcconfig"
`,
				}
			},
		},
		{
			// rail's own ios-release.yml: a multi-line sed over the example,
			// redirected into place, then read back by grep.
			name: "rail",
			manifest: `
[project]
name = "rail"
project_name = "Rail"
scheme = "Rail"

[[product]]
name = "Rail"
scheme = "Rail"
bundle_id = "com.example.Rail"
asc_app_id = "1234567890"
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY", SENTRY_DSN = "SENTRY_DSN" }
`,
			workflows: func(t *testing.T) map[string]string {
				return map[string]string{
					"ios-ci.yml": managedCI(t, ""),
					"ios-release.yml": `jobs:
  release:
    steps:
      - name: Inject secrets
        run: |
          set -euo pipefail
          SENTRY_DSN_ESC=$(printf '%s' "${SENTRY_DSN}" | sed 's|://|:/$()/|')
          sed \
            -e "s|your_revenuecat_api_key_here|${REVENUECAT_API_KEY}|g" \
            -e "s|https:/\$()/your_sentry_dsn_here|${SENTRY_DSN_ESC}|g" \
            xcconfig/Secrets.xcconfig.example > xcconfig/Secrets.xcconfig
          if grep -E '= https?:(//|$)' xcconfig/Secrets.xcconfig; then
            echo "::error::Unescaped URL in Secrets.xcconfig — value would truncate"
            exit 1
          fi
`,
				}
			},
		},
		{
			// momfriend's own ios-release.yml: seed from the example, rewrite
			// through awk into a .tmp, then mv the .tmp into place. The seed alone
			// is not a write; the mv is.
			name: "momfriend",
			manifest: `
[project]
name = "momfriend"
project_name = "MomFriend"
scheme = "MomFriend"
secrets = { PROXY_SECRET = "PROXY_SECRET", SENTRY_DSN = "SENTRY_DSN" }
`,
			workflows: func(t *testing.T) map[string]string {
				return map[string]string{
					"ios-ci.yml": managedCI(t, "ios/"),
					"ios-release.yml": `jobs:
  release:
    steps:
      - name: Create Secrets.xcconfig
        run: |
          if [ -f "ios/Secrets.xcconfig.example" ]; then
            cp "ios/Secrets.xcconfig.example" "ios/MomFriend/Secrets.xcconfig"
          fi
          : "${SENTRY_DSN:?SENTRY_DSN is not set}"
          awk -v proxy_secret="$PROXY_SECRET" -v sentry_dsn="$SENTRY_DSN" '
            /^PROXY_SECRET = / { print "PROXY_SECRET = " proxy_secret; next }
            /^SENTRY_DSN = / { print "SENTRY_DSN = " sentry_dsn; next }
            { print }
          ' "ios/MomFriend/Secrets.xcconfig" > "ios/MomFriend/Secrets.xcconfig.tmp"
          mv "ios/MomFriend/Secrets.xcconfig.tmp" "ios/MomFriend/Secrets.xcconfig"
`,
				}
			},
		},
		{
			// dailybread cuts TestFlight builds from its own testflight.yml, which
			// calls the managed writer; its managed ios-release.yml writes nothing.
			name: "dailybread",
			manifest: `
[project]
name = "dailybread"
project_name = "DailyBread"
scheme = "DailyBread"
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY", SENTRY_DSN = "SENTRY_DSN" }
`,
			workflows: func(t *testing.T) map[string]string {
				return map[string]string{
					"ios-ci.yml":      managedCI(t, "DailyBread/"),
					"ios-release.yml": managedRelease(t, ""),
					"testflight.yml": `jobs:
  testflight:
    steps:
      - name: Write release configuration
        env:
          REVENUECAT_API_KEY: ${{ secrets.REVENUECAT_API_KEY }}
          SENTRY_DSN: ${{ secrets.SENTRY_DSN }}
        run: |
          scripts/write-release-config.sh "DailyBread/Secrets.xcconfig" \
            "REVENUECAT_API_KEY=appl_*" \
            "SENTRY_DSN=https://*"
`,
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := loadManifest(t, dir, tc.manifest)
			wfs := tc.workflows(t)
			if _, ok := wfs["ios-release.yml"]; !ok {
				// The managed release, rendered for this manifest by the real
				// renderer — so the writer it emits is the one being recognised.
				wfs["ios-release.yml"] = managedRelease(t, tokens.ProductSecrets(cfg.Products(), ""))
			}
			writeWorkflows(t, dir, wfs)
			if fs := InertSecretDeclarations(dir, cfg); len(fs) != 0 {
				t.Fatalf("a project whose release genuinely writes its secrets file was reported inert: %+v", fs)
			}
		})
	}
}

// The managed release is the writer this audit must never miss: whatever
// tokens.ProductSecrets renders, for any component prefix and any secrets_file,
// has to count as writing that file. Checked against the renderer's output
// rather than a hand-typed line, so the two cannot drift apart.
func TestTheManagedWriterAsRenderedCounts(t *testing.T) {
	for _, tc := range []struct{ prefix, secretsFile string }{
		{"", ""},
		{"Flare/", ""},
		{"ios/", "Config/Monetization.xcconfig"},
	} {
		manifest := `
[project]
name = "Probe"
project_name = "Probe"
scheme = "Probe"
secrets = { REVENUECAT_API_KEY = "REVENUECAT_API_KEY" }
`
		if tc.secretsFile != "" {
			manifest += "secrets_file = \"" + tc.secretsFile + "\"\n"
		}
		dir := t.TempDir()
		cfg := loadManifest(t, dir, manifest)
		step := tokens.ProductSecrets(cfg.Products(), tc.prefix)
		if step == "" {
			t.Fatal("fixture: the renderer emitted no secrets step")
		}
		writeWorkflows(t, dir, map[string]string{
			"ios-ci.yml":      managedCI(t, tc.prefix),
			"ios-release.yml": managedRelease(t, step),
		})
		if fs := InertSecretDeclarations(dir, cfg); len(fs) != 0 {
			t.Errorf("prefix %q secrets_file %q: the managed writer's own rendered step was not recognised:\n%s",
				tc.prefix, tc.secretsFile, step)
		}
	}
}
