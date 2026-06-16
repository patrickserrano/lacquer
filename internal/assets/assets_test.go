package assets

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/patrickserrano/harness/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlan(t *testing.T) {
	h := t.TempDir()
	// core assets
	write(t, filepath.Join(h, "core", "skills", "git.md"), "x")
	write(t, filepath.Join(h, "core", "commands", "sync-worktree.md"), "x")
	// ios profile assets
	write(t, filepath.Join(h, "profiles", "ios", "skills", "build.md"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "workflows", "ci.yml"), "x")
	write(t, filepath.Join(h, "profiles", "ios", "config", ".swiftlint.yml"), "x")

	cfg := &config.Config{
		Components: []config.Component{
			{Path: "ios", Profiles: []string{"ios"}},
		},
	}

	got, err := Plan(h, cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Map dest -> src for assertion.
	dests := map[string]string{}
	for _, a := range got {
		dests[a.Dest] = a.Src
	}
	want := []string{
		filepath.Join(".claude", "skills", "git.md"),
		filepath.Join(".claude", "commands", "sync-worktree.md"),
		filepath.Join(".claude", "skills", "build.md"),
		filepath.Join(".github", "workflows", "ios-ci.yml"),
		filepath.Join("ios", ".swiftlint.yml"),
	}
	var gotDests []string
	for d := range dests {
		gotDests = append(gotDests, d)
	}
	sort.Strings(want)
	sort.Strings(gotDests)
	if len(gotDests) != len(want) {
		t.Fatalf("got %d assets %v, want %d %v", len(gotDests), gotDests, len(want), want)
	}
	for i := range want {
		if gotDests[i] != want[i] {
			t.Errorf("dest[%d] = %q, want %q", i, gotDests[i], want[i])
		}
	}
	// Source paths must be absolute and exist.
	for _, a := range got {
		if !filepath.IsAbs(a.Src) {
			t.Errorf("src not absolute: %q", a.Src)
		}
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestCopyWritesAssets(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "core", "skills", "git.md"), "SKILL BODY")

	cfg := &config.Config{}
	plan, err := Plan(h, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "git.md"))
	if err != nil {
		t.Fatalf("asset not written: %v", err)
	}
	if string(got) != "SKILL BODY" {
		t.Errorf("content = %q", got)
	}
}

func TestCopyRefusesDirtyTarget(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	write(t, filepath.Join(h, "core", "commands", "build.md"), "NEW")

	// Pre-create the destination with a local edit and commit, then dirty it.
	dest := filepath.Join(project, ".claude", "commands", "build.md")
	write(t, dest, "committed\n")
	gitInit(t, project)
	cmd := exec.Command("git", "commit", "-qm", "init")
	cmd.Dir = project
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	write(t, dest, "LOCAL UNSAVED EDIT\n") // now dirty

	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = Copy(project, plan)
	if err == nil {
		t.Fatal("expected Copy to refuse dirty target, got nil")
	}
	if !strings.Contains(err.Error(), "build.md") {
		t.Errorf("error should name the dirty file: %v", err)
	}
	// The local edit must be preserved.
	got, _ := os.ReadFile(dest)
	if string(got) != "LOCAL UNSAVED EDIT\n" {
		t.Errorf("dirty target was overwritten: %q", got)
	}
}

func TestCopyRefusesNonGitProject(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir() // deliberately NOT git init'd
	write(t, filepath.Join(h, "core", "skills", "git.md"), "BODY")
	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan); err == nil {
		t.Fatal("expected Copy to refuse a non-git project (fail-closed), got nil")
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "git.md")); err == nil {
		t.Error("asset was written to a non-git project despite guard")
	}
}

func TestCopyRefusesSymlinkedDestDir(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "core", "skills", "git.md"), "BODY")
	// .claude is a symlink pointing outside the project root.
	if err := os.Symlink(outside, filepath.Join(project, ".claude")); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(h, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Copy(project, plan); err == nil {
		t.Fatal("expected Copy to refuse writing through symlinked .claude dir")
	}
	if _, err := os.Stat(filepath.Join(outside, "skills", "git.md")); err == nil {
		t.Error("asset escaped the project root via symlinked .claude")
	}
}

// TestCopyAllOrNothingOnConfinementViolation proves the preflight is all-or-
// nothing for deterministic safety checks: a confinement violation on a LATER
// asset must abort before any EARLIER asset is written.
func TestCopyAllOrNothingOnConfinementViolation(t *testing.T) {
	h := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()
	gitInit(t, project)
	write(t, filepath.Join(h, "good.md"), "GOOD")
	// A symlinked dir escaping the project root, used by the second asset.
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	plan := []Asset{
		{Src: filepath.Join(h, "good.md"), Dest: filepath.Join(".claude", "skills", "a.md")},
		{Src: filepath.Join(h, "good.md"), Dest: filepath.Join("escape", "CLAUDE.md")},
	}
	if err := Copy(project, plan); err == nil {
		t.Fatal("expected a confinement violation error, got nil")
	}
	// The earlier, safe asset must NOT have been written.
	if _, err := os.Stat(filepath.Join(project, ".claude", "skills", "a.md")); err == nil {
		t.Error("earlier asset was written despite a later confinement violation (partial sync)")
	}
}
