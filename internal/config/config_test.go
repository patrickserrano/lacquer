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

func TestLoadProjectValues(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"rail\"\nproject_name=\"Rail\"\nscheme=\"Rail\"\nbundle_id=\"com.me.rail\"\nasc_app_id=\"6451234567\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Project
	if p.ProjectName != "Rail" || p.Scheme != "Rail" || p.BundleID != "com.me.rail" || p.AscAppID != "6451234567" {
		t.Errorf("project = %+v", p)
	}
}

func TestLoadAllowsBlankProjectValues(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\nbundle_id=\"\"\n"); err != nil {
		t.Errorf("blank values must be allowed (init stubs them): %v", err)
	}
}

func TestEffectiveToolsDefaultsToClaude(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Project.EffectiveTools()
	if len(got) != 1 || got[0] != "claude" {
		t.Errorf("EffectiveTools() = %v, want [claude]", got)
	}
}

func TestLoadAcceptsKnownTools(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\ntools=[\"claude\",\"codex\",\"antigravity\"]\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Project.EffectiveTools()) != 3 {
		t.Errorf("tools = %v", cfg.Project.EffectiveTools())
	}
}

func TestLoadRejectsUnknownTool(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\ntools=[\"claude\",\"../evil\"]\n"); err == nil {
		t.Error("expected rejection of an unknown/unsafe tool name")
	}
}

func TestLoadRejectsInjectionInProjectValues(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\nscheme=\"Rail\\n  evil: true\"\n",
		"[project]\nname=\"x\"\nbundle_id=\"com.me.$(whoami)\"\n",
		"[project]\nname=\"x\"\nproject_name=\"Rail`id`\"\n",
		"[project]\nname=\"x\"\nasc_app_id=\"12a34\"\n",
		"[project]\nname=\"x\"\nscheme=\"a\\\"b\"\n",
	}
	for _, data := range cases {
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected rejection for project value in:\n%s", data)
		}
	}
}

func TestLoadRejectsDuplicateProfile(t *testing.T) {
	data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"a\"\nprofiles=[\"ios\"]\n\n[[component]]\npath=\"b\"\nprofiles=[\"ios\"]\n"
	if _, err := loadString(t, data); err == nil {
		t.Fatal("expected error: two components declare profile ios")
	}
}

func TestLoadRejectsUnsafeComponentPath(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios;rm -rf\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios app\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"ios$(x)\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"-rf\"\nprofiles=[\"ios\"]\n",
		"[project]\nname=\"x\"\n\n[[component]]\npath=\"apps/-evil\"\nprofiles=[\"ios\"]\n",
	}
	for _, d := range cases {
		if _, err := loadString(t, d); err == nil {
			t.Errorf("expected rejection for unsafe component path in:\n%s", d)
		}
	}
}

func TestLoadAllowsNestedAndRootComponentPaths(t *testing.T) {
	for _, p := range []string{".", "ios", "apps/ios-app"} {
		data := "[project]\nname=\"x\"\n\n[[component]]\npath=\"" + p + "\"\nprofiles=[\"ios\"]\n"
		if _, err := loadString(t, data); err != nil {
			t.Errorf("path %q should be valid: %v", p, err)
		}
	}
}

func TestLoadXcodeproj(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"q\"\nxcodeproj=\"ios/Queueify/Queueify.xcodeproj\"\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Xcodeproj != "ios/Queueify/Queueify.xcodeproj" {
		t.Errorf("xcodeproj = %q", cfg.Project.Xcodeproj)
	}
}

func TestLoadRejectsUnsafeXcodeproj(t *testing.T) {
	cases := []string{
		"[project]\nname=\"x\"\nxcodeproj=\"/abs/App.xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"../escape/App.xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/$(x).xcodeproj\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/App.xcodeproj; rm -rf\"\n",
		"[project]\nname=\"x\"\nxcodeproj=\"ios/App\"\n",
	}
	for _, d := range cases {
		if _, err := loadString(t, d); err == nil {
			t.Errorf("expected rejection for xcodeproj in:\n%s", d)
		}
	}
}

func TestLoadAllowsBlankXcodeproj(t *testing.T) {
	if _, err := loadString(t, "[project]\nname=\"x\"\n"); err != nil {
		t.Errorf("blank xcodeproj must be allowed: %v", err)
	}
}

func TestLoadRejectsUnsafeProjectName(t *testing.T) {
	for _, n := range []string{`--public`, `a;rm -rf`, `a$(x)`, "a\nb", `-x`} {
		data := "[project]\nname=\"" + n + "\"\n"
		if _, err := loadString(t, data); err == nil {
			t.Errorf("expected rejection for project name %q", n)
		}
	}
	if _, err := loadString(t, "[project]\nname=\"ShelfLife\"\n"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}

func TestLoadSwiftVersion(t *testing.T) {
	cfg, err := loadString(t, "[project]\nname=\"x\"\nswift_version=\"6.2\"\n")
	if err != nil || cfg.Project.SwiftVersion != "6.2" {
		t.Fatalf("swift_version=%q err=%v", cfg.Project.SwiftVersion, err)
	}
	if _, err := loadString(t, "[project]\nname=\"x\"\nswift_version=\"6\"\n"); err != nil {
		t.Errorf("single-component version should be allowed: %v", err)
	}
	for _, v := range []string{"6.2; rm", "6.x", "$(id)", "6.2 "} {
		if _, err := loadString(t, "[project]\nname=\"x\"\nswift_version=\""+v+"\"\n"); err == nil {
			t.Errorf("expected rejection for swift_version %q", v)
		}
	}
}
