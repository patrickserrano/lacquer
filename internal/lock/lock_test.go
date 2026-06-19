package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMissingIsNotError(t *testing.T) {
	_, ok, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if ok {
		t.Error("expected ok=false for a missing lockfile")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Lock{Version: 7, Files: map[string]string{"CLAUDE.md#core": Hash("body")}}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok, err := Read(dir)
	if err != nil || !ok {
		t.Fatalf("Read: ok=%v err=%v", ok, err)
	}
	if got.Version != 7 || got.Files["CLAUDE.md#core"] != Hash("body") {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestHashStable(t *testing.T) {
	if Hash("x") != Hash("x") {
		t.Error("Hash not deterministic")
	}
	if Hash("x") == Hash("y") {
		t.Error("Hash collision on distinct input")
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, Name)); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, &Lock{Version: 1, Files: map[string]string{}}); err == nil {
		t.Fatal("expected Write to refuse a symlinked lockfile")
	}
	if got, _ := os.ReadFile(outside); string(got) != "ORIGINAL" {
		t.Errorf("symlink target was clobbered: %q", got)
	}
}
