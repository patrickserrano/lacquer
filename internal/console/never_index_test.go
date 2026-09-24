package console

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeExcludedBeforeLaunch(t *testing.T) {
	for _, kind := range []string{"new", "assigned", "resumed"} {
		t.Run(kind, func(t *testing.T) {
			repo := realPath(t, t.TempDir())
			initGitRepo(t, repo)
			global := filepath.Join(t.TempDir(), "ignore")
			config := filepath.Join(t.TempDir(), "gitconfig")
			t.Setenv("GIT_CONFIG_GLOBAL", config)
			git(t, repo, "config", "--global", "core.excludesFile", global)
			sp := launchSpec{verb: "dispatch", name: "alpha", dir: repo, task: "task", mode: Background}
			if kind != "new" {
				wt := filepath.Join(t.TempDir(), "linked")
				git(t, repo, "worktree", "add", "-qb", "unit", wt)
				if kind == "assigned" {
					sp.place.Worktree = wt
				} else {
					sp.resume = wt
				}
			}
			bin := t.TempDir()
			script := `#!/bin/sh
 test -f .metadata_never_index || { echo 'marker missing before launch'; exit 21; }
 test -z "$(git status --porcelain)" || { echo 'marker visible in git status'; exit 22; }
 echo 'backgrounded · 1234abcd'
`
			if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			launch, err := runDispatch(sp)
			if err != nil {
				t.Fatalf("launch: %v\n%s", err, launch.Output)
			}
			data, err := os.ReadFile(global)
			if err != nil || !strings.Contains(string(data), ".metadata_never_index") {
				t.Fatalf("global excludes = %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(launch.Record.Worktree, ".gitignore")); !os.IsNotExist(err) {
				t.Fatalf("unexpected project .gitignore: %v", err)
			}
		})
	}
}

func TestWorktreeCreationWritesSpotlightMarker(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	wt, err := createWorktree(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(wt.path, neverIndex)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("marker at creation: %v, %v", info, err)
	}
}

func TestSpotlightPreparationPreservesFilesAndRefusesOverrides(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	global := filepath.Join(t.TempDir(), "ignore")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "config"))
	git(t, repo, "config", "--global", "core.excludesFile", global)
	if err := os.WriteFile(global, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, neverIndex), []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := prepareWorktree(repo); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(global)
	if err != nil || !strings.HasPrefix(string(data), "keep-me\n") || strings.Count(string(data), neverIndex) != 1 {
		t.Fatalf("global excludes = %q, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(repo, neverIndex))
	if err != nil || string(data) != "preserve" {
		t.Fatalf("existing marker = %q, %v", data, err)
	}
	git(t, repo, "config", "core.excludesFile", os.DevNull)
	if err := prepareWorktree(repo); err == nil {
		t.Fatal("accepted a repository override that exposes the marker")
	}
}

func TestSpotlightDefaultGlobalExcludes(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := prepareWorktree(repo); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(xdg, "git", "ignore"))
	if err != nil || !strings.Contains(string(data), neverIndex) {
		t.Fatalf("default global excludes = %q, %v", data, err)
	}
}

func TestSpotlightDryRunDoesNotWrite(t *testing.T) {
	repo := realPath(t, t.TempDir())
	initGitRepo(t, repo)
	wt := filepath.Join(t.TempDir(), "linked")
	git(t, repo, "worktree", "add", "-qb", "unit", wt)
	global := filepath.Join(t.TempDir(), "ignore")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "config"))
	git(t, repo, "config", "--global", "core.excludesFile", global)
	for _, mode := range []Mode{Background, Tmux} {
		for _, place := range []Placement{{}, {Worktree: wt}} {
			if _, err := runDispatch(launchSpec{dir: repo, name: "alpha", task: "task", mode: mode, place: place, dryRun: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, path := range []string{global, filepath.Join(wt, neverIndex), filepath.Join(repo, neverIndex)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry run wrote %s: %v", path, err)
		}
	}
}

func TestTmuxWorktreeExcludedBeforeLaunch(t *testing.T) {
	for _, assigned := range []bool{true, false} {
		t.Run(fmt.Sprint(assigned), func(t *testing.T) {
			repo := realPath(t, t.TempDir())
			initGitRepo(t, repo)
			wt := filepath.Join(t.TempDir(), "linked")
			git(t, repo, "worktree", "add", "-qb", "unit", wt)
			sp := launchSpec{verb: "dispatch", dir: repo, name: "alpha", task: "task", mode: Tmux}
			if assigned {
				sp.place.Worktree = wt
			} else {
				sp.dir = wt
			}
			// Exercise the launch boundary without opening a server: the fake tmux
			// rejects new-session unless the marker already exists and is ignored.
			bin := t.TempDir()
			script := `#!/bin/sh
 case "$1" in
 list-sessions) echo 'no server running' >&2; exit 1 ;;
 new-session) cd "$6" || exit 20
 test -f .metadata_never_index || exit 21
 git check-ignore -q .metadata_never_index || exit 22 ;;
 *) exit 23 ;;
 esac
`
			if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if launch, err := runDispatch(sp); err != nil {
				t.Fatalf("tmux launch: %v\n%s", err, launch.Output)
			}
		})
	}
}

func TestSpotlightFailurePreventsLaunch(t *testing.T) {
	for _, obstruction := range []string{"marker-directory", "marker-symlink", "global-directory"} {
		t.Run(obstruction, func(t *testing.T) {
			repo := realPath(t, t.TempDir())
			initGitRepo(t, repo)
			wt := filepath.Join(t.TempDir(), "linked")
			git(t, repo, "worktree", "add", "-qb", "unit", wt)
			global := filepath.Join(t.TempDir(), "ignore")
			t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "config"))
			git(t, repo, "config", "--global", "core.excludesFile", global)
			marker := filepath.Join(wt, neverIndex)
			switch obstruction {
			case "marker-directory":
				if err := os.Mkdir(marker, 0o755); err != nil {
					t.Fatal(err)
				}
			case "marker-symlink":
				if err := os.Symlink(filepath.Join(wt, "f.txt"), marker); err != nil {
					t.Fatal(err)
				}
			case "global-directory":
				if err := os.Mkdir(global, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			calls := fakeClaude(t)
			launch, err := runDispatch(launchSpec{verb: "dispatch", dir: repo, name: "alpha", task: "task", mode: Background, place: Placement{Worktree: wt}})
			if err == nil || launch.Record == nil || launch.Record.LaunchError == "" {
				t.Fatalf("failure not recorded: %+v, %v", launch, err)
			}
			if got := claudeCalls(t, calls); len(got) != 0 {
				t.Fatalf("launched despite failed preparation: %+v", got)
			}
			data, err := os.ReadFile(filepath.Join(wt, "f.txt"))
			if err != nil || string(data) != "hello\n" {
				t.Fatalf("source changed: %q, %v", data, err)
			}
		})
	}
}
