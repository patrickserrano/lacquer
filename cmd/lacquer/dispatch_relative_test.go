package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patrickserrano/lacquer/internal/gittest"
)

// Every lead dispatches from the fleet-ops directory with a relative roster:
//
//	lacquer console --roster fleet.toml --mode bg dispatch <project> "<task>"
//
// where fleet.toml lists each project as `path = "../<project>"`. From 1.37.3
// (bg dispatch makes a worktree) until this test, that failed with "can't
// make ../proj relative to /.../proj" and launched nothing: the roster
// resolved entry paths against filepath.Dir("fleet.toml") == ".", so the
// project path stayed relative, and the worktree code took filepath.Rel of
// git's absolute toplevel against it. An absolute --roster worked, which is
// why the relative form -- the one actually in use -- was never exercised.
//
// relativeFleet builds that layout: <tmp>/fleet-ops/{fleet.toml,roles.toml}
// and <tmp>/proj, a real git repository, then makes fleet-ops the process
// cwd. It returns the tmp directory as the process sees it (possibly through
// a symlink) and the project's fully resolved root, which is what git
// reports and so what the worktree plan is rooted at.
func relativeFleet(t *testing.T, cwdViaSymlink bool) (tmp, projReal string) {
	t.Helper()
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp = real
	if cwdViaSymlink {
		// Reach the same directory through a symlink, the way macOS's /var
		// reaches /private/var: the process's idea of its cwd (and so every
		// filepath.Abs) then disagrees with the resolved path git reports.
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		tmp = link
	}
	proj := filepath.Join(real, "proj")
	fleetOps := filepath.Join(real, "fleet-ops")
	for _, d := range []string{proj, fleetOps} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Init(t, proj, "-q")

	roster := "[[project]]\nname = \"proj\"\npath = \"../proj\"\n"
	roles := "[[role]]\nname = \"pm-bg\"\nmode = \"bg\"\ntask = \"run the unit\"\ndir = \"../proj\"\n\n" +
		"[[role]]\nname = \"pm-tmux\"\ntask = \"run the unit\"\ndir = \"../proj\"\n"
	if err := os.WriteFile(filepath.Join(fleetOps, "fleet.toml"), []byte(roster), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fleetOps, "roles.toml"), []byte(roles), 0o644); err != nil {
		t.Fatal(err)
	}

	// A claude that reports no sessions. A dry run never launches one, but
	// dispatch lists live sessions first, and that must not reach the real
	// binary (or, if it did, a real daemon) from a test.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	chdir(t, filepath.Join(tmp, "fleet-ops"))
	// os.Getwd trusts $PWD when it names the cwd, as a shell's does.
	t.Setenv("PWD", filepath.Join(tmp, "fleet-ops"))
	return tmp, proj
}

func TestConsoleDispatchWithARelativeRoster(t *testing.T) {
	lq := realLacquer(t)
	for _, tt := range []struct {
		name    string
		symlink bool
		// args are the console args; "@" is replaced by the tmp directory as
		// the process sees it, for the absolute-path control.
		args []string
		// want is a line fragment of the dry run's plan; "%" is replaced by
		// the project's resolved root.
		want string
	}{
		{
			name: "bg, relative roster",
			args: []string{"--roster", "fleet.toml", "--mode", "bg", "--dry-run", "dispatch", "proj", "do the thing"},
			want: "worktree add --quiet --no-track -b dispatch/",
		},
		{
			name:    "bg, relative roster, cwd reached through a symlink",
			symlink: true,
			args:    []string{"--roster", "fleet.toml", "--mode", "bg", "--dry-run", "dispatch", "proj", "do the thing"},
			want:    "worktree add --quiet --no-track -b dispatch/",
		},
		{
			name: "bg, absolute roster (control)",
			args: []string{"--roster", "@/fleet-ops/fleet.toml", "--mode", "bg", "--dry-run", "dispatch", "proj", "do the thing"},
			want: "worktree add --quiet --no-track -b dispatch/",
		},
		{
			name: "tmux, relative roster",
			args: []string{"--roster", "fleet.toml", "--mode", "tmux", "--dry-run", "dispatch", "proj", "do the thing"},
			want: "tmux new-session -d -s proj -c %",
		},
		{
			name: "bg role, relative roles file",
			args: []string{"--roles", "roles.toml", "--dry-run", "dispatch-role", "pm-bg"},
			want: "worktree add --quiet --no-track -b dispatch/",
		},
		{
			name:    "bg role, relative roles file, cwd reached through a symlink",
			symlink: true,
			args:    []string{"--roles", "roles.toml", "--dry-run", "dispatch-role", "pm-bg"},
			want:    "worktree add --quiet --no-track -b dispatch/",
		},
		{
			name: "tmux role, relative roles file",
			args: []string{"--roles", "roles.toml", "--dry-run", "dispatch-role", "pm-tmux"},
			want: "tmux new-session -d -s pm-tmux -c %",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tmp, projReal := relativeFleet(t, tt.symlink)
			args := make([]string, len(tt.args))
			for i, a := range tt.args {
				args[i] = strings.ReplaceAll(a, "@", tmp)
			}
			out, errb, code := runConsole(t, lq, args)
			if code != 0 {
				t.Fatalf("dry run exited %d\nstdout:\n%s\nstderr:\n%s", code, out, errb)
			}
			if strings.Contains(out+errb, "relative to") {
				t.Errorf("dry run reported a path error:\n%s%s", out, errb)
			}
			if !strings.Contains(out, "(dry run — nothing started)") {
				t.Errorf("dry run did not finish its plan:\n%s", out)
			}

			if strings.Contains(tt.want, "%") {
				// tmux resolves a relative -c against its SERVER's cwd, not
				// the dispatcher's, so the directory must arrive absolute.
				// It is the project as the roster named it, through whatever
				// symlink the cwd was reached by.
				abs := filepath.Join(tmp, "proj")
				if want := strings.ReplaceAll(tt.want, "%", abs+" "); !strings.Contains(out, want) {
					t.Errorf("want %q in:\n%s", want, out)
				}
				return
			}

			// The worktree goes under the project's own root, which is where
			// git says it is: ../proj from fleet-ops, fully resolved.
			if want := "git -C " + projReal + " " + tt.want; !strings.Contains(out, want) {
				t.Errorf("want %q in:\n%s", want, out)
			}
			wtDir := filepath.Join(projReal, ".claude", "worktrees", "dispatch-")
			if !strings.Contains(out, " "+wtDir) {
				t.Errorf("worktree is not planned under %s:\n%s", wtDir, out)
			}
			if !strings.Contains(out, "(cd "+wtDir) {
				t.Errorf("session would not start in the planned worktree:\n%s", out)
			}
		})
	}
}
