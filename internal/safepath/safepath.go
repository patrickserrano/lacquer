// Package safepath confines a relative path to a root directory, refusing any
// path that escapes the root through a symlinked component. It complements the
// lexical validation in package config (which catches ".." and absolute paths)
// by catching symlinks planted in the filesystem — e.g. a symlink committed into
// a repo the user clones and then runs `lacquer sync` against.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve joins rel under root and verifies that the deepest already-existing
// ancestor of the result resolves (following symlinks) to a location still
// inside root. It returns the path to use (rooted at the resolved root) or an
// error if rel escapes the root via a symlink. rel must already be lexically
// validated as relative and non-".."-escaping.
//
// Resolve only confines directory components; callers that write must still
// guard the final path element against being a symlink itself.
func Resolve(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root %s: %w", root, err)
	}
	full := filepath.Join(realRoot, rel)

	// Walk up from the parent until we reach a path that exists on disk; that is
	// the deepest component the filesystem can already be tricked through.
	existing := filepath.Dir(full)
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}

	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", existing, err)
	}
	rrel, err := filepath.Rel(realRoot, realExisting)
	if err != nil || rrel == ".." || strings.HasPrefix(rrel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes project root via symlink", rel)
	}
	return full, nil
}
