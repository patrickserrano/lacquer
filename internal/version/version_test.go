package version

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v != 7 {
		t.Errorf("version = %d, want 7", v)
	}
}
