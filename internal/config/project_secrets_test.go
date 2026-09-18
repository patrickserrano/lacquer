package config

import (
	"slices"
	"strings"
	"testing"
)

// [project] is already the implicit single product for scheme, bundle_id,
// asc_app_id, extra_bundle_ids, extra_test_targets and watch_tests. Release
// secrets were the one release-shaped field left out, and the omission was not
// cosmetic: flare, kit, port-of-entry and multimeter all read build-time keys
// (a RevenueCat appl_ key, Aptabase, a Sentry DSN, an API key) from a
// gitignored Secrets.xcconfig, declare no [[product]], and so had no way to ask
// the release to write that file. Their next release would have shipped
// placeholder or empty keys: a dead paywall, no analytics, no crash reports.
const singleAppWithSecrets = `
[project]
name = "Flare"
project_name = "Flare"
scheme = "Flare"
bundle_id = "com.example.flare"
asc_app_id = "1000000001"
secrets_file = "Config/Secrets.xcconfig"
secrets = { REVENUECAT_API_KEY = "FLARE_REVENUECAT_API_KEY", SENTRY_DSN = "FLARE_SENTRY_DSN" }
secret_formats = { REVENUECAT_API_KEY = "appl_*" }
`

func TestProjectSecretsFoldIntoTheSynthesisedProduct(t *testing.T) {
	cfg, err := loadString(t, singleAppWithSecrets)
	if err != nil {
		t.Fatalf("a single-app manifest declaring release secrets must load: %v", err)
	}
	products := cfg.Products()
	if len(products) != 1 {
		t.Fatalf("got %d products, want the synthesised one", len(products))
	}
	p := products[0]
	if got := p.Secrets["REVENUECAT_API_KEY"]; got != "FLARE_REVENUECAT_API_KEY" {
		t.Errorf("Secrets[REVENUECAT_API_KEY] = %q — [project].secrets did not reach the synthesised product, "+
			"so the release would write nothing and ship a placeholder key", got)
	}
	if got := p.Secrets["SENTRY_DSN"]; got != "FLARE_SENTRY_DSN" {
		t.Errorf("Secrets[SENTRY_DSN] = %q, want FLARE_SENTRY_DSN", got)
	}
	if got := p.SecretFormats["REVENUECAT_API_KEY"]; got != "appl_*" {
		t.Errorf("SecretFormats[REVENUECAT_API_KEY] = %q — [project].secret_formats was dropped, so a wrong-shaped "+
			"key would pass the release's shape check", got)
	}
	if got := p.SecretsPath(); got != "Config/Secrets.xcconfig" {
		t.Errorf("SecretsPath() = %q — [project].secrets_file was dropped, so the release would write the "+
			"default file while the build reads the declared one", got)
	}
}

