package console

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kill stops one recorded session and removes it from the sessions file. A
// bg record's own worktree (Record.Worktree) is never removed; its path is
// returned in the note instead.
//
// Refuses on an Alive record unless force is set: this exists because a batch
// of bg dispatches sat Blocked with no live process at all (a bg daemon holds
// no OS process while blocked -- it respawns on resume, so there was nothing
// to send a signal to), and the fix for THAT is state cleanup, not a kill
// syscall. But Kill must still not be the easy way to discard a session that
// is genuinely working just because it is inconvenient right now -- that is
// what force is for, an explicit override, not a default.
func Kill(sessionsPath string, r Record, force bool) (string, error) {
	status, _, checkErr := r.Check()
	if status == Alive && checkErr == nil && !force {
		return "", fmt.Errorf("%s is alive — pass force to kill it anyway", r.Name)
	}

	var note string
	switch r.Mode {
	case Tmux:
		// By the session's own id, resolved from its exact name: `-t <name>`
		// prefix-matches, and killed probe-long when asked to kill probe.
		id, name, found, err := findTmuxSession(r.tmuxNames())
		switch {
		case err != nil:
			note = fmt.Sprintf("could not look up the tmux session: %v", err)
		case !found:
			note = "no tmux session named " + strings.Join(r.tmuxNames(), " or ") + " (already gone)"
		default:
			if err := exec.Command("tmux", "kill-session", "-t", id).Run(); err != nil {
				note = fmt.Sprintf("tmux kill-session %s (%s) reported: %v", name, id, err)
			}
		}
	case Background:
		if r.DaemonID != "" {
			var notes []string
			dir, err := claudeJobsDir()
			if err == nil {
				jobDir := filepath.Join(dir, r.DaemonID)
				// worktreePath comes from the job's OWN state file, read
				// before removing that file -- the only record of a worktree
				// Claude Code's bg machinery made and named itself.
				//
				// This is the fix for a real incident: an earlier version of
				// Kill removed only the job directory, leaving the git
				// worktree behind, still git-worktree-locked. Every
				// subsequent redispatch to the same project hit that stale
				// lock and got stuck asking how to proceed -- 7 of 7
				// redispatched sessions, all blocked on the exact thing this
				// function was supposed to have cleaned up.
				//
				// Never when that path is lacquer's own recorded worktree or
				// inside it: a bg session now runs in the worktree dispatch
				// made, so its state file can name that very directory, and
				// force-removing it would delete the uncommitted work that
				// lacquer never removes (see worktree.go). Kept for records
				// that predate lacquer-made worktrees (no r.Worktree), which
				// are the ones that incident was about.
				if st, jsErr := readJobState(filepath.Join(jobDir, "state.json")); jsErr == nil && st.WorktreePath != "" &&
					(r.Worktree == "" || !within(st.WorktreePath, r.Worktree)) {
					if wtErr := removeWorktree(r.Dir, st.WorktreePath); wtErr != nil {
						notes = append(notes, fmt.Sprintf("could not remove worktree %s: %v", st.WorktreePath, wtErr))
					}
				}
				// Best-effort: a bg daemon holds no process while blocked, so
				// there is nothing to signal. Removing its own state directory
				// is what actually stops it from being respawned later.
				if rmErr := os.RemoveAll(jobDir); rmErr != nil {
					notes = append(notes, fmt.Sprintf("could not remove job directory %s: %v", jobDir, rmErr))
				}
			}
			note = strings.Join(notes, "; ")
		}
		if r.Worktree != "" {
			kept := fmt.Sprintf("kept worktree %s (branch %s): remove it with `git -C %s worktree remove %s` once nothing in it is needed", r.Worktree, r.Branch, r.Dir, r.Worktree)
			if note != "" {
				note += "; "
			}
			note += kept
		}
	}

	if err := RemoveRecord(sessionsPath, r); err != nil {
		return note, fmt.Errorf("kill %s: remove from sessions file: %w", r.Name, err)
	}
	return note, nil
}

// removeWorktree unlocks and force-removes the git worktree at path, whose
// parent repository is mainDir (r.Dir -- the project's own checkout, where
// `git worktree` commands must run from since the worktree itself may be
// mid-removal). A dispatch's worktree is created via `git worktree lock`
// (Claude Code's own bg machinery, not lacquer's), so plain `git worktree
// remove` refuses it with "is locked" -- unlock first, then remove --force
// since the killed session left no clean working tree to verify.
func removeWorktree(mainDir, path string) error {
	lock, err := lockWorktrees(mainDir)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Best-effort: an already-unlocked worktree errors here, which is fine --
	// the remove below is what actually matters.
	_ = exec.Command("git", "-C", mainDir, "worktree", "unlock", path).Run()
	if err := exec.Command("git", "-C", mainDir, "worktree", "remove", "--force", path).Run(); err != nil {
		return fmt.Errorf("git worktree remove --force %s: %w", path, err)
	}
	return nil
}

// RemoveRecord drops the first record in the sessions file that exactly
// matches target, rewriting the file without it. Record has no fields beyond
// plain strings, ints and time.Time, so struct equality is a reliable enough
// identity check -- two independent dispatches never share every field
// (StartedAt alone already distinguishes them).
func RemoveRecord(path string, target Record) error {
	return rewriteRecord(path, target, nil)
}

// ReplaceRecord swaps the first record that exactly matches target for
// replacement, in place, so the file's order (its history) is kept. Watch
// uses it to put a relaunched session where the dead one was.
func ReplaceRecord(path string, target, replacement Record) error {
	return rewriteRecord(path, target, &replacement)
}

// rewriteRecord rewrites the sessions file with target removed, or replaced
// when replacement is non-nil. Atomic: written to a temp file, then renamed
// over the original, so an interrupted rewrite never truncates the history.
func rewriteRecord(path string, target Record, replacement *Record) error {
	records, err := ReadRecords(path)
	if err != nil {
		return err
	}
	found := false
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if !found && r == target {
			found = true
			if replacement != nil {
				out = append(out, *replacement)
			}
			continue
		}
		out = append(out, r)
	}
	if !found {
		return fmt.Errorf("no matching record found in %s", path)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("write sessions file: %w", err)
	}
	for _, r := range out {
		if err := writeRecordLine(f, r); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
