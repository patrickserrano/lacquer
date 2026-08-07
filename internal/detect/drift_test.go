package detect

import (
	"path/filepath"
	"strings"
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

// An agent worktree under .claude/ is a full, gitignored checkout of the
// project. Walking into it yields a phantom copy of every component in the
// repo — and since an undeclared stack now makes `sync` refuse and `audit` exit
// 6, those phantoms would block the repo and `lacquer adopt` would write them
// into the manifest as real components. Observed on throughline, which reported
// four undeclared stacks where it has two.
func TestDriftIgnoresAgentWorktrees(t *testing.T) {
	lq := lacquerShipping(t, "web", "supabase")
	root := t.TempDir()
	mk(t, filepath.Join(root, "admin", "package.json"))
	mk(t, filepath.Join(root, "server", "supabase", "config.toml"))
	// The agent worktree: a mirror of the whole project.
	mk(t, filepath.Join(root, ".claude", "worktrees", "agent-abc123", "admin", "package.json"))
	mk(t, filepath.Join(root, ".claude", "worktrees", "agent-abc123", "server", "supabase", "config.toml"))
	// Codex keeps its own; same story.
	mk(t, filepath.Join(root, ".codex", "worktrees", "x", "admin", "package.json"))

	cfg := &config.Config{}
	findings, err := Drift(lq, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Path, ".claude") || strings.HasPrefix(f.Path, ".codex") {
			t.Errorf("agent tool dir reported as a component: %+v", f)
		}
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want exactly admin+server: %+v", len(findings), findings)
	}
}
