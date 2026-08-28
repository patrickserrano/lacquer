package console

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Kill stops one recorded session and removes it from the sessions file.
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
		if err := exec.Command("tmux", "kill-session", "-t", r.Name).Run(); err != nil {
			note = fmt.Sprintf("tmux kill-session reported: %v (session may already be gone)", err)
		}
	case Background:
		if r.DaemonID != "" {
			dir, err := claudeJobsDir()
			if err == nil {
				jobDir := filepath.Join(dir, r.DaemonID)
				// Best-effort: a bg daemon holds no process while blocked, so
				// there is nothing to signal. Removing its own state directory
				// is what actually stops it from being respawned later.
				if rmErr := os.RemoveAll(jobDir); rmErr != nil {
					note = fmt.Sprintf("could not remove job directory %s: %v", jobDir, rmErr)
				}
			}
		}
	}

	if err := RemoveRecord(sessionsPath, r); err != nil {
		return note, fmt.Errorf("kill %s: remove from sessions file: %w", r.Name, err)
	}
	return note, nil
}

// RemoveRecord drops the first record in the sessions file that exactly
// matches target, rewriting the file without it. Record has no fields beyond
// plain strings/time.Time, so struct equality is a reliable enough identity
// check -- two independent dispatches never share every field (StartedAt
// alone already distinguishes them).
func RemoveRecord(path string, target Record) error {
	records, err := ReadRecords(path)
	if err != nil {
		return err
	}
	found := false
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if !found && r == target {
			found = true
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
