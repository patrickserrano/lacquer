package detect

import (
	"path/filepath"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

// lacquerShipping builds a lacquer checkout that ships the named profiles.
func lacquerShipping(t *testing.T, profiles ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range profiles {
		mkFile(t, filepath.Join(root, "profiles", p, "CLAUDE."+p+".md"), "# "+p)
	}
	return root
}

// The needledrop case end to end: a manifest that says "web", a repo that has
// grown Swift, and a lacquer with no swift profile. The stack must be reported —
// as the lacquer's gap, not the project's — rather than passing silently.
func TestDriftReportsSwiftWithNoShippingProfile(t *testing.T) {
	lq := lacquerShipping(t, "web", "ios")
	root := t.TempDir()
	mk(t, filepath.Join(root, "spike", "package.json"))
	mk(t, filepath.Join(root, "ios", "SleevetapNFC", "Package.swift"))

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"web"}}}}
	findings, err := Drift(lq, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly the swift one", findings)
	}
	f := findings[0]
	if f.Profile != SwiftProfile || f.Path != "ios/SleevetapNFC" || f.Ships {
		t.Errorf("finding = %+v, want the swift package reported as unsupported", f)
	}
	if len(Adoptable(findings)) != 0 {
		t.Error("a stack with no shipping profile must not gate: the gap is the lacquer's")
	}
	if len(Unsupported(findings)) != 1 {
		t.Error("a stack with no shipping profile must still be reported, every run")
	}
}

// A stack the lacquer DOES ship is the project's to fix, and gates.
func TestDriftReportsShippingProfileAsAdoptable(t *testing.T) {
	lq := lacquerShipping(t, "web", "supabase")
	root := t.TempDir()
	mk(t, filepath.Join(root, "package.json"))
	mk(t, filepath.Join(root, "server", "supabase", "config.toml"))

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"web"}}}}
	findings, err := Drift(lq, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	adoptable := Adoptable(findings)
	if len(adoptable) != 1 || adoptable[0].Path != "server" || adoptable[0].Profile != "supabase" {
		t.Fatalf("adoptable = %+v, want server -> supabase", adoptable)
	}
}

// A profile already declared anywhere counts as managed. Detection picks a
// component path heuristically, and re-deriving one that disagrees with a
// hand-tuned manifest would report drift on correctly-configured projects.
func TestDriftIgnoresProfileDeclaredAtADifferentPath(t *testing.T) {
	lq := lacquerShipping(t, "ios")
	root := t.TempDir()
	mk(t, filepath.Join(root, "Flare", "Flare.xcodeproj", "project.pbxproj"))

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}}}
	findings, err := Drift(lq, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none: ios is declared, just at another path", findings)
	}
}

// [project].exclude is the escape hatch — a detected stack a project keeps
// deliberately unmanaged. It already means "not the lacquer's business".
func TestDriftHonoursExclude(t *testing.T) {
	lq := lacquerShipping(t, "web")
	root := t.TempDir()
	mk(t, filepath.Join(root, "fixtures", "sample-app", "package.json"))

	cfg := &config.Config{Project: config.Project{Exclude: []string{"fixtures"}}}
	findings, err := Drift(lq, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none under an excluded path", findings)
	}
}

func TestDriftCleanProjectReportsNothing(t *testing.T) {
	lq := lacquerShipping(t, "ios")
	root := t.TempDir()
	mk(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"))

	cfg := &config.Config{Components: []config.Component{{Path: ".", Profiles: []string{"ios"}}}}
	findings, err := Drift(lq, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %+v, want none", findings)
	}
}
