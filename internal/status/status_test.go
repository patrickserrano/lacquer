package status

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRowsReportBehindAndUpToDate(t *testing.T) {
	harness := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(harness, "VERSION"), "5\n")

	writeFile(t, filepath.Join(project, ".harness.toml"),
		"[project]\nname=\"rail\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	// core stamped at v5 (current), ios stamped at v3 (behind).
	writeFile(t, filepath.Join(project, "CLAUDE.md"),
		"<!-- harness:core:start v5 -->\nx\n<!-- harness:core:end -->\n")
	writeFile(t, filepath.Join(project, "ios", "CLAUDE.md"),
		"<!-- harness:ios:start v3 -->\nx\n<!-- harness:ios:end -->\n")

	rows, err := Rows(harness, project)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// rows[0] = core, rows[1] = ios
	if rows[0].Key != "core" || rows[0].Stamped != 5 || rows[0].Behind {
		t.Errorf("core row = %+v, want stamped=5 behind=false", rows[0])
	}
	if rows[1].Key != "ios" || rows[1].Stamped != 3 || !rows[1].Behind {
		t.Errorf("ios row = %+v, want stamped=3 behind=true", rows[1])
	}
}
