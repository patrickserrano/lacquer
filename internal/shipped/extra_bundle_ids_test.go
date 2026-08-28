package shipped

import (
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// extra_bundle_ids is what lets release.yml fetch signing files for a widget
// or watch companion app alongside the main app — each is its own Apple
// target with its own App ID, invisible to the main bundle_id's provisioning
// profile. A project declaring none must still render a valid, no-op catalog
// entry ("extra_bundle_ids":[]), not an absent field or invalid JSON.
func TestReleaseCatalogOmitsExtraBundleIDsByDefault(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.solo", AscAppID: "1", Xcodeproj: "Solo.xcodeproj",
	}}
	rendered := renderRelease(t, cfg)
	if !strings.Contains(rendered, `"extra_bundle_ids":[]`) {
		t.Errorf("a project with no extra_bundle_ids must still render an empty catalog array:\n%s", rendered)
	}
}

func TestReleaseCatalogCarriesExtraBundleIDs(t *testing.T) {
	cfg := &config.Config{Project: config.Project{
		ProjectName: "Solo", Scheme: "Solo", BundleID: "com.x.solo", AscAppID: "1", Xcodeproj: "Solo.xcodeproj",
		ExtraBundleIDs: []string{"com.x.solo.widget", "com.x.solo.watchkitapp"},
	}}
	rendered := renderRelease(t, cfg)
	if !strings.Contains(rendered, `"extra_bundle_ids":["com.x.solo.widget","com.x.solo.watchkitapp"]`) {
		t.Errorf("extra_bundle_ids did not reach the release catalog:\n%s", rendered)
	}
	if !strings.Contains(rendered, "PRODUCT_EXTRA_BUNDLE_IDS") {
		t.Error("the fetch-signing-files step must read the matrix product's extra_bundle_ids")
	}
	if !strings.Contains(rendered, "fetch-signing-files \"$EXTRA_BUNDLE_ID\"") {
		t.Error("expected a loop fetching signing files for each extra bundle id")
	}
}

// Per-product, not just per-project: a paid and free variant's widgets are
// different Apple targets with different bundle ids.
func TestReleaseCatalogCarriesPerProductExtraBundleIDs(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P", Scheme: "P", BundleID: "com.x.p", AscAppID: "1", Xcodeproj: "P.xcodeproj"},
		Product: []config.Product{
			{Name: "Paid", Scheme: "Paid", BundleID: "com.x.paid", AscAppID: "111", TagPrefix: "paid",
				ExtraBundleIDs: []string{"com.x.paid.widget"}},
			{Name: "Free", Scheme: "Free", BundleID: "com.x.free", AscAppID: "222", TagPrefix: "free",
				ExtraBundleIDs: []string{"com.x.free.widget"}},
		},
	}
	rendered := renderRelease(t, cfg)
	if !strings.Contains(rendered, `"bundle_id":"com.x.paid","asc_app_id":"111","tag_prefix":"paid","extra_bundle_ids":["com.x.paid.widget"]`) {
		t.Errorf("Paid's own extra_bundle_ids did not render correctly:\n%s", rendered)
	}
	if !strings.Contains(rendered, `"bundle_id":"com.x.free","asc_app_id":"222","tag_prefix":"free","extra_bundle_ids":["com.x.free.widget"]`) {
		t.Errorf("Free's own extra_bundle_ids did not render correctly:\n%s", rendered)
	}
}
