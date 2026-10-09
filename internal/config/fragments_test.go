package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadManifest(t *testing.T, body string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".lacquer.toml")
	if err := os.WriteFile(path, []byte("[project]\nname='demo'\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestFragmentKeysLoad(t *testing.T) {
	cfg, err := loadManifest(t, `
[ios.swiftlint_custom_rules.sentry_import_confined]
regex = '^\s*import\s+Sentry\s*$'
severity = 'error'
excluded = ['.*Reporter\.swift$']

[web]
typedoc_entry_points = ['src/cli.ts']

[[web.biome_overrides]]
includes = ['core/**']
[web.biome_overrides.linter.rules.style.noRestrictedImports]
level = 'error'
options = { patterns = [{ group = ['node:*'], message = 'x' }] }
`)
	if err != nil {
		t.Fatal(err)
	}
	if r := cfg.IOS.SwiftLintCustomRules["sentry_import_confined"]; r.Regex != `^\s*import\s+Sentry\s*$` || len(r.Excluded) != 1 {
		t.Errorf("swiftlint rule decoded as %+v", r)
	}
	if len(cfg.Web.TypeDocEntryPoints) != 1 || len(cfg.Web.BiomeOverrides) != 1 {
		t.Errorf("web fragments decoded as %+v", cfg.Web)
	}
}

// The unknown-key check is blind inside [[web.biome_overrides]] by design; the
// fragment validator must refuse a nested key it does not name instead. Typos
// in the surrounding tables are still the unknown-key check's to catch.
func TestFragmentKeysRefuseUnknownKeys(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"nested formatter":       {"[[web.biome_overrides]]\nincludes=['**']\n[web.biome_overrides.formatter]\nenabled=false\n", `sets "formatter"`},
		"nested linter key":      {"[[web.biome_overrides]]\nincludes=['**']\n[web.biome_overrides.linter]\nenabled=false\n", "linter.enabled"},
		"misspelled web key":     {"[web]\nbiome_override=[]\n", "unknown key"},
		"misspelled ios key":     {"[ios.swiftlint_custom_rule.x]\nregex='a'\n", "unknown key"},
		"unknown rule key":       {"[ios.swiftlint_custom_rules.x]\nregex='a'\nregx='b'\n", "unknown key"},
		"swiftlint config knob":  {"[ios]\ndisabled_rules=['identifier_name']\n", "unknown key"},
		"empty entry points":     {"[web]\ntypedoc_entry_points=[]\n", "typedoc_entry_points is empty"},
		"bad rule severity":      {"[ios.swiftlint_custom_rules.x]\nregex='a'\nseverity='fatal'\n", "severity"},
		"override lowering rule": {"[[web.biome_overrides]]\nincludes=['**']\nlinter.rules.style.noDefaultExport='off'\n", "never lower"},
	} {
		_, err := loadManifest(t, c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want one containing %q", name, err, c.want)
		}
	}
}
