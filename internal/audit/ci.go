package audit

import (
	"fmt"
	"strings"

	"github.com/patrickserrano/lacquer/internal/lock"
	"github.com/patrickserrano/lacquer/internal/version"
)

// # Behind, when the lacquer is pinned to the lock
//
// Behind means the project's content equals the lock's recorded hash and
// differs from what the lacquer renders now. Run against a newer lacquer, that
// is the ordinary "lacquer advanced, sync updates it" state, and it must never
// block.
//
// CI does not run against a newer lacquer. It checks the lacquer out at the
// exact tag .lacquer.lock names and runs that tag's release binary. A lock
// written by that release, for this project as it stands, records the hash of
// that release's render, so every untouched unit is OK. A Behind row there can
// only mean the recorded hash came from some OTHER render: a dev binary, a
// dirty content root, a hand-edited file with its lock hash refreshed to match,
// or a manifest or project input (package manager, Swift manifests) that changed
// after the last sync. In every one of those the lock vouches for content the
// version it names never produced, and reporting it as routine is a pass for a
// state nobody verified.
//
// So `audit --ci` asserts that premise (CheckLockVersion) and then treats every
// Behind row as a failure of its own (Gate.LockMismatch, exit 8).

// CheckLockVersion verifies the premise `--ci` rests on: the project has a lock,
// and the lacquer being audited against is the version that lock names. Without
// it, Behind can legitimately mean the lacquer advanced, and failing on it would
// be wrong in the other direction.
func CheckLockVersion(projectRoot string, ver version.Version) error {
	lk, locked, err := lock.Read(projectRoot)
	if err != nil {
		return fmt.Errorf("read lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("--ci needs a %s: without one no unit can be attributed, so nothing was verified", lock.Name)
	}
	if lk.Version != ver {
		return fmt.Errorf("--ci audits at the lock's own version, but %s names v%s and the lacquer here is v%s — check out the lacquer at the lock's tag", lock.Name, lk.Version, ver)
	}
	return nil
}

// NotRendered returns the rows whose content matches the lock's recorded hash
// but not what the lacquer renders now: Behind. Only meaningful under `--ci`,
// where the lacquer is the version the lock names (see CheckLockVersion).
func NotRendered(rows []Row) []Row {
	var out []Row
	for _, r := range rows {
		if r.Status == Behind {
			out = append(out, r)
		}
	}
	return out
}

// FormatNotRendered explains a `--ci` failure on Behind rows. Empty when there
// are none, so a clean run prints nothing extra.
func FormatNotRendered(rows []Row, ver version.Version) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nlock mismatch (%d): these files match the lock's recorded hashes, but lacquer v%s does not render them,\n", len(rows), ver)
	b.WriteString("so the lock was written by a different build than it names (a dev binary, a dirty lacquer checkout,\n" +
		"or an edit with its lock hash refreshed), or a project input changed after the last sync.\n" +
		"Re-sync with the released lacquer.\n")
	for _, r := range rows {
		label := r.Dest
		if r.Kind == "region" {
			label = fmt.Sprintf("%s#%s", r.Dest, r.Detail)
		}
		fmt.Fprintf(&b, "  %s\n", label)
	}
	return b.String()
}
