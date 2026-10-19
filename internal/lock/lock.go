// Package lock reads and writes .lacquer.lock, the per-project baseline that
// records the content the lacquer last wrote. It is the third point in the
// three-way comparison the audit uses to tell "the project edited this" apart
// from "the lacquer moved on" — without it, sync can only see that on-disk
// content differs from what it would write now, not who changed it.
package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/patrickserrano/lacquer/internal/version"
)

// Name is the lockfile's filename at the project root.
const Name = ".lacquer.lock"

// Lock is the recorded baseline: the lacquer version that wrote it, and a map
// from managed-unit key to the sha256 of the content the lacquer produced.
// Region keys are "<dest>#<regionKey>" (e.g. "CLAUDE.md#core"); asset keys are
// the destination path (e.g. ".github/workflows/ios-ci.yml").
// Version is written as a semver string ("0.73.0"). A lockfile written before
// semver holds a bare number; version.Version's UnmarshalJSON accepts that and
// reads it as 0.N.0, so an existing project's lock keeps loading rather than
// erroring out and taking audit's three-way comparison offline.
type Lock struct {
	Version version.Version   `json:"version"`
	Files   map[string]string `json:"files"`
	// Downgrade is why this lock names an older lacquer than the one it
	// replaced. Written by hand, after syncing with the older release, and only
	// then: sync never writes it, so the next sync drops it and it lasts exactly
	// as long as the downgrade does. It is a field of the lock rather than of
	// .lacquer.toml because the release being downgraded TO may predate it, and
	// an older lacquer rejects a manifest key it does not know, while
	// encoding/json ignores an unknown lock field. See internal/gateoff.
	Downgrade *Downgrade `json:"downgrade,omitempty"`
}

// Downgrade is the reason recorded for a lock downgrade.
//
//	"downgrade": {"from": "1.78.1", "reason": "1.78.1 breaks the release job, #123"}
//
// From is the version the PR's base named, the one being left. A reason only
// counts for the downgrade it was written for: a later downgrade from another
// version is unexplained until it gets its own.
type Downgrade struct {
	From   string `json:"from"`
	Reason string `json:"reason"`
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
	l, err := Parse(data)
	if err != nil {
		return nil, false, err
	}
	return l, true, nil
}

// Parse decodes a lockfile's content, for a lock read from somewhere other than
// a project directory (a commit, through git or the GitHub API).
func Parse(data []byte) (*Lock, error) {
	var l Lock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, err
	}
	if l.Files == nil {
		l.Files = map[string]string{}
	}
	return &l, nil
}

// Write saves l to projectRoot/.lacquer.lock as indented JSON with a trailing
// newline (stable, diff-friendly, committable like a package lock). It refuses to
// write through a symlink, so a planted .lacquer.lock symlink can't redirect the
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

// IsDowngrade reports whether head names an older lacquer than base. Either is
// nil when that side has no lock, which is never a downgrade.
func IsDowngrade(base, head *Lock) bool {
	return base != nil && head != nil && head.Version.Less(base.Version)
}

// DowngradeReason is the reason head records for leaving base's version, or ""
// when it records none for that version (see Downgrade).
func DowngradeReason(base, head *Lock) string {
	if base == nil || head == nil || head.Downgrade == nil || head.Downgrade.From != base.Version.String() {
		return ""
	}
	return strings.TrimSpace(head.Downgrade.Reason)
}