// secrets_file is optional on [project] exactly as it is on [[product]].
func TestProjectSecretsDefaultTheirFile(t *testing.T) {
	cfg, err := loadString(t, `
[project]
name = "Kit"
project_name = "Kit"
scheme = "Kit"
secrets = { APTABASE_APP_KEY = "KIT_APTABASE_APP_KEY" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Products()[0].SecretsPath(); got != "Secrets.xcconfig" {
		t.Errorf("SecretsPath() = %q, want the profile's default Secrets.xcconfig", got)
	}
}

// Every guard a [[product]]'s secrets get must hold for the [project] spelling.
// The product loop in Load only sees DECLARED products, so without a separate
// pass the [project] route would skip them all — including the one that stops a
// pasted credential being committed and the one that keeps manifest text out of
// an unquoted shell `case` pattern.
//
// Each case is also run through a [[product]] block first, so a case that the
// product route does not reject either (a bad fixture) fails loudly rather than
// passing vacuously here.
func TestProjectSecretsAreValidatedLikeAProductsAre(t *testing.T) {
	project := "[project]\nname = \"X\"\nproject_name = \"X\"\nscheme = \"X\"\n"
	product := "[project]\nname = \"X\"\nproject_name = \"X\"\nscheme = \"X\"\n\n" +
		"[[product]]\nname = \"X\"\nscheme = \"X\"\nbundle_id = \"com.x.x\"\nasc_app_id = \"1\"\n"

	for _, tc := range []struct{ name, keys, want string }{
		{"invalid xcconfig key", `secrets = { "BAD-KEY" = "GOOD_NAME" }`, "invalid secrets key"},
		{"invalid secret name", `secrets = { KEY = "not a name" }`, "must be the NAME of a GitHub secret"},
		{"pasted credential", `secrets = { KEY = "appl_abcdef" }`, "looks like a real credential"},
		{"GITHUB_ prefix", `secrets = { KEY = "GITHUB_TOKEN" }`, "GitHub refuses secret names starting with GITHUB_"},
		{"format with no matching secret", "secrets = { KEY = \"NAME\" }\nsecret_formats = { OTHER = \"appl_*\" }", "has no matching entry in secrets"},
		{"empty format", "secrets = { KEY = \"NAME\" }\nsecret_formats = { KEY = \"\" }", "is empty"},
		{"shell-unsafe format", "secrets = { KEY = \"NAME\" }\nsecret_formats = { KEY = \"a|b\" }", "unsafe in a shell pattern"},
		{"absolute secrets_file", "secrets = { KEY = \"NAME\" }\nsecrets_file = \"/etc/Secrets.xcconfig\"", "must be a relative path inside the project"},
		{"escaping secrets_file", "secrets = { KEY = \"NAME\" }\nsecrets_file = \"../Secrets.xcconfig\"", "must be a relative path inside the project"},
		{"secrets_file with no secrets", `secrets_file = "Config/Secrets.xcconfig"`, "secrets_file set but no secrets declared"},
		{"secret_formats with no secrets", `secret_formats = { KEY = "appl_*" }`, "has no matching entry in secrets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadString(t, product+tc.keys+"\n"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fixture is wrong: the [[product]] route does not reject %s with %q (got %v)", tc.name, tc.want, err)
			}
			_, err := loadString(t, project+tc.keys+"\n")
			if err == nil {
				t.Fatalf("accepted %s via [project] — the same value is rejected on [[product]], so this route "+
					"would be a way around the guard", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tc.want)
			}
			if !strings.Contains(err.Error(), "[project]") {
				t.Errorf("error %q does not name [project], the table the reader has to go and fix", err)
			}
		})
	}
}

// Declaring both spellings is ambiguous: which product do unattached keys
// belong to? For secrets the wrong guess is worse than for test selectors — a
// paid app's key written into the free app's build is not a failure, it is a
// bad release. So each of the three is rejected on its own, not merged.
func TestProjectSecretsRejectedAlongsideDeclaredProducts(t *testing.T) {
	for _, tc := range []struct{ field, keys string }{
		{"secrets", `secrets = { KEY = "NAME" }`},
		{"secrets_file", `secrets_file = "Config/Secrets.xcconfig"`},
		{"secret_formats", `secret_formats = { KEY = "appl_*" }`},
	} {
		t.Run(tc.field, func(t *testing.T) {
			_, err := loadString(t, `
[project]
name = "X"
project_name = "X"
scheme = "X"
`+tc.keys+`

[[product]]
name = "Paid"
scheme = "Paid"
bundle_id = "com.x.paid"
asc_app_id = "1"
secrets = { KEY = "PAID_NAME" }
`)
			if err == nil {
				t.Fatalf("accepted [project].%s alongside a [[product]] block", tc.field)
			}
			if !strings.Contains(err.Error(), "[project]."+tc.field) {
				t.Errorf("error should name [project].%s, got %q", tc.field, err)
			}
			if !strings.Contains(err.Error(), "would have to be guessed") {
				t.Errorf("error should say why both spellings are refused, got %q", err)
			}
		})
	}
}

// A declared product is never touched by the [project] fold: Products() returns
// the declared list verbatim, secrets included.
func TestDeclaredProductSecretsAreUnchanged(t *testing.T) {
	cfg, err := loadString(t, `
[project]
name = "X"
project_name = "X"
scheme = "X"

[[product]]
name = "Paid"
scheme = "Paid"
bundle_id = "com.x.paid"
asc_app_id = "1"
secrets = { KEY = "PAID_NAME" }
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Products()[0].Secrets["KEY"]; got != "PAID_NAME" {
		t.Errorf("declared product's secrets = %q, want PAID_NAME", got)
	}
}

// The secret_formats charset, pinned character by character. The release
// script re-checks every pattern against its own copy of this set, so the
// two must be identical: a character allowed here and refused there loads
// cleanly and then fails every release, and one refused here and allowed
// there is a shape no project can declare. Pinning the exact set, rather than
// a few samples, means widening or narrowing it is a deliberate edit to this
// test, visible to whoever has to make the matching change in the script.
func TestSecretFormatCharsetIsExactly(t *testing.T) {
	const want = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_~.:/*?@-"
	var got strings.Builder
	for c := rune(0x20); c <= 0x7e; c++ {
		if globPatternVal.MatchString(string(c)) {
			got.WriteRune(c)
		}
	}
	norm := func(s string) string {
		b := []byte(s)
		slices.Sort(b)
		return string(b)
	}
	if norm(got.String()) != norm(want) {
		t.Errorf("secret_formats accepts %q, want exactly %q — change scripts/write-release-config.sh's "+
			"`*[!…]*` class in the same breath, or releases will refuse what the manifest accepts", norm(got.String()), norm(want))
	}
	// Non-ASCII and control characters stay out too.
	for _, bad := range []string{"\t", "\n", "é", "appl_\x00"} {
		if globPatternVal.MatchString(bad) {
			t.Errorf("secret_formats accepts %q", bad)
		}
	}
}

// A Sentry DSN is https://<key>@<host>/<project>. Without `@` its shape could
// not be declared at all, on either spelling.
func TestSecretFormatsAcceptASentryDSNShape(t *testing.T) {
	for name, manifest := range map[string]string{
		"[project]": `
[project]
name = "X"
project_name = "X"
scheme = "X"
secrets = { SENTRY_DSN = "X_SENTRY_DSN" }
secret_formats = { SENTRY_DSN = "https://*@*/*" }
`,
		"[[product]]": `
[project]
name = "X"
project_name = "X"
scheme = "X"

[[product]]
name = "X"
scheme = "X"
bundle_id = "com.x.x"
asc_app_id = "1"
secrets = { SENTRY_DSN = "X_SENTRY_DSN" }
secret_formats = { SENTRY_DSN = "https://*@*/*" }
`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadString(t, manifest)
			if err != nil {
				t.Fatalf("a Sentry DSN shape was refused: %v", err)
			}
			if got := cfg.Products()[0].SecretFormats["SENTRY_DSN"]; got != "https://*@*/*" {
				t.Errorf("SecretFormats[SENTRY_DSN] = %q", got)
			}
		})
	}
}
