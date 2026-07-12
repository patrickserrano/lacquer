package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatRendersStatuses(t *testing.T) {
	rows := []Row{
		{Key: "core", Path: "CLAUDE.md", Stamped: 5, Found: true, Latest: 5, Behind: false},
		{Key: "ios", Path: "ios/CLAUDE.md", Stamped: 3, Found: true, Latest: 5, Behind: true},
		{Key: "web", Path: "web/CLAUDE.md", Stamped: 0, Found: false, Latest: 5, Behind: true},
	}
	out := Format(rows)

	if !strings.HasPrefix(out, "LAYER") {
		t.Errorf("missing header row:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 { // header + 3 rows
		t.Fatalf("got %d lines, want 4:\n%s", len(lines), out)
	}
	// core = up to date (ok), ios = behind, web = missing (stamped shown as "-").
	if !strings.Contains(lines[1], "core") || !strings.HasSuffix(lines[1], "ok") {
		t.Errorf("core row not ok: %q", lines[1])
	}
	if !strings.Contains(lines[2], "ios") || !strings.HasSuffix(lines[2], "behind") {
		t.Errorf("ios row not behind: %q", lines[2])
	}
	if !strings.HasSuffix(lines[3], "missing") || !strings.Contains(lines[3], " - ") {
		t.Errorf("web row should be missing with '-' stamped: %q", lines[3])
	}
}

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
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "5\n")

	writeFile(t, filepath.Join(project, ".lacquer.toml"),
		"[project]\nname=\"acme\"\n\n[[component]]\npath=\"ios\"\nprofiles=[\"ios\"]\n")
	// core stamped at v5 (current), ios stamped at v3 (behind).
	writeFile(t, filepath.Join(project, "CLAUDE.md"),
		"<!-- lacquer:core:start v5 -->\nx\n<!-- lacquer:core:end -->\n")
	writeFile(t, filepath.Join(project, "ios", "CLAUDE.md"),
		"<!-- lacquer:ios:start v3 -->\nx\n<!-- lacquer:ios:end -->\n")

	rows, err := Rows(lacquer, project)
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
