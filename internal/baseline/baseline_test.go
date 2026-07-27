package baseline

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSpec puts a baseline.toml at profiles/<profile>/baseline.toml under a
// throwaway lacquer root and returns that root.
func writeSpec(t *testing.T, profile, body string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "profiles", profile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "baseline.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadSpec(t *testing.T) {
	root := writeSpec(t, "ios", `
[baseline]
swift_version      = "6"
warnings_as_errors = true
strict_concurrency = "complete"
`)
	spec, ok, err := LoadSpec(root, "ios")
	if err != nil {
		t.Fatalf("LoadSpec: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true for a profile that ships a baseline")
	}
	if spec.SwiftVersion != "6" {
		t.Errorf("SwiftVersion = %q, want 6", spec.SwiftVersion)
	}
	if !spec.WarningsAsErrors {
		t.Error("WarningsAsErrors = false, want true")
	}
	if spec.StrictConcurrency != "complete" {
		t.Errorf("StrictConcurrency = %q, want complete", spec.StrictConcurrency)
	}
}

// A profile with no baseline.toml (web, supabase) must report absence rather
// than erroring — those profiles run no baseline checks at all.
func TestLoadSpecAbsentProfile(t *testing.T) {
	root := writeSpec(t, "ios", "[baseline]\nswift_version = \"6\"\n")
	_, ok, err := LoadSpec(root, "web")
	if err != nil {
		t.Fatalf("LoadSpec for a profile with no baseline: %v", err)
	}
	if ok {
		t.Error("ok = true, want false when the profile ships no baseline.toml")
	}
}

func TestLoadSpecMalformed(t *testing.T) {
	root := writeSpec(t, "ios", "[baseline]\nswift_version = = broken\n")
	if _, _, err := LoadSpec(root, "ios"); err == nil {
		t.Fatal("LoadSpec on malformed TOML: want error, got nil")
	}
}
