package assets

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/patrickserrano/harness/internal/config"
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

func TestPlan(t *testing.T) {
	h := t.TempDir()
	// core assets
	write(t, filepath.Join(h, "core", "skills", "git.md"), "x")
	write(t, filepath.Join(h, "core", "commands", "sync-worktree.md"), "x")
	// ios profile assets
	write(t, filepath.Join(h, "profiles", "ios", "skills", "build.md"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "config", ".swiftlint.yml"), "x")

	cfg := &config.Config{
		Components: []config.Component{
			{Path: "ios", Profiles: []string{"ios"}},
		},
	}

	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Map dest -> src for assertion.
	dests := map[string]string{}
	for _, a := range got {
		dests[a.Dest] = a.Src
	}
	want := []string{
		filepath.Join(".claude", "skills", "git.md"),
		filepath.Join(".claude", "commands", "sync-worktree.md"),
		filepath.Join(".claude", "skills", "build.md"),
		filepath.Join(".github", "workflows", "ios-ci.yml"),
		filepath.Join("ios", ".swiftlint.yml"),
	}
	var gotDests []string
	for d := range dests {
		gotDests = append(gotDests, d)
	}
	sort.Strings(want)
	sort.Strings(gotDests)
	if len(gotDests) != len(want) {
		t.Fatalf("got %d assets %v, want %d %v", len(gotDests), gotDests, len(want), want)
	}
	for i := range want {
		if gotDests[i] != want[i] {
			t.Errorf("dest[%d] = %q, want %q", i, gotDests[i], want[i])
		}
	}
	// Source paths must be absolute and exist.
	for _, a := range got {
		if !filepath.IsAbs(a.Src) {
			t.Errorf("src not absolute: %q", a.Src)
		}
	}
}
