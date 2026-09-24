package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBiomeIgnoresRejectNonIgnorePatterns(t *testing.T) {
	for _, pattern := range []string{"", "**/generated.ts", "!", "!!", "!\n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".lacquer.toml")
		// A literal multiline string exercises newline rejection as well.
		if err := os.WriteFile(path, []byte("[project]\nname='demo'\n[web]\nbiome_ignores=['''"+pattern+"''']\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("accepted %q", pattern)
		}
	}
}
