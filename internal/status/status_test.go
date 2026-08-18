package status

import (
	"github.com/patrickserrano/lacquer/internal/version"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v builds the Version a legacy counter N embeds to (0.N.0), which is what the
// fixtures below mean when they say "version 5".
func v(minor int) version.Version { return version.Version{Minor: minor} }

func TestFormatRendersStatuses(t *testing.T) {
	rows := []Row{
		{Key: "core", Path: "CLAUDE.md", Stamped: v(5), Found: true, Latest: v(5), Behind: false},
		{Key: "ios", Path: "ios/CLAUDE.md", Stamped: v(3), Found: true, Latest: v(5), Behind: true},
		{Key: "web", Path: "web/CLAUDE.md", Found: false, Latest: v(5), Behind: true},
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
	// core, ios, and the .gitignore region.
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0].Key != "core" || rows[0].Stamped != v(5) || rows[0].Behind {
		t.Errorf("core row = %+v, want stamped=5 behind=false", rows[0])
	}
	if rows[1].Key != "ios" || rows[1].Stamped != v(3) || !rows[1].Behind {
		t.Errorf("ios row = %+v, want stamped=3 behind=true", rows[1])
	}
	// This project has no .gitignore at all, which is the state the whole fleet
	// was in. It must read as missing-and-behind rather than being left out of
	// the report entirely.
	if rows[2].Key != "gitignore" || rows[2].Path != ".gitignore" || rows[2].Found || !rows[2].Behind {
		t.Errorf("gitignore row = %+v, want path=.gitignore found=false behind=true", rows[2])
	}
}

// A .gitignore region is stamped with `#` comments, and the status reader has to
// use that syntax to see it. Reading it as markdown finds nothing, which reports
// a synced project as never-synced — the exact false alarm that teaches people
// to ignore `lacquer status`.
func TestRowsReadTheHashStampedGitignoreRegion(t *testing.T) {
	lacquer := t.TempDir()
	project := t.TempDir()
	writeFile(t, filepath.Join(lacquer, "VERSION"), "5\n")
	writeFile(t, filepath.Join(project, ".lacquer.toml"), "[project]\nname=\"acme\"\n")
	writeFile(t, filepath.Join(project, ".gitignore"),
		"DerivedData/\n\n# lacquer:gitignore:start v5\n*.p8\n# lacquer:gitignore:end\n")

	rows, err := Rows(lacquer, project)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	last := rows[len(rows)-1]
	if last.Key != "gitignore" || !last.Found || last.Stamped != v(5) || last.Behind {
		t.Errorf("gitignore row = %+v, want found=true stamped=5 behind=false", last)
	}
}
