package pluginbootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubTool makes the locating tool print path, or fail.
func stubTool(t *testing.T, path string, err error) {
	t.Helper()
	prev := ToolRunner
	ToolRunner = func(string, ...string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		// Real output carries progress lines before the path.
		return []byte("Launching Xcode...\n" + path + "\n"), nil
	}
	t.Cleanup(func() { ToolRunner = prev })
}

func xcodePlugin(t *testing.T, build string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), build, "claude")
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

var claudeOnly = []Provided{{Name: "xcode-integration", Provider: "xcode", Format: "claude"}}

func TestProvidedPluginIsLinkedWhenAbsent(t *testing.T) {
	home, want := t.TempDir(), xcodePlugin(t, "27A266a")
	stubTool(t, want, nil)

	got := ApplyProvided(home, claudeOnly)
	if len(got) != 1 || got[0].Action != "linked" {
		t.Fatalf("want linked, got %+v", got)
	}
	link := filepath.Join(home, ".claude/skills/xcode-integration")
	if dest, _ := os.Readlink(link); dest != want {
		t.Errorf("link points at %q, want %q", dest, want)
	}
}

// The reason this is re-applied rather than done once. An Xcode upgrade moves
// the build-versioned path, the old link stops resolving, and the plugin
// silently stops loading — fifteen skills and an MCP server absent with nothing
// red to notice.
func TestAnUpgradedToolRepointsTheLink(t *testing.T) {
	home := t.TempDir()
	old := xcodePlugin(t, "27A266a")
	stubTool(t, old, nil)
	ApplyProvided(home, claudeOnly)

	next := xcodePlugin(t, "27B100z")
	stubTool(t, next, nil)
	got := ApplyProvided(home, claudeOnly)

	if len(got) != 1 || got[0].Action != "relinked" {
		t.Fatalf("want relinked after the tool moved, got %+v", got)
	}
	link := filepath.Join(home, ".claude/skills/xcode-integration")
	if dest, _ := os.Readlink(link); dest != next {
		t.Errorf("link still points at %q, want the new %q — the plugin would stay dark", dest, next)
	}
}

func TestAnUnchangedToolIsANoOp(t *testing.T) {
	home, want := t.TempDir(), xcodePlugin(t, "27A266a")
	stubTool(t, want, nil)
	ApplyProvided(home, claudeOnly)
	got := ApplyProvided(home, claudeOnly)
	if len(got) != 1 || got[0].Action != "current" {
		t.Fatalf("want current on a second run, got %+v", got)
	}
}

// A machine without Xcode is legitimate. Reporting it as done, or saying
// nothing, is how a bootstrap step starts lying about what it set up.
func TestAnAbsentToolIsReportedNotSkipped(t *testing.T) {
	home := t.TempDir()
	stubTool(t, "", errors.New("xcrun: unable to find utility"))
	got := ApplyProvided(home, claudeOnly)
	if len(got) != 1 || got[0].Action != "unavailable" {
		t.Fatalf("want unavailable, got %+v", got)
	}
	if got[0].Ok() {
		t.Error("an unavailable plugin reported itself as fine")
	}
}

// The tool answering with a path that is not there is NOT the same as the tool
// being absent, and neither is a pass.
func TestAPathThatDoesNotExistIsNotLinked(t *testing.T) {
	home := t.TempDir()
	stubTool(t, filepath.Join(t.TempDir(), "gone"), nil)
	got := ApplyProvided(home, claudeOnly)
	if len(got) != 1 || got[0].Action != "unavailable" {
		t.Fatalf("want unavailable for a missing path, got %+v", got)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills/xcode-integration")); err == nil {
		t.Error("a link was created to a path that does not exist")
	}
}

// The worst thing this file could do. A real directory there is somebody's own
// plugin or a deliberate copy; replacing it to install ours would delete work.
func TestARealDirectoryIsNeverReplaced(t *testing.T) {
	home, want := t.TempDir(), xcodePlugin(t, "27A266a")
	mine := filepath.Join(home, ".claude/skills/xcode-integration")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(mine, "MY-WORK.md")
	if err := os.WriteFile(keep, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	stubTool(t, want, nil)

	got := ApplyProvided(home, claudeOnly)
	if len(got) != 1 || got[0].Action != "refused" {
		t.Fatalf("want refused, got %+v", got)
	}
	// Assert the REASON, not just the outcome. Without the symlink check this
	// still refused — because os.Remove cannot delete a non-empty directory —
	// so the outcome alone passes against an implementation with no guard at
	// all, and the empty-directory case below then deletes real work.
	if !strings.Contains(got[0].Details, "not a symlink") {
		t.Errorf("refused for the wrong reason (%q); the guard is not what stopped it", got[0].Details)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a real directory was replaced and its contents lost: %v", err)
	}
}

// The case the outcome-only test missed. An EMPTY directory is removable, so
// without the symlink check it is silently deleted and replaced — and an empty
// directory at that path is still somebody's, just not populated yet.
func TestAnEmptyRealDirectoryIsAlsoNeverReplaced(t *testing.T) {
	home, want := t.TempDir(), xcodePlugin(t, "27A266a")
	mine := filepath.Join(home, ".claude/skills/xcode-integration")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	stubTool(t, want, nil)

	got := ApplyProvided(home, claudeOnly)
	if len(got) != 1 || got[0].Action != "refused" {
		t.Fatalf("want refused for an empty real directory, got %+v", got)
	}
	fi, err := os.Lstat(mine)
	if err != nil {
		t.Fatalf("the directory was removed: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("a real directory was replaced by a symlink")
	}
}

func TestAnUnknownProviderOrFormatIsRefused(t *testing.T) {
	home := t.TempDir()
	for _, p := range []Provided{
		{Name: "x", Provider: "nope", Format: "claude"},
		{Name: "x", Provider: "xcode", Format: "emacs"},
	} {
		got := ApplyProvided(home, []Provided{p})
		if len(got) != 1 || got[0].Action != "refused" {
			t.Errorf("%+v: want refused, got %+v", p, got)
		}
	}
}
