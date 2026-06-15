package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".harness.toml")
	data := `
[project]
name = "journalcast"

[[component]]
path = "ios"
profiles = ["ios"]

[[component]]
path = "dashboard"
profiles = ["web"]
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Name != "journalcast" {
		t.Errorf("project name = %q, want journalcast", cfg.Project.Name)
	}
	if len(cfg.Components) != 2 {
		t.Fatalf("got %d components, want 2", len(cfg.Components))
	}
	if cfg.Components[0].Path != "ios" || cfg.Components[0].Profiles[0] != "ios" {
		t.Errorf("component[0] = %+v", cfg.Components[0])
	}
	if cfg.Components[1].Path != "dashboard" || cfg.Components[1].Profiles[0] != "web" {
		t.Errorf("component[1] = %+v", cfg.Components[1])
	}
}
