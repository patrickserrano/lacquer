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

// loadString writes data to a temp .harness.toml and loads it.
func loadString(t *testing.T, data string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".harness.toml")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadRejectsTraversalComponentPath(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"../escape\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"../../etc\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"/abs/path\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios/../../up\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"\"\nprofiles=[\"ios\"]\n",
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected error for component path in:\n%s", data)
		}
	}
}

func TestLoadRejectsInvalidProfileName(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"../evil\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"a/b\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"..\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"UPPER\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"\"]\n",
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected error for profile name in:\n%s", data)
		}
	}
}

func TestLoadAcceptsValidNames(t *testing.T) {
	data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"apps/ios-app\"\nprofiles=[\"ios\",\"web-2\"]\n"
	if _, err := loadString(t, data); err != nil {
		t.Errorf("valid manifest rejected: %v", err)
	}
}
