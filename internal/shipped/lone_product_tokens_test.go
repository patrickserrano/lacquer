package shipped

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
	"github.com/patrickserrano/lacquer/internal/sync"
)

// syncManifest syncs the real lacquer into a fresh git repository carrying
// manifest, and returns the project path and sync's error.
func syncManifest(t *testing.T, manifest string) (string, error) {
	t.Helper()
	project := t.TempDir()
	gittest.Init(t, project, "-q")
	git(t, project, "config", "user.email", "test@example.com")
	git(t, project, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(project, ".lacquer.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := sync.Run(root(t), project, false)
	return project, err
}

// A project whose one app is declared as a lone [[product]], with scheme,
// bundle_id and asc_app_id stated only there. Every iOS template that names
// {{SCHEME}}, {{BUNDLE_ID}} or {{ASC_APP_ID}} (ci.yml, build.md, test.md,
// macos-ci-recipes) used to refuse to render: "missing [project] values for
// placeholders: {{SCHEME}}". flare works around it by stating scheme twice.
func TestLoneProductRendersEveryTemplate(t *testing.T) {
	project, err := syncManifest(t, `
[project]
name = "Flare"
project_name = "Flare"
xcodeproj = "Flare.xcodeproj"
swift_version = "6"

[[product]]
name = "Flare"
scheme = "FlareScheme"
bundle_id = "com.example.flare"
asc_app_id = "1000000001"

[[component]]
path = "."
profiles = ["ios"]
`)
	if err != nil {
		t.Fatalf("a lone [[product]] could not stand in for [project]: %v", err)
	}
	// The scheme reached the templates that name {{SCHEME}}, rather than an
	// empty string or a surviving placeholder (which sync would have refused).
	for _, rel := range []string{".github/workflows/ios-ci.yml", ".claude/commands/build.md", ".claude/commands/test.md"} {
		b, err := os.ReadFile(filepath.Join(project, rel))
		if err != nil {
			t.Errorf("%s was not rendered: %v", rel, err)
			continue
		}
		if !strings.Contains(string(b), "FlareScheme") {
			t.Errorf("%s does not carry the lone product's scheme", rel)
		}
	}
}

// Two products and a blank [project] still refuse: which app a project-wide
// template means would have to be guessed. The refusal now says so, rather
// than only telling the reader to fill in [project].
func TestSeveralProductsWithBlankProjectStillRefuse(t *testing.T) {
	_, err := syncManifest(t, `
[project]
name = "P"
project_name = "P"
xcodeproj = "P.xcodeproj"
swift_version = "6"

[[product]]
name = "Paid"
scheme = "Paid"
bundle_id = "com.x.paid"
asc_app_id = "1"
tag_prefix = "paid"

[[product]]
name = "Free"
scheme = "Free"
bundle_id = "com.x.free"
asc_app_id = "2"
tag_prefix = "free"

[[component]]
path = "."
profiles = ["ios"]
`)
	if err == nil {
		t.Fatal("two products with a blank [project].scheme synced — some template guessed which app it meant")
	}
	for _, want := range []string{"{{SCHEME}}", "2 [[product]] blocks", "would have to be guessed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not say %q:\n%v", want, err)
		}
	}
}
