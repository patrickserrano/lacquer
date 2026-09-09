package config

import (
	"strings"
	"testing"
)

// [project] is already the implicit single product for asc_app_id, bundle_id,
// extra_bundle_ids and scheme. extra_test_targets now follows the same rule.
//
// The gap this closes was silent by construction: dailybread gained a second
// test target (51 tests) that the scheme runs locally, while the synced
// workflow's `-only-testing:` whitelist named only the app's own bundle. The
// job stayed green while those 51 never executed — the same shape as a
// selector matching nothing.
func TestProjectExtraTestTargetsFoldIntoTheSynthesisedProduct(t *testing.T) {
	cfg, err := loadString(t, `
[project]
name = "DailyBread"
project_name = "DailyBread"
scheme = "DailyBread"
extra_test_targets = ["DailyBreadWidgetsTests"]
`)
	if err != nil {
		t.Fatal(err)
	}
	products := cfg.Products()
	if len(products) != 1 {
		t.Fatalf("got %d products, want the synthesised one", len(products))
	}
	got := strings.Join(products[0].TestSelectors(), "|")
	if !strings.Contains(got, "DailyBreadWidgetsTests") {
		t.Errorf("selectors = %q — [project].extra_test_targets did not reach the synthesised product, "+
			"so the extra suite would still be run by nothing while CI stayed green", got)
	}
	if !strings.Contains(got, "DailyBreadTests") {
		t.Errorf("selectors = %q — the derived unit target was lost", got)
	}
}

// The guards are the only thing between this field and, in its own words, "a
// per-line licence to run nothing and call it green". The product loop sees
// only DECLARED products, so the [project] route must be validated separately
// or it skips them entirely.
func TestProjectExtraTestTargetsAreValidatedLikeAProductsAre(t *testing.T) {
	base := "[project]\nname = \"X\"\nproject_name = \"X\"\nscheme = \"X\"\n"

	for _, tc := range []struct{ name, toml, want string }{
		{"empty entry", base + `extra_test_targets = ["XTests2", ""]` + "\n", "empty"},
		{"invalid name", base + `extra_test_targets = ["bad name!"]` + "\n", "invalid"},
		{"duplicate of the derived unit target", base + `extra_test_targets = ["XTests"]` + "\n", "already selected"},
		{"repeat within the list", base + `extra_test_targets = ["ATests", "ATests"]` + "\n", "already selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadString(t, tc.toml)
			if err == nil {
				t.Fatalf("accepted %s via [project] — the same value is rejected on [[product]], so this route "+
					"would be a way around the guard", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tc.want)
			}
		})
	}
}

// Declaring both spellings is ambiguous — which product does an unattached list
// belong to? Rejected rather than merged or silently dropped.
func TestProjectExtraTestTargetsRejectedAlongsideDeclaredProducts(t *testing.T) {
	_, err := loadString(t, `
[project]
name = "X"
project_name = "X"
scheme = "X"
extra_test_targets = ["XKitTests"]

[[product]]
name = "Paid"
scheme = "Paid"
bundle_id = "com.x.paid"
asc_app_id = "1"
`)
	if err == nil {
		t.Fatal("accepted [project].extra_test_targets alongside a [[product]] block")
	}
	if !strings.Contains(err.Error(), "[[product]]") {
		t.Errorf("error should point at the product blocks, got %q", err)
	}
}
