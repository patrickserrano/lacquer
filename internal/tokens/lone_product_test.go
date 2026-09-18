package tokens

import (
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// A manifest whose ONE app is declared as a [[product]] block rather than in
// [project] is a single-product project in every sense Products() recognises,
// but Values() read {{SCHEME}}, {{BUNDLE_ID}} and {{ASC_APP_ID}} from [project]
// only. Sync then refused with "missing [project] values for placeholders:
// {{SCHEME}}", and the only way through was to state the scheme twice, which is
// what flare does today.
func TestLoneProductSuppliesProjectTokens(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "Flare"},
		Product: []config.Product{{Name: "Flare", Scheme: "FlareScheme", BundleID: "com.x.flare", AscAppID: "123"}},
	}
	v := Values(cfg, "")
	for tok, want := range map[string]string{Scheme: "FlareScheme", BundleID: "com.x.flare", AscAppID: "123"} {
		if v[tok] != want {
			t.Errorf("%s = %q, want %q from the lone [[product]]", tok, v[tok], want)
		}
	}
}

// [project] still wins when it names a value: the fallback fills a blank, it
// does not override what the manifest said at the project level.
func TestProjectTokensWinOverALoneProduct(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "Flare", Scheme: "ProjScheme", BundleID: "com.x.proj", AscAppID: "999"},
		Product: []config.Product{{Name: "Flare", Scheme: "FlareScheme", BundleID: "com.x.flare", AscAppID: "123"}},
	}
	v := Values(cfg, "")
	for tok, want := range map[string]string{Scheme: "ProjScheme", BundleID: "com.x.proj", AscAppID: "999"} {
		if v[tok] != want {
			t.Errorf("%s = %q, want [project]'s %q", tok, v[tok], want)
		}
	}
}

// With two products there is no single answer to "the scheme": a project-wide
// template naming {{SCHEME}} would have to guess which app it meant. So the
// tokens stay blank and the sync preflight refuses, exactly as before.
func TestSeveralProductsDoNotSupplyProjectTokens(t *testing.T) {
	cfg := &config.Config{
		Project: config.Project{ProjectName: "P"},
		Product: []config.Product{
			{Name: "Paid", Scheme: "Paid", BundleID: "com.x.paid", AscAppID: "1", TagPrefix: "paid"},
			{Name: "Free", Scheme: "Free", BundleID: "com.x.free", AscAppID: "2", TagPrefix: "free"},
		},
	}
	v := Values(cfg, "")
	for _, tok := range []string{Scheme, BundleID, AscAppID} {
		if v[tok] != "" {
			t.Errorf("%s = %q with two products — that is a guess at which app a project-wide template means", tok, v[tok])
		}
	}
}
