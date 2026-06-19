// Package lock reads and writes .harness.lock, the per-project baseline that
// records the content the harness last wrote. It is the third point in the
// three-way comparison the audit uses to tell "the project edited this" apart
// from "the harness moved on" — without it, sync can only see that on-disk
// content differs from what it would write now, not who changed it.
package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Name is the lockfile's filename at the project root.
const Name = ".harness.lock"

// Lock is the recorded baseline: the harness version that wrote it, and a map
// from managed-unit key to the sha256 of the content the harness produced.
// Region keys are "<dest>#<regionKey>" (e.g. "CLAUDE.md#core"); asset keys are
// the destination path (e.g. ".github/workflows/ios-ci.yml").
type Lock struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

// Hash returns the hex sha256 of content. Used for every recorded baseline and
// every comparison, so identical content always yields an identical key.
func Hash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Read loads the lockfile at projectRoot. The bool is false (with a nil error)
// when no lockfile exists yet — a project synced before locking existed, where
// the audit degrades to a two-way comparison.
func Read(projectRoot string) (*Lock, bool, error) {
	data, err := os.ReadFile(filepath.Join(projectRoot, Name))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, false, err
	}
	if l.Files == nil {
		l.Files = map[string]string{}
	}
	return &l, true, nil
}

// Write saves l to projectRoot/.harness.lock as indented JSON with a trailing
// newline (stable, diff-friendly, committable like a package lock). It refuses to
// write through a symlink, so a planted .harness.lock symlink can't redirect the
// write outside the project root (consistent with sync's other writes).
func Write(projectRoot string, l *Lock) error {
	target := filepath.Join(projectRoot, Name)
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through symlink: %s", target)
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(target, data, 0o644)
}
