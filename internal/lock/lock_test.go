package lock

import (
	"github.com/patrickserrano/lacquer/internal/version"
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
	in := &Lock{Version: version.Version{Minor: 7}, Files: map[string]string{"CLAUDE.md#core": Hash("body")}}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok, err := Read(dir)
	if err != nil || !ok {
		t.Fatalf("Read: ok=%v err=%v", ok, err)
	}
	if got.Version != (version.Version{Minor: 7}) || got.Files["CLAUDE.md#core"] != Hash("body") {
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
	if err := Write(dir, &Lock{Version: version.Version{Minor: 1}, Files: map[string]string{}}); err == nil {
		t.Fatal("expected Write to refuse a symlinked lockfile")
	}
	if got, _ := os.ReadFile(outside); string(got) != "ORIGINAL" {
		t.Errorf("symlink target was clobbered: %q", got)
	}
}

// The downgrade record (#576): parsed when present, ignored by nothing, and a
// reason counts only for leaving the version it names.
func TestDowngradeReasonCountsOnlyForTheVersionItNames(t *testing.T) {
	base, err := Parse([]byte(`{"version": "1.78.1", "files": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	head, err := Parse([]byte(`{"version": "1.77.0", "files": {}, "downgrade": {"from": "1.78.1", "reason": " broken release "}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !IsDowngrade(base, head) || IsDowngrade(head, base) || IsDowngrade(base, base) || IsDowngrade(nil, head) || IsDowngrade(base, nil) {
		t.Error("IsDowngrade misorders")
	}
	if got := DowngradeReason(base, head); got != "broken release" {
		t.Errorf("reason %q", got)
	}
	head.Downgrade.From = "1.79.0"
	if got := DowngradeReason(base, head); got != "" {
		t.Errorf("a reason for leaving 1.79.0 excused leaving 1.78.1: %q", got)
	}
}
