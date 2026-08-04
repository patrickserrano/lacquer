package adoptcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lacquerShipping(t *testing.T, profiles ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range profiles {
		write(t, filepath.Join(root, "profiles", p, "CLAUDE."+p+".md"), "# "+p)
	}
	return root
}

func manifest(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// rail's shape: an Xcode app and a supabase/ backend both at the repo root,
// with only one of them declared.
func TestAdoptAddsAProfileToAnExistingComponent(t *testing.T) {
	lq := lacquerShipping(t, "ios", "supabase")
	root := t.TempDir()
	write(t, filepath.Join(root, "Rail.xcodeproj", "project.pbxproj"), "x")
	write(t, filepath.Join(root, "supabase", "config.toml"), "x")
	write(t, filepath.Join(root, ".lacquer.toml"), `[project]
name = "rail"

# This comment explains something load-bearing and must survive.
[[component]]
path = "."
profiles = ["ios"]
`)

	summary, changed, err := Run(lq, root)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected the manifest to change")
	}
	got := manifest(t, root)
	if !strings.Contains(got, `profiles = ["ios", "supabase"]`) {
		t.Errorf("profiles not merged:\n%s", got)
	}
	if !strings.Contains(got, "must survive") {
		t.Errorf("comments were destroyed by the edit:\n%s", got)
	}
	if !strings.Contains(summary, "adopted: . -> supabase") {
		t.Errorf("summary = %q", summary)
	}
	if _, err := config.Load(filepath.Join(root, ".lacquer.toml")); err != nil {
		t.Errorf("adopted manifest no longer loads: %v", err)
	}
}

func TestAdoptAppendsANewComponent(t *testing.T) {
	lq := lacquerShipping(t, "web", "supabase")
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), "{}")
	write(t, filepath.Join(root, "server", "supabase", "config.toml"), "x")
	write(t, filepath.Join(root, ".lacquer.toml"), `[project]
name = "site"

[[component]]
path = "."
profiles = ["web"]
`)

	if _, changed, err := Run(lq, root); err != nil || !changed {
		t.Fatalf("Run: changed=%v err=%v", changed, err)
	}
	got := manifest(t, root)
	if !strings.Contains(got, `path = "server"`) || !strings.Contains(got, `profiles = ["supabase"]`) {
		t.Errorf("new component not appended:\n%s", got)
	}
	cfg, err := config.Load(filepath.Join(root, ".lacquer.toml"))
	if err != nil {
		t.Fatalf("adopted manifest no longer loads: %v", err)
	}
	if len(cfg.Components) != 2 {
		t.Errorf("components = %+v", cfg.Components)
	}
}

// A stack with no shipping profile cannot be adopted — but it must be reported
// rather than passed over in silence, since nothing gates it.
func TestAdoptReportsButSkipsUnsupportedStacks(t *testing.T) {
	lq := lacquerShipping(t, "web")
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), "{}")
	write(t, filepath.Join(root, "ios", "Kit", "Package.swift"), "x")
	write(t, filepath.Join(root, ".lacquer.toml"), `[project]
name = "x"

[[component]]
path = "."
profiles = ["web"]
`)

	summary, changed, err := Run(lq, root)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("nothing adoptable, so the manifest must not be rewritten")
	}
	if !strings.Contains(summary, "skipped: ios/Kit -> swift") {
		t.Errorf("summary must name the unsupported stack, got %q", summary)
	}
}

func TestAdoptOnACleanProjectChangesNothing(t *testing.T) {
	lq := lacquerShipping(t, "ios")
	root := t.TempDir()
	write(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"), "x")
	before := `[project]
name = "x"

[[component]]
path = "."
profiles = ["ios"]
`
	write(t, filepath.Join(root, ".lacquer.toml"), before)

	summary, changed, err := Run(lq, root)
	if err != nil {
		t.Fatal(err)
	}
	if changed || manifest(t, root) != before {
		t.Errorf("manifest was touched with nothing to adopt:\n%s", manifest(t, root))
	}
	if !strings.Contains(summary, "nothing to adopt") {
		t.Errorf("summary = %q", summary)
	}
}

// Refusing beats guessing: a multi-line array edited by line-splicing is how a
// manifest gets corrupted.
func TestAdoptRefusesAMultiLineProfilesArray(t *testing.T) {
	lq := lacquerShipping(t, "ios", "supabase")
	root := t.TempDir()
	write(t, filepath.Join(root, "App.xcodeproj", "project.pbxproj"), "x")
	write(t, filepath.Join(root, "supabase", "config.toml"), "x")
	before := `[project]
name = "x"

[[component]]
path = "."
profiles = [
  "ios",
]
`
	write(t, filepath.Join(root, ".lacquer.toml"), before)

	_, _, err := Run(lq, root)
	if err == nil || !strings.Contains(err.Error(), "multi-line") {
		t.Fatalf("err = %v; want a refusal naming the multi-line array", err)
	}
	if manifest(t, root) != before {
		t.Error("manifest must be left untouched when the edit is refused")
	}
}

// The component exists (init records one with an empty profile list for a stack
// with no shipping profile) but has no `profiles` key at all.
func TestAdoptInsertsProfilesWhenTheKeyIsAbsent(t *testing.T) {
	lq := lacquerShipping(t, "web")
	root := t.TempDir()
	write(t, filepath.Join(root, "app", "package.json"), "{}")
	write(t, filepath.Join(root, ".lacquer.toml"), `[project]
name = "x"

[[component]]
path = "app"
`)

	if _, changed, err := Run(lq, root); err != nil || !changed {
		t.Fatalf("Run: changed=%v err=%v", changed, err)
	}
	cfg, err := config.Load(filepath.Join(root, ".lacquer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Components) != 1 || len(cfg.Components[0].Profiles) != 1 || cfg.Components[0].Profiles[0] != "web" {
		t.Errorf("components = %+v", cfg.Components)
	}
}

// Adopt never removes. A stack that has genuinely gone away is a real decision
// with real consequences, so it is left for a human.
func TestAdoptNeverRemovesAProfile(t *testing.T) {
	lq := lacquerShipping(t, "ios", "web")
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), "{}")
	write(t, filepath.Join(root, ".lacquer.toml"), `[project]
name = "x"

[[component]]
path = "."
profiles = ["ios", "web"]
`)

	if _, _, err := Run(lq, root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(manifest(t, root), `"ios"`) {
		t.Error("adopt removed a profile whose marker is gone; it must only ever add")
	}
}
