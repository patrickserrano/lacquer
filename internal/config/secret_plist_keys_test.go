package config

import (
	"strings"
	"testing"
)

// secret_plist_keys names the Info.plist key that carries a declared secret,
// for the release's check of the archived app. It is rendered onto a command
// line and matched against the archived plist, so it is validated like every
// other secrets field, on both spellings.

const plistKeysProduct = `
[project]
name = "Demo"
project_name = "Demo"

[[product]]
name = "Free"
scheme = "Free"
bundle_id = "com.example.free"
asc_app_id = "1"
secrets = { API_KEY = "DEMO_API_KEY", AD_APP_ID = "DEMO_AD_APP_ID" }
`

func TestSecretPlistKeysLoadOnAProduct(t *testing.T) {
	cfg, err := loadString(t, plistKeysProduct+`secret_plist_keys = { AD_APP_ID = "GADApplication.Identifier-2" }`+"\n")
	if err != nil {
		t.Fatalf("a valid secret_plist_keys must load: %v", err)
	}
	if got := cfg.Products()[0].SecretPlistKeys["AD_APP_ID"]; got != "GADApplication.Identifier-2" {
		t.Errorf("SecretPlistKeys[AD_APP_ID] = %q", got)
	}
}

func TestSecretPlistKeysFoldFromProject(t *testing.T) {
	cfg, err := loadString(t, `
[project]
name = "Demo"
project_name = "Demo"
scheme = "Demo"
bundle_id = "com.example.demo"
asc_app_id = "1"
secrets = { AD_APP_ID = "DEMO_AD_APP_ID" }
secret_plist_keys = { AD_APP_ID = "AdAppIdentifier" }
`)
	if err != nil {
		t.Fatalf("[project].secret_plist_keys must load: %v", err)
	}
	if got := cfg.Products()[0].SecretPlistKeys["AD_APP_ID"]; got != "AdAppIdentifier" {
		t.Errorf("[project].secret_plist_keys did not reach the synthesised product: %q", got)
	}
}

func TestSecretPlistKeysAreValidated(t *testing.T) {
	for _, tc := range []struct{ name, line, want string }{
		{"names an undeclared secret", `secret_plist_keys = { OTHER = "Other" }`, "secret_plist_keys.OTHER has no matching entry in secrets"},
		{"empty plist key", `secret_plist_keys = { API_KEY = "" }`, "is not an Info.plist key name"},
		{"shell in the plist key", `secret_plist_keys = { API_KEY = "Key$(rm -rf /)" }`, "is not an Info.plist key name"},
		{"a quote in the plist key", `secret_plist_keys = { API_KEY = "Key\"x" }`, "is not an Info.plist key name"},
		{"a space in the plist key", `secret_plist_keys = { API_KEY = "Api Key" }`, "is not an Info.plist key name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, plistKeysProduct+tc.line+"\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// [project].secret_plist_keys beside a [[product]] block would leave which
// product it maps to to a guess.
func TestProjectSecretPlistKeysRefusedBesideAProduct(t *testing.T) {
	m := strings.Replace(plistKeysProduct, `project_name = "Demo"`, `project_name = "Demo"`+"\n"+`secret_plist_keys = { API_KEY = "ApiKey" }`, 1)
	_, err := loadString(t, m)
	if err == nil || !strings.Contains(err.Error(), "[project].secret_plist_keys is set alongside") {
		t.Fatalf("err = %v, want [project].secret_plist_keys refused beside a product", err)
	}
}
